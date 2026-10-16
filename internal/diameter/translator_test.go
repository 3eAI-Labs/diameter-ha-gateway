// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net"
	"testing"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTranslator(t *testing.T) {
	translator := NewTranslator()
	assert.NotNil(t, translator)
}

// =============================================================================
// ToS6aEvent Tests
// =============================================================================

func TestToS6aEvent_ValidULR(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidULR(t)

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	assert.Equal(t, "286011234567890", event.IMSI)
	assert.Equal(t, "trace-123", event.Header.TraceID)
	assert.Equal(t, "mme.test.3gppnetwork.org", event.Header.SourceHost)
	assert.Equal(t, "test.3gppnetwork.org", event.Header.SourceRealm)
	assert.Equal(t, "hss.3gppnetwork.org", event.Header.DestRealm)
	assert.Equal(t, "test-session-123", event.Header.SessionID)
}

func TestToS6aEvent_WithRATType(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidULR(t)
	// RAT-Type (1032) is a 3GPP AVP, needs vendor ID 10415
	msg.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004)) // E-UTRAN

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	assert.Equal(t, uint32(1004), event.RATType)
}

func TestToS6aEvent_WithULRFlags(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidULR(t)
	msg.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(7))

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	assert.Equal(t, uint32(7), event.ULRFlags)
}

func TestToS6aEvent_WithVisitedPLMN(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidULR(t)
	// PLMN: MCC=286, MNC=01 -> 28 60 1F in hex
	msg.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x28, 0x60, 0x1F}))

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	assert.Equal(t, "28601f", event.VisitedPLMN)
}

func TestToS6aEvent_WithTerminalInfo(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidULR(t)
	terminalInfo := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("123456789012345")),
			diam.NewAVP(avp.SoftwareVersion, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("01")),
		},
	}
	msg.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, terminalInfo)

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	require.NotNil(t, event.TerminalInfo)
	assert.Equal(t, "123456789012345", event.TerminalInfo.IMEI)
	assert.Equal(t, "01", event.TerminalInfo.SV)
}

func TestToS6aEvent_WithAuthInfo(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidULR(t)
	authInfo := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.NumberOfRequestedVectors, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(5)),
			diam.NewAVP(avp.ImmediateResponsePreferred, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1)),
		},
	}
	msg.NewAVP(avp.RequestedEUTRANAuthenticationInfo, avp.Mbit|avp.Vbit, 10415, authInfo)

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	require.NotNil(t, event.AuthInfo)
	assert.Equal(t, uint32(5), event.AuthInfo.RequestedVectors)
	assert.True(t, event.AuthInfo.ImmediateRes)
}

func TestToS6aEvent_WithPURFlags(t *testing.T) {
	translator := NewTranslator()
	msg := buildPURMessage(t)

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	assert.Equal(t, uint32(3), event.PURFlags)
}

func TestToS6aEvent_WithNORFlags(t *testing.T) {
	translator := NewTranslator()
	msg := buildNORMessage(t)

	event, err := translator.ToS6aEvent(msg, "trace-123")

	require.NoError(t, err)
	assert.Equal(t, uint32(1), event.NORFlags)
}

func TestToS6aEvent_MinimalMessage(t *testing.T) {
	translator := NewTranslator()
	// Minimal message with no optional AVPs
	msg := diam.NewRequest(316, 16777251, nil)
	msg.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("min-session"))
	msg.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.com"))
	msg.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))

	event, err := translator.ToS6aEvent(msg, "trace-min")

	require.NoError(t, err)
	assert.Empty(t, event.IMSI) // No User-Name AVP
	assert.Equal(t, "min-session", event.Header.SessionID)
}

// =============================================================================
// ToS13Event Tests
// =============================================================================

func TestToS13Event_ValidECR(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidECR(t)

	event, err := translator.ToS13Event(msg, "trace-s13")

	require.NoError(t, err)
	assert.Equal(t, "286011234567890", event.IMSI)
	assert.Equal(t, "trace-s13", event.Header.TraceID)
}

func TestToS13Event_WithTerminalInfo(t *testing.T) {
	translator := NewTranslator()
	msg := buildValidECR(t)
	terminalInfo := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("35293006225621")),
		},
	}
	msg.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, terminalInfo)

	event, err := translator.ToS13Event(msg, "trace-s13")

	require.NoError(t, err)
	require.NotNil(t, event.TerminalInfo)
	assert.Equal(t, "35293006225621", event.TerminalInfo.IMEI)
}

// =============================================================================
// parseTerminalInfo Tests
// =============================================================================

func TestParseTerminalInfo_Complete(t *testing.T) {
	translator := NewTranslator()
	grouped := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("123456789012345")),
			diam.NewAVP(avp.SoftwareVersion, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("42")),
		},
	}
	avpData := diam.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, grouped)

	info := translator.parseTerminalInfo(avpData)

	assert.Equal(t, "123456789012345", info.IMEI)
	assert.Equal(t, "42", info.SV)
}

func TestParseTerminalInfo_IMEIOnly(t *testing.T) {
	translator := NewTranslator()
	grouped := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("353456789012345")),
		},
	}
	avpData := diam.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, grouped)

	info := translator.parseTerminalInfo(avpData)

	assert.Equal(t, "353456789012345", info.IMEI)
	assert.Empty(t, info.SV)
}

// =============================================================================
// parseAuthInfo Tests
// =============================================================================

func TestParseAuthInfo_Complete(t *testing.T) {
	translator := NewTranslator()
	grouped := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.NumberOfRequestedVectors, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(3)),
			diam.NewAVP(avp.ImmediateResponsePreferred, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1)),
			diam.NewAVP(avp.ResynchronizationInfo, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0xAB, 0xCD})),
		},
	}
	avpData := diam.NewAVP(avp.RequestedEUTRANAuthenticationInfo, avp.Mbit|avp.Vbit, 10415, grouped)

	info := translator.parseAuthInfo(avpData)

	assert.Equal(t, uint32(3), info.RequestedVectors)
	assert.True(t, info.ImmediateRes)
	assert.Equal(t, "abcd", info.ReSyncInfo)
}

func TestParseAuthInfo_ImmediateResponseFalse(t *testing.T) {
	translator := NewTranslator()
	grouped := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.NumberOfRequestedVectors, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(2)),
			diam.NewAVP(avp.ImmediateResponsePreferred, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0)),
		},
	}
	avpData := diam.NewAVP(avp.RequestedEUTRANAuthenticationInfo, avp.Mbit|avp.Vbit, 10415, grouped)

	info := translator.parseAuthInfo(avpData)

	assert.Equal(t, uint32(2), info.RequestedVectors)
	assert.False(t, info.ImmediateRes)
}

// =============================================================================
// MaskIMSI Tests
// =============================================================================

func TestMaskIMSI_ValidIMSI(t *testing.T) {
	translator := NewTranslator()

	testCases := []struct {
		input    string
		expected string
	}{
		{"286011234567890", "286011******90"},
		{"310260123456789", "310260******89"},
		{"12345678901234567", "123456******67"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := translator.MaskIMSI(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMaskIMSI_ShortIMSI(t *testing.T) {
	translator := NewTranslator()

	shortIMSIs := []string{"", "1234", "12345", "1234567"}
	for _, imsi := range shortIMSIs {
		t.Run("short_"+imsi, func(t *testing.T) {
			result := translator.MaskIMSI(imsi)
			assert.Equal(t, "****", result)
		})
	}
}

// =============================================================================
// CreateErrorAnswer Tests
// =============================================================================

func TestCreateErrorAnswer_Success(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)

	ans, err := translator.CreateErrorAnswer(req, diam.Success)

	require.NoError(t, err)
	assert.NotNil(t, ans)
	// Answer should have the correct command code (ULA = 316)
	assert.Equal(t, uint32(316), ans.Header.CommandCode)
}

func TestCreateErrorAnswer_UnableToDeliver(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)

	ans, err := translator.CreateErrorAnswer(req, diam.UnableToDeliver)

	require.NoError(t, err)
	assert.NotNil(t, ans)
}

// =============================================================================
// CreateCLR Tests
// =============================================================================

func TestCreateCLR(t *testing.T) {
	translator := NewTranslator()

	msg, sid := translator.CreateCLR(
		"286011234567890",
		"mme.dest.3gppnetwork.org",
		"dest.3gppnetwork.org",
		"hss.origin.3gppnetwork.org",
		"origin.3gppnetwork.org",
	)

	// Session-Id includes IMSI prefix and unique nano-suffix.
	assert.Contains(t, sid, "286011234567890-cl-")

	// Verify message structure
	assert.Equal(t, uint32(317), msg.Header.CommandCode)
	assert.Equal(t, uint32(16777251), msg.Header.ApplicationID)

	// Verify AVPs
	userName, err := msg.FindAVP(avp.UserName, 0)
	require.NoError(t, err)
	assert.Contains(t, userName.Data.String(), "286011234567890")

	destHost, err := msg.FindAVP(avp.DestinationHost, 0)
	require.NoError(t, err)
	assert.Contains(t, destHost.Data.String(), "mme.dest.3gppnetwork.org")

	// CancellationType (1420) is a 3GPP AVP, needs vendor ID 10415
	cancelType, err := msg.FindAVP(avp.CancellationType, 10415)
	require.NoError(t, err)
	assert.Equal(t, datatype.Enumerated(0), cancelType.Data.(datatype.Enumerated))
}

// =============================================================================
// CreateDWR / CreateDPR Tests (active watchdog + graceful shutdown)
// =============================================================================

func TestCreateDWR(t *testing.T) {
	tr := NewTranslator()
	m := tr.CreateDWR("gw.origin.example.com", "origin.example.com")

	// Command code 280 (DWR), App-Id 0 (Common Diameter)
	assert.Equal(t, uint32(280), m.Header.CommandCode)
	assert.Equal(t, uint32(0), m.Header.ApplicationID)

	// Origin-Host / Origin-Realm mandatory per RFC 6733 §5.5.1
	oh, err := m.FindAVP(avp.OriginHost, 0)
	require.NoError(t, err)
	assert.Contains(t, oh.Data.String(), "gw.origin.example.com")

	or, err := m.FindAVP(avp.OriginRealm, 0)
	require.NoError(t, err)
	assert.Contains(t, or.Data.String(), "origin.example.com")

	// Origin-State-Id present so peers can detect gateway restarts.
	osi, err := m.FindAVP(avp.OriginStateID, 0)
	require.NoError(t, err)
	assert.NotZero(t, uint32(osi.Data.(datatype.Unsigned32)))
}

// TestCreateDWR_StableOriginStateID guards the regression: Origin-State-Id
// MUST stay constant across calls within a translator's lifetime (RFC 6733
// §8.16). The previous bug recomputed time.Now() on every DWR which made
// peers think the gateway restarted on every watchdog probe.
func TestCreateDWR_StableOriginStateID(t *testing.T) {
	tr := NewTranslator()

	first := tr.CreateDWR("gw.origin.example.com", "origin.example.com")
	// Sleep long enough that a per-call time.Now() implementation would
	// observe a different unix-second value.
	time.Sleep(1100 * time.Millisecond)
	second := tr.CreateDWR("gw.origin.example.com", "origin.example.com")

	osi1, err := first.FindAVP(avp.OriginStateID, 0)
	require.NoError(t, err)
	osi2, err := second.FindAVP(avp.OriginStateID, 0)
	require.NoError(t, err)

	assert.Equal(t,
		uint32(osi1.Data.(datatype.Unsigned32)),
		uint32(osi2.Data.(datatype.Unsigned32)),
		"Origin-State-Id must be stable for the lifetime of a translator")

	assert.Equal(t, tr.OriginStateID(), uint32(osi1.Data.(datatype.Unsigned32)),
		"OriginStateID() accessor must report the same value as the AVP")
}

func TestCreateDPR_RebootingCause(t *testing.T) {
	tr := NewTranslator()
	m := tr.CreateDPR("gw.origin.example.com", "origin.example.com", 0) // REBOOTING

	assert.Equal(t, uint32(282), m.Header.CommandCode)
	assert.Equal(t, uint32(0), m.Header.ApplicationID)

	dc, err := m.FindAVP(avp.DisconnectCause, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Enumerated(0), dc.Data.(datatype.Enumerated),
		"REBOOTING is the right default for K8s rolling restart")
}

func TestCreateDPA(t *testing.T) {
	tr := NewTranslator()

	// Build a peer-initiated DPR to answer (REBOOTING cause).
	dpr := tr.CreateDPR("peer.example.com", "peer.example.com", 0)

	dpa := tr.CreateDPA(dpr, "gw.origin.example.com", "origin.example.com")

	// Command code preserved (282), Request bit cleared.
	assert.Equal(t, uint32(282), dpa.Header.CommandCode, "DPA reuses DPR command code")
	assert.Equal(t, uint8(0), dpa.Header.CommandFlags&0x80, "Request bit must be cleared in DPA")

	// H2H / E2E IDs preserved so peer can correlate with its outstanding DPR.
	assert.Equal(t, dpr.Header.HopByHopID, dpa.Header.HopByHopID)
	assert.Equal(t, dpr.Header.EndToEndID, dpa.Header.EndToEndID)

	// Mandatory AVPs (RFC 6733 §5.4.4): Result-Code, Origin-Host, Origin-Realm.
	rc, err := dpa.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(2001), rc.Data.(datatype.Unsigned32),
		"DPA must carry DIAMETER_SUCCESS for graceful disconnect")

	oh, err := dpa.FindAVP(avp.OriginHost, 0)
	require.NoError(t, err)
	assert.Contains(t, oh.Data.String(), "gw.origin.example.com")

	or, err := dpa.FindAVP(avp.OriginRealm, 0)
	require.NoError(t, err)
	assert.Contains(t, or.Data.String(), "origin.example.com")
}

// =============================================================================
// CreateRSR Tests
// =============================================================================

func TestCreateRSR_BroadcastModeOmitsUserID(t *testing.T) {
	tr := NewTranslator()
	// No IMSI prefix: the RSR covers every subscriber (TS 29.272 §7.2.15).
	m, sid := tr.CreateRSR("", "mme.x", "x", "gw.x", "x")

	assert.Contains(t, sid, "broadcast-rs-")
	assert.Equal(t, uint32(322), m.Header.CommandCode)
	_, err := m.FindAVP(avpUserID, 10415)
	assert.Error(t, err, "broadcast RSR must not carry User-Id")
}

// TestCreateRSR_UserID — the RSR scopes the reset with User-Id (1444, "V",
// TS 29.272 §7.3.50); its ABNF has no User-Name.
func TestCreateRSR_UserID(t *testing.T) {
	tr := NewTranslator()
	m, sid := tr.CreateRSR("28601", "mme.x", "x", "gw.x", "x")

	assert.Contains(t, sid, "28601-rs-")
	uid, err := m.FindAVP(avpUserID, 10415)
	require.NoError(t, err)
	assert.Equal(t, octets(t, "000005a4 80 000011 000028af 3238363031000000"), serialized(t, uid)) // "28601" + padding
	_, err = m.FindAVP(avp.UserName, 0)
	assert.Error(t, err, "the RSR has no User-Name")
}

// =============================================================================
// Forward-path AVP extensions
// =============================================================================

// TestToS6aEvent_ParsesActiveAPN — multi-APN list extraction (TS 29.272 §7.3.127).
func TestToS6aEvent_ParsesActiveAPN(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)
	m.NewAVP(avp.ActiveAPN, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.ServiceSelection, avp.Mbit, 0, datatype.UTF8String("internet")),
		},
	})
	m.NewAVP(avp.ActiveAPN, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.ServiceSelection, avp.Mbit, 0, datatype.UTF8String("ims")),
		},
	})
	event, err := tr.ToS6aEvent(m, "trace-aa")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"internet", "ims"}, event.ActiveAPN,
		"both APN names must round-trip from Active-APN list")
}

// TestToS6aEvent_ParsesEquivalentPLMNList — Equivalent-PLMN-List (1637, TS
// 29.272 §7.3.151) is Grouped: 1*{ Visited-PLMN-Id }. Decoded from the wire so
// the dictionary registration is exercised too.
func TestToS6aEvent_ParsesEquivalentPLMNList(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)
	plmn1, _ := hex.DecodeString("82f001") // MCC 286, MNC 01
	plmn2, _ := hex.DecodeString("82f002") // MCC 286, MNC 02
	m.NewAVP(1637, avp.Vbit, 10415, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString(plmn1)),
		diam.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString(plmn2)),
	}})
	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	event, err := tr.ToS6aEvent(decoded, "trace-eql")
	require.NoError(t, err)
	assert.Equal(t, []string{"82f001", "82f002"}, event.EquivalentPLMNList)
}

// TestToS6aEvent_ParsesMMENumberForMTSMS — SGd MT routing target.
func TestToS6aEvent_ParsesMMENumberForMTSMS(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)
	num, _ := hex.DecodeString("905321234567f0") // E.164 TBCD
	m.NewAVP(avp.MMENumberforMTSMS, avp.Mbit|avp.Vbit, 10415, datatype.OctetString(num))

	event, err := tr.ToS6aEvent(m, "trace-mn")
	require.NoError(t, err)
	assert.Equal(t, "905321234567f0", event.MMENumberForMTSMS)
}

// TestToS6aEvent_ParsesTraceData — slim input (TraceReference only).
func TestToS6aEvent_ParsesTraceData(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)
	traceRef, _ := hex.DecodeString("82f00100ab")
	m.NewAVP(avp.TraceData, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(1459 /* Trace-Reference */, avp.Mbit|avp.Vbit, 10415, datatype.OctetString(traceRef)),
		},
	})

	event, err := tr.ToS6aEvent(m, "trace-td")
	require.NoError(t, err)
	require.NotNil(t, event.TraceData, "Trace-Data with Trace-Reference must populate event.TraceData")
	assert.Equal(t, traceRef, event.TraceData.TraceReference)
}

// TestToS6aEvent_ParsesUTRANGERANAuth — converged-HSS 2G/3G auth path.
func TestToS6aEvent_ParsesUTRANGERANAuth(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)
	resync, _ := hex.DecodeString("0102030405060708")
	m.NewAVP(avp.RequestedUTRANGERANAuthenticationInfo, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.NumberOfRequestedVectors, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(2)),
			diam.NewAVP(avp.ImmediateResponsePreferred, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1)),
			diam.NewAVP(avp.ResynchronizationInfo, avp.Mbit|avp.Vbit, 10415, datatype.OctetString(resync)),
		},
	})

	event, err := tr.ToS6aEvent(m, "trace-utran")
	require.NoError(t, err)
	require.NotNil(t, event.AuthInfoUTRAN)
	assert.Equal(t, uint32(2), event.AuthInfoUTRAN.RequestedVectors)
	assert.True(t, event.AuthInfoUTRAN.ImmediateRes)
	assert.Equal(t, "0102030405060708", event.AuthInfoUTRAN.ReSyncInfo)
}

// TestToS6aEvent_ParsesDRMP — RFC 7944 priority extracted into event.
func TestToS6aEvent_ParsesDRMP(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)
	m.NewAVP(avp.DRMP, avp.Mbit, 0, datatype.Enumerated(10)) // PRIORITY_10

	event, err := tr.ToS6aEvent(m, "trace-drmp")
	require.NoError(t, err)
	assert.Equal(t, uint32(10), event.DRMP)
}

// TestToDiameterAnswer_PropagatesDRMP — RFC 7944 §3.2 proxy-preserve.
// Gateway auto-copies DRMP from request to answer without backend involvement.
func TestToDiameterAnswer_PropagatesDRMP(t *testing.T) {
	tr := NewTranslator()
	req := buildValidULR(t)
	req.NewAVP(avp.DRMP, avp.Mbit, 0, datatype.Enumerated(5))

	respData, _ := json.Marshal(pb.S6aAnswer{ResultCode: 2001})
	ans, err := tr.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	drmp, err := ans.FindAVP(avp.DRMP, 0)
	require.NoError(t, err, "DRMP must be auto-copied from request to answer")
	e, ok := drmp.Data.(datatype.Enumerated)
	require.True(t, ok)
	assert.Equal(t, datatype.Enumerated(5), e)
}

// TestApplyResetIDs_ULA_RoundTrip — Reset-ID (1670, TS 29.272 §7.3.184) on a
// ULA, for HSS-stateful peers.
func TestApplyResetIDs_ULA_RoundTrip(t *testing.T) {
	tr := NewTranslator()
	req := buildValidULR(t)

	respData, _ := json.Marshal(pb.S6aAnswer{
		ResultCode: 2001,
		ResetIDs:   []string{"deadbeef", "cafebabe"},
	})
	ans, err := tr.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	resets, err := ans.FindAVPs(1670, 10415)
	require.NoError(t, err)
	require.Len(t, resets, 2)
	expected1, _ := hex.DecodeString("deadbeef")
	expected2, _ := hex.DecodeString("cafebabe")
	assert.Equal(t, expected1, resets[0].Data.Serialize())
	assert.Equal(t, expected2, resets[1].Data.Serialize())
}

// TestApplyResetIDs_ULAOnly — of the answers the gateway builds, only the ULA
// carries Reset-ID (TS 29.272 §7.2.4); AIA, PUA and NOA have none (§7.2.6,
// §7.2.14, §7.2.18).
func TestApplyResetIDs_ULAOnly(t *testing.T) {
	tr := NewTranslator()
	respData, _ := json.Marshal(pb.S6aAnswer{ResultCode: 2001, ResetIDs: []string{"deadbeef"}})
	for _, cmd := range []uint32{318, 321, 323} {
		req := diam.NewRequest(cmd, 16777251, dict.Default)
		req.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("mme.example;1"))
		ans, err := tr.ToDiameterAnswer(respData, req)
		require.NoError(t, err)
		_, err = ans.FindAVP(1670, 10415)
		assert.Error(t, err, "command %d: Reset-ID must not be sent", cmd)
	}
}

// TestToS6aEvent_ParsesSupportedFeatures verifies the gateway extracts
// the MME's advertised Rel-12+ feature bits per TS 29.229 §6.2 +
// TS 29.272 §7.3.10. S6aEvent.SupportedFeatures field has been declared
// since v0.x but parsing was missing — this test guards the wire path.
func TestToS6aEvent_ParsesSupportedFeatures(t *testing.T) {
	tr := NewTranslator()
	m := buildValidULR(t)

	// Append two Supported-Features Grouped AVPs (typical: feature list 1
	// for set A, list 2 for set B). Each carries Vendor-Id + List-ID + List.
	m.NewAVP(avp.SupportedFeatures, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
			diam.NewAVP(avp.FeatureListID, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1)),
			diam.NewAVP(avp.FeatureList, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0x0F)),
		},
	})
	m.NewAVP(avp.SupportedFeatures, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(10415)),
			diam.NewAVP(avp.FeatureListID, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(2)),
			diam.NewAVP(avp.FeatureList, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(0x0100)),
		},
	})

	event, err := tr.ToS6aEvent(m, "trace-sf-1")
	require.NoError(t, err)
	require.Len(t, event.SupportedFeatures, 2)

	assert.Equal(t, uint32(10415), event.SupportedFeatures[0].VendorID)
	assert.Equal(t, uint32(1), event.SupportedFeatures[0].FeatureListID)
	assert.Equal(t, uint32(0x0F), event.SupportedFeatures[0].FeatureList)

	assert.Equal(t, uint32(2), event.SupportedFeatures[1].FeatureListID)
	assert.Equal(t, uint32(0x0100), event.SupportedFeatures[1].FeatureList)
}

// TestApplySupportedFeatures_ULA_RoundTrip — end-to-end: backend supplies
// SupportedFeatures in S6aAnswer, ToDiameterAnswer encodes them as Grouped
// AVPs, wire round-trip recovers them with original feature-list bits.
func TestApplySupportedFeatures_ULA_RoundTrip(t *testing.T) {
	tr := NewTranslator()
	req := buildValidULR(t)

	respData, err := json.Marshal(pb.S6aAnswer{
		ResultCode: 2001,
		SupportedFeatures: []pb.SupportedFeature{
			{VendorID: 10415, FeatureListID: 1, FeatureList: 0x07},
		},
	})
	require.NoError(t, err)

	ans, err := tr.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = ans.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	sf, err := decoded.FindAVP(avp.SupportedFeatures, 10415)
	require.NoError(t, err, "Supported-Features missing from decoded ULA")
	grouped, ok := sf.Data.(*diam.GroupedAVP)
	require.True(t, ok, "Supported-Features MUST be Grouped, got %T", sf.Data)

	fl := findInGrouped(grouped, avp.FeatureList, 10415)
	require.NotNil(t, fl)
	assert.Equal(t, uint32(0x07), avpUint32(fl), "Feature-List bits must round-trip")
}

// TestApplyFailedAVPs_ULA_BackendSupplied verifies that backend-provided
// FailedAVPs entries reach the wire as a Failed-AVP grouped AVP (RFC 6733
// §7.5, code 279) wrapping the offending AVP children. Tests the full
// round-trip on a ULA carrying 5005 MISSING_AVP — the carrier-typical case
// where the backend detected a missing User-Name and wants to tell the MME.
func TestApplyFailedAVPs_ULA_BackendSupplied(t *testing.T) {
	tr := NewTranslator()
	req := buildValidULR(t)

	respData, err := json.Marshal(pb.S6aAnswer{
		ResultCode: 5005, // DIAMETER_MISSING_AVP
		FailedAVPs: []pb.FailedAVPEntry{
			{Code: avp.UserName, VendorID: 0, Value: ""},                       // base AVP, no value
			{Code: avp.RATType, VendorID: 10415, Value: "00000006" /* WLAN */}, // 3GPP AVP, populated value
		},
	})
	require.NoError(t, err)

	ans, err := tr.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	// Wire round-trip — Failed-AVP is base AVP code 279, no vendor.
	var buf bytes.Buffer
	_, err = ans.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	failedAVP, err := decoded.FindAVP(avp.FailedAVP, 0)
	require.NoError(t, err, "Failed-AVP missing from decoded ULA")
	grouped, ok := failedAVP.Data.(*diam.GroupedAVP)
	require.True(t, ok, "Failed-AVP MUST be Grouped per RFC 6733 §7.5, got %T", failedAVP.Data)

	// Both child AVPs must round-trip with their original codes.
	un := findInGrouped(grouped, avp.UserName, 0)
	require.NotNil(t, un, "Failed-AVP child for User-Name missing")

	rat := findInGrouped(grouped, avp.RATType, 10415)
	require.NoError(t, err)
	require.NotNil(t, rat, "Failed-AVP child for RAT-Type missing")
	// RAT-Type value byte-pattern must survive (0x00000006 = WLAN).
	expected, _ := hex.DecodeString("00000006")
	assert.Equal(t, expected, rat.Data.Serialize(),
		"Failed-AVP child value must round-trip verbatim")
}

// TestApplyFailedAVPs_EmptyEntries_NoEmission guards a regression: empty
// FailedAVPs slice MUST NOT emit a Failed-AVP grouped AVP. Some strict
// peers reject empty Grouped AVPs.
func TestApplyFailedAVPs_EmptyEntries_NoEmission(t *testing.T) {
	tr := NewTranslator()
	req := buildValidULR(t)

	respData, err := json.Marshal(pb.S6aAnswer{
		ResultCode: 2001, // success — happy-path answer should never carry Failed-AVP
		// FailedAVPs not set
	})
	require.NoError(t, err)

	ans, err := tr.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	_, err = ans.FindAVP(avp.FailedAVP, 0)
	assert.Error(t, err, "Failed-AVP MUST NOT be emitted on empty FailedAVPs slice")
}

// TestApplyFailedAVPs_BadHexValue_SkippedNotFatal: producer typo in hex
// value should not poison the answer. Skip the bad entry, keep the rest.
func TestApplyFailedAVPs_BadHexValue_SkippedNotFatal(t *testing.T) {
	tr := NewTranslator()
	req := buildValidULR(t)

	respData, err := json.Marshal(pb.S6aAnswer{
		ResultCode: 5004,
		FailedAVPs: []pb.FailedAVPEntry{
			{Code: avp.UserName, VendorID: 0, Value: "not-valid-hex!!"},
			{Code: avp.RATType, VendorID: 10415, Value: "00000006"},
		},
	})
	require.NoError(t, err)

	ans, err := tr.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	failedAVP, err := ans.FindAVP(avp.FailedAVP, 0)
	require.NoError(t, err, "Failed-AVP should still be emitted with surviving entries")
	grouped := failedAVP.Data.(*diam.GroupedAVP)
	require.Len(t, grouped.AVP, 1, "bad-hex entry must be skipped, valid entry kept")
	assert.Equal(t, uint32(avp.RATType), grouped.AVP[0].Code, "surviving child should be RAT-Type")
}

// TestCreateDSR_FullScope covers the carrier-typical use case: DSR-Flags
// bit-driven scope + Context-Identifier list scoping APN-specific withdrawal.
// Wire round-trip exercises the dict_extensions registration of cmd 320 +
// DSR-Flags AVP (1421); pre-registration this would fail at ReadMessage.
func TestCreateDSR_FullScope(t *testing.T) {
	tr := NewTranslator()
	// DSR-Flags bit 1 (CompleteAPNConfigurationProfileWithdrawal) + bit 7
	// (RemoveMDT) — typical "subscriber moves carriers, clear extras" flow.
	const dsrFlags uint32 = (1 << 1) | (1 << 7)
	m, sid := tr.CreateDSR(
		"286011234567890",
		"mme.dest.example.com",
		"dest.example.com",
		"gw.origin.example.com",
		"origin.example.com",
		dsrFlags,
		[]uint32{1, 5}, // scope withdrawal to APN ContextIDs 1 and 5
	)

	assert.Contains(t, sid, "286011234567890-ds-")
	assert.Equal(t, uint32(320), m.Header.CommandCode)
	assert.Equal(t, uint32(16777251), m.Header.ApplicationID)

	// Wire round-trip — proves dict.Default registration. Pre-T7-style
	// dict gap would fail at ReadMessage with "Could not find preloaded
	// Command with code 320".
	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err, "DSR wire decode failed — dict.Default registration broken?")

	// DSR-Flags MUST round-trip and decode as Unsigned32 (registration guard).
	dsrFlagsAVP, err := decoded.FindAVP(1421, 10415)
	require.NoError(t, err, "DSR-Flags missing from decoded DSR")
	assert.Equal(t, dsrFlags, avpUint32(dsrFlagsAVP),
		"DSR-Flags must round-trip and decode as Unsigned32 (dict registration)")

	// Context-Identifier — repeated AVP, both 1 and 5 must survive wire pass.
	ctxIDs, err := decoded.FindAVPs(avp.ContextIdentifier, 10415)
	require.NoError(t, err)
	require.Len(t, ctxIDs, 2, "both Context-Identifier AVPs must round-trip")
	got := []uint32{avpUint32(ctxIDs[0]), avpUint32(ctxIDs[1])}
	assert.ElementsMatch(t, []uint32{1, 5}, got,
		"Context-Identifier values must round-trip in submitted order or any")

	// User-Name MUST be present (TS 29.272 §6.1.4 mandates {User-Name}).
	un, err := decoded.FindAVP(avp.UserName, 0)
	require.NoError(t, err)
	assert.Contains(t, un.Data.String(), "286011234567890")
}

// TestCreateDSR_NoContextIDs covers the "withdraw by flags only" path —
// e.g. DSR-Flags bit 0 (RegionalSubscriptionWithdrawal) needs no APN scope.
func TestCreateDSR_NoContextIDs(t *testing.T) {
	tr := NewTranslator()
	m, _ := tr.CreateDSR("286010000000001", "m.x", "x", "g.x", "x", 1<<0, nil)

	// No Context-Identifier AVP should be emitted.
	ctxIDs, _ := m.FindAVPs(avp.ContextIdentifier, 10415)
	assert.Empty(t, ctxIDs, "empty contextIDs slice must NOT emit Context-Identifier AVPs")

	// DSR-Flags is mandatory per TS 29.272 §6.1.4 — MUST be emitted even
	// when only one bit is set (we don't gate emission on non-zero like we
	// do for IDR-Flags / PUR-Flags optional fields).
	dsrFlags := findAVPByCode(m, 1421, 10415)
	require.NotNil(t, dsrFlags, "DSR-Flags MUST be emitted (mandatory AVP)")
}

// TestCreateDSR_ZeroFlagsStillEmitsAVP guards a regression risk: per spec
// DSR-Flags is mandatory, so even zero MUST be emitted (peer's contract is
// "you sent me DSR with no bits set" — interpretive no-op, NOT "you sent
// me DSR without DSR-Flags" which is a parse error).
func TestCreateDSR_ZeroFlagsStillEmitsAVP(t *testing.T) {
	tr := NewTranslator()
	m, _ := tr.CreateDSR("286010000000001", "m.x", "x", "g.x", "x", 0, nil)

	dsrFlags := findAVPByCode(m, 1421, 10415)
	require.NotNil(t, dsrFlags, "DSR-Flags MUST be emitted even when zero (mandatory per ABNF)")
	assert.Equal(t, uint32(0), avpUint32(dsrFlags))
}

// =============================================================================
// ToDiameterAnswer Tests
// =============================================================================

func TestToDiameterAnswer_ULA_Success(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	respData, _ := json.Marshal(pb.S6aAnswer{
		ResultCode: diam.Success,
		ULAFlags:   1,
		SubData:    "test-sub-data",
	})

	ans, err := translator.ToDiameterAnswer(respData, req)

	require.NoError(t, err)
	require.NotNil(t, ans)

	// Check ULA-Flags
	ulaFlags, err := ans.FindAVP(avp.ULAFlags, 10415)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(1), ulaFlags.Data.(datatype.Unsigned32))
}

func TestToDiameterAnswer_ULA_UserUnknown(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	respData, _ := json.Marshal(pb.S6aAnswer{
		ResultCode: 5001, // DIAMETER_ERROR_USER_UNKNOWN
	})

	ans, err := translator.ToDiameterAnswer(respData, req)

	require.NoError(t, err)
	require.NotNil(t, ans)
}

func TestToDiameterAnswer_ECA_Success(t *testing.T) {
	translator := NewTranslator()
	req := buildValidECR(t)
	respData, _ := json.Marshal(pb.S13Answer{
		ResultCode: diam.Success,
		Status:     0, // WHITELISTED
	})

	ans, err := translator.ToDiameterAnswer(respData, req)

	require.NoError(t, err)
	require.NotNil(t, ans)

	// Check Equipment-Status (code 1445) - use manual lookup since not in dictionary
	equipStatus := findAVPByCode(ans, 1445, 10415)
	require.NotNil(t, equipStatus, "Equipment-Status AVP not found")
	assert.Equal(t, datatype.Enumerated(0), equipStatus.Data.(datatype.Enumerated))
}

func TestToDiameterAnswer_ECA_Blacklisted(t *testing.T) {
	translator := NewTranslator()
	req := buildValidECR(t)
	respData, _ := json.Marshal(pb.S13Answer{
		ResultCode: diam.Success,
		Status:     1, // BLACKLISTED
	})

	ans, err := translator.ToDiameterAnswer(respData, req)

	require.NoError(t, err)

	// Use manual lookup since Equipment-Status (1445) is not in dictionary
	equipStatus := findAVPByCode(ans, 1445, 10415)
	require.NotNil(t, equipStatus, "Equipment-Status AVP not found")
	assert.Equal(t, datatype.Enumerated(1), equipStatus.Data.(datatype.Enumerated))
}

func TestToDiameterAnswer_InvalidJSON(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	invalidJSON := []byte("not valid json")

	ans, err := translator.ToDiameterAnswer(invalidJSON, req)

	// Should return error answer but no Go error
	require.Error(t, err)
	require.NotNil(t, ans)
}

// TestToDiameterAnswer_ULA_RedirectIndication exercises the forward-encode
// path: backend signals 3006 + Redirect-Host AVPs in
// the NATS payload, gateway must encode all three §6.5 AVPs onto the wire.
// Round-trips through diam.ReadMessage so wire-codec failures (M-bit, vendor
// bits, datatype tag) surface as test failures rather than silent drops.
func TestToDiameterAnswer_ULA_RedirectIndication(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	respData, _ := json.Marshal(pb.S6aAnswer{
		ResultCode:           3006,
		RedirectHosts:        []string{"aaa://hss-shard-b.example.org", "aaa://hss-shard-c.example.org"},
		RedirectHostUsage:    uint32(RedirectUsageAllRealm),
		RedirectMaxCacheTime: 120,
	})

	ans, err := translator.ToDiameterAnswer(respData, req)
	require.NoError(t, err)
	require.NotNil(t, ans)

	// Wire round-trip — catches AVP encoding regressions the in-memory
	// builder hides (as with Subscription-Data once sent as OctetString).
	var buf bytes.Buffer
	_, err = ans.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	hosts, err := decoded.FindAVPs(avp.RedirectHost, 0)
	require.NoError(t, err)
	require.Len(t, hosts, 2, "both Redirect-Host AVPs must be on the wire in order")
	assert.Equal(t, datatype.DiameterURI("aaa://hss-shard-b.example.org"), hosts[0].Data.(datatype.DiameterURI))
	assert.Equal(t, datatype.DiameterURI("aaa://hss-shard-c.example.org"), hosts[1].Data.(datatype.DiameterURI))

	usage, err := decoded.FindAVP(avp.RedirectHostUsage, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Enumerated(2 /* ALL_REALM */), usage.Data.(datatype.Enumerated))

	ttl, err := decoded.FindAVP(avp.RedirectMaxCacheTime, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(120), ttl.Data.(datatype.Unsigned32))
}

// TestToDiameterAnswer_NoRedirectFields_IsClean guards against accidentally
// emitting Redirect-Host AVPs on non-redirect answers — would confuse peers
// expecting a vanilla 2001 ULA / AIA / PUA / NOA.
func TestToDiameterAnswer_NoRedirectFields_IsClean(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	respData, _ := json.Marshal(pb.S6aAnswer{ResultCode: diam.Success, ULAFlags: 1})

	ans, err := translator.ToDiameterAnswer(respData, req)
	require.NoError(t, err)
	hosts, _ := ans.FindAVPs(avp.RedirectHost, 0)
	assert.Empty(t, hosts, "non-redirect answers must not emit Redirect-Host AVPs")
}

// TestSetTBit_SetsRetransmittedFlag guards the T-bit wire encoding for
// the reverse-path redirect retry and failover retransmit. Bit 4 of Command
// Flags MUST be set after SetTBit; round-trip through ReadMessage so we
// catch any header-codec regression.
func TestSetTBit_SetsRetransmittedFlag(t *testing.T) {
	tr := NewTranslator()
	req, _ := tr.CreateCLR("001010000000001",
		"mme.example.org", "epc.example.org",
		"gw.example.org", "gw.example.org")

	require.False(t, req.Header.CommandFlags&diam.RetransmittedFlag != 0,
		"new request must not have T-bit set")
	tr.SetTBit(req)
	assert.True(t, req.Header.CommandFlags&diam.RetransmittedFlag != 0,
		"after SetTBit the RetransmittedFlag bit must be on")

	// Wire round-trip — ensure the flag survives encode/decode.
	var buf bytes.Buffer
	_, err := req.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)
	assert.True(t, decoded.Header.CommandFlags&diam.RetransmittedFlag != 0,
		"T-bit MUST round-trip through wire codec")
}

func TestSetTBit_NilSafe(t *testing.T) {
	NewTranslator().SetTBit(nil) // must not panic
}

func TestToDiameterAnswer_UnknownCommand(t *testing.T) {
	translator := NewTranslator()
	// Create message with unknown command code
	req := diam.NewRequest(999, 16777251, nil)
	req.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("unknown-session"))
	respData, _ := json.Marshal(struct {
		ResultCode uint32 `json:"result_code"`
	}{ResultCode: diam.Success})

	ans, err := translator.ToDiameterAnswer(respData, req)

	require.NoError(t, err)
	require.NotNil(t, ans)
}

// =============================================================================
// Helper Functions
// =============================================================================

// findAVPByCode finds an AVP by code and vendor ID manually (without dictionary lookup)
// This is needed because go-diameter's FindAVP requires AVPs to be registered in dictionary
func findAVPByCode(m *diam.Message, code uint32, vendorID uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

func buildValidULR(t *testing.T) *diam.Message {
	t.Helper()
	m := diam.NewRequest(316, 16777251, nil) // ULR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-123"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.3gppnetwork.org"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("hss.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

func buildValidECR(t *testing.T) *diam.Message {
	t.Helper()
	m := diam.NewRequest(324, 16777252, nil) // ECR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("ecr-session-123"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

func buildPURMessage(t *testing.T) *diam.Message {
	t.Helper()
	m := diam.NewRequest(321, 16777251, nil) // PUR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("pur-session"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.com"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	m.NewAVP(avp.PURFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(3))
	return m
}

func buildNORMessage(t *testing.T) *diam.Message {
	t.Helper()
	m := diam.NewRequest(323, 16777251, nil) // NOR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("nor-session"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.com"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	m.NewAVP(avp.NORFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1))
	return m
}

// TestToS6aEvent_StringAndAddressValuesOffTheWire decodes a serialised ULR and
// checks every string-like field for its exact value: go-diameter's String()
// is a debug format, and the gateway once forwarded it verbatim.
func TestToS6aEvent_StringAndAddressValuesOffTheWire(t *testing.T) {
	m := buildValidULR(t)
	m.NewAVP(avp.SGSNNumber, avp.Vbit, 10415, datatype.OctetString([]byte{0x91, 0x09, 0x55, 0x10}))
	m.NewAVP(avp.GMLCAddress, avp.Vbit, 10415, datatype.Address(net.ParseIP("192.0.2.7").To4()))
	m.NewAVP(avp.ServiceSelection, avp.Mbit, 0, datatype.UTF8String("internet"))
	raw, err := m.Serialize()
	require.NoError(t, err)
	wire, err := diam.ReadMessage(bytes.NewReader(raw), dict.Default)
	require.NoError(t, err)

	event, err := NewTranslator().ToS6aEvent(wire, "t")
	require.NoError(t, err)
	assert.Equal(t, "286011234567890", event.IMSI)
	assert.Equal(t, "mme.test.3gppnetwork.org", event.Header.SourceHost)
	assert.Equal(t, "test.3gppnetwork.org", event.Header.SourceRealm)
	assert.Equal(t, "hss.3gppnetwork.org", event.Header.DestRealm)
	assert.Equal(t, "test-session-123", event.Header.SessionID)
	assert.Equal(t, "91095510", event.SGSNNumber, "octets as hex, like visited_plmn")
	assert.Equal(t, "192.0.2.7", event.GMLCAddress)
	assert.Equal(t, "internet", event.ServiceSelect)
}
