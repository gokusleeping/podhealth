# Pod Health Sidecar - Full Code Explanation

This document explains every file in the project:

- what the file does
- how it works
- what each important part is responsible for

---

## 1) `go.mod`

### What it does

Defines the Go module name and dependency graph for the sidecar service.

### How it does it

- Declares module path `pod-health-sidecar`
- Sets Go language version (`1.23.0`)
- Pins external libraries used by runtime:
  - Prometheus client (`client_golang`)
  - OpenTelemetry core + HTTP instrumentation + OTLP exporter
  - Zap logger
  - `x/time/rate` for rate limiting

### Why it matters

All imports across the project resolve through this file. It also ensures reproducible builds.

---

## 2) `cmd/sidecar/main.go`

### What it does

Acts as the composition root: bootstraps config, logging, tracing, metrics, health polling, middleware, routes, HTTP server, and graceful shutdown.

### How it works (flow)

1. Load runtime config via `config.Load()`
2. Build Zap logger using configured log level
3. Create signal-aware context (`SIGINT`, `SIGTERM`)
4. Initialize tracing provider (`tracing.Setup`)
5. Create metrics registry wrapper (`metrics.New`)
6. Create pooled outbound client with header propagation
7. Build and start health aggregator background loop
8. Build middleware chain:
   - metadata propagation
   - rate limiter
   - request observer
9. Register routes:
   - `GET /health`
   - `GET /metrics`
   - `/` proxy fallback
10. Wrap server with OTel handler
11. Start server in goroutine
12. On signal, run graceful shutdown with timeout

### Important sections

- **Config + logger setup**: ensures boot-time parameters are centralized and logs are structured.
- **Context + signal handling**: allows clean termination in Kubernetes.
- **Route wiring**: separates health/metrics from proxy traffic.
- **Middleware order**: request path gets rate limit + metadata + metrics/logging before proxy.
- **Shutdown block**: uses `server.Shutdown` first, then fallback close on failure.

---

## 3) `internal/config/config.go`

### What it does

Parses all runtime settings from environment variables and returns a typed `Config` object used by the rest of the application.

### How it works

- `Config` struct stores all sidecar knobs: listen address, proxy target, health settings, circuit breaker settings, rate limits, tracing config, etc.
- `Load()`:
  - reads env vars with defaults (`getEnv`, `getDuration`, `getInt`, `getFloat`, `getLogLevel`)
  - loads health service list via `loadServices()`
  - validates that at least one health service exists
- `loadServices()` supports two sources:
  - inline JSON in `SIDECAR_HEALTH_SERVICES`
  - JSON file path via `SIDECAR_HEALTH_CONFIG_PATH`

### Important sections

- **`Service` struct**: expected monitor input (`name`, `url`)
- **`Load()` defaults**: safe startup behavior without over-configuring
- **`loadServices()` fallback chain**: env JSON first, file second
- **Helper parsers**: avoid repetitive parsing logic and keep `Load()` readable

---

## 4) `internal/metrics/metrics.go`

### What it does

Defines and exposes Prometheus metrics for HTTP traffic, health check performance/failures, and rate limiting.

### How it works

- `Metrics` struct holds metric handles:
  - `requestCount` (counter vec by path/method/status)
  - `requestLatencyMs` (histogram vec by path/method)
  - `healthFailures` (counter vec by service)
  - `healthLatencyMs` (histogram vec by service)
  - `rateLimitedRequests` (counter)
- `New()` registers all collectors with default registry using `promauto`
- `Handler()` returns standard scrape handler (`promhttp.Handler()`)
- `ObserveRequest`, `ObserveHealth`, `IncRateLimited` are helper methods invoked by middleware/health code

### Important sections

- **Histogram buckets** are tuned for low-latency sidecar behavior (1ms to 1000ms range).
- **Label cardinality** remains controlled (path/method/status/service) to keep metrics cost manageable.

---

## 5) `internal/middleware/metadata.go`

### What it does

Ensures every inbound request has correlation metadata and makes it available to downstream handlers and outbound calls.

### How it works

- Defines canonical headers:
  - `X-Request-ID`
  - `X-Trace-ID`
- Middleware behavior:
  - read incoming headers
  - generate missing IDs with `pkg/id.New()`
  - put values in request context
  - write them back into request headers (for proxy/outbound use)
  - set same headers on response
- Exposes accessors:
  - `RequestIDFromContext`
  - `TraceIDFromContext`

### Important sections

- **Context keys are typed** (`contextKey`) to avoid collisions.
- **Response header propagation** lets callers see IDs even when they were generated server-side.

---

## 6) `internal/middleware/http_client.go`

### What it does

Creates a reusable outbound HTTP client with connection pooling and automatic metadata header propagation.

### How it works

- Wraps base transport with custom `propagationRoundTripper`
- In `RoundTrip`:
  - reads request/trace IDs from context
  - injects headers if missing
  - delegates to base transport
- `NewPropagatingClient(timeout)` configures:
  - dial timeout + keepalive
  - HTTP/2 attempt
  - idle conn pools
  - TLS handshake timeout
  - client-wide timeout

### Important sections

- **Transport reuse** is key for lower CPU/memory overhead compared to creating clients per request.
- **Context-driven propagation** keeps metadata consistent across health checks/proxy requests.

---

## 7) `internal/middleware/ratelimit.go`

### What it does

Protects the sidecar from overload by applying token-bucket rate limiting to incoming requests.

### How it works

- Creates one limiter via `rate.NewLimiter(rps, burst)`
- For each request:
  - `Allow()` true -> request continues
  - `Allow()` false -> increments rate-limit metric and returns `429`

### Important sections

- **Global limiter per sidecar instance** (not per route/user)
- **Metrics integration** gives visibility into throttling events

---

## 8) `internal/middleware/request_observer.go`

### What it does

Captures request completion telemetry: status code, latency, and correlation-aware logs.

### How it works

- Uses `statusRecorder` wrapper to capture response status code
- Middleware flow:
  - start timer
  - run downstream handler
  - observe metric via `ObserveRequest`
  - log structured event with path/method/status/latency/request_id/trace_id

### Important sections

- **Default status 200** ensures accurate metrics if handlers never call `WriteHeader`.
- **Single post-handler observation point** keeps metric and log consistent.

---

## 9) `internal/health/aggregator.go`

### What it does

Implements health-check orchestration: periodic concurrent polling, cached snapshot serving, status aggregation, retries, and simple circuit breaker.

### Data models

- `ServiceStatus`: per-service health result
- `AggregateResponse`: payload returned by `/health`
- `breakerState`: failure count and circuit open-until time
- `Aggregator`: runtime object holding config, logger, metrics, HTTP client, cache, breaker state, and lock

### How it works

- `NewAggregator(...)` initializes breaker map + initial cache (`DOWN`)
- `Start(ctx)`:
  - runs one immediate refresh
  - starts ticker loop (`HealthInterval`) until context canceled
- `Handler()`:
  - reads cached snapshot
  - responds `503` only when aggregate is `DOWN`, otherwise `200`
  - returns JSON payload
- `Snapshot()`:
  - copies cached state under read lock to avoid race/mutation leaks
- `refresh(ctx)`:
  - launches one goroutine per configured service
  - waits for completion
  - computes overall status via `evaluate`
  - atomically updates cache under write lock
- `checkService(...)`:
  - skips request if circuit currently open
  - executes request with per-check timeout
  - retries based on config
  - marks success/failure + records metrics
  - logs warning for final failure
- Circuit methods:
  - `breakerOpen` checks cooldown window
  - `markSuccess` resets breaker state
  - `markFailure` increments failures and opens circuit after threshold

### Important sections

- **Concurrent polling** avoids serial latency accumulation.
- **Snapshot caching** keeps `/health` fast and isolated from live probe jitter.
- **Circuit breaker** prevents repeated expensive probes during failure storms.

---

## 10) `internal/proxy/handler.go`

### What it does

Provides reverse proxy behavior for all non-system routes.

### How it works

- Parses upstream URL from `SIDECAR_PROXY_TARGET`
- Builds `httputil.NewSingleHostReverseProxy`
- Reuses shared transport from common client
- Wraps proxy director to inject request/trace IDs from context into outbound headers
- Defines custom `ErrorHandler`:
  - structured log with request context
  - returns `502 upstream unavailable`

### Important sections

- **Startup validation of proxy target** fails fast for bad config.
- **Director hook** is where metadata propagation into upstream happens.

---

## 11) `internal/tracing/tracing.go`

### What it does

Initializes OpenTelemetry tracing and wraps HTTP server handler for automatic span generation.

### How it works

- Builds service resource with service name `pod-health-sidecar`
- Creates tracer provider options:
  - resource metadata
  - parent-based + probabilistic sampling (`TraceSampleRate`)
- If OTLP endpoint exists:
  - builds HTTP OTLP exporter
  - enables batched export
- Registers provider + tracecontext propagator globally
- Returns shutdown function for clean flush
- `WrapServerHandler` wraps root handler with OTel HTTP instrumentation

### Important sections

- **Optional exporter setup** allows tracing-disabled mode without failing app.
- **Returned shutdown func** is used by `main.go` during termination.

---

## 12) `pkg/id/id.go`

### What it does

Generates compact random IDs used for `X-Request-ID` and `X-Trace-ID` when missing.

### How it works

- Reads 12 cryptographically random bytes
- Hex-encodes to 24-char string
- Has fallback constant if randomness read fails (rare)

### Important sections

- **Crypto-grade randomness** reduces ID collision risk.

---

## 13) `Dockerfile`

### What it does

Builds a production container image for the sidecar using a multi-stage approach.

### How it works

- **Builder stage** (`golang:1.23`):
  - downloads modules
  - compiles static Linux binary with:
    - `CGO_ENABLED=0`
    - stripped symbols (`-s -w`)
    - trimmed paths (`-trimpath`)
- **Runtime stage** (`distroless static nonroot`):
  - copies only compiled binary
  - exposes `8080`
  - runs as non-root

### Important sections

- **Distroless + nonroot** hardens runtime surface.
- **Static binary** simplifies portability and startup.

---

## 14) `.dockerignore`

### What it does

Prevents unnecessary files from being sent to Docker build context.

### How it works

Excludes VCS/editor artifacts and transient build outputs:

- `.git`, `.idea`, `.vscode`
- `bin`, `dist`, `coverage`
- `*.log`

### Why it matters

Smaller build context means faster image builds and less accidental leakage.

---

## 15) `k8s/configmap.yaml`

### What it does

Stores sidecar runtime configuration as environment variables in Kubernetes.

### How it works

- Defines key/value pairs for sidecar behavior (listen/proxy/interval/timeouts/rate/circuit/tracing)
- Embeds `SIDECAR_HEALTH_SERVICES` JSON array for monitored local services

### Important sections

- **Service URLs use `127.0.0.1`** to reach sibling containers in same pod.

---

## 16) `k8s/deployment.yaml`

### What it does

Defines workload deployment showing sidecar pattern (main app + sidecar in one pod).

### How it works

- Creates deployment with 2 replicas
- Pod has 2 containers:
  - `main-app` on `8081`
  - `pod-health-sidecar` on `8080`
- Sidecar loads env from `pod-health-sidecar-config` ConfigMap
- Sidecar has resource requests/limits and liveness/readiness probes on `/health`

### Important sections

- **Shared pod networking** is implied by Kubernetes: sidecar reaches app via localhost.
- **Resource limits** protect cluster and help keep memory target in check.

---

## 17) `k8s/service.yaml`

### What it does

Exposes the sidecar over a Kubernetes Service.

### How it works

- Selects pods labeled `app: app-with-health-sidecar`
- Maps service port `80` to pod target port `8080` (sidecar)

### Why it matters

External/internal callers can hit sidecar endpoints (`/health`, proxy routes) consistently.

---

## 18) `README.md`

### What it does

Primary project documentation for setup, architecture, endpoints, configuration, Docker, Kubernetes usage, and performance positioning.

### How it works

- Introduces goals and architecture
- Lists features and project structure
- Provides local run instructions and endpoint contracts
- Documents environment variables
- Includes Docker build/run snippets
- Includes Kubernetes apply commands
- Shares memory target narrative vs Spring baseline

### Important sections

- **Quick start** gets developers running quickly.
- **Performance notes** provide practical validation guidance (`kubectl top`).

---

## 19) `ARCHITECTURE.md`

### What it does

Contains architecture narrative tailored for interviews/resume discussions:

- problem statement
- objective
- design decisions
- trade-offs
- scaling behavior
- risks/mitigations
- text HLD diagram

### How it works

Serves as a concise architectural talking guide complementary to `README.md`.

### Why it matters

Useful when presenting this project to reviewers, interviewers, or stakeholders.

---

## End-to-End Runtime Summary

At runtime, the service behaves like this:

1. Boot with env config and initialize logger/tracing/metrics
2. Start periodic background health polling over pod-local endpoints
3. Serve:
   - `GET /health` from cached aggregate state
   - `GET /metrics` for Prometheus
   - all other paths proxied to main app
4. For each request, enforce metadata + rate limiting + telemetry
5. On shutdown signal, gracefully stop and flush telemetry

This design keeps the sidecar lightweight while still production-ready for observability and reliability.
