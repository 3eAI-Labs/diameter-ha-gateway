// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package sgd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
)

// Subjects the gateway publishes requests from the network on. S6c commands
// share the diameter.sgd prefix with SGd.
const (
	TopicSGdOFR = "diameter.sgd.ofr" // MO-Forward-Short-Message, MME/SGSN → SMS-IWMSC
	TopicSGdTFR = "diameter.sgd.tfr" // MT-Forward-Short-Message, SMS-GMSC → MME/SGSN
	TopicSGdSRR = "diameter.sgd.srr" // Send-Routing-Info-for-SM, SMS-GMSC → HSS
	TopicSGdALR = "diameter.sgd.alr" // Alert-Service-Centre, HSS → SMS-IWMSC, MME/SGSN → SMS-GMSC
	TopicSGdRDR = "diameter.sgd.rdr" // Report-SM-Delivery-Status, SMS-GMSC → HSS
)

// Handler serves S6c and SGd requests from the network: it publishes each as
// a NATS request and answers with the application's reply.
type Handler struct {
	natsClient  nats.Requester
	peerManager *diameter.PeerManager
	timeout     time.Duration
}

func NewHandler(nc nats.Requester, pm *diameter.PeerManager, timeout time.Duration) *Handler {
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	return &Handler{natsClient: nc, peerManager: pm, timeout: timeout}
}

func (h *Handler) HandleOFR(c diam.Conn, m *diam.Message) {
	serve(h, c, m, "OFR", TopicSGdOFR, decodeOFR, encodeOFA)
}

func (h *Handler) HandleTFR(c diam.Conn, m *diam.Message) {
	serve(h, c, m, "TFR", TopicSGdTFR, decodeTFR, encodeTFA)
}

func (h *Handler) HandleSRR(c diam.Conn, m *diam.Message) {
	serve(h, c, m, "SRR", TopicSGdSRR, decodeSRR, encodeSRA)
}

func (h *Handler) HandleALR(c diam.Conn, m *diam.Message) {
	serve(h, c, m, "ALR", TopicSGdALR, decodeALR, encodeALA)
}

func (h *Handler) HandleRDR(c diam.Conn, m *diam.Message) {
	serve(h, c, m, "RDR", TopicSGdRDR, decodeRDR, encodeRDA)
}

// serve answers m:
//   - 5005 with a Failed-AVP when a mandatory AVP of the command is missing;
//   - 3002 when no application answers on subject in time;
//   - 5012 when the application's reply is not valid JSON;
//   - otherwise with the application's reply.
func serve[E, A any](h *Handler, c diam.Conn, m *diam.Message, cmd, subject string,
	decode func(*diam.Message, string) *E, encode func(*diam.Message, *A) *diam.Message) {
	traceID := fmt.Sprintf("sgd-%s-%d-%d", strings.ToLower(cmd), m.Header.HopByHopID, m.Header.EndToEndID)
	monitor.IncrementCounter("diameter_sgd_requests_total", map[string]string{"type": cmd})
	h.peerManager.Touch(c)

	if missing := missingAVPs(m, cmd); len(missing) > 0 {
		host, realm := h.peerManager.Identity()
		h.send(c, diameter.BuildMissingAVPAnswer(m, host, realm, missing), m, cmd, traceID)
		return
	}

	data, err := h.natsClient.Request(subject, decode(m, traceID), h.timeout)
	if err != nil {
		logger.Warn("SGd: no application reply",
			logger.String("cmd", cmd), logger.String("trace_id", traceID), logger.String("err", err.Error()))
		if nats.IsCircuitOpen(err) {
			monitor.IncrementCounter("diameter_sgd_circuit_breaker_open", nil)
		}
		h.send(c, m.Answer(diam.UnableToDeliver), m, cmd, traceID)
		return
	}
	var reply A
	if err := json.Unmarshal(data, &reply); err != nil {
		logger.Warn("SGd: application reply is not valid JSON",
			logger.String("cmd", cmd), logger.String("trace_id", traceID))
		h.send(c, m.Answer(diam.UnableToComply), m, cmd, traceID)
		return
	}
	h.send(c, encode(m, &reply), m, cmd, traceID)
}

func (h *Handler) send(c diam.Conn, ans, req *diam.Message, cmd, traceID string) {
	if err := diameter.SendAnswer(c, ans, req, h.peerManager); err != nil {
		logger.Error("SGd answer write failed", err, logger.String("cmd", cmd), logger.String("trace_id", traceID))
	}
}

// required is what every S6c and SGd request carries ({…} in each Command
// Code Format); commands adds each command's own.
var required = []string{"Session-Id", "Auth-Session-State", "Origin-Host", "Origin-Realm", "Destination-Realm"}

// mandatoryAVP locates an AVP by name, with the minimum length of its value
// for the zero-valued example a Failed-AVP carries (RFC 6733 §7.5).
var mandatoryAVP = map[string]struct {
	code, vendor uint32
	size         int
}{
	"Session-Id":          {263, 0, 0},
	"Auth-Session-State":  {277, 0, 4},
	"Origin-Host":         {264, 0, 0},
	"Origin-Realm":        {296, 0, 0},
	"Destination-Realm":   {283, 0, 0},
	"Destination-Host":    {avpDestinationHost, 0, 0},
	"User-Name":           {avpUserName, 0, 0},
	"SC-Address":          {avpSCAddress, VendorID3GPP, 0},
	"User-Identifier":     {avpUserIdentifier, VendorID3GPP, 0},
	"SM-RP-UI":            {avpSMRPUI, VendorID3GPP, 0},
	"SM-Delivery-Outcome": {avpSMDeliveryOutcome, VendorID3GPP, 0},
}

var commandByName = func() map[string]command {
	out := make(map[string]command, len(commands))
	for _, c := range commands {
		out[strings.ToUpper(c.short)+"R"] = c
	}
	return out
}()

func missingAVPs(m *diam.Message, cmd string) []pb.FailedAVPEntry {
	var missing []pb.FailedAVPEntry
	for _, name := range append(append([]string{}, required...), commandByName[cmd].required...) {
		want := mandatoryAVP[name]
		if avpList(m.AVP).find(want.code, want.vendor) == nil {
			missing = append(missing, pb.FailedAVPEntry{
				Code: want.code, VendorID: want.vendor, Value: strings.Repeat("00", want.size),
			})
		}
	}
	return missing
}
