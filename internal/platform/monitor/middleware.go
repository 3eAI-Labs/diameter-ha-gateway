// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency distribution",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	diameterRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diameter_requests_total",
			Help: "Total number of Diameter requests processed",
		},
		[]string{"cmd"},
	)

	diameterRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diameter_request_duration_seconds",
			Help:    "Diameter request latency distribution",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"cmd"},
	)
)

// PrometheusMiddleware returns a Gin handler that records request metrics
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()

		c.Next()

		if path == "" {
			path = "unknown"
		}

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method

		// Ignore health check endpoints to avoid noise
		if path != "/health" && path != "/metrics" {
			httpRequestsTotal.WithLabelValues(method, path, status).Inc()
			httpRequestDuration.WithLabelValues(method, path).Observe(duration)
		}
	}
}

// PrometheusHandler returns a Gin handler for the /metrics endpoint
func PrometheusHandler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// IncrementCounter is a helper for diameter metrics
func IncrementCounter(name string, labels map[string]string) {
	if name == "diameter_requests_total" {
		diameterRequestsTotal.WithLabelValues(labels["cmd"]).Inc()
	}
}

// ObserveHistogram is a helper for diameter metrics
func ObserveHistogram(name string, value float64, labels map[string]string) {
	if name == "diameter_request_duration_seconds" {
		diameterRequestDuration.WithLabelValues(labels["cmd"]).Observe(value)
	}
}

// Start runs a dedicated metrics server
func Start(port int) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/metrics", PrometheusHandler())
	return r.Run(":" + strconv.Itoa(port))
}
