// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

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

// Translator handles Diameter AVP <-> JSON conversion for SGd
type Translator struct{}

// NewTranslator creates a new SGd translator
func NewTranslator() *Translator {
	return &Translator{}
}

// =============================================================================
// OFR (MO-Forward-Short-Message) Translation
// =============================================================================

// ToOFREvent converts a Diameter OFR message to OFREvent
func (t *Translator) ToOFREvent(m *diam.Message, traceID string) (*OFREvent, error) {
	event := &OFREvent{
		Header: t.extractHeader(m, traceID),
	}

	// Extract User-Identifier (grouped AVP)
	if userIdAVP := t.findAVP(m.AVP, AVPUserIdentifierValue, VendorID3GPP); userIdAVP != nil {
		event.UserIdentifier = t.parseUserIdentifier(userIdAVP)
	}

	// Extract SM-RP-UI (SMS PDU)
	if smrpuiAVP := t.findAVP(m.AVP, AVPSMRPUICode, VendorID3GPP); smrpuiAVP != nil {
		event.SMRPUI = []byte(smrpuiAVP.Data.(datatype.OctetString))
	}

	// Extract SC-Address
	if scAddrAVP := t.findAVP(m.AVP, AVPServiceCentreAddress, VendorID3GPP); scAddrAVP != nil {
		event.SCAddress = t.formatAddress([]byte(scAddrAVP.Data.(datatype.OctetString)))
	}

	// Extract SMS-GMSC-Address
	if gmscAVP := t.findAVP(m.AVP, AVPSMSGMSCAddress, VendorID3GPP); gmscAVP != nil {
		event.SMSGMSCAddress = t.formatAddress([]byte(gmscAVP.Data.(datatype.OctetString)))
	}

	// Extract SMSMI-Correlation-ID
	if corrAVP := t.findAVP(m.AVP, AVPSMSMICorrelationID, VendorID3GPP); corrAVP != nil {
		event.SMSMICorrelationID = t.parseSMSMICorrelationID(corrAVP)
	}

	return event, nil
}

// ToOFAAnswer parses NATS response to OFAAnswer
func (t *Translator) ToOFAAnswer(data []byte) (*OFAAnswer, error) {
	var answer OFAAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal OFA answer: %w", err)
	}
	return &answer, nil
}

// ToOFAMessage builds a Diameter OFA message from OFAAnswer
func (t *Translator) ToOFAMessage(answer *OFAAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)

	// Vendor-Specific-Application-Id
	t.addVSAI(ans)

	// Experimental-Result if present
	if answer.ExperimentalResult != nil {
		t.addExperimentalResult(ans, answer.ExperimentalResult)
	}

	return ans, nil
}

// =============================================================================
// TFR (MT-Forward-Short-Message) Translation
// =============================================================================

// ToTFREvent converts a Diameter TFR message to TFREvent
func (t *Translator) ToTFREvent(m *diam.Message, traceID string) (*TFREvent, error) {
	event := &TFREvent{
		Header: t.extractHeader(m, traceID),
	}

	// Extract User-Identifier
	if userIdAVP := t.findAVP(m.AVP, AVPUserIdentifierValue, VendorID3GPP); userIdAVP != nil {
		event.UserIdentifier = t.parseUserIdentifier(userIdAVP)
	}

	// Extract SM-RP-UI (SMS PDU)
	if smrpuiAVP := t.findAVP(m.AVP, AVPSMRPUICode, VendorID3GPP); smrpuiAVP != nil {
		event.SMRPUI = []byte(smrpuiAVP.Data.(datatype.OctetString))
	}

	// Extract SM-RP-MTI
	if mtiAVP := t.findAVP(m.AVP, AVPSMRPMTI, VendorID3GPP); mtiAVP != nil {
		event.SMRPMTI = uint32(mtiAVP.Data.(datatype.Enumerated))
	}

	// Extract SM-RP-SMEA
	if smeaAVP := t.findAVP(m.AVP, AVPSMRPSMEA, VendorID3GPP); smeaAVP != nil {
		event.SMRPSMEA = []byte(smeaAVP.Data.(datatype.OctetString))
	}

	// Extract MME fields
	if mmeName := t.findAVP(m.AVP, AVPMMEName, VendorID3GPP); mmeName != nil {
		event.MMEName = string(mmeName.Data.(datatype.DiameterIdentity))
	}
	if mmeRealm := t.findAVP(m.AVP, AVPMMERealm, VendorID3GPP); mmeRealm != nil {
		event.MMERealm = string(mmeRealm.Data.(datatype.DiameterIdentity))
	}
	if mmeNum := t.findAVP(m.AVP, AVPMMENumberForMTSMS, VendorID3GPP); mmeNum != nil {
		event.MMENumberForMTSMS = []byte(mmeNum.Data.(datatype.OctetString))
	}

	// Extract SGSN fields
	if sgsnNum := t.findAVP(m.AVP, AVPSGSNNumber, VendorID3GPP); sgsnNum != nil {
		event.SGSNNumber = []byte(sgsnNum.Data.(datatype.OctetString))
	}
	if sgsnName := t.findAVP(m.AVP, AVPSGSNName, VendorID3GPP); sgsnName != nil {
		event.SGSNName = string(sgsnName.Data.(datatype.DiameterIdentity))
	}
	if sgsnRealm := t.findAVP(m.AVP, AVPSGSNRealm, VendorID3GPP); sgsnRealm != nil {
		event.SGSNRealm = string(sgsnRealm.Data.(datatype.DiameterIdentity))
	}

	// Extract SC-Address
	if scAddrAVP := t.findAVP(m.AVP, AVPServiceCentreAddress, VendorID3GPP); scAddrAVP != nil {
		event.SCAddress = t.formatAddress([]byte(scAddrAVP.Data.(datatype.OctetString)))
	}

	// Extract SM-Delivery-Timer
	if timerAVP := t.findAVP(m.AVP, AVPSMDeliveryTimer, VendorID3GPP); timerAVP != nil {
		event.SMDeliveryTimer = uint32(timerAVP.Data.(datatype.Unsigned32))
	}

	// Extract SM-Delivery-Start-Time
	if startAVP := t.findAVP(m.AVP, AVPSMDeliveryStartTime, VendorID3GPP); startAVP != nil {
		event.SMDeliveryStartTime = t.extractTime(startAVP)
	}

	// Extract Maximum-Retransmissions
	if maxRetryAVP := t.findAVP(m.AVP, AVPMaximumRetransmissions, VendorID3GPP); maxRetryAVP != nil {
		event.MaxRetransmissions = uint32(maxRetryAVP.Data.(datatype.Unsigned32))
	}

	// Extract SMSMI-Correlation-ID
	if corrAVP := t.findAVP(m.AVP, AVPSMSMICorrelationID, VendorID3GPP); corrAVP != nil {
		event.SMSMICorrelationID = t.parseSMSMICorrelationID(corrAVP)
	}

	return event, nil
}

// ToTFAAnswer parses NATS response to TFAAnswer
func (t *Translator) ToTFAAnswer(data []byte) (*TFAAnswer, error) {
	var answer TFAAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal TFA answer: %w", err)
	}
	return &answer, nil
}

// ToTFAMessage builds a Diameter TFA message from TFAAnswer
func (t *Translator) ToTFAMessage(answer *TFAAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)

	// Vendor-Specific-Application-Id
	t.addVSAI(ans)

	// Experimental-Result if present
	if answer.ExperimentalResult != nil {
		t.addExperimentalResult(ans, answer.ExperimentalResult)
	}

	// SM-Delivery-Outcome
	if answer.SMDeliveryOutcome != nil {
		t.addSMDeliveryOutcome(ans, answer.SMDeliveryOutcome)
	}

	// Absent-User-Diagnostic-SM
	if answer.AbsentUserDiagnosticSM > 0 {
		ans.NewAVP(AVPAbsentUserDiagnosticSM, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.Unsigned32(answer.AbsentUserDiagnosticSM))
	}

	return ans, nil
}

// =============================================================================
// SRR (Send-Routing-Info-for-SM) Translation
// =============================================================================

// ToSRREvent converts a Diameter SRR message to SRREvent
func (t *Translator) ToSRREvent(m *diam.Message, traceID string) (*SRREvent, error) {
	event := &SRREvent{
		Header: t.extractHeader(m, traceID),
	}

	// Extract User-Identifier
	if userIdAVP := t.findAVP(m.AVP, AVPUserIdentifierValue, VendorID3GPP); userIdAVP != nil {
		event.UserIdentifier = t.parseUserIdentifier(userIdAVP)
	}

	// Extract MSISDN (standalone)
	if msisdnAVP := t.findAVP(m.AVP, AVPMSISDN, VendorID3GPP); msisdnAVP != nil {
		event.MSISDN = []byte(msisdnAVP.Data.(datatype.OctetString))
	}

	// Extract SC-Address
	if scAddrAVP := t.findAVP(m.AVP, AVPServiceCentreAddress, VendorID3GPP); scAddrAVP != nil {
		event.SCAddress = t.formatAddress([]byte(scAddrAVP.Data.(datatype.OctetString)))
	}

	// Extract SM-RP-PRI (Priority)
	if priAVP := t.findAVP(m.AVP, 3318, VendorID3GPP); priAVP != nil { // SM-RP-PRI AVP code
		event.SMRPPri = uint32(priAVP.Data.(datatype.Enumerated))
	}

	// Extract SRR-Flags
	if flagsAVP := t.findAVP(m.AVP, AVPSRRFlags, VendorID3GPP); flagsAVP != nil {
		event.SRRFlags = uint32(flagsAVP.Data.(datatype.Unsigned32))
	}

	// Extract SM-Delivery-Not-Intended
	if notIntendedAVP := t.findAVP(m.AVP, AVPSMDeliveryNotIntended, VendorID3GPP); notIntendedAVP != nil {
		event.SMDeliveryNotIntended = uint32(notIntendedAVP.Data.(datatype.Enumerated))
	}

	return event, nil
}

// ToSRAAnswer parses NATS response to SRAAnswer
func (t *Translator) ToSRAAnswer(data []byte) (*SRAAnswer, error) {
	var answer SRAAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SRA answer: %w", err)
	}
	return &answer, nil
}

// ToSRAMessage builds a Diameter SRA message from SRAAnswer
func (t *Translator) ToSRAMessage(answer *SRAAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)

	// Vendor-Specific-Application-Id
	t.addVSAI(ans)

	// Experimental-Result if present
	if answer.ExperimentalResult != nil {
		t.addExperimentalResult(ans, answer.ExperimentalResult)
	}

	// User-Name (IMSI)
	if answer.UserName != "" {
		ans.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(answer.UserName))
	}

	// LMSI
	if len(answer.LMSI) > 0 {
		ans.NewAVP(AVPLMSI, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(answer.LMSI))
	}

	// Serving-Node(s)
	for _, sn := range answer.ServingNode {
		t.addServingNode(ans, &sn)
	}

	// Additional-Serving-Node(s)
	for _, sn := range answer.AdditionalServingNode {
		t.addServingNode(ans, &sn)
	}

	// MME fields (if not in Serving-Node)
	if answer.MMEName != "" {
		ans.NewAVP(AVPMMEName, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.DiameterIdentity(answer.MMEName))
	}
	if answer.MMERealm != "" {
		ans.NewAVP(AVPMMERealm, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.DiameterIdentity(answer.MMERealm))
	}
	if len(answer.MMENumberForMTSMS) > 0 {
		ans.NewAVP(AVPMMENumberForMTSMS, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(answer.MMENumberForMTSMS))
	}

	// SGSN fields
	if len(answer.SGSNNumber) > 0 {
		ans.NewAVP(AVPSGSNNumber, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(answer.SGSNNumber))
	}
	if answer.SGSNName != "" {
		ans.NewAVP(AVPSGSNName, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.DiameterIdentity(answer.SGSNName))
	}
	if answer.SGSNRealm != "" {
		ans.NewAVP(AVPSGSNRealm, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.DiameterIdentity(answer.SGSNRealm))
	}

	// MSC fields
	if len(answer.MSCNumber) > 0 {
		ans.NewAVP(AVPMSCNumber, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(answer.MSCNumber))
	}
	if answer.ThreeGPPSMSCAddress != "" {
		ans.NewAVP(AVPThreeGPPSMSCAddress, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.OctetString(answer.ThreeGPPSMSCAddress))
	}

	// MWD-Status
	if answer.MWDStatus > 0 {
		ans.NewAVP(AVPMWDStatus, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Unsigned32(answer.MWDStatus))
	}

	return ans, nil
}

// =============================================================================
// ALR (Alert-Service-Centre) Translation
// =============================================================================

// ToALREvent converts a Diameter ALR message to ALREvent
func (t *Translator) ToALREvent(m *diam.Message, traceID string) (*ALREvent, error) {
	event := &ALREvent{
		Header: t.extractHeader(m, traceID),
	}

	// Extract User-Identifier
	if userIdAVP := t.findAVP(m.AVP, AVPUserIdentifierValue, VendorID3GPP); userIdAVP != nil {
		event.UserIdentifier = t.parseUserIdentifier(userIdAVP)
	}

	// Extract SC-Address
	if scAddrAVP := t.findAVP(m.AVP, AVPServiceCentreAddress, VendorID3GPP); scAddrAVP != nil {
		event.SCAddress = t.formatAddress([]byte(scAddrAVP.Data.(datatype.OctetString)))
	}

	// Extract SMS-GMSC-Alert-Event
	if alertAVP := t.findAVP(m.AVP, AVPSMSGMSCAlertEvent, VendorID3GPP); alertAVP != nil {
		event.SMSGMSCAlertEvent = uint32(alertAVP.Data.(datatype.Enumerated))
	}

	// Extract Serving-Node
	if snAVP := t.findAVP(m.AVP, AVPServingNode, VendorID3GPP); snAVP != nil {
		event.ServingNode = t.parseServingNode(snAVP)
	}

	return event, nil
}

// ToALAAnswer parses NATS response to ALAAnswer
func (t *Translator) ToALAAnswer(data []byte) (*ALAAnswer, error) {
	var answer ALAAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ALA answer: %w", err)
	}
	return &answer, nil
}

// ToALAMessage builds a Diameter ALA message from ALAAnswer
func (t *Translator) ToALAMessage(answer *ALAAnswer, req *diam.Message) (*diam.Message, error) {
	ans := req.Answer(answer.ResultCode)

	// Vendor-Specific-Application-Id
	t.addVSAI(ans)

	// Experimental-Result if present
	if answer.ExperimentalResult != nil {
		t.addExperimentalResult(ans, answer.ExperimentalResult)
	}

	return ans, nil
}

// =============================================================================
// Helper Methods
// =============================================================================

func (t *Translator) extractHeader(m *diam.Message, traceID string) charging.DiameterHeader {
	header := charging.DiameterHeader{
		HopByHop:          m.Header.HopByHopID,
		EndToEnd:          m.Header.EndToEndID,
		AuthApplicationID: AppIDSGd,
		TraceID:           traceID,
		Timestamp:         time.Now(),
	}

	if sessionID, err := m.FindAVP(avp.SessionID, 0); err == nil {
		header.SessionID = string(sessionID.Data.(datatype.UTF8String))
	}
	if originHost, err := m.FindAVP(avp.OriginHost, 0); err == nil {
		header.OriginHost = string(originHost.Data.(datatype.DiameterIdentity))
	}
	if originRealm, err := m.FindAVP(avp.OriginRealm, 0); err == nil {
		header.OriginRealm = string(originRealm.Data.(datatype.DiameterIdentity))
	}
	if destHost, err := m.FindAVP(avp.DestinationHost, 0); err == nil {
		header.DestinationHost = string(destHost.Data.(datatype.DiameterIdentity))
	}
	if destRealm, err := m.FindAVP(avp.DestinationRealm, 0); err == nil {
		header.DestinationRealm = string(destRealm.Data.(datatype.DiameterIdentity))
	}

	return header
}

func (t *Translator) extractTime(a *diam.AVP) int64 {
	if dt, ok := a.Data.(datatype.Time); ok {
		return time.Time(dt).Unix()
	}
	return 0
}

func (t *Translator) findAVP(avps []*diam.AVP, code, vendorID uint32) *diam.AVP {
	for _, a := range avps {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
		// Also check with vendorID 0 for non-vendor-specific AVPs
		if vendorID == 0 && a.Code == code {
			return a
		}
	}
	return nil
}

func (t *Translator) parseUserIdentifier(avpData *diam.AVP) *UserIdentifier {
	grouped, ok := avpData.Data.(*diam.GroupedAVP)
	if !ok {
		return nil
	}

	ui := &UserIdentifier{}
	for _, a := range grouped.AVP {
		switch a.Code {
		case AVPMSISDN:
			ui.MSISDN = []byte(a.Data.(datatype.OctetString))
		case AVPUserName:
			ui.UserName = string(a.Data.(datatype.UTF8String))
		case AVPExternalIdentifier:
			ui.ExternalIdentifier = string(a.Data.(datatype.UTF8String))
		case AVPLMSI:
			ui.LMSI = []byte(a.Data.(datatype.OctetString))
		}
	}
	return ui
}

func (t *Translator) parseSMSMICorrelationID(avpData *diam.AVP) *SMSMICorrelationID {
	grouped, ok := avpData.Data.(*diam.GroupedAVP)
	if !ok {
		return nil
	}

	corr := &SMSMICorrelationID{}
	for _, a := range grouped.AVP {
		switch a.Code {
		case AVPHSSID:
			corr.HSSID = string(a.Data.(datatype.DiameterIdentity))
		case AVPOriginatorSCCPAddress:
			corr.OriginatorSCCPAddress = []byte(a.Data.(datatype.OctetString))
		}
	}
	return corr
}

func (t *Translator) parseServingNode(avpData *diam.AVP) *ServingNode {
	grouped, ok := avpData.Data.(*diam.GroupedAVP)
	if !ok {
		return nil
	}

	sn := &ServingNode{}
	for _, a := range grouped.AVP {
		switch a.Code {
		case AVPSGSNNumber:
			sn.SGSNNumber = []byte(a.Data.(datatype.OctetString))
		case AVPSGSNName:
			sn.SGSNName = string(a.Data.(datatype.DiameterIdentity))
		case AVPSGSNRealm:
			sn.SGSNRealm = string(a.Data.(datatype.DiameterIdentity))
		case AVPMMEName:
			sn.MMEName = string(a.Data.(datatype.DiameterIdentity))
		case AVPMMERealm:
			sn.MMERealm = string(a.Data.(datatype.DiameterIdentity))
		case AVPMMENumberForMTSMS:
			sn.MMENumberForMTSMS = []byte(a.Data.(datatype.OctetString))
		case AVPMSCNumber:
			sn.MSCNumber = []byte(a.Data.(datatype.OctetString))
		case AVPThreeGPPSMSCAddress:
			sn.ThreeGPPSMSCAddress = string(a.Data.(datatype.OctetString))
		}
	}
	return sn
}

func (t *Translator) addVSAI(msg *diam.Message) {
	vsaiAVP := diam.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDSGd)),
		},
	})
	msg.AVP = append(msg.AVP, vsaiAVP)
}

func (t *Translator) addExperimentalResult(msg *diam.Message, er *ExperimentalResult) {
	erAVP := diam.NewAVP(avp.ExperimentalResult, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(er.VendorID)),
			diam.NewAVP(avp.ExperimentalResultCode, avp.Mbit, 0, datatype.Unsigned32(er.ExperimentalResultCode)),
		},
	})
	msg.AVP = append(msg.AVP, erAVP)
}

func (t *Translator) addSMDeliveryOutcome(msg *diam.Message, outcome *SMDeliveryOutcome) {
	avps := []*diam.AVP{}

	if outcome.MMEAbsentUserDiagnosticSM > 0 {
		avps = append(avps, diam.NewAVP(3313, avp.Mbit|avp.Vbit, VendorID3GPP, // MME-Absent-User-Diagnostic-SM
			datatype.Unsigned32(outcome.MMEAbsentUserDiagnosticSM)))
	}
	if outcome.MSCAbsentUserDiagnosticSM > 0 {
		avps = append(avps, diam.NewAVP(3314, avp.Mbit|avp.Vbit, VendorID3GPP, // MSC-Absent-User-Diagnostic-SM
			datatype.Unsigned32(outcome.MSCAbsentUserDiagnosticSM)))
	}
	if outcome.SGSNAbsentUserDiagnosticSM > 0 {
		avps = append(avps, diam.NewAVP(3315, avp.Mbit|avp.Vbit, VendorID3GPP, // SGSN-Absent-User-Diagnostic-SM
			datatype.Unsigned32(outcome.SGSNAbsentUserDiagnosticSM)))
	}
	if outcome.SMDeliveryCause > 0 {
		avps = append(avps, diam.NewAVP(3321, avp.Mbit|avp.Vbit, VendorID3GPP, // SM-Delivery-Cause
			datatype.Enumerated(outcome.SMDeliveryCause)))
	}

	if len(avps) > 0 {
		smOutcomeAVP := diam.NewAVP(AVPSMDeliveryOutcome, avp.Mbit|avp.Vbit, VendorID3GPP, &diam.GroupedAVP{
			AVP: avps,
		})
		msg.AVP = append(msg.AVP, smOutcomeAVP)
	}
}

func (t *Translator) addServingNode(msg *diam.Message, sn *ServingNode) {
	avps := []*diam.AVP{}

	if len(sn.SGSNNumber) > 0 {
		avps = append(avps, diam.NewAVP(AVPSGSNNumber, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.OctetString(sn.SGSNNumber)))
	}
	if sn.SGSNName != "" {
		avps = append(avps, diam.NewAVP(AVPSGSNName, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.DiameterIdentity(sn.SGSNName)))
	}
	if sn.SGSNRealm != "" {
		avps = append(avps, diam.NewAVP(AVPSGSNRealm, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.DiameterIdentity(sn.SGSNRealm)))
	}
	if sn.MMEName != "" {
		avps = append(avps, diam.NewAVP(AVPMMEName, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.DiameterIdentity(sn.MMEName)))
	}
	if sn.MMERealm != "" {
		avps = append(avps, diam.NewAVP(AVPMMERealm, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.DiameterIdentity(sn.MMERealm)))
	}
	if len(sn.MMENumberForMTSMS) > 0 {
		avps = append(avps, diam.NewAVP(AVPMMENumberForMTSMS, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.OctetString(sn.MMENumberForMTSMS)))
	}
	if len(sn.MSCNumber) > 0 {
		avps = append(avps, diam.NewAVP(AVPMSCNumber, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.OctetString(sn.MSCNumber)))
	}
	if sn.ThreeGPPSMSCAddress != "" {
		avps = append(avps, diam.NewAVP(AVPThreeGPPSMSCAddress, avp.Mbit|avp.Vbit, VendorID3GPP,
			datatype.OctetString(sn.ThreeGPPSMSCAddress)))
	}

	if len(avps) > 0 {
		snAVP := diam.NewAVP(AVPServingNode, avp.Mbit|avp.Vbit, VendorID3GPP, &diam.GroupedAVP{
			AVP: avps,
		})
		msg.AVP = append(msg.AVP, snAVP)
	}
}

// formatAddress formats TBCD-encoded address to string
func (t *Translator) formatAddress(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	// TBCD decoding: each byte has two digits, LSB first
	result := ""
	for _, b := range data {
		d1 := b & 0x0F
		d2 := (b >> 4) & 0x0F
		if d1 <= 9 {
			result += string('0' + d1)
		}
		if d2 <= 9 {
			result += string('0' + d2)
		}
	}
	return result
}

// FormatMSISDN extracts MSISDN from UserIdentifier
func (t *Translator) FormatMSISDN(ui *UserIdentifier) string {
	if ui == nil {
		return ""
	}
	return t.formatAddress(ui.MSISDN)
}

// FormatMSISDNBytes formats MSISDN bytes to string
func (t *Translator) FormatMSISDNBytes(data []byte) string {
	return t.formatAddress(data)
}

// MaskMSISDN masks MSISDN for logging
func (t *Translator) MaskMSISDN(msisdn string) string {
	if len(msisdn) <= 6 {
		return "***"
	}
	return msisdn[:3] + "****" + msisdn[len(msisdn)-3:]
}

// PDUToHex converts SMS PDU to hex string for debugging
func (t *Translator) PDUToHex(pdu []byte) string {
	return hex.EncodeToString(pdu)
}
