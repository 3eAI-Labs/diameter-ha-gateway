// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import "time"

// =============================================================================
// Credit Control Constants (RFC 4006)
// =============================================================================

// CC-Request-Type values
const (
	CCRequestTypeInitial     uint32 = 1
	CCRequestTypeUpdate      uint32 = 2
	CCRequestTypeTermination uint32 = 3
	CCRequestTypeEvent       uint32 = 4
)

// CC-Request-Type names for logging
var CCRequestTypeNames = map[uint32]string{
	CCRequestTypeInitial:     "INITIAL_REQUEST",
	CCRequestTypeUpdate:      "UPDATE_REQUEST",
	CCRequestTypeTermination: "TERMINATION_REQUEST",
	CCRequestTypeEvent:       "EVENT_REQUEST",
}

// Requested-Action values
const (
	RequestedActionDirectDebiting uint32 = 0
	RequestedActionRefundAccount  uint32 = 1
	RequestedActionCheckBalance   uint32 = 2
	RequestedActionPriceEnquiry   uint32 = 3
)

// Termination-Cause values
const (
	TermCauseDiameterLogout     uint32 = 1
	TermCauseServiceNotProvided uint32 = 2
	TermCauseBadAnswer          uint32 = 3
	TermCauseAdministrative     uint32 = 4
	TermCauseLinkBroken         uint32 = 5
	TermCauseAuthExpired        uint32 = 6
	TermCauseUserMoved          uint32 = 7
	TermCauseSessionTimeout     uint32 = 8
)

// Final-Unit-Action values
const (
	FinalUnitActionTerminate uint32 = 0
	FinalUnitActionRedirect  uint32 = 1
	FinalUnitActionRestrict  uint32 = 2
)

// Redirect-Address-Type values
const (
	RedirectAddressTypeIPv4   uint32 = 0
	RedirectAddressTypeIPv6   uint32 = 1
	RedirectAddressTypeURL    uint32 = 2
	RedirectAddressTypeSIPURI uint32 = 3
)

// Multiple-Services-Indicator values
const (
	MSINotSupported uint32 = 0
	MSISupported    uint32 = 1
)

// Reporting-Reason values
const (
	ReportingReasonThreshold        uint32 = 0
	ReportingReasonQHTExpiry        uint32 = 1
	ReportingReasonFinalReport      uint32 = 2
	ReportingReasonQuotaExhausted   uint32 = 3
	ReportingReasonValidityTime     uint32 = 4
	ReportingReasonOtherQuotaType   uint32 = 5
	ReportingReasonRatingCondition  uint32 = 6
	ReportingReasonForcedReauth     uint32 = 7
	ReportingReasonPoolExhausted    uint32 = 8
	ReportingReasonUnusedQuotaTimer uint32 = 9
)

// =============================================================================
// Service-Context-ID Constants (3GPP)
// =============================================================================

const (
	ServiceContextIDGy = "32251@3gpp.org" // PS Data (Gy)
	ServiceContextIDRo = "32260@3gpp.org" // IMS (Ro)
)

// =============================================================================
// Application IDs
// =============================================================================

const (
	AppIDCreditControl uint32 = 4        // RFC 4006 Credit-Control
	AppIDGx            uint32 = 16777238 // 3GPP Gx
	VendorID3GPP       uint32 = 10415    // 3GPP Vendor ID
)

// =============================================================================
// Common Header Structure
// =============================================================================

// DiameterHeader contains common Diameter message header fields
type DiameterHeader struct {
	SessionID         string    `json:"session_id"`
	OriginHost        string    `json:"origin_host"`
	OriginRealm       string    `json:"origin_realm"`
	DestinationHost   string    `json:"destination_host,omitempty"`
	DestinationRealm  string    `json:"destination_realm"`
	AuthApplicationID uint32    `json:"auth_application_id"`
	HopByHop          uint32    `json:"hop_by_hop"`
	EndToEnd          uint32    `json:"end_to_end"`
	TraceID           string    `json:"trace_id"`
	Timestamp         time.Time `json:"timestamp"`
}

// =============================================================================
// Subscription Identification (RFC 4006)
// =============================================================================

// SubscriptionIDType values
const (
	SubscriptionIDTypeEndUserE164    uint32 = 0
	SubscriptionIDTypeEndUserIMSI    uint32 = 1
	SubscriptionIDTypeEndUserSIPURI  uint32 = 2
	SubscriptionIDTypeEndUserNAI     uint32 = 3
	SubscriptionIDTypeEndUserPrivate uint32 = 4
)

// SubscriptionID identifies the end user
type SubscriptionID struct {
	Type uint32 `json:"type"`
	Data string `json:"data"`
}

// UserEquipmentInfoType values
const (
	UEInfoTypeIMEISV        uint32 = 0
	UEInfoTypeMAC           uint32 = 1
	UEInfoTypeEUI64         uint32 = 2
	UEInfoTypeModifiedEUI64 uint32 = 3
)

// UserEquipmentInfo identifies the user equipment
type UserEquipmentInfo struct {
	Type  uint32 `json:"type"`
	Value []byte `json:"value"`
}

// =============================================================================
// Service Units (RFC 4006)
// =============================================================================

// ServiceUnit represents credit control units
type ServiceUnit struct {
	CCTime                 uint32   `json:"cc_time,omitempty"` // Seconds
	CCMoney                *CCMoney `json:"cc_money,omitempty"`
	CCTotalOctets          uint64   `json:"cc_total_octets,omitempty"`
	CCInputOctets          uint64   `json:"cc_input_octets,omitempty"`
	CCOutputOctets         uint64   `json:"cc_output_octets,omitempty"`
	CCServiceSpecificUnits uint64   `json:"cc_service_specific_units,omitempty"`
	TariffTimeChange       int64    `json:"tariff_time_change,omitempty"`  // Granted-Service-Unit
	TariffChangeUsage      *uint32  `json:"tariff_change_usage,omitempty"` // Used-Service-Unit; UNIT_BEFORE_TARIFF_CHANGE (0) counts
	ReportingReason        *uint32  `json:"reporting_reason,omitempty"`    // Used-Service-Unit (TS 32.299 §7.1.14); THRESHOLD (0) counts
}

// CCMoney represents monetary values
type CCMoney struct {
	UnitValue    *UnitValue `json:"unit_value"`
	CurrencyCode uint32     `json:"currency_code,omitempty"`
}

// UnitValue represents a decimal value
type UnitValue struct {
	ValueDigits int64 `json:"value_digits"`
	Exponent    int32 `json:"exponent,omitempty"`
}

// =============================================================================
// Multiple-Services-Credit-Control (MSCC)
// =============================================================================

// MSCC represents Multiple-Services-Credit-Control grouped AVP
type MSCC struct {
	GrantedServiceUnit    *ServiceUnit           `json:"granted_service_unit,omitempty"`
	RequestedServiceUnit  *ServiceUnit           `json:"requested_service_unit,omitempty"`
	UsedServiceUnit       []ServiceUnit          `json:"used_service_unit,omitempty"`
	TariffChangeUsage     *uint32                `json:"tariff_change_usage,omitempty"` // UNIT_BEFORE_TARIFF_CHANGE (0) counts
	ServiceIdentifier     uint32                 `json:"service_identifier,omitempty"`  // the first of ServiceIdentifiers
	ServiceIdentifiers    []uint32               `json:"service_identifiers,omitempty"` // *[ Service-Identifier ]
	RatingGroup           *uint32                `json:"rating_group,omitempty"`        // optional (TS 32.299 §7.1.9, §7.1.10); 0 counts
	GSUPoolReference      []GSUPoolRef           `json:"gsu_pool_reference,omitempty"`
	ValidityTime          uint32                 `json:"validity_time,omitempty"`
	ResultCode            uint32                 `json:"result_code,omitempty"`
	FinalUnitIndication   *FinalUnitIndication   `json:"final_unit_indication,omitempty"`
	TimeQuotaThreshold    uint32                 `json:"time_quota_threshold,omitempty"`
	VolumeQuotaThreshold  uint32                 `json:"volume_quota_threshold,omitempty"`
	UnitQuotaThreshold    uint32                 `json:"unit_quota_threshold,omitempty"`
	QuotaHoldingTime      *uint32                `json:"quota_holding_time,omitempty"` // 0 disables the mechanism (§7.2.160); absent, the client's default applies
	QuotaConsumptionTime  uint32                 `json:"quota_consumption_time,omitempty"`
	ReportingReason       []uint32               `json:"reporting_reason,omitempty"`
	TriggerType           *TriggerType           `json:"trigger_type,omitempty"` // the Trigger AVP; an empty list disarms every trigger (§7.2.235)
	PSFurnishChargingInfo *PSFurnishChargingInfo `json:"ps_furnish_charging_info,omitempty"`
	RefundInformation     []byte                 `json:"refund_information,omitempty"`
	AFCorrelationInfo     []AFCorrelationInfo    `json:"af_correlation_info,omitempty"`
	Envelope              []Envelope             `json:"envelope,omitempty"`
	EnvelopeReporting     *uint32                `json:"envelope_reporting,omitempty"` // DO_NOT_REPORT_ENVELOPES (0) counts
	TimeQuotaMechanism    *TimeQuotaMechanism    `json:"time_quota_mechanism,omitempty"`
	ServiceSpecificInfo   []ServiceSpecificInfo  `json:"service_specific_info,omitempty"`
	QoSInformation        *QoSInformation        `json:"qos_information,omitempty"`
}

// GSUPoolRef references a GSU pool
type GSUPoolRef struct {
	GSUPoolIdentifier uint32     `json:"gsu_pool_identifier"`
	CCUnitType        uint32     `json:"cc_unit_type,omitempty"`
	UnitValue         *UnitValue `json:"unit_value,omitempty"`
}

// FinalUnitIndication indicates final unit handling
type FinalUnitIndication struct {
	FinalUnitAction       uint32          `json:"final_unit_action"`
	RestrictionFilterRule [][]byte        `json:"restriction_filter_rule,omitempty"`
	FilterID              []string        `json:"filter_id,omitempty"`
	RedirectServer        *RedirectServer `json:"redirect_server,omitempty"`
}

// CostInformation (RFC 8506 §8.7) is the estimated cost of the requested
// service.
type CostInformation struct {
	UnitValue    *UnitValue `json:"unit_value"`
	CurrencyCode uint32     `json:"currency_code"`
	CostUnit     string     `json:"cost_unit,omitempty"`
}

// RedirectServer specifies redirect target
type RedirectServer struct {
	RedirectAddressType   uint32 `json:"redirect_address_type"`
	RedirectServerAddress string `json:"redirect_server_address"`
}

// TriggerType specifies reporting triggers
type TriggerType struct {
	TriggerType []uint32 `json:"trigger_type,omitempty"`
}

// TimeQuotaMechanism specifies time quota handling
type TimeQuotaMechanism struct {
	TimeQuotaType    uint32 `json:"time_quota_type"`
	BaseTimeInterval uint32 `json:"base_time_interval,omitempty"`
}

// Envelope for envelope reporting
type Envelope struct {
	EnvelopeStartTime      int64  `json:"envelope_start_time"`
	EnvelopeEndTime        int64  `json:"envelope_end_time,omitempty"`
	CCTotalOctets          uint64 `json:"cc_total_octets,omitempty"`
	CCInputOctets          uint64 `json:"cc_input_octets,omitempty"`
	CCOutputOctets         uint64 `json:"cc_output_octets,omitempty"`
	CCServiceSpecificUnits uint64 `json:"cc_service_specific_units,omitempty"`
}

// =============================================================================
// Service Information (TS 32.299)
// =============================================================================

// ServiceInformation contains service-specific charging info
type ServiceInformation struct {
	PSInformation    *PSInformation    `json:"ps_information,omitempty"`
	IMSInformation   *IMSInformation   `json:"ims_information,omitempty"`
	MMSInformation   *MMSInformation   `json:"mms_information,omitempty"`
	LCSInformation   *LCSInformation   `json:"lcs_information,omitempty"`
	PoCInformation   *PoCInformation   `json:"poc_information,omitempty"`
	MBMSInformation  *MBMSInformation  `json:"mbms_information,omitempty"`
	SMSInformation   *SMSInformation   `json:"sms_information,omitempty"`
	VCSInformation   *VCSInformation   `json:"vcs_information,omitempty"`
	MMTelInformation *MMTelInformation `json:"mmtel_information,omitempty"`
	ProSeInformation *ProSeInformation `json:"prose_information,omitempty"`
	CPDTInformation  *CPDTInformation  `json:"cpdt_information,omitempty"`
}

// PSInformation contains packet-switched service info (Gy)
type PSInformation struct {
	ThreeGPPChargingID                   []byte                       `json:"3gpp_charging_id,omitempty"`
	PDNConnectionChargingID              uint32                       `json:"pdn_connection_charging_id,omitempty"`
	NodeID                               string                       `json:"node_id,omitempty"`
	ThreeGPPPDPType                      uint32                       `json:"3gpp_pdp_type,omitempty"`
	PDPAddress                           []Address                    `json:"pdp_address,omitempty"`
	PDPAddressPrefixLength               uint32                       `json:"pdp_address_prefix_length,omitempty"`
	DynamicAddressFlag                   uint32                       `json:"dynamic_address_flag,omitempty"`
	DynamicAddressFlagExtension          uint32                       `json:"dynamic_address_flag_extension,omitempty"`
	QoSInformation                       *QoSInformation              `json:"qos_information,omitempty"`
	SGSNAddress                          []Address                    `json:"sgsn_address,omitempty"`
	GGSNAddress                          []Address                    `json:"ggsn_address,omitempty"`
	TDFIPAddress                         []Address                    `json:"tdf_ip_address,omitempty"`
	SGWAddress                           []Address                    `json:"sgw_address,omitempty"`
	EpdgAddress                          []Address                    `json:"epdg_address,omitempty"`
	TWAGAddress                          []Address                    `json:"twag_address,omitempty"`
	CGAddress                            *Address                     `json:"cg_address,omitempty"`
	ServingNodeType                      []uint32                     `json:"serving_node_type,omitempty"`
	SGWChange                            uint32                       `json:"sgw_change,omitempty"`
	ThreeGPPIMSIMCCMNC                   string                       `json:"3gpp_imsi_mcc_mnc,omitempty"`
	IMSIUnauthenticatedFlag              uint32                       `json:"imsi_unauthenticated_flag,omitempty"`
	ThreeGPPGGSNMCCMNC                   string                       `json:"3gpp_ggsn_mcc_mnc,omitempty"`
	ThreeGPPNSAPI                        []byte                       `json:"3gpp_nsapi,omitempty"`
	CalledStationID                      string                       `json:"called_station_id,omitempty"`
	ThreeGPPSelectionMode                string                       `json:"3gpp_selection_mode,omitempty"`
	ThreeGPPChargingCharacteristics      string                       `json:"3gpp_charging_characteristics,omitempty"`
	ChargingCharacteristicsSelectionMode uint32                       `json:"charging_characteristics_selection_mode,omitempty"`
	ThreeGPPSGSNMCCMNC                   string                       `json:"3gpp_sgsn_mcc_mnc,omitempty"`
	ThreeGPPMSTimeZone                   []byte                       `json:"3gpp_ms_timezone,omitempty"`
	ChargingRuleBaseName                 string                       `json:"charging_rule_base_name,omitempty"`
	ADCRuleBaseName                      string                       `json:"adc_rule_base_name,omitempty"`
	ThreeGPPUserLocationInfo             []byte                       `json:"3gpp_user_location_info,omitempty"`
	UserLocationInfoTime                 int64                        `json:"user_location_info_time,omitempty"`
	UserCSGInformation                   *UserCSGInformation          `json:"user_csg_information,omitempty"`
	PresenceReportingAreaInformation     []PresenceReportingAreaInfo  `json:"presence_reporting_area_info,omitempty"`
	ThreeGPP2BSID                        string                       `json:"3gpp2_bsid,omitempty"`
	TWANUserLocationInfo                 *TWANUserLocationInfo        `json:"twan_user_location_info,omitempty"`
	UWANUserLocationInfo                 *UWANUserLocationInfo        `json:"uwan_user_location_info,omitempty"`
	ThreeGPPRATType                      []byte                       `json:"3gpp_rat_type,omitempty"`
	PSFurnishChargingInformation         *PSFurnishChargingInfo       `json:"ps_furnish_charging_information,omitempty"`
	PDPContextType                       uint32                       `json:"pdp_context_type,omitempty"`
	OfflineCharging                      *OfflineCharging             `json:"offline_charging,omitempty"`
	TrafficDataVolumes                   []TrafficDataVolumes         `json:"traffic_data_volumes,omitempty"`
	ServiceDataContainer                 []ServiceDataContainer       `json:"service_data_container,omitempty"`
	UserEquipmentInfo                    *UserEquipmentInfo           `json:"user_equipment_info,omitempty"`
	TerminalInformation                  *TerminalInformation         `json:"terminal_information,omitempty"`
	StartTime                            int64                        `json:"start_time,omitempty"`
	StopTime                             int64                        `json:"stop_time,omitempty"`
	ChangeCondition                      uint32                       `json:"change_condition,omitempty"`
	Diagnostics                          uint32                       `json:"diagnostics,omitempty"`
	LowPriorityIndicator                 uint32                       `json:"low_priority_indicator,omitempty"`
	NBIFOMModeIndicator                  uint32                       `json:"nbifom_mode,omitempty"`
	NBIFOMSupport                        uint32                       `json:"nbifom_support,omitempty"`
	MMENumberForMTSMS                    []byte                       `json:"mme_number_for_mt_sms,omitempty"`
	MMERealm                             string                       `json:"mme_realm,omitempty"`
	MMEName                              string                       `json:"mme_name,omitempty"`
	LogicalAccessID                      []byte                       `json:"logical_access_id,omitempty"`
	PhysicalAccessID                     string                       `json:"physical_access_id,omitempty"`
	FixedUserLocationInfo                *FixedUserLocationInfo       `json:"fixed_user_location_info,omitempty"`
	CNOperatorSelectionEntity            uint32                       `json:"cn_operator_selection_entity,omitempty"`
	EnhancedDiagnostics                  *EnhancedDiagnostics         `json:"enhanced_diagnostics,omitempty"`
	SGiPtPTunnellingMethod               uint32                       `json:"sgi_ptp_tunnelling_method,omitempty"`
	CPCIoTEPSOptimisationIndicator       uint32                       `json:"cp_ciot_eps_optimisation_indicator,omitempty"`
	UNIPDUCPOnlyFlag                     uint32                       `json:"uni_pdu_cp_only_flag,omitempty"`
	ServingPLMNRateControl               *ServingPLMNRateControl      `json:"serving_plmn_rate_control,omitempty"`
	APNRateControl                       *APNRateControl              `json:"apn_rate_control,omitempty"`
	ChargingPerIPCANSessionIndicator     uint32                       `json:"charging_per_ip_can_session_indicator,omitempty"`
	RRCCauseCounter                      *RRCCauseCounter             `json:"rrc_cause_counter,omitempty"`
	ThreeGPPPSDataOffStatus              uint32                       `json:"3gpp_ps_data_off_status,omitempty"`
	SCSASAddress                         *Address                     `json:"scs_as_address,omitempty"`
	UnusedQuotaTimer                     uint32                       `json:"unused_quota_timer,omitempty"`
	RANSecondaryRATUsageReport           []RANSecondaryRATUsageReport `json:"ran_secondary_rat_usage_report,omitempty"`
}

// Address represents an IP address
type Address struct {
	Type    uint16 `json:"type"` // 1=IPv4, 2=IPv6
	Address []byte `json:"address"`
}

// QoSInformation contains QoS parameters
type QoSInformation struct {
	QoSClassIdentifier                uint32                        `json:"qos_class_identifier,omitempty"`
	MaxRequestedBandwidthUL           uint32                        `json:"max_requested_bandwidth_ul,omitempty"`
	MaxRequestedBandwidthDL           uint32                        `json:"max_requested_bandwidth_dl,omitempty"`
	GuaranteedBitrateUL               uint32                        `json:"guaranteed_bitrate_ul,omitempty"`
	GuaranteedBitrateDL               uint32                        `json:"guaranteed_bitrate_dl,omitempty"`
	BearerIdentifier                  []byte                        `json:"bearer_identifier,omitempty"`
	AllocationRetentionPriority       *ARP                          `json:"allocation_retention_priority,omitempty"`
	APNAggregateMaxBitrateUL          uint32                        `json:"apn_aggregate_max_bitrate_ul,omitempty"`
	APNAggregateMaxBitrateDL          uint32                        `json:"apn_aggregate_max_bitrate_dl,omitempty"`
	ConditionalAPNAggregateMaxBitrate []ConditionalAPNAggMaxBitrate `json:"conditional_apn_aggregate_max_bitrate,omitempty"`
}

// ARP represents Allocation-Retention-Priority
type ARP struct {
	PriorityLevel           uint32 `json:"priority_level"`
	PreemptionCapability    uint32 `json:"pre_emption_capability,omitempty"`
	PreemptionVulnerability uint32 `json:"pre_emption_vulnerability,omitempty"`
}

// ConditionalAPNAggMaxBitrate for conditional APN bitrates
type ConditionalAPNAggMaxBitrate struct {
	APNAggregateMaxBitrateUL uint32   `json:"apn_aggregate_max_bitrate_ul,omitempty"`
	APNAggregateMaxBitrateDL uint32   `json:"apn_aggregate_max_bitrate_dl,omitempty"`
	IPCANType                []uint32 `json:"ip_can_type,omitempty"`
	RATType                  []uint32 `json:"rat_type,omitempty"`
}

// TerminalInformation contains terminal info
type TerminalInformation struct {
	IMEI            string `json:"imei,omitempty"`
	SoftwareVersion string `json:"software_version,omitempty"`
}

// =============================================================================
// Additional PS Information Types
// =============================================================================

type UserCSGInformation struct {
	CSGID                   uint32 `json:"csg_id,omitempty"`
	CSGAccessMode           uint32 `json:"csg_access_mode,omitempty"`
	CSGMembershipIndication uint32 `json:"csg_membership_indication,omitempty"`
}

type PresenceReportingAreaInfo struct {
	PresenceReportingAreaIdentifier  []byte                          `json:"presence_reporting_area_identifier,omitempty"`
	PresenceReportingAreaStatus      uint32                          `json:"presence_reporting_area_status,omitempty"`
	PresenceReportingAreaElementList []PresenceReportingAreaElements `json:"presence_reporting_area_elements_list,omitempty"`
	PresenceReportingAreaNode        uint32                          `json:"presence_reporting_area_node,omitempty"`
}

type PresenceReportingAreaElements struct {
	// Elements as per 3GPP
}

type TWANUserLocationInfo struct {
	SSID             string `json:"ssid,omitempty"`
	BSSID            string `json:"bssid,omitempty"`
	CivicAddress     []byte `json:"civic_address,omitempty"`
	TWANOperatorName []byte `json:"twan_operator_name,omitempty"`
	LogicalAccessID  []byte `json:"logical_access_id,omitempty"`
}

type UWANUserLocationInfo struct {
	UELocalIPAddress *Address `json:"ue_local_ip_address,omitempty"`
	UDPSourcePort    uint32   `json:"udp_source_port,omitempty"`
	SSID             string   `json:"ssid,omitempty"`
	BSSID            string   `json:"bssid,omitempty"`
}

type PSFurnishChargingInfo struct {
	ThreeGPPChargingID     []byte  `json:"3gpp_charging_id,omitempty"`
	PSFreeFormatData       []byte  `json:"ps_free_format_data,omitempty"`
	PSAppendFreeFormatData *uint32 `json:"ps_append_free_format_data,omitempty"` // 'Append' (0); absent, the P-GW overwrites (TS 32.299 §7.2.155)
}

type OfflineCharging struct {
	QuotaConsumptionTime          uint32              `json:"quota_consumption_time,omitempty"`
	TimeQuotaMechanism            *TimeQuotaMechanism `json:"time_quota_mechanism,omitempty"`
	EnvelopeReporting             uint32              `json:"envelope_reporting,omitempty"`
	MultipleServicesCreditControl []MSCC              `json:"multiple_services_credit_control,omitempty"`
}

type TrafficDataVolumes struct {
	QoSInformation                 *QoSInformation             `json:"qos_information,omitempty"`
	AccountingInputOctets          uint64                      `json:"accounting_input_octets,omitempty"`
	AccountingInputPackets         uint64                      `json:"accounting_input_packets,omitempty"`
	AccountingOutputOctets         uint64                      `json:"accounting_output_octets,omitempty"`
	AccountingOutputPackets        uint64                      `json:"accounting_output_packets,omitempty"`
	ChangeCondition                uint32                      `json:"change_condition,omitempty"`
	ChangeTime                     int64                       `json:"change_time,omitempty"`
	ThreeGPPUserLocationInfo       []byte                      `json:"3gpp_user_location_info,omitempty"`
	UWANUserLocationInfo           *UWANUserLocationInfo       `json:"uwan_user_location_info,omitempty"`
	ThreeGPPChargingID             []byte                      `json:"3gpp_charging_id,omitempty"`
	PresenceReportingAreaStatus    uint32                      `json:"presence_reporting_area_status,omitempty"`
	PresenceReportingAreaInfo      []PresenceReportingAreaInfo `json:"presence_reporting_area_info,omitempty"`
	UserCSGInformation             *UserCSGInformation         `json:"user_csg_information,omitempty"`
	ThreeGPPRATType                []byte                      `json:"3gpp_rat_type,omitempty"`
	AccessAvailabilityChangeReason uint32                      `json:"access_availability_change_reason,omitempty"`
	RelatedChangeConditionInfo     *RelatedChangeConditionInfo `json:"related_change_condition_info,omitempty"`
	Diagnostics                    uint32                      `json:"diagnostics,omitempty"`
	EnhancedDiagnostics            *EnhancedDiagnostics        `json:"enhanced_diagnostics,omitempty"`
	CPCIoTEPSOptimisationIndicator uint32                      `json:"cp_ciot_eps_optimisation_indicator,omitempty"`
	ServingPLMNRateControl         *ServingPLMNRateControl     `json:"serving_plmn_rate_control,omitempty"`
	APNRateControl                 *APNRateControl             `json:"apn_rate_control,omitempty"`
}

type ServiceDataContainer struct {
	AFCorrelationInfo                  []AFCorrelationInfo         `json:"af_correlation_info,omitempty"`
	ChargingRuleBaseName               string                      `json:"charging_rule_base_name,omitempty"`
	AccountingInputOctets              uint64                      `json:"accounting_input_octets,omitempty"`
	AccountingInputPackets             uint64                      `json:"accounting_input_packets,omitempty"`
	AccountingOutputOctets             uint64                      `json:"accounting_output_octets,omitempty"`
	AccountingOutputPackets            uint64                      `json:"accounting_output_packets,omitempty"`
	LocalSequenceNumber                uint32                      `json:"local_sequence_number,omitempty"`
	QoSInformation                     *QoSInformation             `json:"qos_information,omitempty"`
	RatingGroup                        uint32                      `json:"rating_group,omitempty"`
	ChangeTime                         int64                       `json:"change_time,omitempty"`
	ServiceIdentifier                  uint32                      `json:"service_identifier,omitempty"`
	ServiceSpecificInfo                []ServiceSpecificInfo       `json:"service_specific_info,omitempty"`
	ADCRuleBaseName                    string                      `json:"adc_rule_base_name,omitempty"`
	SGSNAddress                        *Address                    `json:"sgsn_address,omitempty"`
	TimeFirstUsage                     int64                       `json:"time_first_usage,omitempty"`
	TimeLastUsage                      int64                       `json:"time_last_usage,omitempty"`
	TimeUsage                          uint32                      `json:"time_usage,omitempty"`
	ChangeCondition                    []uint32                    `json:"change_condition,omitempty"`
	ThreeGPPUserLocationInfo           []byte                      `json:"3gpp_user_location_info,omitempty"`
	ThreeGPP2BSID                      string                      `json:"3gpp2_bsid,omitempty"`
	UWANUserLocationInfo               *UWANUserLocationInfo       `json:"uwan_user_location_info,omitempty"`
	SponsorIdentity                    string                      `json:"sponsor_identity,omitempty"`
	ApplicationServiceProviderIdentity string                      `json:"application_service_provider_identity,omitempty"`
	PresenceReportingAreaInfo          []PresenceReportingAreaInfo `json:"presence_reporting_area_info,omitempty"`
	PresenceReportingAreaStatus        uint32                      `json:"presence_reporting_area_status,omitempty"`
	UserCSGInformation                 *UserCSGInformation         `json:"user_csg_information,omitempty"`
	ThreeGPPRATType                    []byte                      `json:"3gpp_rat_type,omitempty"`
	RelatedChangeConditionInfo         *RelatedChangeConditionInfo `json:"related_change_condition_info,omitempty"`
	ServingPLMNRateControl             *ServingPLMNRateControl     `json:"serving_plmn_rate_control,omitempty"`
	APNRateControl                     *APNRateControl             `json:"apn_rate_control,omitempty"`
	ThreeGPPPSDataOffStatus            uint32                      `json:"3gpp_ps_data_off_status,omitempty"`
	TrafficSteeringPolicyIdentifierDL  []byte                      `json:"traffic_steering_policy_identifier_dl,omitempty"`
	TrafficSteeringPolicyIdentifierUL  []byte                      `json:"traffic_steering_policy_identifier_ul,omitempty"`
	VoLTEInformation                   *VoLTEInformation           `json:"volte_information,omitempty"`
}

type AFCorrelationInfo struct {
	AFChargingIdentifier []byte  `json:"af_charging_identifier,omitempty"`
	Flows                []Flows `json:"flows,omitempty"`
}

type Flows struct {
	MediaComponentNumber uint32   `json:"media_component_number,omitempty"`
	FlowNumber           []uint32 `json:"flow_number,omitempty"`
	ContentVersion       uint64   `json:"content_version,omitempty"`
	FinalUnitAction      uint32   `json:"final_unit_action,omitempty"`
	MediaComponentStatus uint32   `json:"media_component_status,omitempty"`
}

type ServiceSpecificInfo struct {
	ServiceSpecificData string `json:"service_specific_data,omitempty"`
	ServiceSpecificType uint32 `json:"service_specific_type,omitempty"`
}

type RelatedChangeConditionInfo struct {
	SGSNAddress                 *Address              `json:"sgsn_address,omitempty"`
	ChangeCondition             []uint32              `json:"change_condition,omitempty"`
	ThreeGPPUserLocationInfo    []byte                `json:"3gpp_user_location_info,omitempty"`
	ThreeGPP2BSID               string                `json:"3gpp2_bsid,omitempty"`
	UWANUserLocationInfo        *UWANUserLocationInfo `json:"uwan_user_location_info,omitempty"`
	PresenceReportingAreaStatus uint32                `json:"presence_reporting_area_status,omitempty"`
	UserCSGInformation          *UserCSGInformation   `json:"user_csg_information,omitempty"`
	ThreeGPPRATType             []byte                `json:"3gpp_rat_type,omitempty"`
}

type FixedUserLocationInfo struct {
	SSID             string `json:"ssid,omitempty"`
	BSSID            string `json:"bssid,omitempty"`
	LogicalAccessID  []byte `json:"logical_access_id,omitempty"`
	PhysicalAccessID string `json:"physical_access_id,omitempty"`
}

type EnhancedDiagnostics struct {
	RANNASReleaseCause [][]byte `json:"ran_nas_release_cause,omitempty"`
}

type ServingPLMNRateControl struct {
	UplinkRateLimit   uint32 `json:"uplink_rate_limit,omitempty"`
	DownlinkRateLimit uint32 `json:"downlink_rate_limit,omitempty"`
}

type APNRateControl struct {
	APNRateControlUplink   *APNRateControlParams `json:"apn_rate_control_uplink,omitempty"`
	APNRateControlDownlink *APNRateControlParams `json:"apn_rate_control_downlink,omitempty"`
}

type APNRateControlParams struct {
	AdditionalExceptionReports uint32 `json:"additional_exception_reports,omitempty"`
	RateControlTimeUnit        uint32 `json:"rate_control_time_unit,omitempty"`
	RateControlMaxRate         uint32 `json:"rate_control_max_rate,omitempty"`
	RateControlMaxMessageSize  uint32 `json:"rate_control_max_message_size,omitempty"`
}

type RRCCauseCounter struct {
	CounterValue        uint32 `json:"counter_value,omitempty"`
	RRCCounterTimestamp int64  `json:"rrc_counter_timestamp,omitempty"`
}

type RANSecondaryRATUsageReport struct {
	SecondaryRATType           []byte `json:"secondary_rat_type,omitempty"`
	RANStartTimestamp          int64  `json:"ran_start_timestamp,omitempty"`
	RANEndTimestamp            int64  `json:"ran_end_timestamp,omitempty"`
	AccountingInputDataVolume  uint64 `json:"accounting_input_data_volume,omitempty"`
	AccountingOutputDataVolume uint64 `json:"accounting_output_data_volume,omitempty"`
	ThreeGPPChargingID         []byte `json:"3gpp_charging_id,omitempty"`
}

type VoLTEInformation struct {
	CallingPartyAddress []string `json:"calling_party_address,omitempty"`
	CalledPartyAddress  string   `json:"called_party_address,omitempty"`
}

// =============================================================================
// Failed AVP
// =============================================================================

// FailedAVP represents a failed AVP
type FailedAVP struct {
	Code     uint32 `json:"code"`
	VendorID uint32 `json:"vendor_id,omitempty"`
	Data     []byte `json:"data,omitempty"`
}

// =============================================================================
// IMS Information (Ro Interface - Placeholder)
// =============================================================================

// IMSInformation contains IMS-specific charging info (used by Ro)
type IMSInformation struct {
	// Full implementation in ro/types.go
	EventType             *EventType `json:"event_type,omitempty"`
	RoleOfNode            uint32     `json:"role_of_node,omitempty"`
	NodeFunctionality     uint32     `json:"node_functionality,omitempty"`
	UserSessionID         string     `json:"user_session_id,omitempty"`
	CallingPartyAddress   []string   `json:"calling_party_address,omitempty"`
	CalledPartyAddress    string     `json:"called_party_address,omitempty"`
	IMSChargingIdentifier string     `json:"ims_charging_identifier,omitempty"`
}

type EventType struct {
	SIPMethod string `json:"sip_method,omitempty"`
	Event     string `json:"event,omitempty"`
	Expires   uint32 `json:"expires,omitempty"`
}

// Placeholder types for other service information
type MMSInformation struct{}
type LCSInformation struct{}
type PoCInformation struct{}
type MBMSInformation struct{}
type SMSInformation struct{}
type VCSInformation struct{}
type MMTelInformation struct{}
type ProSeInformation struct{}
type CPDTInformation struct{}
