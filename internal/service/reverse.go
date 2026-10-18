// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/alarm"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/audit"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/nats-io/nats.go"
)

// ReverseService dispatches HSS-initiated S6a commands (CLR/IDR/...) from
// NATS into Diameter requests sent to the appropriate MME peer.
//
// pendingReqs is per-instance and in-memory, keeping the gateway stateless;
// the sender (the subscriber backend) is responsible for retry if the gateway
// crashes mid-flight. Each entry tracks its peer host so peer-drop events can
// fast-fail every in-flight request to that host with 4012 (AGENT_FAILOVER)
// instead of waiting for reverse_timeout.
type ReverseService struct {
	natsClient    *internalNats.Client
	peerManager   *diameter.PeerManager
	translator    *diameter.Translator
	cfg           *config.Config   // bootstrap snapshot (origin host/realm — non-reloadable)
	reloader      *config.Reloader // optional; when set, hot-reloadable reads (timeout, mme_dest_realm) consult it
	pendingReqs   map[string]*pendingReq
	redirectCache *diameter.RedirectCache // §6.5 cache; populated reactively on inbound 3006
	mu            sync.RWMutex
}

// pendingReq couples a reverse-path waiter channel with the peer host the
// request was sent to. The peer host enables OnPeerDrop to find
// every in-flight request for a dropped connection and signal them all.
type pendingReq struct {
	ch       chan *diam.Message
	peerHost string
	// conn is the connection the request was written to. Only an answer
	// arriving on it is delivered: the Session-Id alone is predictable, so
	// any connected peer could otherwise answer for the MME.
	conn diam.Conn
	// hopByHop identifies the request on conn, for an answer that carries
	// no Session-Id.
	hopByHop uint32
}

func NewReverseService(nc *internalNats.Client, pm *diameter.PeerManager, cfg *config.Config) *ReverseService {
	ttl := 60 * time.Second
	if d, err := time.ParseDuration(cfg.Server.RedirectDefaultTTL); err == nil && d > 0 {
		ttl = d
	}
	capacity := cfg.Server.RedirectCacheCapacity
	if capacity <= 0 {
		capacity = 10000
	}
	rs := &ReverseService{
		natsClient:  nc,
		peerManager: pm,
		translator:  diameter.NewTranslator(),
		cfg:         cfg,
		pendingReqs: make(map[string]*pendingReq),
		redirectCache: diameter.NewRedirectCache(diameter.RedirectCacheOptions{
			Capacity:   capacity,
			DefaultTTL: ttl,
		}),
	}
	// Register peer-drop listener (fast-fail). PeerManager calls
	// rs.OnPeerDrop synchronously when any peer connection closes; we
	// signal every pending waiter for that host with a synthetic 4012
	// answer so reverse_timeout doesn't gate failure visibility.
	pm.AddDropListener(rs.OnPeerDrop)
	return rs
}

// RedirectCache exposes the §6.5 cache for admin endpoints (e.g. a
// `/admin/redirect/cache` snapshot). Returned cache is the live instance;
// callers MUST treat the returned value as read-only.
func (rs *ReverseService) RedirectCache() *diameter.RedirectCache {
	return rs.redirectCache
}

// SetReloader wires the live config snapshot used for hot-reloadable reads.
func (rs *ReverseService) SetReloader(r *config.Reloader) {
	rs.reloader = r
}

func (rs *ReverseService) activeConfig() *config.Config {
	if rs.reloader != nil {
		if c := rs.reloader.Get(); c != nil {
			return c
		}
	}
	return rs.cfg
}

// optInForVerb returns true when the operator has explicitly enabled
// redirect retry for a verb classified as RetryIfOperatorOptsIn. Each verb
// has its own flag so operators can mix-and-match per their fleet's
// tolerance for double-application semantics.
func (rs *ReverseService) optInForVerb(verb string) bool {
	c := rs.activeConfig().Server
	switch verb {
	case "IDR":
		return c.RedirectIDRRetryEnabled
	case "ASR":
		return c.RedirectASRRetryEnabled
	}
	return false
}

// reverseTimeout returns the parsed reverse-path timeout, with a
// 5s safety fallback when the config is malformed.
func (rs *ReverseService) reverseTimeout() time.Duration {
	if d, err := time.ParseDuration(rs.activeConfig().Server.ReverseTimeout); err == nil && d > 0 {
		return d
	}
	return 5 * time.Second
}

// destRealmFor resolves the Origin-Realm to put on a reverse request bound for
// destHost (the target MME). Precedence:
//  1. NATS event header.dest_realm (sender-supplied)
//  2. cfg.Server.MMEDestRealm (operator default)
//  3. cfg.Server.OriginRealm (fallback — same realm)
func (rs *ReverseService) destRealmFor(eventDestRealm string) string {
	if eventDestRealm != "" {
		return eventDestRealm
	}
	c := rs.activeConfig()
	if c.Server.MMEDestRealm != "" {
		return c.Server.MMEDestRealm
	}
	return c.Server.OriginRealm
}

// StartListening subscribes to NATS topics for HSS-initiated requests.
// Uses the queue group "diameter-gw" so multiple gateway instances load-balance
// and a re-published event can reach a different instance.
func (rs *ReverseService) StartListening() {
	rs.subscribe("diameter.s6a.clr", "CLR", rs.HandleCLR)
	rs.subscribe("diameter.s6a.idr", "IDR", rs.HandleIDR)
	// PUR and NOR are not here: TS 29.272 §7.2.13 and §7.2.17 define them as
	// sent from the MME or SGSN to the HSS only, and diameter.s6a.pur/nor are
	// the subjects the gateway requests on for an MME's PUR/NOR.
	rs.subscribe("diameter.s6a.rsr", "RSR", rs.HandleRSR)
	// HSS-initiated DSR (Delete-Subscriber-Data) per TS 29.272 §6.1.4.
	// Used by the subscriber backend for selective subscription data deletion (APN removal,
	// trace-data clear, regional subscription withdrawal, etc.). Bit-driven
	// scope via DSR-Flags AVP; APN-scoped withdrawal additionally lists
	// Context-Identifiers in WithdrawnAPNContextIDs.
	rs.subscribe("diameter.s6a.dsr", "DSR", rs.HandleDSR)
}

func (rs *ReverseService) subscribe(topic, label string, handler func(*nats.Msg)) {
	if _, err := rs.natsClient.QueueSubscribe(topic, "diameter-gw", handler); err != nil {
		logger.Error("Failed to subscribe to reverse topic", err,
			logger.String("topic", topic),
			logger.String("cmd", label))
		return
	}
	logger.Info("Listening for Reverse Diameter Requests",
		logger.String("topic", topic),
		logger.String("cmd", label))
}

// HandleCLR processes a Cancel-Location-Request triggered by the backend.
func (rs *ReverseService) HandleCLR(msg *nats.Msg) {
	rs.handleReverse(msg, "CLR", func(event *pb.S6aEvent, destHost, destRealm string) (*diam.Message, string) {
		m, sid := rs.translator.CreateCLR(
			event.IMSI,
			destHost,
			destRealm,
			rs.cfg.Server.OriginHost,
			rs.cfg.Server.OriginRealm,
			event.CancellationType,
		)
		_ = diameter.ApplyRequestExtras(m, "CLR", event) // validated in handleReverse
		return m, sid
	})
}

// HandleIDR processes an Insert-Subscriber-Data-Request triggered by the
// backend.
func (rs *ReverseService) HandleIDR(msg *nats.Msg) {
	rs.handleReverse(msg, "IDR", func(event *pb.S6aEvent, destHost, destRealm string) (*diam.Message, string) {
		m, sid := rs.translator.CreateIDR(
			event.IMSI,
			destHost,
			destRealm,
			rs.cfg.Server.OriginHost,
			rs.cfg.Server.OriginRealm,
			event.SubProfile,     // structured (preferred)
			event.SubscriberData, // legacy hex-OctetString fallback
			event.IDRFlags,
		)
		_ = diameter.ApplyRequestExtras(m, "IDR", event) // validated in handleReverse
		return m, sid
	})
}

// HandleDSR builds a Delete-Subscriber-Data-Request. DSRFlags is the bit
// field that selects which subscription data to withdraw (TS 29.272 §7.3.46);
// WithdrawnAPNContextIDs scopes APN-Configuration withdrawal to a specific
// subset of APNs (used together with the APN-Configuration-Profile bit).
func (rs *ReverseService) HandleDSR(msg *nats.Msg) {
	rs.handleReverse(msg, "DSR", func(event *pb.S6aEvent, destHost, destRealm string) (*diam.Message, string) {
		m, sid := rs.translator.CreateDSR(
			event.IMSI,
			destHost,
			destRealm,
			rs.cfg.Server.OriginHost,
			rs.cfg.Server.OriginRealm,
			event.DSRFlags,
			event.WithdrawnAPNContextIDs,
		)
		_ = diameter.ApplyRequestExtras(m, "DSR", event) // validated in handleReverse
		return m, sid
	})
}

// HandleRSR builds a Reset-Request. event.IMSI is sent as User-Id, an IMSI
// prefix (a full IMSI selects one subscriber); when it is empty the RSR covers
// every subscriber the MME holds from this HSS (TS 29.272 §7.2.15).
func (rs *ReverseService) HandleRSR(msg *nats.Msg) {
	rs.handleReverse(msg, "RSR", func(event *pb.S6aEvent, destHost, destRealm string) (*diam.Message, string) {
		m, sid := rs.translator.CreateRSR(
			event.IMSI,
			destHost,
			destRealm,
			rs.cfg.Server.OriginHost,
			rs.cfg.Server.OriginRealm,
		)
		_ = diameter.ApplyRequestExtras(m, "RSR", event) // validated in handleReverse
		return m, sid
	})
}

// handleReverse is the shared correlation pipeline. The build closure produces
// the Diameter request (and its Session-Id) from the parsed event; everything
// else — peer resolution, audit, NATS reply, timeout, and §6.5 redirect-honor
// retry — is identical across reverse commands.
//
// On inbound 3006 (DIAMETER_REDIRECT_INDICATION), the answer flow consults
// the per-verb idempotency posture (diameter.RetryPostureFor). RetryAlways verbs
// (RSR/CLR) are transparently re-issued to the redirect target; NeverRetry
// verbs (DSR) and opt-out IDR pass the 3006 verbatim back to the NATS caller.
func (rs *ReverseService) handleReverse(
	msg *nats.Msg,
	cmd string,
	build func(*pb.S6aEvent, string, string) (*diam.Message, string),
) {
	monitor.IncrementCounter("diameter_reverse_requests_total", map[string]string{"cmd": cmd})

	var event pb.S6aEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		logger.Error("Failed to unmarshal reverse event", err, logger.String("cmd", cmd))
		rs.sendNatsError(msg, 5004) // INVALID_AVP_VALUE
		return
	}

	if err := diameter.ValidateRequestExtras(cmd, &event); err != nil {
		logger.Warn("Reverse event has an invalid field", logger.String("cmd", cmd), logger.String("err", err.Error()))
		rs.sendNatsError(msg, 5004) // INVALID_AVP_VALUE
		return
	}

	initialDest := event.Header.SourceHost
	if initialDest == "" {
		logger.Warn("Reverse event missing header.source_host",
			logger.String("cmd", cmd))
		rs.sendNatsError(msg, 3002)
		return
	}

	rs.executeReverseHop(msg, cmd, build, &event, initialDest, redirectState{
		visited: []string{rs.cfg.Server.OriginHost}, // gateway itself in the route record
	})
}

// redirectState tracks the running redirect-retry context across hops.
// Visited contains every host the request has touched (gateway origin +
// every prior dest); depth counts redirect hops actually taken.
type redirectState struct {
	depth   int
	visited []string
}

// executeReverseHop sends the request to destHost and handles the response.
// On 3006, evaluates retry posture and (when allowed) recurses to the next
// hop with depth+1. Tail-call style — no goroutines spawned per hop, so the
// reverse_timeout budget caps the entire redirect chain (depth × per-hop wait
// is not bounded by a parent timeout context; a known trade-off).
func (rs *ReverseService) executeReverseHop(
	msg *nats.Msg,
	cmd string,
	build func(*pb.S6aEvent, string, string) (*diam.Message, string),
	event *pb.S6aEvent,
	destHost string,
	state redirectState,
) {
	conn := rs.peerManager.GetConnection(destHost)
	if conn == nil {
		logger.Warn("MME Peer not connected", logger.String("host", destHost), logger.String("cmd", cmd))
		monitor.IncrementCounter("diameter_reverse_peer_not_found_total", map[string]string{"cmd": cmd})
		rs.sendNatsError(msg, 3002)
		return
	}

	destRealm := rs.destRealmFor(event.Header.DestRealm)
	diamMsg, sessionID := build(event, destHost, destRealm)

	// A redirect hop is a new request, not a retransmission: the T flag
	// MUST NOT be set once an error answer (here 3006) has been received
	// for the earlier message (RFC 6733 §3).

	respCh := make(chan *diam.Message, 1)
	rs.mu.Lock()
	rs.pendingReqs[sessionID] = &pendingReq{ch: respCh, peerHost: destHost, conn: conn,
		hopByHop: diamMsg.Header.HopByHopID}
	rs.mu.Unlock()

	maskedIMSI := rs.translator.MaskIMSI(event.IMSI)
	logger.Info(fmt.Sprintf("Sending %s to MME", cmd),
		logger.String("mme", destHost),
		logger.String("sid", sessionID),
		logger.String("imsi", maskedIMSI),
		logger.Int("redirect_depth", state.depth))

	// Audit every reverse-direction trigger with masked IMSI.
	audit.Log(context.Background(), "reverse-trigger", "S6A_"+cmd+"_SENT", "mme:"+destHost,
		maskedIMSI, nil)

	if _, err := diamMsg.WriteTo(conn); err != nil {
		logger.Error(fmt.Sprintf("Failed to send %s", cmd), err)
		rs.removeRequest(sessionID)
		rs.sendNatsError(msg, 3004)
		return
	}

	timeout := rs.reverseTimeout()
	select {
	case answer := <-respCh:
		logger.Info(fmt.Sprintf("Received answer for %s", cmd),
			logger.String("sid", sessionID),
			logger.Int("redirect_depth", state.depth))
		rs.handleReverseAnswer(msg, cmd, build, event, destHost, state, answer)
	case <-time.After(timeout):
		logger.Warn(fmt.Sprintf("Timeout waiting for %s answer", cmd),
			logger.String("sid", sessionID),
			logger.String("timeout", timeout.String()))
		monitor.IncrementCounter("diameter_reverse_timeout_total", map[string]string{"cmd": cmd})
		rs.removeRequest(sessionID)
		rs.sendNatsError(msg, 3002)
	}
}

// handleReverseAnswer routes the inbound answer through the §6.5 redirect
// path when applicable. Non-3006 answers (and 3006 without honor-able retry)
// pass through to the NATS caller verbatim.
func (rs *ReverseService) handleReverseAnswer(
	msg *nats.Msg,
	cmd string,
	build func(*pb.S6aEvent, string, string) (*diam.Message, string),
	event *pb.S6aEvent,
	destHost string,
	state redirectState,
	answer *diam.Message,
) {
	ind, isRedirect := diameter.ParseRedirectIndication(answer)
	if !isRedirect {
		rs.sendNatsResponse(msg, answer)
		return
	}

	monitor.IncrementCounter("diameter_redirect_received_total", map[string]string{
		"direction": "reverse",
		"usage":     ind.Usage.String(),
	})

	// Cache reactively — even if we don't honor the retry below, future
	// admin observers benefit from seeing the indication. A cache hit does
	// not yet short-circuit the send.
	rs.redirectCache.Store(ind, diameter.RedirectContext{
		SessionID:     extractSessionID(answer),
		DestRealm:     rs.destRealmFor(event.Header.DestRealm),
		DestHost:      destHost,
		ApplicationID: answer.Header.ApplicationID,
		UserName:      event.IMSI,
	})

	posture := diameter.RetryPostureFor(cmd)
	if posture == diameter.RetryIfOperatorOptsIn && !rs.optInForVerb(cmd) {
		posture = diameter.NeverRetry
	}
	if posture == diameter.NeverRetry {
		monitor.IncrementCounter("diameter_redirect_idempotency_blocked_total",
			map[string]string{"verb": cmd})
		logger.Info("Redirect indication received but verb is non-idempotent — passing 3006 to NATS caller",
			logger.String("cmd", cmd),
			logger.String("from", destHost),
			logger.String("usage", ind.Usage.String()))
		rs.sendNatsResponse(msg, answer)
		return
	}

	maxDepth := rs.activeConfig().Server.RedirectMaxDepth
	visited := append(rs.collectRouteRecord(answer), state.visited...)
	visited = append(visited, destHost)

	nextHost, nextURI, ok := rs.pickNextRedirectTarget(ind.Hosts, visited, state.depth, maxDepth)
	if !ok {
		monitor.IncrementCounter("diameter_redirect_loop_detected_total",
			map[string]string{"peer": destHost})
		alarm.Raise(alarm.SeverityWarning, "DIAM_GW_REDIRECT_LOOP",
			fmt.Sprintf("redirect chain exhausted for %s from %s (depth=%d)", cmd, destHost, state.depth))
		logger.Warn("Redirect chain exhausted (loop or depth) — returning original 3006 to NATS",
			logger.String("cmd", cmd),
			logger.String("from", destHost),
			logger.Int("depth", state.depth))
		rs.sendNatsResponse(msg, answer)
		return
	}

	monitor.IncrementCounter("diameter_redirect_retry_total", map[string]string{
		"verb":  cmd,
		"depth": fmt.Sprintf("%d", state.depth+1),
	})
	audit.Log(context.Background(), "redirect-honor", "BUS_REDIRECT_HONORED",
		fmt.Sprintf("from=%s to=%s usage=%s depth=%d", destHost, nextHost, ind.Usage.String(), state.depth+1),
		rs.translator.MaskIMSI(event.IMSI), map[string]string{"redirect_uri": nextURI})

	logger.Info("Honoring redirect — retrying to next host",
		logger.String("cmd", cmd),
		logger.String("from", destHost),
		logger.String("to", nextHost),
		logger.String("usage", ind.Usage.String()),
		logger.Int("depth", state.depth+1))

	rs.executeReverseHop(msg, cmd, build, event, nextHost, redirectState{
		depth:   state.depth + 1,
		visited: visited,
	})
}

// pickNextRedirectTarget walks the indication.Hosts list and returns the
// first non-loop entry. Returns (host, uri, true) on success; (_, _, false)
// when every candidate fails DetectLoop or the depth limit is hit.
func (rs *ReverseService) pickNextRedirectTarget(uris, visited []string, depth, maxDepth int) (string, string, bool) {
	for _, uri := range uris {
		host := hostFromDiameterURI(uri)
		if host == "" {
			continue
		}
		if reason := diameter.DetectLoop(host, visited, depth+1, maxDepth); reason != "" {
			if reason == "redirect_depth_exceeded" {
				monitor.IncrementCounter("diameter_redirect_loop_detected_total",
					map[string]string{"peer": "depth"})
				alarm.Raise(alarm.SeverityWarning, "DIAM_GW_REDIRECT_DEPTH_EXCEEDED",
					fmt.Sprintf("redirect depth=%d exceeded max=%d", depth+1, maxDepth))
				return "", "", false // hop-count is global; no later candidate will help
			}
			continue // loop on this candidate — try the next one
		}
		return host, uri, true
	}
	return "", "", false
}

// hostFromDiameterURI extracts the host component from a DiameterURI per
// RFC 6733 §4.3 grammar:
//
//	DiameterURI = "aaa://" FQDN [ port ] [ transport ] [ protocol ]
//	transport   = ";transport=" transport-protocol
//	protocol    = ";protocol=" "diameter"
//
// net/url treats `;` as part of the host, so we strip the parameter tail
// manually before delegating. Returns "" when the URI is malformed —
// caller treats this as "skip this candidate".
func hostFromDiameterURI(uri string) string {
	lower := strings.ToLower(uri)
	if !strings.HasPrefix(lower, "aaa://") && !strings.HasPrefix(lower, "aaas://") {
		return ""
	}
	// Drop transport/protocol parameter tail (`;...`) before parsing —
	// net/url will otherwise glue it onto the host token.
	if idx := strings.IndexByte(lower, ';'); idx >= 0 {
		lower = lower[:idx]
	}
	u, err := url.Parse(lower)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// collectRouteRecord extracts every Route-Record AVP from an answer — these
// are the hosts the message has already traversed (RFC 6733 §6.7). Used to
// seed the visited list before DetectLoop on each redirect candidate.
func (rs *ReverseService) collectRouteRecord(m *diam.Message) []string {
	avps, err := m.FindAVPs(avp.RouteRecord, 0)
	if err != nil || len(avps) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(avps))
	for _, a := range avps {
		if id, ok := a.Data.(datatype.DiameterIdentity); ok {
			hosts = append(hosts, string(id))
		}
	}
	return hosts
}

// extractSessionID pulls Session-Id from an answer for cache-key derivation.
// Empty when absent — RedirectCache.keyFor treats empty SessionID as "skip
// caching at ALL_SESSION scope".
func extractSessionID(m *diam.Message) string {
	a, err := m.FindAVP(avp.SessionID, 0)
	if err != nil {
		return ""
	}
	return diameter.AVPString(a)
}

func (rs *ReverseService) sendNatsResponse(reqMsg *nats.Msg, diamMsg *diam.Message) {
	if reqMsg.Reply == "" {
		return
	}

	data, _ := json.Marshal(diameter.DecodeMMEAnswer(diamMsg))
	rs.natsClient.Respond(reqMsg, data)
}

func (rs *ReverseService) sendNatsError(reqMsg *nats.Msg, code uint32) {
	if reqMsg.Reply == "" {
		return
	}
	resp := pb.S6aAnswer{ResultCode: code}
	data, _ := json.Marshal(resp)
	rs.natsClient.Respond(reqMsg, data)
}

// HandleCLA correlates an inbound CLA from an MME to the pending CLR. Wired
// into the Diameter state machine via DiameterServer.SetAnswerHandler.
func (rs *ReverseService) HandleCLA(c diam.Conn, m *diam.Message) {
	rs.deliverAnswer(c, m)
}

// HandleIDA correlates an inbound IDA — same machinery as CLA.
func (rs *ReverseService) HandleIDA(c diam.Conn, m *diam.Message) {
	rs.deliverAnswer(c, m)
}

// HandleRSA / HandleDSA — same Session-Id correlation as CLA/IDA.
// Kept as separate methods to satisfy the AnswerHandler interface (which
// names commands explicitly per RFC 6733 §3.2 conventions).
func (rs *ReverseService) HandleRSA(c diam.Conn, m *diam.Message) { rs.deliverAnswer(c, m) }
func (rs *ReverseService) HandleDSA(c diam.Conn, m *diam.Message) { rs.deliverAnswer(c, m) }

func (rs *ReverseService) deliverAnswer(c diam.Conn, m *diam.Message) {
	sessionID, pr := rs.pendingFor(c, m)
	if pr == nil {
		return
	}
	if pr.conn != c {
		logger.Warn("Answer arrived on a connection its request was not sent on — ignored",
			logger.String("sid", sessionID), logger.String("expected_peer", pr.peerHost))
		return
	}

	// Non-blocking send because the handleReverse select may already have
	// timed out and abandoned the channel.
	select {
	case pr.ch <- m:
	default:
	}
	rs.removeRequest(sessionID)
}

// pendingFor finds the request m answers: by its Session-Id or, for an
// answer without one, by its hop-by-hop identifier on c. A protocol error
// answer need not carry a Session-Id (RFC 6733 §7.2), and an answer always
// carries its request's hop-by-hop identifier (§6.2).
func (rs *ReverseService) pendingFor(c diam.Conn, m *diam.Message) (string, *pendingReq) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if sidAVP, err := m.FindAVP(avp.SessionID, 0); err == nil {
		sid := diameter.AVPString(sidAVP)
		return sid, rs.pendingReqs[sid]
	}
	for sid, pr := range rs.pendingReqs {
		if pr.conn == c && pr.hopByHop == m.Header.HopByHopID {
			return sid, pr
		}
	}
	return "", nil
}

func (rs *ReverseService) removeRequest(sid string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	delete(rs.pendingReqs, sid)
}

// OnPeerDrop is the PeerManager listener wired in NewReverseService. When a
// peer connection closes (Unregister, idle GC, DrainAll), every pendingReqs
// entry targeting that host is signalled with a synthetic answer carrying
// Result-Code = 4012 (DIAMETER_AGENT_FAILOVER, RFC 6733 §7.1.5). The waiting
// goroutine in executeReverseHop's select unblocks immediately and the NATS
// caller sees fast-fail instead of hitting reverse_timeout.
//
// PeerManager guarantees this is called synchronously on its own goroutine
// path; we MUST NOT call back into PeerManager methods that take its lock
// (deadlock risk). Signalling channels and incrementing metrics
// is safe.
func (rs *ReverseService) OnPeerDrop(originHost string, reason diameter.PeerDropReason) {
	rs.mu.Lock()
	matched := make([]*pendingReq, 0)
	for sid, pr := range rs.pendingReqs {
		if pr.peerHost == originHost {
			matched = append(matched, pr)
			delete(rs.pendingReqs, sid)
		}
	}
	rs.mu.Unlock()

	if len(matched) == 0 {
		return
	}

	logger.Warn("Peer dropped — fast-failing pending reverse requests",
		logger.String("peer", originHost),
		logger.String("reason", string(reason)),
		logger.Int("drained", len(matched)))

	// Synthetic 4012 answer. We don't have the original request to call
	// .Answer() on; build a minimal message with just Result-Code so
	// sendNatsResponse can extract it via FindAVP(ResultCode).
	failoverAns := buildFailoverAnswer()
	for _, pr := range matched {
		select {
		case pr.ch <- failoverAns:
		default:
			// Waiter already moved on (e.g. timeout fired between our
			// map walk and signal). Acceptable race — caller has
			// already failed; we just orphan the message.
		}
	}
	monitor.IncrementCounter("diameter_pending_drained_to_nats_total",
		map[string]string{"peer": originHost, "reason": string(reason)})
}

// buildFailoverAnswer constructs a minimal Diameter message carrying just
// Result-Code = 4012. Used by OnPeerDrop to signal pendingReqs waiters.
// Not a wire-valid answer (no Origin-Host/Realm, no Session-Id) — never
// reaches the wire; consumed in-process by sendNatsResponse which only
// reads ResultCode.
func buildFailoverAnswer() *diam.Message {
	m := diam.NewMessage(0, 0, 0, 0, 0, nil)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(4012))
	return m
}
