// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"fmt"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// NATS Topics for SGd interface
const (
	TopicSGdOFR = "diameter.sgd.ofr" // MO-Forward-Short-Message
	TopicSGdTFR = "diameter.sgd.tfr" // MT-Forward-Short-Message
	TopicSGdSRR = "diameter.sgd.srr" // Send-Routing-Info-for-SM
	TopicSGdALR = "diameter.sgd.alr" // Alert-Service-Centre
)

// Handler handles SGd protocol messages (TS 29.338)
type Handler struct {
	natsClient  nats.Requester
	translator  *Translator
	peerManager *diameter.PeerManager
	timeout     time.Duration
}

// NewHandler creates a new SGd handler
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

// HandleOFR handles MO-Forward-Short-Message-Request (Mobile Originated SMS)
func (h *Handler) HandleOFR(c diam.Conn, m *diam.Message) {
	startTime := time.Now()
	traceID := fmt.Sprintf("sgd-ofr-%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	monitor.IncrementCounter("diameter_sgd_requests_total", map[string]string{"type": "OFR"})

	// Parse OFR to event
	event, err := h.translator.ToOFREvent(m, traceID)
	if err != nil {
		logger.Error("SGd OFR translation failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandOFR, diam.InvalidAVPValue, traceID)
		return
	}

	// Log request
	msisdn := h.translator.FormatMSISDN(event.UserIdentifier)
	logger.Info("SGd OFR received (MO-SMS)",
		logger.String("trace_id", traceID),
		logger.String("msisdn", h.translator.MaskMSISDN(msisdn)),
		logger.String("sc_address", event.SCAddress),
		logger.Int("pdu_length", len(event.SMRPUI)),
	)

	// The peer was registered at CER; the request's Origin-Host does not re-bind it.
	h.peerManager.Touch(c)

	// Route to NATS (SMSC)
	respData, err := h.natsClient.Request(TopicSGdOFR, event, h.timeout)
	if err != nil {
		logger.Error("SGd OFR NATS request failed", err, logger.String("trace_id", traceID))
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_sgd_circuit_breaker_open", nil)
		}
		h.sendErrorAnswer(c, m, CommandOFR, diam.UnableToDeliver, traceID)
		return
	}

	// Parse response
	answer, err := h.translator.ToOFAAnswer(respData)
	if err != nil {
		logger.Error("SGd OFA parse failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandOFR, diam.UnableToDeliver, traceID)
		return
	}

	// Build and send OFA
	ofa, err := h.translator.ToOFAMessage(answer, m)
	if err != nil {
		logger.Error("SGd OFA build failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandOFR, diam.UnableToDeliver, traceID)
		return
	}

	if err := diameter.SendAnswer(c, ofa, m, h.peerManager); err != nil {
		logger.Error("SGd OFA write failed", err, logger.String("trace_id", traceID))
		return
	}

	// Metrics
	duration := time.Since(startTime)
	resultStr := resultString(answer.ResultCode)
	monitor.ObserveHistogram("diameter_sgd_request_duration_seconds", duration.Seconds(),
		map[string]string{"type": "OFR", "result": resultStr})

	logger.Info("SGd OFA sent (MO-SMS response)",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
		logger.String("duration", duration.String()),
	)
}

// HandleTFR handles MT-Forward-Short-Message-Request (Mobile Terminated SMS)
func (h *Handler) HandleTFR(c diam.Conn, m *diam.Message) {
	startTime := time.Now()
	traceID := fmt.Sprintf("sgd-tfr-%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	monitor.IncrementCounter("diameter_sgd_requests_total", map[string]string{"type": "TFR"})

	// Parse TFR to event
	event, err := h.translator.ToTFREvent(m, traceID)
	if err != nil {
		logger.Error("SGd TFR translation failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandTFR, diam.InvalidAVPValue, traceID)
		return
	}

	// Log request
	msisdn := h.translator.FormatMSISDN(event.UserIdentifier)
	logger.Info("SGd TFR received (MT-SMS)",
		logger.String("trace_id", traceID),
		logger.String("msisdn", h.translator.MaskMSISDN(msisdn)),
		logger.String("mme_name", event.MMEName),
		logger.String("sc_address", event.SCAddress),
		logger.Int("pdu_length", len(event.SMRPUI)),
	)

	// The peer was registered at CER; the request's Origin-Host does not re-bind it.
	h.peerManager.Touch(c)

	// Route to NATS (MME via reverse path)
	respData, err := h.natsClient.Request(TopicSGdTFR, event, h.timeout)
	if err != nil {
		logger.Error("SGd TFR NATS request failed", err, logger.String("trace_id", traceID))
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_sgd_circuit_breaker_open", nil)
		}
		h.sendErrorAnswer(c, m, CommandTFR, diam.UnableToDeliver, traceID)
		return
	}

	// Parse response
	answer, err := h.translator.ToTFAAnswer(respData)
	if err != nil {
		logger.Error("SGd TFA parse failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandTFR, diam.UnableToDeliver, traceID)
		return
	}

	// Build and send TFA
	tfa, err := h.translator.ToTFAMessage(answer, m)
	if err != nil {
		logger.Error("SGd TFA build failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandTFR, diam.UnableToDeliver, traceID)
		return
	}

	if err := diameter.SendAnswer(c, tfa, m, h.peerManager); err != nil {
		logger.Error("SGd TFA write failed", err, logger.String("trace_id", traceID))
		return
	}

	// Metrics
	duration := time.Since(startTime)
	resultStr := resultString(answer.ResultCode)
	monitor.ObserveHistogram("diameter_sgd_request_duration_seconds", duration.Seconds(),
		map[string]string{"type": "TFR", "result": resultStr})

	logger.Info("SGd TFA sent (MT-SMS response)",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
		logger.String("duration", duration.String()),
	)
}

// HandleSRR handles Send-Routing-Info-for-SM-Request
func (h *Handler) HandleSRR(c diam.Conn, m *diam.Message) {
	startTime := time.Now()
	traceID := fmt.Sprintf("sgd-srr-%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	monitor.IncrementCounter("diameter_sgd_requests_total", map[string]string{"type": "SRR"})

	// Parse SRR to event
	event, err := h.translator.ToSRREvent(m, traceID)
	if err != nil {
		logger.Error("SGd SRR translation failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandSRR, diam.InvalidAVPValue, traceID)
		return
	}

	// Log request
	msisdn := h.translator.FormatMSISDNBytes(event.MSISDN)
	logger.Info("SGd SRR received (Routing Info)",
		logger.String("trace_id", traceID),
		logger.String("msisdn", h.translator.MaskMSISDN(msisdn)),
		logger.String("sc_address", event.SCAddress),
	)

	// The peer was registered at CER; the request's Origin-Host does not re-bind it.
	h.peerManager.Touch(c)

	// Route to NATS (HSS/HLR)
	respData, err := h.natsClient.Request(TopicSGdSRR, event, h.timeout)
	if err != nil {
		logger.Error("SGd SRR NATS request failed", err, logger.String("trace_id", traceID))
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_sgd_circuit_breaker_open", nil)
		}
		h.sendErrorAnswer(c, m, CommandSRR, diam.UnableToDeliver, traceID)
		return
	}

	// Parse response
	answer, err := h.translator.ToSRAAnswer(respData)
	if err != nil {
		logger.Error("SGd SRA parse failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandSRR, diam.UnableToDeliver, traceID)
		return
	}

	// Build and send SRA
	sra, err := h.translator.ToSRAMessage(answer, m)
	if err != nil {
		logger.Error("SGd SRA build failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandSRR, diam.UnableToDeliver, traceID)
		return
	}

	if err := diameter.SendAnswer(c, sra, m, h.peerManager); err != nil {
		logger.Error("SGd SRA write failed", err, logger.String("trace_id", traceID))
		return
	}

	// Metrics
	duration := time.Since(startTime)
	resultStr := resultString(answer.ResultCode)
	monitor.ObserveHistogram("diameter_sgd_request_duration_seconds", duration.Seconds(),
		map[string]string{"type": "SRR", "result": resultStr})

	logger.Info("SGd SRA sent (Routing Info response)",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
		logger.String("mme_name", answer.MMEName),
		logger.String("duration", duration.String()),
	)
}

// HandleALR handles Alert-Service-Centre-Request
func (h *Handler) HandleALR(c diam.Conn, m *diam.Message) {
	startTime := time.Now()
	traceID := fmt.Sprintf("sgd-alr-%d-%d", m.Header.HopByHopID, m.Header.EndToEndID)

	monitor.IncrementCounter("diameter_sgd_requests_total", map[string]string{"type": "ALR"})

	// Parse ALR to event
	event, err := h.translator.ToALREvent(m, traceID)
	if err != nil {
		logger.Error("SGd ALR translation failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandALR, diam.InvalidAVPValue, traceID)
		return
	}

	// Log request
	msisdn := h.translator.FormatMSISDN(event.UserIdentifier)
	logger.Info("SGd ALR received (Alert SC)",
		logger.String("trace_id", traceID),
		logger.String("msisdn", h.translator.MaskMSISDN(msisdn)),
		logger.String("sc_address", event.SCAddress),
	)

	// The peer was registered at CER; the request's Origin-Host does not re-bind it.
	h.peerManager.Touch(c)

	// Route to NATS (SMSC)
	respData, err := h.natsClient.Request(TopicSGdALR, event, h.timeout)
	if err != nil {
		logger.Error("SGd ALR NATS request failed", err, logger.String("trace_id", traceID))
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_sgd_circuit_breaker_open", nil)
		}
		h.sendErrorAnswer(c, m, CommandALR, diam.UnableToDeliver, traceID)
		return
	}

	// Parse response
	answer, err := h.translator.ToALAAnswer(respData)
	if err != nil {
		logger.Error("SGd ALA parse failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandALR, diam.UnableToDeliver, traceID)
		return
	}

	// Build and send ALA
	ala, err := h.translator.ToALAMessage(answer, m)
	if err != nil {
		logger.Error("SGd ALA build failed", err, logger.String("trace_id", traceID))
		h.sendErrorAnswer(c, m, CommandALR, diam.UnableToDeliver, traceID)
		return
	}

	if err := diameter.SendAnswer(c, ala, m, h.peerManager); err != nil {
		logger.Error("SGd ALA write failed", err, logger.String("trace_id", traceID))
		return
	}

	// Metrics
	duration := time.Since(startTime)
	resultStr := resultString(answer.ResultCode)
	monitor.ObserveHistogram("diameter_sgd_request_duration_seconds", duration.Seconds(),
		map[string]string{"type": "ALR", "result": resultStr})

	logger.Info("SGd ALA sent (Alert SC response)",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
		logger.String("duration", duration.String()),
	)
}

// sendErrorAnswer sends an error answer for the given command
func (h *Handler) sendErrorAnswer(c diam.Conn, req *diam.Message, cmdCode, resultCode uint32, traceID string) {
	ans := req.Answer(resultCode)

	// Add Vendor-Specific-Application-Id
	vsaiAVP := diam.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDSGd)),
		},
	})
	ans.AVP = append(ans.AVP, vsaiAVP)

	if err := diameter.SendAnswer(c, ans, req, h.peerManager); err != nil {
		logger.Error("SGd error answer write failed", err, logger.String("trace_id", traceID))
	}

	cmdName := commandName(cmdCode)
	monitor.IncrementCounter("diameter_sgd_errors_total",
		map[string]string{"command": cmdName, "result_code": fmt.Sprintf("%d", resultCode)})
}

// Helper functions
func resultString(code uint32) string {
	if code == diam.Success || code == DiameterSuccessSGd {
		return "success"
	}
	return fmt.Sprintf("%d", code)
}

func commandName(code uint32) string {
	switch code {
	case CommandOFR:
		return "OFR"
	case CommandTFR:
		return "TFR"
	case CommandSRR:
		return "SRR"
	case CommandALR:
		return "ALR"
	default:
		return "UNKNOWN"
	}
}
