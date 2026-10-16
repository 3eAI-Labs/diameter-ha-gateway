// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock NATS Client
// =============================================================================

type mockNatsClient struct {
	requestFunc func(topic string, msg interface{}, timeout time.Duration) ([]byte, error)
	publishFunc func(topic string, msg interface{}) error
}

func (m *mockNatsClient) Request(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
	if m.requestFunc != nil {
		return m.requestFunc(topic, msg, timeout)
	}
	return nil, errors.New("mock not configured")
}

func (m *mockNatsClient) Publish(topic string, msg interface{}) error {
	if m.publishFunc != nil {
		return m.publishFunc(topic, msg)
	}
	return nil
}

// =============================================================================
// Mock Diameter Connection
// =============================================================================

type mockDiamConn struct {
	writtenBytes []byte
	writeError   error
	mu           sync.Mutex
	ctx          context.Context
}

func newMockDiamConn() *mockDiamConn {
	return &mockDiamConn{ctx: context.Background()}
}

func (m *mockDiamConn) Write(b []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeError != nil {
		return 0, m.writeError
	}
	m.writtenBytes = append(m.writtenBytes, b...)
	return len(b), nil
}

func (m *mockDiamConn) WriteStream(b []byte, stream uint) (int, error) {
	return m.Write(b)
}

func (m *mockDiamConn) Close() {}
func (m *mockDiamConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3868}
}
func (m *mockDiamConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 3868}
}
func (m *mockDiamConn) TLS() *tls.ConnectionState      { return nil }
func (m *mockDiamConn) Dictionary() *dict.Parser       { return nil }
func (m *mockDiamConn) Context() context.Context       { return m.ctx }
func (m *mockDiamConn) SetContext(ctx context.Context) { m.ctx = ctx }
func (m *mockDiamConn) Connection() net.Conn           { return nil }

func (m *mockDiamConn) getWrittenBytes() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writtenBytes
}

func (m *mockDiamConn) wasWriteCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.writtenBytes) > 0
}

// =============================================================================
// Gateway Test Helpers
// =============================================================================

// createTestGatewayService creates a gateway service with mock NATS client
// Note: This requires modifying the GatewayService to accept an interface
// For now, we test with nil NATS client and verify behavior through other means

func buildTestULR() *diam.Message {
	m := diam.NewRequest(316, 16777251, nil) // ULR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-ulr"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.3gppnetwork.org"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("hss.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

func buildTestAIR() *diam.Message {
	m := diam.NewRequest(318, 16777251, nil) // AIR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-air"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.3gppnetwork.org"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("hss.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

func buildTestPUR() *diam.Message {
	m := diam.NewRequest(321, 16777251, nil) // PUR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-pur"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.3gppnetwork.org"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("hss.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

// decodeWrittenAnswer parses the bytes the gateway wrote back to its peer
// connection so tests can inspect application AVPs (regression guard:
// answers once reached the wire as a bare 2001 with no application AVPs). The default dictionary is sufficient for base + S6a / SGd /
// charging AVPs registered via go-diameter's loader at init time.
func decodeWrittenAnswer(t *testing.T, b []byte) *diam.Message {
	t.Helper()
	require.NotEmpty(t, b, "no bytes written — gateway emitted nothing")
	msg, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	require.NoError(t, err, "decode written bytes as Diameter message")
	return msg
}

func buildTestECR() *diam.Message {
	m := diam.NewRequest(324, 16777252, nil) // ECR
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("test-session-ecr"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.test.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

// =============================================================================
// GatewayService Creation Tests
// =============================================================================

func TestNewGatewayService(t *testing.T) {
	pm := diameter.NewPeerManager()

	// Create with nil NATS client (testing creation only)
	gw := NewGatewayService(nil, pm, "test.gw.example.org", "test.example.org")

	require.NotNil(t, gw)
	assert.NotNil(t, gw.translator)
	assert.NotNil(t, gw.peerManager)
}

// =============================================================================
// GatewayService with Interface - Tests for refactored version
// =============================================================================

// NatsRequester is an interface for NATS request operations
// This allows testing without real NATS connection
type NatsRequester interface {
	Request(topic string, msg interface{}, timeout time.Duration) ([]byte, error)
}

// testableGatewayService is a version of GatewayService that uses interface
type testableGatewayService struct {
	natsClient  NatsRequester
	translator  *diameter.Translator
	peerManager *diameter.PeerManager
}

func newTestableGatewayService(nc NatsRequester, pm *diameter.PeerManager) *testableGatewayService {
	return &testableGatewayService{
		natsClient:  nc,
		translator:  diameter.NewTranslator(),
		peerManager: pm,
	}
}

func (g *testableGatewayService) handleRequest(conn diam.Conn, m *diam.Message, cmd, topic string) error {
	var event interface{}
	var err error

	if cmd == "ECR" {
		event, err = g.translator.ToS13Event(m, "test-trace")
	} else {
		event, err = g.translator.ToS6aEvent(m, "test-trace")
	}

	if err != nil {
		return err
	}

	// Register peer
	if originHost, err := m.FindAVP(avp.OriginHost, 0); err == nil {
		g.peerManager.Register(conn, originHost.Data.String())
	}

	// NATS Request
	respData, err := g.natsClient.Request(topic, event, 2*time.Second)
	if err != nil {
		// Write error response
		ans, _ := g.translator.CreateErrorAnswer(m, 3002) // UNABLE_TO_DELIVER
		_, _ = ans.WriteTo(conn)
		return err
	}

	// Mirror production gateway.go: encode the NATS payload into a real
	// Diameter answer rather than emitting a bare 2001. Tests that decode
	// the written bytes can assert application AVPs round-trip through
	// ToDiameterAnswer.
	ans, encErr := g.translator.ToDiameterAnswer(respData, m)
	if encErr != nil {
		ans, _ = g.translator.CreateErrorAnswer(m, 5012) // DIAMETER_UNABLE_TO_COMPLY
	}
	_, _ = ans.WriteTo(conn)
	return nil
}

// =============================================================================
// Testable Gateway Tests
// =============================================================================

func TestTestableGateway_HandleULR_Success(t *testing.T) {
	// Regression: the test must decode the bytes that landed
	// on the wire and assert ULA application AVPs round-tripped through
	// ToDiameterAnswer. wasWriteCalled() alone is false-confident — any
	// bare 2001 satisfies it.
	//
	// NOTE — Subscription-Data NOT asserted here: AVP 1400 is a Grouped
	// type per TS 29.272 §7.3.2, covered by the Subscription-Data encoder
	// tests in internal/diameter.
	const wantULAFlags uint32 = 1 // ULR-FLAGS Bit-0 (S6a/S6d-Indicator)

	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			assert.Equal(t, "diameter.s6a.ulr", topic)
			return json.Marshal(map[string]interface{}{
				"result_code": 2001,
				"ula_flags":   wantULAFlags,
			})
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestULR()

	err := gw.handleRequest(conn, msg, "ULR", "diameter.s6a.ulr")
	assert.NoError(t, err)

	// Decode the written answer and assert AVPs.
	ans := decodeWrittenAnswer(t, conn.getWrittenBytes())
	assert.Equal(t, uint32(316), ans.Header.CommandCode, "answer must echo ULR command code (316)")
	assert.Zero(t, ans.Header.CommandFlags&0x80, "Request bit must be cleared in answer")

	rc, err := ans.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err, "answer must carry Result-Code")
	assert.Equal(t, datatype.Unsigned32(2001), rc.Data.(datatype.Unsigned32))

	flags, err := ans.FindAVP(avp.ULAFlags, 10415)
	require.NoError(t, err, "ULA must carry ULA-Flags when non-zero")
	assert.Equal(t, datatype.Unsigned32(wantULAFlags), flags.Data.(datatype.Unsigned32))

	// Peer registration unchanged.
	active := pm.GetActiveConnections()
	require.Len(t, active, 1)
	assert.Contains(t, active[0], "mme.test.3gppnetwork.org")
}

func TestTestableGateway_HandleAIR_Success(t *testing.T) {
	// Regression: AIR → AIA must carry Authentication-Info
	// (1413, vendor 10415) with nested E-UTRAN-Vector(s) (1414).
	// Crypto material round-trips through hex decode in encodeAuthVectors.
	const (
		wantRANDHex  = "0102030405060708090a0b0c0d0e0f10"
		wantXRESHex  = "1112131415161718"
		wantAUTNHex  = "21222324252627282930313233343536"
		wantKASMEHex = "4142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f60"
	)

	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			assert.Equal(t, "diameter.s6a.air", topic)
			return json.Marshal(map[string]interface{}{
				"result_code": 2001,
				"auth_vectors": []map[string]interface{}{
					{
						"item":  uint32(1),
						"rand":  wantRANDHex,
						"xres":  wantXRESHex,
						"autn":  wantAUTNHex,
						"kasme": wantKASMEHex,
					},
				},
			})
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestAIR()

	err := gw.handleRequest(conn, msg, "AIR", "diameter.s6a.air")
	assert.NoError(t, err)

	ans := decodeWrittenAnswer(t, conn.getWrittenBytes())
	assert.Equal(t, uint32(318), ans.Header.CommandCode)

	rc, err := ans.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(2001), rc.Data.(datatype.Unsigned32))

	authInfo, err := ans.FindAVP(avp.AuthenticationInfo, 10415)
	require.NoError(t, err, "AIA must carry Authentication-Info group AVP")

	// Drill into the grouped AVP and verify the first vector's contents.
	group, ok := authInfo.Data.(*diam.GroupedAVP)
	require.True(t, ok, "Authentication-Info data must be a Grouped AVP")
	require.NotEmpty(t, group.AVP, "Authentication-Info must contain ≥1 child")

	vector := group.AVP[0]
	assert.Equal(t, uint32(avp.EUTRANVector), vector.Code, "child[0] must be E-UTRAN-Vector")
	innerGroup, ok := vector.Data.(*diam.GroupedAVP)
	require.True(t, ok, "E-UTRAN-Vector data must be a Grouped AVP")

	wantRAND, _ := hex.DecodeString(wantRANDHex)
	wantXRES, _ := hex.DecodeString(wantXRESHex)
	wantAUTN, _ := hex.DecodeString(wantAUTNHex)
	wantKASME, _ := hex.DecodeString(wantKASMEHex)
	got := map[uint32]datatype.Type{}
	for _, a := range innerGroup.AVP {
		got[a.Code] = a.Data
	}
	assert.Equal(t, datatype.Unsigned32(1), got[avp.ItemNumber].(datatype.Unsigned32))
	assert.Equal(t, datatype.OctetString(wantRAND), got[avp.RAND].(datatype.OctetString))
	assert.Equal(t, datatype.OctetString(wantXRES), got[avp.XRES].(datatype.OctetString))
	assert.Equal(t, datatype.OctetString(wantAUTN), got[avp.AUTN].(datatype.OctetString))
	assert.Equal(t, datatype.OctetString(wantKASME), got[avp.KASME].(datatype.OctetString))
}

func TestTestableGateway_HandlePUR_Success(t *testing.T) {
	// Regression: PUR → PUA must carry PUA-Flags when non-zero.
	const wantPUAFlags uint32 = 0b11 // UE-Purged-In-MME | UE-Purged-In-SGSN

	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			assert.Equal(t, "diameter.s6a.pur", topic)
			return json.Marshal(map[string]interface{}{
				"result_code": 2001,
				"pua_flags":   wantPUAFlags,
			})
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestPUR()

	err := gw.handleRequest(conn, msg, "PUR", "diameter.s6a.pur")
	assert.NoError(t, err)

	ans := decodeWrittenAnswer(t, conn.getWrittenBytes())
	assert.Equal(t, uint32(321), ans.Header.CommandCode)

	rc, err := ans.FindAVP(avp.ResultCode, 0)
	require.NoError(t, err)
	assert.Equal(t, datatype.Unsigned32(2001), rc.Data.(datatype.Unsigned32))

	flags, err := ans.FindAVP(avp.PUAFlags, 10415)
	require.NoError(t, err, "PUA must carry PUA-Flags when non-zero")
	assert.Equal(t, datatype.Unsigned32(wantPUAFlags), flags.Data.(datatype.Unsigned32))
}

func TestTestableGateway_HandleULR_NATSError(t *testing.T) {
	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			return nil, errors.New("NATS timeout")
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestULR()

	err := gw.handleRequest(conn, msg, "ULR", "diameter.s6a.ulr")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NATS timeout")
	assert.True(t, conn.wasWriteCalled(), "Should write error response")
}

func TestTestableGateway_HandleULR_CircuitBreakerError(t *testing.T) {
	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			return nil, &nats.CircuitOpenError{Subject: topic, State: "open"}
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestULR()

	err := gw.handleRequest(conn, msg, "ULR", "diameter.s6a.ulr")

	assert.Error(t, err)
	assert.True(t, nats.IsCircuitOpen(err))
}

func TestTestableGateway_HandleECR_Success(t *testing.T) {
	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			assert.Equal(t, "diameter.s13.ecr", topic)
			return json.Marshal(map[string]interface{}{"result_code": 2001, "status": 0})
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestECR()

	err := gw.handleRequest(conn, msg, "ECR", "diameter.s13.ecr")

	assert.NoError(t, err)
	assert.True(t, conn.wasWriteCalled())
}

func TestTestableGateway_HandleECR_NATSError(t *testing.T) {
	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			return nil, errors.New("NATS not connected")
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()
	msg := buildTestECR()

	err := gw.handleRequest(conn, msg, "ECR", "diameter.s13.ecr")

	assert.Error(t, err)
	assert.True(t, conn.wasWriteCalled())
}

func TestTestableGateway_PeerRegistration(t *testing.T) {
	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			return json.Marshal(map[string]interface{}{"result_code": 2001})
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn := newMockDiamConn()

	// Process multiple messages from same peer
	msg1 := buildTestULR()
	_ = gw.handleRequest(conn, msg1, "ULR", "diameter.s6a.ulr")

	msg2 := buildTestECR()
	_ = gw.handleRequest(conn, msg2, "ECR", "diameter.s13.ecr")

	// Verify peer is registered
	active := pm.GetActiveConnections()
	assert.Len(t, active, 1, "Same peer should be registered once")
}

func TestTestableGateway_DifferentPeers(t *testing.T) {
	pm := diameter.NewPeerManager()
	mockNats := &mockNatsClient{
		requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
			return json.Marshal(map[string]interface{}{"result_code": 2001})
		},
	}
	gw := newTestableGatewayService(mockNats, pm)
	conn1 := newMockDiamConn()
	conn2 := newMockDiamConn()

	// Message from first peer
	msg1 := diam.NewRequest(316, 16777251, nil)
	msg1.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("sess1"))
	msg1.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme1.test.com"))
	msg1.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))

	// Message from second peer
	msg2 := diam.NewRequest(316, 16777251, nil)
	msg2.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("sess2"))
	msg2.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme2.test.com"))
	msg2.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))

	_ = gw.handleRequest(conn1, msg1, "ULR", "diameter.s6a.ulr")
	_ = gw.handleRequest(conn2, msg2, "ULR", "diameter.s6a.ulr")

	active := pm.GetActiveConnections()
	assert.Len(t, active, 2, "Both peers should be registered")
}

// =============================================================================
// Topic Routing Tests
// =============================================================================

func TestTestableGateway_TopicRouting(t *testing.T) {
	testCases := []struct {
		name          string
		cmd           string
		expectedTopic string
		buildMsg      func() *diam.Message
	}{
		{"ULR routes to s6a.ulr", "ULR", "diameter.s6a.ulr", buildTestULR},
		{"AIR routes to s6a.air", "AIR", "diameter.s6a.air", buildTestULR},
		{"PUR routes to s6a.pur", "PUR", "diameter.s6a.pur", buildTestULR},
		{"NOR routes to s6a.nor", "NOR", "diameter.s6a.nor", buildTestULR},
		{"ECR routes to s13.ecr", "ECR", "diameter.s13.ecr", buildTestECR},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := diameter.NewPeerManager()
			var receivedTopic string
			mockNats := &mockNatsClient{
				requestFunc: func(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
					receivedTopic = topic
					return json.Marshal(map[string]interface{}{"result_code": 2001})
				},
			}
			gw := newTestableGatewayService(mockNats, pm)
			conn := newMockDiamConn()
			msg := tc.buildMsg()

			_ = gw.handleRequest(conn, msg, tc.cmd, tc.expectedTopic)

			assert.Equal(t, tc.expectedTopic, receivedTopic)
		})
	}
}
