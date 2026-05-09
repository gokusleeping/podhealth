package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"go.uber.org/zap"

	"pod-health-sidecar/internal/config"
	"pod-health-sidecar/internal/middleware"
	"pod-health-sidecar/internal/metrics"
)

func NewHandler(cfg config.Config, logger *zap.Logger, _ *metrics.Metrics, client *http.Client) http.Handler {
	target, err := url.Parse(cfg.ProxyTarget)
	if err != nil {
		logger.Fatal("invalid SIDECAR_PROXY_TARGET", zap.String("target", cfg.ProxyTarget), zap.Error(err))
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = client.Transport
	original := rp.Director
	rp.Director = func(req *http.Request) {
		original(req)
		if rid := middleware.RequestIDFromContext(req.Context()); rid != "" {
			req.Header.Set(middleware.HeaderRequestID, rid)
		}
		if tid := middleware.TraceIDFromContext(req.Context()); tid != "" {
			req.Header.Set(middleware.HeaderTraceID, tid)
		}
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("proxy request failed",
			zap.Error(err),
			zap.String("path", r.URL.Path),
			zap.String("request_id", middleware.RequestIDFromContext(r.Context())),
		)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	return rp
}
