// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CreateRAR / CreateASR are the wire builders for PCRF→PCEF reverse
// direction. Tests validate that the produced messages carry the right
// command code, the supplied Session-Id (NOT a freshly generated one), and
// the Gx-specific AVPs the spec mandates.

func TestCreateRAR_CommandCodeAndSessionID(t *testing.T) {
	tr := NewTranslator()
	req := &GxReAuthRequest{ReAuthRequestType: ReAuthTypeAuthorizeOnly}

	m := tr.CreateRAR("orig-session-42", "pcef-a.example.com", "pcef-realm",
		"gw.example.com", "gw-realm", req)

	assert.Equal(t, uint32(CommandRAR), m.Header.CommandCode, "RAR command code is 258")
	assert.Equal(t, uint32(AppIDGx), m.Header.ApplicationID, "Application-Id must be Gx (16777238)")

	sid, err := m.FindAVP(avp.SessionID, 0)
	require.NoError(t, err)
	assert.Equal(t, "orig-session-42", string(sid.Data.(datatype.UTF8String)),
		"Session-Id must be the supplied value, not a freshly generated one — RAR rides the existing PCEF session")
}

func TestCreateRAR_RoutingAVPs(t *testing.T) {
	tr := NewTranslator()
	req := &GxReAuthRequest{ReAuthRequestType: ReAuthTypeAuthorizeOnly}

	m := tr.CreateRAR("sess-1", "pcef-a", "pcef-realm", "gw", "gw-realm", req)

	oh, _ := m.FindAVP(avp.OriginHost, 0)
	assert.Equal(t, "gw", string(oh.Data.(datatype.DiameterIdentity)))
	or, _ := m.FindAVP(avp.OriginRealm, 0)
	assert.Equal(t, "gw-realm", string(or.Data.(datatype.DiameterIdentity)))
	dh, _ := m.FindAVP(avp.DestinationHost, 0)
	assert.Equal(t, "pcef-a", string(dh.Data.(datatype.DiameterIdentity)))
	dr, _ := m.FindAVP(avp.DestinationRealm, 0)
	assert.Equal(t, "pcef-realm", string(dr.Data.(datatype.DiameterIdentity)))
}

func TestCreateRAR_ReAuthRequestType(t *testing.T) {
	tr := NewTranslator()
	for _, tc := range []struct {
		name string
		typ  uint32
	}{
		{"AuthorizeOnly", ReAuthTypeAuthorizeOnly},
		{"AuthorizeAuthenticate", ReAuthTypeAuthorizeAuthenticate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &GxReAuthRequest{ReAuthRequestType: tc.typ}
			m := tr.CreateRAR("s", "p", "pr", "g", "gr", req)
			a := findRawAVP(t, m, 285, 0)
			assert.Equal(t, datatype.Enumerated(tc.typ), a.Data)
		})
	}
}

func TestCreateRAR_ChargingRules(t *testing.T) {
	tr := NewTranslator()
	req := &GxReAuthRequest{
		ReAuthRequestType: ReAuthTypeAuthorizeOnly,
		ChargingRuleInstall: []ChargingRuleInstall{
			{ChargingRuleName: []string{"video_premium", "voip_qos"}},
		},
		ChargingRuleRemove: []ChargingRuleRemove{
			{ChargingRuleName: []string{"throttle_bg"}},
		},
	}
	m := tr.CreateRAR("s", "p", "pr", "g", "gr", req)

	installs, _ := m.FindAVPs(avpChargingRuleInstall, vendorID3GPP)
	require.Len(t, installs, 1, "one Charging-Rule-Install grouped AVP")
	removes, _ := m.FindAVPs(avpChargingRuleRemove, vendorID3GPP)
	require.Len(t, removes, 1)
}

func TestCreateRAR_SessionReleaseCause(t *testing.T) {
	tr := NewTranslator()
	req := &GxReAuthRequest{
		ReAuthRequestType:   ReAuthTypeAuthorizeOnly,
		SessionReleaseCause: 1, // UE_SUBSCRIPTION_REASON
	}
	m := tr.CreateRAR("s", "p", "pr", "g", "gr", req)
	a := findRawAVP(t, m, 1045, vendorID3GPP)
	assert.Equal(t, datatype.Enumerated(1), a.Data)
}

func TestCreateRAR_SessionReleaseCauseOmittedWhenZero(t *testing.T) {
	tr := NewTranslator()
	req := &GxReAuthRequest{ReAuthRequestType: ReAuthTypeAuthorizeOnly}
	m := tr.CreateRAR("s", "p", "pr", "g", "gr", req)
	for _, a := range m.AVP {
		if a.Code == 1045 {
			t.Fatal("Session-Release-Cause AVP must be omitted when zero")
		}
	}
}

// ─── ASR ───────────────────────────────────────────────────────────────────

func TestCreateASR_CommandCodeAndSessionID(t *testing.T) {
	tr := NewTranslator()
	req := &GxAbortSessionRequest{AbortCause: AbortCauseUERemoved}

	m := tr.CreateASR("session-x", "pcef-a", "pcef-realm", "gw", "gw-realm", req)

	assert.Equal(t, uint32(CommandASR), m.Header.CommandCode, "ASR command code is 274")
	assert.Equal(t, uint32(AppIDGx), m.Header.ApplicationID)

	sid, err := m.FindAVP(avp.SessionID, 0)
	require.NoError(t, err)
	assert.Equal(t, "session-x", string(sid.Data.(datatype.UTF8String)))
}

func TestCreateASR_AbortCause(t *testing.T) {
	tr := NewTranslator()
	for _, tc := range []struct {
		name  string
		cause uint32
	}{
		{"BearerReleased", AbortCauseBearerReleased},
		{"UERemoved", AbortCauseUERemoved},
		{"InsufficientResource", AbortCauseInsufficientResource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &GxAbortSessionRequest{AbortCause: tc.cause}
			m := tr.CreateASR("s", "p", "pr", "g", "gr", req)
			a := findRawAVP(t, m, 500, vendorID3GPP)
			assert.Equal(t, datatype.Enumerated(tc.cause), a.Data,
				"Abort-Cause must be emitted explicitly per TS 29.212 §5.3.1")
		})
	}
}

func TestCreateASR_DefaultCauseEmittedExplicitly(t *testing.T) {
	tr := NewTranslator()
	// Even when AbortCause=0 (BEARER_RELEASED), the AVP MUST be present so
	// the PCEF sees an explicit cause rather than relying on its default.
	req := &GxAbortSessionRequest{}
	m := tr.CreateASR("s", "p", "pr", "g", "gr", req)
	a := findRawAVP(t, m, 500, vendorID3GPP)
	assert.Equal(t, datatype.Enumerated(0), a.Data,
		"explicit Abort-Cause=0 MUST be emitted, not omitted")
}

// ─── helpers ───────────────────────────────────────────────────────────────

func findRawAVP(t *testing.T, m *diam.Message, code, vendorID uint32) *diam.AVP {
	t.Helper()
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	t.Fatalf("AVP not found: code=%d vendor=%d", code, vendorID)
	return nil
}
