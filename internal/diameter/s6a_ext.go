// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"encoding/hex"
	"fmt"
	"net"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// The S6a AVPs beyond the subscription data: the further AVPs of the
// requests from the MME, of the MME's answers to the gateway's requests, and
// of the gateway's own requests and answers (TS 29.272 §7.2, §7.3). The
// readers accept whatever type an AVP decoded as and never panic.

// AVP codes go-diameter v4 has no constant for (TS 29.272 Table 7.3.1/1).
const (
	avpDSAFlags                 = 1422
	avpIDAFlags                 = 1441
	avp3GPP2MEID                = 1471
	avpIMSVoiceOverPS           = 1492
	avpLastUEActivityTime       = 1494
	avpEPSUserState             = 1495
	avpEPSLocationInformation   = 1496
	avpMMEUserState             = 1497
	avpSGSNUserState            = 1498
	avpUserState                = 1499
	avpMMELocationInformation   = 1600
	avpSGSNLocationInformation  = 1601
	avpServiceAreaIdentity      = 1607
	avpGeographicalInformation  = 1608
	avpGeodeticInformation      = 1609
	avpCurrentLocationRetrieved = 1610
	avpAgeOfLocationInformation = 1611
	avpErrorDiagnostic          = 1614
	avpCLRFlags                 = 1638
	avpTimeZone                 = 1642
	avpSMSRegisterRequest       = 1648
	avpLocalTimeZone            = 1649
	avpDaylightSavingTime       = 1650
	avpSGsMMEIdentity           = 1664
	avpCoupledNodeDiameterID    = 1666
	avpAdjacentPLMNs            = 1672
	avpAIRFlags                 = 1679
	avpResetID                  = 1670
	avpMaxUEAvailabilityTime    = 3329 // TS 29.338 §5.3.3.22
	avpConfidentialityKey       = 625  // TS 29.229
	avpIntegrityKey             = 626  // TS 29.229
)

// ─── Reading ─────────────────────────────────────────────────────────────

func children(a *diam.AVP) []*diam.AVP {
	if a == nil {
		return nil
	}
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		return g.AVP
	}
	return nil
}

// child returns the first of list with code under vendor.
func child(list []*diam.AVP, code, vendor uint32) *diam.AVP {
	for _, a := range list {
		if a.Code == code && a.VendorID == vendor {
			return a
		}
	}
	return nil
}

func u32Of(a *diam.AVP) (uint32, bool) {
	if a == nil {
		return 0, false
	}
	switch v := a.Data.(type) {
	case datatype.Unsigned32:
		return uint32(v), true
	case datatype.Enumerated:
		return uint32(v), true
	case datatype.Integer32:
		return uint32(v), true
	}
	return 0, false
}

func u32Value(a *diam.AVP) uint32 { v, _ := u32Of(a); return v }

func u32Ptr(a *diam.AVP) *uint32 {
	if v, ok := u32Of(a); ok {
		return &v
	}
	return nil
}

// hexOf is the hex of an octet-string AVP's value.
func hexOf(a *diam.AVP) string {
	if a == nil {
		return ""
	}
	switch v := a.Data.(type) {
	case datatype.OctetString:
		return hex.EncodeToString([]byte(v))
	case datatype.Unknown:
		return hex.EncodeToString(v)
	}
	return ""
}

// octetString reads an OctetString AVP whose bytes are text, the form the
// subscription-data encoder writes Visited-Network-Identifier in.
func octetString(a *diam.AVP) string {
	if a == nil {
		return ""
	}
	if v, ok := a.Data.(datatype.OctetString); ok {
		return string(v)
	}
	return AVPString(a)
}

func timeOf(a *diam.AVP) *time.Time {
	if a == nil {
		return nil
	}
	if v, ok := a.Data.(datatype.Time); ok {
		t := time.Time(v).UTC()
		return &t
	}
	return nil
}

func decodeMIP6AgentInfo(a *diam.AVP) *pb.MIP6AgentInfo {
	c := children(a)
	if c == nil {
		return nil
	}
	info := &pb.MIP6AgentInfo{}
	for _, sub := range c {
		switch sub.Code {
		case avp.MIPHomeAgentAddress:
			if ip, ok := sub.Data.(datatype.Address); ok {
				info.MIP6HomeAgentAddress = append(info.MIP6HomeAgentAddress, net.IP(ip).String())
			}
		case avp.MIPHomeAgentHost:
			hc := children(sub)
			info.MIP6HomeAgentHost = &struct {
				DestinationHost  string `json:"destination_host"`
				DestinationRealm string `json:"destination_realm"`
			}{AVPString(child(hc, avp.DestinationHost, 0)), AVPString(child(hc, avp.DestinationRealm, 0))}
		case avp.MIP6HomeLinkPrefix:
			info.MIP6HomeLinkPrefix = hexOf(sub)
		}
	}
	return info
}

// decodeActiveAPN reads an Active-APN (1612, §7.3.127).
func decodeActiveAPN(a *diam.AVP) pb.ActiveAPN {
	c := children(a)
	out := pb.ActiveAPN{
		ContextID:                u32Value(child(c, avp.ContextIdentifier, vendorID3GPP)),
		ServiceSelection:         AVPString(child(c, avp.ServiceSelection, 0)),
		MIP6AgentInfo:            decodeMIP6AgentInfo(child(c, avp.MIP6AgentInfo, 0)),
		VisitedNetworkIdentifier: octetString(child(c, avp.VisitedNetworkIdentifier, vendorID3GPP)),
	}
	for _, sub := range c {
		if sub.Code != avp.SpecificAPNInfo || sub.VendorID != vendorID3GPP {
			continue
		}
		sc := children(sub)
		out.SpecificAPNInfo = append(out.SpecificAPNInfo, pb.SpecificAPNInfo{
			ServiceSelection:         AVPString(child(sc, avp.ServiceSelection, 0)),
			MIP6AgentInfo:            decodeMIP6AgentInfo(child(sc, avp.MIP6AgentInfo, 0)),
			VisitedNetworkIdentifier: octetString(child(sc, avp.VisitedNetworkIdentifier, vendorID3GPP)),
		})
	}
	return out
}

// decodeEPSLocationInformation reads an EPS-Location-Information (1496,
// §7.3.111).
func decodeEPSLocationInformation(a *diam.AVP) *pb.EPSLocationInformation {
	c := children(a)
	if c == nil {
		return nil
	}
	out := &pb.EPSLocationInformation{}
	if mme := children(child(c, avpMMELocationInformation, vendorID3GPP)); mme != nil {
		out.MME = &pb.MMELocationInformation{
			EUTRANCellGlobalIdentity: hexOf(child(mme, avpEUTRANCellGlobalIdentity, vendorID3GPP)),
			TrackingAreaIdentity:     hexOf(child(mme, avpTrackingAreaIdentity, vendorID3GPP)),
			GeographicalInformation:  hexOf(child(mme, avpGeographicalInformation, vendorID3GPP)),
			GeodeticInformation:      hexOf(child(mme, avpGeodeticInformation, vendorID3GPP)),
			CurrentLocationRetrieved: u32Ptr(child(mme, avpCurrentLocationRetrieved, vendorID3GPP)),
			AgeOfLocationInformation: u32Ptr(child(mme, avpAgeOfLocationInformation, vendorID3GPP)),
		}
	}
	if sgsn := children(child(c, avpSGSNLocationInformation, vendorID3GPP)); sgsn != nil {
		out.SGSN = &pb.SGSNLocationInformation{
			CellGlobalIdentity:       hexOf(child(sgsn, avpCellGlobalIdentity, vendorID3GPP)),
			LocationAreaIdentity:     hexOf(child(sgsn, avpLocationAreaIdentity, vendorID3GPP)),
			ServiceAreaIdentity:      hexOf(child(sgsn, avpServiceAreaIdentity, vendorID3GPP)),
			RoutingAreaIdentity:      hexOf(child(sgsn, avpRoutingAreaIdentity, vendorID3GPP)),
			GeographicalInformation:  hexOf(child(sgsn, avpGeographicalInformation, vendorID3GPP)),
			GeodeticInformation:      hexOf(child(sgsn, avpGeodeticInformation, vendorID3GPP)),
			CurrentLocationRetrieved: u32Ptr(child(sgsn, avpCurrentLocationRetrieved, vendorID3GPP)),
			AgeOfLocationInformation: u32Ptr(child(sgsn, avpAgeOfLocationInformation, vendorID3GPP)),
		}
	}
	return out
}

// decodeNetworkRequestExtras fills the event fields of TS 29.272 §7.2 that
// ToS6aEvent's main body does not.
func decodeNetworkRequestExtras(m *diam.Message, event *pb.S6aEvent) {
	top := m.AVP
	for _, a := range top {
		if a.Code == avp.ActiveAPN && a.VendorID == vendorID3GPP {
			event.ActiveAPNs = append(event.ActiveAPNs, decodeActiveAPN(a))
		}
	}
	event.SMSRegisterRequest = u32Ptr(child(top, avpSMSRegisterRequest, vendorID3GPP))
	event.SGsMMEIdentity = AVPString(child(top, avpSGsMMEIdentity, vendorID3GPP))
	event.CoupledNodeDiameterID = AVPString(child(top, avpCoupledNodeDiameterID, vendorID3GPP))
	for _, plmn := range children(child(top, avpAdjacentPLMNs, vendorID3GPP)) {
		if plmn.Code == avp.VisitedPLMNID && plmn.VendorID == vendorID3GPP {
			event.AdjacentPLMNs = append(event.AdjacentPLMNs, hexOf(plmn))
		}
	}
	event.AIRFlags = u32Value(child(top, avpAIRFlags, vendorID3GPP))
	event.EPSLocationInformation = decodeEPSLocationInformation(child(top, avpEPSLocationInformation, vendorID3GPP))
	event.MaximumUEAvailabilityTime = timeOf(child(top, avpMaxUEAvailabilityTime, vendorID3GPP))
	event.VisitedNetworkIdentifier = octetString(child(top, avp.VisitedNetworkIdentifier, vendorID3GPP))
}

// DecodeMMEAnswer is what the gateway replies to an application that sent
// a CLR, IDR, DSR or RSR: the MME's result and the rest of its answer (TS
// 29.272 §7.2.8, §7.2.10, §7.2.12, §7.2.16).
func DecodeMMEAnswer(m *diam.Message) pb.S6aAnswer {
	rc, exp := AnswerResult(m)
	top := m.AVP
	out := pb.S6aAnswer{ResultCode: rc, ExperimentalResultCode: exp}
	for _, a := range top {
		if a.Code == avp.SupportedFeatures && a.VendorID == vendorID3GPP {
			c := children(a)
			out.SupportedFeatures = append(out.SupportedFeatures, pb.SupportedFeature{
				VendorID:      u32Value(child(c, avp.VendorID, 0)),
				FeatureListID: u32Value(child(c, avp.FeatureListID, vendorID3GPP)),
				FeatureList:   u32Value(child(c, avp.FeatureList, vendorID3GPP)),
			})
		}
	}
	for _, f := range children(child(top, avp.FailedAVP, 0)) {
		out.FailedAVPs = append(out.FailedAVPs, pb.FailedAVPEntry{
			Code: f.Code, VendorID: f.VendorID, Value: hex.EncodeToString(f.Data.Serialize()),
		})
	}
	out.IDAFlags = u32Value(child(top, avpIDAFlags, vendorID3GPP))
	out.DSAFlags = u32Value(child(top, avpDSAFlags, vendorID3GPP))
	out.IMSVoiceOverPSSessionsSupported = u32Ptr(child(top, avpIMSVoiceOverPS, vendorID3GPP))
	out.LastUEActivityTime = timeOf(child(top, avpLastUEActivityTime, vendorID3GPP))
	out.RATType = u32Value(child(top, avp.RATType, vendorID3GPP))
	if us := children(child(top, avpEPSUserState, vendorID3GPP)); us != nil {
		out.EPSUserState = &pb.EPSUserState{
			MMEUserState:  u32Ptr(child(children(child(us, avpMMEUserState, vendorID3GPP)), avpUserState, vendorID3GPP)),
			SGSNUserState: u32Ptr(child(children(child(us, avpSGSNUserState, vendorID3GPP)), avpUserState, vendorID3GPP)),
		}
	}
	out.EPSLocationInformation = decodeEPSLocationInformation(child(top, avpEPSLocationInformation, vendorID3GPP))
	if tz := children(child(top, avpLocalTimeZone, vendorID3GPP)); tz != nil {
		out.LocalTimeZone = &pb.LocalTimeZone{
			TimeZone:           AVPString(child(tz, avpTimeZone, vendorID3GPP)),
			DaylightSavingTime: u32Value(child(tz, avpDaylightSavingTime, vendorID3GPP)),
		}
	}
	return out
}

// ─── Writing ─────────────────────────────────────────────────────────────

// applyRequestCommon writes what the application may give every request
// it sends: DRMP (RFC 7944; the M bit must not be set, TS 29.272 Table
// 7.3.1/2) and Supported-Features, which a request always sends with the M
// bit (TS 29.229 §7.2.1).
func applyRequestCommon(m *diam.Message, event *pb.S6aEvent) {
	if event.DRMP != 0 {
		m.AddAVP(diam.NewAVP(avp.DRMP, 0, 0, datatype.Enumerated(event.DRMP)))
	}
	for _, sf := range event.SupportedFeatures {
		m.AddAVP(avp3GPP(avp.SupportedFeatures, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(sf.VendorID)),
			avp3GPP(avp.FeatureListID, datatype.Unsigned32(sf.FeatureListID)),
			avp3GPP(avp.FeatureList, datatype.Unsigned32(sf.FeatureList)),
		}}))
	}
}

// hexAVPs writes one 3GPP octet-string AVP per hex value; invalid hex is an
// error, so the request is not sent half-built.
func hexAVPs(m *diam.Message, code uint32, field string, values []string) error {
	for i, v := range values {
		b, err := hex.DecodeString(v)
		if err != nil {
			return fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		if len(b) == 0 {
			return fmt.Errorf("%s[%d]: empty", field, i)
		}
		m.AddAVP(avp3GPP(code, datatype.OctetString(b)))
	}
	return nil
}

// encodeUTRANGERANVectors writes the UTRAN-Vector and GERAN-Vector AVPs of
// an AIA's Authentication-Info (§7.3.19, §7.3.20).
func encodeUTRANGERANVectors(g *diam.GroupedAVP, utran []pb.UTRANVector, geran []pb.GERANVector) error {
	decode := func(field string, i int, values ...string) ([][]byte, error) {
		out := make([][]byte, len(values))
		for j, v := range values {
			b, err := hex.DecodeString(v)
			if err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
			}
			out[j] = b
		}
		return out, nil
	}
	for i, v := range utran {
		b, err := decode("utran_vectors", i, v.RAND, v.XRES, v.AUTN, v.CK, v.IK)
		if err != nil {
			return err
		}
		vec := &diam.GroupedAVP{}
		if v.Item != 0 {
			vec.AddAVP(avp3GPP(avp.ItemNumber, datatype.Unsigned32(v.Item)))
		}
		vec.AddAVP(avp3GPP(avp.RAND, datatype.OctetString(b[0])))
		vec.AddAVP(avp3GPP(avp.XRES, datatype.OctetString(b[1])))
		vec.AddAVP(avp3GPP(avp.AUTN, datatype.OctetString(b[2])))
		vec.AddAVP(avp3GPP(avpConfidentialityKey, datatype.OctetString(b[3])))
		vec.AddAVP(avp3GPP(avpIntegrityKey, datatype.OctetString(b[4])))
		g.AddAVP(avp3GPP(avp.UTRANVector, vec))
	}
	for i, v := range geran {
		b, err := decode("geran_vectors", i, v.RAND, v.SRES, v.Kc)
		if err != nil {
			return err
		}
		vec := &diam.GroupedAVP{}
		if v.Item != 0 {
			vec.AddAVP(avp3GPP(avp.ItemNumber, datatype.Unsigned32(v.Item)))
		}
		vec.AddAVP(avp3GPP(avp.RAND, datatype.OctetString(b[0])))
		vec.AddAVP(avp3GPP(avp.SRES, datatype.OctetString(b[1])))
		vec.AddAVP(avp3GPP(avp.Kc, datatype.OctetString(b[2])))
		g.AddAVP(avp3GPP(avp.GERANVector, vec))
	}
	return nil
}

// ApplyRequestExtras writes the fields of an application's CLR, IDR, DSR or
// RSR beyond those its Create function takes (TS 29.272 §7.2.7, §7.2.9,
// §7.2.11, §7.2.15). An invalid hex value is an error.
func ApplyRequestExtras(m *diam.Message, cmd string, event *pb.S6aEvent) error {
	applyRequestCommon(m, event)
	switch cmd {
	case "CLR":
		if event.CLRFlags != 0 {
			m.AddAVP(avp3GPP(avpCLRFlags, datatype.Unsigned32(event.CLRFlags)))
		}
	case "IDR":
		return hexAVPs(m, avpResetID, "reset_ids", event.ResetIDs)
	case "DSR":
		if event.TraceReference != "" {
			if err := hexAVPs(m, avp.TraceReference, "trace_reference", []string{event.TraceReference}); err != nil {
				return err
			}
		}
		if err := hexAVPs(m, avp.TSCode, "ts_codes", event.TSCodes); err != nil {
			return err
		}
		return hexAVPs(m, avp.SSCode, "ss_codes", event.SSCodes)
	case "RSR":
		for _, id := range event.UserIDs {
			if id != "" {
				m.AddAVP(avp3GPP(avpUserID, datatype.UTF8String(id)))
			}
		}
		return hexAVPs(m, avpResetID, "reset_ids", event.ResetIDs)
	}
	return nil
}

// ValidateRequestExtras reports what ApplyRequestExtras would refuse,
// before anything is sent.
func ValidateRequestExtras(cmd string, event *pb.S6aEvent) error {
	return ApplyRequestExtras(diam.NewRequest(0, 0, nil), cmd, event)
}
