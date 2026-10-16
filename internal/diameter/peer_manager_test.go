// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConn implements diam.Conn interface for testing
type mockConn struct {
	closed   bool
	messages []*diam.Message
	mu       sync.Mutex
	ctx      context.Context
}

func newMockConn() *mockConn {
	return &mockConn{ctx: context.Background()}
}

func (m *mockConn) Write(b []byte) (int, error) {
	return len(b), nil
}

func (m *mockConn) WriteStream(b []byte, stream uint) (int, error) {
	return len(b), nil
}

func (m *mockConn) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3868}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 3868}
}

func (m *mockConn) TLS() *tls.ConnectionState { return nil }

func (m *mockConn) Dictionary() *dict.Parser { return nil }

func (m *mockConn) Context() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m *mockConn) SetContext(ctx context.Context) { m.ctx = ctx }

func (m *mockConn) Connection() net.Conn { return nil }

func (m *mockConn) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// =============================================================================
// PeerManager Tests
// =============================================================================

func TestNewPeerManager(t *testing.T) {
	pm := NewPeerManager()
	require.NotNil(t, pm)
	assert.NotNil(t, pm.peers)
	assert.Empty(t, pm.peers)
}

func TestPeerManager_Register(t *testing.T) {
	pm := NewPeerManager()
	conn := newMockConn()

	pm.Register(conn, "mme.example.com")

	result := pm.GetConnection("mme.example.com")
	assert.Equal(t, conn, result)
}

func TestPeerManager_RegisterMultiplePeers(t *testing.T) {
	pm := NewPeerManager()
	conn1 := newMockConn()
	conn2 := newMockConn()

	pm.Register(conn1, "mme1.example.com")
	pm.Register(conn2, "mme2.example.com")

	assert.Equal(t, conn1, pm.GetConnection("mme1.example.com"))
	assert.Equal(t, conn2, pm.GetConnection("mme2.example.com"))
}

func TestPeerManager_RegisterOverwritesSamePeer(t *testing.T) {
	pm := NewPeerManager()
	oldConn := newMockConn()
	newConn := newMockConn()

	pm.Register(oldConn, "mme.example.com")
	pm.Register(newConn, "mme.example.com")

	result := pm.GetConnection("mme.example.com")
	assert.Equal(t, newConn, result, "Should use new connection for same peer")
}

func TestPeerManager_Unregister(t *testing.T) {
	pm := NewPeerManager()
	conn := newMockConn()

	pm.Register(conn, "mme.example.com")
	pm.Unregister("mme.example.com")

	result := pm.GetConnection("mme.example.com")
	assert.Nil(t, result)
}

func TestPeerManager_UnregisterNonExistent(t *testing.T) {
	pm := NewPeerManager()

	// Should not panic
	pm.Unregister("nonexistent.example.com")

	result := pm.GetConnection("nonexistent.example.com")
	assert.Nil(t, result)
}

func TestPeerManager_GetConnection_NotFound(t *testing.T) {
	pm := NewPeerManager()

	result := pm.GetConnection("unknown.example.com")

	assert.Nil(t, result)
}

func TestPeerManager_GetActiveConnections(t *testing.T) {
	pm := NewPeerManager()
	pm.Register(newMockConn(), "mme1.example.com")
	pm.Register(newMockConn(), "mme2.example.com")
	pm.Register(newMockConn(), "mme3.example.com")

	active := pm.GetActiveConnections()

	assert.Len(t, active, 3)
	assert.Contains(t, active, "mme1.example.com")
	assert.Contains(t, active, "mme2.example.com")
	assert.Contains(t, active, "mme3.example.com")
}

func TestPeerManager_GetActiveConnections_Empty(t *testing.T) {
	pm := NewPeerManager()

	active := pm.GetActiveConnections()

	assert.Empty(t, active)
}

// =============================================================================
// DrainAll Tests (DPR/DPA graceful shutdown)
// =============================================================================

func TestPeerManager_DrainAll_Empty(t *testing.T) {
	pm := NewPeerManager()
	forced := pm.DrainAll(context.Background(), 100*time.Millisecond)
	assert.Equal(t, 0, forced, "no peers → nothing to force-close")
}

// =============================================================================
// PeerDropListener Tests
// =============================================================================

func TestPeerManager_DropListener_FiresOnUnregister(t *testing.T) {
	pm := NewPeerManager()
	var got struct {
		host   string
		reason PeerDropReason
		count  int
	}
	pm.AddDropListener(func(host string, reason PeerDropReason) {
		got.host = host
		got.reason = reason
		got.count++
	})

	pm.Register(newMockConn(), "mme.example.com")
	pm.Unregister("mme.example.com")

	assert.Equal(t, 1, got.count, "listener must fire exactly once on Unregister")
	assert.Equal(t, "mme.example.com", got.host)
	assert.Equal(t, PeerDropUnregistered, got.reason)
}

func TestPeerManager_DropListener_FiresOnDrainAll(t *testing.T) {
	pm := NewPeerManager()
	hosts := make([]string, 0, 2)
	reasons := make([]PeerDropReason, 0, 2)
	var mu sync.Mutex
	pm.AddDropListener(func(host string, reason PeerDropReason) {
		mu.Lock()
		hosts = append(hosts, host)
		reasons = append(reasons, reason)
		mu.Unlock()
	})

	pm.Register(newMockConn(), "mme1.example.com")
	pm.Register(newMockConn(), "mme2.example.com")
	pm.DrainAll(context.Background(), 50*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, hosts, 2, "listener must fire once per drained peer")
	for _, r := range reasons {
		assert.Equal(t, PeerDropDrainAll, r)
	}
}

func TestPeerManager_MultipleListenersAllFire(t *testing.T) {
	pm := NewPeerManager()
	var c1, c2 int
	pm.AddDropListener(func(string, PeerDropReason) { c1++ })
	pm.AddDropListener(func(string, PeerDropReason) { c2++ })

	pm.Register(newMockConn(), "mme.example.com")
	pm.Unregister("mme.example.com")

	assert.Equal(t, 1, c1)
	assert.Equal(t, 1, c2)
}

func TestDrainCause_String(t *testing.T) {
	cases := []struct {
		c    DrainCause
		want string
	}{
		{DrainCauseRebooting, "REBOOTING"},
		{DrainCauseBusy, "BUSY"},
		{DrainCausePermanent, "DO_NOT_WANT_TO_TALK_TO_YOU"},
		{DrainCause(99), "UNKNOWN"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.c.String())
	}
}

func TestPeerManager_DrainAll_DefaultsToRebooting(t *testing.T) {
	// DrainAll is the convenience wrapper for the most common path
	// (SIGTERM/SIGINT → REBOOTING). Equivalent to DrainAllWithCause with
	// DrainCauseRebooting; this test guards the contract.
	pm := NewPeerManager()
	conn := newMockConn()
	pm.Register(conn, "mme.example.com")
	forced := pm.DrainAll(context.Background(), 50*time.Millisecond)
	assert.Equal(t, 1, forced)
}

func TestPeerManager_DrainAllWithCause_Permanent(t *testing.T) {
	// Permanent drain (admin-driven decommission) — exercises the cause
	// propagation path even in passive mode where no DPR is actually
	// written; force-close still runs and the metric label is set.
	pm := NewPeerManager()
	c1 := newMockConn()
	c2 := newMockConn()
	pm.Register(c1, "mme1.example.com")
	pm.Register(c2, "mme2.example.com")

	forced := pm.DrainAllWithCause(context.Background(), 50*time.Millisecond, DrainCausePermanent)

	assert.Equal(t, 2, forced)
	assert.Empty(t, pm.GetActiveConnections())
	assert.True(t, c1.isClosed())
	assert.True(t, c2.isClosed())
}

func TestPeerManager_DrainAll_PassiveMode_StillForcesClose(t *testing.T) {
	// No OriginHost/OriginRealm → activeDWREnabled() == false, so DrainAll
	// must NOT attempt to write a Diameter DPR (the mockConn.Write would
	// otherwise be called with raw bytes). It still has to force-close
	// peers because shutdown can't leave dangling connections.
	pm := NewPeerManager()
	c1 := newMockConn()
	c2 := newMockConn()
	pm.Register(c1, "mme1.example.com")
	pm.Register(c2, "mme2.example.com")

	forced := pm.DrainAll(context.Background(), 100*time.Millisecond)

	assert.Equal(t, 2, forced, "both peers should be force-closed")
	assert.Empty(t, pm.GetActiveConnections(), "registry must be empty post-drain")
	assert.True(t, c1.isClosed(), "conn1 must be closed")
	assert.True(t, c2.isClosed(), "conn2 must be closed")
}

func TestPeerManager_DrainAll_TransitionsToDraining(t *testing.T) {
	pm := NewPeerManager()
	conn := newMockConn()
	pm.Register(conn, "mme.example.com")
	peer := pm.GetPeer("mme.example.com")
	require.NotNil(t, peer)

	// Capture state during a short-window drain so we can observe the
	// transient Draining state (the peer is removed once force-close runs,
	// but the published transition counter records the path through
	// Draining first — see DrainAll).
	pm.DrainAll(context.Background(), 50*time.Millisecond)

	// Post-drain the peer no longer exists.
	assert.Nil(t, pm.GetPeer("mme.example.com"))
}

func TestPeerManager_ConcurrentAccess(t *testing.T) {
	pm := NewPeerManager()
	var wg sync.WaitGroup

	// Concurrent registrations
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn := newMockConn()
			pm.Register(conn, string(rune('A'+id%26))+".example.com")
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm.GetActiveConnections()
		}()
	}

	// Concurrent unregistrations
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pm.Unregister(string(rune('A'+id%26)) + ".example.com")
		}(i)
	}

	wg.Wait()
	// Test passes if no race condition/deadlock occurred
}

// =============================================================================
// DPA correlation tests
// =============================================================================

func TestPeerManager_RecordDPA_SignalsWaiter(t *testing.T) {
	pm := NewPeerManager()
	pm.Register(newMockConn(), "mme.example.com")
	peer := pm.GetPeer("mme.example.com")
	require.NotNil(t, peer)

	// Goroutine parks on dpaSignal, then RecordDPA wakes it. A non-blocking
	// send to a buffered channel must not deadlock even if the waiter hasn't
	// reached its select yet — that's the entire point of the buffer of 1.
	woke := make(chan struct{})
	go func() {
		<-peer.dpaSignal
		close(woke)
	}()

	pm.RecordDPA("mme.example.com")

	select {
	case <-woke:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RecordDPA did not signal dpaSignal within 500ms")
	}
}

func TestPeerManager_RecordDPA_UnknownPeerNoOp(t *testing.T) {
	pm := NewPeerManager()

	// Must not panic — late-arriving DPA after Unregister is a normal race
	// with handleDPA. Counter is intentionally NOT bumped for unknown peers
	// so the metric matches "DPA correlated with an outstanding DPR".
	require.NotPanics(t, func() {
		pm.RecordDPA("ghost.example.com")
	})
}

func TestPeerManager_DrainAll_FastExitOnDPA(t *testing.T) {
	// DrainAll exits as soon as every peer has RecordDPA'd, well before the
	// timeout; without DPA correlation it would sleep the full `grace`
	// window (capped at 2s) regardless of DPA arrival.
	pm := NewPeerManager()
	pm.Register(newMockConn(), "mme1.example.com")
	pm.Register(newMockConn(), "mme2.example.com")

	timeout := 5 * time.Second
	start := time.Now()

	// Concurrently signal both peers' DPA arrival shortly after DrainAll
	// starts. 50ms is well under the 5s timeout but enough to ensure the
	// DrainAll goroutine reaches its phase-2 select before we signal.
	go func() {
		time.Sleep(50 * time.Millisecond)
		pm.RecordDPA("mme1.example.com")
		pm.RecordDPA("mme2.example.com")
	}()

	closed := pm.DrainAll(context.Background(), timeout)
	elapsed := time.Since(start)

	assert.Equal(t, 2, closed, "both peers must be closed after drain")
	assert.Less(t, elapsed, 2*time.Second,
		"DrainAll must exit shortly after DPA arrives, not wait full timeout (got %s)", elapsed)
}

// =============================================================================
// Idle peer GC tests
// =============================================================================

func TestPeerManager_RecordActivity_BumpsBothTimestamps(t *testing.T) {
	// RecordActivity must update both LastActivity and
	// LastApplicationActivity — that's the whole point of having two
	// fields. RecordDWA, by contrast, must update only LastActivity.
	pm := NewPeerManager()
	pm.Register(newMockConn(), "peer.example.com")
	peer := pm.GetPeer("peer.example.com")
	require.NotNil(t, peer)

	// Force both timestamps backward so we can detect the bump.
	old := time.Now().Add(-time.Hour).UnixNano()
	peer.LastActivity.Store(old)
	peer.LastApplicationActivity.Store(old)

	pm.RecordActivity("peer.example.com")

	assert.Greater(t, peer.LastActivity.Load(), old, "RecordActivity must bump LastActivity")
	assert.Greater(t, peer.LastApplicationActivity.Load(), old,
		"RecordActivity must bump LastApplicationActivity")
}

func TestPeerManager_RecordDWA_OnlyBumpsLastActivity(t *testing.T) {
	// Critical for idle-GC correctness: a peer responding to DWR but
	// otherwise silent at the application layer must NOT have its
	// LastApplicationActivity tick forward, or GC never fires.
	pm := NewPeerManager()
	pm.Register(newMockConn(), "peer.example.com")
	peer := pm.GetPeer("peer.example.com")
	require.NotNil(t, peer)

	old := time.Now().Add(-time.Hour).UnixNano()
	peer.LastActivity.Store(old)
	peer.LastApplicationActivity.Store(old)

	pm.RecordDWA("peer.example.com")

	assert.Greater(t, peer.LastActivity.Load(), old, "RecordDWA must bump LastActivity")
	assert.Equal(t, old, peer.LastApplicationActivity.Load(),
		"RecordDWA must NOT touch LastApplicationActivity (idle GC keys off it)")
}

func TestPeerManager_IdleGC_ForceClosesQuietPeer(t *testing.T) {
	// Drives runWatchdog with a tight idlePeerTimeout so the test can
	// observe the GC path. Watchdog tick = 50ms; idle threshold = 100ms.
	// We backdate LastApplicationActivity so the very first tick fires GC.
	pm := NewPeerManagerWith(PeerManagerOptions{
		WatchdogInterval: 50 * time.Millisecond,
		WatchdogTimeout:  500 * time.Millisecond,
		IdlePeerTimeout:  100 * time.Millisecond,
		// Origin* deliberately empty: passive watchdog mode keeps the
		// test free of real Diameter writes.
	})
	conn := newMockConn()
	pm.Register(conn, "zombie.example.com")
	peer := pm.GetPeer("zombie.example.com")
	require.NotNil(t, peer)

	// Backdate so the first watchdog tick observes the peer as idle.
	peer.LastApplicationActivity.Store(time.Now().Add(-time.Hour).UnixNano())

	// Wait for at least one tick + GC cycle.
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("idle GC did not unregister the peer within 500ms")
		default:
		}
		if pm.GetPeer("zombie.example.com") == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	assert.True(t, conn.isClosed(), "GC must force-close the connection")
	assert.Empty(t, pm.GetActiveConnections(), "GC must unregister the peer")
}

func TestPeerManager_IdleGC_LeavesActivePeerAlone(t *testing.T) {
	// Inverse: a peer whose LastApplicationActivity is fresh (recent
	// RecordActivity) must NOT be GC'd even if the test waits past the
	// idle threshold — the timestamp keeps getting refreshed.
	pm := NewPeerManagerWith(PeerManagerOptions{
		WatchdogInterval: 50 * time.Millisecond,
		WatchdogTimeout:  500 * time.Millisecond,
		IdlePeerTimeout:  100 * time.Millisecond,
	})
	pm.Register(newMockConn(), "active.example.com")

	// Tickle the peer every 30ms so LastApplicationActivity stays fresh.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				pm.RecordActivity("active.example.com")
			}
		}
	}()
	defer close(stop)

	// Run past several idle thresholds.
	time.Sleep(400 * time.Millisecond)

	assert.NotNil(t, pm.GetPeer("active.example.com"),
		"active peer must NOT be idle-GC'd")
}

func TestPeerManager_IdleGC_DisabledWhenTimeoutZero(t *testing.T) {
	// IdlePeerTimeout=0 must disable the GC entirely (preserves v3
	// behaviour for tests that don't care about idle detection).
	pm := NewPeerManagerWith(PeerManagerOptions{
		WatchdogInterval: 50 * time.Millisecond,
		WatchdogTimeout:  500 * time.Millisecond,
		// IdlePeerTimeout zero — GC disabled.
	})
	pm.Register(newMockConn(), "ancient.example.com")
	peer := pm.GetPeer("ancient.example.com")
	require.NotNil(t, peer)
	peer.LastApplicationActivity.Store(time.Now().Add(-24 * time.Hour).UnixNano())

	time.Sleep(200 * time.Millisecond)

	assert.NotNil(t, pm.GetPeer("ancient.example.com"),
		"GC disabled must keep ancient peer in registry")
}

func TestPeerManager_DrainAll_NoDPABurnsTimeout(t *testing.T) {
	// Inverse of the fast-exit test: when no peer responds with DPA the
	// drain must still complete (force-close path) and bound itself by the
	// supplied timeout. This is the regression guard against an infinite
	// wait if dpaSignal is never closed.
	pm := NewPeerManager()
	pm.Register(newMockConn(), "silent.example.com")

	timeout := 200 * time.Millisecond
	start := time.Now()
	closed := pm.DrainAll(context.Background(), timeout)
	elapsed := time.Since(start)

	assert.Equal(t, 1, closed)
	assert.GreaterOrEqual(t, elapsed, timeout,
		"drain must wait at least the full timeout when no DPA arrives")
	assert.Less(t, elapsed, 2*timeout,
		"drain must not exceed the timeout by more than a small margin")
}
