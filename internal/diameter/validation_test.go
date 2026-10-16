// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

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

// TestValidateMandatoryAVPs_FullValidULR — happy path, no missing AVPs.
// Uses buildULRWithoutAVPs(t, nil) — buildValidULR (translator_test.go) only
// covers the parser-relevant subset and intentionally omits some AVPs that
// validation requires (Auth-Session-State, RAT-Type, ULR-Flags, Visited-PLMN-
// Id). The two helpers serve different test contracts and aren't merged.
func TestValidateMandatoryAVPs_FullValidULR(t *testing.T) {
	m := buildULRWithoutAVPs(t, nil)
	missing := ValidateMandatoryAVPs(m)
	assert.Empty(t, missing, "fully populated ULR must yield zero missing AVPs")
}

// TestValidateMandatoryAVPs_MissingUserName — most common malformed ULR.
func TestValidateMandatoryAVPs_MissingUserName(t *testing.T) {
	m := buildULRWithoutAVPs(t, []uint32{avp.UserName})
	missing := ValidateMandatoryAVPs(m)
	require.Len(t, missing, 1, "exactly one AVP missing")
	assert.Equal(t, uint32(avp.UserName), missing[0].Code)
	assert.Equal(t, uint32(0), missing[0].VendorID, "User-Name is base IETF (vendor 0)")
}

// TestValidateMandatoryAVPs_MissingMultiple — multi-failure path: missing
// ULR-Flags AND VisitedPLMNID. Failed-AVP grouped AVP must list both.
func TestValidateMandatoryAVPs_MissingMultiple(t *testing.T) {
	m := buildULRWithoutAVPs(t, []uint32{avp.ULRFlags, avp.VisitedPLMNID})
	missing := ValidateMandatoryAVPs(m)
	require.Len(t, missing, 2)
	codes := []uint32{missing[0].Code, missing[1].Code}
	assert.ElementsMatch(t, []uint32{uint32(avp.ULRFlags), uint32(avp.VisitedPLMNID)}, codes)
	for _, e := range missing {
		assert.Equal(t, uint32(10415), e.VendorID, "ULR-Flags + VisitedPLMNID are 3GPP vendor")
	}
}

// TestValidateMandatoryAVPs_UnknownCommandCode — defensive default: unknown
// command (e.g. cmd 999) returns nil, doesn't panic. Lets the gateway pass
// through commands without explicit validation tables (e.g. future S6c
// extensions) rather than rejecting them.
func TestValidateMandatoryAVPs_UnknownCommandCode(t *testing.T) {
	m := diam.NewMessage(999, 0, 16777251, 1, 1, dict.Default)
	missing := ValidateMandatoryAVPs(m)
	assert.Nil(t, missing, "unknown command code must return nil missing list")
}

// TestBuildMissingAVPAnswer_WireRoundTrip — full encoder round-trip:
// missing AVPs reach the wire as a Failed-AVP grouped AVP with 5005
// Result-Code, and Origin-Host/Realm get gateway identity (NOT echo from
// the malformed request).
func TestBuildMissingAVPAnswer_WireRoundTrip(t *testing.T) {
	m := buildULRWithoutAVPs(t, []uint32{avp.UserName, avp.RATType})
	missing := ValidateMandatoryAVPs(m)
	require.Len(t, missing, 2)

	ans := BuildMissingAVPAnswer(m, "gateway.example.org", "example.org", missing)

	// Result-Code = 5005 DIAMETER_MISSING_AVP
	rc, err := ans.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(5005), rc.Data.(datatype.Unsigned32))

	// Origin-Host = gateway identity (req had it; req.Answer copies it).
	oh, err := ans.FindAVP(avp.OriginHost, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, oh.Data.String())

	// Wire round-trip — Failed-AVP must survive encode/decode.
	var buf bytes.Buffer
	_, err = ans.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	failedAVP, err := decoded.FindAVP(avp.FailedAVP, 0)
	require.NoError(t, err, "Failed-AVP MUST survive wire round-trip")
	grouped, ok := failedAVP.Data.(*diam.GroupedAVP)
	require.True(t, ok, "Failed-AVP MUST be Grouped, got %T", failedAVP.Data)
	require.Len(t, grouped.AVP, 2, "both missing AVPs must appear as Failed-AVP children")
}

// TestBuildMissingAVPAnswer_RequestMissingOriginHost — when the malformed
// request itself lacks Origin-Host/Realm, the answer MUST still carry them
// (RFC 6733 §3 mandates Origin-Host/Realm on every answer). Gateway
// substitutes its own identity.
func TestBuildMissingAVPAnswer_RequestMissingOriginHost(t *testing.T) {
	m := buildULRWithoutAVPs(t, []uint32{avp.OriginHost, avp.OriginRealm, avp.UserName})
	missing := ValidateMandatoryAVPs(m)

	ans := BuildMissingAVPAnswer(m, "gateway.example.org", "example.org", missing)

	oh, err := ans.FindAVP(avp.OriginHost, 0)
	require.NoError(t, err, "Origin-Host MUST be present on answer even when missing from request")
	// DiameterIdentity.String() returns debug format — type-assert direct.
	ohID, ok := oh.Data.(datatype.DiameterIdentity)
	require.True(t, ok)
	assert.Equal(t, "gateway.example.org", string(ohID))

	or, err := ans.FindAVP(avp.OriginRealm, 0)
	require.NoError(t, err)
	orID, ok := or.Data.(datatype.DiameterIdentity)
	require.True(t, ok)
	assert.Equal(t, "example.org", string(orID))
}

// buildULRWithoutAVPs is a buildValidULR variant that omits the supplied
// AVP codes — used to construct intentionally-malformed ULRs for validation
// tests. Implements the same baseline as buildValidULR but skips entries
// the test wants missing.
func buildULRWithoutAVPs(t *testing.T, omit []uint32) *diam.Message {
	t.Helper()
	skip := make(map[uint32]bool, len(omit))
	for _, c := range omit {
		skip[c] = true
	}

	m := diam.NewMessage(316, diam.RequestFlag, 16777251, 1, 1, dict.Default)
	if !skip[avp.SessionID] {
		m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-1"))
	}
	if !skip[avp.OriginHost] {
		m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.example.org"))
	}
	if !skip[avp.OriginRealm] {
		m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.org"))
	}
	if !skip[avp.DestinationRealm] {
		m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.org"))
	}
	if !skip[avp.AuthSessionState] {
		m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))
	}
	if !skip[avp.UserName] {
		m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286010000000001"))
	}
	if !skip[avp.RATType] {
		m.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004))
	}
	if !skip[avp.ULRFlags] {
		m.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0))
	}
	if !skip[avp.VisitedPLMNID] {
		m.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x82, 0xf0, 0x10}))
	}
	return m
}
