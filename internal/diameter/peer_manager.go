// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/alarm"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
)

// PeerState captures the operational state used by the watchdog and admin
// surfaces.
type PeerState int32

const (
	PeerUp       PeerState = 2
	PeerDegraded PeerState = 1
	PeerDown     PeerState = 0
)

func (s PeerState) String() string {
	switch s {
	case PeerUp:
		return "up"
	case PeerDegraded:
		return "degraded"
	case PeerDown:
		return "down"
	case PeerDraining:
		return "draining"
	}
	return "unknown"
}

// Peer holds the runtime state of a registered Diameter peer.
//
// Atomic-only fields (LastActivity, LastApplicationActivity, State,
// ConsecutiveDWRFailures) are read by the watchdog goroutine without
// taking the manager mutex.
type Peer struct {
	Conn       diam.Conn
	OriginHost string

	// LastActivity tracks ANY inbound message (DWA, DPA, forward request).
	// Used by the watchdog idle-window check so DWR is not emitted when a
	// connection is otherwise busy.
	LastActivity atomic.Int64

	// LastApplicationActivity tracks ONLY forward application requests
	// (ULR/AIR/CCR/...). Distinct from LastActivity because go-diameter's
	// SM auto-handles DWR/DWA in a goroutine independent of the application
	// handlers — a peer whose application layer is hung can keep DWA
	// flowing and still be effectively dead. Idle GC keys off this
	// timestamp to detect "half-open" zombie peers.
	LastApplicationActivity atomic.Int64

	State                  atomic.Int32 // PeerState
	ConsecutiveDWRFailures atomic.Uint32

	// dwaSignal is buffered so non-blocking RecordDWA never drops a wake-up
	// even if the watchdog hasn't reached its select yet.
	dwaSignal chan struct{}

	// dpaSignal is buffered for the same reason as dwaSignal: DrainAll's
	// per-peer goroutine may not be parked yet when DPA arrives. Used by
	// RecordDPA to signal that this peer has acknowledged our DPR.
	dpaSignal chan struct{}

	// cancel stops the per-peer watchdog goroutine when the peer is
	// unregistered or the manager shuts down.
	cancel context.CancelFunc
}

// PeerManager is a thread-safe registry of active Diameter peers and the
// owner of their watchdog goroutines.
type PeerManager struct {
	peers map[string]*Peer
	mu    sync.RWMutex

	// Watchdog config — captured at construction to keep timer goroutines
	// independent of full Config object. Reload of these fields requires
	// re-creating the manager (documented as restart-required).
	watchdogInterval time.Duration
	watchdogTimeout  time.Duration
	watchdogEnabled  bool

	// idlePeerTimeout — duration of "no application traffic" after which a
	// peer is force-closed by the watchdog. Zero disables idle
	// GC (default for tests; set explicitly via PeerManagerOptions).
	idlePeerTimeout time.Duration

	// Identity fields used by the translator when building outbound DWR/DPR.
	// Empty values disable active DWR emission (passive watchdog only).
	originHost  string
	originRealm string
	translator  *Translator

	// dropListeners are notified whenever a peer connection is closed (via
	// Unregister, gcIdlePeer, or forceCloseRemaining). Used by ReverseService
	// to fast-fail pendingReqs without waiting for reverse_timeout.
	// Slice + RWMutex (not channel) so listener execution is synchronous —
	// a slow listener does not block other listeners or peer lifecycle.
	dropListeners []PeerDropListener
	listenersMu   sync.RWMutex
}

// PeerDropReason classifies why a peer's connection was torn down. Used as
// the `reason` metric label and for deciding whether NATS callers should
// see 4012 (transport failure) vs 3002 (gateway-internal).
type PeerDropReason string

const (
	// PeerDropUnregistered — explicit Unregister call (peer-initiated DPR
	// or admin endpoint). Counterpart NATS error: 4012 (AGENT_FAILOVER).
	PeerDropUnregistered PeerDropReason = "peer_unregistered"

	// PeerDropIdleGC — watchdog idle-GC force-closed a half-open zombie
	// peer (>5 min no application traffic). Counterpart: 4012.
	PeerDropIdleGC PeerDropReason = "idle_gc"

	// PeerDropDrainAll — gateway-initiated DrainAll (SIGTERM or admin).
	// Counterpart: 4012; the originating reason is in the audit log.
	PeerDropDrainAll PeerDropReason = "drain_all"
)

// PeerDropListener is called synchronously when a peer connection is closed.
// Listeners MUST be non-blocking and MUST NOT call back into PeerManager
// methods that take its mutex (deadlock risk). The typical implementation
// signals waiters via channels and returns immediately.
type PeerDropListener func(originHost string, reason PeerDropReason)

// AddDropListener registers a callback. There is no Remove counterpart;
// listeners are expected to live for the gateway's lifetime.
func (pm *PeerManager) AddDropListener(l PeerDropListener) {
	pm.listenersMu.Lock()
	defer pm.listenersMu.Unlock()
	pm.dropListeners = append(pm.dropListeners, l)
}

// notifyDrop fans out a drop event to every registered listener.
// Synchronous: callers (Unregister, gcIdlePeer, forceCloseRemaining) await
// listener completion before returning. ReverseService's listener signals
// channels via non-blocking sends, so total latency stays bounded.
func (pm *PeerManager) notifyDrop(originHost string, reason PeerDropReason) {
	pm.listenersMu.RLock()
	listeners := pm.dropListeners
	pm.listenersMu.RUnlock()
	for _, l := range listeners {
		l(originHost, reason)
	}
	monitor.IncrementCounter("diameter_pending_drain_total", map[string]string{
		"peer":   originHost,
		"reason": string(reason),
	})
}

// PeerManagerOptions tunes watchdog behaviour. Zero values disable the
// watchdog (preserves the simple v2 behaviour for tests).
type PeerManagerOptions struct {
	WatchdogInterval time.Duration
	WatchdogTimeout  time.Duration
	// IdlePeerTimeout — application-traffic-only idle threshold. Default
	// 0 disables idle GC; production should set ~5 minutes.
	IdlePeerTimeout time.Duration
	// OriginHost / OriginRealm enable active DWR emission. Both must be set;
	// otherwise the manager falls back to the passive (idle-window) watchdog.
	OriginHost  string
	OriginRealm string
}

func NewPeerManager() *PeerManager {
	return NewPeerManagerWith(PeerManagerOptions{})
}

func NewPeerManagerWith(opts PeerManagerOptions) *PeerManager {
	pm := &PeerManager{
		peers:            make(map[string]*Peer),
		watchdogInterval: opts.WatchdogInterval,
		watchdogTimeout:  opts.WatchdogTimeout,
		watchdogEnabled:  opts.WatchdogInterval > 0 && opts.WatchdogTimeout > 0,
		idlePeerTimeout:  opts.IdlePeerTimeout,
		originHost:       opts.OriginHost,
		originRealm:      opts.OriginRealm,
		translator:       NewTranslator(),
	}
	return pm
}

// activeDWREnabled reports whether the watchdog should actively emit DWR
// over the peer connection. Falls back to passive (idle-window) mode when
// origin identity is missing — keeps unit tests minimal.
func (pm *PeerManager) activeDWREnabled() bool {
	return pm.originHost != "" && pm.originRealm != ""
}

// Register stores or refreshes a peer entry. Idempotent: repeated CER from the
// same Origin-Host swaps the connection without disrupting state metrics.
func (pm *PeerManager) Register(c diam.Conn, originHost string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now().UnixNano()

	if existing := pm.peers[originHost]; existing != nil {
		existing.Conn = c
		existing.LastActivity.Store(now)
		existing.LastApplicationActivity.Store(now)
		existing.State.Store(int32(PeerUp))
		existing.ConsecutiveDWRFailures.Store(0)
		pm.publishState(originHost, PeerUp)
		logger.Info("Peer re-registered", logger.String("host", originHost))
		return
	}

	p := &Peer{
		Conn:       c,
		OriginHost: originHost,
		dwaSignal:  make(chan struct{}, 1),
		dpaSignal:  make(chan struct{}, 1),
	}
	p.LastActivity.Store(now)
	p.LastApplicationActivity.Store(now)
	p.State.Store(int32(PeerUp))
	pm.peers[originHost] = p
	pm.publishState(originHost, PeerUp)
	logger.Info("Peer registered", logger.String("host", originHost))

	if pm.watchdogEnabled {
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		go pm.runWatchdog(ctx, p)
	}
}

func (pm *PeerManager) Unregister(originHost string) {
	pm.unregisterWithReason(originHost, PeerDropUnregistered)
}

// unregisterWithReason is the shared implementation for all unregister paths
// (explicit Unregister, gcIdlePeer, forceCloseRemaining). The reason label
// flows into the drop listener + diameter_pending_drain_total{reason} metric
// so operators can distinguish admin disconnects from idle-GC zombies.
func (pm *PeerManager) unregisterWithReason(originHost string, reason PeerDropReason) {
	pm.mu.Lock()
	p := pm.peers[originHost]
	delete(pm.peers, originHost)
	pm.mu.Unlock()

	if p != nil && p.cancel != nil {
		p.cancel()
	}
	pm.publishState(originHost, PeerDown)
	pm.notifyDrop(originHost, reason)
	logger.Info("Peer unregistered",
		logger.String("host", originHost),
		logger.String("reason", string(reason)))
}

func (pm *PeerManager) GetConnection(originHost string) diam.Conn {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if p := pm.peers[originHost]; p != nil {
		return p.Conn
	}
	return nil
}

// GetPeer returns the full Peer record for diagnostic / admin endpoints.
// Returns nil when no entry exists.
func (pm *PeerManager) GetPeer(originHost string) *Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.peers[originHost]
}

func (pm *PeerManager) GetActiveConnections() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	list := make([]string, 0, len(pm.peers))
	for k := range pm.peers {
		list = append(list, k)
	}
	return list
}

// GetAnyConnection returns any available peer connection — used as a last-
// resort fallback when destination host is unspecified. Anti-pattern; emits a
// warning so call-sites become visible for cleanup.
func (pm *PeerManager) GetAnyConnection() diam.Conn {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for _, p := range pm.peers {
		return p.Conn
	}
	return nil
}

// Identity is the gateway's own Origin-Host and Origin-Realm, as given in
// PeerManagerOptions. Empty for a manager built without them.
func (pm *PeerManager) Identity() (originHost, originRealm string) {
	if pm == nil {
		return "", ""
	}
	return pm.originHost, pm.originRealm
}

// HostOf returns the identity the connection presented in its CER, or ""
// when it is not a registered peer. Only CER registration, after
// AuthorizePeer, binds an identity to a connection. Application messages
// never do: their Origin-Host is whatever the sender wrote, and re-binding
// on it let one authorised peer take over another peer's reverse traffic.
func (pm *PeerManager) HostOf(c diam.Conn) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for host, p := range pm.peers {
		if p.Conn == c {
			return host
		}
	}
	return ""
}

// Touch records application traffic from c's peer.
func (pm *PeerManager) Touch(c diam.Conn) {
	if host := pm.HostOf(c); host != "" {
		pm.RecordActivity(host)
	}
}

// RecordActivity stamps the peer as alive (called from forward application
// request handlers — ULR/AIR/CCR/...). Bumps both the watchdog timestamp
// and the application-only timestamp the idle GC keys off.
//
// Cheap atomic — safe on the hot path.
func (pm *PeerManager) RecordActivity(originHost string) {
	if p := pm.GetPeer(originHost); p != nil {
		now := time.Now().UnixNano()
		p.LastActivity.Store(now)
		p.LastApplicationActivity.Store(now)
	}
}

// RecordDPA is invoked by the Diameter state machine when a Disconnect-
// Peer-Answer is received in response to a gateway-initiated DPR. Signals
// DrainAll's per-peer wait so a peer that responds promptly can be closed
// cleanly without paying the timeout.
//
// Counter `diameter_dpa_received_total` is bumped here so the metric is
// authoritative regardless of which path called us (server.go:handleDPA).
func (pm *PeerManager) RecordDPA(originHost string) {
	p := pm.GetPeer(originHost)
	if p == nil {
		return
	}
	monitor.IncrementCounter("diameter_dpa_received_total", map[string]string{
		"peer": originHost,
	})
	select {
	case p.dpaSignal <- struct{}{}:
	default:
	}
}

// RecordDWA is invoked by the Diameter state machine when a Device-Watchdog-
// Answer is received. Resets watchdog state and signals the per-peer goroutine
// non-blockingly.
func (pm *PeerManager) RecordDWA(originHost string) {
	p := pm.GetPeer(originHost)
	if p == nil {
		return
	}
	p.LastActivity.Store(time.Now().UnixNano())
	p.ConsecutiveDWRFailures.Store(0)
	if PeerState(p.State.Load()) != PeerUp {
		p.State.Store(int32(PeerUp))
		pm.publishState(originHost, PeerUp)
		alarm.Raise(alarm.SeverityInfo, "DIAM_GW_PEER_RECOVERED", "peer back to up: "+originHost)
	}
	select {
	case p.dwaSignal <- struct{}{}:
	default:
	}
}

// runWatchdog is the per-peer probe loop.
// After WatchdogInterval idle time, actively emits a DWR (RFC 6733 §5.5.1)
// and waits for DWA. Counts both DWR write errors and DWA timeouts as
// consecutive failures: the first failure marks the peer Degraded, three
// consecutive failures take it Down. RecordDWA resets the counter.
//
// Also runs the idle peer GC: if no APPLICATION traffic
// (forward request — not DWA) has arrived in idlePeerTimeout, the peer is
// force-closed regardless of watchdog state. Catches half-open zombies
// where DWA still flows (go-diameter SM auto-handler) but the peer's
// application layer is hung.
//
// When activeDWREnabled() is false (origin identity not configured — test
// mode), the loop falls back to the v3 passive idle-window watchdog so unit
// tests don't need a real Diameter conn.
func (pm *PeerManager) runWatchdog(ctx context.Context, p *Peer) {
	ticker := time.NewTicker(pm.watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Idle GC check runs BEFORE the DWR path: a zombie peer must not
		// be probed (its DWA reply would just reset our liveness signal).
		// Returns from the goroutine — gcIdlePeer Unregister'd us, so
		// continuing the loop would touch a deleted peer.
		if pm.idlePeerTimeout > 0 {
			appIdle := time.Now().UnixNano() - p.LastApplicationActivity.Load()
			if appIdle > int64(pm.idlePeerTimeout) {
				pm.gcIdlePeer(p, time.Duration(appIdle))
				return
			}
		}

		idle := time.Now().UnixNano() - p.LastActivity.Load()
		if idle < int64(pm.watchdogInterval) {
			continue
		}

		probeStart := time.Now()
		writeErr := pm.sendDWR(p)
		if writeErr != nil {
			pm.recordWatchdogFailure(p, probeStart, "dwr_write_error", writeErr)
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-p.dwaSignal:
			// DWA observed — RecordDWA already cleared failures and emitted
			// the recovery alarm if the peer was previously degraded/down.
		case <-time.After(pm.watchdogTimeout):
			pm.recordWatchdogFailure(p, probeStart, "dwa_timeout", nil)
		}
	}
}

// sendDWR emits a Device-Watchdog-Request to the peer when active mode is
// enabled. Returns nil in passive mode so the loop's wait-for-signal branch
// preserves v3 idle-window semantics for tests.
func (pm *PeerManager) sendDWR(p *Peer) error {
	if !pm.activeDWREnabled() {
		return nil
	}
	dwr := pm.translator.CreateDWR(pm.originHost, pm.originRealm)
	_, err := dwr.WriteTo(p.Conn)
	if err == nil {
		monitor.IncrementCounter("diameter_dwr_sent_total", map[string]string{"peer": p.OriginHost})
	}
	return err
}

// recordWatchdogFailure increments the consecutive-failure counter and
// drives the state machine. Both DWR write errors and DWA timeouts route
// through here so telemetry stays uniform; the `reason` label distinguishes
// them in `diameter_dwr_failures_total`.
func (pm *PeerManager) recordWatchdogFailure(p *Peer, probeStart time.Time, reason string, writeErr error) {
	n := p.ConsecutiveDWRFailures.Add(1)
	monitor.IncrementCounter("diameter_dwr_failures_total", map[string]string{
		"peer":   p.OriginHost,
		"reason": reason,
	})
	args := []any{
		logger.String("peer", p.OriginHost),
		logger.String("probe_started", probeStart.UTC().Format(time.RFC3339Nano)),
		logger.String("reason", reason),
		logger.Int("consecutive_failures", int(n)),
	}
	if writeErr != nil {
		args = append(args, logger.String("err", writeErr.Error()))
	}
	logger.Warn("Watchdog probe failed", args...)

	switch {
	case n == 1:
		p.State.Store(int32(PeerDegraded))
		pm.publishState(p.OriginHost, PeerDegraded)
		alarm.Raise(alarm.SeverityWarning, "DIAM_GW_PEER_DEGRADED", "peer degraded: "+p.OriginHost)
	case n >= 3:
		p.State.Store(int32(PeerDown))
		pm.publishState(p.OriginHost, PeerDown)
		alarm.Raise(alarm.SeverityCritical, "DIAM_GW_PEER_DOWN", "peer down: "+p.OriginHost)
	}
}

// gcIdlePeer force-closes a peer that has gone too long without sending
// any application traffic. Called from the watchdog goroutine
// when LastApplicationActivity exceeds idlePeerTimeout.
//
// Action set (decision B from the design discussion): mark Down → close
// connection → unregister. We do NOT send DPR — the peer is presumed
// half-open / zombie, so a DPR write is unlikely to succeed and a DPA
// would never arrive. Force close + alarm tells operators the peer
// vanished without a clean disconnect.
func (pm *PeerManager) gcIdlePeer(p *Peer, idleDuration time.Duration) {
	monitor.IncrementCounter("diameter_peer_idle_gc_total", map[string]string{
		"peer": p.OriginHost,
	})
	alarm.Raise(alarm.SeverityWarning, "DIAM_GW_PEER_IDLE_GC",
		"peer idle-GC: "+p.OriginHost+" idle for "+idleDuration.Round(time.Second).String())
	logger.Warn("Peer idle-GC: no application traffic in window — force closing",
		logger.String("peer", p.OriginHost),
		logger.String("idle", idleDuration.Round(time.Second).String()))

	p.State.Store(int32(PeerDown))
	if p.Conn != nil {
		p.Conn.Close()
	}
	// unregisterWithReason handles cancel() + map removal + Down publish.
	// Calling it from inside our own watchdog goroutine is safe: cancel()
	// targets our context but we return immediately after this function.
	// PeerDropIdleGC reason distinguishes zombie-detect from explicit
	// admin disconnect for the drop listener + metric labeling.
	pm.unregisterWithReason(p.OriginHost, PeerDropIdleGC)
}

// DrainCause encodes RFC 6733 §5.4.3 Disconnect-Cause AVP values. Choosing
// the right value matters operationally: peers interpret REBOOTING as
// "expect to reconnect after restart" (K8s rolling deploy), BUSY as
// "back off — overloaded peer doesn't want traffic right now", and
// DO_NOT_WANT_TO_TALK_TO_YOU as "permanent — admin-driven disconnect,
// peer SHOULD NOT auto-reconnect".
type DrainCause uint32

const (
	// DrainCauseRebooting — peer expected to reconnect after restart.
	// Default for SIGTERM/SIGINT (K8s rolling restart). RFC 6733 §5.4.3.
	DrainCauseRebooting DrainCause = 0

	// DrainCauseBusy — peer is overloaded; reconnect after a backoff.
	// Used for admin-triggered drain when the gateway is shedding load
	// (e.g. canary rollback to take pressure off a struggling instance).
	DrainCauseBusy DrainCause = 1

	// DrainCausePermanent (DO_NOT_WANT_TO_TALK_TO_YOU) — admin-driven
	// permanent disconnect. Peers SHOULD NOT auto-reconnect immediately.
	// Used for decommissioning a gateway instance.
	DrainCausePermanent DrainCause = 2
)

func (c DrainCause) String() string {
	switch c {
	case DrainCauseRebooting:
		return "REBOOTING"
	case DrainCauseBusy:
		return "BUSY"
	case DrainCausePermanent:
		return "DO_NOT_WANT_TO_TALK_TO_YOU"
	}
	return "UNKNOWN"
}

// DrainAll is the REBOOTING-default convenience wrapper for the most common
// drain path (SIGTERM/SIGINT during K8s rolling restart). Equivalent to
// DrainAllWithCause(ctx, timeout, DrainCauseRebooting).
func (pm *PeerManager) DrainAll(ctx context.Context, timeout time.Duration) int {
	return pm.DrainAllWithCause(ctx, timeout, DrainCauseRebooting)
}

// DrainAllWithCause initiates a graceful shutdown of every registered peer
// with the supplied Disconnect-Cause:
//
//  1. Each peer's state flips to PeerDraining (new requests should be rejected
//     by upstream callers; this is advisory — the manager doesn't enforce it).
//  2. A DPR with the supplied cause is sent over each peer connection.
//  3. Per-peer goroutines wait for DPA via Peer.dpaSignal until either
//     the answer arrives or the overall `timeout` elapses. A peer that
//     ack'd promptly is closed cleanly; one that didn't is force-closed.
//  4. After the timeout window (or all DPAs in), every remaining connection
//     is closed and the registry cleared.
//
// Sends and DPA waits are issued in parallel (one goroutine per peer) so a
// single hung peer doesn't block the rest. Returns the number of peers
// closed at the end of drain — non-zero is normal; the metric to watch for
// "ungraceful" is `diameter_dpa_timeout_total` minted per missed DPA.
//
// The `diameter_dpr_sent_total{peer,cause}` counter records the cause label
// so dashboards can distinguish K8s restart drains from admin-triggered ones.
func (pm *PeerManager) DrainAllWithCause(ctx context.Context, timeout time.Duration, cause DrainCause) int {
	pm.mu.RLock()
	snapshot := make([]*Peer, 0, len(pm.peers))
	for _, p := range pm.peers {
		snapshot = append(snapshot, p)
	}
	pm.mu.RUnlock()

	if len(snapshot) == 0 {
		return 0
	}

	// One context bounds the entire drain — both send and DPA-wait phases
	// share the budget. ctx.Done() (parent) and timeout are honoured uniformly.
	drainCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logger.Info("Draining peers (DPR)",
		logger.Int("count", len(snapshot)),
		logger.String("cause", cause.String()))

	// Step 1 — mark draining + emit DPR in parallel.
	var sendWG sync.WaitGroup
	for _, p := range snapshot {
		p.State.Store(int32(PeerDraining))
		pm.publishState(p.OriginHost, PeerDraining)
		monitor.IncrementCounter("diameter_dpr_sent_total", map[string]string{
			"peer":  p.OriginHost,
			"cause": cause.String(),
		})

		if !pm.activeDWREnabled() {
			continue // tests / passive mode — skip Diameter emit
		}

		sendWG.Add(1)
		go func(peer *Peer) {
			defer sendWG.Done()
			dpr := pm.translator.CreateDPR(pm.originHost, pm.originRealm, uint32(cause))
			if _, err := dpr.WriteTo(peer.Conn); err != nil {
				logger.Warn("DPR write failed",
					logger.String("peer", peer.OriginHost),
					logger.String("err", err.Error()))
			}
		}(p)
	}

	sendDone := make(chan struct{})
	go func() {
		sendWG.Wait()
		close(sendDone)
	}()

	select {
	case <-sendDone:
	case <-drainCtx.Done():
		logger.Warn("DrainAll: send-DPR phase exceeded budget")
		return pm.forceCloseRemaining(snapshot)
	}

	// Step 2 — wait for DPA from each peer, in parallel. Each goroutine
	// exits on either dpaSignal (peer acknowledged) or drainCtx.Done()
	// (overall timeout). Peers that exit via dpaSignal contribute to the
	// `received` counter; the timeout path mints `dpa_timeout_total` so
	// alerting can fire on ungraceful disconnects without false positives.
	var ackWG sync.WaitGroup
	for _, p := range snapshot {
		if p.dpaSignal == nil {
			continue
		}
		ackWG.Add(1)
		go func(peer *Peer) {
			defer ackWG.Done()
			select {
			case <-peer.dpaSignal:
				// RecordDPA already bumped diameter_dpa_received_total.
			case <-drainCtx.Done():
				monitor.IncrementCounter("diameter_dpa_timeout_total", map[string]string{
					"peer": peer.OriginHost,
				})
			}
		}(p)
	}

	allAcked := make(chan struct{})
	go func() {
		ackWG.Wait()
		close(allAcked)
	}()

	select {
	case <-allAcked:
		logger.Info("DrainAll: all peers acknowledged DPR with DPA")
	case <-drainCtx.Done():
		logger.Warn("DrainAll: timeout — some peers did not return DPA")
	}

	return pm.forceCloseRemaining(snapshot)
}

// forceCloseRemaining tears down every still-registered peer connection and
// flips their state to PeerDown. Called from DrainAll's two exit paths
// (clean DPA-ack and timeout). Returns the number of peers closed —
// distinct from `dpa_timeout_total` because a clean DPA peer is also
// counted here (the close is part of the protocol, not a force-close).
func (pm *PeerManager) forceCloseRemaining(snapshot []*Peer) int {
	pm.mu.Lock()
	closed := 0
	for host, p := range pm.peers {
		if p.cancel != nil {
			p.cancel()
		}
		if p.Conn != nil {
			p.Conn.Close()
		}
		delete(pm.peers, host)
		closed++
	}
	pm.mu.Unlock()

	for _, p := range snapshot {
		pm.publishState(p.OriginHost, PeerDown)
		// Fire drop listener for every drained peer — even peers that
		// completed DPR/DPA cleanly need pendingReqs draining (waiters
		// blocked on those connections must unblock immediately rather
		// than hit reverse_timeout). PeerDropDrainAll distinguishes
		// drain-driven drops from idle/admin paths in metrics.
		pm.notifyDrop(p.OriginHost, PeerDropDrainAll)
	}

	logger.Info("DrainAll complete", logger.Int("closed", closed))
	return closed
}

// PeerDraining is the transitional state between Up/Degraded and Down used
// during graceful shutdown (DPR sent, awaiting DPA). Defined here rather
// than at the top of the file to keep the original three-state public API
// stable for v3 consumers.
const PeerDraining PeerState = 3

// publishState emits a state-transition counter. Until common-go/monitor
// exposes a gauge primitive, `rate()` queries on this
// counter approximate the current state distribution, and the absolute
// state is observable via the admin /peers endpoint.
func (pm *PeerManager) publishState(host string, state PeerState) {
	monitor.IncrementCounter("diameter_peer_state_transition_total", map[string]string{
		"peer": host,
		"to":   state.String(),
	})
}
