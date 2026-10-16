// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type replyRequester struct{}

func (replyRequester) Request(string, interface{}, time.Duration) ([]byte, error) {
	return []byte(`{"result_code":2001}`), nil
}

// TestHandleCCR_BindsSessionToTheConnectionsPeer: the session is bound to the
// identity the connection presented at CER, not to the CCR's Origin-Host —
// otherwise a PCEF could claim another PCEF's identity, take over its
// connection and receive its RAR/ASR.
func TestHandleCCR_BindsSessionToTheConnectionsPeer(t *testing.T) {
	pm := diameter.NewPeerManager()
	victim, impostor := &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(victim, "pgw.example.com") // buildTestGxCCR's Origin-Host
	pm.Register(impostor, "pgw-other.example.com")
	reg := NewSessionRegistry(RegistryOptions{})

	h := NewHandler(replyRequester{}, pm, time.Second).WithSessionRegistry(reg)
	h.HandleCCR(impostor, buildTestGxCCR(t, charging.CCRequestTypeInitial, 0))

	host, ok := reg.Lookup("gx-session-123")
	assert.True(t, ok)
	assert.Equal(t, "pgw-other.example.com", host)
	assert.Same(t, victim, pm.GetConnection("pgw.example.com"))
}

// TestHandleCCR_AnotherPeerCannotTakeOrEndASession: a PCEF that sends CCR-U
// or CCR-T with another PCEF's Session-Id neither moves the session to
// itself nor removes it.
func TestHandleCCR_AnotherPeerCannotTakeOrEndASession(t *testing.T) {
	pm := diameter.NewPeerManager()
	owner, impostor := &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(owner, "pgw.example.com")
	pm.Register(impostor, "pgw-other.example.com")
	reg := NewSessionRegistry(RegistryOptions{})
	h := NewHandler(replyRequester{}, pm, time.Second).WithSessionRegistry(reg)

	h.HandleCCR(owner, buildTestGxCCR(t, charging.CCRequestTypeInitial, 0))
	h.HandleCCR(impostor, buildTestGxCCR(t, charging.CCRequestTypeUpdate, 1))
	h.HandleCCR(impostor, buildTestGxCCR(t, charging.CCRequestTypeTermination, 2))

	host, ok := reg.Lookup("gx-session-123")
	assert.True(t, ok, "session removed by another peer's CCR-T")
	assert.Equal(t, "pgw.example.com", host, "session moved by another peer's CCR-U")
}

// TestHandleCCR_CCACarriesMandatoryAVPs: a CCA carries what RFC 4006 §3.2
// requires even when the PCRF's reply holds only the Result-Code: the
// request's Session-Id first, Origin-Host and Origin-Realm (the gateway's),
// Auth-Application-Id, CC-Request-Type and CC-Request-Number.
func TestHandleCCR_CCACarriesMandatoryAVPs(t *testing.T) {
	pm := diameter.NewPeerManagerWith(diameter.PeerManagerOptions{OriginHost: "gw.example", OriginRealm: "example"})
	pcef := &diamtest.Conn{}
	pm.Register(pcef, "pgw.example.com")
	ccr := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)
	ccr.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDGx))

	NewHandler(replyRequester{}, pm, time.Second).HandleCCR(pcef, ccr)

	answers, err := pcef.Messages()
	require.NoError(t, err)
	require.Len(t, answers, 1)
	cca := answers[0]
	assert.Equal(t, uint32(avp.SessionID), cca.AVP[0].Code)
	assert.Equal(t, "gx-session-123", diameter.AVPString(cca.AVP[0]))
	for _, code := range []uint32{avp.OriginHost, avp.OriginRealm, avp.AuthApplicationID, avp.CCRequestType, avp.CCRequestNumber} {
		_, err := cca.FindAVP(code, 0)
		assert.NoError(t, err, "AVP %d", code)
	}
	host, _ := cca.FindAVP(avp.OriginHost, 0)
	assert.Equal(t, "gw.example", diameter.AVPString(host))
}
