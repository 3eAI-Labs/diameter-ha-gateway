// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A panic in a handler answers the request 5012 on the same connection
// instead of letting go-diameter close the connection.
func TestPeerGate_PanicAnswers5012(t *testing.T) {
	s := &DiameterServer{cfg: &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}}
	g := s.gate()
	g.next = diam.HandlerFunc(func(diam.Conn, *diam.Message) { panic("handler bug") })

	c := &diamtest.Conn{}
	req := s6aRequest(diam.UpdateLocation)
	require.NotPanics(t, func() { g.ServeDIAM(c, req) })

	written, err := c.Messages()
	require.NoError(t, err)
	require.Len(t, written, 1)
	rc, _ := AnswerResult(written[0])
	assert.Equal(t, uint32(diam.UnableToComply), rc)
	assert.Equal(t, "mme.example;1", AVPString(written[0].AVP[0]), "Session-Id first")
}

// An answer that panics its handler gets no reply: answers are not answered.
func TestPeerGate_PanicOnAnswerSendsNothing(t *testing.T) {
	s := &DiameterServer{cfg: &config.Config{}}
	g := s.gate()
	g.next = diam.HandlerFunc(func(diam.Conn, *diam.Message) { panic("handler bug") })

	c := &diamtest.Conn{}
	require.NotPanics(t, func() { g.ServeDIAM(c, s6aRequest(diam.UpdateLocation).Answer(diam.Success)) })
	written, err := c.Messages()
	require.NoError(t, err)
	assert.Empty(t, written)
}

// A registered peer cannot disconnect another by naming it in a DPR's
// Origin-Host: the DPR counts for the peer its connection registered as.
func TestHandleDPR_UnregistersTheConnectionsPeerOnly(t *testing.T) {
	pm := NewPeerManager()
	s := NewServer(&config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}, nil, nil, pm)
	a, b := &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(a, "mme-a.example")
	pm.Register(b, "mme-b.example")

	s.handleDPR(a, NewTranslator().CreateDPR("mme-b.example", "example", 0))

	assert.Same(t, b, pm.GetConnection("mme-b.example"), "the named peer stays registered")
	assert.Nil(t, pm.GetConnection("mme-a.example"), "the sender is the one that disconnects")
	written, err := a.Messages()
	require.NoError(t, err)
	require.Len(t, written, 1, "DPA on the sender's connection")
	assert.Equal(t, uint32(diam.DisconnectPeer), written[0].Header.CommandCode)
}

func TestPeerGate_PanicValueNotLogged(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, nil)))
	defer slog.SetDefault(prev)

	s := &DiameterServer{cfg: &config.Config{}}
	g := s.gate()
	g.next = diam.HandlerFunc(func(diam.Conn, *diam.Message) { panic("vector 0123456789abcdef") })
	g.ServeDIAM(&diamtest.Conn{}, s6aRequest(diam.AuthenticationInformation))

	assert.NotContains(t, logged.String(), "0123456789abcdef")
	assert.Contains(t, logged.String(), "panic of type string")
}
