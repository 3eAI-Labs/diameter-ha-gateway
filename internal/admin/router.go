// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"errors"
	"net/http"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/audit"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/gin-gonic/gin"
)

type StandardResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Code    string      `json:"code"`
	Data    interface{} `json:"data"`
}

// Router bundles the dependencies the admin endpoints need. It is exported as
// a struct so we can wire optional dependencies (config reloader, future auth
// middleware) without churning the constructor signature.
type Router struct {
	PeerManager *diameter.PeerManager
	Reloader    *config.Reloader
	// Auth, when set, guards every /api/admin/v1 endpoint.
	Auth *Authenticator
}

// NewRouter constructs the admin Gin engine. Backward-compatible with prior
// signature (PeerManager-only) by retaining the original NewRouter; new
// caller path is NewRouterWith.
func NewRouter(pm *diameter.PeerManager) *gin.Engine {
	return (&Router{PeerManager: pm}).Build()
}

// NewRouterWith is the recommended constructor — wires both the peer manager
// and the config reloader so /config/reload performs the real work.
func NewRouterWith(pm *diameter.PeerManager, reloader *config.Reloader) *gin.Engine {
	return (&Router{PeerManager: pm, Reloader: reloader}).Build()
}

// Build assembles the Gin engine with static frontend, peer endpoints, and
// the (now-real) config reload endpoint.
func (r *Router) Build() *gin.Engine {
	g := gin.New()
	g.Use(gin.Recovery())
	g.Use(monitor.PrometheusMiddleware())

	// Static SPA assets.
	g.Static("/assets", "./web/dist/assets")
	g.LoadHTMLFiles("./web/dist/index.html")
	g.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})
	g.NoRoute(func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	api := g.Group("/api/admin/v1")
	if r.Auth != nil {
		api.Use(r.Auth.Middleware())
	}
	{
		api.GET("/peers", r.listPeers)
		api.POST("/config/reload", r.reloadConfig)
	}

	return g
}

// listPeers returns the active peer connections.
func (r *Router) listPeers(c *gin.Context) {
	peers := r.PeerManager.GetActiveConnections()
	peerData := make([]map[string]string, 0, len(peers))
	for _, p := range peers {
		peerData = append(peerData, map[string]string{
			"host":   p,
			"status": "Connected",
		})
	}

	c.JSON(http.StatusOK, StandardResponse{
		Success: true,
		Message: "Active peers retrieved",
		Code:    "ADM_PEERS_LISTED",
		Data:    gin.H{"peers": peerData},
	})
}

// reloadConfig re-reads the config from disk via Viper. Non-reloadable
// field changes return 409 with a human-readable list so operators can
// decide whether to schedule a restart.
func (r *Router) reloadConfig(c *gin.Context) {
	if r.Reloader == nil {
		c.JSON(http.StatusServiceUnavailable, StandardResponse{
			Success: false,
			Message: "Config reload not wired (legacy router constructor used)",
			Code:    "ADM_RELOAD_UNAVAILABLE",
		})
		return
	}

	audit.Log(c.Request.Context(), actor(c), "CONFIG_RELOAD_ATTEMPT", "gateway-config", "global", nil)

	newCfg, err := r.Reloader.Reload()
	if err != nil {
		audit.Log(c.Request.Context(), actor(c), "CONFIG_RELOAD_FAIL", "gateway-config", "global",
			map[string]any{"error": err.Error()})

		status := http.StatusInternalServerError
		code := "ADM_CONFIG_RELOAD_FAIL"
		if errors.Is(err, config.ErrNonReloadableField) {
			status = http.StatusConflict
			code = "ADM_CONFIG_RESTART_REQUIRED"
		}

		c.JSON(status, StandardResponse{
			Success: false,
			Message: err.Error(),
			Code:    code,
		})
		return
	}

	audit.Log(c.Request.Context(), actor(c), "CONFIG_RELOAD_OK", "gateway-config", "global",
		map[string]any{
			"peers_count": len(newCfg.Peers),
			"app_env":     newCfg.AppEnv,
		})

	c.JSON(http.StatusOK, StandardResponse{
		Success: true,
		Message: "Configuration reloaded successfully",
		Code:    "ADM_CONFIG_RELOADED",
		Data: gin.H{
			"peers_count": len(newCfg.Peers),
			"app_env":     newCfg.AppEnv,
		},
	})
}
