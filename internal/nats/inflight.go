// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package nats

import "github.com/nats-io/nats.go"

// MaxInFlight bounds the gateway-initiated requests one service waits on
// at a time. A subscription's callbacks run one at a time, so each request
// waits for its peer in a goroutine of its own; without a bound, a burst of
// NATS requests would start as many goroutines and Diameter requests.
const MaxInFlight = 1024

// Inflight runs handlers concurrently, at most cap(f) at once.
type Inflight chan struct{}

func NewInflight() Inflight { return make(Inflight, MaxInFlight) }

// Wrap returns a subscription callback that runs handle in its own
// goroutine, or calls busy when cap(f) requests are already waiting.
func (f Inflight) Wrap(handle, busy func(*nats.Msg)) func(*nats.Msg) {
	return func(m *nats.Msg) {
		select {
		case f <- struct{}{}:
			go func() {
				defer func() { <-f }()
				handle(m)
			}()
		default:
			busy(m)
		}
	}
}
