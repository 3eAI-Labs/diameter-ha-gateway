// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"bytes"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	name string
	hit  *string
}

func (r recorder) HandleCCR(diam.Conn, *diam.Message) { *r.hit = r.name }

// ccr returns a CCR under appID carrying every AVP the dispatcher requires
// except the one coded omit, and scid as Service-Context-Id unless it is empty.
func ccr(appID uint32, scid string, omit ...uint32) *diam.Message {
	m := diam.NewRequest(272, appID, dict.Default)
	defer func() {
		for _, code := range omit {
			for i, a := range m.AVP {
				if a.Code == code {
					m.AVP = append(m.AVP[:i], m.AVP[i+1:]...)
					m.Header.MessageLength -= uint32(a.Len())
					break
				}
			}
		}
	}()
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("pcef.example.com;1;2"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pcef.example.com"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appID))
	if scid != "" {
		m.NewAVP(avp.ServiceContextID, avp.Mbit, 0, datatype.UTF8String(scid))
	}
	m.NewAVP(avp.CCRequestType, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(avp.CCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(0))
	return m
}

// TestDispatcher_RoutesByApplicationAndServiceContextID: a Gx CCR is
// recognised by its Application-Id, with or without a Service-Context-Id
// (TS 29.212 gives it none); a Gy or Ro CCR by its Service-Context-Id, also
// when that carries a release or operator prefix (TS 32.299 §7.1.12). Up to
// 0.10.3 a Gx CCR without Service-Context-Id was answered 5012, and so was
// every Gy/Ro CCR carrying a 3GPP value.
func TestDispatcher_RoutesByApplicationAndServiceContextID(t *testing.T) {
	var hit string
	d := NewDispatcher(recorder{"gx", &hit}, recorder{"gy", &hit}, recorder{"ro", &hit},
		[]string{"32251@3gpp.org"}, []string{"32260@3gpp.org", "32275@3gpp.org"},
		recorder{"default", &hit})

	for _, tc := range []struct {
		app  uint32
		scid string
		want string
	}{
		{AppIDGx, "", "gx"},
		{AppIDGx, "32251@3gpp.org", "gx"},
		{4, "32251@3gpp.org", "gy"},
		{4, "17.32251@3gpp.org", "gy"},
		{4, "ext.01.001.17.32251@3gpp.org", "gy"},
		{4, "32260@3gpp.org", "ro"},
		{4, "17.32275@3gpp.org", "ro"},
		{4, "32251@example.com", "default"},
		{4, "32274@3gpp.org", "default"},
		{4, "other", "default"},
	} {
		hit = ""
		d.HandleCCR(nil, ccr(tc.app, tc.scid))
		assert.Equal(t, tc.want, hit, "app %d scid %q", tc.app, tc.scid)
	}
}

// captureConn records what the dispatcher writes.
type captureConn struct {
	diam.Conn
	buf bytes.Buffer
}

func (c *captureConn) Write(b []byte) (int, error) { return c.buf.Write(b) }

// TestDispatcher_MissingAVPAnswered5005: a CCR without a mandatory AVP
// (RFC 8506 §3.1) is answered 5005 with a Failed-AVP whose child decodes with
// the AVP's own type, and reaches no handler. Up to 0.10.3 a Gy/Ro CCR
// without CC-Request-Type was answered 2001, one without Service-Context-Id
// 5012.
func TestDispatcher_MissingAVPAnswered5005(t *testing.T) {
	var hit string
	d := NewDispatcher(recorder{"gx", &hit}, recorder{"gy", &hit}, recorder{"ro", &hit},
		[]string{"32251@3gpp.org"}, nil, nil).WithIdentity("gw.example.com", "example.com")

	noType := ccr(4, "32251@3gpp.org", avp.CCRequestType)

	for name, tc := range map[string]struct {
		m    *diam.Message
		code uint32
		zero datatype.Type
	}{
		"no CC-Request-Type":    {noType, avp.CCRequestType, datatype.Enumerated(0)},
		"no Service-Context-Id": {ccr(4, ""), avp.ServiceContextID, datatype.UTF8String("")},
	} {
		hit = ""
		c := &captureConn{}
		d.HandleCCR(c, tc.m)
		assert.Empty(t, hit, name)

		ans, err := diam.ReadMessage(&c.buf, dict.Default)
		require.NoError(t, err, name)
		rc, err := ans.FindAVP(avp.ResultCode, 0)
		require.NoError(t, err, name)
		assert.Equal(t, datatype.Unsigned32(5005), rc.Data, name)
		failed, err := ans.FindAVP(avp.FailedAVP, 0)
		require.NoError(t, err, name)
		children := failed.Data.(*diam.GroupedAVP).AVP
		require.Len(t, children, 1, name)
		assert.Equal(t, tc.code, children[0].Code, name)
		assert.Equal(t, tc.zero, children[0].Data, name)
	}
}
