// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

// Application, vendor and command identifiers (TS 29.338 V17.2.0).
const (
	AppIDS6c     uint32 = 16777312 // §5.3.2.2: SRR, ALR, RDR — and ALR over SGd
	AppIDSGd     uint32 = 16777313 // §6.3.2.2: OFR, TFR
	VendorID3GPP uint32 = 10415

	CommandOFR uint32 = 8388645 // MO-Forward-Short-Message (SGd)
	CommandTFR uint32 = 8388646 // MT-Forward-Short-Message (SGd)
	CommandSRR uint32 = 8388647 // Send-Routing-Info-for-SM (S6c)
	CommandALR uint32 = 8388648 // Alert-Service-Centre (S6c, also used over SGd)
	CommandRDR uint32 = 8388649 // Report-SM-Delivery-Status (S6c)
)

// AVP codes. Vendor 3GPP unless marked base.
const (
	avpUserName           uint32 = 1   // base
	avpDestinationHost    uint32 = 293 // base
	avpDRMP               uint32 = 301 // base, RFC 7944
	avpVendorID           uint32 = 266 // base
	avpSupportedFeatures  uint32 = 628
	avpFeatureListID      uint32 = 629
	avpFeatureList        uint32 = 630
	avpMSISDN             uint32 = 701
	avpSGSNNumber         uint32 = 1489
	avpMMENumberForMTSMS  uint32 = 1645
	avpLMSI               uint32 = 2400
	avpServingNode        uint32 = 2401
	avpMMEName            uint32 = 2402
	avpMSCNumber          uint32 = 2403
	avpAdditionalServNode uint32 = 2406
	avpMMERealm           uint32 = 2408
	avpSGSNName           uint32 = 2409
	avpSGSNRealm          uint32 = 2410
	avpIPSMGWNumber       uint32 = 3100
	avpIPSMGWName         uint32 = 3101
	avpUserIdentifier     uint32 = 3102
	avpExternalIdentifier uint32 = 3111
	avpIPSMGWRealm        uint32 = 3112

	avpSCAddress                 uint32 = 3300
	avpSMRPUI                    uint32 = 3301
	avpTFRFlags                  uint32 = 3302
	avpSMDeliveryFailureCause    uint32 = 3303
	avpSMEnumDeliveryFailure     uint32 = 3304
	avpSMDiagnosticInfo          uint32 = 3305
	avpSMDeliveryTimer           uint32 = 3306
	avpSMDeliveryStartTime       uint32 = 3307
	avpSMRPMTI                   uint32 = 3308
	avpSMRPSMEA                  uint32 = 3309
	avpSRRFlags                  uint32 = 3310
	avpSMDeliveryNotIntended     uint32 = 3311
	avpMWDStatus                 uint32 = 3312
	avpMMEAbsentUserDiag         uint32 = 3313
	avpMSCAbsentUserDiag         uint32 = 3314
	avpSGSNAbsentUserDiag        uint32 = 3315
	avpSMDeliveryOutcome         uint32 = 3316
	avpMMESMDeliveryOutcome      uint32 = 3317
	avpMSCSMDeliveryOutcome      uint32 = 3318
	avpSGSNSMDeliveryOutcome     uint32 = 3319
	avpIPSMGWSMDeliveryOutcome   uint32 = 3320
	avpSMDeliveryCause           uint32 = 3321
	avpAbsentUserDiagnosticSM    uint32 = 3322
	avpRDRFlags                  uint32 = 3323
	avpSMSMICorrelationID        uint32 = 3324
	avpHSSID                     uint32 = 3325
	avpOriginatingSIPURI         uint32 = 3326
	avpDestinationSIPURI         uint32 = 3327
	avpOFRFlags                  uint32 = 3328
	avpMaxUEAvailabilityTime     uint32 = 3329
	avpMaxRetransmissionTime     uint32 = 3330
	avpRequestedRetransmTime     uint32 = 3331
	avpSMSGMSCAddress            uint32 = 3332
	avpSMSGMSCAlertEvent         uint32 = 3333
	avpSMSF3GPPAbsentUserDiag    uint32 = 3334
	avpSMSFNon3GPPAbsentUserDiag uint32 = 3335
	avpSMSF3GPPSMDeliveryOutcome uint32 = 3336
	avpSMSFNon3GPPSMDelOutcome   uint32 = 3337
	avpSMSF3GPPNumber            uint32 = 3338
	avpSMSFNon3GPPNumber         uint32 = 3339
	avpSMSF3GPPName              uint32 = 3340
	avpSMSFNon3GPPName           uint32 = 3341
	avpSMSF3GPPRealm             uint32 = 3342
	avpSMSFNon3GPPRealm          uint32 = 3343
	avpSMSF3GPPAddress           uint32 = 3344
	avpSMSFNon3GPPAddress        uint32 = 3345
)

// avpDef is one vendor-3GPP AVP of S6c or SGd: its name and type for the
// dictionary, and whether its M bit is set under each application. Every
// 3GPP AVP carries the V bit.
//
// Sources: TS 29.338 Tables 5.3.3.1/1 (S6c), 6.3.3.1/1 (SGd) and the re-used
// AVP tables 5.3.3.1/2 and 6.3.3.1/2, whose M-bit column overrides the
// defining specification; otherwise TS 29.173 V17.0.0 Table 6.4.1/1 (2400-
// 2410), TS 29.272 Table 7.3.1/1 (1489, 1645), TS 29.336 Table 6.4.1/1 (31xx),
// TS 29.229 Table 6.3.1 (628-630) and, for MSISDN, TS 29.329 (M, V).
type avpDef struct {
	name  string
	code  uint32
	typ   string // go-diameter dictionary type
	mS6c  bool   // M bit under S6c (16777312)
	mSGd  bool   // M bit under SGd (16777313)
	inS6c bool   // used by an S6c command
	inSGd bool   // used by an SGd command
}

// both marks an AVP used under both applications with the same M bit.
func both(name string, code uint32, typ string, m bool) avpDef {
	return avpDef{name: name, code: code, typ: typ, mS6c: m, mSGd: m, inS6c: true, inSGd: true}
}

func s6c(name string, code uint32, typ string, m bool) avpDef {
	return avpDef{name: name, code: code, typ: typ, mS6c: m, inS6c: true}
}

func sgd(name string, code uint32, typ string, m bool) avpDef {
	return avpDef{name: name, code: code, typ: typ, mSGd: m, inSGd: true}
}

var avpDefs = []avpDef{
	// Re-used. MME-Number-for-MT-SMS: "Must" in S6c, blank in SGd (TS 29.272:
	// V, must-not M). SGSN-Number and External-Identifier: "Must not" in SGd.
	both("MSISDN", avpMSISDN, "OctetString", true),
	{name: "SGSN-Number", code: avpSGSNNumber, typ: "OctetString", mS6c: true, inS6c: true, inSGd: true},
	{name: "MME-Number-for-MT-SMS", code: avpMMENumberForMTSMS, typ: "OctetString", mS6c: true, inS6c: true, inSGd: true},
	both("LMSI", avpLMSI, "OctetString", true),
	s6c("Serving-Node", avpServingNode, "Grouped", true),
	s6c("MME-Name", avpMMEName, "DiameterIdentity", true),
	s6c("MSC-Number", avpMSCNumber, "OctetString", true),
	s6c("Additional-Serving-Node", avpAdditionalServNode, "Grouped", true),
	s6c("MME-Realm", avpMMERealm, "DiameterIdentity", true), // "Must" in S6c; TS 29.173: V
	s6c("SGSN-Name", avpSGSNName, "DiameterIdentity", false),
	s6c("SGSN-Realm", avpSGSNRealm, "DiameterIdentity", false),
	s6c("IP-SM-GW-Number", avpIPSMGWNumber, "OctetString", true),
	s6c("IP-SM-GW-Name", avpIPSMGWName, "DiameterIdentity", true),
	s6c("IP-SM-GW-Realm", avpIPSMGWRealm, "DiameterIdentity", true),
	both("User-Identifier", avpUserIdentifier, "Grouped", true),
	{name: "External-Identifier", code: avpExternalIdentifier, typ: "UTF8String", mS6c: true, inS6c: true, inSGd: true},
	both("Supported-Features", avpSupportedFeatures, "Grouped", false),
	both("Feature-List-ID", avpFeatureListID, "Unsigned32", false),
	both("Feature-List", avpFeatureList, "Unsigned32", false),

	// SGd/Gdd specific (Table 6.3.3.1/1); S6c re-uses SC-Address,
	// SM-Delivery-Failure-Cause, SMSMI-Correlation-ID and its children.
	both("SC-Address", avpSCAddress, "OctetString", true),
	sgd("SM-RP-UI", avpSMRPUI, "OctetString", true),
	sgd("TFR-Flags", avpTFRFlags, "Unsigned32", true),
	both("SM-Delivery-Failure-Cause", avpSMDeliveryFailureCause, "Grouped", true),
	both("SM-Enumerated-Delivery-Failure-Cause", avpSMEnumDeliveryFailure, "Enumerated", true),
	both("SM-Diagnostic-Info", avpSMDiagnosticInfo, "OctetString", true),
	sgd("SM-Delivery-Timer", avpSMDeliveryTimer, "Unsigned32", true),
	sgd("SM-Delivery-Start-Time", avpSMDeliveryStartTime, "Time", true),
	// Table 6.3.3.1/1 gives 3324; the clause's own AVP format says 3308,
	// which is SM-RP-MTI.
	both("SMSMI-Correlation-ID", avpSMSMICorrelationID, "Grouped", false),
	both("HSS-ID", avpHSSID, "UTF8String", false),
	both("Originating-SIP-URI", avpOriginatingSIPURI, "UTF8String", false),
	both("Destination-SIP-URI", avpDestinationSIPURI, "UTF8String", false),
	sgd("OFR-Flags", avpOFRFlags, "Unsigned32", false),
	sgd("Maximum-Retransmission-Time", avpMaxRetransmissionTime, "Time", false),
	sgd("Requested-Retransmission-Time", avpRequestedRetransmTime, "Time", false),
	sgd("SMS-GMSC-Address", avpSMSGMSCAddress, "OctetString", false),

	// S6c specific (Table 5.3.3.1/1); SGd re-uses Absent-User-Diagnostic-SM
	// and, in OFR, SM-Delivery-Outcome.
	s6c("SM-RP-MTI", avpSMRPMTI, "Enumerated", true),
	s6c("SM-RP-SMEA", avpSMRPSMEA, "OctetString", true),
	s6c("SRR-Flags", avpSRRFlags, "Unsigned32", true),
	s6c("SM-Delivery-Not-Intended", avpSMDeliveryNotIntended, "Enumerated", true),
	s6c("MWD-Status", avpMWDStatus, "Unsigned32", true),
	s6c("MME-Absent-User-Diagnostic-SM", avpMMEAbsentUserDiag, "Unsigned32", true),
	s6c("MSC-Absent-User-Diagnostic-SM", avpMSCAbsentUserDiag, "Unsigned32", true),
	s6c("SGSN-Absent-User-Diagnostic-SM", avpSGSNAbsentUserDiag, "Unsigned32", true),
	both("SM-Delivery-Outcome", avpSMDeliveryOutcome, "Grouped", true),
	both("MME-SM-Delivery-Outcome", avpMMESMDeliveryOutcome, "Grouped", true),
	both("MSC-SM-Delivery-Outcome", avpMSCSMDeliveryOutcome, "Grouped", true),
	both("SGSN-SM-Delivery-Outcome", avpSGSNSMDeliveryOutcome, "Grouped", true),
	both("IP-SM-GW-SM-Delivery-Outcome", avpIPSMGWSMDeliveryOutcome, "Grouped", true),
	both("SM-Delivery-Cause", avpSMDeliveryCause, "Enumerated", true),
	both("Absent-User-Diagnostic-SM", avpAbsentUserDiagnosticSM, "Unsigned32", true),
	s6c("RDR-Flags", avpRDRFlags, "Unsigned32", false),
	s6c("Maximum-UE-Availability-Time", avpMaxUEAvailabilityTime, "Time", false),
	s6c("SMS-GMSC-Alert-Event", avpSMSGMSCAlertEvent, "Unsigned32", false),
	s6c("SMSF-3GPP-Absent-User-Diagnostic-SM", avpSMSF3GPPAbsentUserDiag, "Unsigned32", false),
	s6c("SMSF-Non-3GPP-Absent-User-Diagnostic-SM", avpSMSFNon3GPPAbsentUserDiag, "Unsigned32", false),
	both("SMSF-3GPP-SM-Delivery-Outcome", avpSMSF3GPPSMDeliveryOutcome, "Grouped", false),
	both("SMSF-Non-3GPP-SM-Delivery-Outcome", avpSMSFNon3GPPSMDelOutcome, "Grouped", false),
	s6c("SMSF-3GPP-Number", avpSMSF3GPPNumber, "OctetString", false),
	s6c("SMSF-Non-3GPP-Number", avpSMSFNon3GPPNumber, "OctetString", false),
	s6c("SMSF-3GPP-Name", avpSMSF3GPPName, "DiameterIdentity", false),
	s6c("SMSF-Non-3GPP-Name", avpSMSFNon3GPPName, "DiameterIdentity", false),
	s6c("SMSF-3GPP-Realm", avpSMSF3GPPRealm, "DiameterIdentity", false),
	s6c("SMSF-Non-3GPP-Realm", avpSMSFNon3GPPRealm, "DiameterIdentity", false),
	s6c("SMSF-3GPP-Address", avpSMSF3GPPAddress, "Grouped", false),
	s6c("SMSF-Non-3GPP-Address", avpSMSFNon3GPPAddress, "Grouped", false),
}

// groupRules lists the children of each Grouped AVP, for the dictionary.
var groupRules = map[uint32][]string{
	avpServingNode: {"SGSN-Name", "SGSN-Realm", "SGSN-Number", "MME-Name", "MME-Realm",
		"MME-Number-for-MT-SMS", "MSC-Number", "IP-SM-GW-Number", "IP-SM-GW-Name", "IP-SM-GW-Realm"},
	avpAdditionalServNode: {"SGSN-Name", "SGSN-Realm", "SGSN-Number", "MME-Name", "MME-Realm",
		"MME-Number-for-MT-SMS", "MSC-Number"},
	avpUserIdentifier:            {"User-Name", "MSISDN", "External-Identifier", "LMSI"},
	avpSupportedFeatures:         {"Vendor-Id", "Feature-List-ID", "Feature-List"},
	avpSMDeliveryFailureCause:    {"SM-Enumerated-Delivery-Failure-Cause", "SM-Diagnostic-Info"},
	avpSMSMICorrelationID:        {"HSS-ID", "Originating-SIP-URI", "Destination-SIP-URI"},
	avpSMDeliveryOutcome:         {"MME-SM-Delivery-Outcome", "MSC-SM-Delivery-Outcome", "SGSN-SM-Delivery-Outcome", "IP-SM-GW-SM-Delivery-Outcome", "SMSF-3GPP-SM-Delivery-Outcome", "SMSF-Non-3GPP-SM-Delivery-Outcome"},
	avpMMESMDeliveryOutcome:      {"SM-Delivery-Cause", "Absent-User-Diagnostic-SM"},
	avpMSCSMDeliveryOutcome:      {"SM-Delivery-Cause", "Absent-User-Diagnostic-SM"},
	avpSGSNSMDeliveryOutcome:     {"SM-Delivery-Cause", "Absent-User-Diagnostic-SM"},
	avpIPSMGWSMDeliveryOutcome:   {"SM-Delivery-Cause", "Absent-User-Diagnostic-SM"},
	avpSMSF3GPPSMDeliveryOutcome: {"SM-Delivery-Cause", "Absent-User-Diagnostic-SM"},
	avpSMSFNon3GPPSMDelOutcome:   {"SM-Delivery-Cause", "Absent-User-Diagnostic-SM"},
	avpSMSF3GPPAddress:           {"SMSF-3GPP-Number", "SMSF-3GPP-Name", "SMSF-3GPP-Realm"},
	avpSMSFNon3GPPAddress:        {"SMSF-Non-3GPP-Number", "SMSF-Non-3GPP-Name", "SMSF-Non-3GPP-Realm"},
}

var defByCode = func() map[uint32]avpDef {
	m := make(map[uint32]avpDef, len(avpDefs))
	for _, d := range avpDefs {
		m[d.code] = d
	}
	return m
}()
