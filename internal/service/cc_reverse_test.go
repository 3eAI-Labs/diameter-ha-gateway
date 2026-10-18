// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/gx"
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

type ccRig struct {
	s        *CCReverseService
	pm       *diameter.PeerManager
	registry *gx.GxSessionRegistry
	app      *nats.Conn
}

func startCCReverse(t *testing.T) *ccRig { return startCCReverseTimeout(t, "2s") }

func startCCReverseTimeout(t *testing.T, timeout string) *ccRig {
	t.Helper()
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example", ReverseTimeout: timeout}}
	pm := diameter.NewPeerManager()
	registry := gx.NewSessionRegistry(gx.RegistryOptions{App: "cc"})
	pm.AddDropListener(registry.DropPeer)
	s := NewCCReverseService(nc, pm, registry, cfg)
	require.NoError(t, s.StartListening())
	require.NoError(t, nc.Conn.Flush())

	app, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return &ccRig{s: s, pm: pm, registry: registry, app: app}
}

// written waits for the n-th message the gateway writes on c.
func written(t *testing.T, c *diamtest.Conn, n int) *diam.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := c.Messages()
		require.NoError(t, err)
		if len(msgs) >= n {
			return msgs[n-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no message %d written", n)
	return nil
}

func answerTo(req *diam.Message, resultCode uint32, origin string) *diam.Message {
	a := diam.NewMessage(req.Header.CommandCode, diam.ProxiableFlag, req.Header.ApplicationID,
		req.Header.HopByHopID, req.Header.EndToEndID, nil)
	sid, _ := req.FindAVP(avp.SessionID, 0)
	a.AddAVP(sid)
	a.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(resultCode))
	a.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(origin))
	a.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example"))
	return a
}

// TestCCReverse_RAROverTheSessionsConnection: an OCS RAR on diameter.gy.rar
// goes to the peer the session is bound to, over its connection, whatever
// destination_host the OCS names; the client's RAA is the reply.
func TestCCReverse_RAROverTheSessionsConnection(t *testing.T) {
	r := startCCReverse(t)
	pgw, other := &diamtest.Conn{}, &diamtest.Conn{}
	r.pm.Register(pgw, "pgw.example")
	r.pm.Register(other, "other.example")
	charging.TrackSession(r.registry, charging.CCRequestTypeInitial, "pgw;1;2", "pgw.example")

	done := make(chan *nats.Msg, 1)
	go func() {
		msg, err := r.app.Request(TopicGyRAR, []byte(`{"header":{"session_id":"pgw;1;2","destination_host":"other.example"},"rating_group":0}`), 3*time.Second)
		assert.NoError(t, err)
		done <- msg
	}()

	rar := written(t, pgw, 1)
	assert.Equal(t, uint32(charging.CommandRAR), rar.Header.CommandCode)
	assert.Equal(t, charging.AppIDCreditControl, rar.Header.ApplicationID)
	dh, err := rar.FindAVP(avp.DestinationHost, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.DiameterIdentity("pgw.example"), dh.Data)

	r.s.HandleRAA(other, answerTo(rar, 2001, "other.example")) // not the session's connection
	r.s.HandleRAA(pgw, answerTo(rar, 2001, "pgw.example"))

	msg := <-done
	require.NotNil(t, msg)
	var ans charging.AbortAnswer
	require.NoError(t, json.Unmarshal(msg.Data, &ans))
	assert.Equal(t, charging.AbortAnswer{ResultCode: 2001, OriginHost: "pgw.example", OriginRealm: "example"}, ans)
	m, _ := other.Messages()
	assert.Empty(t, m, "nothing sent to the named host")
}

// TestCCReverse_ASRByDestinationHost: an unbound session (an EVENT, or after
// a restart) is reached through destination_host.
func TestCCReverse_ASRByDestinationHost(t *testing.T) {
	r := startCCReverse(t)
	ctf := &diamtest.Conn{}
	r.pm.Register(ctf, "ctf.example")

	done := make(chan *nats.Msg, 1)
	go func() {
		msg, err := r.app.Request(TopicRoASR, []byte(`{"header":{"session_id":"ctf;9","destination_host":"ctf.example"}}`), 3*time.Second)
		assert.NoError(t, err)
		done <- msg
	}()
	asr := written(t, ctf, 1)
	assert.Equal(t, uint32(charging.CommandASR), asr.Header.CommandCode)
	r.s.HandleASA(ctf, answerTo(asr, 2001, "ctf.example"))

	var ans charging.AbortAnswer
	require.NoError(t, json.Unmarshal((<-done).Data, &ans))
	assert.Equal(t, uint32(2001), ans.ResultCode)
}

// TestCCReverse_Refusals: no Session-Id is 5005; no connected client is
// 3002; a peer dropping mid-request fails it at once, with its bindings.
func TestCCReverse_Refusals(t *testing.T) {
	r := startCCReverse(t)
	reply := func(subject, body string) charging.AbortAnswer {
		msg, err := r.app.Request(subject, []byte(body), 3*time.Second)
		require.NoError(t, err)
		var a charging.AbortAnswer
		require.NoError(t, json.Unmarshal(msg.Data, &a))
		return a
	}
	assert.Equal(t, uint32(diam.MissingAVP), reply(TopicGyRAR, `{"header":{}}`).ResultCode)
	assert.Equal(t, uint32(diam.UnableToDeliver), reply(TopicRoRAR, `{"header":{"session_id":"x","destination_host":"nobody"}}`).ResultCode)
	assert.Equal(t, uint32(diam.InvalidAVPValue), reply(TopicGyASR, `not json`).ResultCode)

	pgw := &diamtest.Conn{}
	r.pm.Register(pgw, "pgw.example")
	charging.TrackSession(r.registry, charging.CCRequestTypeInitial, "pgw;1;2", "pgw.example")
	done := make(chan charging.AbortAnswer, 1)
	go func() { done <- reply(TopicGyASR, `{"header":{"session_id":"pgw;1;2"}}`) }()
	written(t, pgw, 1)
	start := time.Now()
	r.pm.Unregister("pgw.example")
	a := <-done
	assert.Equal(t, uint32(diam.UnableToDeliver), a.ResultCode)
	assert.Less(t, time.Since(start), time.Second, "failed at the drop, not the timeout")
	_, bound := r.registry.Lookup("pgw;1;2")
	assert.False(t, bound)
}

// TestCCReverse_ConcurrentAndLate: a client that does not answer delays no
// other session; a second RAR for a pending session is 3004; a late RAA to
// a timed-out RAR does not answer the RAR sent after it; an answer under
// another command does not answer the request.
func TestCCReverse_ConcurrentAndLate(t *testing.T) {
	r := startCCReverseTimeout(t, "300ms")
	hung, pgw := &diamtest.Conn{}, &diamtest.Conn{}
	r.pm.Register(hung, "hung.example")
	r.pm.Register(pgw, "pgw.example")
	request := func(subject, body string) <-chan charging.AbortAnswer {
		out := make(chan charging.AbortAnswer, 1)
		go func() {
			msg, err := r.app.Request(subject, []byte(body), 3*time.Second)
			if !assert.NoError(t, err) {
				close(out)
				return
			}
			var a charging.AbortAnswer
			assert.NoError(t, json.Unmarshal(msg.Data, &a))
			out <- a
		}()
		return out
	}

	stuck := request(TopicGyRAR, `{"header":{"session_id":"h;1","destination_host":"hung.example"}}`)
	written(t, hung, 1)
	assert.Equal(t, uint32(diam.TooBusy),
		(<-request(TopicGyRAR, `{"header":{"session_id":"h;1","destination_host":"hung.example"}}`)).ResultCode)

	first := request(TopicGyRAR, `{"header":{"session_id":"p;1","destination_host":"pgw.example"}}`)
	rar1 := written(t, pgw, 1)
	assert.Equal(t, uint32(diam.UnableToDeliver), (<-first).ResultCode, "timed out")
	assert.Equal(t, uint32(diam.UnableToDeliver), (<-stuck).ResultCode)

	second := request(TopicGyRAR, `{"header":{"session_id":"p;1","destination_host":"pgw.example"}}`)
	rar2 := written(t, pgw, 2)
	require.NotEqual(t, rar1.Header.HopByHopID, rar2.Header.HopByHopID)
	r.s.HandleRAA(pgw, answerTo(rar1, 2001, "pgw.example")) // late answer to the first
	asa := answerTo(rar2, 2001, "pgw.example")
	asa.Header.CommandCode = charging.CommandASR
	r.s.HandleASA(pgw, asa) // another command
	r.s.HandleRAA(pgw, answerTo(rar2, 5031, "pgw.example"))
	assert.Equal(t, uint32(5031), (<-second).ResultCode)

	assert.Equal(t, uint32(diam.InvalidAVPValue),
		(<-request(TopicRoRAR, `{"header":{"session_id":"p;1","destination_host":"pgw.example"},"re_auth_request_type":2}`)).ResultCode)
}

// TestInflight_Bounded: past maxInFlight waiting requests, a request is
// refused at once rather than started.
func TestInflight_Bounded(t *testing.T) {
	f := make(inflight, 2)
	release := make(chan struct{})
	var busy int
	h := f.wrap(func(*nats.Msg) { <-release }, func(*nats.Msg) { busy++ })
	for i := 0; i < 3; i++ {
		h(&nats.Msg{})
	}
	assert.Equal(t, 1, busy)
	close(release)
	require.Eventually(t, func() bool { return len(f) == 0 }, time.Second, 5*time.Millisecond)
	h(&nats.Msg{})
	assert.Equal(t, 1, busy, "a slot freed is reused")
}
