// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"encoding/json"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTranslator(t *testing.T) {
	tr := NewTranslator()
	assert.NotNil(t, tr)
}

func TestToCCREvent_BasicFields(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	event, err := tr.ToCCREvent(m, "gx-trace-1")
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, "gx-trace-1", event.Header.TraceID)
	assert.Equal(t, uint32(charging.CCRequestTypeInitial), event.CCRequestType)
	assert.Equal(t, uint32(0), event.CCRequestNumber)
	assert.Equal(t, "gx-session-123", event.Header.SessionID)
	assert.Equal(t, "pgw.example.com", event.Header.OriginHost)
}

func TestToCCREvent_IPCANType(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	// Add IP-CAN-Type (3GPP EPS)
	m.NewAVP(1027, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(IPCANType3GPP_EPS))

	event, err := tr.ToCCREvent(m, "trace-2")
	require.NoError(t, err)

	assert.Equal(t, uint32(IPCANType3GPP_EPS), event.IPCANType)
}

func TestToCCREvent_RATType(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	// Add RAT-Type (E-UTRAN = 1004)
	m.NewAVP(1032, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004))

	event, err := tr.ToCCREvent(m, "trace-3")
	require.NoError(t, err)

	assert.Equal(t, uint32(1004), event.RATType)
}

func TestToCCREvent_FramedIPAddress(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	// Add Framed-IP-Address
	ipAddr := []byte{192, 168, 1, 100}
	m.NewAVP(8, avp.Mbit, 0, datatype.OctetString(ipAddr))

	event, err := tr.ToCCREvent(m, "trace-4")
	require.NoError(t, err)

	assert.Equal(t, ipAddr, event.FramedIPAddress)
}

func TestToCCREvent_QoSInformation(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	// Add QoS-Information
	qosGroup := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(1028, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(9)),        // QCI
			diam.NewAVP(516, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(100000000)), // Max-Bandwidth-UL
			diam.NewAVP(515, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(300000000)), // Max-Bandwidth-DL
		},
	}
	m.NewAVP(1016, avp.Mbit|avp.Vbit, 10415, qosGroup)

	event, err := tr.ToCCREvent(m, "trace-5")
	require.NoError(t, err)

	require.NotNil(t, event.QoSInformation)
	assert.Equal(t, uint32(9), event.QoSInformation.QoSClassIdentifier)
	assert.Equal(t, uint32(100000000), event.QoSInformation.MaxRequestedBandwidthUL)
	assert.Equal(t, uint32(300000000), event.QoSInformation.MaxRequestedBandwidthDL)
}

func TestToCCREvent_EventTriggers(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeUpdate, 1)

	// Add Event-Trigger AVPs
	m.NewAVP(1006, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(EventTriggerRATTypeChange))
	m.NewAVP(1006, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(EventTriggerUserLocationChange))

	event, err := tr.ToCCREvent(m, "trace-6")
	require.NoError(t, err)

	require.Len(t, event.EventTrigger, 2)
	assert.Contains(t, event.EventTrigger, EventTriggerRATTypeChange)
	assert.Contains(t, event.EventTrigger, EventTriggerUserLocationChange)
}

func TestToCCREvent_UsageMonitoringInfo(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeUpdate, 1)

	// Add Usage-Monitoring-Information
	umiGroup := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(1066, avp.Mbit|avp.Vbit, 10415, datatype.OctetString("key1")), // Monitoring-Key
		},
	}
	m.NewAVP(1067, avp.Mbit|avp.Vbit, 10415, umiGroup)

	event, err := tr.ToCCREvent(m, "trace-7")
	require.NoError(t, err)

	require.Len(t, event.UsageMonitoringInformation, 1)
	assert.Equal(t, []byte("key1"), event.UsageMonitoringInformation[0].MonitoringKey)
}

func TestToCCREvent_CalledStationID(t *testing.T) {
	tr := NewTranslator()
	m := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	// Add Called-Station-Id (APN)
	m.NewAVP(30, avp.Mbit, 0, datatype.UTF8String("internet"))

	event, err := tr.ToCCREvent(m, "trace-8")
	require.NoError(t, err)

	assert.Equal(t, "internet", event.CalledStationID)
}

func TestToCCAMessage_Success(t *testing.T) {
	tr := NewTranslator()
	req := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	answer := &GxAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		OriginHost:      "pcrf.example.com",
		OriginRealm:     "example.com",
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)
	require.NotNil(t, cca)

	// Verify it's an answer
	assert.False(t, cca.Header.CommandFlags&diam.RequestFlag != 0)
}

func TestToCCAMessage_WithChargingRules(t *testing.T) {
	tr := NewTranslator()
	req := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	answer := &GxAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		ChargingRuleInstall: []ChargingRuleInstall{
			{
				ChargingRuleName: []string{"rule1", "rule2"},
			},
		},
		EventTrigger: []uint32{EventTriggerRATTypeChange, EventTriggerUserLocationChange},
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)

	// Find Charging-Rule-Install AVP
	var foundCRI bool
	for _, a := range cca.AVP {
		if a.Code == 1001 && a.VendorID == 10415 {
			foundCRI = true
			break
		}
	}
	assert.True(t, foundCRI, "Charging-Rule-Install AVP should be present")

	// Find Event-Trigger AVPs
	var etCount int
	for _, a := range cca.AVP {
		if a.Code == 1006 && a.VendorID == 10415 {
			etCount++
		}
	}
	assert.Equal(t, 2, etCount, "Should have 2 Event-Trigger AVPs")
}

func TestToCCAMessage_WithQoS(t *testing.T) {
	tr := NewTranslator()
	req := buildTestGxCCR(t, charging.CCRequestTypeInitial, 0)

	answer := &GxAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		QoSInformation: &QoSInformation{
			QoSClassIdentifier:      9,
			MaxRequestedBandwidthUL: 100000000,
			MaxRequestedBandwidthDL: 300000000,
		},
		DefaultEPSBearerQoS: &DefaultEPSBearerQoS{
			QoSClassIdentifier: 9,
		},
		APNAMBR: &APNAMBR{
			APNAggregateMaxBitrateUL: 500000000,
			APNAggregateMaxBitrateDL: 1000000000,
		},
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)

	// Find QoS-Information AVP
	var foundQoS bool
	for _, a := range cca.AVP {
		if a.Code == 1016 && a.VendorID == 10415 {
			foundQoS = true
			break
		}
	}
	assert.True(t, foundQoS, "QoS-Information AVP should be present")
}

func TestToJSONAnswer(t *testing.T) {
	tr := NewTranslator()

	jsonData := `{
		"result_code": 2001,
		"cc_request_type": 1,
		"cc_request_number": 0,
		"origin_host": "pcrf.example.com",
		"charging_rule_install": [
			{
				"charging_rule_name": ["default_rule"]
			}
		],
		"event_trigger": [12, 13],
		"qos_information": {
			"qos_class_identifier": 9,
			"max_requested_bandwidth_ul": 100000000,
			"max_requested_bandwidth_dl": 300000000
		}
	}`

	answer, err := tr.ToJSONAnswer([]byte(jsonData))
	require.NoError(t, err)

	assert.Equal(t, uint32(2001), answer.ResultCode)
	assert.Equal(t, "pcrf.example.com", answer.OriginHost)
	require.Len(t, answer.ChargingRuleInstall, 1)
	assert.Equal(t, []string{"default_rule"}, answer.ChargingRuleInstall[0].ChargingRuleName)
	require.Len(t, answer.EventTrigger, 2)
	require.NotNil(t, answer.QoSInformation)
	assert.Equal(t, uint32(9), answer.QoSInformation.QoSClassIdentifier)
}

func TestToJSONAnswer_InvalidJSON(t *testing.T) {
	tr := NewTranslator()
	_, err := tr.ToJSONAnswer([]byte("invalid json"))
	assert.Error(t, err)
}

func TestMaskIMSI(t *testing.T) {
	tr := NewTranslator()

	tests := []struct {
		name     string
		imsi     string
		expected string
	}{
		{"full IMSI", "262021234567890", "262021******90"},
		{"short IMSI", "26202", "****"},
		{"empty IMSI", "", "****"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			masked := tr.MaskIMSI(tc.imsi)
			assert.Equal(t, tc.expected, masked)
		})
	}
}

func TestCCRequestTypeName(t *testing.T) {
	tests := []struct {
		reqType  uint32
		expected string
	}{
		{charging.CCRequestTypeInitial, "INITIAL_REQUEST"},
		{charging.CCRequestTypeUpdate, "UPDATE_REQUEST"},
		{charging.CCRequestTypeTermination, "TERMINATION_REQUEST"},
		{99, "UNKNOWN"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			name := CCRequestTypeName(tc.reqType)
			assert.Equal(t, tc.expected, name)
		})
	}
}

func TestEventTriggerConstants(t *testing.T) {
	// Verify key event trigger constants match TS 29.212
	assert.Equal(t, uint32(0), EventTriggerSGSNChange)
	assert.Equal(t, uint32(1), EventTriggerQoSChange)
	assert.Equal(t, uint32(2), EventTriggerRATChange)
	assert.Equal(t, uint32(12), EventTriggerRATTypeChange)
	assert.Equal(t, uint32(13), EventTriggerUserLocationChange)
	assert.Equal(t, uint32(33), EventTriggerUsageReport)
}

func TestIPCANTypeConstants(t *testing.T) {
	assert.Equal(t, uint32(0), IPCANType3GPP_GPRS)
	assert.Equal(t, uint32(5), IPCANType3GPP_EPS)
	assert.Equal(t, uint32(9), IPCANType3GPP_5GS)
}

func TestHexEncode(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0xAB, 0xCD}
	encoded := HexEncode(data)
	assert.Equal(t, "010203abcd", encoded)
}

// =============================================================================
// Helper Functions
// =============================================================================

func buildTestGxCCR(t *testing.T, reqType uint32, reqNum uint32) *diam.Message {
	// Use Gx Application-ID (16777238)
	m := diam.NewMessage(272, diam.RequestFlag, AppIDGx, 0x12345678, 0x87654321, nil)

	// Session-Id
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("gx-session-123"))
	// Origin-Host
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.example.com"))
	// Origin-Realm
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	// Destination-Realm
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("pcrf.example.com"))

	// CC-Request-Type
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(reqType))
	// CC-Request-Number
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(reqNum))

	return m
}

// Benchmark tests
func BenchmarkToCCREvent(b *testing.B) {
	tr := NewTranslator()
	m := diam.NewMessage(272, diam.RequestFlag, AppIDGx, 0x12345678, 0x87654321, nil)

	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("bench-session"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.example.com"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))
	m.NewAVP(1027, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(5))    // IP-CAN-Type
	m.NewAVP(1032, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004)) // RAT-Type

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToCCREvent(m, "bench-trace")
	}
}

func BenchmarkToCCAMessage(b *testing.B) {
	tr := NewTranslator()
	req := diam.NewMessage(272, diam.RequestFlag, AppIDGx, 0x12345678, 0x87654321, nil)

	answer := &GxAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   1,
		CCRequestNumber: 0,
		ChargingRuleInstall: []ChargingRuleInstall{
			{
				ChargingRuleName: []string{"default_rule"},
			},
		},
		EventTrigger: []uint32{12, 13},
		QoSInformation: &QoSInformation{
			QoSClassIdentifier:      9,
			MaxRequestedBandwidthUL: 100000000,
			MaxRequestedBandwidthDL: 300000000,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToCCAMessage(answer, req)
	}
}

func BenchmarkToJSONAnswer(b *testing.B) {
	tr := NewTranslator()
	jsonData, _ := json.Marshal(&GxAnswer{
		ResultCode:      2001,
		CCRequestType:   1,
		CCRequestNumber: 0,
		ChargingRuleInstall: []ChargingRuleInstall{
			{
				ChargingRuleName: []string{"default_rule"},
			},
		},
		EventTrigger: []uint32{12, 13},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToJSONAnswer(jsonData)
	}
}
