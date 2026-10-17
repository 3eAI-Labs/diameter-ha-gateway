// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/alarm"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/audit"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/sm"
)

type Handler interface {
	HandleULR(c diam.Conn, m *diam.Message)
	HandleAIR(c diam.Conn, m *diam.Message)
	HandlePUR(c diam.Conn, m *diam.Message)
	HandleNOR(c diam.Conn, m *diam.Message)
	HandleECR(c diam.Conn, m *diam.Message)
}

type AnswerHandler interface {
	HandleCLA(c diam.Conn, m *diam.Message)
	HandleIDA(c diam.Conn, m *diam.Message)
	// Gateway-initiated RSR answer correlation.
	HandleRSA(c diam.Conn, m *diam.Message)
	// HSS-initiated DSR (Delete-Subscriber-Data) answer correlation
	// (TS 29.272 §6.1.4).
	HandleDSA(c diam.Conn, m *diam.Message)
}

// ChargingHandler handles Gy/Ro charging interface messages
type ChargingHandler interface {
	HandleCCR(c diam.Conn, m *diam.Message)
}

// SGdHandler handles SGd SMS interface messages (TS 29.338)
type SGdHandler interface {
	HandleOFR(c diam.Conn, m *diam.Message) // MO-Forward-Short-Message
	HandleTFR(c diam.Conn, m *diam.Message) // MT-Forward-Short-Message
	HandleSRR(c diam.Conn, m *diam.Message) // Send-Routing-Info-for-SM
	HandleALR(c diam.Conn, m *diam.Message) // Alert-Service-Centre
}

// SGdAnswerHandler handles SGd answer messages (reverse flow: SMSC → HSS/MME)
type SGdAnswerHandler interface {
	HandleSRA(c diam.Conn, m *diam.Message) // Send-Routing-Info-for-SM-Answer
	HandleTFA(c diam.Conn, m *diam.Message) // MT-Forward-Short-Message-Answer
	HandleALA(c diam.Conn, m *diam.Message) // Alert-Service-Centre-Answer
}

// GxAnswerHandler handles Gx reverse-direction answer messages (PCEF → PCRF
// reply path for gateway-issued RAR/ASR).
type GxAnswerHandler interface {
	HandleRAA(c diam.Conn, m *diam.Message) // Re-Auth-Answer
	HandleASA(c diam.Conn, m *diam.Message) // Abort-Session-Answer
}

type DiameterServer struct {
	cfg              *config.Config   // bootstrap snapshot — used for non-reloadable startup fields
	reloader         *config.Reloader // optional; when set, hot-reloadable reads (peer whitelist) consult it
	stateMachine     *sm.StateMachine
	handler          Handler
	answerHandler    AnswerHandler
	chargingHandler  ChargingHandler
	sgdHandler       SGdHandler
	sgdAnswerHandler SGdAnswerHandler
	gxAnswerHandler  GxAnswerHandler
	peerManager      *PeerManager
	translator       *Translator      // builds DPA / future answer messages for handlers that don't go through PeerManager
	rateLimiter      *PeerRateLimiter // per-peer admit gate; nil = no rate limiting
}

func NewServer(cfg *config.Config, h Handler, ah AnswerHandler, pm *PeerManager) *DiameterServer {
	// Build rate limiter from cfg.Peers[].RateLimitTPS. Bootstrap-only for
	// now — config reload doesn't rebuild the limiter.
	tpsByHost := make(map[string]int, len(cfg.Peers))
	for _, p := range cfg.Peers {
		if p.RateLimitTPS > 0 {
			tpsByHost[p.Name] = p.RateLimitTPS
		}
	}
	return &DiameterServer{
		cfg:           cfg,
		handler:       h,
		answerHandler: ah,
		peerManager:   pm,
		translator:    NewTranslator(),
		rateLimiter:   NewPeerRateLimiter(tpsByHost, 0), // 0 = use default 2× burst
	}
}

// SetReloader plugs in a config reloader so hot-reloadable reads (e.g. peer
// whitelist matching) see the latest committed snapshot without needing a
// gateway restart. Optional — a nil reloader keeps the snapshot semantics.
func (s *DiameterServer) SetReloader(r *config.Reloader) {
	s.reloader = r
}

// activeConfig returns the freshest config snapshot for hot-reloadable reads.
// Falls back to the bootstrap snapshot when no reloader is wired (tests).
func (s *DiameterServer) activeConfig() *config.Config {
	if s.reloader != nil {
		if cfg := s.reloader.Get(); cfg != nil {
			return cfg
		}
	}
	return s.cfg
}

// SetChargingHandler sets the Gy/Ro charging handler
func (s *DiameterServer) SetChargingHandler(ch ChargingHandler) {
	s.chargingHandler = ch
}

// SetSGdHandler sets the SGd SMS handler (TS 29.338)
func (s *DiameterServer) SetSGdHandler(sh SGdHandler) {
	s.sgdHandler = sh
}

// SetSGdAnswerHandler sets the SGd answer handler for reverse flow (SMSC → HSS/MME)
func (s *DiameterServer) SetSGdAnswerHandler(sah SGdAnswerHandler) {
	s.sgdAnswerHandler = sah
}

// SetGxAnswerHandler sets the Gx reverse-direction answer handler (RAA/ASA).
func (s *DiameterServer) SetGxAnswerHandler(gah GxAnswerHandler) {
	s.gxAnswerHandler = gah
}

func (s *DiameterServer) Start(ctx context.Context) error {
	// Diameter Server Setup
	s.stateMachine = sm.New(&sm.Settings{
		OriginHost:  datatype.DiameterIdentity(s.cfg.Server.OriginHost),
		OriginRealm: datatype.DiameterIdentity(s.cfg.Server.OriginRealm),
		ProductName: datatype.UTF8String(s.cfg.Server.ProductName),
	})

	// CER is not registered here: the state machine answers it itself and
	// refuses a handler for it. peerGate authorises the peer first.
	// The state machine reports refused registrations and unhandled
	// messages on its error channel; log them.
	go s.logErrorReports(ctx)

	// Device-Watchdog-Answer observer — feeds the explicit watchdog.
	s.stateMachine.HandleFunc("DWA", s.handleDWA)
	// Disconnect-Peer-Request handler — peer-initiated graceful disconnect.
	// We emit DPA ourselves; go-diameter's sm package only auto-
	// handles CER/CEA + DWR/DWA, NOT DPR/DPA.
	s.stateMachine.HandleFunc("DPR", s.handleDPR)
	// Disconnect-Peer-Answer handler — correlation for our own DPRs
	// emitted from PeerManager.DrainAll. Without this, DrainAll would still
	// fall back to a time-based grace; with it, peers that ack quickly are
	// closed cleanly without paying the timeout.
	s.stateMachine.HandleFunc("DPA", s.handleDPA)

	// S6a Request Handlers — wrapped in admitGate so per-peer rate limit
	// sheds load with 3004 (TOO_BUSY) before the handler does
	// any NATS / backend work. Lifecycle messages (CER/DWR/DPR) and answer
	// correlation (CLA/IDA/RSA/DSA) are intentionally NOT gated.
	s.stateMachine.HandleFunc("ULR", s.admitGate(s.handler.HandleULR))
	s.stateMachine.HandleFunc("AIR", s.admitGate(s.handler.HandleAIR))
	s.stateMachine.HandleFunc("PUR", s.admitGate(s.handler.HandlePUR))
	s.stateMachine.HandleFunc("NOR", s.admitGate(s.handler.HandleNOR))

	// S13 Request Handlers
	s.stateMachine.HandleFunc("ECR", s.admitGate(s.handler.HandleECR))

	// S6a Answer Handlers (Reverse Path)
	if s.answerHandler != nil {
		s.stateMachine.HandleFunc("CLA", s.answerHandler.HandleCLA)
		s.stateMachine.HandleFunc("IDA", s.answerHandler.HandleIDA)
		s.stateMachine.HandleFunc("RSA", s.answerHandler.HandleRSA)
		// DSA correlation for HSS-initiated DSR (TS 29.272 §6.1.4).
		s.stateMachine.HandleFunc("DSA", s.answerHandler.HandleDSA)
	}

	// Gy/Ro Charging Handlers (Credit-Control)
	// Note: go-diameter uses "CCR" for Credit-Control-Request (CC + R for Request)
	if s.chargingHandler != nil {
		s.stateMachine.HandleFunc("CCR", s.admitGate(s.chargingHandler.HandleCCR))
	}

	// SGd SMS Handlers (TS 29.338) — gated for the same DDoS-protection
	// rationale as S6a; SMS-bombing patterns make this gate operationally
	// important.
	// Command codes: OFR(8388645), TFR(8388646), SRR(8388647), ALR(8388648)
	if s.sgdHandler != nil {
		s.stateMachine.HandleFunc("OFR", s.admitGate(s.sgdHandler.HandleOFR)) // MO-Forward-Short-Message
		s.stateMachine.HandleFunc("TFR", s.admitGate(s.sgdHandler.HandleTFR)) // MT-Forward-Short-Message
		s.stateMachine.HandleFunc("SRR", s.admitGate(s.sgdHandler.HandleSRR)) // Send-Routing-Info-for-SM
		s.stateMachine.HandleFunc("ALR", s.admitGate(s.sgdHandler.HandleALR)) // Alert-Service-Centre
		logger.Info("SGd SMS handlers registered")
	}

	// SGd Answer Handlers (Reverse flow: SMSC → HSS/MME)
	if s.sgdAnswerHandler != nil {
		s.stateMachine.HandleFunc("SRA", s.sgdAnswerHandler.HandleSRA) // Send-Routing-Info-for-SM-Answer
		s.stateMachine.HandleFunc("TFA", s.sgdAnswerHandler.HandleTFA) // MT-Forward-Short-Message-Answer
		s.stateMachine.HandleFunc("ALA", s.sgdAnswerHandler.HandleALA) // Alert-Service-Centre-Answer
		logger.Info("SGd SMS answer handlers registered (reverse flow)")
	}

	// Gx Answer Handlers (Reverse flow: PCEF → PCRF reply for gateway-issued
	// RAR/ASR).
	if s.gxAnswerHandler != nil {
		s.stateMachine.HandleFunc("RAA", s.gxAnswerHandler.HandleRAA) // Re-Auth-Answer
		s.stateMachine.HandleFunc("ASA", s.gxAnswerHandler.HandleASA) // Abort-Session-Answer
		logger.Info("Gx reverse-direction answer handlers registered (RAA/ASA)")
	}

	logger.Info("Starting Diameter Server",
		logger.String("addr", s.cfg.Server.ListenAddr),
		logger.String("network", s.cfg.Server.Network),
		logger.String("tls_enabled", fmt.Sprintf("%t", s.cfg.Server.TLS.Enabled)))

	if s.cfg.Server.TLS.Enabled {
		return s.serveTLS()
	}
	return diam.ListenAndServeNetwork(s.cfg.Server.Network, s.cfg.Server.ListenAddr, s.gate(), nil)
}

// gate puts peer authorisation and panic recovery in front of the state
// machine (peer_gate.go).
func (s *DiameterServer) gate() *peerGate {
	return &peerGate{
		next:      s.stateMachine,
		authorize: s.authorizeCER,
		register: func(c diam.Conn, originHost string) {
			if s.peerManager != nil {
				s.peerManager.Register(c, originHost)
			}
		},
		answer: func(c diam.Conn, m *diam.Message, resultCode uint32) {
			a := CompleteAnswer(m.Answer(resultCode), m, s.cfg.Server.OriginHost, s.cfg.Server.OriginRealm)
			if _, err := a.WriteTo(c); err != nil {
				logger.Warn("answer write failed", logger.String("err", err.Error()))
			}
		},
	}
}

func (s *DiameterServer) logErrorReports(ctx context.Context) {
	reports := s.stateMachine.ErrorReports()
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-reports:
			if r == nil || r.Error == nil {
				continue
			}
			attrs := []any{logger.String("err", r.Error.Error())}
			if r.Message != nil {
				attrs = append(attrs,
					logger.String("command_code", strconv.FormatUint(uint64(r.Message.Header.CommandCode), 10)),
					logger.String("application_id", strconv.FormatUint(uint64(r.Message.Header.ApplicationID), 10)))
			}
			logger.Warn("Diameter state machine error", attrs...)
		}
	}
}

// serveTLS starts a Diameter TLS listener per RFC 6733 §13.1.
// SCTP+TLS is not standard; this path forces network=tcp regardless of the
// configured Network value, matching go-diameter's TLS surface area.
func (s *DiameterServer) serveTLS() error {
	cfg := s.cfg.Server.TLS
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return fmt.Errorf("diameter TLS enabled but cert_file / key_file empty")
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Prefer modern AEAD ciphers per OWASP TLS guidance; covered by Go's
		// default cipher suite ordering for TLS 1.2+ but pinned for clarity.
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	}

	if cfg.ClientCA != "" {
		caBytes, err := os.ReadFile(cfg.ClientCA)
		if err != nil {
			return fmt.Errorf("read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return fmt.Errorf("client_ca file contains no PEM certificates: %s", cfg.ClientCA)
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert // mTLS mode
		logger.Info("Diameter TLS mTLS enforced", logger.String("client_ca", cfg.ClientCA))
	}

	srv := &diam.Server{
		Network:   "tcp", // RFC 6733 §13.1 — TLS over TCP only
		Addr:      s.cfg.Server.ListenAddr,
		Handler:   s.gate(),
		TLSConfig: tlsCfg,
	}
	return srv.ListenAndServeTLS(cfg.CertFile, cfg.KeyFile)
}

// authorizeCER checks a Capabilities-Exchange-Request against the cfg.Peers
// whitelist (source IP + Origin-Host). peerGate calls it before the state
// machine sees the CER: on rejection the connection is closed and no CEA is
// sent; on success it returns the Origin-Host the peer is registered under.
func (s *DiameterServer) authorizeCER(c diam.Conn, m *diam.Message) (string, bool) {
	originHostAVP, err := m.FindAVP(avp.OriginHost, 0)
	if err != nil {
		logger.Warn("CER missing Origin-Host (RFC 6733 §5.3 violation)")
		c.Close()
		return "", false
	}
	originHost, ok := originHostAVP.Data.(datatype.DiameterIdentity)
	if !ok {
		logger.Warn("CER Origin-Host AVP has unexpected type")
		c.Close()
		return "", false
	}

	sourceAddr := ""
	if c.RemoteAddr() != nil {
		sourceAddr = c.RemoteAddr().String()
	}

	peerCfg, authErr := AuthorizePeer(s.activeConfig(), sourceAddr, string(originHost))
	if authErr != nil {
		// Whitelist violation. Audit (security event), alarm,
		// metric — then drop the connection. We deliberately do NOT respond
		// with a CEA: revealing acceptance criteria to a rogue probe is worse
		// than a silent drop. RFC 6733 §13 permits implementation discretion.
		audit.Log(context.Background(), "diameter-server", "BUS_PEER_UNAUTHORIZED",
			"diameter-peer", string(originHost),
			map[string]any{
				"source_addr": sourceAddr,
				"origin_host": string(originHost),
				"reason":      authErr.Error(),
			})
		alarm.Raise(alarm.SeverityWarning, "DIAM_GW_PEER_UNAUTHORIZED",
			"unauthorized CER from "+sourceAddr+" claiming "+string(originHost))
		monitor.IncrementCounter("diameter_peer_unauthorized_total", map[string]string{
			"origin_host": string(originHost),
		})
		logger.Warn("Rejecting CER (peer not whitelisted)",
			logger.String("origin_host", string(originHost)),
			logger.String("source_addr", sourceAddr),
			logger.String("err", authErr.Error()))
		c.Close()
		return "", false
	}

	logger.Info("Peer authorized via CER",
		logger.String("origin_host", string(originHost)),
		logger.String("source_addr", sourceAddr),
		logger.String("matched_peer", peerCfg.Name))
	return string(originHost), true
}

// handleDPR processes peer-initiated Disconnect-Peer-Request (RFC 6733 §5.4.3).
//
// Order matters:
//  1. Emit DPA on the same connection so the peer sees its DPR completed
//     before we tear anything down. go-diameter's sm package does NOT
//     auto-reply to DPR (only CER and DWR are auto-handled there).
//  2. Unregister the peer so reverse traffic stops routing there.
//  3. Let the peer close the socket per RFC §5.4.3 (or close it ourselves
//     if it lingers — out of scope for this hook).
//
// If DPA emission fails we still unregister: the peer is going away
// regardless and keeping it in the routing table risks 3002s on reverse path.
//
// The peer is the one the connection registered at CER, not the DPR's
// Origin-Host: a peer must not be able to disconnect another.
func (s *DiameterServer) handleDPR(c diam.Conn, m *diam.Message) {
	if s.peerManager == nil {
		return
	}
	originHost := s.peerManager.HostOf(c)
	if originHost == "" {
		return
	}
	monitor.IncrementCounter("diameter_dpr_received_total", map[string]string{
		"peer": originHost,
	})

	dpa := s.translator.CreateDPA(m,
		s.cfg.Server.OriginHost,
		s.cfg.Server.OriginRealm)
	if _, err := dpa.WriteTo(c); err != nil {
		// Counted separately from successful DPA: alerting can fire on a
		// non-zero rate of failed answers.
		monitor.IncrementCounter("diameter_dpa_write_failures_total", map[string]string{
			"peer": originHost,
		})
		logger.Warn("DPA write failed — proceeding with unregister",
			logger.String("origin_host", originHost),
			logger.String("err", err.Error()))
	} else {
		monitor.IncrementCounter("diameter_dpa_sent_total", map[string]string{
			"peer": originHost,
		})
	}

	logger.Info("Peer initiated DPR — DPA sent, unregistering",
		logger.String("origin_host", originHost))
	s.peerManager.Unregister(originHost)
}

// handleDWA observes Device-Watchdog-Answer messages and feeds the watchdog
// state machine in PeerManager. The SM already replies to
// inbound DWR autonomously; this hook only exists to track liveness for
// outbound (gateway-initiated) probes. The DWA counts for the peer the
// connection registered at CER, whatever its Origin-Host says.
func (s *DiameterServer) handleDWA(c diam.Conn, m *diam.Message) {
	if s.peerManager == nil {
		return
	}
	if originHost := s.peerManager.HostOf(c); originHost != "" {
		s.peerManager.RecordDWA(originHost)
	}
}

// handleDPA correlates a Disconnect-Peer-Answer to a peer that we sent DPR
// to during DrainAll. Forwarding to PeerManager.RecordDPA wakes the
// per-peer ack-wait goroutine so DrainAll can return as soon as all peers
// have acknowledged, instead of always paying the full timeout.
//
// A late-arriving DPA (after DrainAll already force-closed the peer) is
// dropped silently: the connection no longer maps to a registered peer. As
// for DWA, the peer is the connection's, not the DPA's Origin-Host.
func (s *DiameterServer) handleDPA(c diam.Conn, m *diam.Message) {
	if s.peerManager == nil {
		return
	}
	if originHost := s.peerManager.HostOf(c); originHost != "" {
		s.peerManager.RecordDPA(originHost)
	}
}

// admitGate wraps a forward-request handler with the per-peer token-bucket
// rate limit. When the limiter denies a request, the gate
// emits 3004 (TOO_BUSY) immediately without invoking the wrapped handler
// — protecting the NATS/backend hop from a runaway peer.
//
// A request without a parsable Origin-Host bypasses the gate (lets the
// downstream handler reject it as malformed). This avoids spurious
// rate-limit denials for protocol-violating peers; the existing
// peer-whitelist already drops those at CER time.
func (s *DiameterServer) admitGate(handler diam.HandlerFunc) diam.HandlerFunc {
	return func(c diam.Conn, m *diam.Message) {
		ohAVP, err := m.FindAVP(avp.OriginHost, 0)
		if err == nil {
			if oh, ok := ohAVP.Data.(datatype.DiameterIdentity); ok {
				if !s.rateLimiter.Admit(string(oh)) {
					s.replyTooBusy(c, m)
					return
				}
			}
		}
		handler(c, m)
	}
}

// replyTooBusy synthesises a Result-Code 3004 (TOO_BUSY) answer for the
// supplied request and writes it on the same connection. Session-Id is
// copied from the request when present (RFC 6733 §8.8 — answers MUST
// carry the request's Session-Id when applicable).
func (s *DiameterServer) replyTooBusy(c diam.Conn, m *diam.Message) {
	answer := CompleteAnswer(m.Answer(diam.TooBusy), m, s.cfg.Server.OriginHost, s.cfg.Server.OriginRealm)
	if _, err := answer.WriteTo(c); err != nil {
		logger.Warn("3004 (TOO_BUSY) write failed",
			logger.String("err", err.Error()))
	}
}
