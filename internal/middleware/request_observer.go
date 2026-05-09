package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"pod-health-sidecar/internal/metrics"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(status int) {
	sr.status = status
	sr.ResponseWriter.WriteHeader(status)
}

func NewRequestObserver(logger *zap.Logger, m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			path := r.URL.Path
			m.ObserveRequest(path, r.Method, rec.status, start)
			logger.Info("request completed",
				zap.String("path", path),
				zap.String("method", r.Method),
				zap.Int("status", rec.status),
				zap.Int64("latency_ms", time.Since(start).Milliseconds()),
				zap.String("request_id", RequestIDFromContext(r.Context())),
				zap.String("trace_id", TraceIDFromContext(r.Context())),
			)
		})
	}
}
