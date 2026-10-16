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
		event.CCRequestType = uint32(a.Data.(datatype.Enumerated))
	}
	if a := t.findAVP(m, avpCCRequestNumber, 0); a != nil {
		event.CCRequestNumber = uint32(a.Data.(datatype.Unsigned32))
	}
	if a := t.findAVP(m, avpOriginStateID, 0); a != nil {
		event.OriginStateID = uint32(a.Data.(datatype.Unsigned32))
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
		event.ServiceIdentifier = uint32(a.Data.(datatype.Unsigned32))
	}
	if a := t.findAVP(m, avpRequestedAction, 0); a != nil {
		event.RequestedAction = uint32(a.Data.(datatype.Enumerated))
	}

	// Service Units
	if a := t.findAVP(m, avpRequestedServiceUnit, 0); a != nil {
		event.RequestedServiceUnit = t.parseServiceUnit(a)
	}
	if a := t.findAVP(m, avpUsedServiceUnit, 0); a != nil {
		event.UsedServiceUnit = t.parseServiceUnit(a)
	}
	if a := t.findAVP(m, avpMultipleServicesIndicator, 0); a != nil {
		event.MultipleServicesIndicator = uint32(a.Data.(datatype.Enumerated))
	}

	// MSCC
	event.MSCC = t.parseMSCCs(m)

	// Termination
	if a := t.findAVP(m, avpTerminationCause, 0); a != nil {
		event.TerminationCause = uint32(a.Data.(datatype.Enumerated))
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

	// MSCC
	for _, mscc := range answer.MSCC {
		ans.NewAVP(avpMultipleServicesCreditControl, avp.Mbit, 0, t.buildMSCCGrouped(&mscc))
	}

	// Validity Time
	if answer.ValidityTime > 0 {
		ans.NewAVP(avpValidityTime, avp.Mbit, 0, datatype.Unsigned32(answer.ValidityTime))
	}

	// Final Unit Indication
	if answer.FinalUnitIndication != nil {
		ans.NewAVP(avpFinalUnitIndication, avp.Mbit, 0, t.buildFinalUnitIndication(answer.FinalUnitIndication))
	}

	// Failed AVP
	for _, fa := range answer.FailedAVP {
		ans.NewAVP(avpFailedAVP, avp.Mbit, 0, &diam.GroupedAVP{
			AVP: []*diam.AVP{
				diam.NewAVP(fa.Code, 0, fa.VendorID, datatype.OctetString(fa.Data)),
			},
		})
	}

	// Low Balance (3GPP)
	if answer.LowBalanceIndication > 0 {
		ans.NewAVP(2020, avp.Mbit|avp.Vbit, vendorID3GPP, datatype.Enumerated(answer.LowBalanceIndication))
	}

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
					sub.Type = uint32(typeAVP.Data.(datatype.Enumerated))
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
			info.Type = uint32(typeAVP.Data.(datatype.Enumerated))
		}
		if valAVP := t.findAVPInGrouped(g, avpUserEquipmentInfoValue, 0); valAVP != nil {
			info.Value = valAVP.Data.Serialize()
		}
		return info
	}
	return nil
}

func (t *Translator) parseServiceUnit(a *diam.AVP) *charging.ServiceUnit {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		su := &charging.ServiceUnit{}
		if ccTime := t.findAVPInGrouped(g, avpCCTime, 0); ccTime != nil {
			su.CCTime = uint32(ccTime.Data.(datatype.Unsigned32))
		}
		if ccTotal := t.findAVPInGrouped(g, avpCCTotalOctets, 0); ccTotal != nil {
			su.CCTotalOctets = uint64(ccTotal.Data.(datatype.Unsigned64))
		}
		if ccIn := t.findAVPInGrouped(g, avpCCInputOctets, 0); ccIn != nil {
			su.CCInputOctets = uint64(ccIn.Data.(datatype.Unsigned64))
		}
		if ccOut := t.findAVPInGrouped(g, avpCCOutputOctets, 0); ccOut != nil {
			su.CCOutputOctets = uint64(ccOut.Data.(datatype.Unsigned64))
		}
		if ccSSU := t.findAVPInGrouped(g, avpCCServiceSpecificUnits, 0); ccSSU != nil {
			su.CCServiceSpecificUnits = uint64(ccSSU.Data.(datatype.Unsigned64))
		}
		return su
	}
	return nil
}

func (t *Translator) parseMSCCs(m *diam.Message) []charging.MSCC {
	var msccs []charging.MSCC

	for _, a := range m.AVP {
		if a.Code == avpMultipleServicesCreditControl && a.VendorID == 0 {
			if g, ok := a.Data.(*diam.GroupedAVP); ok {
				mscc := charging.MSCC{}

				if rg := t.findAVPInGrouped(g, avpRatingGroup, 0); rg != nil {
					mscc.RatingGroup = uint32(rg.Data.(datatype.Unsigned32))
				}
				if si := t.findAVPInGrouped(g, avpServiceIdentifier, 0); si != nil {
					mscc.ServiceIdentifier = uint32(si.Data.(datatype.Unsigned32))
				}
				if rsu := t.findAVPInGrouped(g, avpRequestedServiceUnit, 0); rsu != nil {
					mscc.RequestedServiceUnit = t.parseServiceUnit(rsu)
				}

				// Used Service Units
				for _, sub := range g.AVP {
					if sub.Code == avpUsedServiceUnit && sub.VendorID == 0 {
						if usu := t.parseServiceUnit(sub); usu != nil {
							mscc.UsedServiceUnit = append(mscc.UsedServiceUnit, *usu)
						}
					}
				}

				msccs = append(msccs, mscc)
			}
		}
	}

	return msccs
}

func (t *Translator) parseServiceInformation(a *diam.AVP) *RoServiceInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		si := &RoServiceInformation{}

		// IMS-Information
		if imsAVP := t.findAVPInGrouped(g, avpIMSInformation, vendorID3GPP); imsAVP != nil {
			si.IMSInformation = t.parseIMSInformation(imsAVP)
		}

		return si
	}
	return nil
}

func (t *Translator) parseIMSInformation(a *diam.AVP) *IMSInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		ims := &IMSInformation{}

		if et := t.findAVPInGrouped(g, avpEventType, vendorID3GPP); et != nil {
			ims.EventType = uint32(et.Data.(datatype.Enumerated))
		}
		if ron := t.findAVPInGrouped(g, avpRoleOfNode, vendorID3GPP); ron != nil {
			ims.RoleOfNode = uint32(ron.Data.(datatype.Enumerated))
		}
		if nf := t.findAVPInGrouped(g, avpNodeFunctionality, vendorID3GPP); nf != nil {
			ims.NodeFunctionality = uint32(nf.Data.(datatype.Enumerated))
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
			ims.CauseCode = int32(cc.Data.(datatype.Integer32))
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

func (t *Translator) buildMSCCGrouped(mscc *charging.MSCC) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	g.AVP = append(g.AVP, diam.NewAVP(avpRatingGroup, avp.Mbit, 0, datatype.Unsigned32(mscc.RatingGroup)))

	if mscc.ServiceIdentifier > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpServiceIdentifier, avp.Mbit, 0, datatype.Unsigned32(mscc.ServiceIdentifier)))
	}

	if mscc.GrantedServiceUnit != nil {
		g.AVP = append(g.AVP, diam.NewAVP(avpGrantedServiceUnit, avp.Mbit, 0, t.buildServiceUnit(mscc.GrantedServiceUnit)))
	}

	if mscc.ResultCode > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpResultCode, avp.Mbit, 0, datatype.Unsigned32(mscc.ResultCode)))
	}

	if mscc.ValidityTime > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpValidityTime, avp.Mbit, 0, datatype.Unsigned32(mscc.ValidityTime)))
	}

	if mscc.FinalUnitIndication != nil {
		g.AVP = append(g.AVP, diam.NewAVP(avpFinalUnitIndication, avp.Mbit, 0, t.buildFinalUnitIndication(mscc.FinalUnitIndication)))
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
	if su.CCServiceSpecificUnits > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(avpCCServiceSpecificUnits, avp.Mbit, 0, datatype.Unsigned64(su.CCServiceSpecificUnits)))
	}

	return g
}

func (t *Translator) buildFinalUnitIndication(fui *charging.FinalUnitIndication) *diam.GroupedAVP {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}
	g.AVP = append(g.AVP, diam.NewAVP(avpFinalUnitAction, avp.Mbit, 0, datatype.Enumerated(fui.FinalUnitAction)))
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
