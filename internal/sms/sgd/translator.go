// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// Each command is translated in four places: a request from the network to
// its event, the application's reply to the answer; and, for the requests
// applications send, the application's request to the Diameter request and
// the peer's answer to the reply. Every field follows the command's Command
// Code Format in TS 29.338 §5.3.2 (S6c) or §6.3.2 (SGd); the Vendor-Specific-
// Application-Id is neither read nor sent (§5.3.2.2: "shall not be included").

var (
	wS6c = writer{app: AppIDS6c}
	wSGd = writer{app: AppIDSGd}
)

// ─── OFR / OFA (SGd, network → application) ──────────────────────────────

func decodeOFR(m *diam.Message, traceID string) *OFREvent {
	l := avpList(m.AVP)
	return &OFREvent{
		Header:             header(m, traceID),
		SCAddress:          tbcdOf(l.get(avpSCAddress)),
		OFRFlags:           u32Of(l.get(avpOFRFlags)),
		SupportedFeatures:  readSupportedFeatures(l),
		UserIdentifier:     readUserIdentifier(l.get(avpUserIdentifier)),
		SMRPUI:             asBytes(l.get(avpSMRPUI)),
		SMSMICorrelationID: readSMSMICorrelationID(l.get(avpSMSMICorrelationID)),
		SMDeliveryOutcome:  readSMDeliveryOutcome(l.get(avpSMDeliveryOutcome)),
	}
}

func encodeOFA(req *diam.Message, a *OFAAnswer) *diam.Message {
	ans := answerFor(req, a.Result)
	echoDRMP(ans, req)
	add(ans, wSGd.supportedFeatures(a.SupportedFeatures)...)
	add(ans,
		wSGd.smDeliveryFailureCause(a.SMDeliveryFailureCause),
		wSGd.octets(avpSMRPUI, a.SMRPUI),
		wSGd.str(avpExternalIdentifier, a.ExternalIdentifier))
	return ans
}

// ─── TFR / TFA (SGd, both directions) ────────────────────────────────────

func decodeTFR(m *diam.Message, traceID string) *TFREvent {
	l := avpList(m.AVP)
	return &TFREvent{
		Header:                    header(m, traceID),
		UserName:                  asString(l.base(avpUserName)),
		SupportedFeatures:         readSupportedFeatures(l),
		SMSMICorrelationID:        readSMSMICorrelationID(l.get(avpSMSMICorrelationID)),
		SCAddress:                 tbcdOf(l.get(avpSCAddress)),
		SMRPUI:                    asBytes(l.get(avpSMRPUI)),
		MMENumberForMTSMS:         tbcdOf(l.get(avpMMENumberForMTSMS)),
		SGSNNumber:                tbcdOf(l.get(avpSGSNNumber)),
		TFRFlags:                  u32Of(l.get(avpTFRFlags)),
		SMDeliveryTimer:           u32Of(l.get(avpSMDeliveryTimer)),
		SMDeliveryStartTime:       unixOf(l.get(avpSMDeliveryStartTime)),
		MaximumRetransmissionTime: unixOf(l.get(avpMaxRetransmissionTime)),
		SMSGMSCAddress:            tbcdOf(l.get(avpSMSGMSCAddress)),
	}
}

func encodeTFA(req *diam.Message, a *TFAAnswer) *diam.Message {
	ans := answerFor(req, a.Result)
	echoDRMP(ans, req)
	add(ans, wSGd.supportedFeatures(a.SupportedFeatures)...)
	add(ans,
		wSGd.u32p(avpAbsentUserDiagnosticSM, a.AbsentUserDiagnosticSM),
		wSGd.smDeliveryFailureCause(a.SMDeliveryFailureCause),
		wSGd.octets(avpSMRPUI, a.SMRPUI),
		wSGd.unix(avpRequestedRetransmTime, a.RequestedRetransmissionTime),
		wSGd.userIdentifier(a.UserIdentifier))
	return ans
}

func buildTFR(r *TFRRequest, sid, originHost, originRealm, destHost, destRealm string) *diam.Message {
	m := newRequest(CommandTFR, AppIDSGd, sid, originHost, originRealm, destHost, destRealm)
	add(m, userNameAVP(r.userName()))
	add(m, wSGd.supportedFeatures(r.SupportedFeatures)...)
	add(m,
		wSGd.smsmiCorrelationID(r.SMSMICorrelationID),
		wSGd.tbcd(avpSCAddress, r.SCAddress),
		wSGd.octets(avpSMRPUI, r.SMRPUI),
		wSGd.tbcd(avpMMENumberForMTSMS, r.MMENumberForMTSMS),
		wSGd.tbcd(avpSGSNNumber, r.SGSNNumber),
		wSGd.u32(avpTFRFlags, r.TFRFlags),
		wSGd.u32(avpSMDeliveryTimer, r.SMDeliveryTimer),
		wSGd.unix(avpSMDeliveryStartTime, r.SMDeliveryStartTime),
		wSGd.unix(avpMaxRetransmissionTime, r.MaximumRetransmissionTime),
		wSGd.tbcd(avpSMSGMSCAddress, r.SMSGMSCAddress))
	return m
}

func decodeTFA(m *diam.Message) *TFAAnswer {
	l := avpList(m.AVP)
	return &TFAAnswer{
		Result:                      readResult(m),
		SupportedFeatures:           readSupportedFeatures(l),
		AbsentUserDiagnosticSM:      u32Ptr(l.get(avpAbsentUserDiagnosticSM)),
		SMDeliveryFailureCause:      readSMDeliveryFailureCause(l.get(avpSMDeliveryFailureCause)),
		SMRPUI:                      asBytes(l.get(avpSMRPUI)),
		RequestedRetransmissionTime: unixOf(l.get(avpRequestedRetransmTime)),
		UserIdentifier:              readUserIdentifier(l.get(avpUserIdentifier)),
	}
}

// ─── SRR / SRA (S6c, both directions) ────────────────────────────────────

func decodeSRR(m *diam.Message, traceID string) *SRREvent {
	l := avpList(m.AVP)
	return &SRREvent{
		Header:                header(m, traceID),
		MSISDN:                tbcdOf(l.get(avpMSISDN)),
		UserName:              asString(l.base(avpUserName)),
		SMSMICorrelationID:    readSMSMICorrelationID(l.get(avpSMSMICorrelationID)),
		SupportedFeatures:     readSupportedFeatures(l),
		SCAddress:             tbcdOf(l.get(avpSCAddress)),
		SMRPMTI:               u32Ptr(l.get(avpSMRPMTI)),
		SMRPSMEA:              asBytes(l.get(avpSMRPSMEA)),
		SRRFlags:              u32Of(l.get(avpSRRFlags)),
		SMDeliveryNotIntended: u32Ptr(l.get(avpSMDeliveryNotIntended)),
	}
}

func encodeSRA(req *diam.Message, a *SRAAnswer) *diam.Message {
	ans := answerFor(req, a.Result)
	echoDRMP(ans, req)
	add(ans, userNameAVP(a.UserName))
	add(ans, wS6c.supportedFeatures(a.SupportedFeatures)...)
	add(ans,
		wS6c.servingNode(avpServingNode, a.ServingNode),
		wS6c.servingNode(avpAdditionalServNode, a.AdditionalServingNode),
		wS6c.smsfAddress(avpSMSF3GPPAddress, avpSMSF3GPPNumber, avpSMSF3GPPName, avpSMSF3GPPRealm, a.SMSF3GPPAddress),
		wS6c.smsfAddress(avpSMSFNon3GPPAddress, avpSMSFNon3GPPNumber, avpSMSFNon3GPPName, avpSMSFNon3GPPRealm, a.SMSFNon3GPPAddress),
		wS6c.octets(avpLMSI, a.LMSI),
		wS6c.userIdentifier(a.UserIdentifier),
		wS6c.u32(avpMWDStatus, a.MWDStatus),
		wS6c.u32p(avpMMEAbsentUserDiag, a.MMEAbsentUserDiagnosticSM),
		wS6c.u32p(avpMSCAbsentUserDiag, a.MSCAbsentUserDiagnosticSM),
		wS6c.u32p(avpSGSNAbsentUserDiag, a.SGSNAbsentUserDiagnosticSM),
		wS6c.u32p(avpSMSF3GPPAbsentUserDiag, a.SMSF3GPPAbsentUserDiagnosticSM),
		wS6c.u32p(avpSMSFNon3GPPAbsentUserDiag, a.SMSFNon3GPPAbsentUserDiagnosticSM))
	return ans
}

func buildSRR(r *SRRRequest, sid, originHost, originRealm, destHost, destRealm string) *diam.Message {
	m := newRequest(CommandSRR, AppIDS6c, sid, originHost, originRealm, destHost, destRealm)
	add(m, wS6c.tbcd(avpMSISDN, r.MSISDN), userNameAVP(r.UserName), wS6c.smsmiCorrelationID(r.SMSMICorrelationID))
	add(m, wS6c.supportedFeatures(r.SupportedFeatures)...)
	add(m,
		wS6c.tbcd(avpSCAddress, r.SCAddress),
		wS6c.enum(avpSMRPMTI, r.SMRPMTI),
		wS6c.octets(avpSMRPSMEA, r.SMRPSMEA),
		wS6c.u32(avpSRRFlags, r.flags()),
		wS6c.enum(avpSMDeliveryNotIntended, r.SMDeliveryNotIntended))
	return m
}

func decodeSRA(m *diam.Message) *SRAAnswer {
	l := avpList(m.AVP)
	a := &SRAAnswer{
		Result:                            readResult(m),
		UserName:                          asString(l.base(avpUserName)),
		SupportedFeatures:                 readSupportedFeatures(l),
		ServingNode:                       readServingNode(l.get(avpServingNode)),
		AdditionalServingNode:             readServingNode(l.get(avpAdditionalServNode)),
		SMSF3GPPAddress:                   readSMSFAddress(l.get(avpSMSF3GPPAddress), avpSMSF3GPPNumber, avpSMSF3GPPName, avpSMSF3GPPRealm),
		SMSFNon3GPPAddress:                readSMSFAddress(l.get(avpSMSFNon3GPPAddress), avpSMSFNon3GPPNumber, avpSMSFNon3GPPName, avpSMSFNon3GPPRealm),
		LMSI:                              asBytes(l.get(avpLMSI)),
		UserIdentifier:                    readUserIdentifier(l.get(avpUserIdentifier)),
		MWDStatus:                         u32Of(l.get(avpMWDStatus)),
		MMEAbsentUserDiagnosticSM:         u32Ptr(l.get(avpMMEAbsentUserDiag)),
		MSCAbsentUserDiagnosticSM:         u32Ptr(l.get(avpMSCAbsentUserDiag)),
		SGSNAbsentUserDiagnosticSM:        u32Ptr(l.get(avpSGSNAbsentUserDiag)),
		SMSF3GPPAbsentUserDiagnosticSM:    u32Ptr(l.get(avpSMSF3GPPAbsentUserDiag)),
		SMSFNon3GPPAbsentUserDiagnosticSM: u32Ptr(l.get(avpSMSFNon3GPPAbsentUserDiag)),
	}
	if n := a.ServingNode; n != nil {
		a.MMEName, a.MMERealm = n.MMEName, n.MMERealm
		a.SGSNName, a.SGSNRealm, a.SGSNNumber = n.SGSNName, n.SGSNRealm, n.SGSNNumber
	}
	return a
}

// ─── ALR / ALA (S6c application, both directions) ────────────────────────

func decodeALR(m *diam.Message, traceID string) *ALREvent {
	l := avpList(m.AVP)
	return &ALREvent{
		Header:                    header(m, traceID),
		SCAddress:                 tbcdOf(l.get(avpSCAddress)),
		UserIdentifier:            readUserIdentifier(l.get(avpUserIdentifier)),
		SMSMICorrelationID:        readSMSMICorrelationID(l.get(avpSMSMICorrelationID)),
		MaximumUEAvailabilityTime: unixOf(l.get(avpMaxUEAvailabilityTime)),
		SMSGMSCAlertEvent:         u32Of(l.get(avpSMSGMSCAlertEvent)),
		ServingNode:               readServingNode(l.get(avpServingNode)),
		SupportedFeatures:         readSupportedFeatures(l),
	}
}

func encodeALA(req *diam.Message, a *ALAAnswer) *diam.Message {
	ans := answerFor(req, a.Result)
	echoDRMP(ans, req)
	add(ans, wS6c.supportedFeatures(a.SupportedFeatures)...)
	return ans
}

func buildALR(r *ALRRequest, sid, originHost, originRealm, destHost, destRealm string) *diam.Message {
	m := newRequest(CommandALR, AppIDS6c, sid, originHost, originRealm, destHost, destRealm)
	add(m,
		wS6c.tbcd(avpSCAddress, r.SCAddress),
		wS6c.userIdentifier(r.userIdentifier()),
		wS6c.smsmiCorrelationID(r.SMSMICorrelationID),
		wS6c.unix(avpMaxUEAvailabilityTime, r.MaximumUEAvailabilityTime),
		wS6c.u32(avpSMSGMSCAlertEvent, r.SMSGMSCAlertEvent),
		wS6c.servingNode(avpServingNode, r.ServingNode))
	add(m, wS6c.supportedFeatures(r.SupportedFeatures)...)
	return m
}

func decodeALA(m *diam.Message) *ALAAnswer {
	return &ALAAnswer{Result: readResult(m), SupportedFeatures: readSupportedFeatures(m.AVP)}
}

// ─── RDR / RDA (S6c, both directions) ────────────────────────────────────

func decodeRDR(m *diam.Message, traceID string) *RDREvent {
	l := avpList(m.AVP)
	return &RDREvent{
		Header:             header(m, traceID),
		SupportedFeatures:  readSupportedFeatures(l),
		UserIdentifier:     readUserIdentifier(l.get(avpUserIdentifier)),
		SMSMICorrelationID: readSMSMICorrelationID(l.get(avpSMSMICorrelationID)),
		SCAddress:          tbcdOf(l.get(avpSCAddress)),
		SMDeliveryOutcome:  readSMDeliveryOutcome(l.get(avpSMDeliveryOutcome)),
		RDRFlags:           u32Of(l.get(avpRDRFlags)),
	}
}

func encodeRDA(req *diam.Message, a *RDAAnswer) *diam.Message {
	ans := answerFor(req, a.Result)
	echoDRMP(ans, req)
	add(ans, wS6c.supportedFeatures(a.SupportedFeatures)...)
	add(ans, wS6c.userIdentifier(a.UserIdentifier))
	return ans
}

func buildRDR(r *RDRRequest, sid, originHost, originRealm, destHost, destRealm string) *diam.Message {
	m := newRequest(CommandRDR, AppIDS6c, sid, originHost, originRealm, destHost, destRealm)
	add(m, wS6c.supportedFeatures(r.SupportedFeatures)...)
	add(m,
		wS6c.userIdentifier(r.userIdentifier()),
		wS6c.smsmiCorrelationID(r.SMSMICorrelationID),
		wS6c.tbcd(avpSCAddress, r.SCAddress),
		wS6c.smDeliveryOutcome(r.SMDeliveryOutcome),
		wS6c.u32(avpRDRFlags, r.RDRFlags))
	return m
}

func decodeRDA(m *diam.Message) *RDAAnswer {
	l := avpList(m.AVP)
	return &RDAAnswer{
		Result:            readResult(m),
		SupportedFeatures: readSupportedFeatures(l),
		UserIdentifier:    readUserIdentifier(l.get(avpUserIdentifier)),
	}
}

// newRequest opens a request: R and P flags ("REQ, PXY" in every command's
// format), Session-Id, Auth-Session-State NO_STATE_MAINTAINED, the origin
// and the destination.
func newRequest(code, app uint32, sid, originHost, originRealm, destHost, destRealm string) *diam.Message {
	m := diam.NewMessage(code, diam.RequestFlag|diam.ProxiableFlag, app, 0, 0, dict.Default)
	add(m,
		diam.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sid)),
		diam.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1)),
		diam.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(originHost)),
		diam.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(originRealm)))
	if destHost != "" {
		add(m, diam.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost)))
	}
	add(m, diam.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm)))
	return m
}
