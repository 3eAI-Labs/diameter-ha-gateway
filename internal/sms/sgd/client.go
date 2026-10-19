// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/nats-io/nats.go"
)

// Subjects applications send S6c and SGd requests on; the reply is the
// peer's answer.
const (
	TopicSGdSRRRequest = "diameter.sgd.srr.request" // SMS-GMSC → HSS
	TopicSGdTFRRequest = "diameter.sgd.tfr.request" // SMS-GMSC → MME/SGSN
	TopicSGdALRRequest = "diameter.sgd.alr.request" // HSS → SMS-IWMSC, MME/SGSN → SMS-GMSC
	TopicSGdRDRRequest = "diameter.sgd.rdr.request" // SMS-GMSC → HSS
)

// outbound holds what every application request may carry. Without
// destination_host the request goes to any connected peer.
type outbound struct {
	TraceID            string              `json:"trace_id,omitempty"`
	DestinationHost    string              `json:"destination_host,omitempty"`
	DestinationRealm   string              `json:"destination_realm,omitempty"`
	SupportedFeatures  []SupportedFeatures `json:"supported_features,omitempty"`
	SMSMICorrelationID *SMSMICorrelationID `json:"smsmi_correlation_id,omitempty"`
}

// SRRRequest asks the HSS for the routing information of an SM (§5.3.2.3).
type SRRRequest struct {
	outbound
	MSISDN                string  `json:"msisdn,omitempty"`
	UserName              string  `json:"user_name,omitempty"` // IMSI
	SCAddress             string  `json:"sc_address,omitempty"`
	SMRPMTI               *uint32 `json:"sm_rp_mti,omitempty"`
	SMRPSMEA              []byte  `json:"sm_rp_smea,omitempty"`
	SRRFlags              uint32  `json:"srr_flags,omitempty"`
	SMRPPri               uint32  `json:"sm_rp_pri,omitempty"` // non-zero sets SRR-Flags bit 1, SM-RP-PRI
	SMDeliveryNotIntended *uint32 `json:"sm_delivery_not_intended,omitempty"`
}

func (r *SRRRequest) flags() uint32 {
	if r.SMRPPri != 0 {
		return r.SRRFlags | 1<<1
	}
	return r.SRRFlags
}

// TFRRequest delivers an MT SM to the MME or SGSN (§6.3.2.5). Without
// destination_host and destination_realm it goes to mme_name in mme_realm.
type TFRRequest struct {
	outbound
	IMSI                      string `json:"imsi,omitempty"`
	UserName                  string `json:"user_name,omitempty"` // IMSI; wins over imsi
	MSISDN                    string `json:"msisdn,omitempty"`    // accepted and not sent: TFR carries no MSISDN
	SMRPUI                    []byte `json:"sm_rp_ui"`
	SCAddress                 string `json:"sc_address"`
	MMEName                   string `json:"mme_name,omitempty"`
	MMERealm                  string `json:"mme_realm,omitempty"`
	MMENumberForMTSMS         string `json:"mme_number_for_mt_sms,omitempty"`
	SGSNNumber                string `json:"sgsn_number,omitempty"`
	TFRFlags                  uint32 `json:"tfr_flags,omitempty"`
	SMDeliveryTimer           uint32 `json:"sm_delivery_timer,omitempty"`
	SMDeliveryStartTime       int64  `json:"sm_delivery_start_time,omitempty"`
	MaximumRetransmissionTime int64  `json:"maximum_retransmission_time,omitempty"`
	SMSGMSCAddress            string `json:"sms_gmsc_address,omitempty"`
	CorrelationID             string `json:"correlation_id,omitempty"` // logged only
}

func (r *TFRRequest) userName() string {
	if r.UserName != "" {
		return r.UserName
	}
	return r.IMSI
}

// subscriber is the User-Identifier of ALR and RDR: given whole, or from
// msisdn and user_name.
type subscriber struct {
	UserIdentifier *UserIdentifier `json:"user_identifier,omitempty"`
	MSISDN         string          `json:"msisdn,omitempty"`
	UserName       string          `json:"user_name,omitempty"` // IMSI
}

func (s *subscriber) userIdentifier() *UserIdentifier {
	if s.UserIdentifier != nil {
		return s.UserIdentifier
	}
	if s.MSISDN == "" && s.UserName == "" {
		return nil
	}
	return &UserIdentifier{MSISDN: s.MSISDN, UserName: s.UserName}
}

// ALRRequest alerts a service centre (§5.3.2.5).
type ALRRequest struct {
	outbound
	subscriber
	SCAddress                 string       `json:"sc_address"`
	SMSGMSCAlertEvent         uint32       `json:"sms_gmsc_alert_event,omitempty"`
	MaximumUEAvailabilityTime int64        `json:"maximum_ue_availability_time,omitempty"`
	ServingNode               *ServingNode `json:"serving_node,omitempty"`
}

// RDRRequest reports the outcome of an SM delivery to the HSS (§5.3.2.7).
type RDRRequest struct {
	outbound
	subscriber
	SCAddress         string             `json:"sc_address"`
	SMDeliveryOutcome *SMDeliveryOutcome `json:"sm_delivery_outcome"`
	RDRFlags          uint32             `json:"rdr_flags,omitempty"`
}

// Client sends the S6c and SGd requests applications publish, and replies
// with the peer's answer.
type Client struct {
	inflight    internalNats.Inflight
	natsClient  *internalNats.Client
	peerManager *diameter.PeerManager
	cfg         *config.Config
	timeout     time.Duration

	mu          sync.RWMutex
	pendingReqs map[string]*pendingReq
}

// pendingReq waits for the answer to one request. Only an answer arriving on
// conn is delivered: the Session-Id alone is predictable, so any connected
// peer could otherwise answer for the HSS or MME.
type pendingReq struct {
	ch       chan *diam.Message
	conn     diam.Conn
	code     uint32 // the request's command code; an answer of another command is not delivered
	hopByHop uint32 // for an answer without Session-Id
}

func NewClient(nc *internalNats.Client, pm *diameter.PeerManager, cfg *config.Config, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Client{natsClient: nc, peerManager: pm, cfg: cfg, timeout: timeout,
		pendingReqs: make(map[string]*pendingReq), inflight: internalNats.NewInflight()}
}

// queueGroup makes replicas share application requests: each is sent to the
// peer once.
const queueGroup = "diameter-gw"

func (c *Client) StartListening() error {
	for subject, handle := range map[string]func(*nats.Msg){
		TopicSGdSRRRequest: c.handleSRRRequest,
		TopicSGdTFRRequest: c.handleTFRRequest,
		TopicSGdALRRequest: c.handleALRRequest,
		TopicSGdRDRRequest: c.handleRDRRequest,
	} {
		// Each request waits for its peer concurrently
		// (internal/nats/inflight.go): a peer that does not answer delays
		// no other request.
		busy := func(m *nats.Msg) { c.replyError(m, diam.TooBusy, "too many requests pending") }
		if _, err := c.natsClient.QueueSubscribe(subject, queueGroup, c.inflight.Wrap(handle, busy)); err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
	}
	return nil
}

func (c *Client) handleSRRRequest(msg *nats.Msg) {
	var r SRRRequest
	if !c.parse(msg, "SRR", &r) {
		return
	}
	if r.MSISDN == "" && r.UserName == "" {
		c.replyError(msg, diam.MissingAVP, "missing msisdn or user_name")
		return
	}
	c.send(msg, "SRR", r.outbound, r.DestinationHost, r.DestinationRealm,
		func(sid, oh, or, dh, dr string) *diam.Message { return buildSRR(&r, sid, oh, or, dh, dr) },
		func(a *diam.Message) any { return decodeSRA(a) })
}

func (c *Client) handleTFRRequest(msg *nats.Msg) {
	var r TFRRequest
	if !c.parse(msg, "TFR", &r) {
		return
	}
	destHost, destRealm := r.DestinationHost, r.DestinationRealm
	if destHost == "" {
		destHost = r.MMEName
	}
	if destRealm == "" {
		destRealm = r.MMERealm
	}
	if field := firstMissing(
		check{"destination_host or mme_name", destHost == ""},
		check{"user_name or imsi", r.userName() == ""},
		check{"sc_address", r.SCAddress == ""},
		check{"sm_rp_ui", len(r.SMRPUI) == 0},
	); field != "" {
		c.replyError(msg, diam.MissingAVP, "missing "+field)
		return
	}
	logger.Info("SGd: TFR request",
		logger.String("trace_id", r.TraceID), logger.String("correlation_id", r.CorrelationID),
		logger.String("dest_host", destHost))
	c.send(msg, "TFR", r.outbound, destHost, destRealm,
		func(sid, oh, or, dh, dr string) *diam.Message { return buildTFR(&r, sid, oh, or, dh, dr) },
		func(a *diam.Message) any { return decodeTFA(a) })
}

func (c *Client) handleALRRequest(msg *nats.Msg) {
	var r ALRRequest
	if !c.parse(msg, "ALR", &r) {
		return
	}
	if field := firstMissing(
		check{"sc_address", r.SCAddress == ""},
		check{"user_identifier, msisdn or user_name", r.userIdentifier() == nil},
	); field != "" {
		c.replyError(msg, diam.MissingAVP, "missing "+field)
		return
	}
	c.send(msg, "ALR", r.outbound, r.DestinationHost, r.DestinationRealm,
		func(sid, oh, or, dh, dr string) *diam.Message { return buildALR(&r, sid, oh, or, dh, dr) },
		func(a *diam.Message) any { return decodeALA(a) })
}

func (c *Client) handleRDRRequest(msg *nats.Msg) {
	var r RDRRequest
	if !c.parse(msg, "RDR", &r) {
		return
	}
	if field := firstMissing(
		check{"sc_address", r.SCAddress == ""},
		check{"user_identifier, msisdn or user_name", r.userIdentifier() == nil},
		check{"sm_delivery_outcome", r.SMDeliveryOutcome == nil},
	); field != "" {
		c.replyError(msg, diam.MissingAVP, "missing "+field)
		return
	}
	c.send(msg, "RDR", r.outbound, r.DestinationHost, r.DestinationRealm,
		func(sid, oh, or, dh, dr string) *diam.Message { return buildRDR(&r, sid, oh, or, dh, dr) },
		func(a *diam.Message) any { return decodeRDA(a) })
}

func (c *Client) parse(msg *nats.Msg, cmd string, v any) bool {
	monitor.IncrementCounter("diameter_sgd_client_requests_total", map[string]string{"type": cmd})
	if err := json.Unmarshal(msg.Data, v); err != nil {
		c.replyError(msg, diam.InvalidAVPValue, "request is not valid JSON")
		return false
	}
	return true
}

// check is a mandatory field of an application request and whether it is
// missing.
type check struct {
	field   string
	missing bool
}

func firstMissing(checks ...check) string {
	for _, c := range checks {
		if c.missing {
			return c.field
		}
	}
	return ""
}

// send writes the request to the destination peer and replies with its
// answer, or with 3002 when the peer is not connected or does not answer in
// time.
func (c *Client) send(msg *nats.Msg, cmd string, o outbound, destHost, destRealm string,
	build func(sid, originHost, originRealm, destHost, destRealm string) *diam.Message,
	decode func(*diam.Message) any) {
	if destRealm == "" {
		destRealm = c.cfg.Server.OriginRealm
	}
	originHost := c.cfg.Server.OriginHost
	sid := diameter.NewSessionID(originHost, strings.ToLower(cmd))
	m := build(sid, originHost, c.cfg.Server.OriginRealm, destHost, destRealm)
	// What the application sent may still leave a mandatory AVP out: an
	// empty group, or a number with no digit.
	if missing := missingAVPs(m, cmd); len(missing) > 0 {
		c.replyError(msg, diam.MissingAVP, fmt.Sprintf("missing mandatory AVP %d", missing[0].Code))
		return
	}

	var conn diam.Conn
	if destHost != "" {
		conn = c.peerManager.GetConnection(destHost)
	} else {
		conn = c.peerManager.GetAnyConnection()
	}
	if conn == nil {
		logger.Warn("SGd: peer not connected", logger.String("cmd", cmd),
			logger.String("trace_id", o.TraceID), logger.String("dest_host", destHost))
		c.replyError(msg, diam.UnableToDeliver, "peer not connected")
		return
	}

	ch := make(chan *diam.Message, 1)
	c.mu.Lock()
	c.pendingReqs[sid] = &pendingReq{ch: ch, conn: conn, code: m.Header.CommandCode, hopByHop: m.Header.HopByHopID}
	c.mu.Unlock()
	defer c.removeRequest(sid)

	if _, err := m.WriteTo(conn); err != nil {
		logger.Warn("SGd: request write failed", logger.String("cmd", cmd), logger.String("err", err.Error()))
		c.replyError(msg, diam.UnableToDeliver, "request write failed")
		return
	}
	select {
	case ans := <-ch:
		data, err := json.Marshal(decode(ans))
		if err != nil {
			c.replyError(msg, diam.UnableToComply, "answer could not be encoded")
			return
		}
		c.respond(msg, data)
	case <-time.After(c.timeout):
		logger.Warn("SGd: no answer", logger.String("cmd", cmd), logger.String("trace_id", o.TraceID))
		c.replyError(msg, diam.UnableToDeliver, "no answer from peer")
	}
}

func (c *Client) HandleSRA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m) }
func (c *Client) HandleTFA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m) }
func (c *Client) HandleALA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m) }
func (c *Client) HandleRDA(conn diam.Conn, m *diam.Message) { c.deliver(conn, m) }

func (c *Client) deliver(conn diam.Conn, m *diam.Message) {
	_, pr := c.pendingFor(conn, m)
	if pr == nil || pr.conn != conn || pr.code != m.Header.CommandCode {
		return
	}
	select {
	case pr.ch <- m:
	default:
	}
}

// pendingFor finds the request m answers: by Session-Id or, for an answer
// without one (RFC 6733 §7.2), by hop-by-hop identifier on conn.
func (c *Client) pendingFor(conn diam.Conn, m *diam.Message) (string, *pendingReq) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if a := avpList(m.AVP).base(avp.SessionID); a != nil {
		sid := asString(a)
		return sid, c.pendingReqs[sid]
	}
	for sid, pr := range c.pendingReqs {
		if pr.conn == conn && pr.hopByHop == m.Header.HopByHopID {
			return sid, pr
		}
	}
	return "", nil
}

func (c *Client) removeRequest(sid string) {
	c.mu.Lock()
	delete(c.pendingReqs, sid)
	c.mu.Unlock()
}

func (c *Client) replyError(msg *nats.Msg, code uint32, message string) {
	data, _ := json.Marshal(Result{ResultCode: code, ErrorMessage: message})
	c.respond(msg, data)
}

func (c *Client) respond(msg *nats.Msg, data []byte) {
	if msg.Reply == "" {
		return
	}
	if err := c.natsClient.Respond(msg, data); err != nil {
		logger.Warn("SGd: reply failed", logger.String("err", err.Error()))
	}
}
