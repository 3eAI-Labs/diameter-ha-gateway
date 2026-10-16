// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"

	"github.com/spf13/viper"
)

// Reloader holds the live *Config and exposes a thread-safe reload primitive.
// Consumers that want hot-reload semantics call Get() at every read; those
// that capture the pointer at construction effectively pin the version they
// were given (acceptable for non-reloadable fields).
type Reloader struct {
	current atomic.Pointer[Config]
}

// NewReloader returns a Reloader seeded with the supplied initial config.
// Pass the result of Load() so that subsequent Reload calls compare against
// the on-disk baseline.
func NewReloader(initial *Config) *Reloader {
	r := &Reloader{}
	r.current.Store(initial)
	return r
}

// Get returns the most recently committed config. Never returns nil after
// NewReloader has been called.
func (r *Reloader) Get() *Config {
	return r.current.Load()
}

// ErrNonReloadableField reports an attempt to reload a configuration field
// that requires a process restart. The wrapped error names the offending
// field for operator diagnostics.
var ErrNonReloadableField = errors.New("non-reloadable field changed; restart required")

// Reload re-reads the on-disk config, rejects changes to fields that are not
// hot-reloadable, and atomically swaps the current pointer on success. Returns
// the new config on success or an error (preserving the prior config on disk
// reads / parse errors / non-reloadable diffs).
//
// The non-reloadable fields are listed in nonReloadableDiff.
func (r *Reloader) Reload() (*Config, error) {
	fresh, err := Load()
	if err != nil {
		return nil, fmt.Errorf("reload: %w", err)
	}
	old := r.current.Load()
	if changed := nonReloadableDiff(old, fresh); len(changed) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrNonReloadableField, changed)
	}
	r.current.Store(fresh)
	return fresh, nil
}

// nonReloadableDiff returns the list of non-reloadable field names that
// differ between old and new. Empty result means the reload is safe.
func nonReloadableDiff(old, fresh *Config) []string {
	var changed []string
	if old.Server.ListenAddr != fresh.Server.ListenAddr {
		changed = append(changed, "server.listen_addr")
	}
	if old.Server.SecondaryAddr != fresh.Server.SecondaryAddr {
		changed = append(changed, "server.secondary_addr")
	}
	if old.Server.Network != fresh.Server.Network {
		changed = append(changed, "server.network")
	}
	if old.Server.OriginHost != fresh.Server.OriginHost {
		changed = append(changed, "server.origin_host")
	}
	if old.Server.OriginRealm != fresh.Server.OriginRealm {
		changed = append(changed, "server.origin_realm")
	}
	if !reflect.DeepEqual(old.Server.TLS, fresh.Server.TLS) {
		changed = append(changed, "server.tls.*")
	}
	if !reflect.DeepEqual(old.Admin, fresh.Admin) {
		changed = append(changed, "admin.*")
	}
	if old.AppEnv != fresh.AppEnv {
		changed = append(changed, "app_env")
	}
	return changed
}

// LoadWithViper provides a hook for tests / future custom paths. Production
// uses Load() which targets ./configs/config.yaml; reload uses the same.
func LoadWithViper(v *viper.Viper) (*Config, error) {
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
