// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerRateLimiter_NilSafe(t *testing.T) {
	// Nil receiver is the "rate limiting disabled" path. Must allow.
	var prl *PeerRateLimiter
	assert.True(t, prl.Admit("any.host"), "nil limiter must allow")
	assert.Equal(t, uint32(0), prl.ConsecutiveTrips("any.host"))
}

func TestPeerRateLimiter_Unconfigured(t *testing.T) {
	// Peer not in tpsByHost = unbounded (the default).
	prl := NewPeerRateLimiter(map[string]int{
		"limited.host": 5,
	}, 0)
	assert.True(t, prl.Admit("ghost.host"), "unconfigured peer must be unbounded")
	for i := 0; i < 100; i++ {
		assert.True(t, prl.Admit("ghost.host"))
	}
}

func TestPeerRateLimiter_AllowsWithinBurst(t *testing.T) {
	// 10 TPS, default burstFactor → 20 token bucket. First 20 must pass
	// instantly (full bucket at construction).
	prl := NewPeerRateLimiter(map[string]int{
		"peer.host": 10,
	}, 0)
	allowed := 0
	for i := 0; i < 20; i++ {
		if prl.Admit("peer.host") {
			allowed++
		}
	}
	assert.Equal(t, 20, allowed, "full burst must be admitted")
}

func TestPeerRateLimiter_DeniesOverBurst(t *testing.T) {
	// Drain the bucket synchronously, then immediate next call must deny.
	// Using burstFactor=1 so we can predict exactly when the deny happens.
	prl := NewPeerRateLimiter(map[string]int{
		"peer.host": 10,
	}, 1) // burst = 10
	for i := 0; i < 10; i++ {
		require.True(t, prl.Admit("peer.host"), "burst capacity %d must admit", i)
	}
	assert.False(t, prl.Admit("peer.host"), "11th call must be denied (burst exhausted)")
	assert.Equal(t, uint32(1), prl.ConsecutiveTrips("peer.host"))
}

func TestPeerRateLimiter_ConsecutiveTripsResetOnAdmit(t *testing.T) {
	// "consecutive" semantic: a single admit between two trips
	// resets the counter. Verifies the trip counter is not a lifetime
	// monotonic counter.
	prl := NewPeerRateLimiter(map[string]int{
		"peer.host": 100, // high TPS so refill is fast
	}, 1)

	// Drain the burst.
	for i := 0; i < 100; i++ {
		require.True(t, prl.Admit("peer.host"))
	}
	assert.False(t, prl.Admit("peer.host"))
	assert.Equal(t, uint32(1), prl.ConsecutiveTrips("peer.host"))

	// Wait for refill (>= 10ms gives us roughly 1 token at 100 TPS).
	time.Sleep(15 * time.Millisecond)

	assert.True(t, prl.Admit("peer.host"), "post-refill admit must succeed")
	assert.Equal(t, uint32(0), prl.ConsecutiveTrips("peer.host"),
		"successful admit must reset consecutive-trips counter")
}

func TestPeerRateLimiter_TripCounterCrossesAuditThreshold(t *testing.T) {
	// Slam enough trips past the audit threshold (100) to confirm the
	// counter advances correctly. The audit emission itself goes through
	// common-go/audit which is exercised in integration tests; here we
	// only verify the trip-count machinery the audit hook relies on.
	prl := NewPeerRateLimiter(map[string]int{
		"peer.host": 1, // tiny TPS so refill rarely happens during the test
	}, 1)
	require.True(t, prl.Admit("peer.host")) // consume the burst

	// Now generate >= 100 trips quickly (< 1s so refill stays at most 1
	// token, which we'll absorb if it hits).
	denies := 0
	for i := 0; i < 110; i++ {
		if !prl.Admit("peer.host") {
			denies++
		}
	}
	assert.GreaterOrEqual(t, denies, 100,
		"≥100 denies expected within sub-second window")
	assert.GreaterOrEqual(t, prl.ConsecutiveTrips("peer.host"), uint32(100),
		"consecutive-trip counter must reach the audit threshold")
}

func TestPeerRateLimiter_PerPeerIsolation(t *testing.T) {
	// Slamming peer A with traffic must not affect peer B's bucket.
	prl := NewPeerRateLimiter(map[string]int{
		"a.host": 1,
		"b.host": 1,
	}, 1)

	require.True(t, prl.Admit("a.host"))
	require.False(t, prl.Admit("a.host"), "a.host bucket exhausted")

	assert.True(t, prl.Admit("b.host"), "b.host bucket must remain full")
	assert.Equal(t, uint32(1), prl.ConsecutiveTrips("a.host"))
	assert.Equal(t, uint32(0), prl.ConsecutiveTrips("b.host"))
}

func TestPeerRateLimiter_ConcurrentAdmit(t *testing.T) {
	// Race detector exercise: many goroutines hitting the same limiter
	// must not deadlock or panic. Logical correctness (exact admit count)
	// is non-deterministic under load and is not the point of this test.
	prl := NewPeerRateLimiter(map[string]int{
		"peer.host": 1000,
	}, 1)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				prl.Admit("peer.host")
			}
		}()
	}
	wg.Wait()
}
