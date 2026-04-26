package telemetry

import (
	"context"
	"testing"
)

func TestInitNoEndpoint(t *testing.T) {
	shutdown, err := Init(context.Background(), "")
	if err != nil {
		t.Fatalf("Init with empty endpoint returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown returned error: %v", err)
	}
}

func TestTracerBeforeInit(t *testing.T) {
	tr := Tracer()
	if tr == nil {
		t.Fatal("Tracer() returned nil")
	}
	ctx, span := tr.Start(context.Background(), "test-span")
	defer span.End()
	if ctx == nil {
		t.Fatal("Start returned nil context")
	}
}

func TestInitInvalidEndpoint(t *testing.T) {
	// otlptracehttp.New is lazy for HTTP — it does not dial at creation time.
	// So Init should succeed even with an unreachable endpoint.
	shutdown, err := Init(context.Background(), "not-a-real-host:4318")
	if err != nil {
		// Some environments may fail eagerly; that is also acceptable.
		t.Logf("Init returned error (acceptable): %v", err)
		return
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}
	// Calling shutdown may or may not return an error depending on buffered spans.
	_ = shutdown(context.Background())
}

func TestInitHTTPURL(t *testing.T) {
	shutdown, err := Init(context.Background(), "http://127.0.0.1:4318/v1/traces")
	if err != nil {
		t.Fatalf("Init with HTTP URL returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}
	_ = shutdown(context.Background())
}
