// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package diameter

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
)

// ErrPeerUnauthorized is returned when a peer fails the whitelist check.
// Wrapped errors carry detail; consumers should rely on errors.Is for the
// sentinel value and avoid string-matching the message.
var ErrPeerUnauthorized = errors.New("peer not in whitelist")

// AuthorizePeer authenticates a peer: a CER is accepted only when (sourceIP,
// originHost) matches an entry in cfg.Peers. The function returns the matching
// PeerConfig (so the caller can stamp the Peer with operator metadata), or
// ErrPeerUnauthorized wrapped with diagnostic context when the check fails.
//
// Implementation contract:
//   - sourceIP is matched first (cheapest reject). The PeerConfig.IP field may
//     be either an exact IP literal or a CIDR (e.g. "10.0.0.0/24"). An empty
//     PeerConfig.IP is treated as "any IP allowed for this entry" — useful
//     for mTLS-only deployments where peer identity is bound by certificate.
//   - A multi-homed SCTP association has several source addresses
//     ("ip1/ip2:port"). Every one of them must match the entry: the peer
//     lists them itself, so one matching address proves nothing. Whitelist
//     such a peer with a CIDR that covers all its addresses.
//   - originHost is then matched against PeerConfig.Name and the optional
//     PeerConfig.AllowedOrigins[] alternates. An empty PeerConfig.Name skips
//     the check IF cfg.Server.PermissiveOriginHost is true (dev-only escape).
//   - Returns ErrPeerUnauthorized on any failure; caller must close the
//     connection and emit BUS_PEER_UNAUTHORIZED audit + alarm.
//
// This predicate is the security chokepoint for the Diameter trust boundary.
// The exact precedence (IP vs Origin-Host, CIDR vs exact match, AllowedOrigins
// fallback semantics) is the place where operator policy meets defensive coding.
func AuthorizePeer(cfg *config.Config, sourceIP, originHost string) (*config.PeerConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: nil config", ErrPeerUnauthorized)
	}
	if originHost == "" {
		return nil, fmt.Errorf("%w: missing Origin-Host", ErrPeerUnauthorized)
	}

	hosts := sourceHosts(sourceIP)

	for i := range cfg.Peers {
		peer := &cfg.Peers[i]

		if !allIPsMatch(peer.IP, hosts) {
			continue
		}
		if !originMatches(peer, originHost, cfg.Server.PermissiveOriginHost) {
			continue
		}
		return peer, nil
	}

	return nil, fmt.Errorf("%w: source_ip=%s origin_host=%s", ErrPeerUnauthorized, sourceIP, originHost)
}

// stripPort drops a trailing ":port" from an address string. Diameter peers
// arrive with addresses like "10.0.0.5:51234"; whitelist entries are the IP
// only. Leaves IPv6 literals intact when bracket-port suffix is absent.
func stripPort(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// sourceHosts returns the address hosts of a connection's remote address:
// one for TCP ("10.0.0.5:51234", "[2001:db8::1]:51234"), one or more for
// SCTP, which go-diameter's SCTP transport writes "ip1/ip2:port" with IPv6
// addresses in brackets.
func sourceHosts(addr string) []string {
	host := stripPort(addr)
	if !strings.Contains(host, "/") {
		return []string{host}
	}
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.HasSuffix(addr, "]") {
		host = addr[:i]
	}
	hosts := strings.Split(host, "/")
	for i, h := range hosts {
		hosts[i] = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	}
	return hosts
}

// allIPsMatch reports whether every source host matches the entry's IP.
func allIPsMatch(peerIP string, hosts []string) bool {
	for _, h := range hosts {
		if !ipMatches(peerIP, net.ParseIP(h), h) {
			return false
		}
	}
	return len(hosts) > 0
}

// ipMatches reports whether parsedIP belongs to the configured peer IP entry.
// peerIP may be empty (matches any), a CIDR ("10.0.0.0/24"), or an exact IP
// literal. raw is the original sourceIP retained for safe fallback comparison
// when parsedIP is nil (e.g. host names — discouraged but tolerated).
func ipMatches(peerIP string, parsedIP net.IP, raw string) bool {
	if peerIP == "" {
		return true
	}
	if strings.Contains(peerIP, "/") {
		_, network, err := net.ParseCIDR(peerIP)
		if err != nil || parsedIP == nil {
			return false
		}
		return network.Contains(parsedIP)
	}
	if parsedIP != nil {
		return parsedIP.Equal(net.ParseIP(peerIP))
	}
	return strings.EqualFold(peerIP, stripPort(raw))
}

// originMatches reports whether the advertised Origin-Host is acceptable for
// the matched peer entry. Permissive mode skips the check when peer.Name is
// empty — strictly a development convenience.
func originMatches(peer *config.PeerConfig, originHost string, permissive bool) bool {
	if peer.Name == "" {
		return permissive
	}
	if strings.EqualFold(peer.Name, originHost) {
		return true
	}
	for _, alt := range peer.AllowedOrigins {
		if strings.EqualFold(alt, originHost) {
			return true
		}
	}
	return false
}
