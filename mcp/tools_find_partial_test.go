package mcp

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/tripsearch"
)

// MIK-8088: the find summary names failed providers whether bundles survived
// or the filters left none, and never calls a partial answer the cheapest.
func TestFormatFindSummary_PartialCoverage(t *testing.T) {
	statuses := []models.ProviderStatus{
		{ID: "kiwi", Name: "Kiwi", Status: models.StatusTimeout, Error: "deadline exceeded"},
		{ID: "google_flights", Name: "Google Flights", Status: models.StatusOK},
	}
	withBundles := &tripsearch.Result{Count: 1, PreFilterCount: 1, ProviderStatuses: statuses,
		Flights: []models.FlightResult{{Price: 99, Currency: "EUR"}}}
	empty := &tripsearch.Result{PreFilterCount: 3, ProviderStatuses: statuses}
	for name, r := range map[string]*tripsearch.Result{"with bundles": withBundles, "filtered to zero": empty} {
		got := formatFindSummary(r)
		if !strings.Contains(got, "Partial coverage:") || !strings.Contains(got, "- Kiwi: ") {
			t.Errorf("%s: summary = %q, want the partial note naming Kiwi", name, got)
		}
	}
	if got := formatFindSummary(withBundles); strings.Contains(got, "Cheapest:") {
		t.Errorf("summary = %q, want \"Cheapest found\" rather than an unqualified cheapest", got)
	}
	complete := &tripsearch.Result{Count: 1, ProviderStatuses: statuses[1:], Flights: withBundles.Flights}
	if got := formatFindSummary(complete); strings.Contains(got, "Partial coverage") || !strings.Contains(got, "Cheapest:") {
		t.Errorf("complete summary = %q, want an unqualified cheapest and no partial note", got)
	}
}
