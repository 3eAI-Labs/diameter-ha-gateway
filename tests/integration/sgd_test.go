// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/natstest"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/service"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/sms/sgd"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sgdGateway starts the gateway with S6c and SGd served, admitting
// mme.allowed.com from 127.0.0.1, and returns a NATS connection for the
// application side.
func sgdGateway(t *testing.T, addr string) (*diameter.PeerManager, *nats.Conn) {
	t.Helper()
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	app, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(app.Close)

	cfg := &config.Config{
		Server: config.ServerConfig{ListenAddr: addr, OriginHost: "gw.test.com", OriginRealm: "test.com", ProductName: "TestGateway"},
		Peers:  []config.PeerConfig{{IP: "127.0.0.1", Name: "mme.allowed.com"}},
	}
	pm := diameter.NewPeerManagerWith(diameter.PeerManagerOptions{OriginHost: "gw.test.com", OriginRealm: "test.com"})
	srv := diameter.NewServer(cfg, service.NewGatewayService(nc, pm, "gw.test.com", "test.com"),
		service.NewReverseService(nc, pm, cfg), pm)
	srv.SetSGdHandler(sgd.NewHandler(nc, pm, 2*time.Second))
	client := sgd.NewClient(nc, pm, cfg, 2*time.Second)
	require.NoError(t, client.StartListening())
	srv.SetSGdAnswerHandler(client)
	require.NoError(t, nc.Conn.Flush())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Start(ctx) }()
	time.Sleep(500 * time.Millisecond)
	return pm, app
}

func vsai(app uint32) *diam.AVP {
	return diam.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(app)),
	}})
}

// TestSGd_EndToEnd over a real connection, with the MME on the Diameter side
// and the SMS-IWMSC / SMS-GMSC on NATS:
//   - the MME's CER offering SGd (16777313) only is accepted;
//   - its OFR reaches diameter.sgd.ofr, the OFA comes back, and the
//     connection stays up for a second OFR;
//   - a TFR the application sends on diameter.sgd.tfr.request reaches the
//     MME as an SGd request, and the MME's 5550 (ABSENT_USER) reaches the
//     application.
//
// Up to 0.11.0 a CER offering only 16777313 was answered 5010, and any SGd
// message closed the connection.
func TestSGd_EndToEnd(t *testing.T) {
	pm, app := sgdGateway(t, "127.0.0.1:3894")

	var ofrs atomic.Int32
	_, err := app.Subscribe("diameter.sgd.ofr", func(m *nats.Msg) {
		var e sgd.OFREvent
		if json.Unmarshal(m.Data, &e) == nil && e.SCAddress == "905309999999" && e.UserIdentifier != nil &&
			e.UserIdentifier.MSISDN == "905551234567" && len(e.SMRPUI) == 2 {
			ofrs.Add(1)
		}
		_ = m.Respond([]byte(`{"result_code":2001}`))
	})
	require.NoError(t, err)
	require.NoError(t, app.Flush())

	mux := sm.New(&sm.Settings{
		OriginHost: "mme.allowed.com", OriginRealm: "test.com", ProductName: "TestMME", VendorID: 10415,
	})
	ofas := make(chan *diam.Message, 2)
	mux.HandleFunc("OFA", func(_ diam.Conn, m *diam.Message) { ofas <- m })
	tfrs := make(chan *diam.Message, 1)
	mux.HandleFunc("TFR", func(c diam.Conn, m *diam.Message) {
		tfrs <- m
		a := m.Answer(0)
		a.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(5550)),
		}})
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
		VendorSpecificApplicationID: []*diam.AVP{vsai(sgd.AppIDSGd)},
	}
	c, err := cli.Dial("127.0.0.1:3894")
	require.NoError(t, err, "CEA for a CER offering SGd")
	defer c.Close()
	require.Eventually(t, func() bool { return pm.GetConnection("mme.allowed.com") != nil },
		2*time.Second, 20*time.Millisecond)

	for i := 0; i < 2; i++ {
		ofr := diam.NewMessage(sgd.CommandOFR, diam.RequestFlag|diam.ProxiableFlag, sgd.AppIDSGd, 0, 0, dict.Default)
		ofr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.allowed.com;1;"+string(rune('a'+i))))
		ofr.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
		ofr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.allowed.com"))
		ofr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
		ofr.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
		ofr.NewAVP(3300, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x09, 0x35, 0x90, 0x99, 0x99, 0x99}))
		ofr.NewAVP(3102, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(701, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x09, 0x55, 0x15, 0x32, 0x54, 0x76})),
		}})
		ofr.NewAVP(3301, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x11, 0x22}))
		_, err = ofr.WriteTo(c)
		require.NoError(t, err)
		select {
		case ofa := <-ofas:
			rc, _ := diameter.AnswerResult(ofa)
			assert.Equal(t, uint32(2001), rc, "OFA %d", i)
		case <-time.After(3 * time.Second):
			t.Fatalf("no OFA %d: the connection was closed or the OFR not served", i)
		}
	}
	assert.Equal(t, int32(2), ofrs.Load(), "both OFRs reached the application, decoded")

	req, _ := json.Marshal(map[string]any{
		"imsi": "286011234567890", "sc_address": "905309999999", "sm_rp_ui": []byte{0x01},
		"mme_name": "mme.allowed.com", "mme_realm": "test.com",
	})
	reply, err := app.Request(sgd.TopicSGdTFRRequest, req, 5*time.Second)
	require.NoError(t, err)
	var ans struct {
		ResultCode uint32 `json:"result_code"`
	}
	require.NoError(t, json.Unmarshal(reply.Data, &ans), "%s", reply.Data)
	assert.Equal(t, uint32(5550), ans.ResultCode, "the MME's ABSENT_USER reaches the application: %s", reply.Data)

	select {
	case tfr := <-tfrs:
		assert.Equal(t, sgd.AppIDSGd, tfr.Header.ApplicationID)
		assert.NotZero(t, tfr.Header.CommandFlags&diam.ProxiableFlag)
		un, err := tfr.FindAVP(avp.UserName, 0)
		require.NoError(t, err, "TFR carries User-Name")
		assert.Equal(t, datatype.UTF8String("286011234567890"), un.Data)
	default:
		t.Fatal("the MME received no TFR")
	}
}
