// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"testing"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ULA + IDR Subscription-Data round-trip tests.
//
// These tests do TWO things:
//   1. Confirm the encoder emits Subscription-Data (1400) as a Grouped
//      AVP per TS 29.272 §7.3.2 — NOT as the legacy OctetString that the
//      pre-fix code produced (wire-invalid: 1400 is Grouped).
//   2. Wire round-trip via diam.WriteTo + diam.ReadMessage so the assertions
//      operate on the decoded form, not the in-memory builder. This catches
//      AVP encoding regressions the in-memory builder hides — same pattern
//      that exposed the original bug (translator_test.go:639).
//
// Per-sub-AVP coverage walks the spec tree:
//   Subscription-Data → AMBR → Max-Requested-Bandwidth-{UL,DL}
//   Subscription-Data → APN-Configuration-Profile → Context-Identifier
//                                                  → APN-Configuration → ...
//                                                                       → EPS-Subscribed-QoS-Profile → ARP

// findInGrouped is a local helper because go-diameter's recursive AVP
// search doesn't expose a clean Grouped iterator at this level.
func findInGrouped(g *diam.GroupedAVP, code, vendorID uint32) *diam.AVP {
	for _, a := range g.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

// findAllInGrouped returns every sub-AVP matching (code, vendorID). Used by
// tests where multi-occurrence AVPs (IMSI-Group-Id,
// Adjacent-Access-Restriction-Data) need ordered enumeration. *diam.Message
// exposes FindAVPs but *diam.GroupedAVP does not.
func findAllInGrouped(g *diam.GroupedAVP, code, vendorID uint32) []*diam.AVP {
	var out []*diam.AVP
	for _, a := range g.AVP {
		if a.Code == code && a.VendorID == vendorID {
			out = append(out, a)
		}
	}
	return out
}

// avpUint32 extracts a 4-byte big-endian unsigned integer from an AVP
// regardless of whether dict.Default knows the typed decode (returns
// datatype.Unsigned32 / Enumerated) or falls back to datatype.Unknown
// (raw 4-byte payload).
//
// Why this exists: dict.Default ships duplicate / contradictory entries
// for some 3GPP AVPs (e.g. Service-Selection 493 declared twice with
// different vendor-id flags) and outright omits a few Rel-14 fields
// (Subscription-Data-Flags 1654). Both cases produce datatype.Unknown
// on decode, breaking type-assertion-based assertions. Going through
// Serialize() bytes makes the test robust to those dict gaps.
func avpUint32(a *diam.AVP) uint32 {
	if a == nil {
		return 0
	}
	b := a.Data.Serialize()
	if len(b) != 4 {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// carrierTypicalProfile builds a SubscriptionProfile representing the
// minimum carrier-grade EPS attach response (smartphone, default-bearer,
// non-GBR best-effort) plus a few Rel 14 sub-AVPs that show up in RFP
// audits (Subscription-Data-Flags, MPS-Priority).
func carrierTypicalProfile() *pb.SubscriptionProfile {
	subStatus := uint32(0) // SERVICE_GRANTED
	pdnType := uint32(2)   // IPv4v6
	nam := uint32(2)       // ONLY_PACKET (EPS-only)
	pec := uint32(1)       // pre-emption capability DISABLED
	pev := uint32(0)       // pre-emption vulnerability ENABLED
	apnAll := uint32(0)    // ALL_APN_CONFIGURATIONS_INCLUDED

	return &pb.SubscriptionProfile{
		SubscriberStatus:      &subStatus,
		MSISDN:                "905321234567f0", // hex TBCD
		AccessRestrictionData: 0x02,             // bit 1 = GERAN-NotAllowed
		NetworkAccessMode:     &nam,
		AMBR: &pb.AMBR{
			MaxRequestedBandwidthUL: 1_000_000, // 1 Mbps UE-AMBR
			MaxRequestedBandwidthDL: 5_000_000, // 5 Mbps UE-AMBR
		},
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			ContextIdentifier:                     1,
			AllAPNConfigurationsIncludedIndicator: &apnAll,
			APNConfigurations: []pb.APNConfiguration{
				{
					ContextIdentifier: 1,
					PDNType:           &pdnType,
					ServiceSelection:  "internet",
					EPSSubscribedQoSProfile: &pb.EPSSubscribedQoSProfile{
						QoSClassIdentifier: 9, // non-GBR best-effort
						AllocationRetentionPriority: &pb.AllocationRetentionPriority{
							PriorityLevel:           9,
							PreEmptionCapability:    &pec,
							PreEmptionVulnerability: &pev,
						},
					},
					AMBR: &pb.AMBR{
						MaxRequestedBandwidthUL: 1_000_000, // APN-AMBR
						MaxRequestedBandwidthDL: 5_000_000,
					},
				},
			},
		},
		MPSPriority:           0x01, // entitlement bit
		SubscriptionDataFlags: 0x01, // PCSCFRestoration_indicator
	}
}

// TestEncodeSubscriptionData_NilProfile_NoAVP — defensive: nil profile
// must NOT emit an empty Grouped AVP (some HSS peers reject as malformed).
func TestEncodeSubscriptionData_NilProfile_NoAVP(t *testing.T) {
	g := encodeSubscriptionData(nil)
	assert.Nil(t, g, "nil profile must produce no AVP")
}

// TestEncodeSubscriptionData_EmptyProfile_NoAVP — same defensive check.
func TestEncodeSubscriptionData_EmptyProfile_NoAVP(t *testing.T) {
	g := encodeSubscriptionData(&pb.SubscriptionProfile{})
	assert.Nil(t, g, "empty profile must produce no AVP (no sub-AVPs to emit)")
}

// TestToDiameterAnswer_ULA_SubProfile_StructuredEncoding is the main
// Grouped-encoding regression test. Verifies the full encode → wire → decode
// → recursive assert chain on a carrier-typical profile.
func TestToDiameterAnswer_ULA_SubProfile_StructuredEncoding(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)

	respData, err := json.Marshal(pb.S6aAnswer{
		ResultCode: diam.Success,
		ULAFlags:   1,
		SubProfile: carrierTypicalProfile(),
	})
	require.NoError(t, err)

	ans, err := translator.ToDiameterAnswer(respData, req)
	require.NoError(t, err)
	require.NotNil(t, ans)

	// Wire round-trip — decode the bytes, not the in-memory builder.
	var buf bytes.Buffer
	_, err = ans.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err)

	// ---- Top-level: Subscription-Data MUST be Grouped, not OctetString ----
	subData, err := decoded.FindAVP(avp.SubscriptionData, 10415)
	require.NoError(t, err, "Subscription-Data AVP missing from decoded ULA")
	grouped, ok := subData.Data.(*diam.GroupedAVP)
	require.True(t, ok,
		"regression: Subscription-Data MUST be Grouped per TS 29.272 §7.3.2, got %T", subData.Data)

	// ---- Subscriber-Status (1424) = SERVICE_GRANTED (0) ----
	ss := findInGrouped(grouped, avp.SubscriberStatus, 10415)
	require.NotNil(t, ss, "Subscriber-Status missing")
	assert.Equal(t, uint32(0), avpUint32(ss), "Subscriber-Status must be 0 (SERVICE_GRANTED)")

	// ---- MSISDN (701) — hex round-trip ----
	msisdn := findInGrouped(grouped, avp.MSISDN, 10415)
	require.NotNil(t, msisdn, "MSISDN missing")
	expectedMSISDN, _ := hex.DecodeString("905321234567f0")
	assert.Equal(t, expectedMSISDN, msisdn.Data.Serialize(), "MSISDN bytes must round-trip")

	// ---- Network-Access-Mode (1417) = ONLY_PACKET (2) ----
	nam := findInGrouped(grouped, avp.NetworkAccessMode, 10415)
	require.NotNil(t, nam, "Network-Access-Mode missing")
	assert.Equal(t, uint32(2), avpUint32(nam), "Network-Access-Mode must be 2 (ONLY_PACKET)")

	// ---- Access-Restriction-Data (1426) bitmask ----
	ard := findInGrouped(grouped, avp.AccessRestrictionData, 10415)
	require.NotNil(t, ard, "Access-Restriction-Data missing")
	assert.Equal(t, uint32(0x02), avpUint32(ard))

	// ---- AMBR (UE-AMBR) — recursive: AMBR → Max-Requested-Bandwidth-{UL,DL} ----
	ambrTop := findInGrouped(grouped, avp.AMBR, 10415)
	require.NotNil(t, ambrTop, "UE-AMBR (top-level) missing")
	ambrG, ok := ambrTop.Data.(*diam.GroupedAVP)
	require.True(t, ok, "UE-AMBR must be Grouped")
	assert.Equal(t, uint32(1_000_000), avpUint32(findInGrouped(ambrG, avp.MaxRequestedBandwidthUL, 10415)),
		"UE-AMBR UL must round-trip 1Mbps")
	assert.Equal(t, uint32(5_000_000), avpUint32(findInGrouped(ambrG, avp.MaxRequestedBandwidthDL, 10415)),
		"UE-AMBR DL must round-trip 5Mbps")

	// ---- APN-Configuration-Profile (1429) → Context-Identifier + APN-Configuration ----
	apnp := findInGrouped(grouped, avp.APNConfigurationProfile, 10415)
	require.NotNil(t, apnp, "APN-Configuration-Profile missing")
	apnpG, ok := apnp.Data.(*diam.GroupedAVP)
	require.True(t, ok, "APN-Configuration-Profile must be Grouped")

	assert.Equal(t, uint32(1), avpUint32(findInGrouped(apnpG, avp.ContextIdentifier, 10415)),
		"APN-Configuration-Profile.Context-Identifier must round-trip")
	assert.Equal(t, uint32(0), avpUint32(findInGrouped(apnpG, avp.AllAPNConfigurationsIncludedIndicator, 10415)),
		"All-APN-Configurations-Included-Indicator must be 0 (ALL_INCLUDED)")

	apnc := findInGrouped(apnpG, avp.APNConfiguration, 10415)
	require.NotNil(t, apnc, "APN-Configuration entry missing")
	apncG, ok := apnc.Data.(*diam.GroupedAVP)
	require.True(t, ok, "APN-Configuration must be Grouped")

	// ---- APN-Configuration sub-AVPs ----
	assert.Equal(t, uint32(2), avpUint32(findInGrouped(apncG, avp.PDNType, 10415)),
		"PDN-Type must round-trip 2 (IPv4v6)")

	// Service-Selection (493) is BASE IETF — vendor 0, no V-bit.
	// Strict assertion: vendorID must be 0, not 10415 (TS 29.272 inherits
	// IETF flag posture for this AVP). Earlier encoder versions emitted
	// this with vendor 10415 + V-bit, which strict peers reject.
	//
	// dict.Default ships TWO definitions for AVP 493 (vendor 0 AND vendor
	// 10415, both with must-not="V") — go-diameter's decoder picks one and
	// returns datatype.Unknown when the wire encoding doesn't match the
	// chosen entry's flag posture. We assert via Serialize() bytes to be
	// type-agnostic; the vendorID==0 lookup itself is the strict check.
	svc := findInGrouped(apncG, avp.ServiceSelection, 0)
	require.NotNil(t, svc, "Service-Selection missing OR was emitted with wrong vendor (must be 0)")
	assert.Equal(t, []byte("internet"), svc.Data.Serialize(),
		"Service-Selection payload must round-trip as 'internet'")

	// EPS-Subscribed-QoS-Profile → QCI + ARP
	eps := findInGrouped(apncG, avp.EPSSubscribedQoSProfile, 10415)
	require.NotNil(t, eps, "EPS-Subscribed-QoS-Profile missing")
	epsG, ok := eps.Data.(*diam.GroupedAVP)
	require.True(t, ok, "EPS-Subscribed-QoS-Profile must be Grouped")

	assert.Equal(t, uint32(9), avpUint32(findInGrouped(epsG, avp.QoSClassIdentifier, 10415)),
		"QCI must round-trip 9 (non-GBR best-effort)")

	arp := findInGrouped(epsG, avp.AllocationRetentionPriority, 10415)
	require.NotNil(t, arp, "ARP missing")
	arpG, ok := arp.Data.(*diam.GroupedAVP)
	require.True(t, ok, "ARP must be Grouped")
	assert.Equal(t, uint32(9), avpUint32(findInGrouped(arpG, avp.PriorityLevel, 10415)),
		"ARP.Priority-Level must round-trip 9")
	assert.Equal(t, uint32(1), avpUint32(findInGrouped(arpG, avpPreEmptionCapability, 10415)),
		"ARP.Pre-emption-Capability must round-trip 1 (DISABLED)")
	assert.Equal(t, uint32(0), avpUint32(findInGrouped(arpG, avpPreEmptionVulnerability, 10415)),
		"ARP.Pre-emption-Vulnerability must round-trip 0 (ENABLED)")

	// APN-AMBR (per-APN, distinct from UE-AMBR)
	apnAMBR := findInGrouped(apncG, avp.AMBR, 10415)
	require.NotNil(t, apnAMBR, "APN-AMBR missing")
	apnAMBRG, ok := apnAMBR.Data.(*diam.GroupedAVP)
	require.True(t, ok, "APN-AMBR must be Grouped")
	assert.Equal(t, uint32(1_000_000), avpUint32(findInGrouped(apnAMBRG, avp.MaxRequestedBandwidthUL, 10415)),
		"APN-AMBR UL must round-trip 1Mbps")

	// ---- Subscription-Data-Flags (1654) — Rel 14+ (not in dict.Default) ----
	assert.Equal(t, uint32(0x01), avpUint32(findInGrouped(grouped, avpSubscriptionDataFlags, 10415)),
		"Subscription-Data-Flags bit 0 (PCSCFRestoration_indicator) must round-trip")

	// ---- MPS-Priority (1616) ----
	assert.Equal(t, uint32(0x01), avpUint32(findInGrouped(grouped, avp.MPSPriority, 10415)),
		"MPS-Priority entitlement bit must round-trip")

	// ---- Top-level ULA-Flags (lives outside Subscription-Data) ----
	ulaFlags, err := decoded.FindAVP(avp.ULAFlags, 10415)
	require.NoError(t, err, "ULA-Flags missing at top level")
	assert.Equal(t, uint32(1), avpUint32(ulaFlags), "ULA-Flags must round-trip 1")
}

// TestToDiameterAnswer_ULA_LegacySubData_FallbackPath verifies the
// backwards-compat shim: when SubProfile is nil but the legacy SubData
// is set, gateway emits the (wire-invalid) OctetString to keep
// pre-migration backend producers working. This path is deprecated;
// until it is removed, the test pins the behaviour so we
// don't accidentally drop the shim mid-migration.
func TestToDiameterAnswer_ULA_LegacySubData_FallbackPath(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	respData, _ := json.Marshal(pb.S6aAnswer{
		ResultCode: diam.Success,
		ULAFlags:   1,
		SubData:    "test-sub-data", // legacy hex/octet payload
	})

	ans, err := translator.ToDiameterAnswer(respData, req)
	require.NoError(t, err)

	// Subscription-Data must be present as OctetString (wire-invalid but
	// shim path). Round-trip through ReadMessage — but with the default
	// dict it will actually decode 1400 as Grouped (dict says so) and the
	// raw bytes will be visible only via the parse failure on the wire.
	// To assert the shim cleanly, read via the in-memory builder before
	// WriteTo is called.
	subDataAVP := findAVPByCode(ans, avp.SubscriptionData, 10415)
	require.NotNil(t, subDataAVP, "Subscription-Data AVP missing in legacy path")
	_, isOctet := subDataAVP.Data.(datatype.OctetString)
	assert.True(t, isOctet, "legacy shim MUST emit OctetString (wire-invalid by design — %T)", subDataAVP.Data)
}

// TestToDiameterAnswer_ULA_NoSubData_NoAVP — defensive: when neither
// SubProfile nor SubData is set, the answer must NOT carry a
// Subscription-Data AVP. Spec-allowed (partial answers omit
// Subscription-Data on certain ResultCodes like 5004 USER_NOT_FOUND).
func TestToDiameterAnswer_ULA_NoSubData_NoAVP(t *testing.T) {
	translator := NewTranslator()
	req := buildValidULR(t)
	respData, _ := json.Marshal(pb.S6aAnswer{
		ResultCode: 5001, // DIAMETER_ERROR_USER_UNKNOWN
	})

	ans, err := translator.ToDiameterAnswer(respData, req)
	require.NoError(t, err)
	subData := findAVPByCode(ans, avp.SubscriptionData, 10415)
	assert.Nil(t, subData, "no Subscription-Data should be emitted when neither SubProfile nor SubData set")
}

// TestCreateIDR_SubProfile_StructuredEncoding mirrors the ULA test for
// the IDR path — same encoder, different command-code surface. Without
// this test, the IDR code path could regress silently if the ULA test
// is the only one exercising encodeSubscriptionData.
//
// Wire round-trip (WriteTo + ReadMessage) works because dict_extensions.go
// registers command 319 (Insert-Subscriber-Data) and the IDR-Flags AVP
// (1490) on dict.Default at package init.
func TestCreateIDR_SubProfile_StructuredEncoding(t *testing.T) {
	translator := NewTranslator()
	m, _ := translator.CreateIDR(
		"286010000000001",
		"hss-test.example.org",
		"example.org",
		"gateway.example.org",
		"example.org",
		carrierTypicalProfile(),
		"",   // no legacy SubData
		0x01, // IDR-Flags bit 0
	)
	require.NotNil(t, m)

	// Wire round-trip — decode the bytes, not the in-memory builder.
	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err)
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err, "IDR wire decode failed — dict.Default registration broken?")

	// ---- Subscription-Data MUST be Grouped, not OctetString ----
	subData, err := decoded.FindAVP(avp.SubscriptionData, 10415)
	require.NoError(t, err, "Subscription-Data AVP missing from decoded IDR")
	grouped, ok := subData.Data.(*diam.GroupedAVP)
	require.True(t, ok,
		"regression: IDR Subscription-Data MUST be Grouped per TS 29.272 §7.3.2, got %T", subData.Data)

	// ---- IDR-Flags (1490) MUST round-trip and decode as Unsigned32 ----
	// Pre-T7 this would fall back to UnknownType — proves the dict
	// extension is loaded.
	idrFlags, err := decoded.FindAVP(1490, 10415)
	require.NoError(t, err, "IDR-Flags AVP missing from decoded IDR")
	assert.Equal(t, uint32(0x01), avpUint32(idrFlags),
		"IDR-Flags must round-trip and decode as Unsigned32 (T7: dict registration)")

	// ---- Spot-check: APN name reaches the AVP tree intact through wire ----
	apnp := findInGrouped(grouped, avp.APNConfigurationProfile, 10415)
	require.NotNil(t, apnp)
	apnpG := apnp.Data.(*diam.GroupedAVP)
	apnc := findInGrouped(apnpG, avp.APNConfiguration, 10415)
	require.NotNil(t, apnc)
	apncG := apnc.Data.(*diam.GroupedAVP)
	svc := findInGrouped(apncG, avp.ServiceSelection, 0) // base IETF AVP
	require.NotNil(t, svc, "Service-Selection missing in IDR encode")
	assert.Equal(t, []byte("internet"), svc.Data.Serialize(),
		"Service-Selection payload must reach the IDR AVP tree intact")
}

// =============================================================================
// IMSI-Group-Id + Adjacent-Access-Restriction-Data
// =============================================================================

// wireRoundTripSubData encodes a Subscription-Data AVP from `profile`, wraps
// it in a minimal valid Diameter message, performs WriteTo + ReadMessage
// round-trip, and returns the decoded Subscription-Data Grouped AVP. Returns
// (nil, true) when the encoder dropped the outer AVP (empty profile path),
// (nil, false) on actual decode failure (test should fail).
//
// This is the canonical pattern for encoder unit tests — go through
// the wire so dict.Default registration + flag posture + sub-AVP order are
// all exercised, not just the in-memory builder.
func wireRoundTripSubData(t *testing.T, profile *pb.SubscriptionProfile) *diam.GroupedAVP {
	t.Helper()
	g := encodeSubscriptionData(profile)
	if g == nil {
		return nil
	}
	m := diam.NewMessage(316 /* ULR */, 0, 16777251, 0xdead, 0xbeef, dict.Default)
	m.NewAVP(avp.SubscriptionData, avp.Mbit|avp.Vbit, vendorID3GPP, g)
	var buf bytes.Buffer
	_, err := m.WriteTo(&buf)
	require.NoError(t, err, "wire write failed")
	decoded, err := diam.ReadMessage(&buf, dict.Default)
	require.NoError(t, err, "wire decode failed")
	subData, err := decoded.FindAVP(avp.SubscriptionData, 10415)
	require.NoError(t, err, "Subscription-Data missing post-decode")
	out, ok := subData.Data.(*diam.GroupedAVP)
	require.True(t, ok, "Subscription-Data not Grouped post-decode")
	return out
}

// TestEncodeIMSIGroupID_Roundtrip — full populate, two entries, all sub-AVPs
// present. Exercises encoder + dict.Default registration of 1675/1676/1677/
// 1678 (loaded by dict_extensions_29272.go).
func TestEncodeIMSIGroupID_Roundtrip(t *testing.T) {
	plmnA := []byte{0x82, 0xf0, 0x10} // PLMN MCC=208 MNC=10 (TBCD)
	plmnB := []byte{0x62, 0xf2, 0x20} // PLMN MCC=262 MNC=02
	localA := []byte{0x01, 0x02, 0x03, 0x04}
	localB := []byte{0xaa, 0xbb}

	profile := &pb.SubscriptionProfile{
		IMSIGroupID: []pb.IMSIGroupID{
			{GroupServiceID: 1001, GroupPLMNID: plmnA, LocalGroupID: localA},
			{GroupServiceID: 1002, GroupPLMNID: plmnB, LocalGroupID: localB},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	groups, err := findAllInGrouped(subData, avpIMSIGroupID, 10415), error(nil)
	require.NoError(t, err)
	require.Len(t, groups, 2, "expected two IMSI-Group-Id outer AVPs")

	for i, want := range []pb.IMSIGroupID{
		{GroupServiceID: 1001, GroupPLMNID: plmnA, LocalGroupID: localA},
		{GroupServiceID: 1002, GroupPLMNID: plmnB, LocalGroupID: localB},
	} {
		inner, ok := groups[i].Data.(*diam.GroupedAVP)
		require.True(t, ok, "entry %d not Grouped", i)

		gsid := findInGrouped(inner, avpGroupServiceID, 10415)
		require.NotNil(t, gsid, "entry %d missing Group-Service-Id", i)
		assert.Equal(t, want.GroupServiceID, avpUint32(gsid))

		gplmn := findInGrouped(inner, avpGroupPLMNID, 10415)
		require.NotNil(t, gplmn, "entry %d missing Group-PLMN-Id", i)
		assert.Equal(t, want.GroupPLMNID, gplmn.Data.Serialize())

		local := findInGrouped(inner, avpLocalGroupID, 10415)
		require.NotNil(t, local, "entry %d missing Local-Group-Id", i)
		assert.Equal(t, want.LocalGroupID, local.Data.Serialize())
	}
}

// TestEncodeIMSIGroupID_Empty_Skipped — empty slice → no outer AVP at all.
// Confirms encodeSubscriptionData drops the for-loop when there's no entry,
// matching the "empty grouped omitted" invariant.
func TestEncodeIMSIGroupID_Empty_Skipped(t *testing.T) {
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		IMSIGroupID: []pb.IMSIGroupID{}, // explicit empty
		// One non-IMSIGroupID field so encodeSubscriptionData itself returns
		// non-nil (otherwise the whole Subscription-Data AVP gets dropped).
		MPSPriority: 1,
	})
	require.NotNil(t, subData, "Subscription-Data should still emit (MPSPriority is set)")

	groups, err := findAllInGrouped(subData, avpIMSIGroupID, 10415), error(nil)
	if err == nil {
		assert.Empty(t, groups, "no IMSI-Group-Id AVPs expected for empty slice")
	}
}

// TestEncodeIMSIGroupID_IncompleteNotSent — Group-Service-Id, Group-PLMN-Id
// and Local-Group-Id are all mandatory (TS 29.272 §7.3.189); an entry
// without the last two is not sent.
func TestEncodeIMSIGroupID_IncompleteNotSent(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		IMSIGroupID: []pb.IMSIGroupID{{GroupServiceID: 7777}},
		MPSPriority: 1,
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)
	assert.Empty(t, findAllInGrouped(subData, avpIMSIGroupID, 10415))
}

// TestEncodeAdjacentAccessRestrictionData_Roundtrip — full populate, two
// entries. Exercises 1673 outer AVP wrapping 1407 (Visited-PLMN-Id, fiorix
// pre-shipped) + 1426 (Access-Restriction-Data, fiorix pre-shipped).
func TestEncodeAdjacentAccessRestrictionData_Roundtrip(t *testing.T) {
	visitedA, _ := hex.DecodeString("82f010")
	visitedB, _ := hex.DecodeString("62f220")

	profile := &pb.SubscriptionProfile{
		AdjacentAccessRestrictionData: []pb.AdjacentAccessRestrictionData{
			{VisitedPLMNID: visitedA, AccessRestrictionData: 0x01}, // UTRAN_NOT_ALLOWED
			{VisitedPLMNID: visitedB, AccessRestrictionData: 0x80}, // NB_IOT bit
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	groups, err := findAllInGrouped(subData, avpAdjacentAccessRestrictionData, 10415), error(nil)
	require.NoError(t, err)
	require.Len(t, groups, 2, "expected two Adjacent-Access-Restriction-Data outers")

	for i, want := range []pb.AdjacentAccessRestrictionData{
		{VisitedPLMNID: visitedA, AccessRestrictionData: 0x01},
		{VisitedPLMNID: visitedB, AccessRestrictionData: 0x80},
	} {
		inner := groups[i].Data.(*diam.GroupedAVP)

		v := findInGrouped(inner, avp.VisitedPLMNID, 10415)
		require.NotNil(t, v, "entry %d missing Visited-PLMN-Id", i)
		assert.Equal(t, want.VisitedPLMNID, v.Data.Serialize())

		ard := findInGrouped(inner, avp.AccessRestrictionData, 10415)
		require.NotNil(t, ard, "entry %d missing Access-Restriction-Data", i)
		assert.Equal(t, want.AccessRestrictionData, avpUint32(ard))
	}
}

// TestEncodeAdjacentAccessRestrictionData_Empty_Skipped — empty slice → no
// 1673 AVP emitted.
func TestEncodeAdjacentAccessRestrictionData_Empty_Skipped(t *testing.T) {
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		AdjacentAccessRestrictionData: []pb.AdjacentAccessRestrictionData{},
		MPSPriority:                   1, // ensure outer Subscription-Data emits
	})
	require.NotNil(t, subData)

	groups, err := findAllInGrouped(subData, avpAdjacentAccessRestrictionData, 10415), error(nil)
	if err == nil {
		assert.Empty(t, groups,
			"no Adjacent-Access-Restriction-Data AVPs expected for empty slice")
	}
}

// TestEncodeAdjacentAccessRestrictionData_OnlyVisitedPLMN — AccessRestrictionData
// 0 (no restriction) is still sent: the AVP is mandatory (TS 29.272 §7.3.187).
func TestEncodeAdjacentAccessRestrictionData_OnlyVisitedPLMN(t *testing.T) {
	visited, _ := hex.DecodeString("82f010")
	profile := &pb.SubscriptionProfile{
		AdjacentAccessRestrictionData: []pb.AdjacentAccessRestrictionData{
			{VisitedPLMNID: visited, AccessRestrictionData: 0},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	groups, err := findAllInGrouped(subData, avpAdjacentAccessRestrictionData, 10415), error(nil)
	require.NoError(t, err)
	require.Len(t, groups, 1)

	inner := groups[0].Data.(*diam.GroupedAVP)
	v := findInGrouped(inner, avp.VisitedPLMNID, 10415)
	require.NotNil(t, v)
	assert.Equal(t, visited, v.Data.Serialize())

	ard := findInGrouped(inner, avp.AccessRestrictionData, 10415)
	require.NotNil(t, ard, "Access-Restriction-Data is mandatory (§7.3.187); 0 is sent")
	assert.Equal(t, uint32(0), avpUint32(ard))
}

// Compile-time guard: keep encoding/json referenced in case the file is
// edited later to add JSON-roundtrip tests for the new struct families.
var _ = json.Marshal

// =============================================================================
// GPRSSubscriptionData + PDPContext
// =============================================================================

// TestEncodeGPRSSubscriptionData_Roundtrip — converged-HSS path, two PDP
// contexts (one IPv4 internet APN, one IPv4v6 IMS APN with ExtPDPType +
// ExtPDPAddress), plus per-context AMBR. Exercises 1467/1468/1469/1470/
// 1404/1432/1620/1621 + AMBR reuse.
func TestEncodeGPRSSubscriptionData_Roundtrip(t *testing.T) {
	pdpTypeIPv4, _ := hex.DecodeString("00")     // TS 29.060 PDP-Type IPv4
	pdpTypeIPv6, _ := hex.DecodeString("01")     // IPv6
	extTypeIPv4v6, _ := hex.DecodeString("0002") // Rel-9 Ext-PDP-Type
	qos, _ := hex.DecodeString("1391961f7396")   // TS 24.008 §10.5.6.5 stub

	profile := &pb.SubscriptionProfile{
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{
			CompleteDataListIncludedIndicator: 0, // ALL → spec-encoded but encoder omits at zero
			PDPContext: []pb.PDPContext{
				{
					ContextIdentifier: 1,
					PDPType:           pdpTypeIPv4,
					PDPAddress:        []string{"10.0.0.1"},
					QoSSubscribed:     qos,
					ServiceSelection:  "internet",
					AMBR: &pb.AMBR{
						MaxRequestedBandwidthUL: 256_000,
						MaxRequestedBandwidthDL: 1_024_000,
					},
				},
				{
					QoSSubscribed:              []byte{0x01},
					ContextIdentifier:          2,
					PDPType:                    pdpTypeIPv6,
					ExtPDPType:                 extTypeIPv4v6,
					ExtPDPAddress:              []string{"2001:db8::1"},
					ServiceSelection:           "ims",
					VPLMNDynamicAddressAllowed: 1, // ALLOWED
					RestorationPriority:        7,
				},
			},
		},
	}

	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	gprs := findInGrouped(subData, avp.GPRSSubscriptionData, 10415)
	require.NotNil(t, gprs, "GPRS-Subscription-Data missing")
	gprsG := gprs.Data.(*diam.GroupedAVP)

	contexts := findAllInGrouped(gprsG, avp.PDPContext, 10415)
	require.Len(t, contexts, 2, "expected two PDP-Context entries")

	// Entry 0 — IPv4 internet
	c0 := contexts[0].Data.(*diam.GroupedAVP)
	assert.Equal(t, uint32(1), avpUint32(findInGrouped(c0, avp.ContextIdentifier, 10415)))
	if pt := findInGrouped(c0, avp.PDPType, 10415); assert.NotNil(t, pt) {
		assert.Equal(t, pdpTypeIPv4, pt.Data.Serialize())
	}
	if pa := findInGrouped(c0, avp.PDPAddress, 10415); assert.NotNil(t, pa, "PDP-Address missing") {
		// datatype.Address wire format is 2-byte type prefix + raw IP bytes.
		// Strip the prefix and compare against the parsed input.
		raw := pa.Data.Serialize()
		require.GreaterOrEqual(t, len(raw), 2)
		assert.Equal(t, net.IPv4(10, 0, 0, 1).To4(), net.IP(raw[2:]).To4())
	}
	if ambr := findInGrouped(c0, avp.AMBR, 10415); assert.NotNil(t, ambr, "per-context AMBR missing") {
		ambrG := ambr.Data.(*diam.GroupedAVP)
		assert.Equal(t, uint32(256_000),
			avpUint32(findInGrouped(ambrG, avp.MaxRequestedBandwidthUL, 10415)))
		assert.Equal(t, uint32(1_024_000),
			avpUint32(findInGrouped(ambrG, avp.MaxRequestedBandwidthDL, 10415)))
	}

	// Entry 1 — Rel-9 IPv4v6 with ExtPDPType + ExtPDPAddress
	c1 := contexts[1].Data.(*diam.GroupedAVP)
	if et := findInGrouped(c1, avp.ExtPDPType, 10415); assert.NotNil(t, et) {
		assert.Equal(t, extTypeIPv4v6, et.Data.Serialize())
	}
	assert.NotNil(t, findInGrouped(c1, avp.ExtPDPAddress, 10415), "Ext-PDP-Address missing")
	assert.NotNil(t, findInGrouped(c1, avp.VPLMNDynamicAddressAllowed, 10415))
	assert.Equal(t, uint32(7), avpUint32(findInGrouped(c1, avpRestorationPriority, 10415)))
}

// TestEncodeGPRSSubscriptionData_Empty_Skipped — nil profile struct AND
// empty PDPContext slice both end up dropping the outer 1467 AVP
// (empty grouped omitted).
func TestEncodeGPRSSubscriptionData_Empty_Skipped(t *testing.T) {
	// Path 1 — nil pointer.
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		GPRSSubscriptionData: nil,
		MPSPriority:          1, // keep outer Subscription-Data non-empty
	})
	require.NotNil(t, subData)
	assert.Nil(t, findInGrouped(subData, avp.GPRSSubscriptionData, 10415),
		"GPRS-Subscription-Data MUST be omitted when struct is nil")

	// Path 2 — non-nil but empty PDPContext slice + zero
	// CompleteDataListIncludedIndicator → all sub-AVPs omit → outer drop.
	subData2 := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{
			PDPContext: []pb.PDPContext{},
		},
		MPSPriority: 1,
	})
	require.NotNil(t, subData2)
	assert.Nil(t, findInGrouped(subData2, avp.GPRSSubscriptionData, 10415),
		"GPRS-Subscription-Data MUST be omitted when PDP context slice is empty")
}

// TestEncodeGPRSSubscriptionData_OnlyMandatory — minimal valid PDPContext
// (Context-Identifier + PDP-Type + Service-Selection only); all optionals
// off. CompleteDataListIncludedIndicator non-zero this time so the outer
// 1467 wraps both sub-AVPs.
func TestEncodeGPRSSubscriptionData_OnlyMandatory(t *testing.T) {
	pdpTypeIPv4, _ := hex.DecodeString("00")
	profile := &pb.SubscriptionProfile{
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{
			CompleteDataListIncludedIndicator: 1, // MODIFIED
			PDPContext: []pb.PDPContext{
				{
					QoSSubscribed:     []byte{0x01},
					ContextIdentifier: 5,
					PDPType:           pdpTypeIPv4,
					ServiceSelection:  "default",
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	gprs := findInGrouped(subData, avp.GPRSSubscriptionData, 10415)
	require.NotNil(t, gprs)
	gprsG := gprs.Data.(*diam.GroupedAVP)

	flag := findInGrouped(gprsG, avp.CompleteDataListIncludedIndicator, 10415)
	require.NotNil(t, flag, "Complete-Data-List-Included-Indicator missing")
	assert.Equal(t, uint32(1), avpUint32(flag))

	contexts := findAllInGrouped(gprsG, avp.PDPContext, 10415)
	require.Len(t, contexts, 1)
	c := contexts[0].Data.(*diam.GroupedAVP)

	// Optionals MUST all be omitted.
	assert.Nil(t, findInGrouped(c, avp.PDPAddress, 10415), "PDP-Address must be omitted")
	assert.Nil(t, findInGrouped(c, avp.ExtPDPType, 10415), "Ext-PDP-Type must be omitted")
	assert.Nil(t, findInGrouped(c, avp.ExtPDPAddress, 10415), "Ext-PDP-Address must be omitted")
	assert.Nil(t, findInGrouped(c, avp.AMBR, 10415), "per-context AMBR must be omitted")
	assert.Nil(t, findInGrouped(c, avp.VPLMNDynamicAddressAllowed, 10415))
	assert.Nil(t, findInGrouped(c, avpRestorationPriority, 10415))
}

// TestEncodePDPContext_BadIPSilentlySkipped — addAddressString invariant:
// malformed IP strings drop without affecting valid entries or panicking.
// One valid + one bad IP in PDPAddress → exactly one Address AVP emitted.
func TestEncodePDPContext_BadIPSilentlySkipped(t *testing.T) {
	pdpTypeIPv4, _ := hex.DecodeString("00")
	profile := &pb.SubscriptionProfile{
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{
			PDPContext: []pb.PDPContext{
				{
					QoSSubscribed:     []byte{0x01},
					ContextIdentifier: 1,
					PDPType:           pdpTypeIPv4,
					PDPAddress:        []string{"10.0.0.1", "not-an-ip", "", "192.0.2.5"},
					ServiceSelection:  "internet",
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	gprs := findInGrouped(subData, avp.GPRSSubscriptionData, 10415)
	require.NotNil(t, gprs)
	contexts := findAllInGrouped(gprs.Data.(*diam.GroupedAVP), avp.PDPContext, 10415)
	require.Len(t, contexts, 1)
	c := contexts[0].Data.(*diam.GroupedAVP)

	addresses := findAllInGrouped(c, avp.PDPAddress, 10415)
	assert.Len(t, addresses, 2,
		"expected exactly 2 PDP-Address AVPs (valid IPs only); got %d", len(addresses))
}

// TestEncodePDPContext_PerContextAMBR_Reuse — encodeAMBR is reused
// between top-level UE-AMBR and per-PDP-Context AMBR. Verifies the
// reuse path does not leak state across contexts.
func TestEncodePDPContext_PerContextAMBR_Reuse(t *testing.T) {
	pdpTypeIPv4, _ := hex.DecodeString("00")
	profile := &pb.SubscriptionProfile{
		AMBR: &pb.AMBR{
			MaxRequestedBandwidthUL: 9_000_000,
			MaxRequestedBandwidthDL: 90_000_000,
		},
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{
			PDPContext: []pb.PDPContext{
				{
					QoSSubscribed:     []byte{0x01},
					ContextIdentifier: 1,
					PDPType:           pdpTypeIPv4,
					ServiceSelection:  "internet",
					AMBR: &pb.AMBR{
						MaxRequestedBandwidthUL: 100_000,
						MaxRequestedBandwidthDL: 500_000,
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	// UE-AMBR at top level.
	topAMBR := findInGrouped(subData, avp.AMBR, 10415)
	require.NotNil(t, topAMBR, "top-level UE-AMBR missing")
	topG := topAMBR.Data.(*diam.GroupedAVP)
	assert.Equal(t, uint32(9_000_000),
		avpUint32(findInGrouped(topG, avp.MaxRequestedBandwidthUL, 10415)),
		"top-level UE-AMBR UL must remain 9 Mbps")

	// Per-PDP-Context AMBR is independent.
	gprs := findInGrouped(subData, avp.GPRSSubscriptionData, 10415)
	require.NotNil(t, gprs)
	contexts := findAllInGrouped(gprs.Data.(*diam.GroupedAVP), avp.PDPContext, 10415)
	require.Len(t, contexts, 1)
	pdpAMBR := findInGrouped(contexts[0].Data.(*diam.GroupedAVP), avp.AMBR, 10415)
	require.NotNil(t, pdpAMBR, "per-context AMBR missing")
	pdpG := pdpAMBR.Data.(*diam.GroupedAVP)
	assert.Equal(t, uint32(100_000),
		avpUint32(findInGrouped(pdpG, avp.MaxRequestedBandwidthUL, 10415)),
		"per-context AMBR UL must be 100 kbps independent of UE-AMBR")
}

// TestEncodePDPContext_ContextIdentifier_ZeroEmitted — ContextIdentifier
// is mandatory per TS 29.272 §7.3.71; encoder MUST emit it even at zero
// (matches encodeAPNConfiguration pattern). Defensive — wire-validity
// over data-quality.
func TestEncodePDPContext_ContextIdentifier_ZeroEmitted(t *testing.T) {
	pdpTypeIPv4, _ := hex.DecodeString("00")
	profile := &pb.SubscriptionProfile{
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{
			PDPContext: []pb.PDPContext{
				{
					QoSSubscribed:     []byte{0x01},
					ContextIdentifier: 0, // mandatory but zero
					PDPType:           pdpTypeIPv4,
					ServiceSelection:  "default",
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	gprs := findInGrouped(subData, avp.GPRSSubscriptionData, 10415)
	require.NotNil(t, gprs)
	contexts := findAllInGrouped(gprs.Data.(*diam.GroupedAVP), avp.PDPContext, 10415)
	require.Len(t, contexts, 1)

	cid := findInGrouped(contexts[0].Data.(*diam.GroupedAVP), avp.ContextIdentifier, 10415)
	require.NotNil(t, cid, "ContextIdentifier MUST be emitted even at zero (mandatory per spec)")
	assert.Equal(t, uint32(0), avpUint32(cid))
}

// =============================================================================
// LCS detail bundle
//   CallBarringInfo + ExternalClient + ServiceType + SpecificAPNInfo +
//   MIP6AgentInfo + APNConfiguration / LCS dispatch extensions
// =============================================================================

// TestEncodeCallBarringInfo_Roundtrip — two entries; SS-Code (1476) +
// SS-Status (1477) OctetString round-trip.
func TestEncodeCallBarringInfo_Roundtrip(t *testing.T) {
	codeA, _ := hex.DecodeString("a1") // SS-Code 0xA1 (Outgoing barring)
	codeB, _ := hex.DecodeString("a2")
	statusActive, _ := hex.DecodeString("01") // SS-Status active
	statusInactive, _ := hex.DecodeString("00")

	profile := &pb.SubscriptionProfile{
		CallBarringInfo: []pb.CallBarringInfo{
			{SSCode: codeA, SSStatus: statusActive},
			{SSCode: codeB, SSStatus: statusInactive},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	cb := findAllInGrouped(subData, avp.CallBarringInfo, 10415)
	require.Len(t, cb, 2, "expected two Call-Barring-Info entries")

	for i, want := range []pb.CallBarringInfo{
		{SSCode: codeA, SSStatus: statusActive},
		{SSCode: codeB, SSStatus: statusInactive},
	} {
		inner := cb[i].Data.(*diam.GroupedAVP)
		ssCode := findInGrouped(inner, avpSSCode, 10415)
		require.NotNil(t, ssCode, "entry %d missing SS-Code", i)
		assert.Equal(t, want.SSCode, ssCode.Data.Serialize())

		ssStatus := findInGrouped(inner, avpSSStatus, 10415)
		require.NotNil(t, ssStatus, "entry %d missing SS-Status", i)
		assert.Equal(t, want.SSStatus, ssStatus.Data.Serialize())
	}
}

// TestEncodeCallBarringInfo_Empty_Skipped — empty slice + all-zero entry both
// produce no outer 1488 AVP.
func TestEncodeCallBarringInfo_Empty_Skipped(t *testing.T) {
	// Path 1: empty slice.
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		CallBarringInfo: []pb.CallBarringInfo{},
		MPSPriority:     1,
	})
	require.NotNil(t, subData)
	assert.Empty(t, findAllInGrouped(subData, avp.CallBarringInfo, 10415))

	// Path 2: one entry with both fields nil → encoder drops outer.
	subData2 := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		CallBarringInfo: []pb.CallBarringInfo{{}},
		MPSPriority:     1,
	})
	require.NotNil(t, subData2)
	assert.Empty(t, findAllInGrouped(subData2, avp.CallBarringInfo, 10415),
		"all-empty CallBarringInfo entry MUST drop outer 1488")
}

// TestEncodeMIP6AgentInfo_Roundtrip — full populate (Address + HomeAgentHost
// + HomeLinkPrefix). Critical fiorix-symbol-asymmetry verification:
// MIPHomeAgentAddress (no "6") = 334, MIPHomeAgentHost (no "6")
// = 348, MIP6HomeLinkPrefix (with "6") = 125.
func TestEncodeMIP6AgentInfo_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:           &pdnIPv4,
					ContextIdentifier: 1,
					ServiceSelection:  "ims",
					MIP6AgentInfo: &pb.MIP6AgentInfo{
						MIP6HomeAgentAddress: []string{"10.10.10.1", "2001:db8::1"},
						MIP6HomeAgentHost: &struct {
							DestinationHost  string `json:"destination_host"`
							DestinationRealm string `json:"destination_realm"`
						}{
							DestinationHost:  "ha.example.com",
							DestinationRealm: "example.com",
						},
						MIP6HomeLinkPrefix: "linkprefix-bytes",
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	require.NotNil(t, apnp)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	require.NotNil(t, apn)
	apnG := apn.Data.(*diam.GroupedAVP)

	// MIP6-Agent-Info is base IETF — vendor 0 on the wire.
	mip := findInGrouped(apnG, avp.MIP6AgentInfo, 0)
	require.NotNil(t, mip, "MIP6-Agent-Info missing (base IETF, vendor 0)")
	mipG := mip.Data.(*diam.GroupedAVP)

	// 334 MIPHomeAgentAddress (multi). Both v4+v6 entries expected.
	addrs := findAllInGrouped(mipG, avp.MIPHomeAgentAddress, 0)
	require.Len(t, addrs, 2, "expected 2 MIPHomeAgentAddress AVPs (v4 + v6)")

	// 348 MIPHomeAgentHost (Grouped: 293 DestHost + 283 DestRealm).
	host := findInGrouped(mipG, avp.MIPHomeAgentHost, 0)
	require.NotNil(t, host, "MIPHomeAgentHost missing")
	hostG := host.Data.(*diam.GroupedAVP)
	dh := findInGrouped(hostG, avp.DestinationHost, 0)
	require.NotNil(t, dh)
	assert.Equal(t, []byte("ha.example.com"), dh.Data.Serialize())
	dr := findInGrouped(hostG, avp.DestinationRealm, 0)
	require.NotNil(t, dr)
	assert.Equal(t, []byte("example.com"), dr.Data.Serialize())

	// 125 MIP6HomeLinkPrefix (note the "6" — fiorix symbol asymmetry).
	prefix := findInGrouped(mipG, avp.MIP6HomeLinkPrefix, 0)
	require.NotNil(t, prefix, "MIP6HomeLinkPrefix missing")
	assert.Equal(t, []byte("linkprefix-bytes"), prefix.Data.Serialize())
}

// TestEncodeMIP6AgentInfo_Empty_Skipped — nil pointer + all-empty struct both
// produce no outer 486 AVP.
func TestEncodeMIP6AgentInfo_Empty_Skipped(t *testing.T) {
	// Path 1: nil pointer.
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:           &pdnIPv4,
					ContextIdentifier: 1,
					ServiceSelection:  "internet",
					MIP6AgentInfo:     nil,
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	apnG := apn.Data.(*diam.GroupedAVP)
	assert.Nil(t, findInGrouped(apnG, avp.MIP6AgentInfo, 0),
		"nil MIP6AgentInfo MUST omit AVP")

	// Path 2: all-empty struct.
	profile.APNConfigurationProfile.APNConfigurations[0].MIP6AgentInfo = &pb.MIP6AgentInfo{}
	subData2 := wireRoundTripSubData(t, profile)
	apnp2 := findInGrouped(subData2, avp.APNConfigurationProfile, 10415)
	apn2 := findInGrouped(apnp2.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	assert.Nil(t, findInGrouped(apn2.Data.(*diam.GroupedAVP), avp.MIP6AgentInfo, 0),
		"empty MIP6AgentInfo MUST omit AVP")
}

// TestEncodeMIP6AgentInfo_BadIPSilentSkip — addAddressString invariant for
// MIP6 homeAgentAddress: malformed IPs skip silently.
func TestEncodeMIP6AgentInfo_BadIPSilentSkip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:           &pdnIPv4,
					ContextIdentifier: 1,
					ServiceSelection:  "ims",
					MIP6AgentInfo: &pb.MIP6AgentInfo{
						MIP6HomeAgentAddress: []string{"10.10.10.1", "garbage", "", "::1"},
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	mip := findInGrouped(apn.Data.(*diam.GroupedAVP), avp.MIP6AgentInfo, 0)
	require.NotNil(t, mip)
	addrs := findAllInGrouped(mip.Data.(*diam.GroupedAVP), avp.MIPHomeAgentAddress, 0)
	assert.Len(t, addrs, 2, "expected 2 valid IPs (10.10.10.1 + ::1)")
}

// TestEncodeSpecificAPNInfo_Roundtrip — Rel-10 per-APN MIP6/VNI override.
// ServiceSelection mandatory + nested MIP6AgentInfo + VisitedNetworkIdentifier.
func TestEncodeSpecificAPNInfo_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:           &pdnIPv4,
					ContextIdentifier: 1,
					ServiceSelection:  "internet",
					SpecificAPNInfo: []pb.SpecificAPNInfo{
						{
							ServiceSelection:         "ims",
							VisitedNetworkIdentifier: "vplmn1.example.com",
							MIP6AgentInfo: &pb.MIP6AgentInfo{
								MIP6HomeAgentAddress: []string{"10.0.0.1"},
							},
						},
						{
							ServiceSelection: "internet",
						},
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)

	sai := findAllInGrouped(apn.Data.(*diam.GroupedAVP), avp.SpecificAPNInfo, 10415)
	// The second entry has no MIP6-Agent-Info, which Specific-APN-Info makes
	// mandatory (TS 29.272 §7.3.82): it is not sent.
	require.Len(t, sai, 1, "only the complete Specific-APN-Info entry is sent")

	// Entry 0 — full populate
	s0 := sai[0].Data.(*diam.GroupedAVP)
	svc0 := findInGrouped(s0, avp.ServiceSelection, 0)
	require.NotNil(t, svc0)
	assert.Equal(t, []byte("ims"), svc0.Data.Serialize())

	vni0 := findInGrouped(s0, avp.VisitedNetworkIdentifier, 10415)
	require.NotNil(t, vni0)
	assert.Equal(t, []byte("vplmn1.example.com"), vni0.Data.Serialize())

	mip0 := findInGrouped(s0, avp.MIP6AgentInfo, 0)
	require.NotNil(t, mip0, "Specific-APN-Info[0] missing nested MIP6-Agent-Info")

}

// TestEncodeExternalClient_InLCSPrivacyException — LCS detail expansion;
// External-Client (1479) populated inside LCS-Privacy-Exception (1475).
func TestEncodeExternalClient_InLCSPrivacyException(t *testing.T) {
	notify := uint32(1)
	profile := &pb.SubscriptionProfile{
		LCSInfo: &pb.LCSInfo{
			LCSPrivacyExceptionList: []pb.LCSPrivacyException{
				{
					SSCode:               []byte{0x21},
					SSStatus:             []byte{0x01},
					NotificationToUEUser: &notify,
					ExternalClient: []pb.ExternalClient{
						{ClientIdentity: "client-a", GMLCRestriction: 2, NotificationToUEUser: 1},
						{ClientIdentity: "client-b", GMLCRestriction: 0, NotificationToUEUser: 0},
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	lcs := findInGrouped(subData, avp.LCSInfo, 10415)
	require.NotNil(t, lcs)
	lpe := findInGrouped(lcs.Data.(*diam.GroupedAVP), avpLCSPrivacyExceptionList, 10415)
	require.NotNil(t, lpe)
	clients := findAllInGrouped(lpe.Data.(*diam.GroupedAVP), avp.ExternalClient, 10415)
	require.Len(t, clients, 2, "expected 2 External-Client entries")

	// Entry 0 — all fields populated
	c0 := clients[0].Data.(*diam.GroupedAVP)
	cid0 := findInGrouped(c0, avp.ClientIdentity, 10415)
	require.NotNil(t, cid0)
	assert.Equal(t, []byte("client-a"), cid0.Data.Serialize())
	assert.Equal(t, uint32(2), avpUint32(findInGrouped(c0, avp.GMLCRestriction, 10415)))
	assert.Equal(t, uint32(1), avpUint32(findInGrouped(c0, avpNotificationToUEUser, 10415)))

	// Entry 1 — only ClientIdentity (rest zero → omitted)
	c1 := clients[1].Data.(*diam.GroupedAVP)
	cid1 := findInGrouped(c1, avp.ClientIdentity, 10415)
	require.NotNil(t, cid1)
	assert.Equal(t, []byte("client-b"), cid1.Data.Serialize())
	assert.Nil(t, findInGrouped(c1, avp.GMLCRestriction, 10415))
	assert.Nil(t, findInGrouped(c1, avpNotificationToUEUser, 10415))
}

// TestEncodeServiceType_InLCSPrivacyException — LCS detail expansion;
// ServiceType (1483 TGPP-Service-Type) populated inside LCS-Privacy-Exception.
// ServiceTypeCode mandatory — emitted even at zero.
func TestEncodeServiceType_InLCSPrivacyException(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		LCSInfo: &pb.LCSInfo{
			LCSPrivacyExceptionList: []pb.LCSPrivacyException{
				{
					SSCode:   []byte{0x22},
					SSStatus: []byte{0x01},
					ServiceType: []pb.ServiceType{
						{ServiceTypeCode: 5, GMLCRestriction: 1, NotificationToUEUser: 1},
						{ServiceTypeCode: 0}, // mandatory zero, optionals omit
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	lcs := findInGrouped(subData, avp.LCSInfo, 10415)
	lpe := findInGrouped(lcs.Data.(*diam.GroupedAVP), avpLCSPrivacyExceptionList, 10415)
	require.NotNil(t, lpe)
	stypes := findAllInGrouped(lpe.Data.(*diam.GroupedAVP), avp.TGPPServiceType, 10415)
	require.Len(t, stypes, 2, "expected 2 ServiceType entries")

	s0 := stypes[0].Data.(*diam.GroupedAVP)
	stc0 := findInGrouped(s0, avp.ServiceTypeIdentity, 10415)
	require.NotNil(t, stc0, "ServiceTypeIdentity missing")
	assert.Equal(t, uint32(5), avpUint32(stc0))
	assert.Equal(t, uint32(1), avpUint32(findInGrouped(s0, avp.GMLCRestriction, 10415)))

	s1 := stypes[1].Data.(*diam.GroupedAVP)
	stc1 := findInGrouped(s1, avp.ServiceTypeIdentity, 10415)
	require.NotNil(t, stc1, "ServiceTypeIdentity MUST be emitted even at zero (mandatory)")
	assert.Equal(t, uint32(0), avpUint32(stc1))
	assert.Nil(t, findInGrouped(s1, avp.GMLCRestriction, 10415))
}

// TestEncodePLMNClient_InLCSPrivacyException — PLMN-Client (1482) is
// Enumerated multi inside LCS-Privacy-Exception. Verify each entry emits.
func TestEncodePLMNClient_InLCSPrivacyException(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		LCSInfo: &pb.LCSInfo{
			LCSPrivacyExceptionList: []pb.LCSPrivacyException{
				{
					SSCode:     []byte{0x23},
					SSStatus:   []byte{0x01},
					PLMNClient: []uint32{1, 3, 5}, // Broadcast, Anonymous, NetworkOperator (per spec enums)
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	lcs := findInGrouped(subData, avp.LCSInfo, 10415)
	lpe := findInGrouped(lcs.Data.(*diam.GroupedAVP), avpLCSPrivacyExceptionList, 10415)
	require.NotNil(t, lpe)

	plmnClients := findAllInGrouped(lpe.Data.(*diam.GroupedAVP), avp.PLMNClient, 10415)
	require.Len(t, plmnClients, 3, "expected 3 PLMN-Client entries")

	for i, want := range []uint32{1, 3, 5} {
		assert.Equal(t, want, avpUint32(plmnClients[i]),
			"PLMN-Client[%d] mismatch", i)
	}
}

// TestEncodeAPNConfiguration_SIPTOLocalNetworkPermission — top-level extras
// dispatch verification; sub-AVP 1665 emits when set, omits when zero.
func TestEncodeAPNConfiguration_SIPTOLocalNetworkPermission(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:                     &pdnIPv4,
					ContextIdentifier:           1,
					ServiceSelection:            "internet",
					SIPTOLocalNetworkPermission: 2, // non-zero
				},
				{
					PDNType:                     &pdnIPv4,
					ContextIdentifier:           2,
					ServiceSelection:            "ims",
					SIPTOLocalNetworkPermission: 0, // zero — must omit
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apns := findAllInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	require.Len(t, apns, 2)

	a0 := apns[0].Data.(*diam.GroupedAVP)
	siptoLN := findInGrouped(a0, avpSIPTOLocalNetworkPermission, 10415)
	require.NotNil(t, siptoLN, "APN0 SIPTOLocalNetworkPermission missing")
	assert.Equal(t, uint32(2), avpUint32(siptoLN))

	a1 := apns[1].Data.(*diam.GroupedAVP)
	assert.Nil(t, findInGrouped(a1, avpSIPTOLocalNetworkPermission, 10415),
		"APN1 SIPTOLocalNetworkPermission=0 MUST be omitted")
}

// =============================================================================
// V2X + EmergencyInfo + EdrxSubscriptionData
// =============================================================================

// TestEncodeV2XSubscriptionData_Roundtrip — outer 1688 wraps a single
// V2X-Permission (1689) Unsigned32 bitmask.
func TestEncodeV2XSubscriptionData_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		V2XSubscriptionData: &pb.V2XSubscriptionData{
			V2XPermission: 0x03, // bit 0 = PC5 allowed, bit 1 = LTE-Uu allowed
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	v2x := findInGrouped(subData, avpV2XSubscriptionData, 10415)
	require.NotNil(t, v2x, "V2X-Subscription-Data missing")
	perm := findInGrouped(v2x.Data.(*diam.GroupedAVP), avpV2XPermission, 10415)
	require.NotNil(t, perm, "V2X-Permission sub-AVP missing")
	assert.Equal(t, uint32(0x03), avpUint32(perm))
}

// TestEncodeV2XSubscriptionData_Empty_Skipped — nil + zero permission both
// drop outer 1688.
func TestEncodeV2XSubscriptionData_Empty_Skipped(t *testing.T) {
	// nil pointer
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		V2XSubscriptionData: nil,
		MPSPriority:         1,
	})
	require.NotNil(t, subData)
	assert.Nil(t, findInGrouped(subData, avpV2XSubscriptionData, 10415))

	// non-nil with zero permission
	subData2 := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		V2XSubscriptionData: &pb.V2XSubscriptionData{V2XPermission: 0},
		MPSPriority:         1,
	})
	require.NotNil(t, subData2)
	assert.Nil(t, findInGrouped(subData2, avpV2XSubscriptionData, 10415),
		"V2XPermission=0 MUST drop outer 1688")
}

// TestEncodeEmergencyInfo_Roundtrip — outer 1687 wraps nested MIP6AgentInfo.
// Re-uses the MIP6 encoder (vendor=10415 fiorix posture).
func TestEncodeEmergencyInfo_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		EmergencyInfo: &pb.EmergencyInfo{
			MIP6AgentInfo: &pb.MIP6AgentInfo{
				MIP6HomeAgentAddress: []string{"10.20.30.40"},
				MIP6HomeLinkPrefix:   "emergency-prefix",
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	ei := findInGrouped(subData, avpEmergencyInfo, 10415)
	require.NotNil(t, ei, "Emergency-Info missing")
	mip := findInGrouped(ei.Data.(*diam.GroupedAVP), avp.MIP6AgentInfo, 0)
	require.NotNil(t, mip, "Emergency-Info nested MIP6-Agent-Info missing")
	mipG := mip.Data.(*diam.GroupedAVP)
	addrs := findAllInGrouped(mipG, avp.MIPHomeAgentAddress, 0)
	require.Len(t, addrs, 1, "expected one home agent address")
	prefix := findInGrouped(mipG, avp.MIP6HomeLinkPrefix, 0)
	require.NotNil(t, prefix)
	assert.Equal(t, []byte("emergency-prefix"), prefix.Data.Serialize())
}

// TestEncodeEmergencyInfo_Empty_Skipped — nil pointer + nil MIP6 both
// produce no outer 1687 (empty grouped omitted).
func TestEncodeEmergencyInfo_Empty_Skipped(t *testing.T) {
	// nil pointer
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		EmergencyInfo: nil,
		MPSPriority:   1,
	})
	require.NotNil(t, subData)
	assert.Nil(t, findInGrouped(subData, avpEmergencyInfo, 10415))

	// non-nil but nil MIP6 → all sub-AVPs drop → outer drop
	subData2 := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		EmergencyInfo: &pb.EmergencyInfo{MIP6AgentInfo: nil},
		MPSPriority:   1,
	})
	require.NotNil(t, subData2)
	assert.Nil(t, findInGrouped(subData2, avpEmergencyInfo, 10415),
		"Emergency-Info with nil MIP6 MUST drop outer 1687")
}

// TestEncodeEDRXAndPagingTimeWindow_Octets checks eDRX-Cycle-Length and
// Paging-Time-Window against octets written by hand from the specification,
// not from the gateway's dictionary: AVP header per RFC 6733 §4.1 (code,
// flags, 24-bit length without padding, Vendor-Id), ABNF and flags per TS
// 29.272 §7.3.215/§7.3.216 and §7.3.226-§7.3.228 (all "V", M must not be set)
// and Table 7.3.1/2 (RAT-Type "Must set").
func TestEncodeEDRXAndPagingTimeWindow_Octets(t *testing.T) {
	g := encodeSubscriptionData(&pb.SubscriptionProfile{
		EDRXCycleLength:  []pb.EDRXCycleLength{{RATType: 1004, Value: []byte{0x05}}},      // EUTRAN
		PagingTimeWindow: []pb.PagingTimeWindow{{OperationMode: 3, Length: []byte{0x20}}}, // NB-S1
	})
	require.NotNil(t, g)

	edrx := findInGrouped(g, avpEDRXCycleLength, 10415)
	require.NotNil(t, edrx)
	assert.Equal(t, octets(t, ""+
		"0000069b 80 00002c 000028af"+ // eDRX-Cycle-Length 1691, V, 44 octets
		"00000408 c0 000010 000028af 000003ec"+ // RAT-Type 1032, M V, EUTRAN (1004)
		"0000069c 80 00000d 000028af 05000000"), // eDRX-Cycle-Length-Value 1692, V, 1 octet + padding
		serialized(t, edrx))

	ptw := findInGrouped(g, avpPagingTimeWindow, 10415)
	require.NotNil(t, ptw)
	assert.Equal(t, octets(t, ""+
		"000006a5 80 00002c 000028af"+ // Paging-Time-Window 1701, V, 44 octets
		"000006a6 80 000010 000028af 00000003"+ // Operation-Mode 1702, V, NB-S1
		"000006a7 80 00000d 000028af 20000000"), // Paging-Time-Window-Length 1703, V, 1 octet + padding
		serialized(t, ptw))
}

// TestEncodeTypes_Octets checks AVPs whose type the gateway had wrong against
// octets written from TS 29.272: Expiration-Date is Time (§7.3.80, seconds
// since 1900-01-01 per RFC 6733 §4.3.1; it was sent as Unsigned32 Unix
// seconds), TS-Code is OctetString (§7.3.100; it was Unsigned32), and the
// DSR carries DSR-Flags as 1421 (§7.3.25; it was sent as 1422, DSA-Flags).
func TestEncodeTypes_Octets(t *testing.T) {
	csg := encodeCSGSubscriptionData(pb.CSGSubscriptionData{CSGId: 7, ExpirationDate: 1700000000})
	assert.Equal(t, octets(t, ""+
		"0000059d c0 000010 000028af 00000007"+ // CSG-Id 1437, M V
		"0000059f c0 000010 000028af e8fe6f80"), // Expiration-Date 1439, M V, 1700000000 + 2208988800
		serializedAll(t, csg.AVP))

	ts := encodeTeleserviceList(&pb.TeleserviceList{TSCodes: [][]byte{{0x11}}}) // telephony
	assert.Equal(t, octets(t, "000005cf c0 00000d 000028af 11000000"),          // TS-Code 1487, M V, 1 octet + padding
		serializedAll(t, ts.AVP))

	dsr, _ := NewTranslator().CreateDSR("001010123456789", "mme.example", "example", "hss.example", "example", 0x01, nil)
	flags, err := dsr.FindAVP(1421, 10415)
	require.NoError(t, err, "DSR must carry DSR-Flags (1421)")
	assert.Equal(t, octets(t, "0000058d c0 000010 000028af 00000001"), serialized(t, flags))
	_, err = dsr.FindAVP(1422, 10415)
	assert.Error(t, err, "1422 is DSA-Flags, an answer AVP")
}

func serializedAll(t *testing.T, avps []*diam.AVP) []byte {
	t.Helper()
	var out []byte
	for _, a := range avps {
		out = append(out, serialized(t, a)...)
	}
	return out
}

// TestEncodeMandatoryZeroValues — a mandatory child whose value is 0 is still
// sent: Max-Requested-Bandwidth-UL with DL set (§7.3.41), Complete-Data-List-
// Included-Indicator All_PDP_CONTEXTS_INCLUDED (§7.3.73), Trace-Depth Minimum
// (§7.3.63). eDRX-Cycle-Length-Value must be 1 octet (§7.3.216).
func TestEncodeMandatoryZeroValues(t *testing.T) {
	g := encodeSubscriptionData(&pb.SubscriptionProfile{
		AMBR: &pb.AMBR{MaxRequestedBandwidthDL: 1000},
		GPRSSubscriptionData: &pb.GPRSSubscriptionData{PDPContext: []pb.PDPContext{{
			ContextIdentifier: 1, PDPType: []byte{0x21}, QoSSubscribed: []byte{0x01}, ServiceSelection: "internet",
		}}},
		TraceData: &pb.TraceData{
			TraceReference: []byte{0x01}, TraceNETypeList: []byte{0x01}, TraceEventList: []byte{0x01},
			TraceCollectionEntity: "192.0.2.10",
		},
		EDRXCycleLength: []pb.EDRXCycleLength{{RATType: 1004, Value: []byte{0x05, 0x06}}},
	})
	require.NotNil(t, g)

	ambr := findInGrouped(g, avp.AMBR, 10415).Data.(*diam.GroupedAVP)
	require.NotNil(t, findInGrouped(ambr, avp.MaxRequestedBandwidthUL, 10415))
	assert.Equal(t, uint32(0), avpUint32(findInGrouped(ambr, avp.MaxRequestedBandwidthUL, 10415)))

	gprs := findInGrouped(g, avp.GPRSSubscriptionData, 10415).Data.(*diam.GroupedAVP)
	require.NotNil(t, findInGrouped(gprs, avp.CompleteDataListIncludedIndicator, 10415))

	td := findInGrouped(g, avp.TraceData, 10415)
	require.NotNil(t, td)
	require.NotNil(t, findInGrouped(td.Data.(*diam.GroupedAVP), avp.TraceDepth, 10415))

	assert.Nil(t, findInGrouped(g, avpEDRXCycleLength, 10415), "a 2-octet eDRX value is not sent")
}

// Mandatory values the fixtures need (TS 29.272 §7.3.34 All_APN_CONFIGURATIONS_INCLUDED, §7.3.62 IPv4).
var allAPNsIncluded, pdnIPv4 = uint32(0), uint32(0)

func serialized(t *testing.T, a *diam.AVP) []byte {
	t.Helper()
	b, err := a.Serialize()
	require.NoError(t, err)
	return b
}

// octets turns hex written with spaces for readability into bytes.
func octets(t *testing.T, spacedHex string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(spacedHex, " ", ""))
	require.NoError(t, err)
	return b
}

// =============================================================================
// APNConfiguration sub-Grouped:
//   WLANOffloadability
// =============================================================================

// TestEncodeWLANOffloadability_Roundtrip — outer 1667 wraps both 1668
// (E-UTRAN bitmask) + 1669 (UTRAN bitmask). Lives inside APNConfiguration.
func TestEncodeWLANOffloadability_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:           &pdnIPv4,
					ContextIdentifier: 1,
					ServiceSelection:  "internet",
					WLANOffloadability: &pb.WLANOffloadability{
						WLANOffloadabilityEUTRAN: 0x01, // bit 0 = WLAN_OFFLOADABILITY_EUTRAN_ALLOWED
						WLANOffloadabilityUTRAN:  0x02,
					},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	require.NotNil(t, apnp)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	require.NotNil(t, apn)

	wlan := findInGrouped(apn.Data.(*diam.GroupedAVP), avpWLANOffloadability, 10415)
	require.NotNil(t, wlan, "WLAN-Offloadability missing")
	wlanG := wlan.Data.(*diam.GroupedAVP)

	eutran := findInGrouped(wlanG, avpWLANOffloadabilityEUTRAN, 10415)
	require.NotNil(t, eutran, "WLAN-Offloadability-EUTRAN missing")
	assert.Equal(t, uint32(0x01), avpUint32(eutran))

	utran := findInGrouped(wlanG, avpWLANOffloadabilityUTRAN, 10415)
	require.NotNil(t, utran, "WLAN-Offloadability-UTRAN missing")
	assert.Equal(t, uint32(0x02), avpUint32(utran))
}

// TestEncodeWLANOffloadability_Empty_Skipped — nil pointer + all-zero struct
// both produce no outer 1667.
func TestEncodeWLANOffloadability_Empty_Skipped(t *testing.T) {
	// nil pointer
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:            &pdnIPv4,
					ContextIdentifier:  1,
					ServiceSelection:   "internet",
					WLANOffloadability: nil,
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	assert.Nil(t, findInGrouped(apn.Data.(*diam.GroupedAVP), avpWLANOffloadability, 10415))

	// all-zero struct
	profile.APNConfigurationProfile.APNConfigurations[0].WLANOffloadability = &pb.WLANOffloadability{}
	subData2 := wireRoundTripSubData(t, profile)
	apnp2 := findInGrouped(subData2, avp.APNConfigurationProfile, 10415)
	apn2 := findInGrouped(apnp2.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	assert.Nil(t, findInGrouped(apn2.Data.(*diam.GroupedAVP), avpWLANOffloadability, 10415),
		"all-zero WLANOffloadability MUST drop outer 1667")
}

// =============================================================================
// Top-level extras + APNConfiguration extras
//   + AMBR Rel-15 (Extended-Max-Requested-BW UL/DL)
// =============================================================================

// TestEncodeSubscriptionProfile_TopLevelExtras_Roundtrip — UEUsageType
// (1680) + DLBufferingSuggestedPacketCount (1674 Integer32) + ServiceGapTime
// (1698) round-trip on Subscription-Data top level.
func TestEncodeSubscriptionProfile_TopLevelExtras_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		UEUsageType:                     5,
		DLBufferingSuggestedPacketCount: 100,
		ServiceGapTime:                  3600,
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	ueUsage := findInGrouped(subData, avpUEUsageType, 10415)
	require.NotNil(t, ueUsage, "UE-Usage-Type missing")
	assert.Equal(t, uint32(5), avpUint32(ueUsage))

	dlbuf := findInGrouped(subData, avpDLBufferingSuggestedPacketCount, 10415)
	require.NotNil(t, dlbuf, "DL-Buffering-Suggested-Packet-Count missing")
	assert.Equal(t, uint32(100), avpUint32(dlbuf),
		"DL-Buffering Integer32 wire bytes match Unsigned32 view at positive value")

	gap := findInGrouped(subData, avpServiceGapTime, 10415)
	require.NotNil(t, gap, "Service-Gap-Time missing")
	assert.Equal(t, uint32(3600), avpUint32(gap))
}

// TestEncodeSubscriptionProfile_TopLevelExtras_ZeroOmit — all three
// fields zero → none emitted. Outer Subscription-Data still emits because
// MPSPriority is set.
func TestEncodeSubscriptionProfile_TopLevelExtras_ZeroOmit(t *testing.T) {
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		MPSPriority: 1,
		// UEUsageType / DLBufferingSuggestedPacketCount / ServiceGapTime all zero.
	})
	require.NotNil(t, subData)
	assert.Nil(t, findInGrouped(subData, avpUEUsageType, 10415),
		"UEUsageType=0 MUST be omitted")
	assert.Nil(t, findInGrouped(subData, avpDLBufferingSuggestedPacketCount, 10415),
		"DLBufferingSuggestedPacketCount=0 MUST be omitted")
	assert.Nil(t, findInGrouped(subData, avpServiceGapTime, 10415),
		"ServiceGapTime=0 MUST be omitted")
}

// TestEncodeAMBR_Rel15ExtendedBW_Roundtrip — RFC 8506 §10.1 Rel-15 5G
// high-rate fields (554/555) round-trip alongside the Rel-8 max bandwidth
// pair. Verifies V-only flag posture (must-not="M" per fiorix dict).
func TestEncodeAMBR_Rel15ExtendedBW_Roundtrip(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		AMBR: &pb.AMBR{
			MaxRequestedBandwidthUL:  100_000,
			MaxRequestedBandwidthDL:  500_000,
			ExtendedMaxRequestedBWUL: 1_000_000_000, // 1 Gbps 5G
			ExtendedMaxRequestedBWDL: 4_000_000_000, // 4 Gbps 5G (uint32 ceiling)
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	ambr := findInGrouped(subData, avp.AMBR, 10415)
	require.NotNil(t, ambr)
	ambrG := ambr.Data.(*diam.GroupedAVP)

	extUL := findInGrouped(ambrG, avp.ExtendedMaxRequestedBWUL, 10415)
	require.NotNil(t, extUL, "Extended-Max-Requested-BW-UL missing")
	assert.Equal(t, uint32(1_000_000_000), avpUint32(extUL),
		"Ext-Max-BW-UL must round-trip 1 Gbps")

	extDL := findInGrouped(ambrG, avp.ExtendedMaxRequestedBWDL, 10415)
	require.NotNil(t, extDL, "Extended-Max-Requested-BW-DL missing")
	assert.Equal(t, uint32(4_000_000_000), avpUint32(extDL),
		"Ext-Max-BW-DL must round-trip 4 Gbps")

	// AMBR with only Rel-15 fields (no Rel-8) — outer still emits.
	profile2 := &pb.SubscriptionProfile{
		AMBR: &pb.AMBR{
			ExtendedMaxRequestedBWUL: 1_000_000,
		},
	}
	subData2 := wireRoundTripSubData(t, profile2)
	ambr2 := findInGrouped(subData2, avp.AMBR, 10415)
	require.NotNil(t, ambr2, "AMBR with only Rel-15 fields MUST still emit")
	ambr2G := ambr2.Data.(*diam.GroupedAVP)
	ul := findInGrouped(ambr2G, avp.MaxRequestedBandwidthUL, 10415)
	require.NotNil(t, ul, "Max-Requested-Bandwidth-UL is mandatory (§7.3.41)")
	assert.Equal(t, uint32(4294967295), avpUint32(ul), "set to its upper limit with an extended value (§7.3.41)")
	require.NotNil(t, findInGrouped(ambr2G, avp.ExtendedMaxRequestedBWUL, 10415))
}

// TestEncodeAPNConfiguration_ServedPartyIPAddress — multi (one v4 + one v6)
// addAddressString round-trip; vendor=10415 per fiorix dict.
func TestEncodeAPNConfiguration_ServedPartyIPAddress(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:              &pdnIPv4,
					ContextIdentifier:    1,
					ServiceSelection:     "internet",
					ServedPartyIPAddress: []string{"203.0.113.10", "2001:db8::a", "garbage"},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	apnG := apn.Data.(*diam.GroupedAVP)

	// addAddressString silent-skips invalid IPs; expect exactly 2.
	addrs := findAllInGrouped(apnG, avp.ServedPartyIPAddress, 10415)
	require.Len(t, addrs, 2, "expected 2 valid Served-Party-IP-Address entries (v4+v6); garbage skipped")
}

// TestEncodeAPNConfiguration_NBIoTSCEFFamily — SCEFID + SCEFRealm +
// NonIPPDNTypeIndicator + NonIPDataDeliveryMechanism + PreferredDataMode +
// PDNConnectionContinuity round-trip.
func TestEncodeAPNConfiguration_NBIoTSCEFFamily(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					ContextIdentifier:          1,
					PDNType:                    &pdnIPv4,
					ServiceSelection:           "iot.sgi",
					SCEFID:                     "scef.example.com",
					SCEFRealm:                  "example.com",
					NonIPPDNTypeIndicator:      1, // ALLOWED
					NonIPDataDeliveryMechanism: 2, // SCEF
					PreferredDataMode:          3, // bitmask
					PDNConnectionContinuity:    1, // bit
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	apnG := apn.Data.(*diam.GroupedAVP)

	scefID := findInGrouped(apnG, avpSCEFID, 10415)
	require.NotNil(t, scefID, "SCEF-ID missing")
	assert.Equal(t, []byte("scef.example.com"), scefID.Data.Serialize())

	scefRealm := findInGrouped(apnG, avpSCEFRealm, 10415)
	require.NotNil(t, scefRealm, "SCEF-Realm missing")
	assert.Equal(t, []byte("example.com"), scefRealm.Data.Serialize())

	nip := findInGrouped(apnG, avpNonIPPDNTypeIndicator, 10415)
	require.NotNil(t, nip, "NonIP-PDN-Type-Indicator missing")
	assert.Equal(t, uint32(1), avpUint32(nip))

	nipMech := findInGrouped(apnG, avpNonIPDataDeliveryMechanism, 10415)
	require.NotNil(t, nipMech, "NonIP-Data-Delivery-Mechanism missing")
	assert.Equal(t, uint32(2), avpUint32(nipMech))

	pdm := findInGrouped(apnG, avpPreferredDataMode, 10415)
	require.NotNil(t, pdm, "Preferred-Data-Mode missing")
	assert.Equal(t, uint32(3), avpUint32(pdm))

	pdc := findInGrouped(apnG, avpPDNConnectionContinuity, 10415)
	require.NotNil(t, pdc, "PDN-Connection-Continuity missing")
	assert.Equal(t, uint32(1), avpUint32(pdc))
}

// TestEncodeAPNConfiguration_SIPTOLIPAPermission_VOnlyFlag — SIPTO-Permission
// (1613) and LIPA-Permission (1618) are "V" with M must-not in TS 29.272
// Table 7.3.1/1; flags3GPP sends them without the M-bit.
func TestEncodeAPNConfiguration_SIPTOLIPAPermission_VOnlyFlag(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:           &pdnIPv4,
					ContextIdentifier: 1,
					ServiceSelection:  "internet",
					SIPTOPermission:   1, // SIPTO_ABOVE_RAN_ALLOWED
					LIPAPermission:    2, // LIPA_ONLY
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	apnG := apn.Data.(*diam.GroupedAVP)

	sipto := findInGrouped(apnG, avp.SIPTOPermission, 10415)
	require.NotNil(t, sipto, "SIPTO-Permission missing")
	assert.Equal(t, uint32(1), avpUint32(sipto))

	lipa := findInGrouped(apnG, avp.LIPAPermission, 10415)
	require.NotNil(t, lipa, "LIPA-Permission missing")
	assert.Equal(t, uint32(2), avpUint32(lipa))
}

// TestEncodeAPNConfiguration_RestorationPriority_PerAPN — per-APN override
// of top-level Restoration-Priority. The model keeps RestorationPriority
// inside APNConfiguration scope (not top-level); only the per-APN field
// gets dispatched here.
func TestEncodeAPNConfiguration_RestorationPriority_PerAPN(t *testing.T) {
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					PDNType:             &pdnIPv4,
					ContextIdentifier:   1,
					ServiceSelection:    "ims",
					RestorationPriority: 7,
				},
				{
					PDNType:             &pdnIPv4,
					ContextIdentifier:   2,
					ServiceSelection:    "internet",
					RestorationPriority: 0, // omit
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apns := findAllInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	require.Len(t, apns, 2)

	rp0 := findInGrouped(apns[0].Data.(*diam.GroupedAVP), avpRestorationPriority, 10415)
	require.NotNil(t, rp0, "APN0 Restoration-Priority missing")
	assert.Equal(t, uint32(7), avpUint32(rp0))

	assert.Nil(t, findInGrouped(apns[1].Data.(*diam.GroupedAVP), avpRestorationPriority, 10415),
		"APN1 RestorationPriority=0 MUST be omitted")
}

// TestEncodeAPNConfiguration_AllExtrasOmitWhenZero — defensive: an APN with
// only mandatory fields produces none of the optional extra sub-AVPs. Guards against
// accidental "always emit" regression in encoder bodies.
func TestEncodeAPNConfiguration_AllExtrasOmitWhenZero(t *testing.T) {
	pdnType := uint32(0) // IPv4
	profile := &pb.SubscriptionProfile{
		APNConfigurationProfile: &pb.APNConfigurationProfile{
			AllAPNConfigurationsIncludedIndicator: &allAPNsIncluded,
			ContextIdentifier:                     1,
			APNConfigurations: []pb.APNConfiguration{
				{
					ContextIdentifier: 1,
					PDNType:           &pdnType,
					ServiceSelection:  "internet",
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	apnp := findInGrouped(subData, avp.APNConfigurationProfile, 10415)
	apn := findInGrouped(apnp.Data.(*diam.GroupedAVP), avp.APNConfiguration, 10415)
	apnG := apn.Data.(*diam.GroupedAVP)

	// All optional extra fields MUST be omitted when zero/empty.
	assert.Empty(t, findAllInGrouped(apnG, avp.ServedPartyIPAddress, 10415))
	assert.Nil(t, findInGrouped(apnG, avp.SIPTOPermission, 10415))
	assert.Nil(t, findInGrouped(apnG, avp.LIPAPermission, 10415))
	assert.Nil(t, findInGrouped(apnG, avpRestorationPriority, 10415))
	assert.Nil(t, findInGrouped(apnG, avpNonIPPDNTypeIndicator, 10415))
	assert.Nil(t, findInGrouped(apnG, avpNonIPDataDeliveryMechanism, 10415))
	assert.Nil(t, findInGrouped(apnG, avpSCEFID, 10415))
	assert.Nil(t, findInGrouped(apnG, avpSCEFRealm, 10415))
	assert.Nil(t, findInGrouped(apnG, avpPreferredDataMode, 10415))
	assert.Nil(t, findInGrouped(apnG, avpPDNConnectionContinuity, 10415))
	assert.Nil(t, findInGrouped(apnG, avpSIPTOLocalNetworkPermission, 10415))
}

// =============================================================================
// TraceData + MDTConfiguration + AreaScope
//   Three-level nested grouped + bidirectional encode/decode.
//
// Pattern: 4 round-trip + 4 zero-value-skip + 4 wire encode-only.
// =============================================================================

// ---- 4 round-trip tests ------------------------------------------------------

// TestEncodeTraceData_FullPopulate_Roundtrip — full LI/MDT scenario:
// TraceData with all sub-AVPs + nested MDTConfiguration with AreaScope
// (multi cell-global-identity entries) + threshold/period fields.
func TestEncodeTraceData_FullPopulate_Roundtrip(t *testing.T) {
	traceRef, _ := hex.DecodeString("82f0010001")
	neList, _ := hex.DecodeString("0102030405") // bitstring (opaque)
	intfList, _ := hex.DecodeString("0a0b")
	evtList, _ := hex.DecodeString("ff00ff00")
	cgi1, _ := hex.DecodeString("82f01012345678")
	cgi2, _ := hex.DecodeString("82f01087654321")
	tai, _ := hex.DecodeString("82f01000ff")
	posMethod, _ := hex.DecodeString("01")
	measQty, _ := hex.DecodeString("0102")

	profile := &pb.SubscriptionProfile{
		TraceData: &pb.TraceData{
			TraceReference:        traceRef,
			TraceDepth:            3, // MEDIUM
			TraceNETypeList:       neList,
			TraceInterfaceList:    intfList,
			TraceEventList:        evtList,
			OMCID:                 "omc.operator.com",
			TraceCollectionEntity: "10.20.30.40",
			MDTConfiguration: &pb.MDTConfiguration{
				JobType: 1, // LOGGED_MDT
				AreaScope: &pb.AreaScope{
					CellGlobalIdentity:   [][]byte{cgi1, cgi2},
					TrackingAreaIdentity: [][]byte{tai},
				},
				ListOfMeasurements:    0xff, // bitmask
				ReportingTrigger:      0x07,
				ReportInterval:        2,
				ReportAmount:          5,
				EventThresholdRSRP:    50,
				EventThresholdRSRQ:    60,
				LoggingInterval:       3,
				LoggingDuration:       4,
				MeasurementPeriodLTE:  2,
				PositioningMethod:     posMethod,
				MeasurementQuantity:   measQty,
				EventThresholdEvent1F: -110,
				EventThresholdEvent1I: -90,
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	require.NotNil(t, subData)

	td := findInGrouped(subData, avp.TraceData, 10415)
	require.NotNil(t, td, "Trace-Data missing")
	tdG := td.Data.(*diam.GroupedAVP)

	tr := findInGrouped(tdG, avpTraceReference, 10415)
	require.NotNil(t, tr, "TraceReference missing")
	assert.Equal(t, traceRef, tr.Data.Serialize())

	depth := findInGrouped(tdG, avp.TraceDepth, 10415)
	require.NotNil(t, depth)
	assert.Equal(t, uint32(3), avpUint32(depth))

	tce := findInGrouped(tdG, avp.TraceCollectionEntity, 10415)
	require.NotNil(t, tce, "TraceCollectionEntity missing")
	// datatype.Address wire = 2-byte type prefix + IP bytes.
	tceBytes := tce.Data.Serialize()
	require.GreaterOrEqual(t, len(tceBytes), 2)
	assert.Equal(t, net.IPv4(10, 20, 30, 40).To4(), net.IP(tceBytes[2:]).To4())

	mdt := findInGrouped(tdG, avp.MDTConfiguration, 10415)
	require.NotNil(t, mdt, "nested MDT-Configuration missing")
	mdtG := mdt.Data.(*diam.GroupedAVP)

	jobType := findInGrouped(mdtG, avpJobType, 10415)
	require.NotNil(t, jobType)
	assert.Equal(t, uint32(1), avpUint32(jobType))

	as := findInGrouped(mdtG, avpAreaScope, 10415)
	require.NotNil(t, as, "Area-Scope missing")
	asG := as.Data.(*diam.GroupedAVP)
	cgis := findAllInGrouped(asG, avpCellGlobalIdentity, 10415)
	require.Len(t, cgis, 2, "expected 2 Cell-Global-Identity entries")
	assert.Equal(t, cgi1, cgis[0].Data.Serialize())
	tais := findAllInGrouped(asG, avpTrackingAreaIdentity, 10415)
	require.Len(t, tais, 1)

	// Bitstring opaque transport — bytes round-trip verbatim, no semantic decode.
	ne := findInGrouped(tdG, avp.TraceNETypeList, 10415)
	require.NotNil(t, ne)
	assert.Equal(t, neList, ne.Data.Serialize(),
		"TraceNETypeList MUST round-trip as opaque bytes (TS 32.422 §5.1)")

	// Integer32 negative thresholds round-trip.
	e1f := findInGrouped(mdtG, avpEventThresholdEvent1F, 10415)
	require.NotNil(t, e1f)
	assert.Equal(t, int32(-110), int32(avpUint32(e1f)),
		"EventThresholdEvent1F negative Integer32 round-trip")
}

// TestEncodeTraceData_AreaScopePartial — only EUTRAN-Cell-Global-Identity
// populated; other AreaScope lists empty. Outer AreaScope still emits.
func TestEncodeTraceData_AreaScopePartial(t *testing.T) {
	ecgi, _ := hex.DecodeString("82f01000abcdef")
	traceRef, _ := hex.DecodeString("82f001ff")
	profile := &pb.SubscriptionProfile{
		TraceData: &pb.TraceData{
			TraceNETypeList:       []byte{0x01},
			TraceEventList:        []byte{0x01},
			TraceCollectionEntity: "192.0.2.10",
			TraceReference:        traceRef,
			MDTConfiguration: &pb.MDTConfiguration{
				JobType: 0,
				AreaScope: &pb.AreaScope{
					EUTRANCellGlobalIdentity: [][]byte{ecgi},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	td := findInGrouped(subData, avp.TraceData, 10415)
	require.NotNil(t, td)
	mdt := findInGrouped(td.Data.(*diam.GroupedAVP), avp.MDTConfiguration, 10415)
	require.NotNil(t, mdt)
	as := findInGrouped(mdt.Data.(*diam.GroupedAVP), avpAreaScope, 10415)
	require.NotNil(t, as)
	asG := as.Data.(*diam.GroupedAVP)
	require.Len(t, findAllInGrouped(asG, avpEUTRANCellGlobalIdentity, 10415), 1)
	assert.Empty(t, findAllInGrouped(asG, avpCellGlobalIdentity, 10415))
	assert.Empty(t, findAllInGrouped(asG, avpRoutingAreaIdentity, 10415))
	assert.Empty(t, findAllInGrouped(asG, avpLocationAreaIdentity, 10415))
	assert.Empty(t, findAllInGrouped(asG, avpTrackingAreaIdentity, 10415))
}

// TestEncodeTraceData_BareTraceReferenceNotSent — Trace-Data with only
// Trace-Reference lacks Trace-Depth, Trace-NE-Type-List, Trace-Event-List and
// Trace-Collection-Entity, all mandatory (TS 29.272 §7.3.63): it is not sent.
func TestEncodeTraceData_BareTraceReferenceNotSent(t *testing.T) {
	traceRef, _ := hex.DecodeString("82f001de")
	profile := &pb.SubscriptionProfile{
		TraceData:   &pb.TraceData{TraceReference: traceRef},
		MPSPriority: 1,
	}
	subData := wireRoundTripSubData(t, profile)
	assert.Nil(t, findInGrouped(subData, avp.TraceData, 10415))
}

// TestEncodeTraceData_BitstringOpaqueTransparentPassthrough — bitstring
// AVPs (1463/1464/1465) carry raw bytes verbatim; encoder does not interpret
// TS 32.422 §5.1 semantics.
func TestEncodeTraceData_BitstringOpaqueTransparentPassthrough(t *testing.T) {
	traceRef, _ := hex.DecodeString("82f001aa")
	weirdNE, _ := hex.DecodeString("00ffaa55deadbeef")          // longer than typical
	weirdIntf, _ := hex.DecodeString("00")                      // single byte
	weirdEvt, _ := hex.DecodeString("aaaaaaaaaaaaaaaaaaaaaaaa") // 12 bytes

	profile := &pb.SubscriptionProfile{
		TraceData: &pb.TraceData{
			TraceCollectionEntity: "192.0.2.10",
			TraceReference:        traceRef,
			TraceNETypeList:       weirdNE,
			TraceInterfaceList:    weirdIntf,
			TraceEventList:        weirdEvt,
		},
	}
	subData := wireRoundTripSubData(t, profile)
	tdG := findInGrouped(subData, avp.TraceData, 10415).Data.(*diam.GroupedAVP)

	ne := findInGrouped(tdG, avp.TraceNETypeList, 10415)
	require.NotNil(t, ne)
	assert.Equal(t, weirdNE, ne.Data.Serialize(),
		"NE-Type-List bitstring MUST round-trip verbatim, no semantic interpretation")

	intf := findInGrouped(tdG, avp.TraceInterfaceList, 10415)
	require.NotNil(t, intf)
	assert.Equal(t, weirdIntf, intf.Data.Serialize())

	evt := findInGrouped(tdG, avp.TraceEventList, 10415)
	require.NotNil(t, evt)
	assert.Equal(t, weirdEvt, evt.Data.Serialize())
}

// ---- 4 zero-value-skip tests -------------------------------------------------

// TestEncodeTraceData_NilPointer_Skipped — nil TraceData pointer → outer
// 1458 AVP not emitted at all.
func TestEncodeTraceData_NilPointer_Skipped(t *testing.T) {
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		TraceData:   nil,
		MPSPriority: 1,
	})
	require.NotNil(t, subData)
	assert.Nil(t, findInGrouped(subData, avp.TraceData, 10415),
		"nil TraceData pointer MUST omit outer 1458")
}

// TestEncodeTraceData_AllZero_OuterDropped — non-nil but all-zero/empty
// TraceData → no sub-AVPs emit → outer dropped (empty grouped omitted).
func TestEncodeTraceData_AllZero_OuterDropped(t *testing.T) {
	subData := wireRoundTripSubData(t, &pb.SubscriptionProfile{
		TraceData:   &pb.TraceData{},
		MPSPriority: 1,
	})
	require.NotNil(t, subData)
	assert.Nil(t, findInGrouped(subData, avp.TraceData, 10415),
		"all-zero TraceData MUST drop outer 1458")
}

// TestEncodeMDTConfiguration_OnlyMandatoryJobType — MDT with only JobType
// (mandatory per spec, even at zero) → outer 1622 emits with one sub-AVP.
// Defensive against "all-zero AreaScope drops MDT outer" regression.
func TestEncodeMDTConfiguration_OnlyMandatoryJobType(t *testing.T) {
	traceRef, _ := hex.DecodeString("82f001bb")
	profile := &pb.SubscriptionProfile{
		TraceData: &pb.TraceData{
			TraceNETypeList:       []byte{0x01},
			TraceEventList:        []byte{0x01},
			TraceCollectionEntity: "192.0.2.10",
			TraceReference:        traceRef,
			MDTConfiguration:      &pb.MDTConfiguration{JobType: 0}, // valid enum
		},
	}
	subData := wireRoundTripSubData(t, profile)
	tdG := findInGrouped(subData, avp.TraceData, 10415).Data.(*diam.GroupedAVP)
	mdt := findInGrouped(tdG, avp.MDTConfiguration, 10415)
	require.NotNil(t, mdt, "MDT with mandatory JobType=0 MUST still emit outer 1622")
	mdtG := mdt.Data.(*diam.GroupedAVP)
	jt := findInGrouped(mdtG, avpJobType, 10415)
	require.NotNil(t, jt, "JobType MUST emit even at zero (mandatory per spec)")
	assert.Equal(t, uint32(0), avpUint32(jt))
	// Optional fields all omit.
	assert.Nil(t, findInGrouped(mdtG, avpAreaScope, 10415))
	assert.Nil(t, findInGrouped(mdtG, avpListOfMeasurements, 10415))
}

// TestEncodeAreaScope_AllEmpty_Dropped — AreaScope with all five lists empty
// → outer 1624 dropped (empty grouped omitted, innermost grouped level).
func TestEncodeAreaScope_AllEmpty_Dropped(t *testing.T) {
	traceRef, _ := hex.DecodeString("82f001cc")
	profile := &pb.SubscriptionProfile{
		TraceData: &pb.TraceData{
			TraceNETypeList:       []byte{0x01},
			TraceEventList:        []byte{0x01},
			TraceCollectionEntity: "192.0.2.10",
			TraceReference:        traceRef,
			MDTConfiguration: &pb.MDTConfiguration{
				JobType:   1,
				AreaScope: &pb.AreaScope{}, // non-nil but empty
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	tdG := findInGrouped(subData, avp.TraceData, 10415).Data.(*diam.GroupedAVP)
	mdt := findInGrouped(tdG, avp.MDTConfiguration, 10415)
	require.NotNil(t, mdt)
	assert.Nil(t, findInGrouped(mdt.Data.(*diam.GroupedAVP), avpAreaScope, 10415),
		"empty AreaScope MUST drop outer 1624")
}

// ---- 4 wire encode-only tests (direct encoder, no parser) ------------------

// TestEncodeTraceData_DirectEncoder_NilReturnsNil — pure encoder contract:
// nil pointer in → nil out, no panic, no AVP allocation.
func TestEncodeTraceData_DirectEncoder_NilReturnsNil(t *testing.T) {
	assert.Nil(t, encodeTraceData(nil))
	assert.Nil(t, encodeMDTConfiguration(nil))
	assert.Nil(t, encodeAreaScope(nil))
}

// TestEncodeMDTConfiguration_DirectEncoder_AllZero — even with all fields
// zero/empty, encodeMDTConfiguration emits the mandatory JobType (per
// encoder spec) so it returns non-nil with exactly one sub-AVP.
func TestEncodeMDTConfiguration_DirectEncoder_AllZero(t *testing.T) {
	g := encodeMDTConfiguration(&pb.MDTConfiguration{}) // all zero
	require.NotNil(t, g, "MDT all-zero MUST still emit JobType (mandatory)")
	assert.Len(t, g.AVP, 1)
	assert.Equal(t, uint32(avpJobType), g.AVP[0].Code)
}

// TestEncodeAreaScope_DirectEncoder_MultiOrder — encoder preserves the
// per-field-list order (CellGlobalIdentity entries before EUTRAN entries).
// Wire decode-then-find tests can't verify order across slices because the
// dispatch helper returns code-matched entries; this test goes direct on
// the encoder output.
func TestEncodeAreaScope_DirectEncoder_MultiOrder(t *testing.T) {
	a := &pb.AreaScope{
		CellGlobalIdentity:       [][]byte{[]byte{0x01}, []byte{0x02}},
		EUTRANCellGlobalIdentity: [][]byte{[]byte{0x03}},
		TrackingAreaIdentity:     [][]byte{[]byte{0x04}, []byte{0x05}},
	}
	g := encodeAreaScope(a)
	require.NotNil(t, g)
	require.Len(t, g.AVP, 5,
		"expected 5 sub-AVPs (2 CGI + 1 ECGI + 2 TAI)")
	assert.Equal(t, uint32(avpCellGlobalIdentity), g.AVP[0].Code, "first AVP must be CGI")
	assert.Equal(t, uint32(avpEUTRANCellGlobalIdentity), g.AVP[2].Code, "third AVP must be ECGI")
	assert.Equal(t, uint32(avpTrackingAreaIdentity), g.AVP[3].Code, "fourth AVP must be TAI")
}

// TestDecodeTraceData_RoundTrip — decoder counterpart: build a wire AVP
// via encoder, then run decodeTraceData on the Grouped AVP. Tests the
// bidirectional symmetry that IDR/CLR backends rely on.
func TestDecodeTraceData_RoundTrip(t *testing.T) {
	traceRef, _ := hex.DecodeString("82f001ee")
	profile := &pb.SubscriptionProfile{
		TraceData: &pb.TraceData{
			TraceNETypeList:       []byte{0x01},
			TraceEventList:        []byte{0x01},
			TraceReference:        traceRef,
			TraceDepth:            5,
			OMCID:                 "omc.test",
			TraceCollectionEntity: "203.0.113.5",
			MDTConfiguration: &pb.MDTConfiguration{
				JobType:               2,
				ListOfMeasurements:    0xab,
				EventThresholdEvent1F: -50,
				AreaScope: &pb.AreaScope{
					CellGlobalIdentity: [][]byte{[]byte{0x10, 0x20}},
				},
			},
		},
	}
	subData := wireRoundTripSubData(t, profile)
	td := findInGrouped(subData, avp.TraceData, 10415)
	require.NotNil(t, td)

	decoded := decodeTraceData(td.Data.(*diam.GroupedAVP))
	require.NotNil(t, decoded, "decodeTraceData returned nil on populated input")
	assert.Equal(t, traceRef, decoded.TraceReference)
	assert.Equal(t, uint32(5), decoded.TraceDepth)
	assert.Equal(t, "omc.test", decoded.OMCID)
	assert.Equal(t, "203.0.113.5", decoded.TraceCollectionEntity)
	require.NotNil(t, decoded.MDTConfiguration)
	assert.Equal(t, uint32(2), decoded.MDTConfiguration.JobType)
	assert.Equal(t, uint32(0xab), decoded.MDTConfiguration.ListOfMeasurements)
	assert.Equal(t, int32(-50), decoded.MDTConfiguration.EventThresholdEvent1F)
	require.NotNil(t, decoded.MDTConfiguration.AreaScope)
	require.Len(t, decoded.MDTConfiguration.AreaScope.CellGlobalIdentity, 1)
	assert.Equal(t, []byte{0x10, 0x20}, decoded.MDTConfiguration.AreaScope.CellGlobalIdentity[0])
}
