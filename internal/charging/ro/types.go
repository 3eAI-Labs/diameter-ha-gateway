// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package ro

import "github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"

// NATSTopics for Ro interface
const (
	TopicRoCCR = "diameter.ro.ccr"
)

// RoEvent represents a Credit-Control-Request for Ro interface (IMS Offline Charging)
// 3GPP TS 32.260 compliance
type RoEvent struct {
	Header charging.DiameterHeader `json:"header"`

	// Credit Control (RFC 4006)
	CCRequestType   uint32 `json:"cc_request_type"`
	CCRequestNumber uint32 `json:"cc_request_number"`
	OriginStateID   uint32 `json:"origin_state_id,omitempty"`
	EventTimestamp  int64  `json:"event_timestamp,omitempty"`

	// Subscription
	SubscriptionID    []charging.SubscriptionID   `json:"subscription_id,omitempty"`
	UserEquipmentInfo *charging.UserEquipmentInfo `json:"user_equipment_info,omitempty"`

	// Service Context
	ServiceContextID  string `json:"service_context_id,omitempty"`
	ServiceIdentifier uint32 `json:"service_identifier,omitempty"`
	RequestedAction   uint32 `json:"requested_action,omitempty"`

	// Credit Control Units
	RequestedServiceUnit      *charging.ServiceUnit `json:"requested_service_unit,omitempty"`
	UsedServiceUnit           *charging.ServiceUnit `json:"used_service_unit,omitempty"`
	MultipleServicesIndicator uint32                `json:"multiple_services_indicator,omitempty"`
	MSCC                      []charging.MSCC       `json:"mscc,omitempty"`

	// Termination
	TerminationCause uint32 `json:"termination_cause,omitempty"`

	// Service Information (IMS specific)
	ServiceInformation *RoServiceInformation `json:"service_information,omitempty"`
}

// RoServiceInformation contains service-specific information for Ro
type RoServiceInformation struct {
	IMSInformation *IMSInformation `json:"ims_information,omitempty"`
}

// IMSInformation contains IMS-specific charging information (TS 32.260)
type IMSInformation struct {
	EventType                    uint32                `json:"event_type,omitempty"`
	RoleOfNode                   uint32                `json:"role_of_node,omitempty"`
	NodeFunctionality            uint32                `json:"node_functionality,omitempty"`
	UserSessionID                string                `json:"user_session_id,omitempty"`
	OutgoingSessionID            string                `json:"outgoing_session_id,omitempty"`
	SessionPriority              uint32                `json:"session_priority,omitempty"`
	CallingPartyAddress          []string              `json:"calling_party_address,omitempty"`
	CalledPartyAddress           string                `json:"called_party_address,omitempty"`
	NumberPortabilityRoutingInfo string                `json:"number_portability_routing_info,omitempty"`
	CarrierSelectRoutingInfo     string                `json:"carrier_select_routing_info,omitempty"`
	AlternateChargedPartyAddress string                `json:"alternate_charged_party_address,omitempty"`
	RequestedPartyAddress        []string              `json:"requested_party_address,omitempty"`
	AssociatedURI                []string              `json:"associated_uri,omitempty"`
	TimeStamps                   *TimeStamps           `json:"time_stamps,omitempty"`
	ApplicationServerInformation []AppServerInfo       `json:"application_server_information,omitempty"`
	InterOperatorIdentifier      []IOI                 `json:"inter_operator_identifier,omitempty"`
	TransitIOIList               []string              `json:"transit_ioi_list,omitempty"`
	IMSChargingIdentifier        string                `json:"ims_charging_identifier,omitempty"`
	SDPSessionDescription        []string              `json:"sdp_session_description,omitempty"`
	SDPMediaComponent            []SDPMediaComponent   `json:"sdp_media_component,omitempty"`
	ServedPartyIPAddress         []byte                `json:"served_party_ip_address,omitempty"`
	ServerCapabilities           *ServerCapabilities   `json:"server_capabilities,omitempty"`
	TrunkGroupID                 *TrunkGroupID         `json:"trunk_group_id,omitempty"`
	BearerService                []byte                `json:"bearer_service,omitempty"`
	ServiceID                    string                `json:"service_id,omitempty"`
	ServiceSpecificInfo          []ServiceSpecificInfo `json:"service_specific_info,omitempty"`
	MessageBody                  []MessageBody         `json:"message_body,omitempty"`
	CauseCode                    int32                 `json:"cause_code,omitempty"`
	ReasonHeader                 []string              `json:"reason_header,omitempty"`
	AccessNetworkInformation     []string              `json:"access_network_information,omitempty"`
	CellularNetworkInformation   []byte                `json:"cellular_network_information,omitempty"`
	EarlyMediaDescription        []EarlyMediaDesc      `json:"early_media_description,omitempty"`
	IMSCommunicationServiceID    string                `json:"ims_communication_service_id,omitempty"`
	IMSApplicationReferenceID    string                `json:"ims_application_reference_id,omitempty"`
	OnlineChargingFlag           uint32                `json:"online_charging_flag,omitempty"`
	RealTimeTariffInformation    *RTTInfo              `json:"real_time_tariff_information,omitempty"`
}

// TimeStamps contains timing information
type TimeStamps struct {
	SIPRequestTimestamp          int64  `json:"sip_request_timestamp,omitempty"`
	SIPResponseTimestamp         int64  `json:"sip_response_timestamp,omitempty"`
	SIPRequestTimestampFraction  uint32 `json:"sip_request_timestamp_fraction,omitempty"`
	SIPResponseTimestampFraction uint32 `json:"sip_response_timestamp_fraction,omitempty"`
}

// AppServerInfo contains application server information
type AppServerInfo struct {
	ApplicationServerName                 string   `json:"application_server_name,omitempty"`
	ApplicationProvidedCalledPartyAddress []string `json:"application_provided_called_party_address,omitempty"`
	StatusASCode                          int32    `json:"status_as_code,omitempty"`
}

// IOI (Inter-Operator Identifier) information
type IOI struct {
	OriginatingIOI string `json:"originating_ioi,omitempty"`
	TerminatingIOI string `json:"terminating_ioi,omitempty"`
}

// SDPMediaComponent contains SDP media information
type SDPMediaComponent struct {
	SDPMediaName                         string   `json:"sdp_media_name,omitempty"`
	SDPMediaDescription                  []string `json:"sdp_media_description,omitempty"`
	LocalGWInsertedIndication            uint32   `json:"local_gw_inserted_indication,omitempty"`
	IPRealmDefaultIndication             uint32   `json:"ip_realm_default_indication,omitempty"`
	TranscoderInsertedIndication         uint32   `json:"transcoder_inserted_indication,omitempty"`
	MediaInitiatorFlag                   uint32   `json:"media_initiator_flag,omitempty"`
	MediaInitiatorParty                  uint32   `json:"media_initiator_party,omitempty"`
	ThreeGPPChargingID                   []byte   `json:"3gpp_charging_id,omitempty"`
	AccessNetworkChargingIdentifierValue []byte   `json:"access_network_charging_identifier_value,omitempty"`
	SDPType                              uint32   `json:"sdp_type,omitempty"`
}

// ServerCapabilities contains S-CSCF capabilities
type ServerCapabilities struct {
	MandatoryCapability []uint32 `json:"mandatory_capability,omitempty"`
	OptionalCapability  []uint32 `json:"optional_capability,omitempty"`
	ServerName          []string `json:"server_name,omitempty"`
}

// TrunkGroupID contains trunk group identification
type TrunkGroupID struct {
	IncomingTrunkGroupID string `json:"incoming_trunk_group_id,omitempty"`
	OutgoingTrunkGroupID string `json:"outgoing_trunk_group_id,omitempty"`
}

// ServiceSpecificInfo contains service-specific data
type ServiceSpecificInfo struct {
	ServiceSpecificData string `json:"service_specific_data,omitempty"`
	ServiceSpecificType uint32 `json:"service_specific_type,omitempty"`
}

// MessageBody contains SIP message body information
type MessageBody struct {
	ContentType        string `json:"content_type,omitempty"`
	ContentLength      uint32 `json:"content_length,omitempty"`
	ContentDisposition string `json:"content_disposition,omitempty"`
	Originator         uint32 `json:"originator,omitempty"`
}

// EarlyMediaDesc contains early media description
type EarlyMediaDesc struct {
	SDPTimeStamps         *TimeStamps         `json:"sdp_timestamps,omitempty"`
	SDPMediaComponent     []SDPMediaComponent `json:"sdp_media_component,omitempty"`
	SDPSessionDescription []string            `json:"sdp_session_description,omitempty"`
}

// RTTInfo contains real-time tariff information
type RTTInfo struct {
	TariffInformation *TariffInfo `json:"tariff_information,omitempty"`
	TariffXML         string      `json:"tariff_xml,omitempty"`
}

// TariffInfo contains tariff details
type TariffInfo struct {
	CurrentTariff    *CurrentTariff `json:"current_tariff,omitempty"`
	TariffTimeChange int64          `json:"tariff_time_change,omitempty"`
	NextTariff       *CurrentTariff `json:"next_tariff,omitempty"`
}

// CurrentTariff contains current tariff details
type CurrentTariff struct {
	CurrencyCode uint32        `json:"currency_code,omitempty"`
	ScaleFactor  *ScaleFactor  `json:"scale_factor,omitempty"`
	RateElement  []RateElement `json:"rate_element,omitempty"`
}

// ScaleFactor contains scale factor for tariff
type ScaleFactor struct {
	ValueDigits int64 `json:"value_digits"`
	Exponent    int32 `json:"exponent,omitempty"`
}

// RateElement contains rate information
type RateElement struct {
	CCUnitType         uint32     `json:"cc_unit_type,omitempty"`
	ReasonCode         uint32     `json:"reason_code,omitempty"`
	UnitValue          *UnitValue `json:"unit_value,omitempty"`
	UnitCost           *UnitCost  `json:"unit_cost,omitempty"`
	UnitQuotaThreshold uint32     `json:"unit_quota_threshold,omitempty"`
}

// UnitValue contains unit value
type UnitValue struct {
	ValueDigits int64 `json:"value_digits"`
	Exponent    int32 `json:"exponent,omitempty"`
}

// UnitCost contains unit cost
type UnitCost struct {
	ValueDigits int64 `json:"value_digits"`
	Exponent    int32 `json:"exponent,omitempty"`
}

// RoAnswer represents a Credit-Control-Answer for Ro interface
type RoAnswer struct {
	ResultCode        uint32 `json:"result_code"`
	CCRequestType     uint32 `json:"cc_request_type"`
	CCRequestNumber   uint32 `json:"cc_request_number"`
	OriginHost        string `json:"origin_host,omitempty"`
	OriginRealm       string `json:"origin_realm,omitempty"`
	CCSessionFailover uint32 `json:"cc_session_failover,omitempty"`

	// Credit Control
	MSCC []charging.MSCC `json:"mscc,omitempty"`

	// Validity
	ValidityTime uint32 `json:"validity_time,omitempty"`

	// Final Unit
	FinalUnitIndication *charging.FinalUnitIndication `json:"final_unit_indication,omitempty"`

	// Error info
	FailedAVP          []charging.FailedAVP `json:"failed_avp,omitempty"`
	ErrorMessage       string               `json:"error_message,omitempty"`
	ErrorReportingHost string               `json:"error_reporting_host,omitempty"`

	// Low Balance
	LowBalanceIndication uint32            `json:"low_balance_indication,omitempty"`
	RemainingBalance     *charging.CCMoney `json:"remaining_balance,omitempty"`
}

// =============================================================================
// IMS Event Type Constants (TS 32.260)
// =============================================================================

const (
	EventTypeSIPSessionInvite    uint32 = 0
	EventTypeSIPSessionTerminate uint32 = 1
	EventTypeSIPRegister         uint32 = 2
	EventTypeSIPSubscribe        uint32 = 3
	EventTypeSIPNotify           uint32 = 4
	EventTypeSIPRefer            uint32 = 5
	EventTypeSIPMessage          uint32 = 6
	EventTypeSIPPublish          uint32 = 7
	EventTypeSIPOption           uint32 = 8
	EventTypeSIPSessionProgress  uint32 = 9
)

// Role-of-Node values
const (
	RoleOriginating uint32 = 0
	RoleTerminating uint32 = 1
	RoleForwarding  uint32 = 2
)

// Node-Functionality values
const (
	NodeFunctionalitySCSCF uint32 = 0
	NodeFunctionalityPCSCF uint32 = 1
	NodeFunctionalityICSCF uint32 = 2
	NodeFunctionalityMRFC  uint32 = 3
	NodeFunctionalityMGCF  uint32 = 4
	NodeFunctionalityBGCF  uint32 = 5
	NodeFunctionalityASIGW uint32 = 6
	NodeFunctionalityIBCF  uint32 = 7
	NodeFunctionalitySGSN  uint32 = 8
	NodeFunctionalityATCF  uint32 = 9
	NodeFunctionalityEPDG  uint32 = 10
	NodeFunctionalityTWAG  uint32 = 11
	NodeFunctionalityTRGW  uint32 = 12
	NodeFunctionalityCSCF  uint32 = 13
	NodeFunctionalityECSCF uint32 = 14
)

// CCRequestTypeName returns the name of the CC-Request-Type
func CCRequestTypeName(t uint32) string {
	if name, ok := charging.CCRequestTypeNames[t]; ok {
		return name
	}
	return "UNKNOWN"
}

// EventTypeName returns the name of the IMS event type
func EventTypeName(t uint32) string {
	names := map[uint32]string{
		EventTypeSIPSessionInvite:    "SIP_SESSION_INVITE",
		EventTypeSIPSessionTerminate: "SIP_SESSION_TERMINATE",
		EventTypeSIPRegister:         "SIP_REGISTER",
		EventTypeSIPSubscribe:        "SIP_SUBSCRIBE",
		EventTypeSIPNotify:           "SIP_NOTIFY",
		EventTypeSIPRefer:            "SIP_REFER",
		EventTypeSIPMessage:          "SIP_MESSAGE",
		EventTypeSIPPublish:          "SIP_PUBLISH",
		EventTypeSIPOption:           "SIP_OPTION",
		EventTypeSIPSessionProgress:  "SIP_SESSION_PROGRESS",
	}
	if name, ok := names[t]; ok {
		return name
	}
	return "UNKNOWN"
}
