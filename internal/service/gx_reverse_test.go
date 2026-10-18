// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"encoding/json"
	"testing"
	"time"

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

// TestGxReverse_ConcurrentLateAndRedirect: a PCEF that does not answer
// delays no other session; a second RAR for a pending session is 3004; a
// late RAA to a timed-out RAR does not answer the next one; a 3006 RAA is
// followed to the Redirect-Host, whose answer is the reply.
func TestGxReverse_ConcurrentLateAndRedirect(t *testing.T) {
	url := natstest.Start(t, false)
	nc, err := internalNats.NewClient(config.NatsConfig{URL: url})
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	cfg := &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example",
		ReverseTimeout: "300ms", RedirectMaxDepth: 3}}
	pm := diameter.NewPeerManager()
	s := NewGxReverseService(nc, pm, gx.NewSessionRegistry(gx.RegistryOptions{}),
		diameter.NewRedirectCache(diameter.RedirectCacheOptions{}), cfg)
	s.StartListening()
	require.NoError(t, nc.Conn.Flush())
	app, err := nats.Connect(url)
	require.NoError(t, err)
	t.Cleanup(app.Close)

	hung, pgw, pgw2 := &diamtest.Conn{}, &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(hung, "hung.example")
	pm.Register(pgw, "pgw.example")
	pm.Register(pgw2, "pgw2.example")
	request := func(body string) <-chan uint32 {
		out := make(chan uint32, 1)
		go func() {
			msg, err := app.Request(gx.TopicGxRAR, []byte(body), 3*time.Second)
			if !assert.NoError(t, err) {
				close(out)
				return
			}
			var a struct {
				ResultCode uint32 `json:"result_code"`
			}
			assert.NoError(t, json.Unmarshal(msg.Data, &a))
			out <- a.ResultCode
		}()
		return out
	}

	stuck := request(`{"header":{"session_id":"h;1","origin_host":"hung.example"}}`)
	written(t, hung, 1)
	assert.Equal(t, uint32(diam.TooBusy), <-request(`{"header":{"session_id":"h;1","origin_host":"hung.example"}}`))

	first := request(`{"header":{"session_id":"p;1","origin_host":"pgw.example"}}`)
	rar1 := written(t, pgw, 1)
	assert.Equal(t, uint32(diam.UnableToDeliver), <-first, "timed out")
	assert.Equal(t, uint32(diam.UnableToDeliver), <-stuck)

	second := request(`{"header":{"session_id":"p;1","origin_host":"pgw.example"}}`)
	rar2 := written(t, pgw, 2)
	s.HandleRAA(pgw, answerTo(rar1, 2001, "pgw.example")) // late answer to the first
	redirect := answerTo(rar2, 3006, "pgw.example")
	redirect.Header.CommandFlags |= diam.ErrorFlag
	redirect.NewAVP(avp.RedirectHost, avp.Mbit, 0, datatype.DiameterURI("aaa://pgw2.example"))
	s.HandleRAA(pgw, redirect)
	rar3 := written(t, pgw2, 1)
	s.HandleRAA(pgw2, answerTo(rar3, 2001, "pgw2.example"))
	assert.Equal(t, uint32(2001), <-second)
}
