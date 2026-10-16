// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/audit"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"golang.org/x/time/rate"
)

// rateLimitTripThreshold — the rate-limit audit fires after this many
// consecutive over-rate trips on the same peer. A successful admit
// resets the counter. Hardcoded (not configurable) so operators don't
// silence the alert by raising it; tune the per-peer TPS instead.
const rateLimitTripThreshold = 100

// peerLimiter is the per-Origin-Host token-bucket plus the trip counter
// used for the "consecutive 100" audit threshold. Atomic field
// access stays lock-free on the hot path so Admit doesn't serialize
// across peers.
type peerLimiter struct {
	limiter          *rate.Limiter
	consecutiveTrips atomic.Uint32
	originHost       string // captured for audit / metric labels
	configuredTPS    int    // surfaced in audit payload for operator triage
}

// PeerRateLimiter is the registry of per-peer token buckets sized from
// cfg.Peers[].RateLimitTPS. Smooth refill via golang.org/x/time/rate
// — period-based refill jitters at second
// boundaries which is unacceptable for a Diameter Gy/Ro charging path
// where session-establishment bursts are normal.
//
// Burst capacity = burstFactor × TPS. Default burstFactor is 2 to
// tolerate the typical LTE attach burst (CER + ULR + AIR + CCR-I in
// tight sequence) without spurious 3004 (TOO_BUSY) responses.
//
// nil-safe: callers may invoke Admit on a nil pointer, in which case it
// returns true. This keeps the hot path branch-free when rate limiting
// is disabled (no peers configured with TPS > 0).
type PeerRateLimiter struct {
	mu    sync.RWMutex
	peers map[string]*peerLimiter
}

// NewPeerRateLimiter seeds the registry from a per-Origin-Host TPS map.
// Peers with TPS == 0 (default) get no limiter so Admit short-circuits
// to true with zero allocation. burstFactor < 1 is clamped to 2.
func NewPeerRateLimiter(tpsByHost map[string]int, burstFactor int) *PeerRateLimiter {
	if burstFactor < 1 {
		burstFactor = 2
	}
	prl := &PeerRateLimiter{
		peers: make(map[string]*peerLimiter, len(tpsByHost)),
	}
	for host, tps := range tpsByHost {
		if tps <= 0 {
			continue
		}
		prl.peers[host] = &peerLimiter{
			limiter:       rate.NewLimiter(rate.Limit(tps), tps*burstFactor),
			originHost:    host,
			configuredTPS: tps,
		}
	}
	return prl
}

// Admit reports whether a forward request from originHost may proceed.
// Returns true for unconfigured peers (treat as unbounded — the default
// for any peer not listed in cfg.Peers[].RateLimitTPS) and for the nil
// receiver. Returns false when the per-peer bucket is empty; callers
// must answer the request with 3004 (TOO_BUSY).
//
// Side effects on deny:
//   - diameter_rate_limit_denied_total{peer} counter increment
//   - consecutiveTrips++
//   - audit BUS_PEER_RATELIMIT every rateLimitTripThreshold boundary
//     (so persistent over-rate keeps firing alerts, not just once)
func (prl *PeerRateLimiter) Admit(originHost string) bool {
	if prl == nil {
		return true
	}
	prl.mu.RLock()
	pl := prl.peers[originHost]
	prl.mu.RUnlock()

	if pl == nil {
		return true
	}

	if pl.limiter.Allow() {
		// Reset is the "consecutive" semantic: a single admit
		// between two trips breaks the chain.
		pl.consecutiveTrips.Store(0)
		return true
	}

	n := pl.consecutiveTrips.Add(1)
	monitor.IncrementCounter("diameter_rate_limit_denied_total", map[string]string{
		"peer": originHost,
	})

	// Audit on every multiple of the threshold — fires at 100, 200, ...
	// while peer stays over-rate. One-shot would bury persistent abuse;
	// per-trip would spam.
	if n%rateLimitTripThreshold == 0 {
		audit.Log(context.Background(), "diameter-server", "BUS_PEER_RATELIMIT",
			"diameter-peer", originHost,
			map[string]any{
				"consecutive_trips": int(n),
				"configured_tps":    pl.configuredTPS,
			})
		logger.Warn("Peer rate-limit consecutive trip threshold reached",
			logger.String("peer", originHost),
			logger.Int("trips", int(n)),
			logger.Int("configured_tps", pl.configuredTPS))
	}
	return false
}

// ConsecutiveTrips returns the current consecutive-trip counter for a
// peer, primarily for tests and admin diagnostics. Zero for unknown or
// reset peers.
func (prl *PeerRateLimiter) ConsecutiveTrips(originHost string) uint32 {
	if prl == nil {
		return 0
	}
	prl.mu.RLock()
	defer prl.mu.RUnlock()
	if pl := prl.peers[originHost]; pl != nil {
		return pl.consecutiveTrips.Load()
	}
	return 0
}
