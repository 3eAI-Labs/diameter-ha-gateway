// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/natstest"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGateway_RequestOriginHostDoesNotRebindPeer: an authorised peer that
// writes another MME's identity into a request's Origin-Host must not take
// over that MME's connection, or the application's CLR/IDR for it would be
// sent to the impostor.
func TestGateway_RequestOriginHostDoesNotRebindPeer(t *testing.T) {
	nc, err := internalNats.NewClient(config.NatsConfig{URL: natstest.Start(t, false)})
	require.NoError(t, err)
	defer nc.Close()

	pm := diameter.NewPeerManager()
	victim, impostor := &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(victim, "mme.test.3gppnetwork.org") // buildTestULR's Origin-Host
	pm.Register(impostor, "mme-other.example")

	ulr := buildTestULR()
	ulr.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	ulr.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004))
	ulr.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0))
	ulr.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x62, 0xf2, 0x10}))

	g := NewGatewayService(nc, pm, "gw.example", "example")
	g.HandleULR(impostor, ulr)

	// The request got past validation to the NATS request (no responder: 3002).
	answers, err := impostor.Messages()
	require.NoError(t, err)
	require.Len(t, answers, 1)
	rc, _ := diameter.AnswerResult(answers[0])
	require.Equal(t, uint32(3002), rc)

	assert.Same(t, victim, pm.GetConnection("mme.test.3gppnetwork.org"))
	assert.Same(t, impostor, pm.GetConnection("mme-other.example"))
}

// TestDeliverAnswer_OnlyFromTheRequestsConnection: the Session-Id of a
// reverse request is predictable, so an answer is only delivered when it
// arrives on the connection the request was sent on.
func TestDeliverAnswer_OnlyFromTheRequestsConnection(t *testing.T) {
	rs := &ReverseService{pendingReqs: map[string]*pendingReq{}, translator: diameter.NewTranslator()}
	_, sid := rs.translator.CreateCLR("001010000000001", "mme.example", "example", "gw.example", "example")
	mme, other := &diamtest.Conn{}, &diamtest.Conn{}
	ch := make(chan *diam.Message, 1)
	rs.pendingReqs[sid] = &pendingReq{ch: ch, peerHost: "mme.example", conn: mme}

	cla := diam.NewMessage(317, 0, 16777251, 1, 2, nil)
	cla.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid))

	rs.HandleCLA(other, cla)
	assert.Empty(t, ch, "answer from another connection delivered")
	assert.Contains(t, rs.pendingReqs, sid, "request dropped by a spoofed answer")

	rs.HandleCLA(mme, cla)
	assert.Len(t, ch, 1)
}

func TestGxDeliverAnswer_OnlyFromTheRequestsConnection(t *testing.T) {
	s := &GxReverseService{pendingReqs: map[string]*gxPendingReq{}}
	pcef, other := &diamtest.Conn{}, &diamtest.Conn{}
	ch := make(chan *diam.Message, 1)
	s.pendingReqs["gx-1"] = &gxPendingReq{ch: ch, peerHost: "pgw.example", verb: "RAR", conn: pcef}

	raa := diam.NewMessage(258, 0, 16777238, 1, 2, nil)
	raa.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("gx-1"))

	s.HandleRAA(other, raa)
	assert.Empty(t, ch, "answer from another connection delivered")
	s.HandleRAA(pcef, raa)
	assert.Len(t, ch, 1)
}

// TestGateway_ULACarriesMandatoryAVPs: an answer carries the request's
// Session-Id, the gateway's Origin-Host and Origin-Realm and the request's
// Auth-Session-State (TS 29.272 §7.2.4, RFC 6733 §6.3, §6.4, §8.8). It used
// to carry the Result-Code alone.
func TestGateway_ULACarriesMandatoryAVPs(t *testing.T) {
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	defer nc.Close()
	hss, err := nats.Connect(url)
	require.NoError(t, err)
	defer hss.Close()
	_, err = hss.QueueSubscribe("diameter.s6a.ulr", "hss", func(m *nats.Msg) { _ = m.Respond([]byte(`{"result_code":2001}`)) })
	require.NoError(t, err)
	require.NoError(t, hss.Flush())

	pm := diameter.NewPeerManagerWith(diameter.PeerManagerOptions{OriginHost: "gw.example", OriginRealm: "example"})
	mme := &diamtest.Conn{}
	pm.Register(mme, "mme.test.3gppnetwork.org")
	ulr := buildTestULR()
	ulr.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	ulr.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004))
	ulr.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0))
	ulr.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x62, 0xf2, 0x10}))

	NewGatewayService(nc, pm, "gw.example", "example").HandleULR(mme, ulr)

	answers, err := mme.Messages()
	require.NoError(t, err)
	require.Len(t, answers, 1)
	ula := answers[0]
	assert.Equal(t, uint32(avp.SessionID), ula.AVP[0].Code)
	assert.Equal(t, "test-session-ulr", diameter.AVPString(ula.AVP[0]))
	for code, want := range map[uint32]string{avp.OriginHost: "gw.example", avp.OriginRealm: "example"} {
		a, err := ula.FindAVP(code, 0)
		require.NoError(t, err, code)
		assert.Equal(t, want, diameter.AVPString(a))
	}
	_, err = ula.FindAVP(avp.AuthSessionState, 0)
	assert.NoError(t, err)
	rc, _ := diameter.AnswerResult(ula)
	assert.Equal(t, uint32(2001), rc)
}
