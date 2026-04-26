// Package telemetry provides optional OpenTelemetry tracing.
// Activate by setting TRVL_OTEL_ENDPOINT to an OTLP/HTTP endpoint (e.g. "http://localhost:4318").
// When the env var is unset or empty the package is a complete no-op and adds zero overhead.
package telemetry

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const serviceName = "trvl"

// Init configures a TracerProvider that exports spans to the given OTLP/HTTP
// endpoint (e.g. "http://localhost:4318"). The returned shutdown function must
// be called before the process exits to flush the span buffer.
// If endpoint is empty, Init is a no-op and returns a no-op shutdown.
func Init(ctx context.Context, endpoint string) (shutdown func(context.Context) error, err error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracehttp.Option{}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		opts = append(opts, otlptracehttp.WithEndpointURL(endpoint))
	} else {
		opts = append(opts, otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)
	if err != nil {
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// Tracer returns the named tracer from the global TracerProvider.
// Before Init is called this returns a no-op tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(serviceName)
}
