// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/natstest"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/service"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const appS6a = 16777251 // TS 29.272 §7.1.8

// peerAuthGateway starts the gateway as cmd/diameter-gateway wires it — S6a
// forward and reverse services over a real NATS server — with a whitelist
// admitting only mme.allowed.com from 127.0.0.1.
func peerAuthGateway(t *testing.T, addr string) (*diameter.PeerManager, *nats.Conn, *atomic.Int32) {
	t.Helper()
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	// The HSS: counts the ULRs that reach it and accepts them.
	app, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(app.Close)
	var ulrs atomic.Int32
	_, err = app.QueueSubscribe("diameter.s6a.ulr", "hss", func(m *nats.Msg) {
		ulrs.Add(1)
		_ = m.Respond([]byte(`{"result_code":2001}`))
	})
	require.NoError(t, err)
	require.NoError(t, app.Flush())

	cfg := &config.Config{
		Server: config.ServerConfig{ListenAddr: addr, OriginHost: "gw.test.com", OriginRealm: "test.com", ProductName: "TestGateway"},
		Peers:  []config.PeerConfig{{IP: "127.0.0.1", Name: "mme.allowed.com"}},
	}
	pm := diameter.NewPeerManagerWith(diameter.PeerManagerOptions{OriginHost: "gw.test.com", OriginRealm: "test.com"})
	gw := service.NewGatewayService(nc, pm, "gw.test.com", "test.com")
	rs := service.NewReverseService(nc, pm, cfg)
	rs.StartListening()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = diameter.NewServer(cfg, gw, rs, pm).Start(ctx) }()
	time.Sleep(500 * time.Millisecond)
	return pm, app, &ulrs
}

func cer(originHost string) *diam.Message {
	m := diam.NewRequest(diam.CapabilitiesExchange, 0, dict.Default)
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	m.NewAVP(avp.HostIPAddress, avp.Mbit, 0, datatype.Address(net.ParseIP("127.0.0.1")))
	m.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415))
	m.NewAVP(avp.ProductName, 0, 0, datatype.UTF8String("TestMME"))
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appS6a))
	return m
}

func ulr(originHost string) *diam.Message {
	m := diam.NewRequest(diam.UpdateLocation, appS6a, dict.Default)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(originHost+";ulr;1"))
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("001010000000001"))
	m.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004))
	m.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0))
	m.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x00, 0xf1, 0x10}))
	return m
}

// TestPeerAuth_UnlistedPeerIsDisconnected: a peer outside the whitelist gets
// no CEA, its connection is closed, and a ULR it sends never reaches the HSS.
// Up to 0.10.2 the state machine refused the gateway's CER handler, so every
// peer was accepted and served.
func TestPeerAuth_UnlistedPeerIsDisconnected(t *testing.T) {
	pm, _, ulrs := peerAuthGateway(t, "127.0.0.1:3890")

	c, err := net.Dial("tcp", "127.0.0.1:3890")
	require.NoError(t, err)
	defer c.Close()
	_, err = cer("mme.rogue.com").WriteTo(c)
	require.NoError(t, err)
	_, _ = ulr("mme.rogue.com").WriteTo(c) // may already fail: the gateway is closing

	require.NoError(t, c.SetReadDeadline(time.Now().Add(2*time.Second)))
	answer, err := diam.ReadMessage(c, dict.Default)
	require.Error(t, err, "no CEA for an unlisted peer, got %v", answer)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the connection was left open")
	}

	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, ulrs.Load(), "the ULR must not reach the HSS")
	assert.Empty(t, pm.GetActiveConnections())
}

// TestPeerAuth_ListedPeerIsRegistered: a whitelisted peer passes CER, is
// registered under its Origin-Host, and receives the requests applications
// send it — here a CLR, answered 2001 back to the application.
func TestPeerAuth_ListedPeerIsRegistered(t *testing.T) {
	pm, app, ulrs := peerAuthGateway(t, "127.0.0.1:3891")

	mux := sm.New(&sm.Settings{
		OriginHost:  datatype.DiameterIdentity("mme.allowed.com"),
		OriginRealm: datatype.DiameterIdentity("test.com"),
		ProductName: datatype.UTF8String("TestMME"),
	})
	ulas := make(chan *diam.Message, 1)
	mux.HandleFunc("ULA", func(_ diam.Conn, m *diam.Message) { ulas <- m })
	mux.HandleFunc("CLR", func(c diam.Conn, m *diam.Message) {
		a := m.Answer(diam.Success)
		if sid, err := m.FindAVP(avp.SessionID, 0); err == nil {
			a.InsertAVP(sid)
		}
		a.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
		a.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.allowed.com"))
		a.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
		_, _ = a.WriteTo(c)
	})
	cli := &sm.Client{
		Dict: dict.Default, Handler: mux, MaxRetransmits: 1, RetransmitInterval: time.Second,
		AuthApplicationID: []*diam.AVP{
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appS6a)),
		},
	}
	c, err := cli.Dial("127.0.0.1:3891")
	require.NoError(t, err, "CEA for a whitelisted peer")
	defer c.Close()

	require.Eventually(t, func() bool { return pm.GetConnection("mme.allowed.com") != nil },
		2*time.Second, 20*time.Millisecond, "peer registered at CER")

	_, err = ulr("mme.allowed.com").WriteTo(c)
	require.NoError(t, err)
	select {
	case <-ulas:
	case <-time.After(3 * time.Second):
		t.Fatal("no ULA")
	}
	assert.Equal(t, int32(1), ulrs.Load())

	reply, err := app.Request("diameter.s6a.clr",
		[]byte(`{"header":{"source_host":"mme.allowed.com","dest_realm":"test.com"},"imsi":"001010000000001"}`),
		5*time.Second)
	require.NoError(t, err)
	var answer struct {
		ResultCode uint32 `json:"result_code"`
	}
	require.NoError(t, json.Unmarshal(reply.Data, &answer))
	assert.Equal(t, uint32(diam.Success), answer.ResultCode, "the CLR reached the MME: %s", reply.Data)
}
