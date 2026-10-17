// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"fmt"
	"runtime"
	"strconv"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/fiorix/go-diameter/v4/diam"
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
	answer    func(c diam.Conn, m *diam.Message, resultCode uint32)
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
	g.next.ServeDIAM(c, m)
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
