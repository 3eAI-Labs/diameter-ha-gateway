// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/sm/smpeer"
)

// peerGate is the handler the listener serves, in front of the go-diameter
// state machine.
//
// The state machine answers CER itself and does not accept a handler of ours
// for it: sm.HandleFunc("CER", …) only emits an ErrorReport and registers
// nothing (diam/sm/sm.go). The whitelist check therefore runs here, before the
// state machine sees the CER: an unauthorised peer is disconnected without a
// CEA, an authorised one is registered once the state machine has accepted the
// capabilities exchange.
//
// Each message is also handled under recover. go-diameter recovers a panic in
// a handler by closing the connection, which drops every other request on it;
// here the request is answered 5012 (DIAMETER_UNABLE_TO_COMPLY) and the
// connection stays up.
type peerGate struct {
	next      diam.Handler
	authorize func(c diam.Conn, m *diam.Message) (originHost string, ok bool)
	register  func(c diam.Conn, originHost string)
	answer    func(c diam.Conn, m *diam.Message, resultCode uint32, extra ...*diam.AVP)
	// serves reports the applications the gateway handles; a request of
	// another is left to the state machine, which answers it 3001 or 3007.
	// Nil serves every application.
	serves func(appID uint32) bool
}

func (g *peerGate) ServeDIAM(c diam.Conn, m *diam.Message) {
	defer g.recoverPanic(c, m)

	if isCER(m) && !handshakeDone(c) {
		originHost, ok := g.authorize(c, m)
		if !ok {
			return // authorize has closed the connection
		}
		g.next.ServeDIAM(c, m)
		if handshakeDone(c) {
			g.register(c, originHost)
		}
		return
	}
	if m.Header.CommandFlags&diam.RequestFlag != 0 && handshakeDone(c) &&
		(g.serves == nil || g.serves(m.Header.ApplicationID)) {
		if failed := unknownMandatory(m); len(failed) > 0 {
			g.rejectUnknown(c, m, failed)
			return
		}
	}
	g.next.ServeDIAM(c, m)
}

// unknownMandatory returns the request's top-level AVPs that carry the M
// bit and that the dictionary does not know. RFC 6733 §4.1: "The receiving
// entity MUST return an appropriate error message if it receives an AVP
// that has the M-bit set but does not understand it. An exception applies
// when the AVP is embedded within a Grouped AVP."
func unknownMandatory(m *diam.Message) []*diam.AVP {
	var out []*diam.AVP
	for _, a := range m.AVP {
		if _, unknown := a.Data.(datatype.Unknown); unknown && a.Flags&avp.Mbit != 0 {
			out = append(out, a)
		}
	}
	return out
}

// rejectUnknown answers 5001 DIAMETER_AVP_UNSUPPORTED with a Failed-AVP
// holding the offending AVPs (RFC 6733 §7.1.5, §7.5), before any handler
// or application sees the request. It logs their codes, never values.
func (g *peerGate) rejectUnknown(c diam.Conn, m *diam.Message, failed []*diam.AVP) {
	codes := make([]string, 0, len(failed))
	for _, a := range failed {
		codes = append(codes, fmt.Sprintf("%d/%d", a.Code, a.VendorID))
	}
	command := strconv.FormatUint(uint64(m.Header.CommandCode), 10)
	logger.Warn("Request with an unknown mandatory AVP answered 5001",
		logger.String("command_code", command),
		logger.String("application_id", strconv.FormatUint(uint64(m.Header.ApplicationID), 10)),
		logger.String("avps", strings.Join(codes, ",")))
	monitor.IncrementCounter("diameter_avp_unsupported_total", map[string]string{"command_code": command})
	g.answer(c, m, diam.AVPUnsupported,
		diam.NewAVP(avp.FailedAVP, avp.Mbit, 0, &diam.GroupedAVP{AVP: failed}))
}

// Error forwards the listener's read errors to the state machine, which the
// listener would otherwise have reported them to.
func (g *peerGate) Error(err *diam.ErrorReport) {
	if er, ok := g.next.(diam.ErrorReporter); ok {
		er.Error(err)
	}
}

// ErrorReports returns the state machine's error channel.
func (g *peerGate) ErrorReports() <-chan *diam.ErrorReport {
	if er, ok := g.next.(diam.ErrorReporter); ok {
		return er.ErrorReports()
	}
	return nil
}

// recoverPanic logs the command only, never AVP values: the message may carry
// authentication vectors. Of the panic value it logs a runtime error's text
// (an interface conversion names types only) and otherwise only its type: a
// handler's own panic message may quote what it was decoding.
func (g *peerGate) recoverPanic(c diam.Conn, m *diam.Message) {
	r := recover()
	if r == nil {
		return
	}
	command := strconv.FormatUint(uint64(m.Header.CommandCode), 10)
	request := m.Header.CommandFlags&diam.RequestFlag != 0
	cause := fmt.Errorf("panic of type %T", r)
	if re, ok := r.(runtime.Error); ok {
		cause = re
	}
	logger.Error("Diameter handler panic", cause,
		logger.String("command_code", command),
		logger.String("application_id", strconv.FormatUint(uint64(m.Header.ApplicationID), 10)),
		logger.String("request", strconv.FormatBool(request)))
	monitor.IncrementCounter("diameter_handler_panic_total", map[string]string{"command_code": command})
	if request {
		g.answer(c, m, diam.UnableToComply)
	}
}

func isCER(m *diam.Message) bool {
	return m.Header.CommandCode == diam.CapabilitiesExchange &&
		m.Header.CommandFlags&diam.RequestFlag != 0
}

// handshakeDone reports whether the state machine has accepted a CER on c.
func handshakeDone(c diam.Conn) bool {
	_, ok := smpeer.FromContext(c.Context())
	return ok
}
