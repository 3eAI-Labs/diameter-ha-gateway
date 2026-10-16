// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

// Package diameter — Redirect-Host AVP processing per RFC 6733 §6.5.
//
// The cache and parser serve both directions: forward answers populate it,
// and reverse-direction retry consumes the same cache.
package diameter

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// RedirectHostUsage encodes the seven values from RFC 6733 §6.13.
//
// The Usage value drives the cache key: a redirect with Usage=ALL_REALM is
// keyed by the destination realm, ALL_HOST by destination host, and so on.
// Mis-keying leaks routing decisions across unrelated traffic.
type RedirectHostUsage uint32

const (
	RedirectUsageDontCache           RedirectHostUsage = 0 // §6.13: do not cache; honor once
	RedirectUsageAllSession          RedirectHostUsage = 1 // cache for the lifetime of the Session-Id
	RedirectUsageAllRealm            RedirectHostUsage = 2 // cache for all messages to the destination Realm
	RedirectUsageRealmAndApplication RedirectHostUsage = 3 // cache for (Realm, Application-Id) tuple
	RedirectUsageAllApplication      RedirectHostUsage = 4 // cache for the Application-Id
	RedirectUsageAllHost             RedirectHostUsage = 5 // cache for the destination Host
	RedirectUsageAllUser             RedirectHostUsage = 6 // cache for the User-Name (IMSI)
)

// String returns the canonical RFC label — used as the `usage` metric label
// so dashboards stay aligned with the spec rather than the integer encoding.
func (u RedirectHostUsage) String() string {
	switch u {
	case RedirectUsageDontCache:
		return "DONT_CACHE"
	case RedirectUsageAllSession:
		return "ALL_SESSION"
	case RedirectUsageAllRealm:
		return "ALL_REALM"
	case RedirectUsageRealmAndApplication:
		return "REALM_AND_APPLICATION"
	case RedirectUsageAllApplication:
		return "ALL_APPLICATION"
	case RedirectUsageAllHost:
		return "ALL_HOST"
	case RedirectUsageAllUser:
		return "ALL_USER"
	}
	return "UNKNOWN"
}

// RetryPosture controls whether a redirected retry is safe for a given
// reverse-direction verb. The classification follows each verb's TS 29.272
// semantics: a desired-state verb (RSR, CLR) is a no-op when applied twice,
// while a retried IDR can leave independent peers with diverging profiles.
type RetryPosture int

const (
	// NeverRetry — verb has non-idempotent side effects on double-application,
	// or is not classified (DSR, fail closed). Gateway returns the 3006
	// answer to the NATS caller verbatim; caller decides what to do.
	NeverRetry RetryPosture = 0

	// RetryAlways — verb is idempotent or has acceptable double-apply
	// semantics (RSR desired-state, CLR Session-Id unique).
	RetryAlways RetryPosture = 1

	// RetryIfOperatorOptsIn — verb has side effects on non-replicated
	// peers but is acceptable in clustered topology (IDR profile push).
	// Gated behind config (server.redirect_idr_retry_enabled).
	RetryIfOperatorOptsIn RetryPosture = 2
)

// RetryPostureFor returns the per-verb posture for reverse-direction redirect
// retry. Verbs not in the table default to NeverRetry — fail closed when the
// classification is uncertain.
//
// Gx:
//
//   - RAR — Charging-Rule install/remove keyed by Rule-Name; PCEF dedups
//     duplicate installs, missing rules tolerate re-remove. Idempotent in
//     practice ⇒ RetryAlways.
//   - ASR — Session abort. PCEF returns 5002 (UNKNOWN_SESSION_ID) on a
//     second hit, which most stacks treat as soft success — but some PCEF
//     vendors raise it as a hard error and operators see noisy alerts.
//     Gated behind config so each operator decides per their PCEF posture.
func RetryPostureFor(verb string) RetryPosture {
	switch verb {
	case "RSR", "CLR", "RAR":
		return RetryAlways
	case "IDR", "ASR":
		return RetryIfOperatorOptsIn
	}
	return NeverRetry
}

// RedirectIndication is the parsed view of an inbound answer carrying
// Result-Code = 3006 (DIAMETER_REDIRECT_INDICATION). Multiple Redirect-Host
// AVPs MAY appear (§8.34); we preserve the order — RFC §6.5 leaves selection
// policy to the implementation, but operators expect "try the first one".
type RedirectIndication struct {
	Hosts        []string          // ordered list of DiameterURI targets
	Usage        RedirectHostUsage // §6.13 scope
	MaxCacheTime time.Duration     // §6.12; zero ⇒ peer omitted the AVP
}

// RedirectContext is the per-request shape needed to derive a cache key from
// a Usage value. Populated at parse time by the caller (gateway.go for
// forward-encode, reverse.go for reverse-honor).
type RedirectContext struct {
	SessionID     string // RFC §8.8 (User-Name binding when Usage=ALL_USER fails)
	DestRealm     string // RFC §8.36
	DestHost      string // RFC §8.35
	ApplicationID uint32 // CCF / S6a / Gx / ...
	UserName      string // RFC §8.14 (IMSI in S6a)
}

// CachedRedirect is the entry stored in the LRU. The expiresAt timestamp is
// computed at Store time from MaxCacheTime (or the configured default TTL
// when peer omits the AVP).
type CachedRedirect struct {
	Targets   []string
	Usage     RedirectHostUsage
	ExpiresAt time.Time
}

// RedirectCacheOptions tunes the cache policy. The defaults favour routing
// freshness over hit rate: a 60s TTL when the peer omits
// Redirect-Max-Cache-Time, and lazy eviction with no sweep goroutine.
type RedirectCacheOptions struct {
	Capacity   int           // LRU bound; 0 ⇒ unbounded
	DefaultTTL time.Duration // applied when peer omits Redirect-Max-Cache-Time
	Now        func() time.Time
}

// RedirectCache is a thread-safe TTL+LRU cache of redirect indications keyed
// by (Usage, derived-key). The key derivation lives in keyFor(); see the
// Usage matrix in §6.13 for the exact mapping.
type RedirectCache struct {
	mu    sync.RWMutex
	opts  RedirectCacheOptions
	byKey map[string]*CachedRedirect
}

func NewRedirectCache(opts RedirectCacheOptions) *RedirectCache {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.DefaultTTL == 0 {
		opts.DefaultTTL = 60 * time.Second
	}
	return &RedirectCache{
		opts:  opts,
		byKey: make(map[string]*CachedRedirect),
	}
}

// keyFor derives the cache key for a given Usage + RedirectContext per RFC
// §6.13. Returns ("", false) when Usage is DONT_CACHE — caller MUST skip
// Store/Lookup in that case.
//
// The Usage value is the cache scope. Wrong derivation here is the single
// most dangerous bug class for this feature: keying ALL_REALM at host
// granularity (or the inverse) silently leaks routing decisions across
// unrelated traffic.
//
// RFC 6733 §6.13 dictates the mapping:
//
//	DONT_CACHE             → no cache; this function returns ("", false)
//	ALL_SESSION            → key by Session-Id   (ctx.SessionID)
//	ALL_REALM              → key by Dest-Realm   (ctx.DestRealm)
//	REALM_AND_APPLICATION  → key by (Dest-Realm, Application-Id)
//	ALL_APPLICATION        → key by Application-Id alone
//	ALL_HOST               → key by Dest-Host    (ctx.DestHost)
//	ALL_USER               → key by User-Name    (ctx.UserName, IMSI in S6a)
//
// Every key is prefixed with usage.String() so keys never collide across
// Usage values. Returns ("", false) when the required ctx field is empty
// (e.g. ALL_USER but ctx.UserName == "") — caller falls back to non-cached
// single-shot honoring.
func (c *RedirectCache) keyFor(usage RedirectHostUsage, ctx RedirectContext) (string, bool) {
	if usage == RedirectUsageDontCache {
		return "", false
	}
	// Prefix every key with usage so two different scopes never collide
	// (e.g. ALL_REALM=epc.example.com vs ALL_USER=epc.example.com).
	prefix := usage.String() + "|"
	switch usage {
	case RedirectUsageAllSession:
		if ctx.SessionID == "" {
			return "", false
		}
		return prefix + ctx.SessionID, true
	case RedirectUsageAllRealm:
		if ctx.DestRealm == "" {
			return "", false
		}
		return prefix + strings.ToLower(ctx.DestRealm), true
	case RedirectUsageRealmAndApplication:
		if ctx.DestRealm == "" {
			return "", false
		}
		return prefix + strings.ToLower(ctx.DestRealm) + "|" + strconv.FormatUint(uint64(ctx.ApplicationID), 10), true
	case RedirectUsageAllApplication:
		return prefix + strconv.FormatUint(uint64(ctx.ApplicationID), 10), true
	case RedirectUsageAllHost:
		if ctx.DestHost == "" {
			return "", false
		}
		return prefix + strings.ToLower(ctx.DestHost), true
	case RedirectUsageAllUser:
		if ctx.UserName == "" {
			return "", false
		}
		return prefix + ctx.UserName, true
	}
	return "", false
}

// Lookup returns a cached redirect for (usage, ctx) when present and unexpired.
// Lazy eviction: an expired entry is deleted on read so the next Store can
// reuse the slot without a sweep goroutine.
func (c *RedirectCache) Lookup(usage RedirectHostUsage, ctx RedirectContext) (*CachedRedirect, bool) {
	key, ok := c.keyFor(usage, ctx)
	if !ok {
		return nil, false
	}
	c.mu.RLock()
	entry, present := c.byKey[key]
	c.mu.RUnlock()
	if !present {
		return nil, false
	}
	if c.opts.Now().After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.byKey, key)
		c.mu.Unlock()
		return nil, false
	}
	return entry, true
}

// Store records a redirect indication in the cache. No-op when Usage is
// DONT_CACHE or when the derived key is empty (missing required ctx field).
func (c *RedirectCache) Store(ind *RedirectIndication, ctx RedirectContext) {
	key, ok := c.keyFor(ind.Usage, ctx)
	if !ok {
		return
	}
	ttl := ind.MaxCacheTime
	if ttl == 0 {
		ttl = c.opts.DefaultTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// When Capacity > 0 and the map is full, we MUST evict before inserting,
	// otherwise byKey grows unbounded under sustained redirect traffic.
	// Capacity == 0 is unbounded mode.
	if c.opts.Capacity > 0 && len(c.byKey) >= c.opts.Capacity {
		// Single-pass scan: drop the entry with the smallest ExpiresAt.
		// Predictable under load; survives capacity pressure without
		// the hot-key churn random-victim eviction can cause.
		var oldestKey string
		var oldestExp time.Time
		for k, v := range c.byKey {
			if oldestKey == "" || v.ExpiresAt.Before(oldestExp) {
				oldestKey = k
				oldestExp = v.ExpiresAt
			}
		}
		if oldestKey != "" {
			delete(c.byKey, oldestKey)
		}
	}

	c.byKey[key] = &CachedRedirect{
		Targets:   ind.Hosts,
		Usage:     ind.Usage,
		ExpiresAt: c.opts.Now().Add(ttl),
	}
}

// Size returns the current cache occupancy — used by the admin endpoint and
// the `redirect_cache_size` gauge.
func (c *RedirectCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byKey)
}

// ParseRedirectIndication extracts §6.5 AVPs from an inbound Diameter
// answer. Returns (nil, false) when the message lacks Result-Code = 3006 OR
// when no Redirect-Host AVP is present (3006 without Redirect-Host is a
// peer-side spec violation but we tolerate it by surfacing a parse miss).
func ParseRedirectIndication(m *diam.Message) (*RedirectIndication, bool) {
	rcAVP, err := m.FindAVP(avp.ResultCode, 0)
	if err != nil {
		return nil, false
	}
	rc, ok := rcAVP.Data.(datatype.Unsigned32)
	if !ok || uint32(rc) != 3006 {
		return nil, false
	}

	hostAVPs, _ := m.FindAVPs(avp.RedirectHost, 0)
	if len(hostAVPs) == 0 {
		return nil, false
	}

	ind := &RedirectIndication{
		Usage: RedirectUsageDontCache, // §6.5 default when AVP absent
	}
	for _, hAVP := range hostAVPs {
		uri, ok := hAVP.Data.(datatype.DiameterURI)
		if !ok {
			continue
		}
		ind.Hosts = append(ind.Hosts, string(uri))
	}
	if len(ind.Hosts) == 0 {
		return nil, false
	}

	if uAVP, err := m.FindAVP(avp.RedirectHostUsage, 0); err == nil {
		if u, ok := uAVP.Data.(datatype.Enumerated); ok {
			ind.Usage = RedirectHostUsage(u)
		}
	}
	if tAVP, err := m.FindAVP(avp.RedirectMaxCacheTime, 0); err == nil {
		if t, ok := tAVP.Data.(datatype.Unsigned32); ok {
			ind.MaxCacheTime = time.Duration(t) * time.Second
		}
	}

	return ind, true
}

// DetectLoop guards redirect retries. RFC 6733 §6.7 mandates Route-Record
// AVP inspection: every relay/proxy adds its Origin-Host to Route-Record,
// and a redirect retry that lands back on a previously-traversed peer must
// abort with Result-Code = 3005 (DIAMETER_LOOP_DETECTED).
//
// Two checks must both pass for the retry to proceed:
//
//  1. Hop-count check: depth (number of redirect hops taken so far for
//     this request) MUST be < maxDepth. Beyond that, abort regardless of
//     Route-Record content. The configured default is maxDepth = 3.
//
//  2. Route-Record check: the proposed nextHost MUST NOT appear in the
//     visited slice. visited is the running list of hosts the request
//     has already touched (built by the caller from Route-Record AVPs +
//     previously-redirected hosts).
//
// Return values:
//
//	reason == ""    → no loop, retry is safe
//	reason != ""    → loop detected; caller emits 3005, increments
//	                  diameter_redirect_loop_detected_total, raises
//	                  DIAM_GW_REDIRECT_LOOP alarm
//
// The host comparison is case-insensitive (DiameterIdentity is
// case-insensitive per RFC §4.3.1).
func DetectLoop(nextHost string, visited []string, depth, maxDepth int) (reason string) {
	if maxDepth > 0 && depth >= maxDepth {
		return "redirect_depth_exceeded"
	}
	for _, h := range visited {
		if strings.EqualFold(h, nextHost) {
			return "next_host_already_visited"
		}
	}
	return ""
}
