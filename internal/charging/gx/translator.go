// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// AVP codes for Gx interface (3GPP TS 29.212)
const (
	// RFC 4006 AVPs
	avpCCRequestType          = 416
	avpCCRequestNumber        = 415
	avpOriginStateID          = 278
	avpSubscriptionID         = 443
	avpSubscriptionIDType     = 450
	avpSubscriptionIDData     = 444
	avpUserEquipmentInfo      = 458
	avpUserEquipmentInfoType  = 459
	avpUserEquipmentInfoValue = 460
	avpTerminationCause       = 295
	avpResultCode             = 268
	avpFailedAVP              = 279

	// Network Location (3GPP)
	avpNetworkRequestSupport = 1024 // 3GPP
	avpIPCANType             = 1027 // 3GPP
	avpRATType               = 1032 // 3GPP
	avpANGWAddress           = 1050 // 3GPP
	avpANGWStatus            = 2811 // 3GPP

	// Framed IP
	avpFramedIPAddress  = 8
	avpFramedIPv6Prefix = 97

	// 3GPP Specific
	avp3GPPSGSNMCCMNC       = 18 // 3GPP
	avp3GPPUserLocationInfo = 22 // 3GPP
	avp3GPPMSTimeZone       = 23 // 3GPP
	avpCalledStationID      = 30
	avpPDNConnectionID      = 1065 // 3GPP
	avpBearerIdentifier     = 1020 // 3GPP
	avpBearerOperation      = 1021 // 3GPP

	// QoS Information
	avpQoSInformation              = 1016 // 3GPP
	avpQoSClassIdentifier          = 1028 // 3GPP
	avpMaxRequestedBandwidthUL     = 516  // 3GPP
	avpMaxRequestedBandwidthDL     = 515  // 3GPP
	avpGuaranteedBitrateUL         = 1026 // 3GPP
	avpGuaranteedBitrateDL         = 1025 // 3GPP
	avpAllocationRetentionPriority = 1034 // 3GPP
	avpPriorityLevel               = 1046 // 3GPP
	avpPreEmptionCapability        = 1047 // 3GPP
	avpPreEmptionVulnerability     = 1048 // 3GPP
	avpDefaultEPSBearerQoS         = 1049 // 3GPP
	avpAPNAggregateMaxBitrateUL    = 1041 // 3GPP
	avpAPNAggregateMaxBitrateDL    = 1040 // 3GPP

	// Event Trigger
	avpEventTrigger = 1006 // 3GPP

	// Usage Monitoring
	avpUsageMonitoringInformation = 1067 // 3GPP
	avpMonitoringKey              = 1066 // 3GPP
	avpGrantedServiceUnit         = 431
	avpUsedServiceUnit            = 446
	avpCCTime                     = 420
	avpCCTotalOctets              = 421
	avpCCInputOctets              = 412
	avpCCOutputOctets             = 414
	avpUsageMonitoringLevel       = 1068 // 3GPP
	avpUsageMonitoringReport      = 1069 // 3GPP
	avpUsageMonitoringSupport     = 1070 // 3GPP

	// Charging Rules
	avpChargingRuleInstall    = 1001 // 3GPP
	avpChargingRuleRemove     = 1002 // 3GPP
	avpChargingRuleName       = 1005 // 3GPP
	avpChargingRuleBaseName   = 1004 // 3GPP
	avpChargingRuleDefinition = 1003 // 3GPP
	avpChargingRuleReport     = 1018 // 3GPP
	avpPCCRuleStatus          = 1019 // 3GPP
	avpRuleFailureCode        = 1031 // 3GPP

	// Rule Definition AVPs
	avpServiceIdentifier     = 439
	avpRatingGroup           = 432
	avpFlowDescription       = 507  // 3GPP
	avpFlowInformation       = 1058 // 3GPP
	avpFlowStatus            = 511  // 3GPP
	avpReportingLevel        = 1011 // 3GPP
	avpOnline                = 1009 // 3GPP
	avpOffline               = 1008 // 3GPP
	avpMeteringMethod        = 1007 // 3GPP
	avpPrecedence            = 1010 // 3GPP
	avpRedirectInformation   = 1085 // 3GPP
	avpRedirectSupport       = 1086 // 3GPP
	avpFlowDirection         = 1080 // 3GPP
	avpSessionReleaseCause   = 1045 // 3GPP
	avpRedirectAddressType   = 433  // RFC 8506
	avpRedirectServerAddress = 435  // RFC 8506
	avpRuleActivationTime    = 1043 // 3GPP
	avpRuleDeactivationTime  = 1044 // 3GPP

	// Revalidation
	avpRevalidationTime = 1042 // 3GPP
)

// 3GPP Vendor ID
const vendorID3GPP = 10415

// Translator handles Gx protocol translation
type Translator struct{}

// NewTranslator creates a new Gx translator
func NewTranslator() *Translator {
	return &Translator{}
}

// ToCCREvent converts a Diameter CCR message to GxEvent
func (t *Translator) ToCCREvent(m *diam.Message, traceID string) (*GxEvent, error) {
	event := &GxEvent{
		Header: t.extractHeader(m, traceID),
	}

	// Credit Control AVPs
	if a := t.findAVP(m, avpCCRequestType, 0); a != nil {
		event.CCRequestType = u32(a)
	}
	if a := t.findAVP(m, avpCCRequestNumber, 0); a != nil {
		event.CCRequestNumber = u32(a)
	}
	if a := t.findAVP(m, avpOriginStateID, 0); a != nil {
		event.OriginStateID = u32(a)
	}

	// Subscription IDs
	event.SubscriptionID = t.parseSubscriptionIDs(m)

	// User Equipment Info
	if a := t.findAVP(m, avpUserEquipmentInfo, 0); a != nil {
		event.UserEquipmentInfo = t.parseUserEquipmentInfo(a)
	}

	// Network Location (3GPP)
	if a := t.findAVP(m, avpNetworkRequestSupport, vendorID3GPP); a != nil {
		event.NetworkRequestSupport = u32p(a)
	}
	if a := t.findAVP(m, avpIPCANType, vendorID3GPP); a != nil {
		event.IPCANType = u32p(a)
	}
	if a := t.findAVP(m, avpRATType, vendorID3GPP); a != nil {
		event.RATType = u32p(a)
	}
	for _, a := range m.AVP {
		if a.Code == avpANGWAddress && a.VendorID == vendorID3GPP {
			event.ANGWAddresses = append(event.ANGWAddresses, a.Data.Serialize())
		}
	}
	if len(event.ANGWAddresses) > 0 {
		event.ANGWAddress = event.ANGWAddresses[0]
	}
	if a := t.findAVP(m, avpANGWStatus, vendorID3GPP); a != nil {
		event.ANGWStatus = u32p(a)
	}

	// Framed IP
	if a := t.findAVP(m, avpFramedIPAddress, 0); a != nil {
		event.FramedIPAddress = a.Data.Serialize()
	}
	if a := t.findAVP(m, avpFramedIPv6Prefix, 0); a != nil {
		event.FramedIPv6Prefix = a.Data.Serialize()
	}

	// 3GPP Specific
	if a := t.findAVP(m, avp3GPPSGSNMCCMNC, vendorID3GPP); a != nil {
		event.ThreeGPPSGSNMCCMNC = avpString(a)
	}
	if a := t.findAVP(m, avp3GPPUserLocationInfo, vendorID3GPP); a != nil {
		event.ThreeGPPUserLocationInfo = a.Data.Serialize()
	}
	if a := t.findAVP(m, avp3GPPMSTimeZone, vendorID3GPP); a != nil {
		event.ThreeGPPMSTimeZone = a.Data.Serialize()
	}
	if a := t.findAVP(m, avpCalledStationID, 0); a != nil {
		event.CalledStationID = avpString(a)
	}
	if a := t.findAVP(m, avpPDNConnectionID, vendorID3GPP); a != nil {
		event.PDNConnectionID = a.Data.Serialize() // OctetString, TS 29.212 §5.3.58
	}
	if a := t.findAVP(m, avpBearerIdentifier, vendorID3GPP); a != nil {
		event.BearerIdentifier = a.Data.Serialize()
	}
	if a := t.findAVP(m, avpBearerOperation, vendorID3GPP); a != nil {
		event.BearerOperation = u32p(a)
	}

	// QoS Information
	if a := t.findAVP(m, avpQoSInformation, vendorID3GPP); a != nil {
		event.QoSInformation = t.parseQoSInformation(a)
	}
	if a := t.findAVP(m, avpDefaultEPSBearerQoS, vendorID3GPP); a != nil {
		event.DefaultEPSBearerQoS = t.parseDefaultEPSBearerQoS(a)
	}
	// APN-AMBR is QoS-Information's APN-Aggregate-Max-Bitrate-UL/DL (TS
	// 29.212 §5.3.16); apn_ambr repeats it for applications of earlier
	// releases.
	if q := event.QoSInformation; q != nil && (q.APNAggregateMaxBitrateUL != 0 || q.APNAggregateMaxBitrateDL != 0) {
		event.APNAMBR = &APNAMBR{APNAggregateMaxBitrateUL: q.APNAggregateMaxBitrateUL, APNAggregateMaxBitrateDL: q.APNAggregateMaxBitrateDL}
	}

	// Event Triggers
	event.EventTrigger = t.parseEventTriggers(m)

	// Usage Monitoring
	event.UsageMonitoringInformation = t.parseUsageMonitoringInfos(m)

	// Charging Rule Reports
	event.ChargingRuleReport = t.parseChargingRuleReports(m)

	// Termination
	if a := t.findAVP(m, avpTerminationCause, 0); a != nil {
		event.TerminationCause = u32(a)
	}

	return event, nil
}

// ToCCAMessage converts a GxAnswer to a Diameter CCA message
func (t *Translator) ToCCAMessage(answer *GxAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)
	if answer.ExperimentalResultCode != 0 {
		// A 3GPP result travels in Experimental-Result, without Result-Code.
		ans = req.Answer(0)
		ans.AddAVP(diam.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(vendorID3GPP)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(answer.ExperimentalResultCode)),
		}}))
	}

	// CC-Request-Type and Number
	ans.NewAVP(avpCCRequestType, avp.Mbit, 0, datatype.Enumerated(answer.CCRequestType))
	ans.NewAVP(avpCCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(answer.CCRequestNumber))

	// Origin info
	if answer.OriginHost != "" {
		ans.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(answer.OriginHost))
	}
	if answer.OriginRealm != "" {
		ans.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(answer.OriginRealm))
	}

	// Charging Rule Install
	for _, cri := range answer.ChargingRuleInstall {
		ans.AddAVP(avpGx(avpChargingRuleInstall, t.buildChargingRuleInstall(&cri)))
	}

	// Charging Rule Remove
	for _, crr := range answer.ChargingRuleRemove {
		ans.AddAVP(avpGx(avpChargingRuleRemove, t.buildChargingRuleRemove(&crr)))
	}

	// QoS Information, with apn_ambr folded in: TS 29.212 has no APN-AMBR
	// AVP, only QoS-Information's APN-Aggregate-Max-Bitrate-UL/DL.
	if qos := withAPNAMBR(answer.QoSInformation, answer.APNAMBR); qos != nil {
		ans.AddAVP(avpGx(avpQoSInformation, t.buildQoSInformation(qos)))
	}

	// Default EPS Bearer QoS
	if answer.DefaultEPSBearerQoS != nil {
		ans.AddAVP(avpGx(avpDefaultEPSBearerQoS, t.buildDefaultEPSBearerQoS(answer.DefaultEPSBearerQoS)))
	}

	// Event Triggers
	for _, et := range answer.EventTrigger {
		ans.AddAVP(avpGx(avpEventTrigger, datatype.Enumerated(et)))
	}

	// Revalidation Time
	if answer.RevalidationTime > 0 {
		ans.AddAVP(avpGx(avpRevalidationTime, datatype.Time(time.Unix(answer.RevalidationTime, 0))))
	}

	if answer.SessionReleaseCause != nil {
		ans.AddAVP(avpGx(avpSessionReleaseCause, datatype.Enumerated(*answer.SessionReleaseCause)))
	}

	// Usage Monitoring Information
	for _, umi := range answer.UsageMonitoringInformation {
		ans.AddAVP(avpGx(avpUsageMonitoringInformation, t.buildUsageMonitoringInfo(&umi)))
	}

	// Failed AVP
	for _, fa := range answer.FailedAVP {
		ans.NewAVP(avpFailedAVP, avp.Mbit, 0, &diam.GroupedAVP{
			AVP: []*diam.AVP{
				diam.NewAVP(fa.Code, 0, fa.VendorID, datatype.OctetString(fa.Data)),
			},
		})
	}

	return ans, nil
}

// CreateRAR builds a Re-Auth-Request (RFC 4006 §3.2.4 + TS 29.212 §5.6.2) for
// PCRF→PCEF reverse direction. sessionID MUST be the original CCR-I session
// id; this is NOT a fresh session — RAR rides the existing PCEF session.
//
// Returns the message with H2H/E2E IDs already populated by go-diameter.
// Caller registers sessionID in pendingReqs to correlate the inbound RAA.
func (t *Translator) CreateRAR(sessionID, destHost, destRealm, originHost, originRealm string,
	req *GxReAuthRequest) *diam.Message {

	// "REQ, PXY" (RAR: TS 29.212 §5.6.4; ASR: RFC 6733 §8.5.1); NewRequest sets R only.
	m := diam.NewMessage(CommandRAR, diam.RequestFlag|diam.ProxiableFlag, AppIDGx, 0, 0, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sessionID))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))
	m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDGx))
	// Re-Auth-Request-Type AVP code 285 (RFC 4006 §8.12).
	m.NewAVP(285, avp.Mbit, 0, datatype.Enumerated(req.ReAuthRequestType))

	for _, cri := range req.ChargingRuleInstall {
		m.AddAVP(avpGx(avpChargingRuleInstall, t.buildChargingRuleInstall(&cri)))
	}
	for _, crr := range req.ChargingRuleRemove {
		m.AddAVP(avpGx(avpChargingRuleRemove, t.buildChargingRuleRemove(&crr)))
	}
	if req.QoSInformation != nil {
		m.AddAVP(avpGx(avpQoSInformation, t.buildQoSInformation(req.QoSInformation)))
	}
	if req.DefaultEPSBearerQoS != nil {
		m.AddAVP(avpGx(avpDefaultEPSBearerQoS, t.buildDefaultEPSBearerQoS(req.DefaultEPSBearerQoS)))
	}
	for _, et := range req.EventTrigger {
		m.AddAVP(avpGx(avpEventTrigger, datatype.Enumerated(et)))
	}
	if req.SessionReleaseCause != nil {
		// Session-Release-Cause AVP code 1045 (TS 29.212 §5.3.46).
		m.AddAVP(avpGx(avpSessionReleaseCause, datatype.Enumerated(*req.SessionReleaseCause)))
	}

	return m
}

// CreateASR builds an Abort-Session-Request (RFC 6733 §8.5.1, RFC 4006
// §3.2.5; TS 29.212 defines none) for PCRF→PCEF reverse direction. Like RAR, sessionID is the
// original CCR-I session id — ASR rides the existing PCEF session.
//
// Per TS 29.214 §5.3.1, Abort-Cause (AVP 500) is OPTIONAL but operationally
// recommended so the PCEF can disambiguate teardown intent in CDRs. Pass
// AbortCauseBearerReleased (0) when in doubt.
func (t *Translator) CreateASR(sessionID, destHost, destRealm, originHost, originRealm string,
	req *GxAbortSessionRequest) *diam.Message {

	// "REQ, PXY" (RAR: TS 29.212 §5.6.4; ASR: RFC 6733 §8.5.1); NewRequest sets R only.
	m := diam.NewMessage(CommandASR, diam.RequestFlag|diam.ProxiableFlag, AppIDGx, 0, 0, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sessionID))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))
	m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	m.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDGx))
	// Abort-Cause AVP code 500 (TS 29.214 §5.3.1). Always emit so the PCEF
	// sees an explicit cause rather than relying on its default.
	m.AddAVP(avpGx(500, datatype.Enumerated(req.AbortCause)))
	return m
}

// ToJSONAnswer converts a JSON response to GxAnswer
func (t *Translator) ToJSONAnswer(data []byte) (*GxAnswer, error) {
	var answer GxAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal GxAnswer: %w", err)
	}
	return &answer, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

func (t *Translator) extractHeader(m *diam.Message, traceID string) charging.DiameterHeader {
	h := charging.DiameterHeader{
		TraceID:           traceID,
		AuthApplicationID: m.Header.ApplicationID,
		HopByHop:          m.Header.HopByHopID,
		EndToEnd:          m.Header.EndToEndID,
		Timestamp:         time.Now(),
	}

	if a, err := m.FindAVP(avp.SessionID, 0); err == nil {
		h.SessionID = avpString(a)
	}
	if a, err := m.FindAVP(avp.OriginHost, 0); err == nil {
		h.OriginHost = avpString(a)
	}
	if a, err := m.FindAVP(avp.OriginRealm, 0); err == nil {
		h.OriginRealm = avpString(a)
	}
	if a, err := m.FindAVP(avp.DestinationHost, 0); err == nil {
		h.DestinationHost = avpString(a)
	}
	if a, err := m.FindAVP(avp.DestinationRealm, 0); err == nil {
		h.DestinationRealm = avpString(a)
	}

	return h
}

func (t *Translator) findAVP(m *diam.Message, code, vendorID uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

func (t *Translator) findAVPInGrouped(g *diam.GroupedAVP, code, vendorID uint32) *diam.AVP {
	for _, a := range g.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

func avpString(a *diam.AVP) string {
	switch v := a.Data.(type) {
	case datatype.UTF8String:
		return string(v)
	case datatype.DiameterIdentity:
		return string(v)
	case datatype.OctetString:
		return string(v)
	default:
		return "" // String() is go-diameter's debug format, not the value
	}
}

func (t *Translator) parseSubscriptionIDs(m *diam.Message) []charging.SubscriptionID {
	var subs []charging.SubscriptionID

	for _, a := range m.AVP {
		if a.Code == avpSubscriptionID && a.VendorID == 0 {
			if g, ok := a.Data.(*diam.GroupedAVP); ok {
				var sub charging.SubscriptionID
				if typeAVP := t.findAVPInGrouped(g, avpSubscriptionIDType, 0); typeAVP != nil {
					sub.Type = u32(typeAVP)
				}
				if dataAVP := t.findAVPInGrouped(g, avpSubscriptionIDData, 0); dataAVP != nil {
					sub.Data = avpString(dataAVP)
				}
				subs = append(subs, sub)
			}
		}
	}

	return subs
}

func (t *Translator) parseUserEquipmentInfo(a *diam.AVP) *charging.UserEquipmentInfo {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		info := &charging.UserEquipmentInfo{}
		if typeAVP := t.findAVPInGrouped(g, avpUserEquipmentInfoType, 0); typeAVP != nil {
			info.Type = u32(typeAVP)
		}
		if valAVP := t.findAVPInGrouped(g, avpUserEquipmentInfoValue, 0); valAVP != nil {
			info.Value = valAVP.Data.Serialize()
		}
		return info
	}
	return nil
}

func (t *Translator) parseQoSInformation(a *diam.AVP) *QoSInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		qos := &QoSInformation{}
		if qci := t.findAVPInGrouped(g, avpQoSClassIdentifier, vendorID3GPP); qci != nil {
			qos.QoSClassIdentifier = u32(qci)
		}
		if ul := t.findAVPInGrouped(g, avpMaxRequestedBandwidthUL, vendorID3GPP); ul != nil {
			qos.MaxRequestedBandwidthUL = u32(ul)
		}
		if dl := t.findAVPInGrouped(g, avpMaxRequestedBandwidthDL, vendorID3GPP); dl != nil {
			qos.MaxRequestedBandwidthDL = u32(dl)
		}
		if gul := t.findAVPInGrouped(g, avpGuaranteedBitrateUL, vendorID3GPP); gul != nil {
			qos.GuaranteedBitrateUL = u32(gul)
		}
		if gdl := t.findAVPInGrouped(g, avpGuaranteedBitrateDL, vendorID3GPP); gdl != nil {
			qos.GuaranteedBitrateDL = u32(gdl)
		}
		if bi := t.findAVPInGrouped(g, avpBearerIdentifier, vendorID3GPP); bi != nil {
			qos.BearerIdentifier = bi.Data.Serialize()
		}
		if arp := t.findAVPInGrouped(g, avpAllocationRetentionPriority, vendorID3GPP); arp != nil {
			qos.AllocationRetentionPriority = t.parseARP(arp)
		}
		qos.APNAggregateMaxBitrateUL = u32(t.findAVPInGrouped(g, avpAPNAggregateMaxBitrateUL, vendorID3GPP))
		qos.APNAggregateMaxBitrateDL = u32(t.findAVPInGrouped(g, avpAPNAggregateMaxBitrateDL, vendorID3GPP))
		return qos
	}
	return nil
}

func (t *Translator) parseARP(a *diam.AVP) *AllocationRetentionPriority {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		arp := &AllocationRetentionPriority{}
		if pl := t.findAVPInGrouped(g, avpPriorityLevel, vendorID3GPP); pl != nil {
			arp.PriorityLevel = u32(pl)
		}
		if pc := t.findAVPInGrouped(g, avpPreEmptionCapability, vendorID3GPP); pc != nil {
			arp.PreEmptionCapability = u32p(pc)
		}
		if pv := t.findAVPInGrouped(g, avpPreEmptionVulnerability, vendorID3GPP); pv != nil {
			arp.PreEmptionVulnerability = u32p(pv)
		}
		return arp
	}
	return nil
}

func (t *Translator) parseDefaultEPSBearerQoS(a *diam.AVP) *DefaultEPSBearerQoS {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		qos := &DefaultEPSBearerQoS{}
		if qci := t.findAVPInGrouped(g, avpQoSClassIdentifier, vendorID3GPP); qci != nil {
			qos.QoSClassIdentifier = u32(qci)
		}
		if arp := t.findAVPInGrouped(g, avpAllocationRetentionPriority, vendorID3GPP); arp != nil {
			qos.AllocationRetentionPriority = t.parseARP(arp)
		}
		qos.APNAggregateMaxBitrateUL = u32(t.findAVPInGrouped(g, avpAPNAggregateMaxBitrateUL, vendorID3GPP))
		qos.APNAggregateMaxBitrateDL = u32(t.findAVPInGrouped(g, avpAPNAggregateMaxBitrateDL, vendorID3GPP))
		return qos
	}
	return nil
}

func (t *Translator) parseEventTriggers(m *diam.Message) []uint32 {
	var triggers []uint32
	for _, a := range m.AVP {
		if a.Code == avpEventTrigger && a.VendorID == vendorID3GPP {
			triggers = append(triggers, u32(a))
		}
	}
	return triggers
}

func (t *Translator) parseUsageMonitoringInfos(m *diam.Message) []UsageMonitoringInfo {
	var infos []UsageMonitoringInfo
	for _, a := range m.AVP {
		if a.Code == avpUsageMonitoringInformation && a.VendorID == vendorID3GPP {
			if g, ok := a.Data.(*diam.GroupedAVP); ok {
				infos = append(infos, t.parseUsageMonitoringInfo(g))
			}
		}
	}
	return infos
}

func (t *Translator) parseUsageMonitoringInfo(g *diam.GroupedAVP) UsageMonitoringInfo {
	umi := UsageMonitoringInfo{}
	if mk := t.findAVPInGrouped(g, avpMonitoringKey, vendorID3GPP); mk != nil {
		umi.MonitoringKey = mk.Data.Serialize()
	}
	if gsu := t.findAVPInGrouped(g, avpGrantedServiceUnit, 0); gsu != nil {
		umi.GrantedServiceUnit = t.parseServiceUnit(gsu)
	}
	if usu := t.findAVPInGrouped(g, avpUsedServiceUnit, 0); usu != nil {
		umi.UsedServiceUnit = t.parseServiceUnit(usu)
	}
	if uml := t.findAVPInGrouped(g, avpUsageMonitoringLevel, vendorID3GPP); uml != nil {
		umi.UsageMonitoringLevel = u32p(uml)
	}
	if umr := t.findAVPInGrouped(g, avpUsageMonitoringReport, vendorID3GPP); umr != nil {
		umi.UsageMonitoringReport = u32p(umr)
	}
	if ums := t.findAVPInGrouped(g, avpUsageMonitoringSupport, vendorID3GPP); ums != nil {
		umi.UsageMonitoringSupport = u32p(ums)
	}
	return umi
}

func (t *Translator) parseServiceUnit(a *diam.AVP) *charging.ServiceUnit {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		su := &charging.ServiceUnit{}
		if ccTime := t.findAVPInGrouped(g, avpCCTime, 0); ccTime != nil {
			su.CCTime = u32(ccTime)
		}
		if ccTotal := t.findAVPInGrouped(g, avpCCTotalOctets, 0); ccTotal != nil {
			su.CCTotalOctets = u64(ccTotal)
		}
		if ccIn := t.findAVPInGrouped(g, avpCCInputOctets, 0); ccIn != nil {
			su.CCInputOctets = u64(ccIn)
		}
		if ccOut := t.findAVPInGrouped(g, avpCCOutputOctets, 0); ccOut != nil {
			su.CCOutputOctets = u64(ccOut)
		}
		return su
	}
	return nil
}

// ChargingRuleReports reads the Charging-Rule-Report AVPs of a CCR or RAA
// (TS 29.212 §5.3.18).
func (t *Translator) ChargingRuleReports(m *diam.Message) []ChargingRuleReport {
	return t.parseChargingRuleReports(m)
}

func (t *Translator) parseChargingRuleReports(m *diam.Message) []ChargingRuleReport {
	var reports []ChargingRuleReport
	for _, a := range m.AVP {
		if a.Code == avpChargingRuleReport && a.VendorID == vendorID3GPP {
			if g, ok := a.Data.(*diam.GroupedAVP); ok {
				reports = append(reports, t.parseChargingRuleReport(g))
			}
		}
	}
	return reports
}

func (t *Translator) parseChargingRuleReport(g *diam.GroupedAVP) ChargingRuleReport {
	crr := ChargingRuleReport{}
	for _, a := range g.AVP {
		if a.Code == avpChargingRuleName && a.VendorID == vendorID3GPP {
			crr.ChargingRuleName = append(crr.ChargingRuleName, avpString(a))
		}
		if a.Code == avpChargingRuleBaseName && a.VendorID == vendorID3GPP {
			crr.ChargingRuleBaseName = append(crr.ChargingRuleBaseName, avpString(a))
		}
	}
	if bi := t.findAVPInGrouped(g, avpBearerIdentifier, vendorID3GPP); bi != nil {
		crr.BearerIdentifier = bi.Data.Serialize()
	}
	if status := t.findAVPInGrouped(g, avpPCCRuleStatus, vendorID3GPP); status != nil {
		crr.PCCRuleStatus = u32p(status)
	}
	if fc := t.findAVPInGrouped(g, avpRuleFailureCode, vendorID3GPP); fc != nil {
		crr.RuleFailureCode = u32(fc)
	}
	return crr
}

// =============================================================================
// Build AVP Helpers
// =============================================================================

func (t *Translator) buildChargingRuleInstall(cri *ChargingRuleInstall) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	for _, name := range cri.ChargingRuleName {
		g.AVP = append(g.AVP, avpGx(avpChargingRuleName, datatype.OctetString(name)))
	}
	for _, baseName := range cri.ChargingRuleBaseName {
		g.AVP = append(g.AVP, avpGx(avpChargingRuleBaseName, datatype.UTF8String(baseName)))
	}
	for _, rule := range cri.ChargingRuleDefinition {
		g.AVP = append(g.AVP, avpGx(avpChargingRuleDefinition, t.buildChargingRuleDefinition(&rule)))
	}
	if len(cri.BearerIdentifier) > 0 {
		g.AVP = append(g.AVP, avpGx(avpBearerIdentifier, datatype.OctetString(cri.BearerIdentifier)))
	}
	if cri.RuleActivationTime > 0 {
		g.AVP = append(g.AVP, avpGx(avpRuleActivationTime, datatype.Time(time.Unix(cri.RuleActivationTime, 0))))
	}
	if cri.RuleDeactivationTime > 0 {
		g.AVP = append(g.AVP, avpGx(avpRuleDeactivationTime, datatype.Time(time.Unix(cri.RuleDeactivationTime, 0))))
	}

	return g
}

func (t *Translator) buildChargingRuleRemove(crr *ChargingRuleRemove) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	for _, name := range crr.ChargingRuleName {
		g.AVP = append(g.AVP, avpGx(avpChargingRuleName, datatype.OctetString(name)))
	}
	for _, baseName := range crr.ChargingRuleBaseName {
		g.AVP = append(g.AVP, avpGx(avpChargingRuleBaseName, datatype.UTF8String(baseName)))
	}

	return g
}

func (t *Translator) buildChargingRuleDefinition(rule *ChargingRule) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	g.AVP = append(g.AVP, avpGx(avpChargingRuleName, datatype.OctetString(rule.ChargingRuleName)))

	if rule.ServiceIdentifier > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpServiceIdentifier, avp.Mbit, 0, datatype.Unsigned32(rule.ServiceIdentifier)))
	}
	if rule.RatingGroup > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpRatingGroup, avp.Mbit, 0, datatype.Unsigned32(rule.RatingGroup)))
	}
	// Flow-Description belongs in Flow-Information (TS 29.212 §5.3.4,
	// §5.3.53); flow_description entries each become one.
	for _, fi := range rule.FlowInformation {
		g.AVP = append(g.AVP, avpGx(avpFlowInformation, buildFlowInformation(fi)))
	}
	for _, fd := range rule.FlowDescription {
		g.AVP = append(g.AVP, avpGx(avpFlowInformation, buildFlowInformation(FlowInfo{FlowDescription: fd})))
	}
	if rule.FlowStatus != nil {
		g.AVP = append(g.AVP, avpGx(avpFlowStatus, datatype.Enumerated(*rule.FlowStatus)))
	}
	if rule.QoSInformation != nil {
		g.AVP = append(g.AVP, avpGx(avpQoSInformation, t.buildQoSInformation(rule.QoSInformation)))
	}
	if rule.ReportingLevel != nil {
		g.AVP = append(g.AVP, avpGx(avpReportingLevel, datatype.Enumerated(*rule.ReportingLevel)))
	}
	if rule.Online != nil {
		g.AVP = append(g.AVP, avpGx(avpOnline, datatype.Enumerated(*rule.Online)))
	}
	if rule.Offline != nil {
		g.AVP = append(g.AVP, avpGx(avpOffline, datatype.Enumerated(*rule.Offline)))
	}
	if rule.MeteringMethod != nil {
		g.AVP = append(g.AVP, avpGx(avpMeteringMethod, datatype.Enumerated(*rule.MeteringMethod)))
	}
	if rule.Precedence > 0 {
		g.AVP = append(g.AVP, avpGx(avpPrecedence, datatype.Unsigned32(rule.Precedence)))
	}
	if len(rule.MonitoringKey) > 0 {
		g.AVP = append(g.AVP, avpGx(avpMonitoringKey, datatype.OctetString(rule.MonitoringKey)))
	}
	if r := rule.RedirectInformation; r != nil {
		// Redirect-Information (§5.3.82): Redirect-Support (Gx) and the
		// RFC 8506 Redirect-Address-Type and Redirect-Server-Address.
		ri := &diam.GroupedAVP{}
		if r.RedirectSupport != nil {
			ri.AddAVP(avpGx(avpRedirectSupport, datatype.Enumerated(*r.RedirectSupport)))
		}
		if r.RedirectServerAddress != "" {
			if r.RedirectAddressType != nil {
				ri.AddAVP(diam.NewAVP(avpRedirectAddressType, avp.Mbit, 0, datatype.Enumerated(*r.RedirectAddressType)))
			}
			ri.AddAVP(diam.NewAVP(avpRedirectServerAddress, avp.Mbit, 0, datatype.UTF8String(r.RedirectServerAddress)))
		}
		g.AVP = append(g.AVP, avpGx(avpRedirectInformation, ri))
	}

	return g
}

func (t *Translator) buildQoSInformation(qos *QoSInformation) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	if qos.QoSClassIdentifier > 0 {
		g.AVP = append(g.AVP, avpGx(avpQoSClassIdentifier, datatype.Enumerated(qos.QoSClassIdentifier)))
	}
	if qos.MaxRequestedBandwidthUL > 0 {
		g.AVP = append(g.AVP, avpGx(avpMaxRequestedBandwidthUL, datatype.Unsigned32(qos.MaxRequestedBandwidthUL)))
	}
	if qos.MaxRequestedBandwidthDL > 0 {
		g.AVP = append(g.AVP, avpGx(avpMaxRequestedBandwidthDL, datatype.Unsigned32(qos.MaxRequestedBandwidthDL)))
	}
	if qos.GuaranteedBitrateUL > 0 {
		g.AVP = append(g.AVP, avpGx(avpGuaranteedBitrateUL, datatype.Unsigned32(qos.GuaranteedBitrateUL)))
	}
	if qos.GuaranteedBitrateDL > 0 {
		g.AVP = append(g.AVP, avpGx(avpGuaranteedBitrateDL, datatype.Unsigned32(qos.GuaranteedBitrateDL)))
	}
	if len(qos.BearerIdentifier) > 0 {
		g.AVP = append(g.AVP, avpGx(avpBearerIdentifier, datatype.OctetString(qos.BearerIdentifier)))
	}
	if qos.AllocationRetentionPriority != nil {
		g.AVP = append(g.AVP, avpGx(avpAllocationRetentionPriority, t.buildARP(qos.AllocationRetentionPriority)))
	}
	if qos.APNAggregateMaxBitrateUL > 0 {
		g.AVP = append(g.AVP, avpGx(avpAPNAggregateMaxBitrateUL, datatype.Unsigned32(qos.APNAggregateMaxBitrateUL)))
	}
	if qos.APNAggregateMaxBitrateDL > 0 {
		g.AVP = append(g.AVP, avpGx(avpAPNAggregateMaxBitrateDL, datatype.Unsigned32(qos.APNAggregateMaxBitrateDL)))
	}

	return g
}

func (t *Translator) buildARP(arp *AllocationRetentionPriority) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	g.AVP = append(g.AVP, avpGx(avpPriorityLevel, datatype.Unsigned32(arp.PriorityLevel)))
	if arp.PreEmptionCapability != nil {
		g.AVP = append(g.AVP, avpGx(avpPreEmptionCapability, datatype.Enumerated(*arp.PreEmptionCapability)))
	}
	if arp.PreEmptionVulnerability != nil {
		g.AVP = append(g.AVP, avpGx(avpPreEmptionVulnerability, datatype.Enumerated(*arp.PreEmptionVulnerability)))
	}

	return g
}

func (t *Translator) buildDefaultEPSBearerQoS(qos *DefaultEPSBearerQoS) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	if qos.QoSClassIdentifier > 0 { // QCI values start at 1 (§5.3.17)
		g.AVP = append(g.AVP, avpGx(avpQoSClassIdentifier, datatype.Enumerated(qos.QoSClassIdentifier)))
	}
	if qos.AllocationRetentionPriority != nil {
		g.AVP = append(g.AVP, avpGx(avpAllocationRetentionPriority, t.buildARP(qos.AllocationRetentionPriority)))
	}

	return g
}

func (t *Translator) buildUsageMonitoringInfo(umi *UsageMonitoringInfo) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	if len(umi.MonitoringKey) > 0 {
		g.AVP = append(g.AVP, avpGx(avpMonitoringKey, datatype.OctetString(umi.MonitoringKey)))
	}
	if umi.GrantedServiceUnit != nil {
		g.AVP = append(g.AVP, diam.NewAVP(avpGrantedServiceUnit, avp.Mbit, 0, t.buildServiceUnit(umi.GrantedServiceUnit)))
	}
	if umi.UsageMonitoringLevel != nil {
		g.AVP = append(g.AVP, avpGx(avpUsageMonitoringLevel, datatype.Enumerated(*umi.UsageMonitoringLevel)))
	}
	if umi.UsageMonitoringReport != nil {
		g.AVP = append(g.AVP, avpGx(avpUsageMonitoringReport, datatype.Enumerated(*umi.UsageMonitoringReport)))
	}
	if umi.UsageMonitoringSupport != nil {
		g.AVP = append(g.AVP, avpGx(avpUsageMonitoringSupport, datatype.Enumerated(*umi.UsageMonitoringSupport)))
	}

	return g
}

func (t *Translator) buildServiceUnit(su *charging.ServiceUnit) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	if su.CCTime > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpCCTime, avp.Mbit, 0, datatype.Unsigned32(su.CCTime)))
	}
	if su.CCTotalOctets > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpCCTotalOctets, avp.Mbit, 0, datatype.Unsigned64(su.CCTotalOctets)))
	}
	if su.CCInputOctets > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpCCInputOctets, avp.Mbit, 0, datatype.Unsigned64(su.CCInputOctets)))
	}
	if su.CCOutputOctets > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpCCOutputOctets, avp.Mbit, 0, datatype.Unsigned64(su.CCOutputOctets)))
	}

	return g
}

// MaskIMSI masks the middle digits of an IMSI for logging
func (t *Translator) MaskIMSI(imsi string) string {
	if len(imsi) < 8 {
		return "****"
	}
	return imsi[:6] + "******" + imsi[len(imsi)-2:]
}

// GetIMSI extracts IMSI from subscription IDs
func (t *Translator) GetIMSI(event *GxEvent) string {
	for _, sub := range event.SubscriptionID {
		if sub.Type == charging.SubscriptionIDTypeEndUserIMSI {
			return sub.Data
		}
	}
	return ""
}

// HexEncode encodes bytes to hex string
func HexEncode(data []byte) string {
	return hex.EncodeToString(data)
}

// withAPNAMBR is qos with the legacy apn_ambr folded into its
// APN-Aggregate-Max-Bitrate-UL/DL where those are not set.
func withAPNAMBR(qos *QoSInformation, ambr *APNAMBR) *QoSInformation {
	if ambr == nil {
		return qos
	}
	out := QoSInformation{}
	if qos != nil {
		out = *qos
	}
	if out.APNAggregateMaxBitrateUL == 0 {
		out.APNAggregateMaxBitrateUL = ambr.APNAggregateMaxBitrateUL
	}
	if out.APNAggregateMaxBitrateDL == 0 {
		out.APNAggregateMaxBitrateDL = ambr.APNAggregateMaxBitrateDL
	}
	return &out
}

func buildFlowInformation(fi FlowInfo) *diam.GroupedAVP {
	g := &diam.GroupedAVP{}
	if fi.FlowDescription != "" {
		g.AddAVP(avpGx(avpFlowDescription, datatype.IPFilterRule(fi.FlowDescription)))
	}
	if dir := flowDirection(fi); dir != nil {
		g.AddAVP(avpGx(avpFlowDirection, datatype.Enumerated(*dir)))
	}
	return g
}

// u32 reads an Unsigned32, Enumerated or Integer32 AVP; anything else, or
// nil, reads as 0. u32p keeps absence apart from 0.
func u32(a *diam.AVP) uint32 { v, _ := u32ok(a); return v }

func u32p(a *diam.AVP) *uint32 {
	if v, ok := u32ok(a); ok {
		return &v
	}
	return nil
}

func u32ok(a *diam.AVP) (uint32, bool) {
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

func u64(a *diam.AVP) uint64 {
	if a == nil {
		return 0
	}
	switch v := a.Data.(type) {
	case datatype.Unsigned64:
		return uint64(v)
	case datatype.Unsigned32:
		return uint64(v)
	}
	return 0
}

// flowDirection is fi's Flow-Direction: as given, or for a description
// "permit out …" DOWNLINK (1) and "permit in …" UPLINK (2) (TS 29.212
// §5.3.53: "out" refers to downlink, "in" to uplink). With neither, none.
func flowDirection(fi FlowInfo) *uint32 {
	if fi.FlowDirection != nil {
		return fi.FlowDirection
	}
	f := strings.Fields(fi.FlowDescription)
	if len(f) < 2 {
		return nil
	}
	var dir uint32
	switch f[1] {
	case "out":
		dir = 1
	case "in":
		dir = 2
	default:
		return nil
	}
	return &dir
}
