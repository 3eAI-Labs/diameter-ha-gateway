// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIssuer   = "https://idp.example/realms/telco"
	testAudience = "diameter-gateway-admin"
)

// signingKey is an RSA key with a kid and alg, as an identity provider
// publishes it.
func signingKey(t *testing.T, kid string) jwk.Key {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	k, err := jwk.FromRaw(raw)
	require.NoError(t, err)
	require.NoError(t, k.Set(jwk.KeyIDKey, kid))
	require.NoError(t, k.Set(jwk.AlgorithmKey, jwa.RS256))
	return k
}

// serveJWKS publishes the public halves of keys, and the raw entries given,
// as a JWKS document.
func serveJWKS(t *testing.T, keys []jwk.Key, raw ...map[string]string) string {
	t.Helper()
	set := jwk.NewSet()
	for _, k := range keys {
		pub, err := k.PublicKey()
		require.NoError(t, err)
		require.NoError(t, set.AddKey(pub))
	}
	doc, err := json.Marshal(set)
	require.NoError(t, err)
	var out struct {
		Keys []any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(doc, &out))
	for _, r := range raw {
		out.Keys = append(out.Keys, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type claims struct {
	issuer, audience string
	expires          time.Duration // 0: no exp claim
}

var valid = claims{issuer: testIssuer, audience: testAudience, expires: time.Minute}

func token(t *testing.T, alg jwa.SignatureAlgorithm, key any, c claims) string {
	t.Helper()
	b := jwt.NewBuilder().Issuer(c.issuer).Audience([]string{c.audience}).Subject("operator@example")
	if c.expires != 0 {
		b = b.Expiration(time.Now().Add(c.expires))
	}
	tok, err := b.Build()
	require.NoError(t, err)
	signed, err := jwt.Sign(tok, jwt.WithKey(alg, key))
	require.NoError(t, err)
	return string(signed)
}

// guarded is an admin API group behind the authenticator; the handler echoes
// the subject the middleware recorded.
func guarded(t *testing.T, jwksURL string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	a, err := NewAuthenticator(context.Background(),
		config.AuthConfig{Enabled: true, JWKSURL: jwksURL, Issuer: testIssuer, Audience: testAudience})
	require.NoError(t, err)
	g := gin.New()
	api := g.Group("/api/admin/v1", a.Middleware())
	api.GET("/peers", func(c *gin.Context) { c.String(http.StatusOK, actor(c)) })
	return g
}

func get(g *gin.Engine, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/v1/peers", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	return w
}

func TestAuth_ValidTokenPasses(t *testing.T) {
	key := signingKey(t, "k1")
	g := guarded(t, serveJWKS(t, []jwk.Key{key}))

	w := get(g, token(t, jwa.RS256, key, valid))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "operator@example", w.Body.String(), "audit actor is the token subject")
}

func TestAuth_Rejected(t *testing.T) {
	key := signingKey(t, "k1")
	impostor := signingKey(t, "k1") // same kid, key not in the JWKS
	hsSecret := []byte("0123456789abcdef0123456789abcdef")
	g := guarded(t, serveJWKS(t, []jwk.Key{key}, map[string]string{
		// A symmetric key in the set must never verify anything.
		"kty": "oct", "kid": "hs", "alg": "HS256", "k": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY",
	}))
	hsKey, err := jwk.FromRaw(hsSecret)
	require.NoError(t, err)
	require.NoError(t, hsKey.Set(jwk.KeyIDKey, "hs"))

	cases := map[string]string{
		"no token":         "",
		"not a JWT":        "not-a-token",
		"unknown signer":   token(t, jwa.RS256, impostor, valid),
		"wrong issuer":     token(t, jwa.RS256, key, claims{issuer: "https://other.example", audience: testAudience, expires: time.Minute}),
		"wrong audience":   token(t, jwa.RS256, key, claims{issuer: testIssuer, audience: "another-service", expires: time.Minute}),
		"expired":          token(t, jwa.RS256, key, claims{issuer: testIssuer, audience: testAudience, expires: -time.Hour}),
		"no exp":           token(t, jwa.RS256, key, claims{issuer: testIssuer, audience: testAudience}),
		"HS256 key in set": token(t, jwa.HS256, hsKey, valid),
	}
	for name, bearer := range cases {
		t.Run(name, func(t *testing.T) {
			w := get(g, bearer)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, `Bearer realm="admin"`, w.Header().Get("WWW-Authenticate"))
			assert.NotContains(t, w.Body.String(), "operator@example")
		})
	}
}

// TestAuth_UnreachableJWKSFailsClosed: when the key set cannot be fetched the
// gateway still starts, and rejects every request until it can.
func TestAuth_UnreachableJWKSFailsClosed(t *testing.T) {
	key := signingKey(t, "k1")
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	g := guarded(t, srv.URL)
	assert.Equal(t, http.StatusUnauthorized, get(g, token(t, jwa.RS256, key, valid)).Code)
}

func TestNewAuthenticator_RequiresIssuerAndAudience(t *testing.T) {
	for _, cfg := range []config.AuthConfig{
		{Enabled: true, Issuer: testIssuer, Audience: testAudience},
		{Enabled: true, JWKSURL: "https://idp.example/certs", Audience: testAudience},
		{Enabled: true, JWKSURL: "https://idp.example/certs", Issuer: testIssuer},
	} {
		_, err := NewAuthenticator(context.Background(), cfg)
		assert.Error(t, err, "%+v", cfg)
	}
}
