// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
)

// The NATS envelopes of S6c and SGd (TS 29.338). Conventions:
//   - an E.164 number (MSISDN, SC-Address, SMS-GMSC-Address and the node
//     numbers) is a string of digits; the gateway encodes it as the TBCD
//     string the specification requires and decodes it back;
//   - other octet strings (SM-RP-UI, SM-RP-SMEA, LMSI, SM-Diagnostic-Info)
//     are bytes, base64 in JSON;
//   - a Time AVP is Unix seconds;
//   - an Enumerated AVP whose value 0 is meaningful is a pointer, absent
//     when the AVP is.

// UserIdentifier (TS 29.336 §6.4.2) names the subscriber; it holds at least
// one of its fields.
type UserIdentifier struct {
	UserName           string `json:"user_name,omitempty"` // IMSI
	MSISDN             string `json:"msisdn,omitempty"`
	ExternalIdentifier string `json:"external_identifier,omitempty"`
	LMSI               []byte `json:"lmsi,omitempty"`
}

// ServingNode is a Serving-Node or Additional-Serving-Node (TS 29.338
// §5.3.3.6, §5.3.3.7; the IP-SM-GW fields exist in Serving-Node only).
type ServingNode struct {
	SGSNName          string `json:"sgsn_name,omitempty"`
	SGSNRealm         string `json:"sgsn_realm,omitempty"`
	SGSNNumber        string `json:"sgsn_number,omitempty"`
	MMEName           string `json:"mme_name,omitempty"`
	MMERealm          string `json:"mme_realm,omitempty"`
	MMENumberForMTSMS string `json:"mme_number_for_mt_sms,omitempty"`
	MSCNumber         string `json:"msc_number,omitempty"`
	IPSMGWNumber      string `json:"ip_sm_gw_number,omitempty"`
	IPSMGWName        string `json:"ip_sm_gw_name,omitempty"`
	IPSMGWRealm       string `json:"ip_sm_gw_realm,omitempty"`
}

// SMSFAddress is an SMSF-3GPP-Address or SMSF-Non-3GPP-Address (§5.3.3.35,
// §5.3.3.36).
type SMSFAddress struct {
	Number string `json:"number,omitempty"`
	Name   string `json:"name,omitempty"`
	Realm  string `json:"realm,omitempty"`
}

// SupportedFeatures is one Supported-Features AVP (TS 29.229 §6.3.29).
type SupportedFeatures struct {
	VendorID      uint32 `json:"vendor_id"`
	FeatureListID uint32 `json:"feature_list_id"`
	FeatureList   uint32 `json:"feature_list"`
}

// SMSMICorrelationID (§6.3.3.13) identifies MSISDN-less SMS in IMS.
type SMSMICorrelationID struct {
	HSSID             string `json:"hss_id,omitempty"`
	OriginatingSIPURI string `json:"originating_sip_uri,omitempty"`
	DestinationSIPURI string `json:"destination_sip_uri,omitempty"`
}

// DeliveryOutcome is one node's outcome in an SM-Delivery-Outcome
// (§5.3.3.15-18, 27, 28). Cause: UE_MEMORY_CAPACITY_EXCEEDED (0),
// ABSENT_USER (1), SUCCESSFUL_TRANSFER (2).
type DeliveryOutcome struct {
	Cause                  *uint32 `json:"sm_delivery_cause,omitempty"`
	AbsentUserDiagnosticSM *uint32 `json:"absent_user_diagnostic_sm,omitempty"`
}

// SMDeliveryOutcome (§5.3.3.14) holds the outcome per serving node.
type SMDeliveryOutcome struct {
	MME         *DeliveryOutcome `json:"mme,omitempty"`
	MSC         *DeliveryOutcome `json:"msc,omitempty"`
	SGSN        *DeliveryOutcome `json:"sgsn,omitempty"`
	IPSMGW      *DeliveryOutcome `json:"ip_sm_gw,omitempty"`
	SMSF3GPP    *DeliveryOutcome `json:"smsf_3gpp,omitempty"`
	SMSFNon3GPP *DeliveryOutcome `json:"smsf_non_3gpp,omitempty"`
}

// SMDeliveryFailureCause (§6.3.3.5). Cause: MEMORY_CAPACITY_EXCEEDED (0) …
// USER_NOT_SC-USER (6), §6.3.3.6.
type SMDeliveryFailureCause struct {
	Cause          uint32 `json:"cause"`
	DiagnosticInfo []byte `json:"diagnostic_info,omitempty"`
}

// ExperimentalResult carries a TS 29.338 §7 result code (5550-5558, 5001),
// sent without Result-Code.
type ExperimentalResult struct {
	VendorID               uint32 `json:"vendor_id"`
	ExperimentalResultCode uint32 `json:"experimental_result_code"`
}

// Result is how every answer reports its outcome. From an application, a
// set ExperimentalResult wins over ResultCode. To an application,
// ResultCode holds the Experimental-Result-Code when the peer sent one, so a
// reader of result_code alone sees the 3GPP code too.
type Result struct {
	ResultCode         uint32              `json:"result_code"`
	ExperimentalResult *ExperimentalResult `json:"experimental_result,omitempty"`
	ErrorMessage       string              `json:"error_message,omitempty"` // set by the gateway only
}

// ─── Requests from the network, published on diameter.sgd.<cmd> ─────────

// OFREvent is an MO-Forward-Short-Message-Request (§6.3.2.3).
type OFREvent struct {
	Header             charging.DiameterHeader `json:"header"`
	SCAddress          string                  `json:"sc_address"`
	OFRFlags           uint32                  `json:"ofr_flags,omitempty"` // bit 0: S6a/S6d-Indicator
	SupportedFeatures  []SupportedFeatures     `json:"supported_features,omitempty"`
	UserIdentifier     *UserIdentifier         `json:"user_identifier"`
	SMRPUI             []byte                  `json:"sm_rp_ui"`
	SMSMICorrelationID *SMSMICorrelationID     `json:"smsmi_correlation_id,omitempty"`
	SMDeliveryOutcome  *SMDeliveryOutcome      `json:"sm_delivery_outcome,omitempty"`
}

// OFAAnswer is the application's reply to an OFREvent (§6.3.2.4).
type OFAAnswer struct {
	Result
	SupportedFeatures      []SupportedFeatures     `json:"supported_features,omitempty"`
	SMDeliveryFailureCause *SMDeliveryFailureCause `json:"sm_delivery_failure_cause,omitempty"`
	SMRPUI                 []byte                  `json:"sm_rp_ui,omitempty"`
	ExternalIdentifier     string                  `json:"external_identifier,omitempty"`
}

// TFREvent is an MT-Forward-Short-Message-Request (§6.3.2.5).
type TFREvent struct {
	Header                    charging.DiameterHeader `json:"header"`
	UserName                  string                  `json:"user_name"` // IMSI
	SupportedFeatures         []SupportedFeatures     `json:"supported_features,omitempty"`
	SMSMICorrelationID        *SMSMICorrelationID     `json:"smsmi_correlation_id,omitempty"`
	SCAddress                 string                  `json:"sc_address"`
	SMRPUI                    []byte                  `json:"sm_rp_ui"`
	MMENumberForMTSMS         string                  `json:"mme_number_for_mt_sms,omitempty"`
	SGSNNumber                string                  `json:"sgsn_number,omitempty"`
	TFRFlags                  uint32                  `json:"tfr_flags,omitempty"`         // bit 0: More-Messages-To-Send
	SMDeliveryTimer           uint32                  `json:"sm_delivery_timer,omitempty"` // seconds
	SMDeliveryStartTime       int64                   `json:"sm_delivery_start_time,omitempty"`
	MaximumRetransmissionTime int64                   `json:"maximum_retransmission_time,omitempty"`
	SMSGMSCAddress            string                  `json:"sms_gmsc_address,omitempty"`
}

// TFAAnswer is the application's reply to a TFREvent (§6.3.2.6).
type TFAAnswer struct {
	Result
	SupportedFeatures           []SupportedFeatures     `json:"supported_features,omitempty"`
	AbsentUserDiagnosticSM      *uint32                 `json:"absent_user_diagnostic_sm,omitempty"`
	SMDeliveryFailureCause      *SMDeliveryFailureCause `json:"sm_delivery_failure_cause,omitempty"`
	SMRPUI                      []byte                  `json:"sm_rp_ui,omitempty"`
	RequestedRetransmissionTime int64                   `json:"requested_retransmission_time,omitempty"`
	UserIdentifier              *UserIdentifier         `json:"user_identifier,omitempty"`
}

// SRREvent is a Send-Routing-Info-for-SM-Request (§5.3.2.3).
type SRREvent struct {
	Header                charging.DiameterHeader `json:"header"`
	MSISDN                string                  `json:"msisdn,omitempty"`
	UserName              string                  `json:"user_name,omitempty"` // IMSI
	SMSMICorrelationID    *SMSMICorrelationID     `json:"smsmi_correlation_id,omitempty"`
	SupportedFeatures     []SupportedFeatures     `json:"supported_features,omitempty"`
	SCAddress             string                  `json:"sc_address,omitempty"`
	SMRPMTI               *uint32                 `json:"sm_rp_mti,omitempty"` // SM_DELIVER (0), SM_STATUS_REPORT (1)
	SMRPSMEA              []byte                  `json:"sm_rp_smea,omitempty"`
	SRRFlags              uint32                  `json:"srr_flags,omitempty"`                // bits: 0 GPRS-Indicator, 1 SM-RP-PRI, 2 Single-Attempt-Delivery
	SMDeliveryNotIntended *uint32                 `json:"sm_delivery_not_intended,omitempty"` // ONLY_IMSI_REQUESTED (0), ONLY_MCC_MNC_REQUESTED (1)
}

// SRAAnswer is a Send-Routing-Info-for-SM-Answer (§5.3.2.4): the reply of an
// application serving SRREvents, and the reply the gateway gives to
// diameter.sgd.srr.request.
type SRAAnswer struct {
	Result
	UserName              string              `json:"user_name,omitempty"` // IMSI
	SupportedFeatures     []SupportedFeatures `json:"supported_features,omitempty"`
	ServingNode           *ServingNode        `json:"serving_node,omitempty"`
	AdditionalServingNode *ServingNode        `json:"additional_serving_node,omitempty"`
	SMSF3GPPAddress       *SMSFAddress        `json:"smsf_3gpp_address,omitempty"`
	SMSFNon3GPPAddress    *SMSFAddress        `json:"smsf_non_3gpp_address,omitempty"`
	LMSI                  []byte              `json:"lmsi,omitempty"`
	UserIdentifier        *UserIdentifier     `json:"user_identifier,omitempty"`
	MWDStatus             uint32              `json:"mwd_status,omitempty"`

	MMEAbsentUserDiagnosticSM         *uint32 `json:"mme_absent_user_diagnostic_sm,omitempty"`
	MSCAbsentUserDiagnosticSM         *uint32 `json:"msc_absent_user_diagnostic_sm,omitempty"`
	SGSNAbsentUserDiagnosticSM        *uint32 `json:"sgsn_absent_user_diagnostic_sm,omitempty"`
	SMSF3GPPAbsentUserDiagnosticSM    *uint32 `json:"smsf_3gpp_absent_user_diagnostic_sm,omitempty"`
	SMSFNon3GPPAbsentUserDiagnosticSM *uint32 `json:"smsf_non_3gpp_absent_user_diagnostic_sm,omitempty"`

	// The serving node's MME and SGSN identities, copied from ServingNode
	// in the reply to diameter.sgd.srr.request, where applications written
	// for earlier releases read them. Ignored in an application's SRA.
	MMEName    string `json:"mme_name,omitempty"`
	MMERealm   string `json:"mme_realm,omitempty"`
	SGSNName   string `json:"sgsn_name,omitempty"`
	SGSNRealm  string `json:"sgsn_realm,omitempty"`
	SGSNNumber string `json:"sgsn_number,omitempty"`
}

// ALREvent is an Alert-Service-Centre-Request (§5.3.2.5): from the HSS to
// the SMS-IWMSC, or from the MME or SGSN to the SMS-GMSC.
type ALREvent struct {
	Header                    charging.DiameterHeader `json:"header"`
	SCAddress                 string                  `json:"sc_address"`
	UserIdentifier            *UserIdentifier         `json:"user_identifier"`
	SMSMICorrelationID        *SMSMICorrelationID     `json:"smsmi_correlation_id,omitempty"`
	MaximumUEAvailabilityTime int64                   `json:"maximum_ue_availability_time,omitempty"`
	SMSGMSCAlertEvent         uint32                  `json:"sms_gmsc_alert_event,omitempty"` // bits: 0 UE-Available-For-MT-SMS, 1 UE-Under-New-Serving-Node
	ServingNode               *ServingNode            `json:"serving_node,omitempty"`
	SupportedFeatures         []SupportedFeatures     `json:"supported_features,omitempty"`
}

// ALAAnswer is an Alert-Service-Centre-Answer (§5.3.2.6).
type ALAAnswer struct {
	Result
	SupportedFeatures []SupportedFeatures `json:"supported_features,omitempty"`
}

// RDREvent is a Report-SM-Delivery-Status-Request (§5.3.2.7).
type RDREvent struct {
	Header             charging.DiameterHeader `json:"header"`
	SupportedFeatures  []SupportedFeatures     `json:"supported_features,omitempty"`
	UserIdentifier     *UserIdentifier         `json:"user_identifier"`
	SMSMICorrelationID *SMSMICorrelationID     `json:"smsmi_correlation_id,omitempty"`
	SCAddress          string                  `json:"sc_address"`
	SMDeliveryOutcome  *SMDeliveryOutcome      `json:"sm_delivery_outcome"`
	RDRFlags           uint32                  `json:"rdr_flags,omitempty"` // bit 0: Single-Attempt-Delivery
}

// RDAAnswer is a Report-SM-Delivery-Status-Answer (§5.3.2.8).
type RDAAnswer struct {
	Result
	SupportedFeatures []SupportedFeatures `json:"supported_features,omitempty"`
	UserIdentifier    *UserIdentifier     `json:"user_identifier,omitempty"`
}

// TS 29.338 §7 Experimental-Result-Code values (vendor 3GPP).
const (
	DiameterErrorUserUnknown          uint32 = 5001
	DiameterErrorAbsentUser           uint32 = 5550
	DiameterErrorUserBusyForMTSMS     uint32 = 5551
	DiameterErrorFacilityNotSupported uint32 = 5552
	DiameterErrorIllegalUser          uint32 = 5553
	DiameterErrorIllegalEquipment     uint32 = 5554
	DiameterErrorSMDeliveryFailure    uint32 = 5555
	DiameterErrorServiceNotSubscribed uint32 = 5556
	DiameterErrorServiceBarred        uint32 = 5557
	DiameterErrorMWDListFull          uint32 = 5558
)
