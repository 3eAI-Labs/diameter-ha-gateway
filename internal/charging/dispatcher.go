// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package charging

import (
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

// Dispatcher routes incoming CCR messages to the right interface handler
// by inspecting the Service-Context-Id AVP. Necessary because Gx, Gy and Ro
// share Command-Code 272 (Gx under Application-Id 16777238, Gy and Ro under
// 4) and go-diameter dispatches by command — the gateway can host all three
// concurrently and only this AVP discriminates the semantics.
type Dispatcher struct {
	gx          CCRHandler
	gy          CCRHandler
	ro          CCRHandler
	gxContexts  map[string]struct{}
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

// NewDispatcher wires the per-interface handlers and the Service-Context-Id
// allow-lists from configuration. defaultDest is used when none of the lists
// match; pass nil to reject unknown contexts with 5012 (UNABLE_TO_COMPLY).
func NewDispatcher(gx, gy, ro CCRHandler, gxIDs, gyIDs, roIDs []string, defaultDest CCRHandler) *Dispatcher {
	return &Dispatcher{
		gx:          gx,
		gy:          gy,
		ro:          ro,
		gxContexts:  toSet(gxIDs),
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
	scidAVP, err := m.FindAVP(avp.ServiceContextID, 0)
	if err != nil {
		logger.Warn("CCR missing Service-Context-Id; cannot dispatch")
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "missing"})
		d.reject(c, m)
		return
	}
	scid := diameter.AVPString(scidAVP)

	switch {
	case d.gx != nil && d.contains(d.gxContexts, scid):
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "gx"})
		d.gx.HandleCCR(c, m)
	case d.gy != nil && d.contains(d.gyContexts, scid):
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "gy"})
		d.gy.HandleCCR(c, m)
	case d.ro != nil && d.contains(d.roContexts, scid):
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "ro"})
		d.ro.HandleCCR(c, m)
	case d.defaultDest != nil:
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "default"})
		d.defaultDest.HandleCCR(c, m)
	default:
		logger.Warn("CCR Service-Context-Id matched no configured interface",
			logger.String("service_context_id", scid))
		monitor.IncrementCounter("diameter_charging_dispatch_total", map[string]string{"interface": "unmatched"})
		d.reject(c, m)
	}
}

func (d *Dispatcher) contains(set map[string]struct{}, scid string) bool {
	_, ok := set[scid]
	return ok
}

func (d *Dispatcher) reject(c diam.Conn, m *diam.Message) {
	// 5012 DIAMETER_UNABLE_TO_COMPLY per BUSINESS_EXCEPTION_CODES (gateway can
	// not route the request to any backend). Stateless: no NATS publish.
	ans := diameter.CompleteAnswer(m.Answer(uint32(diam.UnableToComply)), m, d.originHost, d.originRealm)
	if _, err := ans.WriteTo(c); err != nil {
		logger.Error("Failed to send dispatcher rejection answer", err)
	}
}
