// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"encoding/json"
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

// TestS13_EndToEnd sends ME-Identity-Check requests over TCP through the
// gateway to an EIR answering on NATS, and checks the ECA the MME receives.
func TestS13_EndToEnd(t *testing.T) {
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	defer nc.Close()

	// The EIR: IMEI 35293006225621 is prohibited, anything else unknown (5422).
	eir, err := nats.Connect(url)
	require.NoError(t, err)
	defer eir.Close()
	_, err = eir.QueueSubscribe("diameter.s13.ecr", "eir", func(m *nats.Msg) {
		var ev struct {
			TerminalInfo struct {
				IMEI string `json:"imei"`
			} `json:"terminal_info"`
		}
		_ = json.Unmarshal(m.Data, &ev)
		reply := `{"experimental_result_code":5422}`
		if ev.TerminalInfo.IMEI == "35293006225621" {
			reply = `{"result_code":2001,"equipment_status":1}`
		}
		_ = m.Respond([]byte(reply))
	})
	require.NoError(t, err)
	require.NoError(t, eir.Flush())

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr: "127.0.0.1:3874", OriginHost: "gw.test.com", OriginRealm: "test.com", ProductName: "TestGateway",
		},
		Peers: []config.PeerConfig{{IP: "127.0.0.1", Name: "mme.client.com"}},
	}
	pm := diameter.NewPeerManagerWith(diameter.PeerManagerOptions{OriginHost: "gw.test.com", OriginRealm: "test.com"})
	gw := service.NewGatewayService(nc, pm, "gw.test.com", "test.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = diameter.NewServer(cfg, gw, nil, pm).Start(ctx) }()
	time.Sleep(500 * time.Millisecond)

	answers := make(chan *diam.Message, 2)
	mux := sm.New(&sm.Settings{
		OriginHost:  datatype.DiameterIdentity("mme.client.com"),
		OriginRealm: datatype.DiameterIdentity("test.com"),
		ProductName: datatype.UTF8String("TestClient"),
	})
	mux.HandleIdx(diam.CommandIndex{AppID: diameter.AppIDS13, Code: 324},
		diam.HandlerFunc(func(_ diam.Conn, m *diam.Message) { answers <- m }))
	cli := &sm.Client{
		Dict: dict.Default, Handler: mux, MaxRetransmits: 1, RetransmitInterval: time.Second,
		AuthApplicationID: []*diam.AVP{
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(diameter.AppIDS13)),
		},
	}
	c, err := cli.Dial("127.0.0.1:3874")
	require.NoError(t, err)
	defer c.Close()

	ecr := func(sid, imei string) *diam.Message {
		m := diam.NewRequest(324, diameter.AppIDS13, dict.Default)
		m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid))
		m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
		m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.client.com"))
		m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
		m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
		m.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String(imei)),
			diam.NewAVP(avp.SoftwareVersion, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("01")),
		}})
		return m
	}
	next := func() *diam.Message {
		select {
		case m := <-answers:
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("no ECA")
			return nil
		}
	}

	_, err = ecr("mme.client.com;s13;1", "35293006225621").WriteTo(c)
	require.NoError(t, err)
	eca := next()
	rc, exp := diameter.AnswerResult(eca)
	assert.Equal(t, uint32(2001), rc)
	assert.Zero(t, exp)
	status, err := eca.FindAVP(1445, 10415)
	require.NoError(t, err, "Equipment-Status")
	assert.Equal(t, datatype.Enumerated(1), status.Data, "PROHIBITEDLISTED")
	assert.Equal(t, "mme.client.com;s13;1", diameter.AVPString(eca.AVP[0]))
	host, err := eca.FindAVP(avp.OriginHost, 0)
	require.NoError(t, err)
	assert.Equal(t, "gw.test.com", diameter.AVPString(host))

	_, err = ecr("mme.client.com;s13;2", "490154203237518").WriteTo(c)
	require.NoError(t, err)
	eca = next()
	rc, exp = diameter.AnswerResult(eca)
	assert.Zero(t, rc, "no Result-Code with an Experimental-Result (TS 29.272 §7.4.3)")
	assert.Equal(t, uint32(5422), exp, "DIAMETER_ERROR_EQUIPMENT_UNKNOWN")
	_, err = eca.FindAVP(1445, 10415)
	assert.Error(t, err, "no Equipment-Status on an error answer")
}

// TestS13_AdvertisedInCEA: the state machine advertises the applications of
// dict.Default, which now include S13.
func TestS13_AdvertisedInCEA(t *testing.T) {
	var found bool
	for _, app := range sm.PrepareSupportedApps(dict.Default) {
		if app.ID == diameter.AppIDS13 {
			found = true
		}
	}
	assert.True(t, found)
}
