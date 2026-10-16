// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/stretchr/testify/assert"
)

// fakeClock returns a controllable time source for deterministic eviction
// tests. The TTL paths depend on time arithmetic that real time.Now would
// make impossible to assert.
type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

func newRegistry(policy EvictionPolicy, fc *fakeClock) *GxSessionRegistry {
	return NewSessionRegistry(RegistryOptions{Policy: policy, Now: fc.Now})
}

// ─── Bind / Lookup / Unbind happy path ──────────────────────────────────────

func TestRegistry_BindLookupUnbind(t *testing.T) {
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{}, fc)

	r.Bind("sess-1", "pcef-a.example.com")
	host, ok := r.Lookup("sess-1")
	assert.True(t, ok)
	assert.Equal(t, "pcef-a.example.com", host)
	assert.Equal(t, 1, r.Size())

	r.Unbind("sess-1", "pcef-b.example.com")
	_, ok = r.Lookup("sess-1")
	assert.True(t, ok, "only the bound peer can unbind")

	r.Unbind("sess-1", "pcef-a.example.com")
	_, ok = r.Lookup("sess-1")
	assert.False(t, ok)
	assert.Equal(t, 0, r.Size())
}

func TestRegistry_BindIgnoresEmpty(t *testing.T) {
	r := newRegistry(EvictionPolicy{}, &fakeClock{})
	r.Bind("", "peer")
	r.Bind("sess", "")
	assert.Equal(t, 0, r.Size())
}

func TestRegistry_LookupMiss(t *testing.T) {
	r := newRegistry(EvictionPolicy{}, &fakeClock{})
	_, ok := r.Lookup("nope")
	assert.False(t, ok)
}

func TestRegistry_RebindRefreshesLastSeen(t *testing.T) {
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{IdleTimeout: 1 * time.Hour}, fc)

	r.Bind("sess-1", "pcef-a")
	fc.Advance(50 * time.Minute)
	r.Bind("sess-1", "pcef-a") // CCR-U refresh
	fc.Advance(50 * time.Minute)
	// Total elapsed since BoundAt = 100 min, but LastSeen was refreshed at
	// 50 min — so idle is only 50 min, under the 60-min threshold.
	_, ok := r.Lookup("sess-1")
	assert.True(t, ok, "rebind should refresh LastSeen and prevent eviction")
}

// TestRegistry_SessionStaysWithItsPeer: a peer that sends a CCR with another
// peer's Session-Id must not take the session over, or it would receive that
// session's RAR/ASR.
func TestRegistry_SessionStaysWithItsPeer(t *testing.T) {
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{}, fc)

	r.Bind("sess-1", "pcef-a")
	r.Bind("sess-1", "pcef-b") // a different PCEF claims the same session id
	host, ok := r.Lookup("sess-1")
	assert.True(t, ok)
	assert.Equal(t, "pcef-a", host, "first bind holds")

	r.DropPeer("pcef-a", "test")
	r.Bind("sess-1", "pcef-b")
	host, _ = r.Lookup("sess-1")
	assert.Equal(t, "pcef-b", host, "free again once the bound peer is gone")
}

// ─── Eviction policy formula ────────────────────────────────────────────────

func TestShouldEvict_StrictPolicy_NeverEvicts(t *testing.T) {
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{}, fc) // all zero ⇒ strict
	r.Bind("sess-1", "pcef-a")
	fc.Advance(365 * 24 * time.Hour)
	_, ok := r.Lookup("sess-1")
	assert.True(t, ok, "strict policy must never TTL-evict")
}

func TestShouldEvict_IdlePolicy_SmartphoneProfile(t *testing.T) {
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{IdleTimeout: 24 * time.Hour}, fc)
	r.Bind("sess-1", "pcef-a")

	fc.Advance(23 * time.Hour)
	_, ok := r.Lookup("sess-1")
	assert.True(t, ok, "under threshold — keep")

	fc.Advance(2 * time.Hour) // now 25h since LastSeen
	_, ok = r.Lookup("sess-1")
	assert.False(t, ok, "past idle threshold — evict")
}

func TestShouldEvict_AbsolutePolicy(t *testing.T) {
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{AbsoluteLifetime: 30 * 24 * time.Hour}, fc)
	r.Bind("sess-1", "pcef-a")

	// Even with constant CCR-U traffic refreshing LastSeen, absolute caps it.
	for i := 0; i < 30; i++ {
		fc.Advance(20 * time.Hour)
		r.Bind("sess-1", "pcef-a") // refresh
	}
	// 30*20h = 600h = 25d → still under 30d cap
	_, ok := r.Lookup("sess-1")
	assert.True(t, ok)

	fc.Advance(6 * 24 * time.Hour) // now well past 30d
	_, ok = r.Lookup("sess-1")
	assert.False(t, ok, "absolute lifetime evicts despite refreshes")
}

func TestShouldEvict_IoTPolicy_FloorProtects(t *testing.T) {
	// IoT: min=30d, idle=90d. Long-quiet sessions kept up to 30d
	// regardless of activity, then idle takes over.
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{
		MinLifetime: 30 * 24 * time.Hour,
		IdleTimeout: 90 * 24 * time.Hour,
	}, fc)
	r.Bind("sess-iot", "pcef-iot")

	// 29 days of total silence — under floor.
	fc.Advance(29 * 24 * time.Hour)
	_, ok := r.Lookup("sess-iot")
	assert.True(t, ok, "MinLifetime floor protects quiet IoT session")

	// Past floor (30d) and still quiet — but 30d < 90d idle threshold.
	fc.Advance(2 * 24 * time.Hour)
	_, ok = r.Lookup("sess-iot")
	assert.True(t, ok, "past floor but still under idle threshold")

	// Past 90d idle.
	fc.Advance(90 * 24 * time.Hour)
	_, ok = r.Lookup("sess-iot")
	assert.False(t, ok, "past idle threshold after floor — evict")
}

func TestShouldEvict_MixedHybridPolicy(t *testing.T) {
	// Mixed preset: min=7d, idle=24h, absolute=30d.
	fc := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := newRegistry(EvictionPolicy{
		MinLifetime:      7 * 24 * time.Hour,
		IdleTimeout:      24 * time.Hour,
		AbsoluteLifetime: 30 * 24 * time.Hour,
	}, fc)
	r.Bind("sess-1", "pcef-a")

	// Day 6 of silence — under floor, keep.
	fc.Advance(6 * 24 * time.Hour)
	_, ok := r.Lookup("sess-1")
	assert.True(t, ok)

	// Day 8 — past floor (7d) AND past idle (24h since bind), evict.
	fc.Advance(2 * 24 * time.Hour)
	_, ok = r.Lookup("sess-1")
	assert.False(t, ok, "past floor + past idle ⇒ evict")
}

// ─── DropPeer ──────────────────────────────────────────────────────────────

func TestDropPeer_ClearsAllForHost(t *testing.T) {
	r := newRegistry(EvictionPolicy{}, &fakeClock{t: time.Unix(1_700_000_000, 0)})
	r.Bind("s1", "pcef-a")
	r.Bind("s2", "pcef-a")
	r.Bind("s3", "pcef-b")
	assert.Equal(t, 3, r.Size())

	r.DropPeer("pcef-a", diameter.PeerDropUnregistered)
	assert.Equal(t, 1, r.Size())

	_, ok := r.Lookup("s1")
	assert.False(t, ok)
	_, ok = r.Lookup("s2")
	assert.False(t, ok)
	host, ok := r.Lookup("s3")
	assert.True(t, ok)
	assert.Equal(t, "pcef-b", host)
}

// ─── PolicyFromConfig + preset expansion ───────────────────────────────────

func TestPolicyFromConfig_ExplicitDurations(t *testing.T) {
	c := config.GxSessionEvictionConfig{
		MinLifetime:      "1h",
		IdleTimeout:      "30m",
		AbsoluteLifetime: "24h",
	}
	p := PolicyFromConfig(c)
	assert.Equal(t, time.Hour, p.MinLifetime)
	assert.Equal(t, 30*time.Minute, p.IdleTimeout)
	assert.Equal(t, 24*time.Hour, p.AbsoluteLifetime)
}

func TestPolicyFromConfig_InvalidDurationDisablesField(t *testing.T) {
	c := config.GxSessionEvictionConfig{
		IdleTimeout: "garbage",
	}
	p := PolicyFromConfig(c)
	assert.Equal(t, time.Duration(0), p.IdleTimeout, "malformed duration ⇒ disabled, not silent default")
}

func TestPolicyFromConfig_EmptyAllZero(t *testing.T) {
	p := PolicyFromConfig(config.GxSessionEvictionConfig{})
	assert.Equal(t, EvictionPolicy{}, p)
}
