// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"fmt"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// Handler handles Gx protocol messages (Policy and Charging Control)
type Handler struct {
	natsClient  nats.Requester
	translator  *Translator
	peerManager *diameter.PeerManager
	registry    *GxSessionRegistry // optional; when nil, reverse-direction routing falls back to event.OriginHost hint
	timeout     time.Duration
}

// NewHandler creates a new Gx handler
func NewHandler(nc nats.Requester, pm *diameter.PeerManager, timeout time.Duration) *Handler {
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	return &Handler{
		natsClient:  nc,
		translator:  NewTranslator(),
		peerManager: pm,
		timeout:     timeout,
	}
}

// WithSessionRegistry wires a GxSessionRegistry so CCR-I/CCR-U Bind the
// Session-Id → PCEF Origin-Host mapping that PCRF→PCEF reverse-direction
// (RAR/ASR) needs to resolve target peers. Optional — without a registry
// reverse routing relies entirely on event.OriginHost hints from the PCRF.
func (h *Handler) WithSessionRegistry(r *GxSessionRegistry) *Handler {
	h.registry = r
	return h
}

// HandleCCR handles Credit-Control-Request messages for Gx interface
func (h *Handler) HandleCCR(c diam.Conn, m *diam.Message) {
	startTime := time.Now()
	traceID := fmt.Sprintf("gx-%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	// Metrics
	monitor.IncrementCounter("diameter_gx_requests_total", map[string]string{"type": "CCR"})

	// Parse CCR to GxEvent
	event, err := h.translator.ToCCREvent(m, traceID)
	if err != nil {
		logger.Error("Gx CCR translation failed", err,
			logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, diam.InvalidAVPValue, traceID)
		return
	}

	// Log request
	ccTypeName := CCRequestTypeName(event.CCRequestType)
	imsi := h.translator.GetIMSI(event)
	maskedIMSI := h.translator.MaskIMSI(imsi)
	apn := event.CalledStationID

	logger.Info("Gx CCR received",
		logger.String("trace_id", traceID),
		logger.String("cc_type", ccTypeName),
		logger.String("imsi", maskedIMSI),
		logger.String("apn", apn),
		logger.Int("cc_request_number", int(event.CCRequestNumber)),
		logger.Int("event_triggers", len(event.EventTrigger)),
	)

	// The peer was registered at CER; the request's Origin-Host does not
	// re-bind it. The session is bound to the identity of the connection the
	// CCR arrived on, so RAR/ASR go back over that connection.
	h.peerManager.Touch(c)
	pcef := h.peerManager.HostOf(c)

	// Maintain Session-Id → PCEF binding so PCRF→PCEF reverse direction
	// (RAR/ASR) can route by Session-Id (TS 29.212 §4.5.1). Bind on INITIAL
	// and refresh LastSeen on UPDATE; Unbind on TERMINATION. Skipped when
	// no registry is wired (registry is optional — see WithSessionRegistry).
	if h.registry != nil && pcef != "" && event.Header.SessionID != "" {
		switch event.CCRequestType {
		case charging.CCRequestTypeInitial, charging.CCRequestTypeUpdate:
			h.registry.Bind(event.Header.SessionID, pcef)
		case charging.CCRequestTypeTermination:
			h.registry.Unbind(event.Header.SessionID, pcef)
		}
	}

	// Route to NATS
	respData, err := h.natsClient.Request(TopicGxCCR, event, h.timeout)
	if err != nil {
		logger.Error("Gx NATS request failed", err,
			logger.String("trace_id", traceID),
			logger.String("cc_type", ccTypeName),
		)

		// Check if circuit breaker is open
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_gx_circuit_breaker_open", nil)
		}

		h.sendErrorAnswer(c, m, diam.UnableToDeliver, traceID)
		return
	}

	// Parse response
	answer, err := h.translator.ToJSONAnswer(respData)
	if err != nil {
		logger.Error("Gx answer parse failed", err,
			logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, diam.UnableToDeliver, traceID)
		return
	}

	// Echo CC-Request-Type and Number
	answer.CCRequestType = event.CCRequestType
	answer.CCRequestNumber = event.CCRequestNumber

	// Build CCA
	cca, err := h.translator.ToCCAMessage(answer, m)
	if err != nil {
		logger.Error("Gx CCA build failed", err,
			logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, diam.UnableToDeliver, traceID)
		return
	}

	// Send CCA
	if err := diameter.SendAnswer(c, cca, m, h.peerManager); err != nil {
		logger.Error("Gx CCA write failed", err,
			logger.String("trace_id", traceID))
		return
	}

	// Metrics
	duration := time.Since(startTime)
	resultStr := "success"
	if answer.ResultCode != diam.Success {
		resultStr = fmt.Sprintf("%d", answer.ResultCode)
	}

	monitor.ObserveHistogram("diameter_gx_request_duration_seconds", duration.Seconds(),
		map[string]string{"type": ccTypeName, "result": resultStr})

	// Log rules installed/removed
	rulesInstalled := len(answer.ChargingRuleInstall)
	rulesRemoved := len(answer.ChargingRuleRemove)

	logger.Info("Gx CCA sent",
		logger.String("trace_id", traceID),
		logger.String("cc_type", ccTypeName),
		logger.Int("result_code", int(answer.ResultCode)),
		logger.Int("rules_installed", rulesInstalled),
		logger.Int("rules_removed", rulesRemoved),
		logger.String("duration", duration.String()),
	)
}

// sendErrorAnswer sends an error CCA
func (h *Handler) sendErrorAnswer(c diam.Conn, req *diam.Message, resultCode uint32, traceID string) {
	// Get CC-Request-Type and Number from request for echo
	var ccType, ccNum uint32
	if a := h.findAVP(req, 416, 0); a != nil {
		ccType = uint32(a.Data.(datatype.Enumerated))
	}
	if a := h.findAVP(req, 415, 0); a != nil {
		ccNum = uint32(a.Data.(datatype.Unsigned32))
	}

	ans := req.Answer(resultCode)
	ans.NewAVP(416, avp.Mbit, 0, datatype.Enumerated(ccType))
	ans.NewAVP(415, avp.Mbit, 0, datatype.Unsigned32(ccNum))

	if err := diameter.SendAnswer(c, ans, req, h.peerManager); err != nil {
		logger.Error("Gx error answer write failed", err,
			logger.String("trace_id", traceID))
	}

	monitor.IncrementCounter("diameter_gx_errors_total",
		map[string]string{"result_code": fmt.Sprintf("%d", resultCode)})
}

// findAVP searches for an AVP by code and vendor
func (h *Handler) findAVP(m *diam.Message, code, vendorID uint32) *diam.AVP {
	for _, a := range m.AVP {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
	}
	return nil
}

// AppID returns the application ID for Gx
func AppID() uint32 {
	return charging.AppIDGx
}
