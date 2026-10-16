// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package ro

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

// Handler handles Ro protocol messages (IMS Offline Charging)
type Handler struct {
	natsClient  nats.Requester
	translator  *Translator
	peerManager *diameter.PeerManager
	timeout     time.Duration
}

// NewHandler creates a new Ro handler
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

// HandleCCR handles Credit-Control-Request messages for Ro interface
func (h *Handler) HandleCCR(c diam.Conn, m *diam.Message) {
	startTime := time.Now()
	traceID := fmt.Sprintf("ro-%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	// Metrics
	monitor.IncrementCounter("diameter_ro_requests_total", map[string]string{"type": "CCR"})

	// Parse CCR to RoEvent
	event, err := h.translator.ToCCREvent(m, traceID)
	if err != nil {
		logger.Error("Ro CCR translation failed", err,
			logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, diam.InvalidAVPValue, traceID)
		return
	}

	// Log request
	ccTypeName := CCRequestTypeName(event.CCRequestType)
	imsi := h.translator.GetIMSI(event)
	maskedIMSI := h.translator.MaskIMSI(imsi)
	callingParty := h.translator.GetCallingParty(event)

	// IMS event type if available
	var eventTypeName string
	if event.ServiceInformation != nil && event.ServiceInformation.IMSInformation != nil {
		eventTypeName = EventTypeName(event.ServiceInformation.IMSInformation.EventType)
	}

	logger.Info("Ro CCR received",
		logger.String("trace_id", traceID),
		logger.String("cc_type", ccTypeName),
		logger.String("imsi", maskedIMSI),
		logger.String("calling_party", callingParty),
		logger.String("ims_event", eventTypeName),
		logger.Int("cc_request_number", int(event.CCRequestNumber)),
		logger.Int("mscc_count", len(event.MSCC)),
	)

	// The peer was registered at CER; the request's Origin-Host does not re-bind it.
	h.peerManager.Touch(c)

	// Route to NATS
	respData, err := h.natsClient.Request(TopicRoCCR, event, h.timeout)
	if err != nil {
		logger.Error("Ro NATS request failed", err,
			logger.String("trace_id", traceID),
			logger.String("cc_type", ccTypeName),
		)

		// Check if circuit breaker is open
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_ro_circuit_breaker_open", nil)
		}

		h.sendErrorAnswer(c, m, diam.UnableToDeliver, traceID)
		return
	}

	// Parse response
	answer, err := h.translator.ToJSONAnswer(respData)
	if err != nil {
		logger.Error("Ro answer parse failed", err,
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
		logger.Error("Ro CCA build failed", err,
			logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, diam.UnableToDeliver, traceID)
		return
	}

	// Send CCA
	if err := diameter.SendAnswer(c, cca, m, h.peerManager); err != nil {
		logger.Error("Ro CCA write failed", err,
			logger.String("trace_id", traceID))
		return
	}

	// Metrics
	duration := time.Since(startTime)
	resultStr := "success"
	if answer.ResultCode != diam.Success {
		resultStr = fmt.Sprintf("%d", answer.ResultCode)
	}

	monitor.ObserveHistogram("diameter_ro_request_duration_seconds", duration.Seconds(),
		map[string]string{"type": ccTypeName, "result": resultStr})

	logger.Info("Ro CCA sent",
		logger.String("trace_id", traceID),
		logger.String("cc_type", ccTypeName),
		logger.Int("result_code", int(answer.ResultCode)),
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
		logger.Error("Ro error answer write failed", err,
			logger.String("trace_id", traceID))
	}

	monitor.IncrementCounter("diameter_ro_errors_total",
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

// ServiceContextID returns the service context ID for Ro
func ServiceContextID() string {
	return charging.ServiceContextIDRo
}

// AppID returns the application ID for Credit-Control
func AppID() uint32 {
	return charging.AppIDCreditControl
}
