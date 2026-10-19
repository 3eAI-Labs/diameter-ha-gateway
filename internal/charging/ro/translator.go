// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package ro

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// AVP codes for Ro interface (RFC 4006 + TS 32.260)
const (
	// RFC 4006 AVPs
	avpCCRequestType                 = 416
	avpCCRequestNumber               = 415
	avpOriginStateID                 = 278
	avpEventTimestamp                = 55
	avpSubscriptionID                = 443
	avpSubscriptionIDType            = 450
	avpSubscriptionIDData            = 444
	avpUserEquipmentInfo             = 458
	avpUserEquipmentInfoType         = 459
	avpUserEquipmentInfoValue        = 460
	avpServiceContextID              = 461
	avpServiceIdentifier             = 439
	avpRequestedAction               = 436
	avpRequestedServiceUnit          = 437
	avpUsedServiceUnit               = 446
	avpMultipleServicesIndicator     = 455
	avpMultipleServicesCreditControl = 456
	avpTerminationCause              = 295
	avpResultCode                    = 268
	avpFailedAVP                     = 279
	avpCCTime                        = 420
	avpCCTotalOctets                 = 421
	avpCCInputOctets                 = 412
	avpCCOutputOctets                = 414
	avpCCServiceSpecificUnits        = 417
	avpGrantedServiceUnit            = 431
	avpRatingGroup                   = 432
	avpValidityTime                  = 448
	avpFinalUnitIndication           = 430
	avpFinalUnitAction               = 449

	// 3GPP Service-Information
	avpServiceInformation = 873
	avpIMSInformation     = 876

	// IMS-Information AVPs
	avpEventType               = 823
	avpSIPMethod               = 824
	avpEvent                   = 825
	avpExpires                 = 888
	avpRoleOfNode              = 829
	avpNodeFunctionality       = 862
	avpUserSessionID           = 830
	avpOutgoingSessionID       = 2320
	avpSessionPriority         = 650
	avpCallingPartyAddress     = 831
	avpCalledPartyAddress      = 832
	avpRequestedPartyAddress   = 1251
	avpAssociatedURI           = 856
	avpTimeStamps              = 833
	avpSIPRequestTimestamp     = 834
	avpSIPResponseTimestamp    = 835
	avpAppServerInformation    = 850
	avpAppServerName           = 851
	avpInterOperatorIdentifier = 838
	avpOriginatingIOI          = 839
	avpTerminatingIOI          = 840
	avpIMSChargingIdentifier   = 841
	avpSDPSessionDescription   = 842
	avpSDPMediaComponent       = 843
	avpSDPMediaName            = 844
	avpSDPMediaDescription     = 845
	avpServedPartyIPAddress    = 848
	avpCauseCode               = 861
	avpAccessNetworkInfo       = 1263
)

// 3GPP Vendor ID
const vendorID3GPP = 10415

// Translator handles Ro protocol translation
type Translator struct{}

// NewTranslator creates a new Ro translator
func NewTranslator() *Translator {
	return &Translator{}
}

// ToCCREvent converts a Diameter CCR message to RoEvent
func (t *Translator) ToCCREvent(m *diam.Message, traceID string) (*RoEvent, error) {
	event := &RoEvent{
		Header: t.extractHeader(m, traceID),
	}

	// Credit Control AVPs
	if a := t.findAVP(m, avpCCRequestType, 0); a != nil {
		event.CCRequestType = charging.U32(a)
	}
	if a := t.findAVP(m, avpCCRequestNumber, 0); a != nil {
		event.CCRequestNumber = charging.U32(a)
	}
	if a := t.findAVP(m, avpOriginStateID, 0); a != nil {
		event.OriginStateID = charging.U32(a)
	}
	if a, err := m.FindAVP(avpEventTimestamp, 0); err == nil {
		event.EventTimestamp = avpTime(a)
	}

	// Subscription IDs
	event.SubscriptionID = t.parseSubscriptionIDs(m)

	// User Equipment Info
	if a := t.findAVP(m, avpUserEquipmentInfo, 0); a != nil {
		event.UserEquipmentInfo = t.parseUserEquipmentInfo(a)
	}

	// Service Context
	if a := t.findAVP(m, avpServiceContextID, 0); a != nil {
		event.ServiceContextID = avpString(a)
	}
	if a := t.findAVP(m, avpServiceIdentifier, 0); a != nil {
		event.ServiceIdentifier = charging.U32(a)
	}
	if a := t.findAVP(m, avpRequestedAction, 0); a != nil {
		event.RequestedAction = charging.U32(a)
	}

	// Service Units
	if a := t.findAVP(m, avpRequestedServiceUnit, 0); a != nil {
		event.RequestedServiceUnit = charging.ReadServiceUnit(a)
	}
	if a := t.findAVP(m, avpUsedServiceUnit, 0); a != nil {
		event.UsedServiceUnit = charging.ReadServiceUnit(a)
	}
	if a := t.findAVP(m, avpMultipleServicesIndicator, 0); a != nil {
		event.MultipleServicesIndicator = charging.U32(a)
	}

	// MSCC
	event.MSCC = charging.ReadMSCCs(m.AVP)

	// Termination
	if a := t.findAVP(m, avpTerminationCause, 0); a != nil {
		event.TerminationCause = charging.U32(a)
	}

	// Service-Information (IMS)
	if a := t.findAVP(m, avpServiceInformation, vendorID3GPP); a != nil {
		event.ServiceInformation = t.parseServiceInformation(a)
	}

	return event, nil
}

// ToCCAMessage converts a RoAnswer to a Diameter CCA message
func (t *Translator) ToCCAMessage(answer *RoAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)

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

	// Multiple-Services-Credit-Control: the codec Gy and Ro share
	for i := range answer.MSCC {
		ans.AddAVP(charging.WriteMSCC(&answer.MSCC[i]))
	}

	// Validity Time
	if answer.ValidityTime > 0 {
		ans.NewAVP(avpValidityTime, avp.Mbit, 0, datatype.Unsigned32(answer.ValidityTime))
	}

	// Final Unit Indication
	if answer.FinalUnitIndication != nil {
		ans.AddAVP(charging.WriteFinalUnitIndication(answer.FinalUnitIndication))
	}

	if si := answer.ServiceInformation; si != nil {
		a, err := charging.ServiceInformationAVP(si.AVPs)
		if err != nil {
			return nil, err
		}
		if a != nil {
			ans.AddAVP(a)
		}
	}
	charging.ApplyCCATail(ans, charging.CCATail{
		CCSessionFailover:    answer.CCSessionFailover,
		LowBalanceIndication: answer.LowBalanceIndication,
		RemainingBalance:     answer.RemainingBalance,
		CostInformation:      answer.CostInformation,
		FailedAVP:            answer.FailedAVP,
	})

	return ans, nil
}

// ToJSONAnswer converts a JSON response to RoAnswer
func (t *Translator) ToJSONAnswer(data []byte) (*RoAnswer, error) {
	var answer RoAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal RoAnswer: %w", err)
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

func avpTime(a *diam.AVP) int64 {
	if t, ok := a.Data.(datatype.Time); ok {
		return time.Time(t).Unix()
	}
	return 0
}

func (t *Translator) parseSubscriptionIDs(m *diam.Message) []charging.SubscriptionID {
	var subs []charging.SubscriptionID

	for _, a := range m.AVP {
		if a.Code == avpSubscriptionID && a.VendorID == 0 {
			if g, ok := a.Data.(*diam.GroupedAVP); ok {
				var sub charging.SubscriptionID
				if typeAVP := t.findAVPInGrouped(g, avpSubscriptionIDType, 0); typeAVP != nil {
					sub.Type = charging.U32(typeAVP)
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
			info.Type = charging.U32(typeAVP)
		}
		if valAVP := t.findAVPInGrouped(g, avpUserEquipmentInfoValue, 0); valAVP != nil {
			info.Value = valAVP.Data.Serialize()
		}
		return info
	}
	return nil
}

func (t *Translator) parseServiceInformation(a *diam.AVP) *RoServiceInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		si := &RoServiceInformation{AVPs: charging.ReadAVPs(g.AVP)}

		// IMS-Information
		if imsAVP := t.findAVPInGrouped(g, avpIMSInformation, vendorID3GPP); imsAVP != nil {
			si.IMSInformation = t.parseIMSInformation(imsAVP)
		}

		return si
	}
	return nil
}

// parseEventType decodes Event-Type, a Grouped AVP (TS 32.299 §7.2.65).
func (t *Translator) parseEventType(a *diam.AVP) *EventType {
	g, ok := a.Data.(*diam.GroupedAVP)
	if !ok {
		return nil
	}
	et := &EventType{}
	if v := t.findAVPInGrouped(g, avpSIPMethod, vendorID3GPP); v != nil {
		et.SIPMethod = avpString(v)
	}
	if v := t.findAVPInGrouped(g, avpEvent, vendorID3GPP); v != nil {
		et.Event = avpString(v)
	}
	if v := t.findAVPInGrouped(g, avpExpires, vendorID3GPP); v != nil {
		if n, ok := v.Data.(datatype.Unsigned32); ok {
			et.Expires = uint32(n)
		}
	}
	return et
}

func (t *Translator) parseIMSInformation(a *diam.AVP) *IMSInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		ims := &IMSInformation{}

		if et := t.findAVPInGrouped(g, avpEventType, vendorID3GPP); et != nil {
			ims.EventType = t.parseEventType(et)
		}
		if ron := t.findAVPInGrouped(g, avpRoleOfNode, vendorID3GPP); ron != nil {
			ims.RoleOfNode = charging.U32(ron)
		}
		if nf := t.findAVPInGrouped(g, avpNodeFunctionality, vendorID3GPP); nf != nil {
			ims.NodeFunctionality = charging.U32(nf)
		}
		if usid := t.findAVPInGrouped(g, avpUserSessionID, vendorID3GPP); usid != nil {
			ims.UserSessionID = avpString(usid)
		}
		if cpa := t.findAVPInGrouped(g, avpCalledPartyAddress, vendorID3GPP); cpa != nil {
			ims.CalledPartyAddress = avpString(cpa)
		}
		if ici := t.findAVPInGrouped(g, avpIMSChargingIdentifier, vendorID3GPP); ici != nil {
			ims.IMSChargingIdentifier = avpString(ici)
		}
		if cc := t.findAVPInGrouped(g, avpCauseCode, vendorID3GPP); cc != nil {
			ims.CauseCode = int32(charging.U32(cc))
		}

		// Calling Party Addresses (multiple)
		for _, sub := range g.AVP {
			if sub.Code == avpCallingPartyAddress && sub.VendorID == vendorID3GPP {
				ims.CallingPartyAddress = append(ims.CallingPartyAddress, avpString(sub))
			}
		}

		// Time-Stamps
		if ts := t.findAVPInGrouped(g, avpTimeStamps, vendorID3GPP); ts != nil {
			ims.TimeStamps = t.parseTimeStamps(ts)
		}

		return ims
	}
	return nil
}

func (t *Translator) parseTimeStamps(a *diam.AVP) *TimeStamps {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		ts := &TimeStamps{}
		if srt := t.findAVPInGrouped(g, avpSIPRequestTimestamp, vendorID3GPP); srt != nil {
			ts.SIPRequestTimestamp = avpTime(srt)
		}
		if srest := t.findAVPInGrouped(g, avpSIPResponseTimestamp, vendorID3GPP); srest != nil {
			ts.SIPResponseTimestamp = avpTime(srest)
		}
		return ts
	}
	return nil
}

// =============================================================================
// Build AVP Helpers
// =============================================================================

// MaskIMSI masks the middle digits of an IMSI for logging
func (t *Translator) MaskIMSI(imsi string) string {
	if len(imsi) < 8 {
		return "****"
	}
	return imsi[:6] + "******" + imsi[len(imsi)-2:]
}

// GetIMSI extracts IMSI from subscription IDs
func (t *Translator) GetIMSI(event *RoEvent) string {
	for _, sub := range event.SubscriptionID {
		if sub.Type == charging.SubscriptionIDTypeEndUserIMSI {
			return sub.Data
		}
	}
	return ""
}

// GetCallingParty extracts the calling party address from IMS information
func (t *Translator) GetCallingParty(event *RoEvent) string {
	if event.ServiceInformation != nil && event.ServiceInformation.IMSInformation != nil {
		if len(event.ServiceInformation.IMSInformation.CallingPartyAddress) > 0 {
			return event.ServiceInformation.IMSInformation.CallingPartyAddress[0]
		}
	}
	return ""
}

// HexEncode encodes bytes to hex string
func HexEncode(data []byte) string {
	return hex.EncodeToString(data)
}
