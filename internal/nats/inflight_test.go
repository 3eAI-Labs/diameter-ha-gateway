// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package nats

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInflight_Bounded: past MaxInFlight waiting requests, a request is
// refused at once rather than started.
func TestInflight_Bounded(t *testing.T) {
	f := make(Inflight, 2)
	release := make(chan struct{})
	var busy int
	h := f.Wrap(func(*nats.Msg) { <-release }, func(*nats.Msg) { busy++ })
	for i := 0; i < 3; i++ {
		h(&nats.Msg{})
	}
	assert.Equal(t, 1, busy)
	close(release)
	require.Eventually(t, func() bool { return len(f) == 0 }, time.Second, 5*time.Millisecond)
	h(&nats.Msg{})
	assert.Equal(t, 1, busy, "a slot freed is reused")
}
