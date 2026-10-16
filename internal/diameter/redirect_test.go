// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"bytes"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock returns a deterministic time.Now() so TTL-expiry tests don't
// flake under load.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// ─── RedirectCache.keyFor — §6.13 Usage matrix ──────────────────────────────

func TestRedirectCache_KeyFor_DontCache(t *testing.T) {
	c := NewRedirectCache(RedirectCacheOptions{})
	_, ok := c.keyFor(RedirectUsageDontCache, RedirectContext{SessionID: "s"})
	assert.False(t, ok, "DONT_CACHE must never produce a cache key")
}

func TestRedirectCache_KeyFor_AllUsageVariants(t *testing.T) {
	c := NewRedirectCache(RedirectCacheOptions{})
	ctx := RedirectContext{
		SessionID:     "sess-1",
		DestRealm:     "EPC.example.org", // mixed case to exercise lower-casing
		DestHost:      "Mme01.epc.example.org",
		ApplicationID: 16777251, // S6a
		UserName:      "001010000000001",
	}

	cases := []struct {
		usage RedirectHostUsage
		want  string
	}{
		{RedirectUsageAllSession, "ALL_SESSION|sess-1"},
		{RedirectUsageAllRealm, "ALL_REALM|epc.example.org"},
		{RedirectUsageRealmAndApplication, "REALM_AND_APPLICATION|epc.example.org|16777251"},
		{RedirectUsageAllApplication, "ALL_APPLICATION|16777251"},
		{RedirectUsageAllHost, "ALL_HOST|mme01.epc.example.org"},
		{RedirectUsageAllUser, "ALL_USER|001010000000001"},
	}

	for _, tc := range cases {
		t.Run(tc.usage.String(), func(t *testing.T) {
			got, ok := c.keyFor(tc.usage, ctx)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRedirectCache_KeyFor_MissingFieldRefusesKey(t *testing.T) {
	// Each Usage requires a specific ctx field; missing field ⇒ no key
	// (caller falls back to single-shot honoring instead of caching).
	c := NewRedirectCache(RedirectCacheOptions{})
	cases := []struct {
		usage RedirectHostUsage
		ctx   RedirectContext
	}{
		{RedirectUsageAllSession, RedirectContext{}},
		{RedirectUsageAllRealm, RedirectContext{}},
		{RedirectUsageRealmAndApplication, RedirectContext{ApplicationID: 1}},
		{RedirectUsageAllHost, RedirectContext{}},
		{RedirectUsageAllUser, RedirectContext{}},
	}
	for _, tc := range cases {
		_, ok := c.keyFor(tc.usage, tc.ctx)
		assert.False(t, ok, "%s with missing field must refuse caching", tc.usage)
	}
}

func TestRedirectCache_KeyFor_NoCollisionAcrossUsage(t *testing.T) {
	// A redirect with Usage=ALL_REALM=foo MUST NOT share a cache slot with
	// Usage=ALL_USER=foo. Prefix discipline is the safety property.
	c := NewRedirectCache(RedirectCacheOptions{})
	ctx := RedirectContext{DestRealm: "foo", UserName: "foo"}
	k1, _ := c.keyFor(RedirectUsageAllRealm, ctx)
	k2, _ := c.keyFor(RedirectUsageAllUser, ctx)
	assert.NotEqual(t, k1, k2, "cross-Usage key collision would leak routing decisions")
}

// ─── RedirectCache.Store / Lookup TTL + eviction ────────────────────────────

func TestRedirectCache_LookupReturnsStoredEntry(t *testing.T) {
	now := time.Now()
	c := NewRedirectCache(RedirectCacheOptions{Now: fixedClock(now)})
	c.Store(&RedirectIndication{
		Hosts:        []string{"aaa://mme02.example.org"},
		Usage:        RedirectUsageAllRealm,
		MaxCacheTime: 30 * time.Second,
	}, RedirectContext{DestRealm: "example.org"})

	got, ok := c.Lookup(RedirectUsageAllRealm, RedirectContext{DestRealm: "example.org"})
	require.True(t, ok)
	assert.Equal(t, []string{"aaa://mme02.example.org"}, got.Targets)
}

func TestRedirectCache_LookupExpiresLazily(t *testing.T) {
	clock := time.Now()
	clockFn := func() time.Time { return clock }
	c := NewRedirectCache(RedirectCacheOptions{Now: clockFn, DefaultTTL: 10 * time.Second})

	c.Store(&RedirectIndication{
		Hosts: []string{"aaa://mme02.example.org"},
		Usage: RedirectUsageAllRealm,
	}, RedirectContext{DestRealm: "example.org"})
	require.Equal(t, 1, c.Size())

	clock = clock.Add(11 * time.Second) // past TTL
	_, ok := c.Lookup(RedirectUsageAllRealm, RedirectContext{DestRealm: "example.org"})
	assert.False(t, ok)
	assert.Equal(t, 0, c.Size(), "expired entry must be lazily evicted on Lookup")
}

func TestRedirectCache_StoreDontCacheIsNoop(t *testing.T) {
	c := NewRedirectCache(RedirectCacheOptions{})
	c.Store(&RedirectIndication{
		Hosts: []string{"aaa://x"},
		Usage: RedirectUsageDontCache,
	}, RedirectContext{SessionID: "s"})
	assert.Equal(t, 0, c.Size())
}

func TestRedirectCache_CapacityEvictsOldestExpiring(t *testing.T) {
	// Capacity=2: insert three entries with increasing TTL and verify the
	// shortest-lived one is evicted to make room for the third.
	now := time.Now()
	clock := now
	c := NewRedirectCache(RedirectCacheOptions{
		Capacity:   2,
		DefaultTTL: 1 * time.Hour,
		Now:        func() time.Time { return clock },
	})

	c.Store(&RedirectIndication{Hosts: []string{"aaa://a"}, Usage: RedirectUsageAllRealm, MaxCacheTime: 5 * time.Second}, RedirectContext{DestRealm: "a"})
	c.Store(&RedirectIndication{Hosts: []string{"aaa://b"}, Usage: RedirectUsageAllRealm, MaxCacheTime: 60 * time.Second}, RedirectContext{DestRealm: "b"})
	require.Equal(t, 2, c.Size())

	c.Store(&RedirectIndication{Hosts: []string{"aaa://c"}, Usage: RedirectUsageAllRealm, MaxCacheTime: 30 * time.Second}, RedirectContext{DestRealm: "c"})

	// Eviction policy: drop oldest-expiring (a). b and c must survive.
	assert.Equal(t, 2, c.Size())
	_, okA := c.Lookup(RedirectUsageAllRealm, RedirectContext{DestRealm: "a"})
	_, okB := c.Lookup(RedirectUsageAllRealm, RedirectContext{DestRealm: "b"})
	_, okC := c.Lookup(RedirectUsageAllRealm, RedirectContext{DestRealm: "c"})
	assert.False(t, okA, "oldest-expiring entry should have been evicted")
	assert.True(t, okB)
	assert.True(t, okC)
}

func TestRedirectCache_DefaultTTLAppliedWhenPeerOmitsMaxCacheTime(t *testing.T) {
	clock := time.Now()
	c := NewRedirectCache(RedirectCacheOptions{
		DefaultTTL: 60 * time.Second,
		Now:        func() time.Time { return clock },
	})
	c.Store(&RedirectIndication{
		Hosts: []string{"aaa://x"},
		Usage: RedirectUsageAllHost,
		// MaxCacheTime intentionally zero — peer omitted.
	}, RedirectContext{DestHost: "x"})

	clock = clock.Add(59 * time.Second) // still within default
	_, ok := c.Lookup(RedirectUsageAllHost, RedirectContext{DestHost: "x"})
	assert.True(t, ok)

	clock = clock.Add(2 * time.Second) // 61s — past default
	_, ok = c.Lookup(RedirectUsageAllHost, RedirectContext{DestHost: "x"})
	assert.False(t, ok)
}

// ─── DetectLoop ──────────────────────────────────────────────────────────────

func TestDetectLoop_NoVisitedNoDepth(t *testing.T) {
	assert.Empty(t, DetectLoop("mme02.example", nil, 0, 3))
}

func TestDetectLoop_HopCountExceeded(t *testing.T) {
	r := DetectLoop("mme04.example", []string{"a", "b", "c"}, 3, 3)
	assert.Equal(t, "redirect_depth_exceeded", r)
}

func TestDetectLoop_MaxDepthZeroDisablesHopCheck(t *testing.T) {
	// maxDepth=0 disables hop-count check (admin opt-out).
	assert.Empty(t, DetectLoop("x", []string{"a"}, 100, 0))
}

func TestDetectLoop_RouteRecordHit_CaseInsensitive(t *testing.T) {
	// DiameterIdentity is case-insensitive (RFC §4.3.1) — comparison must
	// fold case or peer pools with mixed-case naming will ping-pong silently.
	r := DetectLoop("MME01.Example", []string{"mme01.example"}, 1, 3)
	assert.Equal(t, "next_host_already_visited", r)
}

func TestDetectLoop_NotInVisited(t *testing.T) {
	assert.Empty(t, DetectLoop("mme03.example", []string{"mme01.example", "mme02.example"}, 2, 3))
}

// ─── RetryPostureFor ─────────────────────────────────────────────────────────

func TestRetryPostureFor_PerVerb(t *testing.T) {
	cases := []struct {
		verb string
		want RetryPosture
	}{
		{"RSR", RetryAlways},
		{"CLR", RetryAlways},
		{"IDR", RetryIfOperatorOptsIn},
		// PUR and NOR are MME → HSS only (TS 29.272 §7.2.13, §7.2.17): the
		// gateway never sends them, so they are unclassified.
		{"PUR", NeverRetry},
		{"NOR", NeverRetry},
		// Gx verbs
		{"RAR", RetryAlways},           // rule-name idempotent at PCEF
		{"ASR", RetryIfOperatorOptsIn}, // 5002 reaction varies by PCEF vendor
		{"AIR", NeverRetry},            // unclassified verb defaults to NeverRetry (fail-closed)
		{"", NeverRetry},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			assert.Equal(t, tc.want, RetryPostureFor(tc.verb))
		})
	}
}

// ─── ParseRedirectIndication ─────────────────────────────────────────────────

func TestParseRedirectIndication_Roundtrip(t *testing.T) {
	// Build a synthetic answer with 3006 + Redirect-Host AVPs and verify
	// the parser extracts every field. Round-trip via diam.ReadMessage so
	// we exercise the wire codec path, not just the in-memory builder.
	m := diam.NewMessage(316 /* ULA */, 0, 16777251, 1, 1, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(3006))
	m.NewAVP(avp.RedirectHost, avp.Mbit, 0, datatype.DiameterURI("aaa://mme02.example.org"))
	m.NewAVP(avp.RedirectHost, avp.Mbit, 0, datatype.DiameterURI("aaa://mme03.example.org"))
	m.NewAVP(avp.RedirectHostUsage, avp.Mbit, 0, datatype.Enumerated(RedirectUsageAllRealm))
	m.NewAVP(avp.RedirectMaxCacheTime, avp.Mbit, 0, datatype.Unsigned32(120))

	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err)
	parsed, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	ind, ok := ParseRedirectIndication(parsed)
	require.True(t, ok)
	assert.Equal(t, []string{"aaa://mme02.example.org", "aaa://mme03.example.org"}, ind.Hosts)
	assert.Equal(t, RedirectUsageAllRealm, ind.Usage)
	assert.Equal(t, 120*time.Second, ind.MaxCacheTime)
}

func TestParseRedirectIndication_NotRedirectResult(t *testing.T) {
	m := diam.NewMessage(316, 0, 16777251, 1, 1, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(2001))
	_, ok := ParseRedirectIndication(m)
	assert.False(t, ok, "non-3006 answers must not parse as redirect")
}

func TestParseRedirectIndication_3006WithoutRedirectHostIsMiss(t *testing.T) {
	// Spec violation by peer — we tolerate by surfacing parse miss.
	m := diam.NewMessage(316, 0, 16777251, 1, 1, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(3006))
	_, ok := ParseRedirectIndication(m)
	assert.False(t, ok)
}

func TestParseRedirectIndication_DefaultUsageIsDontCache(t *testing.T) {
	// RFC §6.5 default when Redirect-Host-Usage AVP is absent.
	m := diam.NewMessage(316, 0, 16777251, 1, 1, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(3006))
	m.NewAVP(avp.RedirectHost, avp.Mbit, 0, datatype.DiameterURI("aaa://x"))

	ind, ok := ParseRedirectIndication(m)
	require.True(t, ok)
	assert.Equal(t, RedirectUsageDontCache, ind.Usage)
	assert.Equal(t, time.Duration(0), ind.MaxCacheTime)
}
