// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// =============================================================================
// Translator Benchmarks
// =============================================================================

func BenchmarkTranslator_ToS6aEvent(b *testing.B) {
	translator := NewTranslator()
	msg := buildBenchmarkULR()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = translator.ToS6aEvent(msg, "trace-123")
	}
}

func BenchmarkTranslator_ToS6aEvent_WithAllAVPs(b *testing.B) {
	translator := NewTranslator()
	msg := buildBenchmarkULR()
	// Add all optional AVPs
	msg.NewAVP(avp.RATType, avp.Mbit|avp.Vbit, 10415, datatype.Enumerated(1004))
	msg.NewAVP(avp.ULRFlags, avp.Mbit|avp.Vbit, 10415, datatype.Unsigned32(7))
	msg.NewAVP(avp.VisitedPLMNID, avp.Mbit|avp.Vbit, 10415, datatype.OctetString([]byte{0x28, 0x60, 0x1F}))

	terminalInfo := &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.IMEI, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("123456789012345")),
			diam.NewAVP(avp.SoftwareVersion, avp.Mbit|avp.Vbit, 10415, datatype.UTF8String("01")),
		},
	}
	msg.NewAVP(avp.TerminalInformation, avp.Mbit|avp.Vbit, 10415, terminalInfo)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = translator.ToS6aEvent(msg, "trace-123")
	}
}

func BenchmarkTranslator_ToS13Event(b *testing.B) {
	translator := NewTranslator()
	msg := buildBenchmarkECR()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = translator.ToS13Event(msg, "trace-123")
	}
}

func BenchmarkTranslator_MaskIMSI(b *testing.B) {
	translator := NewTranslator()
	imsi := "286011234567890"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = translator.MaskIMSI(imsi)
	}
}

func BenchmarkTranslator_CreateCLR(b *testing.B) {
	translator := NewTranslator()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = translator.CreateCLR(
			"286011234567890",
			"mme.dest.3gppnetwork.org",
			"dest.3gppnetwork.org",
			"hss.origin.3gppnetwork.org",
			"origin.3gppnetwork.org",
			0,
		)
	}
}

func BenchmarkTranslator_CreateErrorAnswer(b *testing.B) {
	translator := NewTranslator()
	msg := buildBenchmarkULR()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = translator.CreateErrorAnswer(msg, 2001)
	}
}

// =============================================================================
// PeerManager Benchmarks
// =============================================================================

func BenchmarkPeerManager_Register(b *testing.B) {
	pm := NewPeerManager()
	conn := newMockConn()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pm.Register(conn, "mme.test.com")
	}
}

func BenchmarkPeerManager_GetConnection(b *testing.B) {
	pm := NewPeerManager()
	conn := newMockConn()
	pm.Register(conn, "mme.test.com")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pm.GetConnection("mme.test.com")
	}
}

func BenchmarkPeerManager_GetConnection_NotFound(b *testing.B) {
	pm := NewPeerManager()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pm.GetConnection("nonexistent.test.com")
	}
}

func BenchmarkPeerManager_GetActiveConnections_10Peers(b *testing.B) {
	pm := NewPeerManager()
	for i := 0; i < 10; i++ {
		pm.Register(newMockConn(), string(rune('a'+i))+".test.com")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pm.GetActiveConnections()
	}
}

func BenchmarkPeerManager_GetActiveConnections_100Peers(b *testing.B) {
	pm := NewPeerManager()
	for i := 0; i < 100; i++ {
		pm.Register(newMockConn(), string(rune(i%26+'a'))+string(rune(i/26+'a'))+".test.com")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pm.GetActiveConnections()
	}
}

// =============================================================================
// Concurrent Access Benchmarks
// =============================================================================

func BenchmarkPeerManager_ConcurrentRegister(b *testing.B) {
	pm := NewPeerManager()

	b.RunParallel(func(pb *testing.PB) {
		conn := newMockConn()
		i := 0
		for pb.Next() {
			pm.Register(conn, string(rune(i%26+'a'))+".test.com")
			i++
		}
	})
}

func BenchmarkPeerManager_ConcurrentRead(b *testing.B) {
	pm := NewPeerManager()
	for i := 0; i < 50; i++ {
		pm.Register(newMockConn(), string(rune(i%26+'a'))+".test.com")
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = pm.GetConnection(string(rune(i%26+'a')) + ".test.com")
			i++
		}
	})
}

func BenchmarkPeerManager_ConcurrentMixed(b *testing.B) {
	pm := NewPeerManager()
	for i := 0; i < 50; i++ {
		pm.Register(newMockConn(), string(rune(i%26+'a'))+".test.com")
	}

	b.RunParallel(func(pb *testing.PB) {
		conn := newMockConn()
		i := 0
		for pb.Next() {
			if i%3 == 0 {
				pm.Register(conn, string(rune(i%26+'a'))+".test.com")
			} else {
				_ = pm.GetConnection(string(rune(i%26+'a')) + ".test.com")
			}
			i++
		}
	})
}

// =============================================================================
// Helper Functions for Benchmarks
// =============================================================================

func buildBenchmarkULR() *diam.Message {
	m := diam.NewRequest(316, 16777251, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("bench-session-123"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.bench.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("bench.3gppnetwork.org"))
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity("hss.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}

func buildBenchmarkECR() *diam.Message {
	m := diam.NewRequest(324, 16777252, nil)
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String("bench-ecr-session"))
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity("mme.bench.3gppnetwork.org"))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity("bench.3gppnetwork.org"))
	m.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String("286011234567890"))
	return m
}
