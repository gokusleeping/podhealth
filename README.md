# Pod Health Sidecar (Go)

Production-grade Kubernetes sidecar that aggregates health checks from co-located containers, exposes a unified endpoint, and proxies traffic with request metadata propagation.

## Architecture

```text
                 +--------------------+
                 |   Client / Ingress |
                 +---------+----------+
                           |
                           v
   =========================================================
   |                 Kubernetes Pod (shared net)           |
   |                                                       |
   |   +---------------- Go Sidecar :8080 ---------------+ |
   |   |  Reverse Proxy                                  | |
   |   |  Health Aggregator                              | |
   |   |  Metadata Middleware                            | |
   |   |  Metrics + Tracing                              | |
   |   +-----+----------------------+--------------------+ |
   |         |                      |                      |
   |         | proxy traffic        | health polls         |
   |         v                      v                      |
   |   +-----------+          +-----------+                |
   |   | Main App  |          |  Worker   |                |
   |   |   :8081   |          |   :8082   |                |
   |   +-----------+          +-----------+                |
   =========================================================
             |                           |
             | /metrics                  | traces
             v                           v
      +-------------+            +------------------+
      | Prometheus  |            | OTel Collector   |
      +-------------+            +------------------+
Outputs:
- /health  (aggregated state)
- /metrics (Prometheus)
```

## Why this design

- Replaces a heavyweight Java health service with a low-overhead Go runtime.
- Uses shared pod network namespace (`localhost`) for sidecar-to-container calls.
- Keeps response path hot: in-memory cached health snapshots and pooled HTTP connections.
- Clean package boundaries allow extensibility (additional checks, alternative proxy behavior).

## Features

- Concurrent health aggregation with retries and per-service timeout
- Lightweight circuit breaker for unstable dependencies
- Unified `GET /health`:
  - `UP` when all checks pass
  - `DEGRADED` when partial failures
  - `DOWN` when all monitored services fail
- Proxy mode with automatic `X-Request-ID` and `X-Trace-ID` propagation
- Structured logging using Zap with correlation IDs
- Prometheus metrics: request counts, request latency, health failures
- OpenTelemetry setup with optional OTLP exporter
- Graceful shutdown and rate limiting middleware

## Project structure

```text
cmd/
  sidecar/
internal/
  config/
  health/
  middleware/
  metrics/
  proxy/
  tracing/
pkg/
  id/
k8s/
Dockerfile
```

## Quick start

### 1) Configure monitored services

```powershell
$env:SIDECAR_HEALTH_SERVICES='[
  {"name":"service-a","url":"http://127.0.0.1:8081/health"},
  {"name":"service-b","url":"http://127.0.0.1:8082/health"}
]'
```

### 2) Run locally

```bash
go mod tidy
go run ./cmd/sidecar
```

### 3) Endpoints

- `GET /health` aggregated health JSON
- `GET /metrics` Prometheus metrics
- `/*` reverse proxy to `SIDECAR_PROXY_TARGET`

## Health payload

```json
{
  "status": "DEGRADED",
  "services": [
    { "name": "service-a", "status": "UP", "latency_ms": 12 },
    { "name": "service-b", "status": "DOWN", "error": "timeout" }
  ]
}
```

## Key environment variables

- `SIDECAR_LISTEN_ADDR` (default `:8080`)
- `SIDECAR_PROXY_TARGET` (default `http://127.0.0.1:8081`)
- `SIDECAR_HEALTH_SERVICES` (JSON array)
- `SIDECAR_HEALTH_CONFIG_PATH` (optional file path instead of inline JSON)
- `SIDECAR_HEALTH_INTERVAL` (default `5s`)
- `SIDECAR_HEALTH_TIMEOUT` (default `1200ms`)
- `SIDECAR_HEALTH_RETRIES` (default `1`)
- `SIDECAR_CB_FAIL_THRESHOLD` (default `3`)
- `SIDECAR_CB_COOLDOWN` (default `15s`)
- `SIDECAR_RATE_LIMIT_RPS` (default `250`)
- `SIDECAR_RATE_LIMIT_BURST` (default `500`)
- `OTEL_EXPORTER_OTLP_ENDPOINT` (optional)

## Docker

```bash
docker build -t pod-health-sidecar:latest .
docker run --rm -p 8080:8080 `
  -e SIDECAR_PROXY_TARGET=http://host.docker.internal:8081 `
  -e SIDECAR_HEALTH_SERVICES='[{"name":"app","url":"http://host.docker.internal:8081/health"}]' `
  pod-health-sidecar:latest
```

## Kubernetes usage

Apply:

```bash
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
```

`k8s/deployment.yaml` demonstrates sidecar pattern:

- Main app listens on `8081`
- Sidecar listens on `8080`
- Sidecar probes app endpoints over `127.0.0.1` in the same pod network namespace

## Performance notes

- Expected steady-state RSS for this design is typically in tens of MB in common workloads.
- Resource limits in the sample manifest are set to keep usage under a 64Mi envelope.
- To validate in your cluster:
  - `kubectl top pod <pod-name>`
  - profile with `pprof` (optional if you expose it)

### Spring Boot comparison (typical baseline)

| Service Type               | Typical Idle Memory |
| -------------------------- | ------------------- |
| Spring Boot health service | 150MB-350MB         |
| This Go sidecar            | 20MB-50MB target    |

Values depend on workload, probes, and telemetry exporters.
