// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"encoding/json"
	"testing"
	"time"

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

// TestStartListening_ReplicasShareRequests: with several gateway replicas an
// SMSC request is handled by one of them, so it reaches the Diameter network
// once. Without a queue group every replica handled it. No HSS peer is
// connected here, so the replica that takes the request answers 3002.
func TestStartListening_ReplicasShareRequests(t *testing.T) {
	url := natstest.Start(t, false)
	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}
	for i := 0; i < 2; i++ {
		nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		require.NoError(t, NewClient(nc, diameter.NewPeerManager(), cfg, time.Second).StartListening())
		require.NoError(t, nc.Conn.Flush())
	}

	smsc, err := nats.Connect(url)
	require.NoError(t, err)
	defer smsc.Close()
	inbox := nats.NewInbox()
	replies, err := smsc.SubscribeSync(inbox)
	require.NoError(t, err)
	req, _ := json.Marshal(SRRRequest{outbound: outbound{TraceID: "t1"}, MSISDN: "905551234567", SCAddress: "905309999999"})
	require.NoError(t, smsc.PublishRequest(TopicSGdSRRRequest, inbox, req))

	first, err := replies.NextMsg(time.Second)
	require.NoError(t, err)
	var ans map[string]interface{}
	require.NoError(t, json.Unmarshal(first.Data, &ans), "reply %s", first.Data)
	assert.EqualValues(t, 3002, ans["result_code"])

	_, err = replies.NextMsg(300 * time.Millisecond)
	assert.ErrorIs(t, err, nats.ErrTimeout, "a second replica handled the same request")
}

// TestHandleSRR_MalformedRequestIsNotUserUnknown: a request the gateway cannot
// parse says so (5004), instead of claiming the subscriber is unknown (5001).
func TestHandleSRR_MalformedRequestIsNotUserUnknown(t *testing.T) {
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	defer nc.Close()
	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}
	require.NoError(t, NewClient(nc, diameter.NewPeerManager(), cfg, time.Second).StartListening())
	require.NoError(t, nc.Conn.Flush())

	smsc, err := nats.Connect(url)
	require.NoError(t, err)
	defer smsc.Close()
	msg, err := smsc.Request(TopicSGdSRRRequest, []byte(`not json`), time.Second)
	require.NoError(t, err)
	var ans map[string]interface{}
	require.NoError(t, json.Unmarshal(msg.Data, &ans))
	assert.EqualValues(t, 5004, ans["result_code"])
}

// TestDeliver_OnlyFromTheRequestsConnection: an S6c/SGd answer is delivered
// only when it arrives on the connection the request was sent on and
// answers the request's command.
func TestDeliver_OnlyFromTheRequestsConnection(t *testing.T) {
	c := &Client{pendingReqs: map[string]*pendingReq{}}
	hss, other := &diamtest.Conn{}, &diamtest.Conn{}
	ch := make(chan *diam.Message, 1)
	c.pendingReqs["sgd-1"] = &pendingReq{ch: ch, conn: hss, code: CommandSRR}

	sra := diam.NewMessage(CommandSRR, 0, AppIDS6c, 1, 2, nil)
	sra.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("sgd-1"))

	c.HandleSRA(other, sra)
	assert.Empty(t, ch, "answer from another connection delivered")
	rda := diam.NewMessage(CommandRDR, 0, AppIDS6c, 1, 2, nil)
	rda.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("sgd-1"))
	c.HandleRDA(hss, rda)
	assert.Empty(t, ch, "an answer of another command delivered")
	c.HandleSRA(hss, sra)
	assert.Len(t, ch, 1)
}

// TestClient_RejectsIncompleteRequests: a request that would leave a
// mandatory AVP out is answered 5005 and not sent; no peer is needed to
// find out.
func TestClient_RejectsIncompleteRequests(t *testing.T) {
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	defer nc.Close()
	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}
	require.NoError(t, NewClient(nc, diameter.NewPeerManager(), cfg, time.Second).StartListening())
	require.NoError(t, nc.Conn.Flush())
	app, err := nats.Connect(url)
	require.NoError(t, err)
	defer app.Close()

	for subject, body := range map[string]string{
		TopicSGdSRRRequest: `{"sc_address":"905309999999"}`,
		TopicSGdRDRRequest: `{"msisdn":"905551234567","sc_address":"905309999999","sm_delivery_outcome":{}}`,
		TopicSGdALRRequest: `{"user_identifier":{},"sc_address":"905309999999"}`,
		TopicSGdTFRRequest: `{"imsi":"1","sc_address":"x","sm_rp_ui":"AQ==","mme_name":"mme"}`,
	} {
		msg, err := app.Request(subject, []byte(body), time.Second)
		require.NoError(t, err, subject)
		var ans Result
		require.NoError(t, json.Unmarshal(msg.Data, &ans))
		assert.Equal(t, uint32(diam.MissingAVP), ans.ResultCode, "%s: %s", subject, msg.Data)
	}
}

// TestClient_SilentPeerDelaysNoOtherRequest: a subscription's callbacks run
// one at a time; an HSS that does not answer must not hold up an SRR to
// another HSS until the first one times out.
func TestClient_SilentPeerDelaysNoOtherRequest(t *testing.T) {
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	defer nc.Close()
	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}
	pm := diameter.NewPeerManager()
	hung, hss := &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(hung, "hung.example")
	pm.Register(hss, "hss.example")
	c := NewClient(nc, pm, cfg, 5*time.Second)
	require.NoError(t, c.StartListening())
	require.NoError(t, nc.Conn.Flush())
	app, err := nats.Connect(url)
	require.NoError(t, err)
	defer app.Close()

	writtenTo := func(conn *diamtest.Conn) *diam.Message {
		var msgs []*diam.Message
		require.Eventually(t, func() bool {
			msgs, _ = conn.Messages()
			return len(msgs) == 1
		}, 2*time.Second, 5*time.Millisecond)
		return msgs[0]
	}
	go app.Request(TopicSGdSRRRequest, []byte(`{"msisdn":"905551234567","sc_address":"905309999999","destination_host":"hung.example"}`), 6*time.Second)
	writtenTo(hung)

	start := time.Now()
	done := make(chan *nats.Msg, 1)
	go func() {
		msg, err := app.Request(TopicSGdSRRRequest, []byte(`{"msisdn":"905551234568","sc_address":"905309999999","destination_host":"hss.example"}`), 6*time.Second)
		assert.NoError(t, err)
		done <- msg
	}()
	srr := writtenTo(hss)
	sra := diam.NewMessage(srr.Header.CommandCode, 0, srr.Header.ApplicationID, srr.Header.HopByHopID, srr.Header.EndToEndID, nil)
	sid, err := srr.FindAVP(avp.SessionID, 0)
	require.NoError(t, err)
	sra.AddAVP(sid)
	sra.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(2001))
	c.HandleSRA(hss, sra)
	msg := <-done
	require.NotNil(t, msg)
	var ans map[string]interface{}
	require.NoError(t, json.Unmarshal(msg.Data, &ans))
	assert.EqualValues(t, 2001, ans["result_code"])
	assert.Less(t, time.Since(start), 2*time.Second, "waited for the silent HSS")
}
