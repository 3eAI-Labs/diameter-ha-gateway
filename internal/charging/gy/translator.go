// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gy

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

// AVP codes for Credit-Control (RFC 4006 + TS 32.299)
const (
	// RFC 4006 AVPs
	avpCCRequestType                 = 416
	avpCCRequestNumber               = 415
	avpCCSubSessionID                = 419
	avpAcctMultiSessionID            = 50
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
	avpServiceInformation            = 873 // 3GPP
	avpPSInformation                 = 874 // 3GPP
	avpCCTime                        = 420
	avpCCMoney                       = 413
	avpCCTotalOctets                 = 421
	avpCCInputOctets                 = 412
	avpCCOutputOctets                = 414
	avpCCServiceSpecificUnits        = 417
	avpGrantedServiceUnit            = 431
	avpRatingGroup                   = 432
	avpValidityTime                  = 448
	avpFinalUnitIndication           = 430
	avpFinalUnitAction               = 449
	avpRestrictionFilterRule         = 438
	avpFilterID                      = 11
	avpRedirectServer                = 434
	avpRedirectAddressType           = 433
	avpRedirectServerAddress         = 435
	avpResultCode                    = 268
	avpFailedAVP                     = 279
	avpTimeQuotaThreshold            = 868 // 3GPP
	avpVolumeQuotaThreshold          = 869 // 3GPP
	avpQuotaHoldingTime              = 871 // 3GPP
	avpQuotaConsumptionTime          = 881 // 3GPP
	avpReportingReason               = 872 // 3GPP
	avpTriggerType                   = 870 // 3GPP
	avpGSUPoolReference              = 457
	avpGSUPoolIdentifier             = 453
	avpCCUnitType                    = 454
	avpUnitValue                     = 445
	avpValueDigits                   = 447
	avpExponent                      = 429
	avpCurrencyCode                  = 425
)

// 3GPP Vendor ID
const vendorID3GPP = 10415

// Translator handles Gy protocol translation
type Translator struct{}

// NewTranslator creates a new Gy translator
func NewTranslator() *Translator {
	return &Translator{}
}

// ToCCREvent converts a Diameter CCR message to GyEvent
func (t *Translator) ToCCREvent(m *diam.Message, traceID string) (*GyEvent, error) {
	event := &GyEvent{
		Header: t.extractHeader(m, traceID),
	}

	// Mandatory Credit Control AVPs
	if a := t.findAVP(m, avpCCRequestType, 0); a != nil {
		event.CCRequestType = charging.U32(a)
	}
	if a := t.findAVP(m, avpCCRequestNumber, 0); a != nil {
		event.CCRequestNumber = charging.U32(a)
	}

	// Optional Credit Control AVPs
	if a := t.findAVP(m, avpCCSubSessionID, 0); a != nil {
		event.CCSubSessionID = charging.U64(a)
	}
	if a, err := m.FindAVP(avpAcctMultiSessionID, 0); err == nil {
		event.AcctMultiSessionID = avpString(a)
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

	// Multiple-Services-Credit-Control
	event.MSCC = charging.ReadMSCCs(m.AVP)

	// Termination
	if a := t.findAVP(m, avpTerminationCause, 0); a != nil {
		event.TerminationCause = charging.U32(a)
	}

	// 3GPP Service-Information
	if a := t.findAVP(m, avpServiceInformation, vendorID3GPP); a != nil {
		event.ServiceInformation = t.parseServiceInformation(a)
	}

	return event, nil
}

// ToCCAMessage converts a GyAnswer to a Diameter CCA message
func (t *Translator) ToCCAMessage(answer *GyAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)

	// Add CC-Request-Type and Number (echo from request)
	ans.NewAVP(avpCCRequestType, avp.Mbit, 0, datatype.Enumerated(answer.CCRequestType))
	ans.NewAVP(avpCCRequestNumber, avp.Mbit, 0, datatype.Unsigned32(answer.CCRequestNumber))

	// Origin info if provided
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
	// Redirect-Host (RFC 8506 CCA, RFC 6733 §6.12-6.14)
	for _, h := range answer.RedirectHost {
		ans.AddAVP(diam.NewAVP(avp.RedirectHost, avp.Mbit, 0, datatype.DiameterURI(h)))
	}
	if len(answer.RedirectHost) > 0 {
		ans.AddAVP(diam.NewAVP(avp.RedirectHostUsage, avp.Mbit, 0, datatype.Enumerated(answer.RedirectHostUsage)))
		if answer.RedirectMaxCacheTime > 0 {
			ans.AddAVP(diam.NewAVP(avp.RedirectMaxCacheTime, avp.Mbit, 0, datatype.Unsigned32(answer.RedirectMaxCacheTime)))
		}
	}

	return ans, nil
}

// ToJSONAnswer converts a JSON response to GyAnswer
func (t *Translator) ToJSONAnswer(data []byte) (*GyAnswer, error) {
	var answer GyAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal GyAnswer: %w", err)
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

// findAVP searches for an AVP manually (for non-dictionary AVPs)
func (t *Translator) findAVP(m *diam.Message, code, vendorID uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

// findAVPInGrouped searches for an AVP in a grouped AVP
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

func (t *Translator) parseServiceInformation(a *diam.AVP) *charging.ServiceInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		si := &charging.ServiceInformation{AVPs: charging.ReadAVPs(g.AVP)}

		// PS-Information
		if psAVP := t.findAVPInGrouped(g, avpPSInformation, vendorID3GPP); psAVP != nil {
			si.PSInformation = t.parsePSInformation(psAVP)
		}

		return si
	}
	return nil
}

func (t *Translator) parsePSInformation(a *diam.AVP) *charging.PSInformation {
	if g, ok := a.Data.(*diam.GroupedAVP); ok {
		ps := &charging.PSInformation{}

		// 3GPP-Charging-Id
		if cgid := t.findAVPInGrouped(g, 2, vendorID3GPP); cgid != nil {
			ps.ThreeGPPChargingID = cgid.Data.Serialize()
		}

		// Called-Station-Id
		if csid := t.findAVPInGrouped(g, 30, 0); csid != nil {
			ps.CalledStationID = avpString(csid)
		}

		// 3GPP-SGSN-MCC-MNC
		if mccmnc := t.findAVPInGrouped(g, 18, vendorID3GPP); mccmnc != nil {
			ps.ThreeGPPSGSNMCCMNC = avpString(mccmnc)
		}

		// 3GPP-User-Location-Info
		if uli := t.findAVPInGrouped(g, 22, vendorID3GPP); uli != nil {
			ps.ThreeGPPUserLocationInfo = uli.Data.Serialize()
		}

		// 3GPP-RAT-Type
		if rat := t.findAVPInGrouped(g, 21, vendorID3GPP); rat != nil {
			ps.ThreeGPPRATType = rat.Data.Serialize()
		}

		return ps
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
func (t *Translator) GetIMSI(event *GyEvent) string {
	for _, sub := range event.SubscriptionID {
		if sub.Type == charging.SubscriptionIDTypeEndUserIMSI {
			return sub.Data
		}
	}
	return ""
}

// GetMSISDN extracts MSISDN from subscription IDs
func (t *Translator) GetMSISDN(event *GyEvent) string {
	for _, sub := range event.SubscriptionID {
		if sub.Type == charging.SubscriptionIDTypeEndUserE164 {
			return sub.Data
		}
	}
	return ""
}

// HexEncode encodes bytes to hex string
func HexEncode(data []byte) string {
	return hex.EncodeToString(data)
}
