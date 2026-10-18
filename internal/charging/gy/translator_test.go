// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gy

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

	// Build a CCR message
	m := buildTestCCR(t, charging.CCRequestTypeInitial, 0)

	event, err := tr.ToCCREvent(m, "test-trace-1")
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, "test-trace-1", event.Header.TraceID)
	assert.Equal(t, uint32(charging.CCRequestTypeInitial), event.CCRequestType)
	assert.Equal(t, uint32(0), event.CCRequestNumber)
	assert.Equal(t, "test-session-123", event.Header.SessionID)
	assert.Equal(t, "pgw.example.com", event.Header.OriginHost)
	assert.Equal(t, "example.com", event.Header.OriginRealm)
}

func TestToCCREvent_SubscriptionIDs(t *testing.T) {
	tr := NewTranslator()
	m := buildTestCCR(t, charging.CCRequestTypeInitial, 0)

	// Add Subscription-ID with IMSI
	addSubscriptionID(m, charging.SubscriptionIDTypeEndUserIMSI, "262021234567890")
	// Add Subscription-ID with MSISDN
	addSubscriptionID(m, charging.SubscriptionIDTypeEndUserE164, "+4917612345678")

	event, err := tr.ToCCREvent(m, "trace-2")
	require.NoError(t, err)

	assert.Len(t, event.SubscriptionID, 2)
	assert.Equal(t, uint32(charging.SubscriptionIDTypeEndUserIMSI), event.SubscriptionID[0].Type)
	assert.Equal(t, "262021234567890", event.SubscriptionID[0].Data)
	assert.Equal(t, uint32(charging.SubscriptionIDTypeEndUserE164), event.SubscriptionID[1].Type)
	assert.Equal(t, "+4917612345678", event.SubscriptionID[1].Data)
}

func TestToCCREvent_MSCC(t *testing.T) {
	tr := NewTranslator()
	m := buildTestCCR(t, charging.CCRequestTypeUpdate, 1)

	// Add MSCC with Used-Service-Unit
	addMSCC(m, 100, 1, &charging.ServiceUnit{
		CCTime:        3600,
		CCTotalOctets: 1024000,
	})

	event, err := tr.ToCCREvent(m, "trace-3")
	require.NoError(t, err)

	require.Len(t, event.MSCC, 1)
	assert.Equal(t, rg(100), event.MSCC[0].RatingGroup)
	assert.Equal(t, uint32(1), event.MSCC[0].ServiceIdentifier)
}

func TestToCCREvent_ServiceContextID(t *testing.T) {
	tr := NewTranslator()
	m := buildTestCCR(t, charging.CCRequestTypeInitial, 0)

	// Add Service-Context-Id
	m.NewAVP(461, avp.Mbit, 0, datatype.UTF8String("32251@3gpp.org"))

	event, err := tr.ToCCREvent(m, "trace-4")
	require.NoError(t, err)

	assert.Equal(t, "32251@3gpp.org", event.ServiceContextID)
}

func TestToCCREvent_TerminationCause(t *testing.T) {
	tr := NewTranslator()
	m := buildTestCCR(t, charging.CCRequestTypeTermination, 2)

	// Add Termination-Cause (1 = DIAMETER_LOGOUT)
	m.NewAVP(295, avp.Mbit, 0, datatype.Enumerated(charging.TermCauseDiameterLogout))

	event, err := tr.ToCCREvent(m, "trace-5")
	require.NoError(t, err)

	assert.Equal(t, uint32(charging.CCRequestTypeTermination), event.CCRequestType)
	assert.Equal(t, uint32(charging.TermCauseDiameterLogout), event.TerminationCause)
}

func TestToCCAMessage_Success(t *testing.T) {
	tr := NewTranslator()
	req := buildTestCCR(t, charging.CCRequestTypeInitial, 0)

	answer := &GyAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		OriginHost:      "ocs.example.com",
		OriginRealm:     "example.com",
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)
	require.NotNil(t, cca)

	// Verify it's an answer (not request flag set)
	assert.False(t, cca.Header.CommandFlags&diam.RequestFlag != 0, "Should be an answer, not request")

	// Verify CC-Request-Type in answer
	ccTypeAVP := findAVPByCode(cca, 416)
	require.NotNil(t, ccTypeAVP)
	assert.Equal(t, datatype.Enumerated(charging.CCRequestTypeInitial), ccTypeAVP.Data)

	// Verify CC-Request-Number
	ccNumAVP := findAVPByCode(cca, 415)
	require.NotNil(t, ccNumAVP)
	assert.Equal(t, datatype.Unsigned32(0), ccNumAVP.Data)
}

func TestToCCAMessage_WithMSCC(t *testing.T) {
	tr := NewTranslator()
	req := buildTestCCR(t, charging.CCRequestTypeUpdate, 1)

	answer := &GyAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeUpdate,
		CCRequestNumber: 1,
		MSCC: []charging.MSCC{
			{
				RatingGroup:       rg(100),
				ServiceIdentifier: 1,
				GrantedServiceUnit: &charging.ServiceUnit{
					CCTime:        1800,
					CCTotalOctets: 50 * 1024 * 1024,
				},
				ValidityTime: 3600,
				ResultCode:   diam.Success,
			},
		},
		ValidityTime: 3600,
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)

	// Find MSCC AVP
	msccAVP := findAVPByCode(cca, 456)
	require.NotNil(t, msccAVP, "MSCC AVP should be present")
}

func TestToCCAMessage_FinalUnitIndication(t *testing.T) {
	tr := NewTranslator()
	req := buildTestCCR(t, charging.CCRequestTypeUpdate, 2)

	answer := &GyAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeUpdate,
		CCRequestNumber: 2,
		FinalUnitIndication: &charging.FinalUnitIndication{
			FinalUnitAction: charging.FinalUnitActionRedirect,
			RedirectServer: &charging.RedirectServer{
				RedirectAddressType:   0, // IPv4
				RedirectServerAddress: "http://redirect.example.com",
			},
		},
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)

	// Find Final-Unit-Indication AVP
	fuiAVP := findAVPByCode(cca, 430)
	require.NotNil(t, fuiAVP, "Final-Unit-Indication AVP should be present")
}

func TestToCCAMessage_FailedAVP(t *testing.T) {
	tr := NewTranslator()
	req := buildTestCCR(t, charging.CCRequestTypeInitial, 0)

	answer := &GyAnswer{
		ResultCode:      diam.InvalidAVPValue,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		FailedAVP: []charging.FailedAVP{
			{
				Code:     443,
				VendorID: 0,
				Data:     []byte("invalid"),
			},
		},
	}

	cca, err := tr.ToCCAMessage(answer, req)
	require.NoError(t, err)

	// Find Failed-AVP
	failedAVP := findAVPByCode(cca, 279)
	require.NotNil(t, failedAVP, "Failed-AVP should be present")
}

func TestToJSONAnswer(t *testing.T) {
	tr := NewTranslator()

	jsonData := `{
		"result_code": 2001,
		"cc_request_type": 1,
		"cc_request_number": 0,
		"origin_host": "ocs.example.com",
		"mscc": [
			{
				"rating_group": 100,
				"granted_service_unit": {
					"cc_time": 3600,
					"cc_total_octets": 104857600
				},
				"validity_time": 1800,
				"result_code": 2001
			}
		]
	}`

	answer, err := tr.ToJSONAnswer([]byte(jsonData))
	require.NoError(t, err)

	assert.Equal(t, uint32(2001), answer.ResultCode)
	assert.Equal(t, uint32(1), answer.CCRequestType)
	assert.Equal(t, "ocs.example.com", answer.OriginHost)
	require.Len(t, answer.MSCC, 1)
	assert.Equal(t, rg(100), answer.MSCC[0].RatingGroup)
	assert.Equal(t, uint32(3600), answer.MSCC[0].GrantedServiceUnit.CCTime)
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

func TestGetIMSI(t *testing.T) {
	tr := NewTranslator()

	event := &GyEvent{
		SubscriptionID: []charging.SubscriptionID{
			{Type: charging.SubscriptionIDTypeEndUserE164, Data: "+4917612345678"},
			{Type: charging.SubscriptionIDTypeEndUserIMSI, Data: "262021234567890"},
		},
	}

	imsi := tr.GetIMSI(event)
	assert.Equal(t, "262021234567890", imsi)
}

func TestGetIMSI_NotFound(t *testing.T) {
	tr := NewTranslator()

	event := &GyEvent{
		SubscriptionID: []charging.SubscriptionID{
			{Type: charging.SubscriptionIDTypeEndUserE164, Data: "+4917612345678"},
		},
	}

	imsi := tr.GetIMSI(event)
	assert.Equal(t, "", imsi)
}

func TestGetMSISDN(t *testing.T) {
	tr := NewTranslator()

	event := &GyEvent{
		SubscriptionID: []charging.SubscriptionID{
			{Type: charging.SubscriptionIDTypeEndUserIMSI, Data: "262021234567890"},
			{Type: charging.SubscriptionIDTypeEndUserE164, Data: "+4917612345678"},
		},
	}

	msisdn := tr.GetMSISDN(event)
	assert.Equal(t, "+4917612345678", msisdn)
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

func TestHexEncode(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0xAB, 0xCD}
	encoded := HexEncode(data)
	assert.Equal(t, "010203abcd", encoded)
}

// =============================================================================
// Helper Functions
// =============================================================================

func buildTestCCR(t *testing.T, reqType uint32, reqNum uint32) *diam.Message {
	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	// Session-Id
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-123"))
	// Origin-Host
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.example.com"))
	// Origin-Realm
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	// Destination-Realm
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("ocs.example.com"))

	// CC-Request-Type (416)
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(reqType))
	// CC-Request-Number (415)
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(reqNum))

	return m
}

func addSubscriptionID(m *diam.Message, subType uint32, data string) {
	// Subscription-ID is a grouped AVP (443)
	g := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(450, avp.Mbit, 0, datatype.Enumerated(subType)), // Subscription-ID-Type
			diam.NewAVP(444, avp.Mbit, 0, datatype.UTF8String(data)),    // Subscription-ID-Data
		},
	}
	m.NewAVP(443, avp.Mbit, 0, g)
}

func addMSCC(m *diam.Message, ratingGroup, serviceID uint32, usedUnit *charging.ServiceUnit) {
	g := &diam.GroupedAVP{AVP: []*diam.AVP{}}

	// Rating-Group (432)
	g.AVP = append(g.AVP, diam.NewAVP(432, avp.Mbit, 0, datatype.Unsigned32(ratingGroup)))

	// Service-Identifier (439)
	if serviceID > 0 {
		g.AVP = append(g.AVP, diam.NewAVP(439, avp.Mbit, 0, datatype.Unsigned32(serviceID)))
	}

	// Used-Service-Unit (446)
	if usedUnit != nil {
		usu := &diam.GroupedAVP{AVP: []*diam.AVP{}}
		if usedUnit.CCTime > 0 {
			usu.AVP = append(usu.AVP, diam.NewAVP(420, avp.Mbit, 0, datatype.Unsigned32(usedUnit.CCTime)))
		}
		if usedUnit.CCTotalOctets > 0 {
			usu.AVP = append(usu.AVP, diam.NewAVP(421, avp.Mbit, 0, datatype.Unsigned64(usedUnit.CCTotalOctets)))
		}
		g.AVP = append(g.AVP, diam.NewAVP(446, avp.Mbit, 0, usu))
	}

	// Multiple-Services-Credit-Control (456)
	m.NewAVP(456, avp.Mbit, 0, g)
}

func findAVPByCode(m *diam.Message, code uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code {
			return a
		}
	}
	return nil
}

// Benchmark tests
func BenchmarkToCCREvent(b *testing.B) {
	tr := NewTranslator()
	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("bench-session"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.example.com"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("example.com"))
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tr.ToCCREvent(m, "bench-trace")
	}
}

func BenchmarkToCCAMessage(b *testing.B) {
	tr := NewTranslator()
	req := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	answer := &GyAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   1,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup: rg(100),
				GrantedServiceUnit: &charging.ServiceUnit{
					CCTime:        1800,
					CCTotalOctets: 50 * 1024 * 1024,
				},
				ValidityTime: 3600,
				ResultCode:   diam.Success,
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
	jsonData, _ := json.Marshal(&GyAnswer{
		ResultCode:      2001,
		CCRequestType:   1,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup:        rg(100),
				GrantedServiceUnit: &charging.ServiceUnit{CCTime: 3600},
				ValidityTime:       1800,
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
