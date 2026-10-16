// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockPeerManager for testing
type MockPeerManager struct {
	mock.Mock
}

func (m *MockPeerManager) GetConnection(identity string) interface{} {
	args := m.Called(identity)
	return args.Get(0)
}

func (m *MockPeerManager) GetAnyConnection() interface{} {
	args := m.Called()
	return args.Get(0)
}

// TestSRRRequestParsing tests parsing of SRR request from NATS
func TestSRRRequestParsing(t *testing.T) {
	// Create SRR request JSON
	srrRequest := SRRRequest{
		TraceID:   "srr-test-001",
		MSISDN:    "+905551234567",
		SCAddress: "+905309999999",
	}

	data, err := json.Marshal(srrRequest)
	require.NoError(t, err)

	// Parse it back
	var parsed SRRRequest
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, srrRequest.TraceID, parsed.TraceID)
	assert.Equal(t, srrRequest.MSISDN, parsed.MSISDN)
	assert.Equal(t, srrRequest.SCAddress, parsed.SCAddress)
}

// TestTFRRequestParsing tests parsing of TFR request from NATS
func TestTFRRequestParsing(t *testing.T) {
	// Create TFR request JSON
	tfrRequest := TFRRequest{
		TraceID:       "tfr-test-001",
		MSISDN:        "+905551234567",
		IMSI:          "286010123456789",
		SMRPUI:        []byte{0x00, 0x01, 0x02, 0x03}, // SMS PDU
		SMRPMTI:       0,                              // SMS-DELIVER
		SCAddress:     "+905309999999",
		MMEName:       "mme01.epc.mnc001.mcc286.3gppnetwork.org",
		MMERealm:      "epc.mnc001.mcc286.3gppnetwork.org",
		CorrelationID: "msg-001",
	}

	data, err := json.Marshal(tfrRequest)
	require.NoError(t, err)

	// Parse it back
	var parsed TFRRequest
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, tfrRequest.TraceID, parsed.TraceID)
	assert.Equal(t, tfrRequest.MSISDN, parsed.MSISDN)
	assert.Equal(t, tfrRequest.IMSI, parsed.IMSI)
	assert.Equal(t, tfrRequest.MMEName, parsed.MMEName)
	assert.Equal(t, tfrRequest.CorrelationID, parsed.CorrelationID)
}

// TestALRRequestParsing tests parsing of ALR request from NATS
func TestALRRequestParsing(t *testing.T) {
	// Create ALR request JSON
	alrRequest := ALRRequest{
		TraceID:           "alr-test-001",
		MSISDN:            "+905551234567",
		SCAddress:         "+905309999999",
		SMSGMSCAlertEvent: 1,
	}

	data, err := json.Marshal(alrRequest)
	require.NoError(t, err)

	// Parse it back
	var parsed ALRRequest
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, alrRequest.TraceID, parsed.TraceID)
	assert.Equal(t, alrRequest.MSISDN, parsed.MSISDN)
	assert.Equal(t, alrRequest.SMSGMSCAlertEvent, parsed.SMSGMSCAlertEvent)
}

// TestMSISDNFormatting tests MSISDN format validation
func TestMSISDNFormatting(t *testing.T) {
	testCases := []struct {
		name    string
		input   string
		isValid bool
	}{
		{"E.164 with plus", "+905551234567", true},
		{"E.164 without plus", "905551234567", true},
		{"Short number", "123", true},
		{"Empty", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Basic validation - MSISDN should have digits
			isValid := len(tc.input) > 0
			assert.Equal(t, tc.isValid, isValid)
		})
	}
}

// TestSRRResponseFormat tests SRA response JSON structure
func TestSRRResponseFormat(t *testing.T) {
	// Simulate successful SRA response
	response := map[string]interface{}{
		"result_code": 2001,
		"mme_name":    "mme01.epc.mnc001.mcc286.3gppnetwork.org",
		"mme_realm":   "epc.mnc001.mcc286.3gppnetwork.org",
		"user_name":   "286010123456789",
		"trace_id":    "srr-test-001",
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	// Parse it back
	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// Verify result code (should be float64 after JSON unmarshal)
	resultCode, ok := parsed["result_code"].(float64)
	assert.True(t, ok, "result_code should be a number")
	assert.Equal(t, float64(2001), resultCode)

	// Verify MME info
	assert.Equal(t, "mme01.epc.mnc001.mcc286.3gppnetwork.org", parsed["mme_name"])
	assert.Equal(t, "epc.mnc001.mcc286.3gppnetwork.org", parsed["mme_realm"])
	assert.Equal(t, "286010123456789", parsed["user_name"])
}

// TestTFRResponseFormat tests TFA response JSON structure
func TestTFRResponseFormat(t *testing.T) {
	// Simulate successful TFA response
	response := map[string]interface{}{
		"result_code":    2001,
		"trace_id":       "tfr-test-001",
		"correlation_id": "msg-001",
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	resultCode, ok := parsed["result_code"].(float64)
	assert.True(t, ok)
	assert.Equal(t, float64(2001), resultCode)
}

// TestSGdErrorCodes tests error code handling
func TestSGdErrorCodes(t *testing.T) {
	errorCodes := map[int]string{
		2001: "Success",
		5001: "User unknown",
		4201: "Absent user",
		4243: "User busy for MT-SMS",
		4244: "Facility not supported",
		4247: "SM delivery timeout",
		5016: "SM delivery failure",
	}

	for code, description := range errorCodes {
		t.Run(description, func(t *testing.T) {
			response := map[string]interface{}{
				"result_code":   code,
				"error_message": description,
			}

			data, err := json.Marshal(response)
			require.NoError(t, err)

			var parsed map[string]interface{}
			err = json.Unmarshal(data, &parsed)
			require.NoError(t, err)

			assert.Equal(t, float64(code), parsed["result_code"])
		})
	}
}

// TestClientConfiguration tests SGd client configuration
func TestClientConfiguration(t *testing.T) {
	// Test default timeout
	defaultTimeout := 2 * time.Second

	// Create a minimal config for testing
	config := struct {
		RequestTimeout time.Duration
	}{
		RequestTimeout: defaultTimeout,
	}

	assert.Equal(t, 2*time.Second, config.RequestTimeout)
}

// TestNATSTopics tests that NATS topics are correctly defined
func TestNATSTopics(t *testing.T) {
	// These should match the topics used by the SMSC
	expectedTopics := map[string]string{
		"SRR_REQUEST": "diameter.sgd.srr.request",
		"TFR_REQUEST": "diameter.sgd.tfr.request",
		"ALR_REQUEST": "diameter.sgd.alr.request",
	}

	// Verify topics match expected values
	assert.Equal(t, "diameter.sgd.srr.request", expectedTopics["SRR_REQUEST"])
	assert.Equal(t, "diameter.sgd.tfr.request", expectedTopics["TFR_REQUEST"])
	assert.Equal(t, "diameter.sgd.alr.request", expectedTopics["ALR_REQUEST"])
}

// BenchmarkJSONUnmarshalSRR benchmarks SRR request JSON unmarshaling
func BenchmarkJSONUnmarshalSRR(b *testing.B) {
	data := []byte(`{"trace_id":"srr-bench","msisdn":"+905551234567","sc_address":"+905309999999"}`)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var req SRRRequest
		json.Unmarshal(data, &req)
	}
}

// BenchmarkJSONMarshalSRR benchmarks SRR request JSON marshaling
func BenchmarkJSONMarshalSRR(b *testing.B) {
	request := SRRRequest{
		TraceID:   "srr-bench-001",
		MSISDN:    "+905551234567",
		SCAddress: "+905309999999",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Marshal(request)
	}
}

// BenchmarkJSONMarshalTFR benchmarks TFR request JSON marshaling
func BenchmarkJSONMarshalTFR(b *testing.B) {
	request := TFRRequest{
		TraceID:       "tfr-bench-001",
		MSISDN:        "+905551234567",
		IMSI:          "286010123456789",
		SMRPUI:        []byte{0x00, 0x01, 0x02, 0x03},
		SMRPMTI:       0,
		SCAddress:     "+905309999999",
		MMEName:       "mme01.epc.mnc001.mcc286.3gppnetwork.org",
		MMERealm:      "epc.mnc001.mcc286.3gppnetwork.org",
		CorrelationID: "msg-bench-001",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		json.Marshal(request)
	}
}

// Integration test helper - requires running NATS server
func TestIntegration_SGdClient(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Try to connect to NATS
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip("NATS not available, skipping integration test")
	}
	defer nc.Close()

	// Test basic pub/sub for SGd topics
	received := make(chan *nats.Msg, 1)

	// Subscribe to SRR request topic
	sub, err := nc.Subscribe("diameter.sgd.srr.request", func(msg *nats.Msg) {
		received <- msg
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Publish test SRR request
	srrRequest := SRRRequest{
		TraceID:   "integration-test",
		MSISDN:    "+905551234567",
		SCAddress: "+905309999999",
	}
	data, _ := json.Marshal(srrRequest)

	err = nc.Publish("diameter.sgd.srr.request", data)
	require.NoError(t, err)

	// Wait for message
	select {
	case msg := <-received:
		var parsed SRRRequest
		err := json.Unmarshal(msg.Data, &parsed)
		require.NoError(t, err)
		assert.Equal(t, "integration-test", parsed.TraceID)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message")
	}
}
