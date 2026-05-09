package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"pod-health-sidecar/internal/config"
	"pod-health-sidecar/internal/metrics"
)

type ServiceStatus struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

type AggregateResponse struct {
	Status   string          `json:"status"`
	Services []ServiceStatus `json:"services"`
}

type breakerState struct {
	failures  int
	openUntil time.Time
}

type Aggregator struct {
	cfg    config.Config
	logger *zap.Logger
	m      *metrics.Metrics
	client *http.Client

	mu      sync.RWMutex
	cache   AggregateResponse
	breaker map[string]breakerState
}

func NewAggregator(cfg config.Config, logger *zap.Logger, m *metrics.Metrics, client *http.Client) *Aggregator {
	return &Aggregator{
		cfg:     cfg,
		logger:  logger,
		m:       m,
		client:  client,
		breaker: make(map[string]breakerState, len(cfg.HealthServices)),
		cache: AggregateResponse{
			Status:   "DOWN",
			Services: make([]ServiceStatus, 0, len(cfg.HealthServices)),
		},
	}
}

func (a *Aggregator) Start(ctx context.Context) {
	a.refresh(ctx)
	go func() {
		ticker := time.NewTicker(a.cfg.HealthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.refresh(ctx)
			}
		}
	}()
}

func (a *Aggregator) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot := a.Snapshot()
		statusCode := http.StatusOK
		if snapshot.Status == "DOWN" {
			statusCode = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}

func (a *Aggregator) Snapshot() AggregateResponse {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := AggregateResponse{
		Status:   a.cache.Status,
		Services: make([]ServiceStatus, len(a.cache.Services)),
	}
	copy(out.Services, a.cache.Services)
	return out
}

func (a *Aggregator) refresh(ctx context.Context) {
	wg := sync.WaitGroup{}
	results := make([]ServiceStatus, len(a.cfg.HealthServices))
	for i, svc := range a.cfg.HealthServices {
		wg.Add(1)
		go func(idx int, service config.Service) {
			defer wg.Done()
			results[idx] = a.checkService(ctx, service)
		}(i, svc)
	}
	wg.Wait()

	overall := evaluate(results)
	a.mu.Lock()
	a.cache = AggregateResponse{
		Status:   overall,
		Services: results,
	}
	a.mu.Unlock()
}

func (a *Aggregator) checkService(ctx context.Context, service config.Service) ServiceStatus {
	if a.breakerOpen(service.Name) {
		return ServiceStatus{
			Name:   service.Name,
			Status: "DOWN",
			Error:  "circuit open",
		}
	}

	var lastErr error
	for attempt := 0; attempt <= a.cfg.HealthRetries; attempt++ {
		begin := time.Now()
		reqCtx, cancel := context.WithTimeout(ctx, a.cfg.HealthTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, service.URL, nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}

		resp, err := a.client.Do(req)
		latency := time.Since(begin)
		cancel()

		if err == nil {
			_ = resp.Body.Close()
		}
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			a.m.ObserveHealth(service.Name, latency, true)
			a.markSuccess(service.Name)
			return ServiceStatus{
				Name:      service.Name,
				Status:    "UP",
				LatencyMS: latency.Milliseconds(),
			}
		}

		if err == nil {
			lastErr = errors.New(resp.Status)
		} else {
			lastErr = err
		}
		a.m.ObserveHealth(service.Name, latency, false)
	}

	a.markFailure(service.Name)
	a.logger.Warn("health check failed", zap.String("service", service.Name), zap.Error(lastErr))
	return ServiceStatus{
		Name:   service.Name,
		Status: "DOWN",
		Error:  lastErr.Error(),
	}
}

func evaluate(items []ServiceStatus) string {
	if len(items) == 0 {
		return "DOWN"
	}
	up := 0
	for _, s := range items {
		if s.Status == "UP" {
			up++
		}
	}
	if up == len(items) {
		return "UP"
	}
	if up == 0 {
		return "DOWN"
	}
	return "DEGRADED"
}

func (a *Aggregator) breakerOpen(service string) bool {
	a.mu.RLock()
	state, ok := a.breaker[service]
	a.mu.RUnlock()
	if !ok {
		return false
	}
	return time.Now().Before(state.openUntil)
}

func (a *Aggregator) markSuccess(service string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.breaker[service] = breakerState{}
}

func (a *Aggregator) markFailure(service string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.breaker[service]
	state.failures++
	if state.failures >= a.cfg.CBFailThreshold {
		state.openUntil = time.Now().Add(a.cfg.CBCooldown)
	}
	a.breaker[service] = state
}
