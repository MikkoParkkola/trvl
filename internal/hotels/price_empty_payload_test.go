package hotels

import (
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestParseHotelPriceResponse_EmptyPayload locks in the pipeline's behavior
// against a REAL Google batchexecute response captured 2026-06 for the yY52ce
// price RPC (testdata/price_empty_payload.txt).
//
// As of that capture Google's unauthenticated price RPC returns a wrb.fr
// envelope with a NULL payload and an internal status of [3] — i.e. it accepts
// the request (HTTP 200) but returns zero booking-partner data. The decode step
// must succeed and ParseHotelPriceResponse must surface the routine
// "no provider prices found" signal (NOT a hard parse error), so the caller can
// degrade gracefully to a Notice rather than failing the whole lookup.
//
// This is a regression guard for #187/#188: it documents that empty room/OTA
// pricing is a Google-side request-protocol change, not a parser defect. When a
// refreshed batchexecute request format (captured via a browser HAR) starts
// returning a populated payload, replace this fixture with a populated one and
// assert real providers parse out.
func TestParseHotelPriceResponse_EmptyPayload(t *testing.T) {
	body, err := os.ReadFile("testdata/price_empty_payload.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Decode must not error on the real (null-payload) response shape.
	entries, err := batchexec.DecodeBatchResponse(body)
	if err != nil {
		t.Fatalf("DecodeBatchResponse on real empty payload errored: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected decoded entries from the wrb.fr envelope, got none")
	}

	// Parse must surface the routine no-provider signal, not a hard error.
	prices, perr := ParseHotelPriceResponse(entries)
	if perr == nil {
		t.Fatalf("expected a no-provider error for an empty payload, got %d prices", len(prices))
	}
	if len(prices) != 0 {
		t.Fatalf("expected 0 prices for an empty payload, got %d", len(prices))
	}

	// The error must be classified as the routine no-provider case so the
	// GetHotelPricesWithOpts caller degrades to a Notice instead of failing.
	if !isNoProviderPricesError(perr) {
		t.Fatalf("error %q is not classified as no-provider-prices; the graceful "+
			"degradation path in GetHotelPricesWithOpts would not trigger", perr)
	}
}
