// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import "github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"

// NATSTopics for Gx interface
const (
	TopicGxCCR = "diameter.gx.ccr"
	TopicGxRAR = "diameter.gx.rar" // PCRF→PCEF Re-Auth (rule update / QoS / event-trigger)
	TopicGxASR = "diameter.gx.asr" // PCRF→PCEF Abort-Session (force terminate)
)

// Diameter command codes for Gx reverse direction (RFC 4006 §3.2 + TS 29.212).
// STR (275) is intentionally absent — STR is PCEF→PCRF forward direction;
// session terminate from PCRF uses ASR (274).
const (
	CommandRAR = 258 // Re-Auth-Request
	CommandASR = 274 // Abort-Session-Request
)

// Re-Auth-Request-Type values (RFC 4006 §8.12, AVP code 285).
const (
	ReAuthTypeAuthorizeOnly         uint32 = 0
	ReAuthTypeAuthorizeAuthenticate uint32 = 1
)

// Abort-Cause values (TS 29.212 §5.3.1, AVP code 500). Drives the PCEF's
// disposition of in-flight bearers when the session is aborted.
const (
	AbortCauseBearerReleased       uint32 = 0 // PCRF releases the bearer (admin)
	AbortCauseInsufficientResource uint32 = 1
	AbortCauseUERemoved            uint32 = 2
	AbortCausePSToCSHandover       uint32 = 3
	AbortCauseLinkBroken           uint32 = 4
)

// Gx Application ID (3GPP TS 29.212)
const AppIDGx = 16777238

// GxEvent represents a Credit-Control-Request for Gx interface (Policy Control)
// 3GPP TS 29.212 compliance
type GxEvent struct {
	Header charging.DiameterHeader `json:"header"`

	// Credit Control (RFC 4006)
	CCRequestType   uint32 `json:"cc_request_type"`
	CCRequestNumber uint32 `json:"cc_request_number"`
	OriginStateID   uint32 `json:"origin_state_id,omitempty"`

	// Subscription
	SubscriptionID    []charging.SubscriptionID   `json:"subscription_id,omitempty"`
	UserEquipmentInfo *charging.UserEquipmentInfo `json:"user_equipment_info,omitempty"`

	// Network Location
	NetworkRequestSupport uint32 `json:"network_request_support,omitempty"`
	IPCANType             uint32 `json:"ip_can_type,omitempty"`
	RATType               uint32 `json:"rat_type,omitempty"`
	ANGWAddress           []byte `json:"an_gw_address,omitempty"`
	ANGWStatus            uint32 `json:"an_gw_status,omitempty"`

	// Framed IP
	FramedIPAddress  []byte `json:"framed_ip_address,omitempty"`
	FramedIPv6Prefix []byte `json:"framed_ipv6_prefix,omitempty"`

	// 3GPP Specific
	ThreeGPPSGSNMCCMNC       string `json:"3gpp_sgsn_mcc_mnc,omitempty"`
	ThreeGPPUserLocationInfo []byte `json:"3gpp_user_location_info,omitempty"`
	ThreeGPPMSTimeZone       []byte `json:"3gpp_ms_timezone,omitempty"`
	CalledStationID          string `json:"called_station_id,omitempty"`
	PDNConnectionID          []byte `json:"pdn_connection_id,omitempty"`
	BearerIdentifier         []byte `json:"bearer_identifier,omitempty"`
	BearerOperation          uint32 `json:"bearer_operation,omitempty"`

	// QoS
	QoSInformation      *QoSInformation      `json:"qos_information,omitempty"`
	DefaultEPSBearerQoS *DefaultEPSBearerQoS `json:"default_eps_bearer_qos,omitempty"`
	APNAMBR             *APNAMBR             `json:"apn_ambr,omitempty"`

	// Event Triggers
	EventTrigger []uint32 `json:"event_trigger,omitempty"`

	// Usage Monitoring
	UsageMonitoringInformation []UsageMonitoringInfo `json:"usage_monitoring_information,omitempty"`

	// Charging Rules (for updates)
	ChargingRuleReport []ChargingRuleReport `json:"charging_rule_report,omitempty"`

	// Termination
	TerminationCause uint32 `json:"termination_cause,omitempty"`
}

// GxAnswer represents a Credit-Control-Answer for Gx interface
type GxAnswer struct {
	ResultCode      uint32 `json:"result_code"`
	CCRequestType   uint32 `json:"cc_request_type"`
	CCRequestNumber uint32 `json:"cc_request_number"`
	OriginHost      string `json:"origin_host,omitempty"`
	OriginRealm     string `json:"origin_realm,omitempty"`

	// Charging Rules
	ChargingRuleInstall []ChargingRuleInstall `json:"charging_rule_install,omitempty"`
	ChargingRuleRemove  []ChargingRuleRemove  `json:"charging_rule_remove,omitempty"`

	// QoS
	QoSInformation      *QoSInformation      `json:"qos_information,omitempty"`
	DefaultEPSBearerQoS *DefaultEPSBearerQoS `json:"default_eps_bearer_qos,omitempty"`
	APNAMBR             *APNAMBR             `json:"apn_ambr,omitempty"`

	// Event Triggers
	EventTrigger []uint32 `json:"event_trigger,omitempty"`

	// Revalidation
	RevalidationTime int64 `json:"revalidation_time,omitempty"`

	// Usage Monitoring
	UsageMonitoringInformation []UsageMonitoringInfo `json:"usage_monitoring_information,omitempty"`

	// Error handling
	FailedAVP          []charging.FailedAVP `json:"failed_avp,omitempty"`
	ErrorMessage       string               `json:"error_message,omitempty"`
	ErrorReportingHost string               `json:"error_reporting_host,omitempty"`
}

// GxReAuthRequest is the NATS event body for diameter.gx.rar — PCRF backend
// requests the gateway to push an RAR (TS 29.212 §5.6.2) to the PCEF holding
// the session.
//
// Header.SessionID MUST be the original CCR-I session id (PCRF must remember
// it from the CCR-I/CCA exchange). Header.SourceHost MAY hint the target
// peer; when empty, the gateway resolves it via GxSessionRegistry.
type GxReAuthRequest struct {
	Header            charging.DiameterHeader `json:"header"`
	ReAuthRequestType uint32                  `json:"re_auth_request_type"`

	// Charging Rules
	ChargingRuleInstall []ChargingRuleInstall `json:"charging_rule_install,omitempty"`
	ChargingRuleRemove  []ChargingRuleRemove  `json:"charging_rule_remove,omitempty"`

	// QoS
	QoSInformation      *QoSInformation      `json:"qos_information,omitempty"`
	DefaultEPSBearerQoS *DefaultEPSBearerQoS `json:"default_eps_bearer_qos,omitempty"`

	// Event Triggers
	EventTrigger []uint32 `json:"event_trigger,omitempty"`

	// Session Release (optional — when set, asks PCEF to tear down the
	// session after applying any rules in this RAR).
	SessionReleaseCause uint32 `json:"session_release_cause,omitempty"`
}

// GxReAuthAnswer is the NATS reply envelope for an RAR exchange. ResultCode
// follows RFC 6733 §7 codes; ChargingRuleReport surfaces per-rule status
// from the PCEF (TS 29.212 §5.3.18) so the PCRF can react to install
// failures.
type GxReAuthAnswer struct {
	ResultCode uint32 `json:"result_code"`
	// ExperimentalResultCode is set, and ResultCode is 0, when the PCEF
	// answered with an Experimental-Result (RFC 6733 §7.6).
	ExperimentalResultCode uint32 `json:"experimental_result_code,omitempty"`
	OriginHost             string `json:"origin_host,omitempty"`
	OriginRealm            string `json:"origin_realm,omitempty"`

	// Rule status
	ChargingRuleReport []ChargingRuleReport `json:"charging_rule_report,omitempty"`
}

// GxAbortSessionRequest is the NATS event body for diameter.gx.asr — PCRF
// backend requests the gateway to push an ASR (RFC 4006 §3.2.5 + TS 29.212
// §4.5.5) to the PCEF holding the session.
//
// Header.SessionID is REQUIRED — ASR carries no User-Name in Gx; routing is
// purely Session-Id based. AbortCause defaults to 0 (BEARER_RELEASED) when
// not set.
type GxAbortSessionRequest struct {
	Header     charging.DiameterHeader `json:"header"`
	AbortCause uint32                  `json:"abort_cause,omitempty"`
}

// GxAbortSessionAnswer is the NATS reply envelope for an ASR exchange.
// 5002 (DIAMETER_UNKNOWN_SESSION_ID) is the expected "session already gone"
// response — operators typically treat it as soft success.
type GxAbortSessionAnswer struct {
	ResultCode uint32 `json:"result_code"`
	// ExperimentalResultCode is set, and ResultCode is 0, when the PCEF
	// answered with an Experimental-Result (RFC 6733 §7.6).
	ExperimentalResultCode uint32 `json:"experimental_result_code,omitempty"`
	OriginHost             string `json:"origin_host,omitempty"`
	OriginRealm            string `json:"origin_realm,omitempty"`
}

// =============================================================================
// QoS Types
// =============================================================================

// QoSInformation contains QoS parameters
type QoSInformation struct {
	QoSClassIdentifier          uint32                       `json:"qos_class_identifier,omitempty"`
	MaxRequestedBandwidthUL     uint32                       `json:"max_requested_bandwidth_ul,omitempty"`
	MaxRequestedBandwidthDL     uint32                       `json:"max_requested_bandwidth_dl,omitempty"`
	GuaranteedBitrateUL         uint32                       `json:"guaranteed_bitrate_ul,omitempty"`
	GuaranteedBitrateDL         uint32                       `json:"guaranteed_bitrate_dl,omitempty"`
	BearerIdentifier            []byte                       `json:"bearer_identifier,omitempty"`
	AllocationRetentionPriority *AllocationRetentionPriority `json:"allocation_retention_priority,omitempty"`
}

// AllocationRetentionPriority contains ARP information
type AllocationRetentionPriority struct {
	PriorityLevel           uint32 `json:"priority_level"`
	PreEmptionCapability    uint32 `json:"pre_emption_capability,omitempty"`
	PreEmptionVulnerability uint32 `json:"pre_emption_vulnerability,omitempty"`
}

// DefaultEPSBearerQoS contains default bearer QoS
type DefaultEPSBearerQoS struct {
	QoSClassIdentifier          uint32                       `json:"qos_class_identifier"`
	AllocationRetentionPriority *AllocationRetentionPriority `json:"allocation_retention_priority,omitempty"`
}

// APNAMBR contains APN Aggregate Maximum Bit Rate
type APNAMBR struct {
	APNAggregateMaxBitrateUL uint32 `json:"apn_aggregate_max_bitrate_ul"`
	APNAggregateMaxBitrateDL uint32 `json:"apn_aggregate_max_bitrate_dl"`
}

// =============================================================================
// Charging Rule Types
// =============================================================================

// ChargingRuleInstall contains rules to install
type ChargingRuleInstall struct {
	ChargingRuleName       []string       `json:"charging_rule_name,omitempty"`
	ChargingRuleBaseName   []string       `json:"charging_rule_base_name,omitempty"`
	ChargingRuleDefinition []ChargingRule `json:"charging_rule_definition,omitempty"`
	BearerIdentifier       []byte         `json:"bearer_identifier,omitempty"`
	RuleActivationTime     int64          `json:"rule_activation_time,omitempty"`
	RuleDeactivationTime   int64          `json:"rule_deactivation_time,omitempty"`
}

// ChargingRuleRemove contains rules to remove
type ChargingRuleRemove struct {
	ChargingRuleName     []string `json:"charging_rule_name,omitempty"`
	ChargingRuleBaseName []string `json:"charging_rule_base_name,omitempty"`
}

// ChargingRule defines a charging rule
type ChargingRule struct {
	ChargingRuleName    string          `json:"charging_rule_name"`
	ServiceIdentifier   uint32          `json:"service_identifier,omitempty"`
	RatingGroup         uint32          `json:"rating_group,omitempty"`
	FlowDescription     []string        `json:"flow_description,omitempty"`
	FlowInformation     []FlowInfo      `json:"flow_information,omitempty"`
	FlowStatus          uint32          `json:"flow_status,omitempty"`
	QoSInformation      *QoSInformation `json:"qos_information,omitempty"`
	ReportingLevel      uint32          `json:"reporting_level,omitempty"`
	Online              uint32          `json:"online,omitempty"`
	Offline             uint32          `json:"offline,omitempty"`
	MeteringMethod      uint32          `json:"metering_method,omitempty"`
	Precedence          uint32          `json:"precedence,omitempty"`
	MonitoringKey       []byte          `json:"monitoring_key,omitempty"`
	RedirectInformation *RedirectInfo   `json:"redirect_information,omitempty"`
}

// FlowInfo contains flow information
type FlowInfo struct {
	FlowDescription string `json:"flow_description,omitempty"`
	FlowDirection   uint32 `json:"flow_direction,omitempty"`
}

// RedirectInfo contains redirect information
type RedirectInfo struct {
	RedirectSupport       uint32 `json:"redirect_support,omitempty"`
	RedirectAddressType   uint32 `json:"redirect_address_type,omitempty"`
	RedirectServerAddress string `json:"redirect_server_address,omitempty"`
}

// ChargingRuleReport reports rule status
type ChargingRuleReport struct {
	ChargingRuleName     []string `json:"charging_rule_name,omitempty"`
	ChargingRuleBaseName []string `json:"charging_rule_base_name,omitempty"`
	BearerIdentifier     []byte   `json:"bearer_identifier,omitempty"`
	PCCRuleStatus        uint32   `json:"pcc_rule_status,omitempty"`
	RuleFailureCode      uint32   `json:"rule_failure_code,omitempty"`
}

// =============================================================================
// Usage Monitoring Types
// =============================================================================

// UsageMonitoringInfo contains usage monitoring data
type UsageMonitoringInfo struct {
	MonitoringKey          []byte                `json:"monitoring_key,omitempty"`
	GrantedServiceUnit     *charging.ServiceUnit `json:"granted_service_unit,omitempty"`
	UsedServiceUnit        *charging.ServiceUnit `json:"used_service_unit,omitempty"`
	UsageMonitoringLevel   uint32                `json:"usage_monitoring_level,omitempty"`
	UsageMonitoringReport  uint32                `json:"usage_monitoring_report,omitempty"`
	UsageMonitoringSupport uint32                `json:"usage_monitoring_support,omitempty"`
}

// =============================================================================
// Event Trigger Constants (TS 29.212)
// =============================================================================

const (
	EventTriggerSGSNChange                uint32 = 0
	EventTriggerQoSChange                 uint32 = 1
	EventTriggerRATChange                 uint32 = 2
	EventTriggerTFTChange                 uint32 = 3
	EventTriggerPLMNChange                uint32 = 4
	EventTriggerLossOfBearer              uint32 = 5
	EventTriggerRecoveryOfBearer          uint32 = 6
	EventTriggerIPCANChange               uint32 = 7
	EventTriggerQoSChangeExceedAuth       uint32 = 11
	EventTriggerRATTypeChange             uint32 = 12
	EventTriggerUserLocationChange        uint32 = 13
	EventTriggerNoEventTriggers           uint32 = 14
	EventTriggerOutOfCredit               uint32 = 15
	EventTriggerReallocationOfCredit      uint32 = 16
	EventTriggerRevalidationTimeout       uint32 = 17
	EventTriggerUEIPAddressAllocate       uint32 = 18
	EventTriggerUEIPAddressRelease        uint32 = 19
	EventTriggerDefaultEPSBearerQoSChange uint32 = 20
	EventTriggerANGWChange                uint32 = 21
	EventTriggerSuccessfulResourceAlloc   uint32 = 22
	EventTriggerResourceModificationReq   uint32 = 23
	EventTriggerUsageReport               uint32 = 33
	EventTriggerDefaultEPSBearerQoSMod    uint32 = 34
)

// IP-CAN-Type values
const (
	IPCANType3GPP_GPRS uint32 = 0
	IPCANType3GPP_EPS  uint32 = 5
	IPCANType3GPP_5GS  uint32 = 9
)

// Bearer-Operation values
const (
	BearerOperationTermination   uint32 = 0
	BearerOperationEstablishment uint32 = 1
	BearerOperationModification  uint32 = 2
)

// PCC-Rule-Status values
const (
	PCCRuleStatusActive            uint32 = 0
	PCCRuleStatusInactive          uint32 = 1
	PCCRuleStatusTemporaryInactive uint32 = 2
)

// Reporting-Level values
const (
	ReportingLevelServiceIdentifier     uint32 = 0
	ReportingLevelRatingGroup           uint32 = 1
	ReportingLevelSponsoredConnectivity uint32 = 2
)

// Online/Offline values
const (
	OnlineDisable  uint32 = 0
	OnlineEnable   uint32 = 1
	OfflineDisable uint32 = 0
	OfflineEnable  uint32 = 1
)

// CCRequestTypeName returns the name of the CC-Request-Type
func CCRequestTypeName(t uint32) string {
	if name, ok := charging.CCRequestTypeNames[t]; ok {
		return name
	}
	return "UNKNOWN"
}
