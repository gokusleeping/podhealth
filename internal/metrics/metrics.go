package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requestCount        *prometheus.CounterVec
	requestLatencyMs    *prometheus.HistogramVec
	healthFailures      *prometheus.CounterVec
	healthLatencyMs     *prometheus.HistogramVec
	rateLimitedRequests prometheus.Counter
}

func New() *Metrics {
	return &Metrics{
		requestCount: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "sidecar_http_requests_total",
			Help: "Total number of incoming HTTP requests.",
		}, []string{"path", "method", "status"}),
		requestLatencyMs: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sidecar_http_request_latency_ms",
			Help:    "HTTP request latency in milliseconds.",
			Buckets: []float64{1, 3, 5, 10, 20, 50, 100, 250, 500, 1000},
		}, []string{"path", "method"}),
		healthFailures: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "sidecar_health_check_failures_total",
			Help: "Total health check failures per service.",
		}, []string{"service"}),
		healthLatencyMs: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sidecar_health_check_latency_ms",
			Help:    "Health check latency in milliseconds.",
			Buckets: []float64{1, 3, 5, 10, 20, 50, 100, 250, 500, 1000},
		}, []string{"service"}),
		rateLimitedRequests: promauto.NewCounter(prometheus.CounterOpts{
			Name: "sidecar_rate_limited_requests_total",
			Help: "Total requests rejected by rate limiter.",
		}),
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.Handler()
}

func (m *Metrics) ObserveRequest(path, method string, status int, start time.Time) {
	m.requestCount.WithLabelValues(path, method, strconv.Itoa(status)).Inc()
	m.requestLatencyMs.WithLabelValues(path, method).Observe(float64(time.Since(start).Milliseconds()))
}

func (m *Metrics) ObserveHealth(service string, latency time.Duration, healthy bool) {
	m.healthLatencyMs.WithLabelValues(service).Observe(float64(latency.Milliseconds()))
	if !healthy {
		m.healthFailures.WithLabelValues(service).Inc()
	}
}

func (m *Metrics) IncRateLimited() {
	m.rateLimitedRequests.Inc()
}
