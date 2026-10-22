// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"context"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm/smpeer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPeerGate_UnknownMandatoryAVP: RFC 6733 §4.1 — a request with a
// top-level AVP that has the M bit and that the gateway does not know is
// answered 5001 DIAMETER_AVP_UNSUPPORTED with a Failed-AVP holding it
// (§7.1.5), before any handler; one without the M bit, or inside a
// Grouped AVP, is passed on.
func TestPeerGate_UnknownMandatoryAVP(t *testing.T) {
	s := &DiameterServer{cfg: &config.Config{Server: config.ServerConfig{OriginHost: "gw.example", OriginRealm: "example"}}}
	g := s.gate()
	var handled int
	g.next = diam.HandlerFunc(func(diam.Conn, *diam.Message) { handled++ })
	c := &diamtest.Conn{}
	c.SetContext(smpeer.NewContext(context.Background(), &smpeer.Metadata{}))

	req := s6aRequest(diam.UpdateLocation)
	req.NewAVP(65000, avp.Mbit|avp.Vbit, 99999, datatype.OctetString("x"))
	g.ServeDIAM(c, fromWire(t, req))
	assert.Equal(t, 0, handled, "not passed to the handler")
	written, err := c.Messages()
	require.NoError(t, err)
	require.Len(t, written, 1)
	rc, _ := AnswerResult(written[0])
	assert.Equal(t, uint32(diam.AVPUnsupported), rc)
	assert.Equal(t, "mme.example;1", AVPString(written[0].AVP[0]), "Session-Id first")
	failed, err := written[0].FindAVP(avp.FailedAVP, 0)
	require.NoError(t, err)
	kids := failed.Data.(*diam.GroupedAVP).AVP
	require.Len(t, kids, 1)
	assert.Equal(t, [2]uint32{65000, 99999}, [2]uint32{kids[0].Code, kids[0].VendorID})

	for name, m := range map[string]*diam.Message{
		"no M bit": func() *diam.Message {
			r := s6aRequest(diam.UpdateLocation)
			r.NewAVP(65000, avp.Vbit, 99999, datatype.OctetString("x"))
			return r
		}(),
		"inside a group": func() *diam.Message {
			r := s6aRequest(diam.UpdateLocation)
			r.NewAVP(avp.ProxyInfo, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
				diam.NewAVP(avp.ProxyHost, avp.Mbit, 0, datatype.DiameterIdentity("dra.example")),
				diam.NewAVP(65001, avp.Mbit, 0, datatype.OctetString("y")),
			}})
			return r
		}(),
	} {
		before := handled
		g.ServeDIAM(c, fromWire(t, m))
		assert.Equal(t, before+1, handled, name)
	}

	// A request of an application the gateway does not handle is left to
	// the state machine (3001/3007, handleUnsupported).
	g.serves = func(app uint32) bool { return app != 3 }
	acr := diam.NewRequest(diam.Accounting, 3, dict.Default)
	acr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("ctf;1"))
	acr.NewAVP(65000, avp.Mbit|avp.Vbit, 99999, datatype.OctetString("x"))
	before := handled
	g.ServeDIAM(c, fromWire(t, acr))
	assert.Equal(t, before+1, handled, "not answered 5001 by the gate")

	// Before the capabilities exchange the state machine drops the request.
	fresh := &diamtest.Conn{}
	g.ServeDIAM(fresh, fromWire(t, req))
	none, err := fresh.Messages()
	require.NoError(t, err)
	assert.Empty(t, none)
}
