// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package nats

import "fmt"

// CircuitOpenError indicates the circuit breaker is in open state
// and rejecting requests to protect the system
type CircuitOpenError struct {
	Subject string
	State   string
}

func (e *CircuitOpenError) Error() string {
	return fmt.Sprintf("circuit breaker open for subject %s (state: %s)", e.Subject, e.State)
}

// IsCircuitOpen returns true if the error is a CircuitOpenError
func IsCircuitOpen(err error) bool {
	_, ok := err.(*CircuitOpenError)
	return ok
}

// CircuitHalfOpenError indicates too many requests are being made
// while the circuit breaker is in half-open state
type CircuitHalfOpenError struct {
	Subject string
}

func (e *CircuitHalfOpenError) Error() string {
	return fmt.Sprintf("circuit breaker half-open, too many requests for subject %s", e.Subject)
}

// IsCircuitHalfOpen returns true if the error is a CircuitHalfOpenError
func IsCircuitHalfOpen(err error) bool {
	_, ok := err.(*CircuitHalfOpenError)
	return ok
}
