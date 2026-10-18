// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gy

import "github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"

// GyEvent represents a Credit-Control-Request event for Gy interface
// Full RFC 4006 + TS 32.299 compliance
type GyEvent struct {
	// Header
	Header charging.DiameterHeader `json:"header"`

	// Credit Control (RFC 4006)
	CCRequestType      uint32 `json:"cc_request_type"`
	CCRequestNumber    uint32 `json:"cc_request_number"`
	CCSubSessionID     uint64 `json:"cc_sub_session_id,omitempty"`
	AcctMultiSessionID string `json:"acct_multi_session_id,omitempty"`
	OriginStateID      uint32 `json:"origin_state_id,omitempty"`
	EventTimestamp     int64  `json:"event_timestamp,omitempty"`

	// Subscription (RFC 4006)
	SubscriptionID    []charging.SubscriptionID   `json:"subscription_id,omitempty"`
	UserEquipmentInfo *charging.UserEquipmentInfo `json:"user_equipment_info,omitempty"`

	// Service (RFC 4006)
	ServiceContextID  string `json:"service_context_id,omitempty"`
	ServiceIdentifier uint32 `json:"service_identifier,omitempty"`
	RequestedAction   uint32 `json:"requested_action,omitempty"`

	// Credit Control Units (RFC 4006)
	RequestedServiceUnit      *charging.ServiceUnit `json:"requested_service_unit,omitempty"`
	UsedServiceUnit           *charging.ServiceUnit `json:"used_service_unit,omitempty"`
	MultipleServicesIndicator uint32                `json:"multiple_services_indicator,omitempty"`
	MSCC                      []charging.MSCC       `json:"mscc,omitempty"`

	// Termination (RFC 4006)
	TerminationCause uint32 `json:"termination_cause,omitempty"`

	// 3GPP Specific (TS 32.299)
	ServiceInformation *charging.ServiceInformation `json:"service_information,omitempty"`

	// Additional fields for routing/debugging
	RoutingInfo *RoutingInfo `json:"routing_info,omitempty"`
}

// RoutingInfo contains additional routing information
type RoutingInfo struct {
	CallReferenceNumber string `json:"call_reference_number,omitempty"`
	PDNConnectionID     uint64 `json:"pdn_connection_id,omitempty"`
	BearerIdentifier    []byte `json:"bearer_identifier,omitempty"`
}

// GyAnswer represents a Credit-Control-Answer for Gy interface
type GyAnswer struct {
	ResultCode        uint32  `json:"result_code"`
	CCRequestType     uint32  `json:"cc_request_type"`
	CCRequestNumber   uint32  `json:"cc_request_number"`
	OriginHost        string  `json:"origin_host,omitempty"`
	OriginRealm       string  `json:"origin_realm,omitempty"`
	CCSessionFailover *uint32 `json:"cc_session_failover,omitempty"` // FAILOVER_NOT_SUPPORTED (0) counts

	// Credit Control
	MSCC []charging.MSCC `json:"mscc,omitempty"`

	// Validity
	ValidityTime uint32 `json:"validity_time,omitempty"`

	// Final Unit
	FinalUnitIndication *charging.FinalUnitIndication `json:"final_unit_indication,omitempty"`

	// Redirect
	RedirectHost         []string `json:"redirect_host,omitempty"`
	RedirectHostUsage    uint32   `json:"redirect_host_usage,omitempty"`
	RedirectMaxCacheTime uint32   `json:"redirect_max_cache_time,omitempty"`

	// Error info
	FailedAVP          []charging.FailedAVP `json:"failed_avp,omitempty"`
	ErrorMessage       string               `json:"error_message,omitempty"`
	ErrorReportingHost string               `json:"error_reporting_host,omitempty"`

	// 3GPP Specific
	LowBalanceIndication *uint32                   `json:"low_balance_indication,omitempty"` // NOT-APPLICABLE (0) counts
	CostInformation      *charging.CostInformation `json:"cost_information,omitempty"`
	RemainingBalance     *charging.CCMoney         `json:"remaining_balance,omitempty"`

	// Service Information (for reverse path responses)
	ServiceInformation *charging.ServiceInformation `json:"service_information,omitempty"`
}

// CCRequestTypeName returns the name of the CC-Request-Type
func CCRequestTypeName(t uint32) string {
	if name, ok := charging.CCRequestTypeNames[t]; ok {
		return name
	}
	return "UNKNOWN"
}
