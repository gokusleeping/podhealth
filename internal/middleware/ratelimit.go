package middleware

import (
	"net/http"

	"golang.org/x/time/rate"

	"pod-health-sidecar/internal/metrics"
)

func NewRateLimiter(rps, burst int, m *metrics.Metrics) func(http.Handler) http.Handler {
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter.Allow() {
				next.ServeHTTP(w, r)
				return
			}
			if m != nil {
				m.IncRateLimited() 
			}
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		})
	}
}
