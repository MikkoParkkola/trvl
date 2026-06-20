package flights

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// legacyParseString is the misleading message issue #198 reported and issue #228
// re-surfaced through the round-trip composer. It must never reach a user/agent-
// facing field (Error or a provider status); a debug log is the only place it may
// remain.
const legacyParseString = "unexpected flight data format"

// TestExtractFlightData_ErrorResponseEmitsLegacyString documents WHY the
// classification layer is needed: the raw batchexec extractor still emits the
// legacy string for a non-flight (ErrorResponse) payload. The flights layer must
// intercept this BEFORE it reaches a user-facing field.
func TestExtractFlightData_ErrorResponseEmitsLegacyString(t *testing.T) {
	body, err := os.ReadFile("testdata/google_flights_error_response.json")
	if err != nil {
		t.Fatalf("read error fixture: %v", err)
	}
	inner, err := batchexec.DecodeFlightResponse(body)
	if err != nil {
		t.Fatalf("DecodeFlightResponse on error fixture: %v", err)
	}
	// The fixture is a Google travel.frontend.flights.ErrorResponse, so inner is a
	// JSON object, not the flight array.
	if isFlightPayload(inner) {
		t.Fatalf("error fixture should NOT be classified as a flight payload")
	}
	// Confirm the raw extractor is the source of the legacy string we must hide.
	if _, exErr := batchexec.ExtractFlightData(inner); exErr == nil ||
		!strings.Contains(exErr.Error(), legacyParseString) {
		t.Fatalf("expected raw extractor to emit %q, got %v", legacyParseString, exErr)
	}
}

// TestIsFlightPayload covers the array-vs-object discriminator that separates a
// genuine flight response from an error/challenge payload.
func TestIsFlightPayload(t *testing.T) {
	if !isFlightPayload([]any{1, 2, 3}) {
		t.Errorf("array payload should be a flight payload")
	}
	if isFlightPayload(map[string]any{"error": "x"}) {
		t.Errorf("object payload (ErrorResponse) must not be a flight payload")
	}
	if isFlightPayload("sorry") {
		t.Errorf("scalar payload (challenge page) must not be a flight payload")
	}
	if isFlightPayload(nil) {
		t.Errorf("nil payload must not be a flight payload")
	}
}

// TestGoogleUpstreamStatusError classifies rate-limit / block / overload HTTP
// statuses as a typed retryable rate-limit (AC TRVL.FLIGHTS.2) and leaves other
// statuses to the normal path.
func TestGoogleUpstreamStatusError(t *testing.T) {
	for _, status := range []int{429, 403, 503} {
		err := googleUpstreamStatusError(status)
		if err == nil {
			t.Fatalf("status %d should classify as a typed rate-limit", status)
		}
		if !errors.Is(err, models.ErrRateLimited) {
			t.Errorf("status %d error %v should wrap ErrRateLimited", status, err)
		}
		if models.ClassifyProviderError(err) != models.StatusRateLimited {
			t.Errorf("status %d should classify to rate_limited", status)
		}
		if strings.Contains(err.Error(), legacyParseString) {
			t.Errorf("status %d message leaked legacy string: %q", status, err.Error())
		}
	}
	if err := googleUpstreamStatusError(200); err != nil {
		t.Errorf("200 must not be a rate-limit, got %v", err)
	}
	if err := googleUpstreamStatusError(500); err != nil {
		t.Errorf("500 must fall through to the generic path, got %v", err)
	}
}

// TestLegErrorsRateLimited verifies the composer's decision gate: a leg whose
// error chain carries the typed rate-limit (even when joined with sibling
// provider errors, as searchFlightsCore builds it) is treated as retryable; a
// purely genuine failure is not.
func TestLegErrorsRateLimited(t *testing.T) {
	rl := fmt.Errorf("google flights returned a non-flight error/challenge payload: %w", models.ErrRateLimited)
	// Mirror how searchFlightsCore aggregates a fully-failed leg.
	joinedRL := errors.Join(rl, fmt.Errorf("kiwi: %w", models.ErrRateLimited))
	if !legErrorsRateLimited(joinedRL, nil) {
		t.Errorf("a rate-limited outbound leg should be classified rate-limited")
	}
	if !legErrorsRateLimited(nil, rl) {
		t.Errorf("a rate-limited inbound leg should be classified rate-limited")
	}
	genuine := errors.New("decode response: invalid character")
	if legErrorsRateLimited(genuine, nil) {
		t.Errorf("a genuine parse failure must NOT be classified rate-limited")
	}
	// Mixed: one leg rate-limited, the other a genuine bug -> not rate-limit-only.
	if legErrorsRateLimited(rl, genuine) {
		t.Errorf("a leg with a genuine failure must fall through, not rate-limit")
	}
	if legErrorsRateLimited(nil, nil) {
		t.Errorf("no errors should not be classified rate-limited")
	}
}

// TestRoundTripComposerRateLimit_NoLegacyLeak is the issue #228 regression. It
// reconstructs the exact error chain the round-trip composer sees when the Google
// leg returns an upstream ErrorResponse payload and asserts the composer routes it
// to the soft rate-limit path (legErrorsRateLimited == true) while NO leg-facing
// error string carries the legacy "unexpected flight data format" message
// (AC TRVL.FLIGHTS.1, .2, .4). The string is intercepted at the search layer
// (isFlightPayload), so it never enters the chain that joinLegErrors would leak.
func TestRoundTripComposerRateLimit_NoLegacyLeak(t *testing.T) {
	body, err := os.ReadFile("testdata/google_flights_error_response.json")
	if err != nil {
		t.Fatalf("read error fixture: %v", err)
	}
	inner, derr := batchexec.DecodeFlightResponse(body)
	if derr != nil {
		t.Fatalf("decode error fixture: %v", derr)
	}
	if isFlightPayload(inner) {
		t.Fatal("fixture precondition: error payload must not be a flight payload")
	}

	// The search layer turns a non-flight payload into THIS typed error (clean,
	// no legacy string), which searchFlightsCore folds into the leg error.
	googleErr := fmt.Errorf("google flights returned a non-flight error/challenge payload: %w", models.ErrRateLimited)
	legErr := errors.Join(googleErr, fmt.Errorf("kiwi: %w", models.ErrRateLimited))

	// AC TRVL.FLIGHTS.4: legacy string never reaches the user/agent-facing chain.
	if strings.Contains(legErr.Error(), legacyParseString) {
		t.Errorf("leg error chain leaked legacy string: %q", legErr.Error())
	}
	if strings.Contains(models.ClassifyProviderError(googleErr), legacyParseString) {
		t.Errorf("provider status leaked legacy string")
	}
	// AC TRVL.FLIGHTS.2: the composer routes this to the retryable rate-limit path.
	if !legErrorsRateLimited(legErr, nil) {
		t.Errorf("rate-limited leg should route to the soft rate-limit path")
	}
	if got := models.ClassifyProviderError(googleErr); got != models.StatusRateLimited {
		t.Errorf("google leg status = %q, want rate_limited", got)
	}
}

// TestRoundTripComposerGenuineError_FallsThrough ensures a real parse bug is NOT
// reclassified as a retryable rate-limit — the soft path only triggers on typed
// rate-limits, so genuine failures still surface for diagnosis.
func TestRoundTripComposerGenuineError_FallsThrough(t *testing.T) {
	genuine := errors.New("decode response: invalid character 'x'")
	if legErrorsRateLimited(genuine, nil) {
		t.Errorf("a genuine failure must not be classified as a rate-limit")
	}
	if errors.Is(genuine, models.ErrRateLimited) {
		t.Errorf("a genuine failure must not satisfy errors.Is(ErrRateLimited)")
	}
}
