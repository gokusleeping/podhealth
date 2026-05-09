package middleware

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"pod-health-sidecar/pkg/id"
)

const (
	HeaderRequestID = "X-Request-ID"
	HeaderTraceID   = "X-Trace-ID"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	traceIDKey   contextKey = "trace_id"
)

type Metadata struct {
	logger *zap.Logger
}

func NewMetadata(logger *zap.Logger) func(http.Handler) http.Handler {
	m := &Metadata{logger: logger}
	return m.Middleware
}

func (m *Metadata) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(HeaderRequestID)
		if reqID == "" {
			reqID = id.New()
		}
		traceID := r.Header.Get(HeaderTraceID)
		if traceID == "" {
			traceID = id.New()
		}

		ctx := context.WithValue(r.Context(), requestIDKey, reqID)
		ctx = context.WithValue(ctx, traceIDKey, traceID)

		r = r.WithContext(ctx)
		r.Header.Set(HeaderRequestID, reqID)
		r.Header.Set(HeaderTraceID, traceID)
		w.Header().Set(HeaderRequestID, reqID)
		w.Header().Set(HeaderTraceID, traceID)

		next.ServeHTTP(w, r)
	})
}

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

func TraceIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}
