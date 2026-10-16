// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"encoding/json"
	"testing"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/natstest"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startReverse runs a ReverseService against an in-process NATS server and
// returns a plain connection for playing the application.
func startReverse(t *testing.T) (*ReverseService, *nats.Conn) {
	t.Helper()
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}
	rs := NewReverseService(nc, diameter.NewPeerManager(), cfg)
	rs.StartListening()
	require.NoError(t, nc.Conn.Flush())

	app, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return rs, app
}

// TestReverse_PURAndNORAreNotSubscribed: PUR and NOR travel MME → HSS only
// (TS 29.272 §7.2.13, §7.2.17), and diameter.s6a.pur/nor carry an MME's
// request to the application. The gateway must not also consume them, or an
// MME's PUR would be sent back towards the MME.
func TestReverse_PURAndNORAreNotSubscribed(t *testing.T) {
	_, app := startReverse(t)
	for _, subject := range []string{"diameter.s6a.pur", "diameter.s6a.nor"} {
		_, err := app.Request(subject, []byte(`{}`), time.Second)
		assert.ErrorIs(t, err, nats.ErrNoResponders, subject)
	}
}

// TestReverse_ReplyIsAJSONObject: a reverse request gets one reply, as the
// S6aAnswer object. A request without header.source_host is refused with 3002.
func TestReverse_ReplyIsAJSONObject(t *testing.T) {
	_, app := startReverse(t)
	for _, subject := range []string{"diameter.s6a.clr", "diameter.s6a.idr", "diameter.s6a.dsr", "diameter.s6a.rsr"} {
		msg, err := app.Request(subject, []byte(`{"imsi":"001010000000001"}`), time.Second)
		require.NoError(t, err, subject)
		var ans pb.S6aAnswer
		require.NoError(t, json.Unmarshal(msg.Data, &ans), "%s reply %s", subject, msg.Data)
		assert.Equal(t, uint32(3002), ans.ResultCode, subject)
	}
}

// TestReverse_ExperimentalResultReachesApplication: an MME answering with
// Experimental-Result 5001 and no Result-Code (TS 29.272 §7.4.3) is reported
// to the application as such, not as 2001.
func TestReverse_ExperimentalResultReachesApplication(t *testing.T) {
	rs, app := startReverse(t)

	inbox := nats.NewInbox()
	sub, err := app.SubscribeSync(inbox)
	require.NoError(t, err)
	require.NoError(t, app.Flush())

	cla := diam.NewMessage(317, 0, 16777251, 1, 2, nil)
	cla.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
		diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(5001)),
	}})
	rs.sendNatsResponse(&nats.Msg{Subject: "diameter.s6a.clr", Reply: inbox}, cla)

	msg, err := sub.NextMsg(time.Second)
	require.NoError(t, err)
	var ans pb.S6aAnswer
	require.NoError(t, json.Unmarshal(msg.Data, &ans))
	assert.Equal(t, pb.S6aAnswer{ExperimentalResultCode: 5001}, ans)
}
