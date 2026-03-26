// Package telemetry provides OpenTelemetry tracing initialization for Prism services.
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is set, it configures a TracerProvider with an
// OTLP/HTTP exporter and registers it as the global provider. When the env var is
// absent, it returns a no-op shutdown function and leaves the default (no-op) global
// provider in place -- zero overhead.
//
// All other configuration uses standard OTEL env vars:
//
//	OTEL_SERVICE_NAME              — overrides the serviceName argument
//	OTEL_EXPORTER_OTLP_ENDPOINT   — e.g. "http://localhost:4318"
//	OTEL_EXPORTER_OTLP_HEADERS    — extra headers for the exporter
//	OTEL_RESOURCE_ATTRIBUTES       — additional resource attributes
package telemetry

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Init initializes the global OTEL TracerProvider.
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is not set, it returns a no-op shutdown
// function and does not register any provider (zero overhead).
//
// The returned shutdown function flushes pending spans and shuts down the
// exporter. Callers should defer it in main().
func Init(serviceName string, logger *slog.Logger) (shutdown func(context.Context) error) {
	noop := func(context.Context) error { return nil }

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return noop
	}

	ctx := context.Background()

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		logger.Error("failed to create OTLP trace exporter", "error", err)
		return noop
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
		resource.WithFromEnv(),
	)
	if err != nil {
		logger.Error("failed to create OTEL resource", "error", err)
		return noop
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	logger.Info("OpenTelemetry tracing enabled",
		"service", serviceName,
		"endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)

	return tp.Shutdown
}
