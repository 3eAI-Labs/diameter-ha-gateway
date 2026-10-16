// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package alarm

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Standard Alarm Levels
const (
	SeverityInfo     = "INFO"
	SeverityWarning  = "WARNING"
	SeverityCritical = "CRITICAL"
)

var (
	// Prometheus metric to track alarms
	alarmCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "telco_platform_alarms_total",
			Help: "Total number of alarms raised by the service",
		},
		[]string{"service", "severity", "code"},
	)
	serviceName string
)

// Init initializes the alarm module with the service name
func Init(name string) {
	serviceName = name
}

// Raise increments the alarm metric.
// AlertManager will pick this up based on rules (e.g., rate > 0).
// It is called from many goroutines, so it only reads serviceName, which
// Init sets once at start-up.
func Raise(severity, code, message string) {
	name := serviceName
	if name == "" {
		name = "unknown-service"
	}
	alarmCounter.WithLabelValues(name, severity, code).Inc()
}
