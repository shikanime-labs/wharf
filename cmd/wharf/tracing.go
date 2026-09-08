// OpenTelemetry tracing for the wharf build pipeline.
//
// The OTLP/gRPC exporter reads its endpoint and TLS mode from the standard
// OTEL_EXPORTER_OTLP_* environment variables (TRACES-specific take precedence).
// With no endpoint set it defaults to https://localhost:4317 and export
// failures are logged rather than fatal. Exporter init errors are logged and
// never fail a build.
package main

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation scope for spans created here.
const tracerName = "github.com/shikanime-studio/wharf"

// version mirrors the Nix flake package version (flake.nix packages.default.version).
const version = "v0.1.0"

// setupTracing installs a process-wide TracerProvider that exports spans over
// OTLP/gRPC. It returns a shutdown function that flushes and stops the provider;
// call it on process exit.
func setupTracing(ctx context.Context) func(context.Context) error {
	exporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		slog.WarnContext(ctx, "opentelemetry exporter init failed; tracing disabled", "err", err)
		return func(context.Context) error { return nil }
	}

	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceName("wharf"),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		slog.WarnContext(ctx, "opentelemetry resource init failed; tracing disabled", "err", err)
		_ = exporter.Shutdown(ctx)
		return func(context.Context) error { return nil }
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return func(ctx context.Context) error {
		if err := tp.Shutdown(ctx); err != nil {
			slog.WarnContext(ctx, "opentelemetry shutdown failed", "err", err)
			return err
		}
		return nil
	}
}

// startSpan starts a span on the package tracer, attaching the given attributes.
func startSpan(
	ctx context.Context,
	name string,
	attrs ...attribute.KeyValue,
) (context.Context, oteltrace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name, oteltrace.WithAttributes(attrs...))
}
