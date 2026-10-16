// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diamtest"
	"github.com/stretchr/testify/assert"
)

func TestHostOf_IsTheRegisteredIdentity(t *testing.T) {
	pm := NewPeerManager()
	a, b := &diamtest.Conn{}, &diamtest.Conn{}
	pm.Register(a, "mme-a.example")
	pm.Register(b, "mme-b.example")

	assert.Equal(t, "mme-a.example", pm.HostOf(a))
	assert.Equal(t, "mme-b.example", pm.HostOf(b))
	assert.Empty(t, pm.HostOf(&diamtest.Conn{}), "an unregistered connection has no identity")
}
