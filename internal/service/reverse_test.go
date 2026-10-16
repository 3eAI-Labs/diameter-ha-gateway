// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"sync"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── hostFromDiameterURI ────────────────────────────────────────────────────

func TestHostFromDiameterURI(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{"plain aaa", "aaa://mme01.epc.example.org", "mme01.epc.example.org"},
		{"aaa with port", "aaa://mme02.epc.example.org:3868", "mme02.epc.example.org"},
		{"aaas (TLS)", "aaas://hss01.example.org", "hss01.example.org"},
		{"upper-case scheme", "AAA://mme03.example.org", "mme03.example.org"},
		{"with transport param", "aaa://mme04.example.org;transport=tcp", "mme04.example.org"},
		{"empty", "", ""},
		{"unknown scheme", "http://mme.example.org", ""},
		{"missing host", "aaa://", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hostFromDiameterURI(tc.uri))
		})
	}
}

// ─── pickNextRedirectTarget ─────────────────────────────────────────────────

func TestPickNextRedirectTarget_FirstFreshCandidateWins(t *testing.T) {
	rs := &ReverseService{}
	host, uri, ok := rs.pickNextRedirectTarget(
		[]string{"aaa://mme02.example.org", "aaa://mme03.example.org"},
		[]string{"gw.example.org", "mme01.example.org"},
		0, 3,
	)
	require.True(t, ok)
	assert.Equal(t, "mme02.example.org", host)
	assert.Equal(t, "aaa://mme02.example.org", uri)
}

func TestPickNextRedirectTarget_SkipsLoopedCandidate(t *testing.T) {
	// First candidate already visited → skip → second candidate accepted.
	rs := &ReverseService{}
	host, _, ok := rs.pickNextRedirectTarget(
		[]string{"aaa://mme01.example.org", "aaa://mme02.example.org"},
		[]string{"mme01.example.org"},
		0, 3,
	)
	require.True(t, ok)
	assert.Equal(t, "mme02.example.org", host)
}

func TestPickNextRedirectTarget_AllVisitedReturnsFalse(t *testing.T) {
	rs := &ReverseService{}
	_, _, ok := rs.pickNextRedirectTarget(
		[]string{"aaa://mme01.example.org", "aaa://mme02.example.org"},
		[]string{"mme01.example.org", "mme02.example.org"},
		0, 3,
	)
	assert.False(t, ok, "every candidate already visited ⇒ no fresh hop")
}

func TestPickNextRedirectTarget_DepthExceededShortCircuits(t *testing.T) {
	// depth=3, maxDepth=3 → depth+1=4 > 3 → fail closed even when candidates
	// look fresh. Hop count is global; trying later candidates can't help.
	rs := &ReverseService{}
	_, _, ok := rs.pickNextRedirectTarget(
		[]string{"aaa://mme02.example.org", "aaa://mme03.example.org"},
		[]string{"gw"},
		3, 3,
	)
	assert.False(t, ok)
}

func TestPickNextRedirectTarget_SkipsMalformedURI(t *testing.T) {
	// Malformed scheme → skip → fall through to the well-formed second URI.
	rs := &ReverseService{}
	host, _, ok := rs.pickNextRedirectTarget(
		[]string{"http://wrong.example.org", "aaa://mme02.example.org"},
		nil, 0, 3,
	)
	require.True(t, ok)
	assert.Equal(t, "mme02.example.org", host)
}

// ─── collectRouteRecord ─────────────────────────────────────────────────────

func TestCollectRouteRecord_ReturnsAllHostsInOrder(t *testing.T) {
	rs := &ReverseService{}
	m := diam.NewMessage(317 /* CLA */, 0, 16777251, 1, 1, dict.Default)
	m.NewAVP(avp.RouteRecord, avp.Mbit, 0, datatype.DiameterIdentity("relay-a.example.org"))
	m.NewAVP(avp.RouteRecord, avp.Mbit, 0, datatype.DiameterIdentity("relay-b.example.org"))
	hosts := rs.collectRouteRecord(m)
	assert.Equal(t, []string{"relay-a.example.org", "relay-b.example.org"}, hosts)
}

func TestCollectRouteRecord_NoneReturnsNil(t *testing.T) {
	rs := &ReverseService{}
	m := diam.NewMessage(317, 0, 16777251, 1, 1, dict.Default)
	assert.Nil(t, rs.collectRouteRecord(m))
}

// ─── extractSessionID ───────────────────────────────────────────────────────

func TestExtractSessionID_Present(t *testing.T) {
	m := diam.NewMessage(317, 0, 16777251, 1, 1, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("gw.example.org;1234;5678"))
	assert.Equal(t, "gw.example.org;1234;5678", extractSessionID(m))
}

func TestExtractSessionID_Absent(t *testing.T) {
	m := diam.NewMessage(317, 0, 16777251, 1, 1, dict.Default)
	assert.Equal(t, "", extractSessionID(m))
}

// ─── OnPeerDrop ─────────────────────────────────────────────────────────────

// TestOnPeerDrop_FastFailsMatchingPeerOnly verifies the listener drains
// every pendingReqs entry whose peerHost matches the dropped peer, leaves
// entries for other peers untouched, and signals the synthetic 4012 message.
func TestOnPeerDrop_FastFailsMatchingPeerOnly(t *testing.T) {
	rs := &ReverseService{
		pendingReqs: make(map[string]*pendingReq),
	}
	chA1 := make(chan *diam.Message, 1)
	chA2 := make(chan *diam.Message, 1)
	chB := make(chan *diam.Message, 1)
	rs.pendingReqs["sid-a1"] = &pendingReq{ch: chA1, peerHost: "mme-a.example.org"}
	rs.pendingReqs["sid-a2"] = &pendingReq{ch: chA2, peerHost: "mme-a.example.org"}
	rs.pendingReqs["sid-b"] = &pendingReq{ch: chB, peerHost: "mme-b.example.org"}

	rs.OnPeerDrop("mme-a.example.org", diameter.PeerDropUnregistered)

	// Both A entries should be drained with synthetic 4012; B untouched.
	for _, ch := range []chan *diam.Message{chA1, chA2} {
		select {
		case ans := <-ch:
			rc, err := ans.FindAVP(avp.ResultCode, 0)
			require.NoError(t, err)
			assert.Equal(t, datatype.Unsigned32(4012), rc.Data.(datatype.Unsigned32),
				"drained waiter must receive Result-Code 4012 (AGENT_FAILOVER)")
		default:
			t.Fatal("expected synthetic 4012 on dropped peer waiter, channel empty")
		}
	}
	select {
	case <-chB:
		t.Fatal("waiter for unrelated peer must NOT be drained")
	default:
	}

	rs.mu.RLock()
	defer rs.mu.RUnlock()
	assert.Len(t, rs.pendingReqs, 1, "only the unrelated peer entry should remain")
	_, ok := rs.pendingReqs["sid-b"]
	assert.True(t, ok)
}

func TestOnPeerDrop_NoMatchingPeerIsNoop(t *testing.T) {
	rs := &ReverseService{
		pendingReqs: make(map[string]*pendingReq),
	}
	ch := make(chan *diam.Message, 1)
	rs.pendingReqs["sid"] = &pendingReq{ch: ch, peerHost: "mme-a.example.org"}

	rs.OnPeerDrop("mme-other.example.org", diameter.PeerDropUnregistered)

	rs.mu.RLock()
	defer rs.mu.RUnlock()
	assert.Len(t, rs.pendingReqs, 1, "no-match drop must not modify pendingReqs")
	select {
	case <-ch:
		t.Fatal("waiter must not be signalled when peer host doesn't match")
	default:
	}
}

func TestOnPeerDrop_AbandonedWaiterDoesNotPanic(t *testing.T) {
	// Race window: handleReverse's timeout case fired and abandoned the
	// channel, but pendingReqs entry not yet removed when OnPeerDrop runs.
	// Non-blocking send must not block or panic.
	rs := &ReverseService{
		pendingReqs: make(map[string]*pendingReq),
	}
	ch := make(chan *diam.Message) // unbuffered + no reader → would block
	rs.pendingReqs["sid"] = &pendingReq{ch: ch, peerHost: "mme.example.org"}

	done := make(chan struct{})
	go func() {
		rs.OnPeerDrop("mme.example.org", diameter.PeerDropUnregistered)
		close(done)
	}()

	select {
	case <-done:
		// success — non-blocking send orphaned the synthetic answer
	default:
		// give it a moment to run
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-done
		}()
		// Force progress; if OnPeerDrop blocks forever the test times out.
		wg.Wait()
	}
}

// TestBuildFailoverAnswer_HasResultCode4012 documents the synthetic message
// shape consumed by OnPeerDrop. Not wire-valid (no Origin-Host/Realm),
// in-process consumer only.
func TestBuildFailoverAnswer_HasResultCode4012(t *testing.T) {
	m := buildFailoverAnswer()
	require.NotNil(t, m)
	rc, err := m.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(4012), rc.Data.(datatype.Unsigned32))
}
