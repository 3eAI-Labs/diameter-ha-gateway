// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

// Package gx — GxSessionRegistry tracks the Session-Id → PCEF peer Origin-Host
// binding so PCRF→PCEF reverse-direction commands (RAR/ASR) can be routed
// without requiring the NATS caller to remember which gateway peer holds a
// given session.
//
// Lifecycle (TS 29.212 §4.5.1):
//   - CCR-I (CC-Request-Type=0 INITIAL_REQUEST)        → Bind
//   - CCR-T (CC-Request-Type=3 TERMINATION_REQUEST)    → Unbind
//   - Peer connection close (PeerManager OnPeerDrop)   → DropPeer (bulk)
//
// State is per-gateway-instance and in-memory only (as is the reverse-path
// pendingReqs map; distributing one without the other would be incoherent). On gateway restart, every PCEF
// session re-binds at the next CCR-U; the gap window is the configured
// ReverseTimeout — RAR/ASR issued during this window will fail with 3002
// (peer not found) and the NATS caller is expected to retry.
package gx

import (
	"sync"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
)

// PolicyFromConfig converts the operator-facing config (preset + duration
// strings) into the runtime EvictionPolicy. Malformed durations are logged
// and treated as 0 (disabled) — a misconfigured TTL field MUST NOT silently
// fall back to a different value, that's a deployment bug to surface, not
// to paper over.
func PolicyFromConfig(c config.GxSessionEvictionConfig) EvictionPolicy {
	return EvictionPolicy{
		MinLifetime:      parseDurationField(c.MinLifetime, "gx.session_eviction.min_lifetime"),
		IdleTimeout:      parseDurationField(c.IdleTimeout, "gx.session_eviction.idle_timeout"),
		AbsoluteLifetime: parseDurationField(c.AbsoluteLifetime, "gx.session_eviction.absolute_lifetime"),
	}
}

func parseDurationField(s, name string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		logger.Warn("Invalid duration in config — disabling field",
			logger.String("field", name),
			logger.String("value", s),
			logger.String("error", err.Error()))
		return 0
	}
	return d
}

// SessionBinding is one row in the registry. PeerHost is the Origin-Host of
// the PCEF that initiated the session (captured from CCR-I OriginHost).
// LastSeen tracks the timestamp of the most recent CCR-I/CCR-U so the
// eviction policy can age-out stale bindings (defense-in-depth against
// missed CCR-T or peer-drop events).
type SessionBinding struct {
	PeerHost string
	BoundAt  time.Time
	LastSeen time.Time
}

// GxSessionRegistry is a thread-safe Session-Id → SessionBinding map with a
// configurable eviction policy. Callers MUST call DropPeer from a
// PeerManager drop listener to keep the registry consistent on connection
// close.
type GxSessionRegistry struct {
	mu       sync.RWMutex
	bindings map[string]*SessionBinding
	policy   EvictionPolicy
	now      func() time.Time
}

// EvictionPolicy is the 3-parameter hybrid formula governing TTL eviction.
// All three durations are independent toggles; operators set the ones that
// match their network profile (see GxSessionEvictionConfig in
// internal/config). Setting all three to zero disables TTL eviction
// entirely — the strict-telco posture where Unbind + DropPeer are the only
// trusted signals.
type EvictionPolicy struct {
	// MinLifetime — bindings younger than this are NEVER evicted by TTL,
	// regardless of activity. Defense for long-lived but quiet sessions
	// (IoT default bearers that may go days without CCR-U).
	MinLifetime time.Duration

	// IdleTimeout — bindings whose LastSeen is older than this get
	// evicted. LastSeen refreshes on every CCR-I/CCR-U, so this is an
	// idle-since-last-traffic semantic.
	IdleTimeout time.Duration

	// AbsoluteLifetime — bindings whose BoundAt is older than this get
	// evicted, regardless of activity. Backstop for billing windows or
	// memory ceilings.
	AbsoluteLifetime time.Duration
}

// RegistryOptions tunes the registry. Now is overridable for tests; defaults
// to time.Now.
type RegistryOptions struct {
	Policy EvictionPolicy
	Now    func() time.Time
}

// NewSessionRegistry constructs a registry with the given options.
func NewSessionRegistry(opts RegistryOptions) *GxSessionRegistry {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &GxSessionRegistry{
		bindings: make(map[string]*SessionBinding),
		policy:   opts.Policy,
		now:      now,
	}
}

// Bind records the session→peer association. Called from HandleCCR on
// CCR-I; idempotent re-bind on CCR-U refreshes LastSeen so the eviction
// policy can use activity recency.
//
// A session stays with the peer it was first bound to: a CCR from another
// peer carrying the same Session-Id is refused, or that peer would receive
// the session's RAR/ASR. The binding goes away with Unbind by its peer,
// DropPeer when the peer's connection closes, or eviction.
func (r *GxSessionRegistry) Bind(sessionID, peerHost string) {
	if sessionID == "" || peerHost == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if existing, ok := r.bindings[sessionID]; ok {
		if existing.PeerHost != peerHost {
			logger.Warn("Gx session claimed by a different peer — refused",
				logger.String("session_id", sessionID),
				logger.String("bound_peer", existing.PeerHost),
				logger.String("claiming_peer", peerHost))
			return
		}
		existing.LastSeen = now
		return
	}
	r.bindings[sessionID] = &SessionBinding{
		PeerHost: peerHost,
		BoundAt:  now,
		LastSeen: now,
	}
	monitor.IncrementCounter("diameter_gx_session_bind_total",
		map[string]string{"peer": peerHost})
}

// Lookup returns the peer host bound to sessionID. Returns ("", false)
// when the session is unknown (gateway restart, missed CCR-I, expired).
//
// Applies the eviction policy in-band: if shouldEvict reports the entry
// expired, Lookup deletes it and returns not-found, so a stale entry can
// never serve a stale routing decision.
func (r *GxSessionRegistry) Lookup(sessionID string) (string, bool) {
	r.mu.RLock()
	b, ok := r.bindings[sessionID]
	r.mu.RUnlock()
	if !ok {
		return "", false
	}
	if r.shouldEvict(b, r.now()) {
		r.mu.Lock()
		delete(r.bindings, sessionID)
		r.mu.Unlock()
		monitor.IncrementCounter("diameter_gx_session_evicted_total",
			map[string]string{"reason": "stale_on_lookup"})
		return "", false
	}
	return b.PeerHost, true
}

// Unbind removes the session, called on CCR-T. Only the peer the session is
// bound to can remove it.
func (r *GxSessionRegistry) Unbind(sessionID, peerHost string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.bindings[sessionID]; ok && b.PeerHost == peerHost {
		delete(r.bindings, sessionID)
		monitor.IncrementCounter("diameter_gx_session_unbind_total", nil)
	}
}

// DropPeer removes every binding for the given peer host. Wired from
// PeerManager.AddDropListener so connection close clears all sessions
// that PCEF was holding.
func (r *GxSessionRegistry) DropPeer(peerHost string, reason diameter.PeerDropReason) {
	r.mu.Lock()
	dropped := 0
	for sid, b := range r.bindings {
		if b.PeerHost == peerHost {
			delete(r.bindings, sid)
			dropped++
		}
	}
	r.mu.Unlock()
	if dropped > 0 {
		logger.Info("Gx session bindings cleared after peer drop",
			logger.String("peer", peerHost),
			logger.String("reason", string(reason)),
			logger.Int("dropped", dropped))
		monitor.IncrementCounter("diameter_gx_session_evicted_total",
			map[string]string{"reason": "peer_drop"})
	}
}

// Size returns the current number of active bindings (for /admin + metrics).
func (r *GxSessionRegistry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.bindings)
}

// shouldEvict applies the 3-parameter hybrid eviction formula. See the
// EvictionPolicy doc above for the rationale. Order matters:
// MinLifetime is checked first as a hard floor — once that gate passes, the
// idle and absolute checks fire independently (either is sufficient cause).
//
// All three policy fields = 0 ⇒ TTL eviction is disabled; only Unbind +
// DropPeer can clear bindings (strict telco posture).
func (r *GxSessionRegistry) shouldEvict(b *SessionBinding, now time.Time) bool {
	if r.policy.MinLifetime > 0 && now.Sub(b.BoundAt) < r.policy.MinLifetime {
		return false // floor not yet passed — protect long-lived but quiet sessions
	}
	if r.policy.IdleTimeout > 0 && now.Sub(b.LastSeen) >= r.policy.IdleTimeout {
		return true
	}
	if r.policy.AbsoluteLifetime > 0 && now.Sub(b.BoundAt) >= r.policy.AbsoluteLifetime {
		return true
	}
	return false
}
