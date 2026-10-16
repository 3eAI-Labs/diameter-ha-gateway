// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

// Package natstest runs an in-process NATS server for tests. Only test code
// imports it, so the server never reaches the gateway binary.
package natstest

import (
	"testing"
	"time"

	srvtest "github.com/nats-io/nats-server/v2/test"
)

// Start boots a NATS server on a random port and returns its URL. With
// jetStream set the server also runs JetStream, as a production server may;
// the gateway must not depend on either.
func Start(t *testing.T, jetStream bool) string {
	t.Helper()
	opts := srvtest.DefaultTestOptions
	opts.Port = -1
	if jetStream {
		opts.JetStream = true
		opts.StoreDir = t.TempDir()
	}
	srv := srvtest.RunServer(&opts)
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}
	return srv.ClientURL()
}
