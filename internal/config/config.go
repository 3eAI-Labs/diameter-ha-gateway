// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Admin    AdminConfig    `mapstructure:"admin"`
	Nats     NatsConfig     `mapstructure:"nats"`
	Peers    []PeerConfig   `mapstructure:"peers"`
	SGd      SGdConfig      `mapstructure:"sgd"`
	Charging ChargingConfig `mapstructure:"charging"`
	AppEnv   string         `mapstructure:"app_env"` // "dev" | "stage" | "prod"
}

// ChargingConfig configures the Gx/Gy/Ro charging interfaces (TS 29.212/32.299).
// Single CCR command code is dispatched by Service-Context-Id AVP.
type ChargingConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	RequestTimeout string   `mapstructure:"request_timeout"`
	GxContextIDs   []string `mapstructure:"gx_context_ids"` // Service-Context-Id values mapped to Gx
	GyContextIDs   []string `mapstructure:"gy_context_ids"`
	RoContextIDs   []string `mapstructure:"ro_context_ids"`

	// Gx interface-specific knobs (PCRF reverse direction).
	Gx GxConfig `mapstructure:"gx"`
}

// GxConfig — Gx-only knobs that don't apply to Gy/Ro.
type GxConfig struct {
	// SessionEviction tunes the Session-Id → PCEF binding registry that
	// reverse-direction RAR/ASR uses to resolve the target peer.
	SessionEviction GxSessionEvictionConfig `mapstructure:"session_eviction"`
}

// GxSessionEvictionConfig drives the GxSessionRegistry eviction predicate.
// All three durations compose into a single hybrid formula, so no single
// pure policy (TTL, idle, absolute) has to fit every traffic mix:
//
//	if MinLifetime > 0 AND (now - BoundAt) < MinLifetime        → never evict
//	if IdleTimeout > 0 AND (now - LastSeen) >= IdleTimeout      → evict
//	if AbsoluteLifetime > 0 AND (now - BoundAt) >= AbsoluteLifetime → evict
//	otherwise                                                   → keep
//
// Pure policies are special cases:
//   - "none" / strict telco: all three = 0 (rely only on Unbind + DropPeer)
//   - "idle" / smartphone-heavy: IdleTimeout=24h, others = 0
//   - "absolute" / fixed-lifetime billing: AbsoluteLifetime=30d, others = 0
//   - "hybrid" / mixed-traffic: all three set
//
// Preset is a shortcut. When set, it fills the three durations with carrier-
// vetted defaults. Explicit values override preset defaults. Empty preset
// reads the explicit values directly.
type GxSessionEvictionConfig struct {
	// Preset selects a named operator profile. Recognized values: "iot",
	// "smartphone", "mixed", "strict". Empty (default) ⇒ use explicit
	// duration fields below as-is.
	Preset string `mapstructure:"preset"`

	// MinLifetime — bindings younger than this are never evicted by TTL.
	// Defense for long-lived but sometimes-quiet sessions (IoT default
	// bearers). 0 ⇒ no floor.
	MinLifetime string `mapstructure:"min_lifetime"`

	// IdleTimeout — bindings whose LastSeen is older than this get
	// evicted (idle semantic). LastSeen refreshes on every CCR-I/CCR-U.
	// 0 ⇒ no idle eviction.
	IdleTimeout string `mapstructure:"idle_timeout"`

	// AbsoluteLifetime — bindings older than this (BoundAt-based) get
	// evicted regardless of activity. Backstop for billing windows.
	// 0 ⇒ no absolute cap.
	AbsoluteLifetime string `mapstructure:"absolute_lifetime"`
}

// SGdConfig configures the SGd SMS interface (TS 29.338)
type SGdConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	RequestTimeout string `mapstructure:"request_timeout"` // Timeout for NATS requests
}

type ServerConfig struct {
	ListenAddr     string `mapstructure:"listen_addr"`
	SecondaryAddr  string `mapstructure:"secondary_addr"`
	Network        string `mapstructure:"network"` // tcp, tcp4, tcp6, sctp, sctp4, sctp6
	OriginHost     string `mapstructure:"origin_host"`
	OriginRealm    string `mapstructure:"origin_realm"`
	ProductName    string `mapstructure:"product_name"`
	MaxConnections int    `mapstructure:"max_connections"`

	// ReverseTimeout caps how long the reverse path (gateway → MME) waits for an
	// answer (CLA/IDA/...) before publishing 3002 to the NATS reply subject.
	// Default: 5s.
	ReverseTimeout string `mapstructure:"reverse_timeout"`

	// MMEDestRealm is the default Origin-Realm used when constructing reverse
	// path Diameter requests targeted at MMEs and the inbound NATS event has
	// no header.dest_realm set. Was previously hardcoded to a TR PLMN realm.
	MMEDestRealm string `mapstructure:"mme_dest_realm"`

	// Watchdog controls explicit DWR/DWA emission.
	WatchdogInterval string `mapstructure:"watchdog_interval"` // default 30s
	WatchdogTimeout  string `mapstructure:"watchdog_timeout"`  // default 90s

	// IdlePeerTimeout — peers that haven't sent any APPLICATION traffic
	// (forward request) within this window are force-closed by the
	// watchdog. Distinct from watchdog_timeout because DWA-only
	// liveness can mask half-open zombies. Default 5m. Empty / "0" disables.
	IdlePeerTimeout string `mapstructure:"idle_peer_timeout"`

	// TLS controls Diameter TLS (RFC 6733 §13.1).
	TLS DiameterTLSConfig `mapstructure:"tls"`

	// PermissiveOriginHost allows accepting any Origin-Host that matches an
	// IP-whitelisted peer entry, even when PeerConfig.Name is non-empty.
	// MUST remain false in production. Default false.
	PermissiveOriginHost bool `mapstructure:"permissive_origin_host"`

	// Redirect-Host (RFC 6733 §6.5) controls. RedirectDefaultTTL and
	// RedirectCacheCapacity size the redirect cache (forward-encode lane);
	// RedirectMaxDepth bounds the reverse-honor retry path.
	RedirectDefaultTTL    string `mapstructure:"redirect_default_ttl"`    // default 60s
	RedirectMaxDepth      int    `mapstructure:"redirect_max_depth"`      // default 3
	RedirectCacheCapacity int    `mapstructure:"redirect_cache_capacity"` // default 10000

	// RedirectIDRRetryEnabled — operator opt-in for transparent IDR retry
	// on §6.5 redirect. Off by default: IDR pushes
	// subscriber profile delta and double-application risks state divergence
	// across non-replicated peers. Safe to enable in clustered HSS where
	// MMEs replicate profile state across the cluster.
	RedirectIDRRetryEnabled bool `mapstructure:"redirect_idr_retry_enabled"`

	// RedirectASRRetryEnabled — operator opt-in for transparent ASR retry
	// on §6.5 redirect. Off by default: ASR can produce
	// 5002 (UNKNOWN_SESSION_ID) on a re-hit, which some PCEF vendors raise
	// as a hard error rather than soft success. Enable when the PCEF fleet
	// is known to treat 5002-on-ASR as benign.
	RedirectASRRetryEnabled bool `mapstructure:"redirect_asr_retry_enabled"`
}

// DiameterTLSConfig is the Diameter peer-side TLS configuration (server side).
// Distinct from NATS TLSConfig; do not reuse, paths and trust roots differ.
type DiameterTLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	// ClientCA enables mTLS when set: peer must present a cert signed by ClientCA.
	ClientCA string `mapstructure:"client_ca"`
}

type AdminConfig struct {
	Port    int        `mapstructure:"port"`
	Enabled bool       `mapstructure:"enabled"`
	Auth    AuthConfig `mapstructure:"auth"`
}

// AuthConfig enables bearer-token authentication on the admin API. A token is
// accepted when its signature verifies against a key from JWKSURL and its iss,
// aud and exp claims match. All three settings are required when enabled.
type AuthConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	JWKSURL  string `mapstructure:"jwks_url"`
	Issuer   string `mapstructure:"issuer"`
	Audience string `mapstructure:"audience"`
}

type NatsConfig struct {
	URL            string               `mapstructure:"url"`
	ConnectTimeout string               `mapstructure:"connect_timeout"`
	RequestTimeout string               `mapstructure:"request_timeout"`
	TLS            TLSConfig            `mapstructure:"tls"`
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
}

// CircuitBreakerConfig configures the circuit breaker for NATS calls
// Based on CODING_GUIDELINES.md resilience patterns
type CircuitBreakerConfig struct {
	// Name identifies this circuit breaker instance
	Name string `mapstructure:"name"`
	// MaxRequests is the maximum number of requests allowed in half-open state
	MaxRequests uint32 `mapstructure:"max_requests"`
	// Interval is the cyclic period of closed state to clear internal counts
	Interval string `mapstructure:"interval"`
	// Timeout is the period of open state before transitioning to half-open
	Timeout string `mapstructure:"timeout"`
	// FailureRatio is the failure ratio threshold (0.0-1.0) that trips the circuit
	FailureRatio float64 `mapstructure:"failure_ratio"`
	// MinRequests is minimum requests needed before circuit can trip
	MinRequests uint32 `mapstructure:"min_requests"`
}

type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CACert   string `mapstructure:"ca_cert"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

type PeerConfig struct {
	IP             string   `mapstructure:"ip"`              // IP or CIDR
	Name           string   `mapstructure:"name"`            // expected Origin-Host
	Realm          string   `mapstructure:"realm"`           // expected Origin-Realm
	AllowedOrigins []string `mapstructure:"allowed_origins"` // optional alternate Origin-Host values

	// RateLimitTPS — per-peer token-bucket cap on forward requests.
	// 0 = unbounded (default). Burst capacity is sized as
	// 2× this value to tolerate normal LTE attach bursts. Over-rate
	// requests get Result-Code 3004 (TOO_BUSY); 100 consecutive trips
	// trigger BUS_PEER_RATELIMIT audit.
	RateLimitTPS int `mapstructure:"rate_limit_tps"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath("./configs")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		logger.Warn("No config file found, using defaults and ENV")
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// Set Defaults
	if cfg.Admin.Port == 0 {
		cfg.Admin.Port = 8081
	}
	if cfg.Server.Network == "" {
		cfg.Server.Network = "tcp" // Default to TCP for backward compatibility
	}

	// Circuit Breaker Defaults (per CODING_GUIDELINES.md)
	if cfg.Nats.CircuitBreaker.Name == "" {
		cfg.Nats.CircuitBreaker.Name = "diameter-gateway-nats"
	}
	if cfg.Nats.CircuitBreaker.MaxRequests == 0 {
		cfg.Nats.CircuitBreaker.MaxRequests = 5
	}
	if cfg.Nats.CircuitBreaker.Interval == "" {
		cfg.Nats.CircuitBreaker.Interval = "10s"
	}
	if cfg.Nats.CircuitBreaker.Timeout == "" {
		cfg.Nats.CircuitBreaker.Timeout = "30s"
	}
	if cfg.Nats.CircuitBreaker.FailureRatio == 0 {
		cfg.Nats.CircuitBreaker.FailureRatio = 0.5 // Trip at 50% failure rate
	}
	if cfg.Nats.CircuitBreaker.MinRequests == 0 {
		cfg.Nats.CircuitBreaker.MinRequests = 10
	}

	// SGd Defaults
	if cfg.SGd.RequestTimeout == "" {
		cfg.SGd.RequestTimeout = "2s"
	}

	// Server reverse path defaults
	if cfg.Server.ReverseTimeout == "" {
		cfg.Server.ReverseTimeout = "5s"
	}
	if cfg.Server.MMEDestRealm == "" {
		// Fall back to OriginRealm when sender supplies no dest_realm.
		cfg.Server.MMEDestRealm = cfg.Server.OriginRealm
	}

	// Watchdog defaults
	if cfg.Server.WatchdogInterval == "" {
		cfg.Server.WatchdogInterval = "30s"
	}
	if cfg.Server.WatchdogTimeout == "" {
		cfg.Server.WatchdogTimeout = "90s"
	}
	if cfg.Server.IdlePeerTimeout == "" {
		cfg.Server.IdlePeerTimeout = "5m"
	}

	// Redirect-Host defaults.
	if cfg.Server.RedirectDefaultTTL == "" {
		cfg.Server.RedirectDefaultTTL = "60s"
	}
	if cfg.Server.RedirectMaxDepth == 0 {
		cfg.Server.RedirectMaxDepth = 3
	}
	if cfg.Server.RedirectCacheCapacity == 0 {
		cfg.Server.RedirectCacheCapacity = 10000
	}

	// Charging defaults
	if cfg.Charging.RequestTimeout == "" {
		cfg.Charging.RequestTimeout = "2s"
	}

	// Gx session-eviction preset expansion. Preset fills any field left empty;
	// explicit values always win. Default preset = "mixed" (sensible carrier
	// baseline; operators with skewed traffic should set "iot" or "smartphone").
	applyGxEvictionPreset(&cfg.Charging.Gx.SessionEviction)

	if cfg.AppEnv == "" {
		cfg.AppEnv = "dev"
	}

	return &cfg, nil
}

// applyGxEvictionPreset fills empty duration fields from a named preset.
// Recognized presets (operator-targeted defaults):
//
//	"iot"        — long-lived sessions with rare CCR-U (smart meters, M2M).
//	               Floor: 30d, idle: 90d, absolute: off.
//	"smartphone" — short to medium sessions with frequent refresh.
//	               Floor: off, idle: 24h, absolute: off.
//	"mixed"      — heterogeneous traffic; carrier-grade default.
//	               Floor: 7d, idle: 24h, absolute: 30d.
//	"strict"     — never TTL-evict; rely on Unbind/DropPeer only.
//	               All zero.
//	""           — no preset; honor explicit fields verbatim. When ALL three
//	               are also empty, falls back to "mixed".
//
// Operators wanting a custom mix set Preset to one of the above AND override
// individual fields. e.g. preset=iot + idle_timeout=180d → IoT floor with
// stretched idle window.
func applyGxEvictionPreset(c *GxSessionEvictionConfig) {
	preset := c.Preset
	if preset == "" && c.MinLifetime == "" && c.IdleTimeout == "" && c.AbsoluteLifetime == "" {
		preset = "mixed"
	}
	var p GxSessionEvictionConfig
	switch preset {
	case "iot":
		p = GxSessionEvictionConfig{MinLifetime: "720h", IdleTimeout: "2160h"} // 30d, 90d
	case "smartphone":
		p = GxSessionEvictionConfig{IdleTimeout: "24h"}
	case "mixed":
		p = GxSessionEvictionConfig{MinLifetime: "168h", IdleTimeout: "24h", AbsoluteLifetime: "720h"} // 7d, 24h, 30d
	case "strict":
		// All zero — explicit no-op preset for documentation clarity.
	}
	if c.MinLifetime == "" {
		c.MinLifetime = p.MinLifetime
	}
	if c.IdleTimeout == "" {
		c.IdleTimeout = p.IdleTimeout
	}
	if c.AbsoluteLifetime == "" {
		c.AbsoluteLifetime = p.AbsoluteLifetime
	}
}

// IsProduction returns true when the gateway is running under a non-development
// environment. Used to gate auth-mandatory paths.
func (c *Config) IsProduction() bool {
	switch c.AppEnv {
	case "prod", "production", "stage", "staging":
		return true
	}
	return false
}
