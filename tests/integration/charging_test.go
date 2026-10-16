// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/gx"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/gy"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/ro"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/fiorix/go-diameter/v4/diam/sm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock NATS Client for Integration Tests
// =============================================================================

// MockNATSClient simulates NATS for integration testing
type MockNATSClient struct {
	mu        sync.Mutex
	responses map[string][]byte
	errors    map[string]error
	requests  []MockRequest
}

// MockRequest captures request details for verification
type MockRequest struct {
	Subject string
	Data    interface{}
}

func NewMockNATSClient() *MockNATSClient {
	return &MockNATSClient{
		responses: make(map[string][]byte),
		errors:    make(map[string]error),
		requests:  make([]MockRequest, 0),
	}
}

func (m *MockNATSClient) Request(subject string, data interface{}, timeout time.Duration) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, MockRequest{Subject: subject, Data: data})

	if err, ok := m.errors[subject]; ok {
		return nil, err
	}

	if resp, ok := m.responses[subject]; ok {
		return resp, nil
	}

	return nil, errors.New("no mock response configured for subject: " + subject)
}

func (m *MockNATSClient) SetResponse(subject string, response interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, _ := json.Marshal(response)
	m.responses[subject] = data
}

func (m *MockNATSClient) SetError(subject string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[subject] = err
}

func (m *MockNATSClient) GetRequests() []MockRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *MockNATSClient) ClearRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = make([]MockRequest, 0)
}

// =============================================================================
// Gy Integration Tests
// =============================================================================

func TestGyIntegration_InitialRequest(t *testing.T) {
	// Setup mock NATS
	mockNATS := NewMockNATSClient()
	mockNATS.SetResponse(gy.TopicGyCCR, &gy.GyAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup: 100,
				GrantedServiceUnit: &charging.ServiceUnit{
					CCTotalOctets: 10485760,
					CCTime:        3600,
				},
				ResultCode: diam.Success,
			},
		},
	})

	// Create handler
	pm := diameter.NewPeerManager()
	handler := gy.NewHandler(mockNATS, pm, 2*time.Second)

	// Setup test server
	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:  "127.0.0.1:3881",
			OriginHost:  "ocs.test.com",
			OriginRealm: "test.com",
			ProductName: "TestOCS",
		},
	}

	server := diameter.NewServer(cfg, &MockHandler{}, nil, pm)
	server.SetChargingHandler(handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	// Connect client with CER/CEA handshake (Credit-Control App ID = 4)
	c := createDiameterClient(t, "127.0.0.1:3881", 4)
	defer c.Close()

	// Build CCR-Initial
	ccr := diam.NewRequest(272, 4, dict.Default)
	ccr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("gy-session-1"))
	ccr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.test.com"))
	ccr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(charging.CCRequestTypeInitial))
	ccr.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))

	// Add Subscription-ID
	addSubscriptionID(ccr, charging.SubscriptionIDTypeEndUserIMSI, "262021234567890")

	// Send CCR
	_, err := ccr.WriteTo(c)
	require.NoError(t, err, "Failed to send CCR")

	// Wait for processing
	time.Sleep(300 * time.Millisecond)

	// Verify NATS request was made
	requests := mockNATS.GetRequests()
	require.Len(t, requests, 1, "Expected one NATS request")
	assert.Equal(t, gy.TopicGyCCR, requests[0].Subject)
}

func TestGyIntegration_TerminationRequest(t *testing.T) {
	mockNATS := NewMockNATSClient()
	mockNATS.SetResponse(gy.TopicGyCCR, &gy.GyAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeTermination,
		CCRequestNumber: 2,
	})

	pm := diameter.NewPeerManager()
	handler := gy.NewHandler(mockNATS, pm, 2*time.Second)

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:  "127.0.0.1:3882",
			OriginHost:  "ocs.test.com",
			OriginRealm: "test.com",
			ProductName: "TestOCS",
		},
	}

	server := diameter.NewServer(cfg, &MockHandler{}, nil, pm)
	server.SetChargingHandler(handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	c := createDiameterClient(t, "127.0.0.1:3882", 4)
	defer c.Close()

	// Build CCR-Termination
	ccr := diam.NewRequest(272, 4, dict.Default)
	ccr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("gy-session-term"))
	ccr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.test.com"))
	ccr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(charging.CCRequestTypeTermination))
	ccr.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(2))
	ccr.NewAVP(295, avp.Mbit, 0, datatype.Enumerated(charging.TermCauseDiameterLogout))

	addSubscriptionID(ccr, charging.SubscriptionIDTypeEndUserIMSI, "262021234567890")

	_, err := ccr.WriteTo(c)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	requests := mockNATS.GetRequests()
	require.Len(t, requests, 1)
	assert.Equal(t, gy.TopicGyCCR, requests[0].Subject)
}

// =============================================================================
// Gx Integration Tests
// =============================================================================

func TestGxIntegration_InitialAttach(t *testing.T) {
	mockNATS := NewMockNATSClient()
	mockNATS.SetResponse(gx.TopicGxCCR, &gx.GxAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeInitial,
		CCRequestNumber: 0,
		DefaultEPSBearerQoS: &gx.DefaultEPSBearerQoS{
			QoSClassIdentifier: 9,
			AllocationRetentionPriority: &gx.AllocationRetentionPriority{
				PriorityLevel: 8,
			},
		},
		ChargingRuleInstall: []gx.ChargingRuleInstall{
			{
				ChargingRuleName: []string{"default-internet"},
			},
		},
	})

	pm := diameter.NewPeerManager()
	handler := gx.NewHandler(mockNATS, pm, 2*time.Second)

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:  "127.0.0.1:3883",
			OriginHost:  "pcrf.test.com",
			OriginRealm: "test.com",
			ProductName: "TestPCRF",
		},
	}

	server := diameter.NewServer(cfg, &MockHandler{}, nil, pm)
	server.SetChargingHandler(handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	// Gx uses Auth-Application-ID 16777238, but dictionary uses Credit-Control (4)
	// The CCR is command 272, which is Credit-Control in the default dictionary
	c := createDiameterClient(t, "127.0.0.1:3883", 4)
	defer c.Close()

	// Build Gx CCR-Initial (we still send it as CCR, but with Gx App ID in header)
	ccr := diam.NewRequest(272, 4, dict.Default) // Use CC app for dictionary lookup
	ccr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("gx-session-1"))
	ccr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.test.com"))
	ccr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(charging.CCRequestTypeInitial))
	ccr.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))
	ccr.NewAVP(30, avp.Mbit, 0, datatype.UTF8String("internet.example.com")) // Called-Station-ID (APN)

	addSubscriptionID(ccr, charging.SubscriptionIDTypeEndUserIMSI, "262021234567890")

	_, err := ccr.WriteTo(c)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	requests := mockNATS.GetRequests()
	require.Len(t, requests, 1)
	assert.Equal(t, gx.TopicGxCCR, requests[0].Subject)
}

// =============================================================================
// Ro Integration Tests
// =============================================================================

func TestRoIntegration_IMSVoiceCall(t *testing.T) {
	mockNATS := NewMockNATSClient()
	mockNATS.SetResponse(ro.TopicRoCCR, &ro.RoAnswer{
		ResultCode:      diam.Success,
		CCRequestType:   charging.CCRequestTypeEvent,
		CCRequestNumber: 0,
		MSCC: []charging.MSCC{
			{
				RatingGroup: 100,
				GrantedServiceUnit: &charging.ServiceUnit{
					CCTime: 300,
				},
				ResultCode: diam.Success,
			},
		},
	})

	pm := diameter.NewPeerManager()
	handler := ro.NewHandler(mockNATS, pm, 2*time.Second)

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:  "127.0.0.1:3884",
			OriginHost:  "ocs.test.com",
			OriginRealm: "test.com",
			ProductName: "TestOCS",
		},
	}

	server := diameter.NewServer(cfg, &MockHandler{}, nil, pm)
	server.SetChargingHandler(handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	c := createDiameterClient(t, "127.0.0.1:3884", 4)
	defer c.Close()

	// Build Ro CCR-Event for IMS Voice Call
	ccr := diam.NewRequest(272, 4, dict.Default)
	ccr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("ro-session-1"))
	ccr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.test.com"))
	ccr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(charging.CCRequestTypeEvent))
	ccr.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))
	ccr.NewAVP(461, avp.Mbit, 0, datatype.UTF8String("32260@3gpp.org")) // Service-Context-Id (IMS)

	addSubscriptionID(ccr, charging.SubscriptionIDTypeEndUserIMSI, "262021234567890")

	_, err := ccr.WriteTo(c)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	requests := mockNATS.GetRequests()
	require.Len(t, requests, 1)
	assert.Equal(t, ro.TopicRoCCR, requests[0].Subject)
}

// =============================================================================
// Error Scenario Tests
// =============================================================================

func TestGyIntegration_NATSError(t *testing.T) {
	mockNATS := NewMockNATSClient()
	mockNATS.SetError(gy.TopicGyCCR, errors.New("timeout"))

	pm := diameter.NewPeerManager()
	handler := gy.NewHandler(mockNATS, pm, 100*time.Millisecond)

	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:  "127.0.0.1:3885",
			OriginHost:  "ocs.test.com",
			OriginRealm: "test.com",
			ProductName: "TestOCS",
		},
	}

	server := diameter.NewServer(cfg, &MockHandler{}, nil, pm)
	server.SetChargingHandler(handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go server.Start(ctx)
	time.Sleep(500 * time.Millisecond)

	c := createDiameterClient(t, "127.0.0.1:3885", 4)
	defer c.Close()

	ccr := diam.NewRequest(272, 4, dict.Default)
	ccr.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("error-session"))
	ccr.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("pgw.test.com"))
	ccr.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
	ccr.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(charging.CCRequestTypeInitial))
	ccr.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(0))

	_, err := ccr.WriteTo(c)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// Request was made but failed
	requests := mockNATS.GetRequests()
	require.Len(t, requests, 1)
}

// =============================================================================
// Helper Functions
// =============================================================================

// createDiameterClient creates a Diameter client with CER/CEA handshake
func createDiameterClient(t *testing.T, addr string, appID uint32) diam.Conn {
	// Create client state machine
	mux := sm.New(&sm.Settings{
		OriginHost:  datatype.DiameterIdentity("client.test.com"),
		OriginRealm: datatype.DiameterIdentity("test.com"),
		ProductName: datatype.UTF8String("TestClient"),
	})

	// Create sm.Client for proper CER/CEA handshake
	cli := &sm.Client{
		Dict:               dict.Default,
		Handler:            mux,
		MaxRetransmits:     1,
		RetransmitInterval: time.Second,
		AuthApplicationID: []*diam.AVP{
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(appID)),
		},
	}

	conn, err := cli.Dial(addr)
	require.NoError(t, err, "Failed to connect to %s", addr)
	return conn
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
