package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"pod-health-sidecar/internal/config"
	"pod-health-sidecar/internal/health"
	"pod-health-sidecar/internal/metrics"
	"pod-health-sidecar/internal/middleware"
	"pod-health-sidecar/internal/proxy"
	"pod-health-sidecar/internal/tracing"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	loggerCfg := zap.NewProductionConfig()
	loggerCfg.Level = cfg.LogLevel
	logger, err := loggerCfg.Build()
	if err != nil {
		panic(err)
	}
	defer func() { _ = logger.Sync() }()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	shutdownTracing, err := tracing.Setup(ctx, cfg, logger)
	if err != nil {
		logger.Fatal("failed to configure tracing", zap.Error(err))
	}
	defer func() {
		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer timeoutCancel()
		_ = shutdownTracing(timeoutCtx)
	}()

	m := metrics.New()
	httpClient := middleware.NewPropagatingClient(cfg.OutboundTimeout)
	aggregator := health.NewAggregator(cfg, logger, m, httpClient)
	aggregator.Start(ctx)

	metaMW := middleware.NewMetadata(logger)
	rateMW := middleware.NewRateLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst, m)
	reqMW := middleware.NewRequestObserver(logger, m)

	mux := http.NewServeMux()
	mux.Handle("GET /health", aggregator.Handler())
	mux.Handle("GET /metrics", m.Handler())
	mux.Handle("/", proxy.NewHandler(cfg, logger, m, httpClient))

	handler := reqMW(metaMW(rateMW(mux)))
	handler = tracing.WrapServerHandler(handler, "sidecar-http")

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("sidecar server starting",
			zap.String("listen_addr", cfg.ListenAddr),
			zap.String("proxy_target", cfg.ProxyTarget),
			zap.Int("health_services", len(cfg.HealthServices)),
		)
		if serveErr := server.ListenAndServe(); !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Fatal("http server crashed", zap.Error(serveErr))
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", zap.Error(err))
		_ = server.Close()
		os.Exit(1)
	}
	logger.Info("sidecar stopped")
}
