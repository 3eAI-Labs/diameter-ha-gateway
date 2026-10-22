// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

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

// MockHandler implements the Handler interface for testing
type MockHandler struct{}

func (h *MockHandler) HandleULR(c diam.Conn, m *diam.Message) {
	// Negative Logic: Check Mandatory AVPs
	if _, err := m.FindAVP(avp.UserName, 0); err != nil {
		a := m.Answer(diam.MissingAVP) // 5005
		a.WriteTo(c)
		return
	}
	h.reply(c, m)
}
func (h *MockHandler) HandleAIR(c diam.Conn, m *diam.Message) { h.reply(c, m) }
func (h *MockHandler) HandlePUR(c diam.Conn, m *diam.Message) { h.reply(c, m) }
func (h *MockHandler) HandleNOR(c diam.Conn, m *diam.Message) { h.reply(c, m) }
func (h *MockHandler) HandleECR(c diam.Conn, m *diam.Message) { h.reply(c, m) }

func (h *MockHandler) reply(c diam.Conn, m *diam.Message) {
	a := m.Answer(diam.Success)
	a.WriteTo(c)
}

func TestDiameterMessages(t *testing.T) {
	// 1. Start Server
	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:  "127.0.0.1:3872", // Port incremented
			OriginHost:  "gateway.test.com",
			OriginRealm: "test.com",
			ProductName: "TestGateway",
		},
		Peers: []config.PeerConfig{{IP: "127.0.0.1", Name: "mme.client.com"}},
	}
	handler := &MockHandler{}
	pm := diameter.NewPeerManager()
	// 3. Start Server
	server := diameter.NewServer(cfg, handler, nil, pm) // No AnswerHandler for this test

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { server.Start(ctx) }()
	time.Sleep(1 * time.Second)

	// 2. Client Config
	cliCfg := &sm.Settings{
		OriginHost:  datatype.DiameterIdentity("mme.client.com"),
		OriginRealm: datatype.DiameterIdentity("test.com"),
		ProductName: datatype.UTF8String("TestClient"),
	}

	// 3. Test Scenarios
	tests := []struct {
		name             string
		cmdCode          uint32
		appID            uint32
		avps             []*diam.AVP
		expectSuccess    bool   // True if we expect a response (any response)
		expectResultCode uint32 // Expected Diameter Result-Code (2001, 5005, etc)
		knownGap         string // non-empty: the scenario cannot pass yet, and why
	}{
		// --- HAPPY PATHS ---
		{
			name:    "S6a ULR (Happy Path)",
			cmdCode: diam.UpdateLocation,
			appID:   16777251,
			avps: []*diam.AVP{
				diam.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("123456789012345")),
				diam.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004)),
				diam.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1)),
				diam.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString("28601F")),
			},
			expectSuccess:    true,
			expectResultCode: diam.Success, // 2001
		},
		{
			name:    "S13 ECR (Happy Path)",
			cmdCode: 324, // ECR
			appID:   16777252,
			avps: []*diam.AVP{
				diam.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("123456789012345")),
				diam.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, &diam.GroupedAVP{
					AVP: []*diam.AVP{
						diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("35293006225621")),
					},
				}),
			},
			expectSuccess:    true,
			expectResultCode: diam.Success,
		},

		// --- NEGATIVE PATHS ---
		{
			name:    "S6a ULR (Missing Mandatory AVP: User-Name)",
			cmdCode: diam.UpdateLocation,
			appID:   16777251,
			avps: []*diam.AVP{
				// User-Name is MISSING
				diam.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004)),
				diam.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(1)),
				diam.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString("28601F")),
			},
			expectSuccess:    true,
			expectResultCode: diam.MissingAVP, // 5005
		},
	}

	// 4. Execute Tests. The client completes the CER/CEA handshake first — the server drops
	// application requests on a connection that has not — and each answer's Result-Code is
	// checked against the scenario.
	answers := make(chan uint32, len(tests))
	mux := sm.New(cliCfg)
	onAnswer := diam.HandlerFunc(func(_ diam.Conn, m *diam.Message) {
		code := uint32(0)
		if rc, err := m.FindAVP(avp.ResultCode, 0); err == nil {
			code = uint32(rc.Data.(datatype.Unsigned32))
		}
		answers <- code
	})
	mux.HandleIdx(diam.CommandIndex{AppID: 16777251, Code: diam.UpdateLocation}, onAnswer)
	mux.HandleIdx(diam.CommandIndex{AppID: 16777252, Code: 324}, onAnswer)

	cli := &sm.Client{
		Dict:               dict.Default,
		Handler:            mux,
		MaxRetransmits:     1,
		RetransmitInterval: time.Second,
		AuthApplicationID: []*diam.AVP{
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(16777251)),
		},
	}
	c, err := cli.Dial("127.0.0.1:3872")
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer c.Close()

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.knownGap != "" {
				t.Skip("known gap: " + tt.knownGap)
			}
			m := diam.NewRequest(tt.cmdCode, tt.appID, dict.Default)
			m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(fmt.Sprintf("mme.client.com;1;%d", i)))
			m.NewAVP(avp.OriginHost, avp.Mbit, 0, cliCfg.OriginHost)
			m.NewAVP(avp.OriginRealm, avp.Mbit, 0, cliCfg.OriginRealm)
			m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("test.com"))
			for _, a := range tt.avps {
				m.AddAVP(a)
			}

			_, err := m.WriteTo(c)
			require.NoError(t, err, "Failed to send %s", tt.name)

			select {
			case got := <-answers:
				assert.Equal(t, tt.expectResultCode, got, "Result-Code of the answer to %s", tt.name)
			case <-time.After(3 * time.Second):
				t.Fatalf("no answer to %s", tt.name)
			}
		})
	}
}
