// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// applyGxEvictionPreset is the operator-facing layer between named profiles
// (iot/smartphone/mixed/strict) and the 3-parameter hybrid formula. The
// guarantees we test:
//
//  1. Empty preset + empty fields ⇒ "mixed" (carrier-grade default).
//  2. Named preset fills the empty fields with vetted durations.
//  3. Explicit values always win over preset defaults (override semantic).
//  4. "strict" preset ⇒ all zero (TTL eviction off).
//  5. Unknown preset ⇒ no preset durations applied (explicit fields stay).
func TestApplyGxEvictionPreset_DefaultIsMixed(t *testing.T) {
	c := GxSessionEvictionConfig{}
	applyGxEvictionPreset(&c)
	assert.Equal(t, "168h", c.MinLifetime, "default ⇒ mixed preset min=7d")
	assert.Equal(t, "24h", c.IdleTimeout)
	assert.Equal(t, "720h", c.AbsoluteLifetime)
}

func TestApplyGxEvictionPreset_IoT(t *testing.T) {
	c := GxSessionEvictionConfig{Preset: "iot"}
	applyGxEvictionPreset(&c)
	assert.Equal(t, "720h", c.MinLifetime, "iot floor 30d")
	assert.Equal(t, "2160h", c.IdleTimeout, "iot idle 90d")
	assert.Equal(t, "", c.AbsoluteLifetime, "iot has no absolute cap")
}

func TestApplyGxEvictionPreset_Smartphone(t *testing.T) {
	c := GxSessionEvictionConfig{Preset: "smartphone"}
	applyGxEvictionPreset(&c)
	assert.Equal(t, "", c.MinLifetime)
	assert.Equal(t, "24h", c.IdleTimeout)
	assert.Equal(t, "", c.AbsoluteLifetime)
}

func TestApplyGxEvictionPreset_Strict(t *testing.T) {
	c := GxSessionEvictionConfig{Preset: "strict"}
	applyGxEvictionPreset(&c)
	assert.Equal(t, GxSessionEvictionConfig{Preset: "strict"}, c, "strict ⇒ all zero")
}

func TestApplyGxEvictionPreset_ExplicitOverridesPreset(t *testing.T) {
	c := GxSessionEvictionConfig{
		Preset:      "iot",
		IdleTimeout: "180d", // operator wants stretched idle on top of IoT floor
	}
	applyGxEvictionPreset(&c)
	assert.Equal(t, "720h", c.MinLifetime, "preset fills empty MinLifetime")
	assert.Equal(t, "180d", c.IdleTimeout, "explicit IdleTimeout overrides preset's 90d")
}

func TestApplyGxEvictionPreset_UnknownPresetIsNoOp(t *testing.T) {
	c := GxSessionEvictionConfig{
		Preset:      "made-up",
		IdleTimeout: "5h",
	}
	applyGxEvictionPreset(&c)
	assert.Equal(t, "5h", c.IdleTimeout, "explicit value preserved")
	assert.Equal(t, "", c.MinLifetime, "unknown preset doesn't synthesize values")
}

func TestApplyGxEvictionPreset_ExplicitOnlyStaysExplicit(t *testing.T) {
	c := GxSessionEvictionConfig{IdleTimeout: "10m"}
	applyGxEvictionPreset(&c)
	// Has at least one explicit field ⇒ no preset fallback.
	assert.Equal(t, "", c.MinLifetime)
	assert.Equal(t, "10m", c.IdleTimeout)
	assert.Equal(t, "", c.AbsoluteLifetime)
}
