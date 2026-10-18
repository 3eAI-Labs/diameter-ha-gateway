// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

// Package service — GxReverseService dispatches PCRF-initiated Gx commands
// (RAR/ASR) from NATS into Diameter requests sent to the appropriate PCEF
// peer.
//
// Differences from the S6a ReverseService:
//
//   - Routing is by Session-Id, not IMSI. The GxSessionRegistry tracks the
//     Session-Id → PCEF Origin-Host binding established at CCR-I and torn
//     down at CCR-T (or peer drop).
//   - Session-Id is NOT freshly generated on each RAR/ASR — the request
//     rides the existing PCEF session, so the caller (PCRF) MUST supply
//     the original session id in event.Header.SessionID.
//   - STR is intentionally absent; STR is PCEF→PCRF forward direction.
//     PCRF-initiated session terminate uses ASR.
//
// Same machinery as S6a reverse: pendingReqs[sessionID] correlation, fast-
// fail on peer drop, redirect-honor retry per RetryPostureFor (RAR=
// RetryAlways, ASR=RetryIfOperatorOptsIn), T-bit retransmit on hop>0.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/gx"
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

// GxReverseService routes PCRF-initiated RAR/ASR over NATS into Diameter
// requests for the PCEF peer that originated the Gx session.
type GxReverseService struct {
	natsClient    *internalNats.Client
	peerManager   *diameter.PeerManager
	gxTranslator  *gx.Translator
	registry      *gx.GxSessionRegistry
	redirectCache *diameter.RedirectCache // shared with S6a reverse path

	cfg      *config.Config
	reloader *config.Reloader

	mu          sync.RWMutex
	pendingReqs map[string]*gxPendingReq
}

// gxPendingReq couples a waiter channel with the peer host the request was
// sent to. Peer-drop fast-fail uses peerHost to find every in-
// flight request for a dropped connection.
type gxPendingReq struct {
	ch       chan *diam.Message
	peerHost string
	verb     string    // "RAR" / "ASR" — for metrics on drop
	conn     diam.Conn // only an answer arriving on this connection is delivered
	hopByHop uint32    // identifies the request on conn, for an answer without Session-Id
}

// NewGxReverseService wires up the service and registers a peer-drop
// listener so connection loss synthesizes a 4012 (AGENT_FAILOVER) for every
// in-flight RAR/ASR to the dropped peer (S6a parity).
//
// redirectCache is shared with the S6a ReverseService — every cache entry
// is keyed by Usage scope (ALL_REALM, ALL_HOST, ...) so cross-application
// sharing is correct: a 3006 received on Gx for ALL_REALM benefits future
// S6a routing decisions for that realm and vice versa.
func NewGxReverseService(
	nc *internalNats.Client,
	pm *diameter.PeerManager,
	registry *gx.GxSessionRegistry,
	redirectCache *diameter.RedirectCache,
	cfg *config.Config,
) *GxReverseService {
	s := &GxReverseService{
		natsClient:    nc,
		peerManager:   pm,
		gxTranslator:  gx.NewTranslator(),
		registry:      registry,
		redirectCache: redirectCache,
		cfg:           cfg,
		pendingReqs:   make(map[string]*gxPendingReq),
	}
	pm.AddDropListener(s.OnPeerDrop)
	return s
}

// SetReloader plugs in the live config snapshot for hot-reloadable reads
// (timeout, opt-in flags). Optional.
func (s *GxReverseService) SetReloader(r *config.Reloader) { s.reloader = r }

func (s *GxReverseService) activeConfig() *config.Config {
	if s.reloader != nil {
		if c := s.reloader.Get(); c != nil {
			return c
		}
	}
	return s.cfg
}

func (s *GxReverseService) reverseTimeout() time.Duration {
	if d, err := time.ParseDuration(s.activeConfig().Server.ReverseTimeout); err == nil && d > 0 {
		return d
	}
	return 5 * time.Second
}

// StartListening subscribes to the Gx reverse-direction NATS topics with the
// shared "diameter-gw" queue group so multiple gateway instances load-
// balance.
func (s *GxReverseService) StartListening() {
	s.subscribe(gx.TopicGxRAR, "RAR", s.HandleRAR)
	s.subscribe(gx.TopicGxASR, "ASR", s.HandleASR)
}

func (s *GxReverseService) subscribe(topic, label string, handler func(*nats.Msg)) {
	if _, err := s.natsClient.QueueSubscribe(topic, "diameter-gw", handler); err != nil {
		logger.Error("Failed to subscribe to Gx reverse topic", err,
			logger.String("topic", topic),
			logger.String("cmd", label))
		return
	}
	logger.Info("Listening for Reverse Gx Requests",
		logger.String("topic", topic),
		logger.String("cmd", label))
}

// ─── NATS handlers ──────────────────────────────────────────────────────────

// HandleRAR is invoked when the PCRF backend publishes diameter.gx.rar.
func (s *GxReverseService) HandleRAR(msg *nats.Msg) {
	monitor.IncrementCounter("diameter_gx_reverse_requests_total",
		map[string]string{"cmd": "RAR"})

	var req gx.GxReAuthRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		logger.Error("Failed to unmarshal GxReAuthRequest", err)
		s.replyError(msg, 5004) // INVALID_AVP_VALUE
		return
	}
	if req.Header.SessionID == "" {
		logger.Warn("RAR event missing header.session_id — required for routing")
		s.replyError(msg, 5005) // MISSING_AVP equivalent
		return
	}

	destHost, ok := s.resolveTargetPeer(req.Header.SessionID, req.Header.OriginHost)
	if !ok {
		logger.Warn("RAR target PCEF unknown — neither registry nor event.source_host resolved",
			logger.String("session_id", req.Header.SessionID))
		s.replyError(msg, 3002) // UNABLE_TO_DELIVER
		return
	}

	build := func(destRealm string) *diam.Message {
		return s.gxTranslator.CreateRAR(req.Header.SessionID, destHost, destRealm,
			s.cfg.Server.OriginHost, s.cfg.Server.OriginRealm, &req)
	}
	s.executeGxHop(msg, "RAR", req.Header.SessionID, req.Header.DestinationRealm,
		destHost, build, redirectState{visited: []string{s.cfg.Server.OriginHost}})
}

// HandleASR is invoked when the PCRF backend publishes diameter.gx.asr.
func (s *GxReverseService) HandleASR(msg *nats.Msg) {
	monitor.IncrementCounter("diameter_gx_reverse_requests_total",
		map[string]string{"cmd": "ASR"})

	var req gx.GxAbortSessionRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		logger.Error("Failed to unmarshal GxAbortSessionRequest", err)
		s.replyError(msg, 5004)
		return
	}
	if req.Header.SessionID == "" {
		logger.Warn("ASR event missing header.session_id — required for routing")
		s.replyError(msg, 5005)
		return
	}

	destHost, ok := s.resolveTargetPeer(req.Header.SessionID, req.Header.OriginHost)
	if !ok {
		logger.Warn("ASR target PCEF unknown",
			logger.String("session_id", req.Header.SessionID))
		s.replyError(msg, 3002)
		return
	}

	build := func(destRealm string) *diam.Message {
		return s.gxTranslator.CreateASR(req.Header.SessionID, destHost, destRealm,
			s.cfg.Server.OriginHost, s.cfg.Server.OriginRealm, &req)
	}
	s.executeGxHop(msg, "ASR", req.Header.SessionID, req.Header.DestinationRealm,
		destHost, build, redirectState{visited: []string{s.cfg.Server.OriginHost}})
}

// resolveTargetPeer returns the PCEF Origin-Host to send the reverse request
// to. Lookup precedence:
//
//  1. GxSessionRegistry binding (CCR-I → Bind, authoritative)
//  2. event.Header.SourceHost (PCRF-supplied hint when registry is cold,
//     e.g. after a gateway restart while sessions are still alive on PCEF)
//
// Returns (host, true) on success; ("", false) when neither source resolves.
func (s *GxReverseService) resolveTargetPeer(sessionID, hint string) (string, bool) {
	if host, ok := s.registry.Lookup(sessionID); ok {
		return host, true
	}
	if hint != "" {
		monitor.IncrementCounter("diameter_gx_session_lookup_fallback_total", nil)
		return hint, true
	}
	return "", false
}

// ─── Send pipeline ──────────────────────────────────────────────────────────

// executeGxHop sends a freshly built request and registers the pending
// waiter. Mirrors S6a ReverseService.executeReverseHop — same redirect
// retry, T-bit retransmit, and timeout semantics.
func (s *GxReverseService) executeGxHop(
	msg *nats.Msg,
	cmd, sessionID, eventDestRealm, destHost string,
	build func(string) *diam.Message,
	state redirectState,
) {
	conn := s.peerManager.GetConnection(destHost)
	if conn == nil {
		logger.Warn("PCEF peer not connected",
			logger.String("host", destHost), logger.String("cmd", cmd))
		monitor.IncrementCounter("diameter_gx_reverse_peer_not_found_total",
			map[string]string{"cmd": cmd})
		s.replyError(msg, 3002)
		return
	}

	destRealm := eventDestRealm
	if destRealm == "" {
		destRealm = s.cfg.Server.OriginRealm
	}
	diamMsg := build(destRealm)

	// No T flag on a redirect hop: it follows an error answer (3006), after
	// which RFC 6733 §3 forbids it.

	respCh := make(chan *diam.Message, 1)
	s.mu.Lock()
	s.pendingReqs[sessionID] = &gxPendingReq{ch: respCh, peerHost: destHost, verb: cmd, conn: conn,
		hopByHop: diamMsg.Header.HopByHopID}
	s.mu.Unlock()

	logger.Info(fmt.Sprintf("Sending %s to PCEF", cmd),
		logger.String("pcef", destHost),
		logger.String("session_id", sessionID),
		logger.Int("redirect_depth", state.depth))

	audit.Log(context.Background(), "reverse-trigger", "GX_"+cmd+"_SENT",
		"pcef:"+destHost, "session:"+sessionID, nil)

	if _, err := diamMsg.WriteTo(conn); err != nil {
		logger.Error(fmt.Sprintf("Failed to send %s", cmd), err)
		s.removePending(sessionID)
		s.replyError(msg, 3004)
		return
	}

	timeout := s.reverseTimeout()
	select {
	case answer := <-respCh:
		s.handleGxAnswer(msg, cmd, sessionID, eventDestRealm, destHost, build, state, answer)
	case <-time.After(timeout):
		logger.Warn(fmt.Sprintf("Timeout waiting for %s answer", cmd),
			logger.String("session_id", sessionID),
			logger.String("timeout", timeout.String()))
		monitor.IncrementCounter("diameter_gx_reverse_timeout_total",
			map[string]string{"cmd": cmd})
		s.removePending(sessionID)
		s.replyError(msg, 3002)
	}
}

// handleGxAnswer routes inbound answers through the §6.5 redirect path when
// applicable. Non-3006 answers and 3006 without retry posture pass through
// to the NATS caller verbatim.
func (s *GxReverseService) handleGxAnswer(
	msg *nats.Msg,
	cmd, sessionID, eventDestRealm, destHost string,
	build func(string) *diam.Message,
	state redirectState,
	answer *diam.Message,
) {
	ind, isRedirect := diameter.ParseRedirectIndication(answer)
	if !isRedirect {
		s.replyAnswer(msg, cmd, answer)
		return
	}

	monitor.IncrementCounter("diameter_redirect_received_total", map[string]string{
		"direction": "reverse_gx",
		"usage":     ind.Usage.String(),
	})
	s.redirectCache.Store(ind, diameter.RedirectContext{
		SessionID:     sessionID,
		DestRealm:     eventDestRealm,
		DestHost:      destHost,
		ApplicationID: answer.Header.ApplicationID,
	})

	posture := diameter.RetryPostureFor(cmd)
	if posture == diameter.RetryIfOperatorOptsIn && !s.optInForVerb(cmd) {
		posture = diameter.NeverRetry
	}
	if posture == diameter.NeverRetry {
		monitor.IncrementCounter("diameter_redirect_idempotency_blocked_total",
			map[string]string{"verb": cmd})
		logger.Info("Gx redirect indication received but verb is non-idempotent — passing 3006 to NATS",
			logger.String("cmd", cmd),
			logger.String("from", destHost))
		s.replyAnswer(msg, cmd, answer)
		return
	}

	maxDepth := s.activeConfig().Server.RedirectMaxDepth
	visited := append(collectRouteRecordHosts(answer), state.visited...)
	visited = append(visited, destHost)

	nextHost, nextURI, ok := s.pickNextRedirect(ind.Hosts, visited, state.depth, maxDepth)
	if !ok {
		monitor.IncrementCounter("diameter_redirect_loop_detected_total",
			map[string]string{"peer": destHost})
		alarm.Raise(alarm.SeverityWarning, "DIAM_GW_GX_REDIRECT_LOOP",
			fmt.Sprintf("Gx redirect chain exhausted for %s from %s (depth=%d)",
				cmd, destHost, state.depth))
		s.replyAnswer(msg, cmd, answer)
		return
	}

	monitor.IncrementCounter("diameter_redirect_retry_total", map[string]string{
		"verb":  cmd,
		"depth": fmt.Sprintf("%d", state.depth+1),
	})
	audit.Log(context.Background(), "redirect-honor", "BUS_GX_REDIRECT_HONORED",
		fmt.Sprintf("from=%s to=%s usage=%s depth=%d", destHost, nextHost,
			ind.Usage.String(), state.depth+1),
		"session:"+sessionID, map[string]string{"redirect_uri": nextURI})

	s.executeGxHop(msg, cmd, sessionID, eventDestRealm, nextHost, build, redirectState{
		depth:   state.depth + 1,
		visited: visited,
	})
}

func (s *GxReverseService) optInForVerb(verb string) bool {
	c := s.activeConfig().Server
	if verb == "ASR" {
		return c.RedirectASRRetryEnabled
	}
	return false
}

// pickNextRedirect walks indication.Hosts and returns the first non-loop
// candidate. Same DetectLoop semantics as S6a (depth + visited + max).
func (s *GxReverseService) pickNextRedirect(uris, visited []string, depth, maxDepth int) (string, string, bool) {
	for _, uri := range uris {
		host := hostFromDiameterURI(uri)
		if host == "" {
			continue
		}
		if reason := diameter.DetectLoop(host, visited, depth+1, maxDepth); reason != "" {
			if reason == "redirect_depth_exceeded" {
				monitor.IncrementCounter("diameter_redirect_loop_detected_total",
					map[string]string{"peer": "depth"})
				alarm.Raise(alarm.SeverityWarning, "DIAM_GW_GX_REDIRECT_DEPTH_EXCEEDED",
					fmt.Sprintf("Gx redirect depth=%d exceeded max=%d", depth+1, maxDepth))
				return "", "", false
			}
			continue
		}
		return host, uri, true
	}
	return "", "", false
}

// ─── Diameter answer handlers (state-machine routes) ───────────────────────

// HandleRAA correlates an inbound Re-Auth-Answer to the pending RAR.
func (s *GxReverseService) HandleRAA(c diam.Conn, m *diam.Message) { s.deliverAnswer(c, m) }

// HandleASA correlates an inbound Abort-Session-Answer to the pending ASR.
func (s *GxReverseService) HandleASA(c diam.Conn, m *diam.Message) { s.deliverAnswer(c, m) }

func (s *GxReverseService) deliverAnswer(c diam.Conn, m *diam.Message) {
	sessionID, pr := s.pendingFor(c, m)
	if pr == nil {
		return
	}
	if pr.conn != c {
		logger.Warn("Gx answer arrived on a connection its request was not sent on — ignored",
			logger.String("session_id", sessionID), logger.String("expected_peer", pr.peerHost))
		return
	}
	select {
	case pr.ch <- m:
	default:
	}
	s.removePending(sessionID)
}

// pendingFor finds the request m answers: by its Session-Id or, for an
// answer without one (RFC 6733 §7.2), by its hop-by-hop identifier on c.
func (s *GxReverseService) pendingFor(c diam.Conn, m *diam.Message) (string, *gxPendingReq) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sidAVP, err := m.FindAVP(avp.SessionID, 0); err == nil {
		sid := avpStringValue(sidAVP)
		return sid, s.pendingReqs[sid]
	}
	for sid, pr := range s.pendingReqs {
		if pr.conn == c && pr.hopByHop == m.Header.HopByHopID {
			return sid, pr
		}
	}
	return "", nil
}

func (s *GxReverseService) removePending(sid string) {
	s.mu.Lock()
	delete(s.pendingReqs, sid)
	s.mu.Unlock()
}

// ─── Peer drop fast-fail (S6a parity) ──────────────────────────────────────

// OnPeerDrop is wired in NewGxReverseService. When a PCEF connection closes,
// every in-flight RAR/ASR for that host is signaled with a synthetic 4012
// (AGENT_FAILOVER) so the NATS caller fast-fails instead of waiting for
// reverse_timeout. Also clears every Session-Id binding for that peer so
// future RAR/ASR don't try to route to the dead peer.
func (s *GxReverseService) OnPeerDrop(originHost string, reason diameter.PeerDropReason) {
	s.mu.Lock()
	matched := make([]*gxPendingReq, 0)
	for sid, pr := range s.pendingReqs {
		if pr.peerHost == originHost {
			matched = append(matched, pr)
			delete(s.pendingReqs, sid)
		}
	}
	s.mu.Unlock()

	// Always drop session bindings (even when no in-flight matches), since
	// future RAR/ASR for those sessions would otherwise route to a dead
	// peer connection.
	s.registry.DropPeer(originHost, reason)

	if len(matched) == 0 {
		return
	}
	logger.Warn("PCEF peer dropped — fast-failing pending Gx reverse requests",
		logger.String("peer", originHost),
		logger.String("reason", string(reason)),
		logger.Int("drained", len(matched)))

	failoverAns := buildGxFailoverAnswer()
	for _, pr := range matched {
		select {
		case pr.ch <- failoverAns:
		default:
		}
		monitor.IncrementCounter("diameter_gx_pending_drained_to_nats_total",
			map[string]string{"peer": originHost, "reason": string(reason), "verb": pr.verb})
	}
}

func buildGxFailoverAnswer() *diam.Message {
	m := diam.NewMessage(0, 0, 0, 0, 0, nil)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(4012))
	return m
}

// ─── NATS reply helpers ────────────────────────────────────────────────────

func (s *GxReverseService) replyAnswer(msg *nats.Msg, cmd string, diamMsg *diam.Message) {
	if msg.Reply == "" {
		return
	}
	resultCode, expCode := diameter.AnswerResult(diamMsg)
	switch cmd {
	case "RAR":
		resp := gx.GxReAuthAnswer{ResultCode: resultCode, ExperimentalResultCode: expCode,
			ChargingRuleReport: gx.NewTranslator().ChargingRuleReports(diamMsg)}
		data, _ := json.Marshal(resp)
		s.natsClient.Respond(msg, data)
	case "ASR":
		resp := gx.GxAbortSessionAnswer{ResultCode: resultCode, ExperimentalResultCode: expCode}
		data, _ := json.Marshal(resp)
		s.natsClient.Respond(msg, data)
	}
}

func (s *GxReverseService) replyError(msg *nats.Msg, code uint32) {
	if msg.Reply == "" {
		return
	}
	resp := struct {
		ResultCode uint32 `json:"result_code"`
	}{ResultCode: code}
	data, _ := json.Marshal(resp)
	s.natsClient.Respond(msg, data)
}

// ─── Helpers shared with S6a reverse path ──────────────────────────────────

// collectRouteRecordHosts pulls every Route-Record AVP from an answer for
// loop-detection seeding. Mirrors ReverseService.collectRouteRecord — kept
// duplicated rather than exported across services to avoid coupling them
// (each service may evolve its loop semantics independently).
func collectRouteRecordHosts(m *diam.Message) []string {
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

func avpStringValue(a *diam.AVP) string {
	switch v := a.Data.(type) {
	case datatype.UTF8String:
		return string(v)
	case datatype.DiameterIdentity:
		return string(v)
	default:
		return "" // String() is go-diameter's debug format, not the value
	}
}
