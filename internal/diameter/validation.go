// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// avpKey identifies a mandatory AVP by code + vendor (vendor 0 = base IETF).
// Used by the per-command mandatory-AVP table for parse-time validation.
type avpKey struct {
	Code     uint32
	VendorID uint32
	Name     string // for Failed-AVP entry; not used for matching
}

// mandatoryAVPsByCommand is the per-S6a/S13-command minimum-AVP set per
// TS 29.272 §5.2.1.x ABNF "Diameter Header" + "{...}" mandatory entries.
// Session-Id, Origin-Host, Origin-Realm, Destination-Realm, Auth-Session-State
// are mandatory on every command — listed once per command for clarity.
//
// This is a defensive layer in front of the NATS bridge: a malformed Diameter
// request that misses a mandatory AVP gets 5005 (DIAMETER_MISSING_AVP) +
// Failed-AVP per RFC 6733 §7.5 *before* the gateway burns a backend
// roundtrip. Lenient parse continues to apply to OPTIONAL fields — those
// remain "missing → zero/nil at backend"; we only enforce ABNF mandatory.
var mandatoryAVPsByCommand = map[uint32][]avpKey{
	// 316 ULR — TS 29.272 §5.2.1.1.2
	316: {
		{avp.SessionID, 0, "Session-Id"},
		{avp.OriginHost, 0, "Origin-Host"},
		{avp.OriginRealm, 0, "Origin-Realm"},
		{avp.DestinationRealm, 0, "Destination-Realm"},
		{avp.AuthSessionState, 0, "Auth-Session-State"},
		{avp.UserName, 0, "User-Name"},
		{avp.RATType, 10415, "RAT-Type"},
		{avp.ULRFlags, 10415, "ULR-Flags"},
		{avp.VisitedPLMNID, 10415, "Visited-PLMN-Id"},
	},
	// 318 AIR — TS 29.272 §5.2.1.2.2 (Visited-PLMN-Id mandatory; one of
	// the Auth-Info request AVPs SHOULD be present but ABNF doesn't make
	// it mandatory — leave to backend).
	318: {
		{avp.SessionID, 0, "Session-Id"},
		{avp.OriginHost, 0, "Origin-Host"},
		{avp.OriginRealm, 0, "Origin-Realm"},
		{avp.DestinationRealm, 0, "Destination-Realm"},
		{avp.AuthSessionState, 0, "Auth-Session-State"},
		{avp.UserName, 0, "User-Name"},
		{avp.VisitedPLMNID, 10415, "Visited-PLMN-Id"},
	},
	// 321 PUR — TS 29.272 §5.2.1.5.2
	321: {
		{avp.SessionID, 0, "Session-Id"},
		{avp.OriginHost, 0, "Origin-Host"},
		{avp.OriginRealm, 0, "Origin-Realm"},
		{avp.DestinationRealm, 0, "Destination-Realm"},
		{avp.AuthSessionState, 0, "Auth-Session-State"},
		{avp.UserName, 0, "User-Name"},
	},
	// 323 NOR — TS 29.272 §5.2.1.7.2
	323: {
		{avp.SessionID, 0, "Session-Id"},
		{avp.OriginHost, 0, "Origin-Host"},
		{avp.OriginRealm, 0, "Origin-Realm"},
		{avp.DestinationRealm, 0, "Destination-Realm"},
		{avp.AuthSessionState, 0, "Auth-Session-State"},
		{avp.UserName, 0, "User-Name"},
	},
	// 324 ECR (S13) — TS 29.272 §5.2.2.1.2 (User-Name optional; Terminal-
	// Information mandatory but is itself a Grouped AVP — checking presence
	// at top level is enough).
	324: {
		{avp.SessionID, 0, "Session-Id"},
		{avp.OriginHost, 0, "Origin-Host"},
		{avp.OriginRealm, 0, "Origin-Realm"},
		{avp.DestinationRealm, 0, "Destination-Realm"},
		{avp.AuthSessionState, 0, "Auth-Session-State"},
		{avp.TerminalInformation, 10415, "Terminal-Information"},
	},
}

// ValidateMandatoryAVPs walks the per-command mandatory AVP table and
// returns one FailedAVPEntry per AVP that is absent from the request.
// Returns nil when every mandatory AVP is present (or when the command
// code has no validation table — defensive default for new commands not
// yet enumerated here).
//
// Vendor-specific AVP lookup uses direct iteration rather than dict.Default
// because go-diameter's typed FindAVP requires dictionary registration of
// every checked code, and many 3GPP AVPs aren't in the bundled dict.
func ValidateMandatoryAVPs(m *diam.Message) []pb.FailedAVPEntry {
	mandatory, ok := mandatoryAVPsByCommand[m.Header.CommandCode]
	if !ok {
		return nil
	}
	var missing []pb.FailedAVPEntry
	for _, want := range mandatory {
		found := false
		for _, got := range m.AVP {
			if got.Code == want.Code && got.VendorID == want.VendorID {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, pb.FailedAVPEntry{
				Code:     want.Code,
				VendorID: want.VendorID,
				// Empty Value — peer learns the missing AVP code (the part
				// they need to fix); we have no original bytes to echo.
			})
		}
	}
	return missing
}

// BuildMissingAVPAnswer constructs a 5005 (DIAMETER_MISSING_AVP) answer
// carrying a Failed-AVP grouped AVP listing every missing mandatory AVP.
// Per RFC 6733 §7.5, AVP-class result codes (5005 et al.) MUST include
// Failed-AVP — this is the gateway-self-detected counterpart to the
// backend-supplied path in applyFailedAVPs.
//
// Origin-Host / Origin-Realm are written from the supplied origin so the
// answer remains spec-conformant even when the request itself was missing
// these (req.Answer would otherwise produce an answer the gateway can't
// honestly attribute to itself).
func BuildMissingAVPAnswer(req *diam.Message, originHost, originRealm string,
	missing []pb.FailedAVPEntry) *diam.Message {
	ans := req.Answer(5005) // DIAMETER_MISSING_AVP

	// Override Origin-Host/Realm: req.Answer copies AVPs from the request,
	// but the request might be the one missing them — use the gateway's
	// own identity instead.
	if _, err := ans.FindAVP(avp.OriginHost, 0); err != nil {
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	}
	if _, err := ans.FindAVP(avp.OriginRealm, 0); err != nil {
		ans.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	}

	applyFailedAVPs(ans, missing)
	return ans
}
