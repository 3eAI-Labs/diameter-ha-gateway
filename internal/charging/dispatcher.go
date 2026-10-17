// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
	"strings"

	pb "github.com/3eAI-Labs/diameter-ha-gateway-client"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
)

// CCRHandler is the per-interface (Gx / Gy / Ro) handler contract. Each
// concrete implementation lives in its own subpackage and translates a single
// command (CCR) for that interface only.
type CCRHandler interface {
	HandleCCR(c diam.Conn, m *diam.Message)
}

// Dispatcher routes incoming CCR messages to the right interface handler.
// Gx, Gy and Ro share Command-Code 272, and go-diameter dispatches by
// command. A Gx CCR is recognised by its Application-Id (16777238, TS 29.212
// §5.6); TS 29.212 gives it no Service-Context-Id. Gy and Ro share
// Application-Id 4 (RFC 8506), and their mandatory Service-Context-Id
// (TS 32.299 §7.1.12) tells them apart.
type Dispatcher struct {
	gx          CCRHandler
	gy          CCRHandler
	ro          CCRHandler
	gyContexts  map[string]struct{}
	roContexts  map[string]struct{}
	defaultDest CCRHandler // optional; nil → reject 5012 if no match
	originHost  string     // identity on the gateway's own rejections
	originRealm string
}

// WithIdentity sets the Origin-Host and Origin-Realm the dispatcher's own
// rejections carry.
func (d *Dispatcher) WithIdentity(originHost, originRealm string) *Dispatcher {
	d.originHost, d.originRealm = originHost, originRealm
	return d
}

// NewDispatcher wires the per-interface handlers and the Gy and Ro
// Service-Context-Id lists from configuration. defaultDest is used for an
// application-4 CCR that neither list matches; pass nil to reject it with
// 5012 (UNABLE_TO_COMPLY).
func NewDispatcher(gx, gy, ro CCRHandler, gyIDs, roIDs []string, defaultDest CCRHandler) *Dispatcher {
	return &Dispatcher{
		gx:          gx,
		gy:          gy,
		ro:          ro,
		gyContexts:  toSet(gyIDs),
		roContexts:  toSet(roIDs),
		defaultDest: defaultDest,
	}
}

func toSet(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

// HandleCCR satisfies the diameter.ChargingHandler contract.
func (d *Dispatcher) HandleCCR(c diam.Conn, m *diam.Message) {
	if missing := diameter.ValidateMandatoryAVPs(m); len(missing) > 0 {
		logger.Warn("CCR missing mandatory AVPs; answering 5005", logger.Int("missing", len(missing)))
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "missing"})
		d.rejectMissing(c, m, missing)
		return
	}
	if m.Header.ApplicationID == AppIDGx {
		if d.gx == nil {
			monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "unmatched"})
			d.reject(c, m, diam.ApplicationUnsupported)
			return
		}
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "gx"})
		d.gx.HandleCCR(c, m)
		return
	}

	scidAVP, err := m.FindAVP(avp.ServiceContextID, 0)
	if err != nil {
		logger.Warn("CCR missing Service-Context-Id; answering 5005")
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "missing"})
		// Mandatory in a Gy or Ro CCR (RFC 8506 §3.1).
		d.rejectMissing(c, m, []pb.FailedAVPEntry{{Code: avp.ServiceContextID}})
		return
	}
	scid := diameter.AVPString(scidAVP)

	switch {
	case d.gy != nil && matchesContext(d.gyContexts, scid):
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "gy"})
		d.gy.HandleCCR(c, m)
	case d.ro != nil && matchesContext(d.roContexts, scid):
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "ro"})
		d.ro.HandleCCR(c, m)
	case d.defaultDest != nil:
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "default"})
		d.defaultDest.HandleCCR(c, m)
	default:
		logger.Warn("CCR Service-Context-Id matched no configured interface",
			logger.String("service_context_id", scid))
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "unmatched"})
		d.reject(c, m, diam.UnableToComply)
	}
}

// matchesContext reports whether scid is in set, either as it is or by its
// "service-context"@"domain" part. TS 32.299 §7.1.12 gives a
// Service-Context-Id the form
// "extensions".MNC.MCC."Release"."service-context"@"domain", of which only
// "Release"."service-context"@"domain" is required, so 17.32251@3gpp.org and
// 001.01.17.32251@3gpp.org both match a configured 32251@3gpp.org.
func matchesContext(set map[string]struct{}, scid string) bool {
	if _, ok := set[scid]; ok {
		return true
	}
	at := strings.LastIndexByte(scid, '@')
	if at < 0 {
		return false
	}
	dot := strings.LastIndexByte(scid[:at], '.')
	if dot < 0 {
		return false
	}
	_, ok := set[scid[dot+1:]]
	return ok
}

func (d *Dispatcher) reject(c diam.Conn, m *diam.Message, resultCode uint32) {
	// Stateless: no NATS publish.
	ans := diameter.CompleteAnswer(m.Answer(resultCode), m, d.originHost, d.originRealm)
	if _, err := ans.WriteTo(c); err != nil {
		logger.Error("Failed to send dispatcher rejection answer", err)
	}
}

// rejectMissing answers 5005 (DIAMETER_MISSING_AVP) with a Failed-AVP
// naming each missing AVP (RFC 6733 §7.5).
func (d *Dispatcher) rejectMissing(c diam.Conn, m *diam.Message, missing []pb.FailedAVPEntry) {
	ans := diameter.BuildMissingAVPAnswer(m, d.originHost, d.originRealm, missing)
	ans = diameter.CompleteAnswer(ans, m, d.originHost, d.originRealm)
	if _, err := ans.WriteTo(c); err != nil {
		logger.Error("Failed to send dispatcher rejection answer", err)
	}
}
