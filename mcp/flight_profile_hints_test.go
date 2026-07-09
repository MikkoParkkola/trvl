package mcp

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/profile"
)

// TestApplyFlightProfileHints_DoesNotAutoApplyMaxPrice is a regression test
// for the search_flights MCP truncation bug (issue #452): @gurubrix reported
// that search_flights via MCP returned only 4 kiwi-only flights for FCO->HRG
// while the CLI and a direct flights.SearchFlights() call — identical params —
// returned 106 flights across every provider (kiwi, google_flights, egyptair,
// lufthansa, ...). provider_statuses showed google_flights as "ok" with 91
// results that never made it into the final flights[] array.
//
// Root cause: handleSearchFlights silently applied a profile-derived
// AvgFlightPrice*1.5 budget ceiling to opts.MaxPrice whenever the caller did
// not pass max_price explicitly. opts.MaxPrice is a hard post-fetch filter
// (internal/flights.filterFlightResults), applied AFTER provider_statuses
// already recorded each provider's raw fetch count — so a route priced above
// the user's historical average silently collapsed the merged multi-provider
// result down to whichever provider's flights happened to fit under the
// ceiling (here: only the 4 cheapest Kiwi fares), with no signal in the
// response that anything had been filtered. The CLI (cmd/trvl/flights.go)
// never applies profile hints at all, so it returned the unfiltered set —
// hence the MCP/CLI divergence for identical params.
//
// This test locks in the fix: a profile-derived MaxPrice hint must never be
// auto-applied as a hard filter, matching CLI behavior exactly. See
// internal/flights.TestMergeFlightResults_ProfileMaxPriceHintWouldTruncateMultiProvider
// for the end-to-end reproduction of the reported symptom using the real
// merge/filter pipeline.
func TestApplyFlightProfileHints_DoesNotAutoApplyMaxPrice(t *testing.T) {
	// hints.MaxPrice mirrors profile.FlightHints() deriving a ceiling from
	// AvgFlightPrice*1.5 (see internal/profile/apply.go), simulating a user
	// whose historical average is far below a specific route's real fares.
	hints := profile.FlightSearchHints{MaxPrice: 300}
	args := map[string]any{
		"origin":         "FCO",
		"destination":    "HRG",
		"departure_date": "2026-08-10",
		// No max_price passed by the caller.
	}

	got := applyFlightProfileHints(flights.SearchOptions{}, args, hints)

	if got.MaxPrice != 0 {
		t.Fatalf("MaxPrice = %d, want 0 (profile-derived budget hints must never be auto-applied as a hard filter — CLI parity, issue #452)", got.MaxPrice)
	}
}

// TestApplyFlightProfileHints_ExplicitMaxPriceUnaffected verifies the fix does
// not disturb a caller-supplied max_price: applyFlightProfileHints only ever
// fills in defaults for parameters the caller left unset, and since MaxPrice
// is no longer touched at all, an explicit opts.MaxPrice must survive
// untouched.
func TestApplyFlightProfileHints_ExplicitMaxPriceUnaffected(t *testing.T) {
	hints := profile.FlightSearchHints{MaxPrice: 300}
	args := map[string]any{"max_price": float64(500)}

	got := applyFlightProfileHints(flights.SearchOptions{MaxPrice: 500}, args, hints)

	if got.MaxPrice != 500 {
		t.Fatalf("MaxPrice = %d, want 500 (explicit caller value must be preserved)", got.MaxPrice)
	}
}

// TestApplyFlightProfileHints_CabinClassStillApplied proves the fix did not
// overcorrect: CabinClass is a legitimate profile default (it narrows the
// provider query itself rather than silently post-filtering an already-merged
// result set) and must still apply when the caller has not set cabin_class
// explicitly.
func TestApplyFlightProfileHints_CabinClassStillApplied(t *testing.T) {
	hints := profile.FlightSearchHints{CabinClass: int(models.Business)}
	args := map[string]any{}

	got := applyFlightProfileHints(flights.SearchOptions{}, args, hints)

	if got.CabinClass != models.Business {
		t.Fatalf("CabinClass = %v, want %v (legitimate cabin-class hint must still apply)", got.CabinClass, models.Business)
	}
}

// TestApplyFlightProfileHints_ExplicitCabinClassUnaffected verifies an
// explicit caller cabin_class arg blocks the profile hint from overriding it.
func TestApplyFlightProfileHints_ExplicitCabinClassUnaffected(t *testing.T) {
	hints := profile.FlightSearchHints{CabinClass: int(models.Business)}
	args := map[string]any{"cabin_class": "economy"}

	got := applyFlightProfileHints(flights.SearchOptions{CabinClass: models.Economy}, args, hints)

	if got.CabinClass != models.Economy {
		t.Fatalf("CabinClass = %v, want %v (explicit caller cabin_class must be preserved)", got.CabinClass, models.Economy)
	}
}
