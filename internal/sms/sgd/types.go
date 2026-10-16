// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
)

// =============================================================================
// SGd Interface Constants (TS 29.338)
// =============================================================================

// Application and Vendor IDs
const (
	AppIDSGd     uint32 = 16777312 // 3GPP SGd Application ID
	VendorID3GPP uint32 = 10415    // 3GPP Vendor ID
)

// Command Codes
const (
	CommandOFR uint32 = 8388645 // MO-Forward-Short-Message-Request
	CommandTFR uint32 = 8388646 // MT-Forward-Short-Message-Request
	CommandSRR uint32 = 8388647 // Send-Routing-Info-for-SM-Request
	CommandALR uint32 = 8388648 // Alert-Service-Centre-Request
)

// AVP Codes (3GPP Vendor Specific)
const (
	AVPUserIdentifier                   uint32 = 3102 // Grouped
	AVPMSISDN                           uint32 = 701
	AVPUserName                         uint32 = 1    // RFC
	AVPSMRPUICode                       uint32 = 3301 // SM-RP-UI (SMS PDU)
	AVPSMDeliveryOutcome                uint32 = 3320
	AVPSMSMICorrelationID               uint32 = 3324 // Grouped
	AVPHSSID                            uint32 = 3325
	AVPOriginatorSCCPAddress            uint32 = 3326
	AVPMTCIWFAddress                    uint32 = 3406
	AVPSMDeliveryTimer                  uint32 = 3306
	AVPSMDeliveryStartTime              uint32 = 3307
	AVPMaximumRetransmissions           uint32 = 3330
	AVPSMSGMSCAddress                   uint32 = 3332
	AVPSMSGMSCAlertEvent                uint32 = 3333
	AVPSMRPMTI                          uint32 = 3308 // SM-RP-MTI (0=SMS-DELIVER, 1=SMS-STATUS-REPORT)
	AVPSMRPSMEA                         uint32 = 3309 // SM-RP-SMEA
	AVPSRRFlags                         uint32 = 3310
	AVPSMDeliveryNotIntended            uint32 = 3311
	AVPMWDStatus                        uint32 = 3312
	AVPMMENumberForMTSMS                uint32 = 1645
	AVPMMEName                          uint32 = 1643
	AVPMMERealm                         uint32 = 1644
	AVPServingNode                      uint32 = 2401 // Grouped
	AVPSGSNNumber                       uint32 = 1489
	AVPSGSNName                         uint32 = 2409
	AVPSGSNRealm                        uint32 = 2410
	AVPMSCNumber                        uint32 = 2403
	AVPThreeGPPSMSCAddress              uint32 = 3329
	AVPSMDeliveryFailureCause           uint32 = 3303
	AVPSMDiagnosticInfo                 uint32 = 3305
	AVPServiceCentreAddress             uint32 = 3300 // SC Address
	AVPSMEnumeratedDeliveryFailureCause uint32 = 3304
	AVPAbsentUserDiagnosticSM           uint32 = 3322
	AVPLMSI                             uint32 = 2400
	AVPUserIdentifierValue              uint32 = 3102
	AVPExternalIdentifier               uint32 = 3111
)

// SM-Delivery-Outcome values
const (
	SMDeliveryOutcomeSuccess          uint32 = 0
	SMDeliveryOutcomeAbsentSubscriber uint32 = 1
	SMDeliveryOutcomeUEMemoryCapacity uint32 = 2
	SMDeliveryOutcomeNotReachable     uint32 = 3
	SMDeliveryOutcomeNetworkError     uint32 = 4
)

// SM-RP-MTI values
const (
	SMRPMTISMSDeliver      uint32 = 0
	SMRPMTISMSStatusReport uint32 = 1
)

// SRR-Flags bit values
const (
	SRRFlagSingleAttempt             uint32 = 0x01 // Bit 0
	SRRFlagMoreMessagesToSend        uint32 = 0x02 // Bit 1
	SRRFlagGPRSIndicator             uint32 = 0x04 // Bit 2
	SRRFlagSMDeliveryNotIntendedFlag uint32 = 0x08 // Bit 3
	SRRFlagIPSMGWIndicator           uint32 = 0x10 // Bit 4
)

// MWD-Status bit values (Message Waiting Data)
const (
	MWDStatusSCAddressNotIncluded uint32 = 0x01
	MWDStatusMNRFSet              uint32 = 0x02
	MWDStatusMCEFSet              uint32 = 0x04
	MWDStatusMNRGSet              uint32 = 0x08
)

// =============================================================================
// MO-Forward-Short-Message (OFR/OFA) - Mobile Originated SMS
// =============================================================================

// OFREvent represents MO-Forward-Short-Message-Request
type OFREvent struct {
	Header charging.DiameterHeader `json:"header"`

	// User identification
	UserIdentifier *UserIdentifier `json:"user_identifier,omitempty"`

	// SM-RP fields
	SMRPUI     []byte `json:"sm_rp_ui"`                // SMS PDU (TPDU)
	SMRPOAType uint32 `json:"sm_rp_oa_type,omitempty"` // 0=MSISDN, 1=External-ID
	SMRPOA     string `json:"sm_rp_oa,omitempty"`      // Originating Address

	// Routing
	SCAddress      string `json:"sc_address,omitempty"`       // Service Centre
	SMSGMSCAddress string `json:"sms_gmsc_address,omitempty"` // SMS-GMSC

	// Additional
	SupportedFeatures  uint64              `json:"supported_features,omitempty"`
	SMSMICorrelationID *SMSMICorrelationID `json:"smsmi_correlation_id,omitempty"`
	ExternalIdentifier string              `json:"external_identifier,omitempty"`
}

// OFAAnswer represents MO-Forward-Short-Message-Answer
type OFAAnswer struct {
	ResultCode         uint32              `json:"result_code"`
	ExperimentalResult *ExperimentalResult `json:"experimental_result,omitempty"`
	OriginHost         string              `json:"origin_host,omitempty"`
	OriginRealm        string              `json:"origin_realm,omitempty"`
	SupportedFeatures  uint64              `json:"supported_features,omitempty"`
}

// =============================================================================
// MT-Forward-Short-Message (TFR/TFA) - Mobile Terminated SMS
// =============================================================================

// TFREvent represents MT-Forward-Short-Message-Request
type TFREvent struct {
	Header charging.DiameterHeader `json:"header"`

	// User identification
	UserIdentifier *UserIdentifier `json:"user_identifier,omitempty"`

	// SM-RP fields
	SMRPUI   []byte `json:"sm_rp_ui"`             // SMS PDU (TPDU)
	SMRPMTI  uint32 `json:"sm_rp_mti,omitempty"`  // 0=SMS-DELIVER, 1=STATUS-REPORT
	SMRPSMEA []byte `json:"sm_rp_smea,omitempty"` // SM originating entity address

	// MME/SGSN identification
	MMEName           string `json:"mme_name,omitempty"`
	MMERealm          string `json:"mme_realm,omitempty"`
	MMENumberForMTSMS []byte `json:"mme_number_for_mt_sms,omitempty"`
	SGSNNumber        []byte `json:"sgsn_number,omitempty"`
	SGSNName          string `json:"sgsn_name,omitempty"`
	SGSNRealm         string `json:"sgsn_realm,omitempty"`

	// Routing
	SCAddress           string `json:"sc_address,omitempty"`
	SMDeliveryTimer     uint32 `json:"sm_delivery_timer,omitempty"`
	SMDeliveryStartTime int64  `json:"sm_delivery_start_time,omitempty"`
	MaxRetransmissions  uint32 `json:"max_retransmissions,omitempty"`

	// Correlation
	SMSMICorrelationID *SMSMICorrelationID `json:"smsmi_correlation_id,omitempty"`

	// Additional
	SupportedFeatures uint64 `json:"supported_features,omitempty"`
}

// TFAAnswer represents MT-Forward-Short-Message-Answer
type TFAAnswer struct {
	ResultCode         uint32              `json:"result_code"`
	ExperimentalResult *ExperimentalResult `json:"experimental_result,omitempty"`
	OriginHost         string              `json:"origin_host,omitempty"`
	OriginRealm        string              `json:"origin_realm,omitempty"`

	// Delivery status
	SMDeliveryOutcome      *SMDeliveryOutcome `json:"sm_delivery_outcome,omitempty"`
	AbsentUserDiagnosticSM uint32             `json:"absent_user_diagnostic_sm,omitempty"`

	// Supported features
	SupportedFeatures uint64 `json:"supported_features,omitempty"`
}

// =============================================================================
// Send-Routing-Info-for-SM (SRR/SRA)
// =============================================================================

// SRREvent represents Send-Routing-Info-for-SM-Request
type SRREvent struct {
	Header charging.DiameterHeader `json:"header"`

	// User identification
	UserIdentifier *UserIdentifier `json:"user_identifier,omitempty"`
	MSISDN         []byte          `json:"msisdn,omitempty"`

	// SC identification
	SCAddress string `json:"sc_address,omitempty"`
	SMRPPri   uint32 `json:"sm_rp_pri,omitempty"` // Priority indicator

	// Flags
	SRRFlags              uint32 `json:"srr_flags,omitempty"` // Bit flags
	SMDeliveryNotIntended uint32 `json:"sm_delivery_not_intended,omitempty"`
	SupportedFeatures     uint64 `json:"supported_features,omitempty"`
}

// SRAAnswer represents Send-Routing-Info-for-SM-Answer
type SRAAnswer struct {
	ResultCode         uint32              `json:"result_code"`
	ExperimentalResult *ExperimentalResult `json:"experimental_result,omitempty"`
	OriginHost         string              `json:"origin_host,omitempty"`
	OriginRealm        string              `json:"origin_realm,omitempty"`

	// User data
	UserName string `json:"user_name,omitempty"` // IMSI
	LMSI     []byte `json:"lmsi,omitempty"`

	// Serving node(s)
	ServingNode           []ServingNode `json:"serving_node,omitempty"`
	AdditionalServingNode []ServingNode `json:"additional_serving_node,omitempty"`

	// MME info
	MMEName           string `json:"mme_name,omitempty"`
	MMERealm          string `json:"mme_realm,omitempty"`
	MMENumberForMTSMS []byte `json:"mme_number_for_mt_sms,omitempty"`

	// SGSN info
	SGSNNumber []byte `json:"sgsn_number,omitempty"`
	SGSNName   string `json:"sgsn_name,omitempty"`
	SGSNRealm  string `json:"sgsn_realm,omitempty"`

	// MSC info
	MSCNumber           []byte `json:"msc_number,omitempty"`
	ThreeGPPSMSCAddress string `json:"3gpp_smsc_address,omitempty"`

	// MWD (Message Waiting Data)
	MWDStatus uint32 `json:"mwd_status,omitempty"`

	// Additional
	SupportedFeatures uint64 `json:"supported_features,omitempty"`
}

// =============================================================================
// Alert-Service-Centre (ALR/ALA)
// =============================================================================

// ALREvent represents Alert-Service-Centre-Request
type ALREvent struct {
	Header charging.DiameterHeader `json:"header"`

	// User identification
	UserIdentifier *UserIdentifier `json:"user_identifier,omitempty"`

	// SC identification
	SCAddress string `json:"sc_address,omitempty"`

	// Alert info
	SMSGMSCAlertEvent uint32       `json:"sms_gmsc_alert_event,omitempty"`
	ServingNode       *ServingNode `json:"serving_node,omitempty"`

	// Additional
	SupportedFeatures uint64 `json:"supported_features,omitempty"`
}

// ALAAnswer represents Alert-Service-Centre-Answer
type ALAAnswer struct {
	ResultCode         uint32              `json:"result_code"`
	ExperimentalResult *ExperimentalResult `json:"experimental_result,omitempty"`
	OriginHost         string              `json:"origin_host,omitempty"`
	OriginRealm        string              `json:"origin_realm,omitempty"`
	SupportedFeatures  uint64              `json:"supported_features,omitempty"`
}

// =============================================================================
// Common Types
// =============================================================================

// UserIdentifier identifies a subscriber
type UserIdentifier struct {
	MSISDN             []byte `json:"msisdn,omitempty"`
	UserName           string `json:"user_name,omitempty"` // IMSI
	ExternalIdentifier string `json:"external_identifier,omitempty"`
	LMSI               []byte `json:"lmsi,omitempty"`
}

// ServingNode contains serving node information
type ServingNode struct {
	// SGSN
	SGSNNumber []byte `json:"sgsn_number,omitempty"`
	SGSNName   string `json:"sgsn_name,omitempty"`
	SGSNRealm  string `json:"sgsn_realm,omitempty"`

	// MME
	MMEName           string `json:"mme_name,omitempty"`
	MMERealm          string `json:"mme_realm,omitempty"`
	MMENumberForMTSMS []byte `json:"mme_number_for_mt_sms,omitempty"`

	// MSC
	MSCNumber           []byte `json:"msc_number,omitempty"`
	ThreeGPPSMSCAddress string `json:"3gpp_smsc_address,omitempty"`

	// IP-SM-GW
	IPSMGWNumber []byte `json:"ip_sm_gw_number,omitempty"`
	IPSMGWName   string `json:"ip_sm_gw_name,omitempty"`
	IPSMGWRealm  string `json:"ip_sm_gw_realm,omitempty"`
}

// SMDeliveryOutcome contains delivery result
type SMDeliveryOutcome struct {
	MMEAbsentUserDiagnosticSM    uint32 `json:"mme_absent_user_diagnostic_sm,omitempty"`
	MSCAbsentUserDiagnosticSM    uint32 `json:"msc_absent_user_diagnostic_sm,omitempty"`
	SGSNAbsentUserDiagnosticSM   uint32 `json:"sgsn_absent_user_diagnostic_sm,omitempty"`
	IPSMGWAbsentUserDiagnosticSM uint32 `json:"ip_sm_gw_absent_user_diagnostic_sm,omitempty"`

	SMDeliveryCause              uint32 `json:"sm_delivery_cause,omitempty"`
	AbsentSubscriberDiagnosticSM uint32 `json:"absent_subscriber_diagnostic_sm,omitempty"`
}

// SMSMICorrelationID for SMS correlation
type SMSMICorrelationID struct {
	HSSID                   string `json:"hss_id,omitempty"`
	OriginatorSCCPAddress   []byte `json:"originator_sccp_address,omitempty"`
	SMDeliveryCorrelationID string `json:"sm_delivery_correlation_id,omitempty"`
}

// ExperimentalResult for 3GPP experimental result codes
type ExperimentalResult struct {
	VendorID               uint32 `json:"vendor_id"`
	ExperimentalResultCode uint32 `json:"experimental_result_code"`
}

// SMSDeliveryStatus for NATS response
type SMSDeliveryStatus struct {
	Success         bool      `json:"success"`
	ResultCode      uint32    `json:"result_code"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	DeliveryOutcome uint32    `json:"delivery_outcome,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// =============================================================================
// Experimental Result Codes (TS 29.338)
// =============================================================================

const (
	// Success
	DiameterSuccessSGd uint32 = 2001

	// Errors (5xxx)
	DiameterErrorUserUnknown          uint32 = 5001
	DiameterErrorSMDeliveryFailure    uint32 = 5016
	DiameterErrorServiceNotSubscribed uint32 = 5117
	DiameterErrorAbsentUser           uint32 = 4201
	DiameterErrorUserBusyForMTSMS     uint32 = 4243
	DiameterErrorFacilityNotSupported uint32 = 4244
	DiameterErrorIllegalUser          uint32 = 4245
	DiameterErrorIllegalEquipment     uint32 = 4246
	DiameterErrorSMDeliveryTimeout    uint32 = 4247
	DiameterErrorSMNotSupported       uint32 = 4248
)
