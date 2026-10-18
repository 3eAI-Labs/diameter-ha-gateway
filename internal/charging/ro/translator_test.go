// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package ro

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTranslator(t *testing.T) {
	tr := NewTranslator()
	assert.NotNil(t, tr)
}

func TestToCCREvent_BasicFields(t *testing.T) {
	tr := NewTranslator()
	m := buildTestRoCCR(t, charging.CCRequestTypeEvent, 0)

	event, err := tr.ToCCREvent(m, "ro-trace-1")
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, "ro-trace-1", event.Header.TraceID)
	assert.Equal(t, uint32(charging.CCRequestTypeEvent), event.CCRequestType)
	assert.Equal(t, uint32(0), event.CCRequestNumber)
	assert.Equal(t, "ro-session-123", event.Header.SessionID)
	assert.Equal(t, "scscf.ims.example.com", event.Header.OriginHost)
}

func TestToCCREvent_ServiceContextID(t *testing.T) {
	tr := NewTranslator()
	m := buildTestRoCCR(t, charging.CCRequestTypeEvent, 0)

	// Add Service-Context-Id (Ro)
	m.NewAVP(461, avp.Mbit, 0, datatype.UTF8String("32260@3gpp.org"))

	event, err := tr.ToCCREvent(m, "trace-2")
	require.NoError(t, err)

	assert.Equal(t, "32260@3gpp.org", event.ServiceContextID)
}

func TestToCCREvent_IMSInformation(t *testing.T) {
	tr := NewTranslator()
	m := buildTestRoCCR(t, charging.CCRequestTypeEvent, 0)

	// Add Service-Information with IMS-Information
	imsInfo := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(823, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{AVP: []*diam.AVP{ // Event-Type
				diam.NewAVP(824, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("INVITE")), // SIP-Method
				diam.NewAVP(888, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(3600)),     // Expires
			}}),
			diam.NewAVP(829, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(RoleOriginating)),                     // Role-of-Node
			diam.NewAVP(862, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(NodeFunctionalitySCSCF)),              // Node-Functionality
			diam.NewAVP(832, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("sip:+491234567890@ims.example.com")), // Called-Party-Address
			diam.NewAVP(831, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("sip:+499876543210@ims.example.com")), // Calling-Party-Address
		},
	}
	serviceInfo := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(876, avp.Mbit|avp.Vbit, 10415, imsInfo),
		},
	}
	m.NewAVP(873, avp.Mbit|avp.Vbit, 10415, serviceInfo)

	// Decoded from the wire, as the server reads it: Event-Type is Grouped
	// in the dictionary, which an AVP built in memory does not show.
	wire, err := m.Serialize()
	require.NoError(t, err)
	m, err = diam.ReadMessage(bytes.NewReader(wire), dict.Default)
	require.NoError(t, err)

	event, err := tr.ToCCREvent(m, "trace-3")
	require.NoError(t, err)

	require.NotNil(t, event.ServiceInformation)
	require.NotNil(t, event.ServiceInformation.IMSInformation)

	ims := event.ServiceInformation.IMSInformation
	require.NotNil(t, ims.EventType)
	assert.Equal(t, EventType{SIPMethod: "INVITE", Expires: 3600}, *ims.EventType)
	assert.Equal(t, uint32(RoleOriginating), ims.RoleOfNode)
	assert.Equal(t, uint32(NodeFunctionalitySCSCF), ims.NodeFunctionality)
	assert.Equal(t, "sip:+491234567890@ims.example.com", ims.CalledPartyAddress)
	require.Len(t, ims.CallingPartyAddress, 1)
	assert.Equal(t, "sip:+499876543210@ims.example.com", ims.CallingPartyAddress[0])
}

func TestToCCREvent_SubscriptionIDs(t *testing.T) {
	tr := NewTranslator()
	m := buildTestRoCCR(t, charging.CCRequestTypeEvent, 0)

	// Add Subscription-ID with IMSI
	addSubscriptionID(m, charging.SubscriptionIDTypeEndUserIMSI, "262021234567890")
	// Add Subscription-ID with SIP URI
	addSubscriptionID(m, charging.SubscriptionIDTypeEndUserSIPURI, "sip:user@ims.example.com")

	event, err := tr.ToCCREvent(m, "trace-4")
	require.NoError(t, err)

	assert.Len(t, event.SubscriptionID, 2)
}

func TestToCCAMessage_Success(t *testing.T) {
	tr := NewTranslator()
	req := buildTestRoCCR(t, charging.CCRequestTypeEvent, 0)

	answer := &RoAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeEvent,
		CCRequestNumber: 0,
		OriginHost:      "ocs.example.com",
		OriginRealm:     "example.com",
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)
	require.NotNil(t, cca)

	// Verify it's an answer
	assert.False(t, cca.Header.CommandFlags&diam.RequestFlag != 0)
}

func TestToCCAMessage_WithMSCC(t *testing.T) {
	tr := NewTranslator()
	req := buildTestRoCCR(t, charging.CCRequestTypeEvent, 0)

	answer := &RoAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeEvent,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup:       rg(100),
				ServiceIdentifier: 1,
				GrantedServiceUnit: &charging.ServiceUnit{
					CCTime: 300,
				},
				ResultCode: diam.Success,
			},
		},
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)

	// Find MSCC AVP
	var foundMSCC bool
	for _, a := range cca.AVP {
		if a.Code == 456 {
			foundMSCC = true
			break
		}
	}
	assert.True(t, foundMSCC, "MSCC AVP should be present")
}

func TestToJSONAnswer(t *testing.T) {
	tr := NewTranslator()

	jsonData := `{
		"result_code": 2001,
		"cc_request_type": 4,
		"cc_request_number": 0,
		"origin_host": "ocs.example.com",
		"mscc": [
			{
				"rating_group": 100,
				"granted_service_unit": {
					"cc_time": 300
				},
				"result_code": 2001
			}
		]
	}`

	answer, err := tr.ToJSONAnswer([]byte(jsonData))
	require.NoError(t, err)

	assert.Equal(t, uint32(2001), answer.ResultCode)
	assert.Equal(t, uint32(4), answer.CCRequestType) // EVENT_REQUEST
	assert.Equal(t, "ocs.example.com", answer.OriginHost)
	require.Len(t, answer.MSCC, 1)
	assert.Equal(t, rg(100), answer.MSCC[0].RatingGroup)
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

func TestGetCallingParty(t *testing.T) {
	tr := NewTranslator()

	event := &RoEvent{
		ServiceInformation: &RoServiceInformation{
			IMSInformation: &IMSInformation{
				CallingPartyAddress: []string{"sip:+491234567890@ims.example.com"},
			},
		},
	}

	callingParty := tr.GetCallingParty(event)
	assert.Equal(t, "sip:+491234567890@ims.example.com", callingParty)
}

func TestGetCallingParty_NoIMSInfo(t *testing.T) {
	tr := NewTranslator()
	event := &RoEvent{}

	callingParty := tr.GetCallingParty(event)
	assert.Equal(t, "", callingParty)
}

func TestCCRequestTypeName(t *testing.T) {
	tests := []struct {
		reqType  uint32
		expected string
	}{
		{charging.CCRequestTypeInitial, "INITIAL_REQUEST"},
		{charging.CCRequestTypeUpdate, "UPDATE_REQUEST"},
		{charging.CCRequestTypeTermination, "TERMINATION_REQUEST"},
		{charging.CCRequestTypeEvent, "EVENT_REQUEST"},
		{99, "UNKNOWN"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			name := CCRequestTypeName(tc.reqType)
			assert.Equal(t, tc.expected, name)
		})
	}
}

func TestIMSConstants(t *testing.T) {
	// Role-of-Node
	assert.Equal(t, uint32(0), RoleOriginating)
	assert.Equal(t, uint32(1), RoleTerminating)

	// Node-Functionality
	assert.Equal(t, uint32(0), NodeFunctionalitySCSCF)
	assert.Equal(t, uint32(1), NodeFunctionalityPCSCF)
	assert.Equal(t, uint32(2), NodeFunctionalityICSCF)
}

func TestHexEncode(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0xAB, 0xCD}
	encoded := HexEncode(data)
	assert.Equal(t, "010203abcd", encoded)
}

func TestServiceContextID(t *testing.T) {
	assert.Equal(t, charging.ServiceContextIDRo, ServiceContextID())
}

func TestAppID(t *testing.T) {
	assert.Equal(t, charging.AppIDCreditControl, AppID())
}

// =============================================================================
// Helper Functions
// =============================================================================

func buildTestRoCCR(t *testing.T, reqType uint32, reqNum uint32) *diam.Message {
	// Use Credit-Control Application-ID (4)
	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	// Session-Id
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("ro-session-123"))
	// Origin-Host
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("scscf.ims.example.com"))
	// Origin-Realm
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("ims.example.com"))
	// Destination-Realm
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("ocs.example.com"))

	// CC-Request-Type
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(reqType))
	// CC-Request-Number
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(reqNum))

	return m
}

func addSubscriptionID(m *diam.Message, subType uint32, data string) {
	g := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(450, avp.Mbit, 0, datatype.Enumerated(subType)),
			diam.NewAVP(444, avp.Mbit, 0, datatype.UTF8String(data)),
		},
	}
	m.NewAVP(443, avp.Mbit, 0, g)
}

// Benchmark tests
func BenchmarkToCCREvent(b *testing.B) {
	tr := NewTranslator()
	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("bench-session"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("scscf.example.com"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(4))
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToCCREvent(m, "bench-trace")
	}
}

func BenchmarkToCCAMessage(b *testing.B) {
	tr := NewTranslator()
	req := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	answer := &RoAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   4,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup:        rg(100),
				GrantedServiceUnit: &charging.ServiceUnit{CCTime: 300},
				ResultCode:         diam.Success,
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToCCAMessage(answer, req)
	}
}

func BenchmarkToJSONAnswer(b *testing.B) {
	tr := NewTranslator()
	jsonData, _ := json.Marshal(&RoAnswer{
		ResultCode:      2001,
		CCRequestType:   4,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup:        rg(100),
				GrantedServiceUnit: &charging.ServiceUnit{CCTime: 300},
				ResultCode:         2001,
			},
		},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToJSONAnswer(jsonData)
	}
}

func rg(v uint32) *uint32 { return &v }

// TestToCCAMessage_MSCCAsGy: a Ro CCA's MSCC carries the quota thresholds,
// Quota-Holding-Time and the Final-Unit-Indication's Redirect-Server, as a
// Gy CCA does; Ro wrote Rating-Group, Service-Identifier, GSU, Result-Code,
// Validity-Time and the Final-Unit-Action only.
func TestToCCAMessage_MSCCAsGy(t *testing.T) {
	answer := &RoAnswer{ResultCode: 2001, MSCC: []charging.MSCC{{
		GrantedServiceUnit: &charging.ServiceUnit{CCTime: 60},
		TimeQuotaThreshold: 10, QuotaHoldingTime: rg(0),
		FinalUnitIndication: &charging.FinalUnitIndication{FinalUnitAction: 1,
			RedirectServer: &charging.RedirectServer{RedirectAddressType: 2, RedirectServerAddress: "http://topup"}},
	}}}
	cca, err := NewTranslator().ToCCAMessage(answer, diam.NewRequest(272, 4, dict.Default))
	require.NoError(t, err)
	got := charging.ReadMSCCs(cca.AVP)
	require.Len(t, got, 1)
	assert.Equal(t, uint32(10), got[0].TimeQuotaThreshold)
	assert.Equal(t, rg(0), got[0].QuotaHoldingTime)
	require.NotNil(t, got[0].FinalUnitIndication.RedirectServer)
	assert.Equal(t, "http://topup", got[0].FinalUnitIndication.RedirectServer.RedirectServerAddress)
}
