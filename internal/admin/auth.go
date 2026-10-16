// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/gin-gonic/gin"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// allowedAlgs are the signature algorithms a JWKS key may carry. Symmetric
// (HS*) keys and keys without an alg are dropped when the set is fetched, so
// a token can never be verified with a key the issuer did not mean for it.
var allowedAlgs = map[jwa.SignatureAlgorithm]struct{}{
	jwa.RS256: {},
	jwa.ES256: {},
}

const (
	// jwksRefresh is how often the key set is fetched again, so that a key
	// rotation at the issuer takes effect without a restart.
	jwksRefresh = 5 * time.Minute
	// clockSkew tolerates small clock differences on exp and nbf.
	clockSkew = 30 * time.Second
	// subjectKey holds the token's sub claim in the request context.
	subjectKey = "admin_subject"
)

// Authenticator verifies the bearer token of every admin API request.
type Authenticator struct {
	keys     jwk.Set
	issuer   string
	audience string
}

// NewAuthenticator registers the JWKS URL and fetches the key set once. A
// failed first fetch is not fatal: requests are rejected until a background
// refresh succeeds. A configuration without JWKS URL, issuer or audience is.
func NewAuthenticator(ctx context.Context, cfg config.AuthConfig) (*Authenticator, error) {
	if cfg.JWKSURL == "" || cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("admin.auth requires jwks_url, issuer and audience")
	}
	cache := jwk.NewCache(ctx, jwk.WithRefreshWindow(clockSkew), jwk.WithErrSink(errSink{}))
	if err := cache.Register(cfg.JWKSURL,
		jwk.WithMinRefreshInterval(jwksRefresh),
		jwk.WithRefreshInterval(jwksRefresh),
		jwk.WithPostFetcher(jwk.PostFetchFunc(signingKeys)),
	); err != nil {
		return nil, fmt.Errorf("admin.auth.jwks_url: %w", err)
	}
	if _, err := cache.Refresh(ctx, cfg.JWKSURL); err != nil {
		logger.Warn("Admin JWKS fetch failed; admin API rejects every request until a refresh succeeds",
			logger.String("jwks_url", cfg.JWKSURL), logger.String("error", err.Error()))
	}
	return &Authenticator{keys: jwk.NewCachedSet(cache, cfg.JWKSURL), issuer: cfg.Issuer, audience: cfg.Audience}, nil
}

// Middleware rejects a request with 401 unless it carries a valid token, and
// records the token's subject for the audit trail.
func (a *Authenticator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || raw == "" {
			reject(c, "missing bearer token")
			return
		}
		tok, err := jwt.Parse([]byte(raw),
			jwt.WithKeySet(a.keys, jws.WithInferAlgorithmFromKey(false), jws.WithRequireKid(true)),
			jwt.WithValidate(true),
			jwt.WithIssuer(a.issuer),
			jwt.WithAudience(a.audience),
			jwt.WithRequiredClaim("exp"),
			jwt.WithAcceptableSkew(clockSkew),
		)
		if err != nil {
			reject(c, err.Error())
			return
		}
		c.Set(subjectKey, tok.Subject())
		c.Next()
	}
}

// reject answers 401 with the RFC 6750 §3 challenge. The reason is logged,
// never returned, and the token itself is never logged.
func reject(c *gin.Context, reason string) {
	logger.Warn("Admin API request rejected", logger.String("path", c.FullPath()), logger.String("reason", reason))
	c.Header("WWW-Authenticate", `Bearer realm="admin"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, StandardResponse{
		Success: false,
		Message: "authentication required",
		Code:    "ADM_UNAUTHORIZED",
	})
}

// actor names who made the request, for the audit trail.
func actor(c *gin.Context) string {
	if sub := c.GetString(subjectKey); sub != "" {
		return sub
	}
	return "admin-user"
}

// signingKeys keeps the keys whose alg is in allowedAlgs.
func signingKeys(_ string, in jwk.Set) (jwk.Set, error) {
	out := jwk.NewSet()
	for i := 0; i < in.Len(); i++ {
		key, ok := in.Key(i)
		if !ok {
			continue
		}
		alg, ok := key.Algorithm().(jwa.SignatureAlgorithm)
		if !ok {
			continue
		}
		if _, allowed := allowedAlgs[alg]; !allowed {
			continue
		}
		if err := out.AddKey(key); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type errSink struct{}

func (errSink) Error(err error) {
	logger.Warn("Admin JWKS refresh failed", logger.String("error", err.Error()))
}
