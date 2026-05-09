package tracing

import (
	"context"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/zap"

	"pod-health-sidecar/internal/config"
)

func Setup(ctx context.Context, cfg config.Config, logger *zap.Logger) (func(context.Context) error, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("pod-health-sidecar"),
	))
	if err != nil {
		return nil, err
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.TraceSampleRate))),
	}

	if cfg.OTLPEndpoint != "" {
		exporter, exporterErr := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint))
		if exporterErr != nil {
			return nil, exporterErr
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
		logger.Info("otlp exporter configured", zap.String("endpoint", cfg.OTLPEndpoint))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp.Shutdown, nil
}

func WrapServerHandler(next http.Handler, name string) http.Handler {
	return otelhttp.NewHandler(next, name)
}
