// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
	"github.com/nats-io/nats.go"
)

// NATS Topics for SMSC-initiated requests (reverse flow)
const (
	TopicSGdSRRRequest = "diameter.sgd.srr.request" // SMSC queries HSS for subscriber location
	TopicSGdTFRRequest = "diameter.sgd.tfr.request" // SMSC sends MT-SMS to MME
	TopicSGdALRRequest = "diameter.sgd.alr.request" // SMSC sends Alert to HSS
)

// Client handles SMSC-initiated Diameter requests (reverse flow)
// SMSC → NATS → Client → Diameter → HSS/MME
type Client struct {
	natsClient  *internalNats.Client
	peerManager *diameter.PeerManager
	translator  *Translator
	cfg         *config.Config
	timeout     time.Duration

	// Pending requests waiting for answers
	pendingReqs map[string]*pendingReq
	mu          sync.RWMutex

	// Sequence numbers for Hop-by-Hop and End-to-End IDs
	hopByHop uint32
	seqMu    sync.Mutex
}

// NewClient creates a new SGd client for reverse flow
func NewClient(nc *internalNats.Client, pm *diameter.PeerManager, cfg *config.Config, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		natsClient:  nc,
		peerManager: pm,
		translator:  NewTranslator(),
		cfg:         cfg,
		timeout:     timeout,
		pendingReqs: make(map[string]*pendingReq),
		hopByHop:    uint32(time.Now().UnixNano() & 0xFFFFFFFF),
	}
}

// queueGroup makes replicas share SMSC-initiated requests: each request is
// delivered to one gateway instance and sent to the Diameter peer once.
const queueGroup = "diameter-gw"

// StartListening subscribes to NATS topics for SMSC-initiated requests
func (c *Client) StartListening() error {
	// Subscribe to SRR requests (SMSC → HSS)
	if _, err := c.natsClient.QueueSubscribe(TopicSGdSRRRequest, queueGroup, c.handleSRRRequest); err != nil {
		return fmt.Errorf("failed to subscribe to SRR request topic: %w", err)
	}
	logger.Info("SGd Client listening for SRR requests",
		logger.String("topic", TopicSGdSRRRequest))

	// Subscribe to TFR requests (SMSC → MME)
	if _, err := c.natsClient.QueueSubscribe(TopicSGdTFRRequest, queueGroup, c.handleTFRRequest); err != nil {
		return fmt.Errorf("failed to subscribe to TFR request topic: %w", err)
	}
	logger.Info("SGd Client listening for TFR requests",
		logger.String("topic", TopicSGdTFRRequest))

	// Subscribe to ALR requests (SMSC → HSS)
	if _, err := c.natsClient.QueueSubscribe(TopicSGdALRRequest, queueGroup, c.handleALRRequest); err != nil {
		return fmt.Errorf("failed to subscribe to ALR request topic: %w", err)
	}
	logger.Info("SGd Client listening for ALR requests",
		logger.String("topic", TopicSGdALRRequest))

	return nil
}

// =============================================================================
// SRR Request Handler (SMSC → HSS)
// =============================================================================

// SRRRequest represents a routing info request from SMSC
type SRRRequest struct {
	TraceID   string `json:"trace_id"`
	MSISDN    string `json:"msisdn"`              // E.164 format
	SCAddress string `json:"sc_address"`          // Service Centre address
	SMRPPri   uint32 `json:"sm_rp_pri,omitempty"` // Priority
	SRRFlags  uint32 `json:"srr_flags,omitempty"` // Flags
	// Target HSS
	DestinationHost  string `json:"destination_host,omitempty"`
	DestinationRealm string `json:"destination_realm,omitempty"`
}

func (c *Client) handleSRRRequest(msg *nats.Msg) {
	startTime := time.Now()

	var req SRRRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		logger.Error("SGd Client: Failed to unmarshal SRR request", err)
		c.sendNatsError(msg, diam.InvalidAVPValue, "invalid request format")
		return
	}

	traceID := req.TraceID
	if traceID == "" {
		traceID = fmt.Sprintf("sgd-srr-client-%d", time.Now().UnixNano())
	}

	monitor.IncrementCounter("diameter_sgd_client_requests_total", map[string]string{"type": "SRR"})

	logger.Info("SGd Client: SRR request received",
		logger.String("trace_id", traceID),
		logger.String("msisdn", c.translator.MaskMSISDN(req.MSISDN)),
		logger.String("dest_host", req.DestinationHost),
	)

	// Get HSS connection
	conn := c.getConnection(req.DestinationHost)
	if conn == nil {
		logger.Warn("SGd Client: HSS peer not connected",
			logger.String("trace_id", traceID),
			logger.String("host", req.DestinationHost))
		c.sendNatsError(msg, diam.UnableToDeliver, "HSS peer not connected")
		return
	}

	// Build SRR message
	srr := c.buildSRRMessage(&req)
	sessionID := c.getSessionID(srr)

	// Register pending request
	respCh := make(chan *diam.Message, 1)
	c.mu.Lock()
	c.pendingReqs[sessionID] = &pendingReq{ch: respCh, conn: conn}
	c.mu.Unlock()

	// Send SRR to HSS
	logger.Info("SGd Client: Sending SRR to HSS",
		logger.String("trace_id", traceID),
		logger.String("session_id", sessionID))

	if _, err := srr.WriteTo(conn); err != nil {
		logger.Error("SGd Client: Failed to send SRR", err, logger.String("trace_id", traceID))
		c.removeRequest(sessionID)
		c.sendNatsError(msg, diam.TooBusy, "failed to send to HSS")
		return
	}

	// Wait for SRA response
	select {
	case sra := <-respCh:
		duration := time.Since(startTime)
		logger.Info("SGd Client: SRA received from HSS",
			logger.String("trace_id", traceID),
			logger.String("duration", duration.String()))
		c.sendSRAResponse(msg, sra, traceID)

		monitor.ObserveHistogram("diameter_sgd_client_request_duration_seconds",
			duration.Seconds(), map[string]string{"type": "SRR", "result": "success"})

	case <-time.After(c.timeout):
		logger.Warn("SGd Client: Timeout waiting for SRA",
			logger.String("trace_id", traceID),
			logger.String("session_id", sessionID))
		c.removeRequest(sessionID)
		c.sendNatsError(msg, diam.UnableToDeliver, "timeout waiting for HSS response")

		monitor.IncrementCounter("diameter_sgd_client_timeouts_total",
			map[string]string{"type": "SRR"})
	}
}

// =============================================================================
// TFR Request Handler (SMSC → MME)
// =============================================================================

// TFRRequest represents an MT-SMS request from SMSC
type TFRRequest struct {
	TraceID string `json:"trace_id"`

	// Recipient identification
	MSISDN string `json:"msisdn"`         // E.164 format
	IMSI   string `json:"imsi,omitempty"` // User-Name

	// SMS PDU
	SMRPUI  []byte `json:"sm_rp_ui"`            // SMS-DELIVER TPDU
	SMRPMTI uint32 `json:"sm_rp_mti,omitempty"` // 0=SMS-DELIVER, 1=STATUS-REPORT

	// SC address
	SCAddress string `json:"sc_address"`

	// Delivery parameters
	SMDeliveryTimer    uint32 `json:"sm_delivery_timer,omitempty"`
	MaxRetransmissions uint32 `json:"max_retransmissions,omitempty"`

	// Target MME (obtained from SRR)
	MMEName          string `json:"mme_name"`
	MMERealm         string `json:"mme_realm,omitempty"`
	DestinationHost  string `json:"destination_host,omitempty"`
	DestinationRealm string `json:"destination_realm,omitempty"`

	// Correlation
	CorrelationID string `json:"correlation_id,omitempty"`
}

func (c *Client) handleTFRRequest(msg *nats.Msg) {
	startTime := time.Now()

	var req TFRRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		logger.Error("SGd Client: Failed to unmarshal TFR request", err)
		c.sendNatsError(msg, diam.InvalidAVPValue, "invalid request format")
		return
	}

	traceID := req.TraceID
	if traceID == "" {
		traceID = fmt.Sprintf("sgd-tfr-client-%d", time.Now().UnixNano())
	}

	monitor.IncrementCounter("diameter_sgd_client_requests_total", map[string]string{"type": "TFR"})

	logger.Info("SGd Client: TFR request received (MT-SMS)",
		logger.String("trace_id", traceID),
		logger.String("msisdn", c.translator.MaskMSISDN(req.MSISDN)),
		logger.String("mme_name", req.MMEName),
		logger.Int("pdu_length", len(req.SMRPUI)),
	)

	// Determine destination host (prefer explicit, fallback to MMEName)
	destHost := req.DestinationHost
	if destHost == "" {
		destHost = req.MMEName
	}

	// Get MME connection
	conn := c.getConnection(destHost)
	if conn == nil {
		logger.Warn("SGd Client: MME peer not connected",
			logger.String("trace_id", traceID),
			logger.String("host", destHost))
		c.sendNatsError(msg, diam.UnableToDeliver, "MME peer not connected")
		return
	}

	// Build TFR message
	tfr := c.buildTFRMessage(&req)
	sessionID := c.getSessionID(tfr)

	// Register pending request
	respCh := make(chan *diam.Message, 1)
	c.mu.Lock()
	c.pendingReqs[sessionID] = &pendingReq{ch: respCh, conn: conn}
	c.mu.Unlock()

	// Send TFR to MME
	logger.Info("SGd Client: Sending TFR to MME",
		logger.String("trace_id", traceID),
		logger.String("session_id", sessionID),
		logger.String("mme", destHost))

	if _, err := tfr.WriteTo(conn); err != nil {
		logger.Error("SGd Client: Failed to send TFR", err, logger.String("trace_id", traceID))
		c.removeRequest(sessionID)
		c.sendNatsError(msg, diam.TooBusy, "failed to send to MME")
		return
	}

	// Wait for TFA response
	select {
	case tfa := <-respCh:
		duration := time.Since(startTime)
		logger.Info("SGd Client: TFA received from MME",
			logger.String("trace_id", traceID),
			logger.String("duration", duration.String()))
		c.sendTFAResponse(msg, tfa, traceID)

		monitor.ObserveHistogram("diameter_sgd_client_request_duration_seconds",
			duration.Seconds(), map[string]string{"type": "TFR", "result": "success"})

	case <-time.After(c.timeout):
		logger.Warn("SGd Client: Timeout waiting for TFA",
			logger.String("trace_id", traceID),
			logger.String("session_id", sessionID))
		c.removeRequest(sessionID)
		c.sendNatsError(msg, diam.UnableToDeliver, "timeout waiting for MME response")

		monitor.IncrementCounter("diameter_sgd_client_timeouts_total",
			map[string]string{"type": "TFR"})
	}
}

// =============================================================================
// ALR Request Handler (SMSC → HSS)
// =============================================================================

// ALRRequest represents an alert request from SMSC
type ALRRequest struct {
	TraceID           string `json:"trace_id"`
	MSISDN            string `json:"msisdn"`
	SCAddress         string `json:"sc_address"`
	SMSGMSCAlertEvent uint32 `json:"sms_gmsc_alert_event,omitempty"`
	DestinationHost   string `json:"destination_host,omitempty"`
	DestinationRealm  string `json:"destination_realm,omitempty"`
}

func (c *Client) handleALRRequest(msg *nats.Msg) {
	startTime := time.Now()

	var req ALRRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		logger.Error("SGd Client: Failed to unmarshal ALR request", err)
		c.sendNatsError(msg, diam.InvalidAVPValue, "invalid request format")
		return
	}

	traceID := req.TraceID
	if traceID == "" {
		traceID = fmt.Sprintf("sgd-alr-client-%d", time.Now().UnixNano())
	}

	monitor.IncrementCounter("diameter_sgd_client_requests_total", map[string]string{"type": "ALR"})

	logger.Info("SGd Client: ALR request received (Alert SC)",
		logger.String("trace_id", traceID),
		logger.String("msisdn", c.translator.MaskMSISDN(req.MSISDN)),
	)

	// Get HSS connection
	conn := c.getConnection(req.DestinationHost)
	if conn == nil {
		logger.Warn("SGd Client: HSS peer not connected",
			logger.String("trace_id", traceID),
			logger.String("host", req.DestinationHost))
		c.sendNatsError(msg, diam.UnableToDeliver, "HSS peer not connected")
		return
	}

	// Build ALR message
	alr := c.buildALRMessage(&req)
	sessionID := c.getSessionID(alr)

	// Register pending request
	respCh := make(chan *diam.Message, 1)
	c.mu.Lock()
	c.pendingReqs[sessionID] = &pendingReq{ch: respCh, conn: conn}
	c.mu.Unlock()

	// Send ALR to HSS
	if _, err := alr.WriteTo(conn); err != nil {
		logger.Error("SGd Client: Failed to send ALR", err, logger.String("trace_id", traceID))
		c.removeRequest(sessionID)
		c.sendNatsError(msg, diam.TooBusy, "failed to send to HSS")
		return
	}

	// Wait for ALA response
	select {
	case ala := <-respCh:
		duration := time.Since(startTime)
		logger.Info("SGd Client: ALA received from HSS",
			logger.String("trace_id", traceID),
			logger.String("duration", duration.String()))
		c.sendALAResponse(msg, ala, traceID)

		monitor.ObserveHistogram("diameter_sgd_client_request_duration_seconds",
			duration.Seconds(), map[string]string{"type": "ALR", "result": "success"})

	case <-time.After(c.timeout):
		logger.Warn("SGd Client: Timeout waiting for ALA",
			logger.String("trace_id", traceID))
		c.removeRequest(sessionID)
		c.sendNatsError(msg, diam.UnableToDeliver, "timeout waiting for HSS response")
	}
}

// =============================================================================
// Answer Handlers (called by Diameter server when SRA/TFA/ALA received)
// =============================================================================

// pendingReq is a request waiting for its answer. Only an answer arriving on
// conn, the connection the request was written to, is delivered: the
// Session-Id alone is predictable, so any connected peer could otherwise
// answer for the HSS or MME.
type pendingReq struct {
	ch   chan *diam.Message
	conn diam.Conn
}

// HandleSRA handles Send-Routing-Info-for-SM-Answer from HSS
func (c *Client) HandleSRA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m, "SRA") }

// HandleTFA handles MT-Forward-Short-Message-Answer from MME
func (c *Client) HandleTFA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m, "TFA") }

// HandleALA handles Alert-Service-Centre-Answer from HSS
func (c *Client) HandleALA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m, "ALA") }

func (c *Client) deliver(conn diam.Conn, m *diam.Message, cmd string) {
	sessionID := c.getSessionID(m)

	c.mu.RLock()
	pr, exists := c.pendingReqs[sessionID]
	c.mu.RUnlock()

	switch {
	case !exists:
		logger.Warn("SGd Client: Received unexpected "+cmd,
			logger.String("session_id", sessionID))
	case pr.conn != conn:
		logger.Warn("SGd Client: "+cmd+" arrived on a connection its request was not sent on — ignored",
			logger.String("session_id", sessionID))
	default:
		select {
		case pr.ch <- m:
		default: // the waiter already timed out
		}
		c.removeRequest(sessionID)
	}
}

// =============================================================================
// Message Builders
// =============================================================================

func (c *Client) buildSRRMessage(req *SRRRequest) *diam.Message {
	m := diam.NewRequest(CommandSRR, AppIDSGd, dict.Default)

	// Session-ID
	sessionID := fmt.Sprintf("%s;%d;%d",
		c.cfg.Server.OriginHost,
		time.Now().UnixNano(),
		c.nextHopByHop())
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sessionID))

	// Vendor-Specific-Application-Id
	c.addVSAI(m)

	// Auth-Session-State (NO_STATE_MAINTAINED)
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))

	// Origin-Host/Realm
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(c.cfg.Server.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(c.cfg.Server.OriginRealm))

	// Destination-Host/Realm
	if req.DestinationHost != "" {
		m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(req.DestinationHost))
	}
	destRealm := req.DestinationRealm
	if destRealm == "" {
		destRealm = c.cfg.Server.OriginRealm // fallback to local realm
	}
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))

	// MSISDN (TBCD encoded)
	msisdnBytes := c.encodeTBCD(req.MSISDN)
	m.NewAVP(AVPMSISDN, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(msisdnBytes))

	// Service-Centre-Address (TBCD encoded)
	if req.SCAddress != "" {
		scBytes := c.encodeTBCD(req.SCAddress)
		m.NewAVP(AVPServiceCentreAddress, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(scBytes))
	}

	// SM-RP-PRI
	if req.SMRPPri > 0 {
		m.NewAVP(3318, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Enumerated(req.SMRPPri))
	}

	// SRR-Flags
	if req.SRRFlags > 0 {
		m.NewAVP(AVPSRRFlags, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Unsigned32(req.SRRFlags))
	}

	return m
}

func (c *Client) buildTFRMessage(req *TFRRequest) *diam.Message {
	m := diam.NewRequest(CommandTFR, AppIDSGd, dict.Default)

	// Session-ID
	sessionID := fmt.Sprintf("%s;%d;%d",
		c.cfg.Server.OriginHost,
		time.Now().UnixNano(),
		c.nextHopByHop())
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sessionID))

	// Vendor-Specific-Application-Id
	c.addVSAI(m)

	// Auth-Session-State
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))

	// Origin-Host/Realm
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(c.cfg.Server.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(c.cfg.Server.OriginRealm))

	// Destination-Host/Realm
	destHost := req.DestinationHost
	if destHost == "" {
		destHost = req.MMEName
	}
	if destHost != "" {
		m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(destHost))
	}
	destRealm := req.DestinationRealm
	if destRealm == "" {
		destRealm = req.MMERealm
	}
	if destRealm == "" {
		destRealm = c.cfg.Server.OriginRealm
	}
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))

	// User-Identifier (Grouped AVP with MSISDN)
	userIdAVPs := []*diam.AVP{}
	if req.MSISDN != "" {
		msisdnBytes := c.encodeTBCD(req.MSISDN)
		userIdAVPs = append(userIdAVPs,
			diam.NewAVP(AVPMSISDN, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(msisdnBytes)))
	}
	if req.IMSI != "" {
		userIdAVPs = append(userIdAVPs,
			diam.NewAVP(avp.UserName, avp.Mbit, 0, datatype.UTF8String(req.IMSI)))
	}
	if len(userIdAVPs) > 0 {
		m.NewAVP(AVPUserIdentifier, avp.Mbit|avp.Vbit, VendorID3GPP, &diam.GroupedAVP{AVP: userIdAVPs})
	}

	// SC-Address (TBCD encoded)
	if req.SCAddress != "" {
		scBytes := c.encodeTBCD(req.SCAddress)
		m.NewAVP(AVPServiceCentreAddress, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(scBytes))
	}

	// SM-RP-UI (SMS PDU)
	if len(req.SMRPUI) > 0 {
		m.NewAVP(AVPSMRPUICode, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(req.SMRPUI))
	}

	// SM-RP-MTI
	m.NewAVP(AVPSMRPMTI, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Enumerated(req.SMRPMTI))

	// MME-Name
	if req.MMEName != "" {
		m.NewAVP(AVPMMEName, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.DiameterIdentity(req.MMEName))
	}

	// MME-Realm
	if req.MMERealm != "" {
		m.NewAVP(AVPMMERealm, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.DiameterIdentity(req.MMERealm))
	}

	// SM-Delivery-Timer
	if req.SMDeliveryTimer > 0 {
		m.NewAVP(AVPSMDeliveryTimer, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Unsigned32(req.SMDeliveryTimer))
	}

	// Maximum-Retransmissions
	if req.MaxRetransmissions > 0 {
		m.NewAVP(AVPMaximumRetransmissions, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Unsigned32(req.MaxRetransmissions))
	}

	return m
}

func (c *Client) buildALRMessage(req *ALRRequest) *diam.Message {
	m := diam.NewRequest(CommandALR, AppIDSGd, dict.Default)

	// Session-ID
	sessionID := fmt.Sprintf("%s;%d;%d",
		c.cfg.Server.OriginHost,
		time.Now().UnixNano(),
		c.nextHopByHop())
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(sessionID))

	// Vendor-Specific-Application-Id
	c.addVSAI(m)

	// Auth-Session-State
	m.NewAVP(avp.AuthSessionState, avp.Mbit, 0, datatype.Enumerated(1))

	// Origin-Host/Realm
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(c.cfg.Server.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(c.cfg.Server.OriginRealm))

	// Destination-Host/Realm
	if req.DestinationHost != "" {
		m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(req.DestinationHost))
	}
	destRealm := req.DestinationRealm
	if destRealm == "" {
		destRealm = c.cfg.Server.OriginRealm
	}
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(destRealm))

	// User-Identifier with MSISDN
	if req.MSISDN != "" {
		msisdnBytes := c.encodeTBCD(req.MSISDN)
		m.NewAVP(AVPUserIdentifier, avp.Mbit|avp.Vbit, VendorID3GPP, &diam.GroupedAVP{
			AVP: []*diam.AVP{
				diam.NewAVP(AVPMSISDN, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(msisdnBytes)),
			},
		})
	}

	// SC-Address
	if req.SCAddress != "" {
		scBytes := c.encodeTBCD(req.SCAddress)
		m.NewAVP(AVPServiceCentreAddress, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.OctetString(scBytes))
	}

	// SMS-GMSC-Alert-Event
	if req.SMSGMSCAlertEvent > 0 {
		m.NewAVP(AVPSMSGMSCAlertEvent, avp.Mbit|avp.Vbit, VendorID3GPP, datatype.Enumerated(req.SMSGMSCAlertEvent))
	}

	return m
}

// =============================================================================
// Response Senders
// =============================================================================

func (c *Client) sendSRAResponse(reqMsg *nats.Msg, sra *diam.Message, traceID string) {
	if reqMsg.Reply == "" {
		return
	}

	// Parse SRA to SRAAnswer
	answer := &SRAAnswer{
		ResultCode: c.extractResultCode(sra),
	}

	// Extract IMSI (User-Name)
	if userNameAVP := c.findAVP(sra.AVP, avp.UserName, 0); userNameAVP != nil {
		answer.UserName = string(userNameAVP.Data.(datatype.UTF8String))
	}

	// Extract MME info
	if mmeNameAVP := c.findAVP(sra.AVP, AVPMMEName, VendorID3GPP); mmeNameAVP != nil {
		answer.MMEName = string(mmeNameAVP.Data.(datatype.DiameterIdentity))
	}
	if mmeRealmAVP := c.findAVP(sra.AVP, AVPMMERealm, VendorID3GPP); mmeRealmAVP != nil {
		answer.MMERealm = string(mmeRealmAVP.Data.(datatype.DiameterIdentity))
	}
	if mmeNumAVP := c.findAVP(sra.AVP, AVPMMENumberForMTSMS, VendorID3GPP); mmeNumAVP != nil {
		answer.MMENumberForMTSMS = []byte(mmeNumAVP.Data.(datatype.OctetString))
	}

	// Extract SGSN info
	if sgsnNumAVP := c.findAVP(sra.AVP, AVPSGSNNumber, VendorID3GPP); sgsnNumAVP != nil {
		answer.SGSNNumber = []byte(sgsnNumAVP.Data.(datatype.OctetString))
	}
	if sgsnNameAVP := c.findAVP(sra.AVP, AVPSGSNName, VendorID3GPP); sgsnNameAVP != nil {
		answer.SGSNName = string(sgsnNameAVP.Data.(datatype.DiameterIdentity))
	}

	// Extract MSC info
	if mscNumAVP := c.findAVP(sra.AVP, AVPMSCNumber, VendorID3GPP); mscNumAVP != nil {
		answer.MSCNumber = []byte(mscNumAVP.Data.(datatype.OctetString))
	}

	// Extract MWD-Status
	if mwdAVP := c.findAVP(sra.AVP, AVPMWDStatus, VendorID3GPP); mwdAVP != nil {
		answer.MWDStatus = uint32(mwdAVP.Data.(datatype.Unsigned32))
	}

	data, _ := json.Marshal(answer)
	c.natsClient.Respond(reqMsg, data)

	logger.Info("SGd Client: SRA response sent",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
		logger.String("mme_name", answer.MMEName),
	)
}

func (c *Client) sendTFAResponse(reqMsg *nats.Msg, tfa *diam.Message, traceID string) {
	if reqMsg.Reply == "" {
		return
	}

	answer := &TFAAnswer{
		ResultCode: c.extractResultCode(tfa),
	}

	// Extract Absent-User-Diagnostic-SM
	if absentAVP := c.findAVP(tfa.AVP, AVPAbsentUserDiagnosticSM, VendorID3GPP); absentAVP != nil {
		answer.AbsentUserDiagnosticSM = uint32(absentAVP.Data.(datatype.Unsigned32))
	}

	data, _ := json.Marshal(answer)
	c.natsClient.Respond(reqMsg, data)

	logger.Info("SGd Client: TFA response sent",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
	)
}

func (c *Client) sendALAResponse(reqMsg *nats.Msg, ala *diam.Message, traceID string) {
	if reqMsg.Reply == "" {
		return
	}

	answer := &ALAAnswer{
		ResultCode: c.extractResultCode(ala),
	}

	data, _ := json.Marshal(answer)
	c.natsClient.Respond(reqMsg, data)

	logger.Info("SGd Client: ALA response sent",
		logger.String("trace_id", traceID),
		logger.Int("result_code", int(answer.ResultCode)),
	)
}

func (c *Client) sendNatsError(reqMsg *nats.Msg, code uint32, errMsg string) {
	if reqMsg.Reply == "" {
		return
	}

	resp := map[string]interface{}{
		"result_code":   code,
		"error_message": errMsg,
	}
	data, _ := json.Marshal(resp)
	c.natsClient.Respond(reqMsg, data)
}

// =============================================================================
// Helper Methods
// =============================================================================

func (c *Client) getConnection(destHost string) diam.Conn {
	if destHost != "" {
		return c.peerManager.GetConnection(destHost)
	}
	// If no specific host, try to get any available connection
	return c.peerManager.GetAnyConnection()
}

func (c *Client) getSessionID(m *diam.Message) string {
	if sidAVP, err := m.FindAVP(avp.SessionID, 0); err == nil {
		return string(sidAVP.Data.(datatype.UTF8String))
	}
	return ""
}

func (c *Client) removeRequest(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingReqs, sessionID)
}

func (c *Client) nextHopByHop() uint32 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.hopByHop++
	return c.hopByHop
}

func (c *Client) addVSAI(m *diam.Message) {
	vsaiAVP := diam.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{
		AVP: []*diam.AVP{
			diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
			diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(AppIDSGd)),
		},
	})
	m.AVP = append(m.AVP, vsaiAVP)
}

// extractResultCode folds the answer's outcome into one code, as the SGd reply
// envelopes carry it: the Result-Code, or the Experimental-Result-Code when
// the peer reported an application result. An answer with neither is
// reported as DIAMETER_UNABLE_TO_COMPLY, never as success.
func (c *Client) extractResultCode(m *diam.Message) uint32 {
	rc, exp := diameter.AnswerResult(m)
	if exp != 0 {
		return exp
	}
	return rc
}

func (c *Client) findAVP(avps []*diam.AVP, code, vendorID uint32) *diam.AVP {
	for _, a := range avps {
		if a.Code == code && a.VendorID == vendorID {
			return a
		}
		if vendorID == 0 && a.Code == code {
			return a
		}
	}
	return nil
}

// encodeTBCD encodes a phone number string to TBCD format
func (c *Client) encodeTBCD(number string) []byte {
	// Remove leading + if present
	if len(number) > 0 && number[0] == '+' {
		number = number[1:]
	}

	// TBCD encoding: each byte holds two digits, LSB first
	// If odd number of digits, pad with 0xF
	result := make([]byte, (len(number)+1)/2)

	for i := 0; i < len(number); i += 2 {
		d1 := number[i] - '0'
		var d2 byte = 0x0F // padding
		if i+1 < len(number) {
			d2 = number[i+1] - '0'
		}
		result[i/2] = d1 | (d2 << 4)
	}

	return result
}
