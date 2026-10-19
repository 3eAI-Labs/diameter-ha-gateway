// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package service

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	internalNats "github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/nats-io/nats.go"
)

// Subjects an OCS sends Gy and Ro Re-Auth and Abort-Session requests on;
// the reply is the client's RAA or ASA.
const (
	TopicGyRAR = "diameter.gy.rar"
	TopicGyASR = "diameter.gy.asr"
	TopicRoRAR = "diameter.ro.rar"
	TopicRoASR = "diameter.ro.asr"
)

// SessionLookup resolves a credit-control session to the peer it runs on.
type SessionLookup interface {
	Lookup(sessionID string) (string, bool)
}

// CCReverseService sends the RAR and ASR an OCS publishes to the Gy or Ro
// client holding the session (TS 32.299 §6.4.4, §6.4.12), over the
// connection the session's CCRs arrived on, and replies with the answer.
// A 3006 answer is passed to the OCS; redirects are not followed.
type CCReverseService struct {
	natsClient  *internalNats.Client
	peerManager *diameter.PeerManager
	sessions    SessionLookup
	cfg         *config.Config

	inflight internalNats.Inflight
	mu       sync.Mutex
	pending  map[string]*ccPending
}

type ccPending struct {
	ch       chan *diam.Message
	conn     diam.Conn
	peerHost string
	code     uint32
	hopByHop uint32
}

func NewCCReverseService(nc *internalNats.Client, pm *diameter.PeerManager, sessions SessionLookup, cfg *config.Config) *CCReverseService {
	s := &CCReverseService{natsClient: nc, peerManager: pm, sessions: sessions, cfg: cfg,
		pending: make(map[string]*ccPending), inflight: internalNats.NewInflight()}
	pm.AddDropListener(s.OnPeerDrop)
	return s
}

func (s *CCReverseService) StartListening() error {
	for subject, handle := range map[string]func(*nats.Msg){
		TopicGyRAR: s.handleRAR, TopicRoRAR: s.handleRAR,
		TopicGyASR: s.handleASR, TopicRoASR: s.handleASR,
	} {
		// Each request waits for its client concurrently
		// (internal/nats/inflight.go): a client that does not answer
		// delays no other session.
		if _, err := s.natsClient.QueueSubscribe(subject, "diameter-gw", s.inflight.Wrap(handle, s.busy)); err != nil {
			return err
		}
	}
	return nil
}

func (s *CCReverseService) busy(msg *nats.Msg) {
	s.reply(msg, charging.AbortAnswer{ResultCode: diam.TooBusy, ErrorMessage: "too many requests pending"})
}

func (s *CCReverseService) handleRAR(msg *nats.Msg) {
	var r charging.ReAuthRequest
	if !s.parse(msg, "RAR", &r) {
		return
	}
	if r.ReAuthRequestType > 1 { // AUTHORIZE_ONLY 0, AUTHORIZE_AUTHENTICATE 1 (RFC 6733 §8.12)
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.InvalidAVPValue, ErrorMessage: "re_auth_request_type is 0 or 1"})
		return
	}
	s.send(msg, "RAR", r.Header, func(destHost, destRealm string) *diam.Message {
		return charging.BuildRAR(&r, destHost, destRealm, s.cfg.Server.OriginHost, s.cfg.Server.OriginRealm)
	})
}

func (s *CCReverseService) handleASR(msg *nats.Msg) {
	var r charging.AbortSessionRequest
	if !s.parse(msg, "ASR", &r) {
		return
	}
	s.send(msg, "ASR", r.Header, func(destHost, destRealm string) *diam.Message {
		return charging.BuildASR(&r, destHost, destRealm, s.cfg.Server.OriginHost, s.cfg.Server.OriginRealm)
	})
}

func (s *CCReverseService) parse(msg *nats.Msg, cmd string, v any) bool {
	monitor.IncrementCounter("diameter_cc_reverse_requests_total", map[string]string{"cmd": cmd})
	if err := json.Unmarshal(msg.Data, v); err != nil {
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.InvalidAVPValue, ErrorMessage: "request is not valid JSON"})
		return false
	}
	return true
}

// target is the client of the session: its binding, else the request's
// destination_host, else its origin_host as echoed from the CCR.
func (s *CCReverseService) target(h charging.DiameterHeader) string {
	if s.sessions != nil {
		if host, ok := s.sessions.Lookup(h.SessionID); ok {
			return host
		}
	}
	if h.DestinationHost != "" {
		return h.DestinationHost
	}
	return h.OriginHost
}

func (s *CCReverseService) timeout() time.Duration {
	if d, err := time.ParseDuration(s.cfg.Server.ReverseTimeout); err == nil && d > 0 {
		return d
	}
	return 5 * time.Second
}

func (s *CCReverseService) send(msg *nats.Msg, cmd string, h charging.DiameterHeader, build func(destHost, destRealm string) *diam.Message) {
	if h.SessionID == "" {
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.MissingAVP, ErrorMessage: "missing header.session_id"})
		return
	}
	destHost := s.target(h)
	conn := s.peerManager.GetConnection(destHost)
	if destHost == "" || conn == nil {
		logger.Warn("Gy/Ro reverse: client not connected", logger.String("cmd", cmd), logger.String("dest_host", destHost))
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.UnableToDeliver, ErrorMessage: "client not connected"})
		return
	}
	destRealm := h.DestinationRealm
	if destRealm == "" {
		destRealm = s.cfg.Server.OriginRealm
	}
	m := build(destHost, destRealm)

	ch := make(chan *diam.Message, 1)
	key := h.SessionID + "/" + cmd
	s.mu.Lock()
	if _, busy := s.pending[key]; busy {
		s.mu.Unlock()
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.TooBusy, ErrorMessage: cmd + " already pending for the session"})
		return
	}
	s.pending[key] = &ccPending{ch: ch, conn: conn, peerHost: destHost, code: m.Header.CommandCode, hopByHop: m.Header.HopByHopID}
	s.mu.Unlock()
	defer s.remove(key)

	if _, err := m.WriteTo(conn); err != nil {
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.UnableToDeliver, ErrorMessage: "request write failed"})
		return
	}
	select {
	case ans := <-ch:
		if ans == nil {
			s.reply(msg, charging.AbortAnswer{ResultCode: diam.UnableToDeliver, ErrorMessage: "client disconnected"})
			return
		}
		rc, exp := diameter.AnswerResult(ans)
		s.reply(msg, charging.ReadAbortAnswer(ans, rc, exp))
	case <-time.After(s.timeout()):
		s.reply(msg, charging.AbortAnswer{ResultCode: diam.UnableToDeliver, ErrorMessage: "no answer from client"})
	}
}

// HandleRAA and HandleASA take the answers of Application-Id 4.
func (s *CCReverseService) HandleRAA(c diam.Conn, m *diam.Message) { s.deliver(c, m) }
func (s *CCReverseService) HandleASA(c diam.Conn, m *diam.Message) { s.deliver(c, m) }

// deliver hands m to the request it answers: the same Session-Id (when the
// answer carries one) and command, the same hop-by-hop identifier (RFC 6733
// §6.2), on the connection the request was sent on. A late answer to a
// request that timed out does not answer the request sent after it.
func (s *CCReverseService) deliver(c diam.Conn, m *diam.Message) {
	cmd := "RAR"
	if m.Header.CommandCode == charging.CommandASR {
		cmd = "ASR"
	}
	s.mu.Lock()
	var pr *ccPending
	if a, err := m.FindAVP(avp.SessionID, 0); err == nil {
		pr = s.pending[diameter.AVPString(a)+"/"+cmd]
	} else {
		for _, p := range s.pending {
			if p.conn == c && p.hopByHop == m.Header.HopByHopID {
				pr = p
				break
			}
		}
	}
	s.mu.Unlock()
	if pr == nil || pr.conn != c || pr.code != m.Header.CommandCode || pr.hopByHop != m.Header.HopByHopID {
		return
	}
	select {
	case pr.ch <- m:
	default:
	}
}

// OnPeerDrop fails the requests waiting on a peer that disconnected, at
// once rather than at the timeout.
func (s *CCReverseService) OnPeerDrop(host string, _ diameter.PeerDropReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pending {
		if p.peerHost == host {
			select {
			case p.ch <- nil:
			default:
			}
		}
	}
}

func (s *CCReverseService) remove(key string) {
	s.mu.Lock()
	delete(s.pending, key)
	s.mu.Unlock()
}

func (s *CCReverseService) reply(msg *nats.Msg, a charging.AbortAnswer) {
	if msg.Reply == "" {
		return
	}
	data, _ := json.Marshal(a)
	if err := s.natsClient.Respond(msg, data); err != nil {
		logger.Warn("Gy/Ro reverse: reply failed", logger.String("err", err.Error()))
	}
}
