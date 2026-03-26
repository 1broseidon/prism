// Package metrics provides Prometheus metrics for Prism.
//
// Call Init() to register metrics with the default Prometheus registry.
// When not initialized, all recording functions are safe no-ops.
package metrics

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var enabled atomic.Bool

// Pre-defined metrics, registered on Init().
var (
	ToolCallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prism_tool_calls_total",
			Help: "Total number of tool calls routed through the gateway.",
		},
		[]string{"namespace", "tool", "backend", "allowed"},
	)

	ToolCallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "prism_tool_call_duration_seconds",
			Help:    "Duration of tool calls in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "tool", "backend"},
	)

	AuthValidationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prism_auth_validations_total",
			Help: "Total number of auth token validations.",
		},
		[]string{"result"},
	)

	ScopeDenialsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "prism_scope_denials_total",
			Help: "Total number of scope-based access denials.",
		},
		[]string{"namespace", "tool"},
	)

	ActiveBackends = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "prism_active_backends",
			Help: "Number of currently connected backends.",
		},
	)
)

// Init registers all metrics with the default Prometheus registry.
// Must be called once at startup before serving requests.
func Init() {
	prometheus.MustRegister(
		ToolCallsTotal,
		ToolCallDuration,
		AuthValidationsTotal,
		ScopeDenialsTotal,
		ActiveBackends,
	)
	enabled.Store(true)
}

// Enabled reports whether metrics have been initialized.
func Enabled() bool {
	return enabled.Load()
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordToolCall records a tool call metric. No-op when not initialized.
func RecordToolCall(namespace, tool, backend string, allowed bool, duration time.Duration) {
	if !enabled.Load() {
		return
	}
	allowedStr := "true"
	if !allowed {
		allowedStr = "false"
	}
	ToolCallsTotal.WithLabelValues(namespace, tool, backend, allowedStr).Inc()
	if allowed {
		ToolCallDuration.WithLabelValues(namespace, tool, backend).Observe(duration.Seconds())
	}
}

// RecordScopeDenial records a scope denial. No-op when not initialized.
func RecordScopeDenial(namespace, tool string) {
	if !enabled.Load() {
		return
	}
	ScopeDenialsTotal.WithLabelValues(namespace, tool).Inc()
}

// RecordAuthValidation records an auth validation result. No-op when not initialized.
func RecordAuthValidation(result string) {
	if !enabled.Load() {
		return
	}
	AuthValidationsTotal.WithLabelValues(result).Inc()
}

// IncActiveBackends increments the active backends gauge. No-op when not initialized.
func IncActiveBackends() {
	if !enabled.Load() {
		return
	}
	ActiveBackends.Inc()
}

// DecActiveBackends decrements the active backends gauge. No-op when not initialized.
func DecActiveBackends() {
	if !enabled.Load() {
		return
	}
	ActiveBackends.Dec()
}
