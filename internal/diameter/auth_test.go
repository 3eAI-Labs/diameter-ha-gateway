// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"testing"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestAuthorizePeer(t *testing.T) {
	cfg := &config.Config{Peers: []config.PeerConfig{
		{IP: "10.0.0.5", Name: "mme1.example"},
		{IP: "10.1.0.0/24", Name: "mme2.example", AllowedOrigins: []string{"mme2-alt.example"}},
		{IP: "2001:db8::1", Name: "mme3.example"},
	}}
	for _, tc := range []struct {
		name, source, originHost string
		ok                       bool
	}{
		{"exact IP", "10.0.0.5:51234", "mme1.example", true},
		{"Origin-Host case-insensitive", "10.0.0.5:51234", "MME1.example", true},
		{"wrong IP", "10.0.0.6:51234", "mme1.example", false},
		{"IP of another entry", "10.1.0.7:51234", "mme1.example", false},
		{"CIDR", "10.1.0.7:3868", "mme2.example", true},
		{"allowed alternate Origin-Host", "10.1.0.7:3868", "mme2-alt.example", true},
		{"no Origin-Host", "10.0.0.5:51234", "", false},
		{"IPv6", "[2001:db8::1]:3868", "mme3.example", true},
		{"SCTP, all addresses in the CIDR", "10.1.0.7/10.1.0.8:3868", "mme2.example", true},
		{"SCTP, one address outside the CIDR", "10.1.0.7/192.0.2.1:3868", "mme2.example", false},
		{"SCTP, a listed address added to a foreign one", "192.0.2.1/10.0.0.5:3868", "mme1.example", false},
		{"SCTP, IPv6 single address", "[2001:db8::1]:3868", "mme3.example", true},
		{"SCTP, IPv6 with a foreign address", "[2001:db8::1]/[2001:db8::2]:3868", "mme3.example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AuthorizePeer(cfg, tc.source, tc.originHost)
			assert.Equal(t, tc.ok, err == nil, "err=%v", err)
		})
	}
}

func TestSourceHosts(t *testing.T) {
	assert.Equal(t, []string{"10.0.0.5"}, sourceHosts("10.0.0.5:51234"))
	assert.Equal(t, []string{"2001:db8::1"}, sourceHosts("[2001:db8::1]:51234"))
	assert.Equal(t, []string{"10.0.0.5", "10.0.0.6"}, sourceHosts("10.0.0.5/10.0.0.6:3868"))
	assert.Equal(t, []string{"2001:db8::1", "2001:db8::2"}, sourceHosts("[2001:db8::1]/[2001:db8::2]:3868"))
}
