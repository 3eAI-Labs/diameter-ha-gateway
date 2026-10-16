// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package nats

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/natstest"
	"github.com/nats-io/nats.go"
	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Circuit Breaker Configuration Tests
// =============================================================================

func TestNewCircuitBreaker_DefaultConfig(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-cb",
		MaxRequests:  5,
		Interval:     "10s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  10,
	}

	cb := newCircuitBreaker(cfg)

	require.NotNil(t, cb)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

func TestNewCircuitBreaker_InvalidDurations(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-cb",
		MaxRequests:  5,
		Interval:     "invalid",  // Invalid duration
		Timeout:      "also bad", // Invalid duration
		FailureRatio: 0.5,
		MinRequests:  10,
	}

	cb := newCircuitBreaker(cfg)

	require.NotNil(t, cb, "Should create CB even with invalid durations using defaults")
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

func TestNewCircuitBreaker_EmptyDurations(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-cb",
		MaxRequests:  5,
		Interval:     "", // Empty, should use default
		Timeout:      "", // Empty, should use default
		FailureRatio: 0.5,
		MinRequests:  10,
	}

	cb := newCircuitBreaker(cfg)

	require.NotNil(t, cb)
	// Should use default values (10s interval, 30s timeout)
}

// =============================================================================
// Circuit Breaker State Tests
// =============================================================================

func TestCircuitBreakerState_InitialClosed(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-cb",
		MaxRequests:  2,
		Interval:     "10s",
		Timeout:      "1s", // Short timeout for testing
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	cb := newCircuitBreaker(cfg)

	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

func TestCircuitBreaker_TripsOnFailureRatio(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-trip",
		MaxRequests:  2,
		Interval:     "60s", // Long interval to not reset counts
		Timeout:      "1s",
		FailureRatio: 0.5, // Trip at 50% failures
		MinRequests:  4,   // Need at least 4 requests
	}

	cb := newCircuitBreaker(cfg)

	// Execute 4 requests: 2 success, 2 failures (50% failure rate)
	// First 2 successful
	cb.Execute(func() (interface{}, error) { return nil, nil })
	cb.Execute(func() (interface{}, error) { return nil, nil })

	// Next 2 fail - this should trip the circuit
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })

	assert.Equal(t, gobreaker.StateOpen, cb.State(), "Circuit should be open after 50% failure rate")
}

func TestCircuitBreaker_DoesNotTripBelowMinRequests(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-min",
		MaxRequests:  2,
		Interval:     "60s",
		Timeout:      "1s",
		FailureRatio: 0.5,
		MinRequests:  10, // Need 10 requests before tripping
	}

	cb := newCircuitBreaker(cfg)

	// Execute fewer than MinRequests, all failures
	for i := 0; i < 5; i++ {
		cb.Execute(func() (interface{}, error) { return nil, assert.AnError })
	}

	assert.Equal(t, gobreaker.StateClosed, cb.State(), "Circuit should remain closed below MinRequests")
}

func TestCircuitBreaker_RejectsInOpenState(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-reject",
		MaxRequests:  1,
		Interval:     "60s",
		Timeout:      "60s", // Long timeout to stay open
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	cb := newCircuitBreaker(cfg)

	// Trip the circuit
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })

	require.Equal(t, gobreaker.StateOpen, cb.State())

	// Next request should be rejected
	_, err := cb.Execute(func() (interface{}, error) { return "should not run", nil })

	assert.ErrorIs(t, err, gobreaker.ErrOpenState)
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-halfopen",
		MaxRequests:  2,
		Interval:     "60s",
		Timeout:      "100ms", // Very short timeout
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	cb := newCircuitBreaker(cfg)

	// Trip the circuit
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })

	require.Equal(t, gobreaker.StateOpen, cb.State())

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Circuit should now be half-open
	assert.Equal(t, gobreaker.StateHalfOpen, cb.State())
}

func TestCircuitBreaker_ClosesAfterSuccessInHalfOpen(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-recover",
		MaxRequests:  1, // Allow 1 request in half-open
		Interval:     "60s",
		Timeout:      "50ms", // Short timeout
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	cb := newCircuitBreaker(cfg)

	// Trip the circuit
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })
	cb.Execute(func() (interface{}, error) { return nil, assert.AnError })

	// Wait for half-open
	time.Sleep(100 * time.Millisecond)

	// Success in half-open should close the circuit
	_, err := cb.Execute(func() (interface{}, error) { return "success", nil })
	assert.NoError(t, err)

	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// =============================================================================
// Error Type Tests
// =============================================================================

func TestCircuitOpenError(t *testing.T) {
	err := &CircuitOpenError{
		Subject: "diameter.s6a.ulr",
		State:   "open",
	}

	assert.Contains(t, err.Error(), "circuit breaker open")
	assert.Contains(t, err.Error(), "diameter.s6a.ulr")
	assert.Contains(t, err.Error(), "open")
}

func TestCircuitHalfOpenError(t *testing.T) {
	err := &CircuitHalfOpenError{
		Subject: "diameter.s6a.ulr",
	}

	assert.Contains(t, err.Error(), "half-open")
	assert.Contains(t, err.Error(), "diameter.s6a.ulr")
}

func TestIsCircuitOpen(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"CircuitOpenError", &CircuitOpenError{Subject: "test"}, true},
		{"CircuitHalfOpenError", &CircuitHalfOpenError{Subject: "test"}, false},
		{"GenericError", assert.AnError, false},
		{"NilError", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsCircuitOpen(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestIsCircuitHalfOpen(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"CircuitHalfOpenError", &CircuitHalfOpenError{Subject: "test"}, true},
		{"CircuitOpenError", &CircuitOpenError{Subject: "test"}, false},
		{"GenericError", assert.AnError, false},
		{"NilError", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsCircuitHalfOpen(tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// =============================================================================
// WrapCircuitBreakerError Tests
// =============================================================================

func TestWrapCircuitBreakerError_OpenState(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-wrap",
		MaxRequests:  1,
		Interval:     "10s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	client := &Client{
		cb:  newCircuitBreaker(cfg),
		cfg: config.NatsConfig{CircuitBreaker: cfg},
	}

	wrapped := client.wrapCircuitBreakerError(gobreaker.ErrOpenState, "test.topic")

	require.NotNil(t, wrapped)
	assert.True(t, IsCircuitOpen(wrapped))

	openErr, ok := wrapped.(*CircuitOpenError)
	require.True(t, ok)
	assert.Equal(t, "test.topic", openErr.Subject)
}

func TestWrapCircuitBreakerError_TooManyRequests(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-wrap",
		MaxRequests:  1,
		Interval:     "10s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	client := &Client{
		cb:  newCircuitBreaker(cfg),
		cfg: config.NatsConfig{CircuitBreaker: cfg},
	}

	wrapped := client.wrapCircuitBreakerError(gobreaker.ErrTooManyRequests, "test.topic")

	require.NotNil(t, wrapped)
	assert.True(t, IsCircuitHalfOpen(wrapped))

	halfOpenErr, ok := wrapped.(*CircuitHalfOpenError)
	require.True(t, ok)
	assert.Equal(t, "test.topic", halfOpenErr.Subject)
}

func TestWrapCircuitBreakerError_OtherError(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-wrap",
		MaxRequests:  1,
		Interval:     "10s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	client := &Client{
		cb:  newCircuitBreaker(cfg),
		cfg: config.NatsConfig{CircuitBreaker: cfg},
	}

	originalErr := assert.AnError
	wrapped := client.wrapCircuitBreakerError(originalErr, "test.topic")

	assert.Equal(t, originalErr, wrapped, "Non-CB errors should pass through unchanged")
}

// =============================================================================
// Circuit Breaker Counts Tests
// =============================================================================

func TestCircuitBreakerCounts(t *testing.T) {
	cfg := config.CircuitBreakerConfig{
		Name:         "test-counts",
		MaxRequests:  5,
		Interval:     "60s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  10,
	}

	client := &Client{
		cb:  newCircuitBreaker(cfg),
		cfg: config.NatsConfig{CircuitBreaker: cfg},
	}

	// Execute some requests
	for i := 0; i < 3; i++ {
		client.cb.Execute(func() (interface{}, error) { return nil, nil })
	}
	for i := 0; i < 2; i++ {
		client.cb.Execute(func() (interface{}, error) { return nil, assert.AnError })
	}

	counts := client.CircuitBreakerCounts()

	assert.Equal(t, uint32(5), counts.Requests)
	assert.Equal(t, uint32(3), counts.TotalSuccesses)
	assert.Equal(t, uint32(2), counts.TotalFailures)
}

// =============================================================================
// JSON Marshal Tests for Events
// =============================================================================

func TestJSONMarshal_CircuitBreakerEvent(t *testing.T) {
	// This tests that events can be properly marshaled for NATS
	event := struct {
		Type    string `json:"type"`
		Subject string `json:"subject"`
		State   string `json:"state"`
	}{
		Type:    "circuit_breaker_event",
		Subject: "diameter.s6a.ulr",
		State:   "open",
	}

	data, err := json.Marshal(event)

	require.NoError(t, err)
	assert.Contains(t, string(data), "circuit_breaker_event")
	assert.Contains(t, string(data), "diameter.s6a.ulr")
}

// =============================================================================
// Circuit Breaker Benchmarks
// =============================================================================

func BenchmarkCircuitBreaker_Execute_Success(b *testing.B) {
	cfg := config.CircuitBreakerConfig{
		Name:         "bench-cb",
		MaxRequests:  5,
		Interval:     "60s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  100,
	}
	cb := newCircuitBreaker(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cb.Execute(func() (interface{}, error) { return nil, nil })
	}
}

func BenchmarkCircuitBreaker_StateCheck(b *testing.B) {
	cfg := config.CircuitBreakerConfig{
		Name:         "bench-state",
		MaxRequests:  5,
		Interval:     "60s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  100,
	}
	cb := newCircuitBreaker(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.State()
	}
}

func BenchmarkCircuitBreaker_Counts(b *testing.B) {
	cfg := config.CircuitBreakerConfig{
		Name:         "bench-counts",
		MaxRequests:  5,
		Interval:     "60s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  100,
	}
	cb := newCircuitBreaker(cfg)

	// Execute some requests first
	for i := 0; i < 50; i++ {
		cb.Execute(func() (interface{}, error) { return nil, nil })
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.Counts()
	}
}

func BenchmarkCircuitBreaker_ConcurrentExecute(b *testing.B) {
	cfg := config.CircuitBreakerConfig{
		Name:         "bench-concurrent",
		MaxRequests:  5,
		Interval:     "60s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  1000,
	}
	cb := newCircuitBreaker(cfg)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cb.Execute(func() (interface{}, error) { return nil, nil })
		}
	})
}

func BenchmarkWrapCircuitBreakerError(b *testing.B) {
	cfg := config.CircuitBreakerConfig{
		Name:         "bench-wrap",
		MaxRequests:  1,
		Interval:     "10s",
		Timeout:      "30s",
		FailureRatio: 0.5,
		MinRequests:  2,
	}

	client := &Client{
		cb:  newCircuitBreaker(cfg),
		cfg: config.NatsConfig{CircuitBreaker: cfg},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.wrapCircuitBreakerError(assert.AnError, "test.topic")
	}
}

// =============================================================================
// Request/reply over a real server
// =============================================================================

// TestQueueSubscribeRespond_CoreRequestReply pins the transport: a requester's
// core NATS request reaches a queue subscriber and receives the reply bytes
// unchanged — with JetStream off, and with JetStream on but no stream on the
// subject. A JetStream subscription failed the first ("no stream matches
// subject"), and replying through a JetStream publish delivered the reply only
// after a 5 s acknowledgement timeout that also counted as a breaker failure.
func TestQueueSubscribeRespond_CoreRequestReply(t *testing.T) {
	for _, js := range []bool{false, true} {
		t.Run(map[bool]string{false: "jetstream off", true: "jetstream on"}[js], func(t *testing.T) {
			c, err := NewClient(config.NatsConfig{URL: natstest.Start(t, js)})
			require.NoError(t, err)
			defer c.Close()

			reply := []byte(`{"result_code":2001}`)
			_, err = c.QueueSubscribe("diameter.s6a.clr", "diameter-gw", func(m *nats.Msg) {
				assert.NoError(t, c.Respond(m, reply))
			})
			require.NoError(t, err)

			start := time.Now()
			got, err := c.Conn.Request("diameter.s6a.clr", []byte(`{}`), 2*time.Second)
			require.NoError(t, err)
			assert.Equal(t, reply, got.Data, "reply must arrive as sent, not re-encoded")
			assert.Less(t, time.Since(start), time.Second)
			assert.Equal(t, uint32(0), c.CircuitBreakerCounts().TotalFailures)
		})
	}
}

func TestQueueSubscribe_OneDeliveryPerGroup(t *testing.T) {
	url := natstest.Start(t, false)
	var delivered atomic.Int32
	for i := 0; i < 3; i++ {
		c, err := NewClient(config.NatsConfig{URL: url})
		require.NoError(t, err)
		defer c.Close()
		_, err = c.QueueSubscribe("diameter.sgd.srr.request", "diameter-gw", func(*nats.Msg) {
			delivered.Add(1)
		})
		require.NoError(t, err)
		require.NoError(t, c.Conn.Flush())
	}

	pub, err := nats.Connect(url)
	require.NoError(t, err)
	defer pub.Close()
	require.NoError(t, pub.Publish("diameter.sgd.srr.request", []byte(`{}`)))
	require.NoError(t, pub.Flush())

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), delivered.Load())
}

func TestRespond_NoReplySubjectIsNoop(t *testing.T) {
	c := &Client{} // no connection: Respond must not touch it
	assert.NoError(t, c.Respond(&nats.Msg{Subject: "x"}, []byte(`{}`)))
}

func TestTLSOptions(t *testing.T) {
	opts, err := tlsOptions(config.TLSConfig{})
	require.NoError(t, err)
	assert.Empty(t, opts, "TLS disabled adds no option")

	opts, err = tlsOptions(config.TLSConfig{Enabled: true})
	require.NoError(t, err)
	assert.Len(t, opts, 1, "TLS enabled requires a secure connection")

	opts, err = tlsOptions(config.TLSConfig{Enabled: true, CACert: "ca.pem", CertFile: "c.pem", KeyFile: "k.pem"})
	require.NoError(t, err)
	assert.Len(t, opts, 3)

	_, err = tlsOptions(config.TLSConfig{Enabled: true, CertFile: "c.pem"})
	assert.Error(t, err, "a certificate without its key is a configuration error")
}

// TestNewClient_TLSEnabledRefusesPlaintextServer shows that nats.tls.enabled
// is honoured: before, the setting was never read and the connection silently
// stayed in plaintext.
func TestNewClient_TLSEnabledRefusesPlaintextServer(t *testing.T) {
	_, err := NewClient(config.NatsConfig{URL: natstest.Start(t, false), TLS: config.TLSConfig{Enabled: true}})
	assert.Error(t, err)
}
