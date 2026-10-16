// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package nats

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/alarm"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/nats-io/nats.go"
	"github.com/sony/gobreaker"
)

// Requester defines the interface for making NATS requests
// This interface enables dependency injection for testing
type Requester interface {
	Request(topic string, msg interface{}, timeout time.Duration) ([]byte, error)
}

// Client wraps NATS connection with circuit breaker protection.
//
// Everything the gateway exchanges is core NATS request/reply. JetStream is
// not used: a JetStream subscription needs a stream on the subject, and with a
// stream in place a requester receives the stream's publish acknowledgement
// instead of the reply, while the reply goes to the acknowledgement subject.
type Client struct {
	Conn *nats.Conn // Exported for direct access in reverse service
	cb   *gobreaker.CircuitBreaker
	cfg  config.NatsConfig
}

// NewClient creates a new NATS client with circuit breaker
func NewClient(cfg config.NatsConfig) (*Client, error) {
	opts := []nats.Option{
		nats.Name("diameter-ha-gateway"),
		nats.Timeout(5 * time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Error("NATS Disconnected", err)
			alarm.Raise(alarm.SeverityCritical, "BUS_NATS_DISCONNECTED", "NATS connection lost")
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("NATS Reconnected", logger.String("url", nc.ConnectedUrl()))
			// Note: alarm.Clear not available in common-go, reconnection logged instead
		}),
	}

	tlsOpts, err := tlsOptions(cfg.TLS)
	if err != nil {
		return nil, err
	}
	opts = append(opts, tlsOpts...)

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, err
	}

	// Initialize circuit breaker
	cb := newCircuitBreaker(cfg.CircuitBreaker)

	return &Client{
		Conn: nc,
		cb:   cb,
		cfg:  cfg,
	}, nil
}

// tlsOptions turns nats.tls into connection options. With TLS enabled the
// connection must be secure; a CA file pins the server's trust root, and a
// certificate/key pair enables mutual TLS.
func tlsOptions(cfg config.TLSConfig) ([]nats.Option, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	opts := []nats.Option{nats.Secure()}
	if cfg.CACert != "" {
		opts = append(opts, nats.RootCAs(cfg.CACert))
	}
	switch {
	case cfg.CertFile != "" && cfg.KeyFile != "":
		opts = append(opts, nats.ClientCert(cfg.CertFile, cfg.KeyFile))
	case cfg.CertFile != "" || cfg.KeyFile != "":
		return nil, errors.New("nats.tls: cert_file and key_file must be set together")
	}
	return opts, nil
}

// newCircuitBreaker creates a gobreaker instance with the given config
func newCircuitBreaker(cfg config.CircuitBreakerConfig) *gobreaker.CircuitBreaker {
	// Parse durations
	interval, _ := time.ParseDuration(cfg.Interval)
	if interval == 0 {
		interval = 10 * time.Second
	}

	timeout, _ := time.ParseDuration(cfg.Timeout)
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	settings := gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: cfg.MaxRequests,
		Interval:    interval,
		Timeout:     timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < cfg.MinRequests {
				return false
			}
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return failureRatio >= cfg.FailureRatio
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Warn("Circuit breaker state changed",
				logger.String("name", name),
				logger.String("from", from.String()),
				logger.String("to", to.String()),
			)

			// Raise alarm when circuit opens
			if to == gobreaker.StateOpen {
				alarm.Raise(alarm.SeverityWarning, "DIAM_GW_CB_OPEN",
					"NATS circuit breaker opened: "+name)
			}

			// Log when circuit recovers (alarm.Clear not available in common-go)
			if to == gobreaker.StateClosed && from != gobreaker.StateClosed {
				logger.Info("Circuit breaker recovered",
					logger.String("name", name),
					logger.String("state", to.String()))
			}
		},
	}

	return gobreaker.NewCircuitBreaker(settings)
}

// Respond sends data, already encoded, as the reply to req. It is a no-op
// when the request carries no reply subject. Replies bypass the circuit
// breaker: the breaker guards calls to the backend, and a reply is not one.
func (c *Client) Respond(req *nats.Msg, data []byte) error {
	if req.Reply == "" {
		return nil
	}
	return c.Conn.Publish(req.Reply, data)
}

// Request sends a request and waits for a response with circuit breaker protection
func (c *Client) Request(topic string, msg interface{}, timeout time.Duration) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	result, cbErr := c.cb.Execute(func() (interface{}, error) {
		resp, err := c.Conn.Request(topic, data, timeout)
		if err != nil {
			return nil, err
		}
		return resp.Data, nil
	})

	if cbErr != nil {
		return nil, c.wrapCircuitBreakerError(cbErr, topic)
	}

	return result.([]byte), nil
}

// QueueSubscribe creates a queue-group subscription. Multiple gateway instances
// joining the same queue group share message delivery (load-balanced), which is
// the load-balancing primitive that makes the per-instance pendingReqs design
// viable: a re-published event can reach a different instance.
func (c *Client) QueueSubscribe(topic, queueGroup string, handler func(*nats.Msg)) (*nats.Subscription, error) {
	return c.Conn.QueueSubscribe(topic, queueGroup, handler)
}

// Close closes the NATS connection
func (c *Client) Close() {
	if c.Conn != nil {
		c.Conn.Close()
	}
}

// CircuitBreakerState returns the current state of the circuit breaker
func (c *Client) CircuitBreakerState() gobreaker.State {
	return c.cb.State()
}

// CircuitBreakerCounts returns the current counts of the circuit breaker
func (c *Client) CircuitBreakerCounts() gobreaker.Counts {
	return c.cb.Counts()
}

// wrapCircuitBreakerError converts gobreaker errors to domain-specific errors
func (c *Client) wrapCircuitBreakerError(err error, subject string) error {
	if errors.Is(err, gobreaker.ErrOpenState) {
		return &CircuitOpenError{
			Subject: subject,
			State:   c.cb.State().String(),
		}
	}
	if errors.Is(err, gobreaker.ErrTooManyRequests) {
		return &CircuitHalfOpenError{
			Subject: subject,
		}
	}
	return err
}
