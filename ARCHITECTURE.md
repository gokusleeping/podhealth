# Pod Health Sidecar Architecture

## Interview-Ready One-Pager

### Problem

The legacy pod health service built in Spring Boot had high memory overhead and startup latency for a narrow responsibility: health aggregation, metadata propagation, and proxying.

### Objective

Replace it with a lightweight Go sidecar that:

- Aggregates health from multiple containers in the same pod
- Exposes one `GET /health` endpoint
- Propagates request and trace metadata
- Provides strong observability and operational safety
- Targets a 20-50MB RSS profile

### Solution

Implemented a Go sidecar using `net/http` with modular internals:

- **Health Aggregator**: concurrent polling and in-memory cache
- **Reverse Proxy**: traffic forwarding to main app container
- **Metadata Middleware**: ensures `X-Request-ID` and `X-Trace-ID` exist and propagate
- **Observability**: Prometheus metrics, Zap structured logs, and OpenTelemetry tracing
- **Resilience**: timeout, retries, circuit breaker, rate limiter, and graceful shutdown

### Why this architecture works

- Pod-local networking (`127.0.0.1`) keeps calls fast and simple
- Cached health snapshots avoid fan-out on every `/health` call
- Shared pooled HTTP client minimizes connection and allocation overhead
- Package-level separation (`config`, `health`, `proxy`, `middleware`, `metrics`, `tracing`) keeps code testable and maintainable

### Key trade-offs

**Pros**

- Lower memory and faster startup than JVM baseline
- Operationally simple (single binary, distroless image)
- Easy drop-in sidecar pattern for Kubernetes

**Cons**

- Fewer batteries-included defaults than a Spring stack
- Requires explicit resilience and middleware policies (implemented here)

### Scaling behavior

- Horizontal scaling through pod replicas
- Per-instance health polling complexity: `O(number_of_monitored_services)`
- Rate limiter protects sidecar under bursts
- Circuit breaker prevents repeated calls to failing dependencies

### Risks and mitigations

- **False health negatives** -> configurable timeout/retry/interval
- **Upstream instability** -> breaker plus proxy error handling
- **Observability overhead** -> configurable trace sample rate
- **Resource drift** -> K8s requests/limits plus runtime checks (`kubectl top`)

### Resume impact line

Built a production-grade Go Kubernetes sidecar replacing a Spring Boot health service, delivering unified pod health, request metadata propagation, and observability with a low-memory footprint target (20-50MB), while adding resilience controls (retry/timeout/circuit breaker/rate limiting) and a clean modular architecture.

---

## High-Level Design (HLD Diagram - Text)

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

Unified /health = aggregate(Main App /health, Worker /health) => UP/DEGRADED/DOWN
```
