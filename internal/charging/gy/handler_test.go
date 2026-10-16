// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gy

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConn implements diam.Conn for testing
type mockConn struct {
	mu       sync.Mutex
	messages []*diam.Message
	closed   bool
	ctx      context.Context
}

func newMockConn() *mockConn {
	return &mockConn{ctx: context.Background()}
}

func (m *mockConn) Write(b []byte) (int, error) {
	return len(b), nil
}

func (m *mockConn) WriteStream(b []byte, stream uint) (int, error) {
	return len(b), nil
}

func (m *mockConn) Read(b []byte) (int, error) {
	return 0, io.EOF
}

func (m *mockConn) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3868}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.168.1.1"), Port: 50000}
}

func (m *mockConn) TLS() *tls.ConnectionState { return nil }

func (m *mockConn) Dictionary() *dict.Parser {
	return dict.Default
}

func (m *mockConn) Context() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m *mockConn) SetContext(ctx context.Context) {
	m.ctx = ctx
}

func (m *mockConn) Connection() net.Conn {
	return nil
}

func (m *mockConn) GetMessages() []*diam.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages
}

func (m *mockConn) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func TestNewHandler(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 5*time.Second)

	require.NotNil(t, h)
	assert.NotNil(t, h.translator)
	assert.Equal(t, 5*time.Second, h.timeout)
}

func TestNewHandler_DefaultTimeout(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 0)

	require.NotNil(t, h)
	assert.Equal(t, 2*time.Second, h.timeout)
}

func TestServiceContextID(t *testing.T) {
	assert.Equal(t, charging.ServiceContextIDGy, ServiceContextID())
}

func TestAppID(t *testing.T) {
	assert.Equal(t, charging.AppIDCreditControl, AppID())
}

func TestFindAVP(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 2*time.Second)

	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x123, 0x456, nil)
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(1))
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))

	// Find CC-Request-Type
	a := h.findAVP(m, 416, 0)
	require.NotNil(t, a)
	assert.Equal(t, uint32(416), a.Code)

	// Find non-existent AVP
	a = h.findAVP(m, 999, 0)
	assert.Nil(t, a)

	// Find AVP with wrong vendor
	a = h.findAVP(m, 416, 10415)
	assert.Nil(t, a)
}

func TestSendErrorAnswer(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 2*time.Second)

	conn := newMockConn()
	req := buildTestCCRMessage(charging.CCRequestTypeInitial, 0)

	// This should not panic and should write an error response
	h.sendErrorAnswer(conn, req, diam.UnableToDeliver, "test-trace")
}

func TestSendErrorAnswer_WithDifferentResultCodes(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 2*time.Second)

	testCases := []struct {
		name       string
		resultCode uint32
	}{
		{"UnableToDeliver", diam.UnableToDeliver},
		{"InvalidAVPValue", diam.InvalidAVPValue},
		{"MissingAVP", diam.MissingAVP},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			conn := newMockConn()
			req := buildTestCCRMessage(charging.CCRequestTypeInitial, 0)
			h.sendErrorAnswer(conn, req, tc.resultCode, "test-"+tc.name)
		})
	}
}

func TestCCRequestTypeNames(t *testing.T) {
	tests := []struct {
		reqType  uint32
		expected string
	}{
		{charging.CCRequestTypeInitial, "INITIAL_REQUEST"},
		{charging.CCRequestTypeUpdate, "UPDATE_REQUEST"},
		{charging.CCRequestTypeTermination, "TERMINATION_REQUEST"},
		{charging.CCRequestTypeEvent, "EVENT_REQUEST"},
		{0, "UNKNOWN"},
		{99, "UNKNOWN"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			name := CCRequestTypeName(tc.reqType)
			assert.Equal(t, tc.expected, name)
		})
	}
}

func TestNATSTopics(t *testing.T) {
	assert.Equal(t, "diameter.gy.ccr", TopicGyCCR)
	assert.Equal(t, "diameter.gy.rar", TopicGyRAR)
}

// =============================================================================
// Helper Functions
// =============================================================================

func buildTestCCRMessage(reqType uint32, reqNum uint32) *diam.Message {
	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x12345678, 0x87654321, nil)

	// Session-Id
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-123"))
	// Origin-Host
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.test.com"))
	// Origin-Realm
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))

	// CC-Request-Type
	m.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(reqType))
	// CC-Request-Number
	m.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(reqNum))

	return m
}

// Benchmark for handler operations
func BenchmarkFindAVP(b *testing.B) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 2*time.Second)

	m := diam.NewMessage(272, diam.RequestFlag, 4, 0x123, 0x456, nil)
	// Add multiple AVPs
	for i := uint32(1); i <= 50; i++ {
		m.NewAVP(i, avp.Mbit, 0, datatype.Unsigned32(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.findAVP(m, 25, 0)
	}
}

func BenchmarkSendErrorAnswer(b *testing.B) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 2*time.Second)

	conn := newMockConn()
	req := buildTestCCRMessage(charging.CCRequestTypeInitial, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.sendErrorAnswer(conn, req, diam.UnableToDeliver, "bench-trace")
	}
}

// Test NATS-related functionality (requires integration test setup)
// These tests verify the handler structure and constants

func TestHandlerFields(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 3*time.Second)

	assert.Nil(t, h.natsClient)
	assert.NotNil(t, h.translator)
	assert.NotNil(t, h.peerManager)
	assert.Equal(t, 3*time.Second, h.timeout)
}

// Ensure Handler implements ChargingHandler interface
type chargingHandler interface {
	HandleCCR(c diam.Conn, m *diam.Message)
}

func TestHandlerImplementsInterface(t *testing.T) {
	pm := diameter.NewPeerManager()
	h := NewHandler(nil, pm, 2*time.Second)

	// Compile-time check
	var _ chargingHandler = h
}

// Test error scenarios
func TestCircuitBreakerError(t *testing.T) {
	// Verify IsCircuitOpen function exists and works
	err := errors.New("some error")
	result := nats.IsCircuitOpen(err)
	assert.False(t, result)

	// Circuit breaker open error (using proper type)
	cbErr := &nats.CircuitOpenError{Subject: "test", State: "open"}
	result = nats.IsCircuitOpen(cbErr)
	assert.True(t, result)
}
