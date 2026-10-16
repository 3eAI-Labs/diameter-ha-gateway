// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"fmt"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
)

type GatewayService struct {
	natsClient  *nats.Client
	translator  *diameter.Translator
	peerManager *diameter.PeerManager
	// originHost / originRealm identify the gateway on outbound answers.
	// Used by BuildMissingAVPAnswer when the gateway self-rejects a
	// malformed request (req.Answer copies AVPs from the request, but a
	// request missing Origin-Host/Realm can't supply them — gateway
	// substitutes its own identity per RFC 6733 §3 mandate that every
	// answer MUST carry Origin-Host/Realm).
	originHost  string
	originRealm string
}

func NewGatewayService(nc *nats.Client, pm *diameter.PeerManager, originHost, originRealm string) *GatewayService {
	return &GatewayService{
		natsClient:  nc,
		translator:  diameter.NewTranslator(),
		peerManager: pm,
		originHost:  originHost,
		originRealm: originRealm,
	}
}

func (g *GatewayService) HandleULR(c diam.Conn, m *diam.Message) {
	g.process(c, m, "ULR", "diameter.s6a.ulr")
}

func (g *GatewayService) HandleAIR(c diam.Conn, m *diam.Message) {
	g.process(c, m, "AIR", "diameter.s6a.air")
}

func (g *GatewayService) HandlePUR(c diam.Conn, m *diam.Message) {
	g.process(c, m, "PUR", "diameter.s6a.pur")
}

func (g *GatewayService) HandleNOR(c diam.Conn, m *diam.Message) {
	g.process(c, m, "NOR", "diameter.s6a.nor")
}

func (g *GatewayService) HandleECR(c diam.Conn, m *diam.Message) {
	g.process(c, m, "ECR", "diameter.s13.ecr")
}

func (g *GatewayService) process(c diam.Conn, m *diam.Message, cmd, topic string) {
	startTime := time.Now()
	traceID := fmt.Sprintf("%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	monitor.IncrementCounter("diameter_requests_total", map[string]string{"cmd": cmd})

	// RFC 6733 §7.5 + TS 29.272 §5.2.1.x ABNF: validate mandatory
	// AVPs before burning a NATS roundtrip on a malformed request. Returns
	// the missing AVP list; we emit 5005 (DIAMETER_MISSING_AVP) with a
	// Failed-AVP grouped AVP listing every absent code so peers can fix
	// the request without a debug round.
	if missing := diameter.ValidateMandatoryAVPs(m); len(missing) > 0 {
		ans := diameter.BuildMissingAVPAnswer(m, g.originHost, g.originRealm, missing)
		diameter.SendAnswer(c, ans, m, g.peerManager)
		logger.Warn(cmd+" rejected — missing mandatory AVPs",
			logger.String("trace_id", traceID),
			logger.Int("missing_count", len(missing)))
		monitor.IncrementCounter("diameter_request_validation_rejected_total",
			map[string]string{"cmd": cmd, "reason": "missing_mandatory_avp"})
		return
	}

	var event interface{}
	var err error

	if cmd == "ECR" {
		event, err = g.translator.ToS13Event(m, traceID)
	} else {
		event, err = g.translator.ToS6aEvent(m, traceID)
	}

	if err != nil {
		logger.Error(cmd+" Translation failed", err, logger.String("trace_id", traceID))
		return
	}

	// Refresh liveness so the watchdog sees real traffic. The peer was
	// registered at CER; the request's Origin-Host does not re-bind it.
	g.peerManager.Touch(c)

	// NATS request with timeout, behind the circuit breaker
	respData, err := g.natsClient.Request(topic, event, 2*time.Second)
	if err != nil {
		logger.Error(cmd+" NATS Request failed", err, logger.String("trace_id", traceID))
		// Map to 3002 (UNABLE_TO_DELIVER)
		ans, _ := g.translator.CreateErrorAnswer(m, 3002)
		diameter.SendAnswer(c, ans, m, g.peerManager)
		return
	}

	// Successful reply handling — encode the NATS payload into a real
	// Diameter answer. Earlier behaviour discarded respData
	// and emitted a bare 2001, leaving downstream tests false-confident:
	// the answer reached the wire but carried zero application AVPs.
	logger.Info(cmd+" processed successfully",
		logger.String("trace_id", traceID),
		logger.Int("resp_len", len(respData)))

	ans, err := g.translator.ToDiameterAnswer(respData, m)
	if err != nil {
		// Deterministic failure code so callers see "backend reply was
		// malformed" rather than a silent 2001 with empty AVPs.
		logger.Error(cmd+" answer encode failed", err,
			logger.String("trace_id", traceID))
		ans, _ = g.translator.CreateErrorAnswer(m, 5012) // DIAMETER_UNABLE_TO_COMPLY
	}
	diameter.SendAnswer(c, ans, m, g.peerManager)

	monitor.ObserveHistogram("diameter_request_duration_seconds", time.Since(startTime).Seconds(), map[string]string{"cmd": cmd})
}
