// Package chaos provides a configurable HTTP fault injector for use in
// tests. It wraps any http.RoundTripper and intercepts requests whose
// host matches a fault plan, returning synthetic error responses (429,
// 503) or a simulated timeout rather than forwarding the request.
//
// Typical usage:
//
//	plan := chaos.Plan{
//	    "api.provider.com": {Fault: chaos.Fault503},
//	}
//	client := &http.Client{Transport: chaos.New(plan, nil)}
//
// This is MIK-3089 AC: "Chaos harness internal/chaos/ injects
// 429/503/timeout per-provider; canary test runs in CI."
package chaos

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

// Fault identifies the kind of synthetic failure to inject.
type Fault int

const (
	// FaultNone passes the request through to the inner transport.
	FaultNone Fault = iota
	// Fault429 returns HTTP 429 Too Many Requests with an optional
	// Retry-After header.
	Fault429
	// Fault503 returns HTTP 503 Service Unavailable.
	Fault503
	// FaultTimeout returns a context.DeadlineExceeded error without
	// contacting the inner transport.
	FaultTimeout
)

// Entry describes the fault to inject for a specific provider host.
type Entry struct {
	// Fault is the kind of failure to inject.
	Fault Fault
	// Probability is the fraction of requests that should receive the
	// fault (0.0 = never, 1.0 = always). Zero is treated as 1.0 so
	// that a zero-value Entry with a non-zero Fault always fires.
	Probability float64
	// RetryAfter is written as the Retry-After header value when
	// Fault == Fault429. Zero means the header is omitted.
	RetryAfter time.Duration
}

// Plan maps a host (without port, e.g. "api.example.com") to the
// fault that should be injected for requests to that host.
type Plan map[string]Entry

// Transport wraps an inner RoundTripper and injects faults per Plan.
// It is safe for concurrent use.
type Transport struct {
	// Inner is the transport used for requests that are not intercepted.
	// If nil, http.DefaultTransport is used.
	Inner http.RoundTripper
	// Plan holds the per-host fault configuration.
	Plan Plan
	// randFn is injectable for deterministic tests; defaults to
	// rand.Float64 from math/rand/v2.
	randFn func() float64
}

// New returns a Transport that injects faults according to plan.
// inner may be nil to fall back to http.DefaultTransport.
func New(plan Plan, inner http.RoundTripper) *Transport {
	if inner == nil {
		inner = http.DefaultTransport
	}
	return &Transport{Inner: inner, Plan: plan}
}

// NewDeterministic returns a Transport that always fires its faults
// (probability ignored) regardless of random state. Useful for unit
// tests that want predictable behaviour.
func NewDeterministic(plan Plan, inner http.RoundTripper) *Transport {
	t := New(plan, inner)
	t.randFn = func() float64 { return 0.0 } // always ≤ any probability
	return t
}

// RoundTrip implements http.RoundTripper. It intercepts requests whose
// host matches an entry in Plan and either injects the configured fault
// or forwards to Inner depending on the entry's Probability.
//
// Plan key matching: the full host:port string (req.URL.Host) is tried
// first. If no match is found the bare hostname (req.URL.Hostname(),
// no port) is tried as fallback. This lets plans use bare hostnames
// for standard ports (e.g. "api.example.com") and host:port pairs for
// tests using httptest servers on ephemeral ports.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Try host:port first (useful for httptest), then bare hostname.
	entry, ok := t.Plan[req.URL.Host]
	if !ok {
		entry, ok = t.Plan[req.URL.Hostname()]
	}
	if !ok || entry.Fault == FaultNone {
		return t.inner().RoundTrip(req)
	}

	prob := entry.Probability
	if prob == 0 {
		prob = 1.0
	}
	rf := t.randFn
	if rf == nil {
		rf = rand.Float64
	}
	if rf() >= prob {
		// Below probability threshold — pass through normally.
		return t.inner().RoundTrip(req)
	}

	switch entry.Fault {
	case Fault429:
		resp := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Too Many Requests",
			Header:     make(http.Header),
			Body:       io.NopCloser(nil),
			Request:    req,
		}
		if entry.RetryAfter > 0 {
			resp.Header.Set("Retry-After",
				fmt.Sprintf("%.0f", entry.RetryAfter.Seconds()))
		}
		// Use http.NoBody so callers can io.ReadAll safely.
		resp.Body = http.NoBody
		return resp, nil

	case Fault503:
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil

	case FaultTimeout:
		return nil, context.DeadlineExceeded

	default:
		return t.inner().RoundTrip(req)
	}
}

func (t *Transport) inner() http.RoundTripper {
	if t.Inner == nil {
		return http.DefaultTransport
	}
	return t.Inner
}
