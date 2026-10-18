// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"context"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"github.com/fiorix/go-diameter/v4/diam/sm/smpeer"
	"github.com/stretchr/testify/assert"
)

type answerLog struct {
	name string
	got  *[]string
}

func (a answerLog) HandleRAA(diam.Conn, *diam.Message) { *a.got = append(*a.got, a.name+" RAA") }
func (a answerLog) HandleASA(diam.Conn, *diam.Message) { *a.got = append(*a.got, a.name+" ASA") }

// TestReverseAnswers_ByApplicationID: the go-diameter mux keys on the
// command name, under which a Gy/Ro RAA (Application-Id 4) is a Gx RAA.
// Each goes to its own handler; an RAA of another application to neither.
func TestReverseAnswers_ByApplicationID(t *testing.T) {
	var got []string
	s := &DiameterServer{
		cfg:             &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}},
		gxAnswerHandler: answerLog{"gx", &got},
		ccAnswerHandler: answerLog{"cc", &got},
	}
	s.stateMachine = sm.New(&sm.Settings{OriginHost: "gw.example", OriginRealm: "example",
		VendorID: 10415, ProductName: datatype.UTF8String("test")})
	s.stateMachine.HandleFunc("ALL", s.handleUnsupported)
	s.registerReverseAnswers()

	c := &diamtest.Conn{}
	c.SetContext(smpeer.NewContext(context.Background(), &smpeer.Metadata{}))
	for _, m := range []*diam.Message{
		diam.NewMessage(diam.ReAuth, 0, 4, 1, 1, dict.Default),
		diam.NewMessage(diam.ReAuth, 0, gxApplicationID, 2, 2, dict.Default),
		diam.NewMessage(diam.AbortSession, 0, 4, 3, 3, dict.Default),
		diam.NewMessage(diam.AbortSession, 0, gxApplicationID, 4, 4, dict.Default),
		diam.NewMessage(diam.ReAuth, 0, 16777251, 5, 5, dict.Default),
	} {
		s.stateMachine.ServeDIAM(c, m)
	}
	assert.Equal(t, []string{"cc RAA", "gx RAA", "cc ASA", "gx ASA"}, got)
	written, _ := c.Messages()
	assert.Empty(t, written, "an answer is not answered")
}
