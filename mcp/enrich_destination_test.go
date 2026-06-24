package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// withStubEnricher swaps the package-level enricher seam for the duration of a
// test and restores it afterward.
func withStubEnricher(t *testing.T, fn func(context.Context, string, models.DateRange) (*models.DestinationInfo, error)) {
	t.Helper()
	prev := destinationEnricher
	destinationEnricher = fn
	t.Cleanup(func() { destinationEnricher = prev })
}

func TestEnrichDestination_ContextGatedOnEmptyLocation(t *testing.T) {
	called := false
	withStubEnricher(t, func(context.Context, string, models.DateRange) (*models.DestinationInfo, error) {
		called = true
		return &models.DestinationInfo{Location: "should-not-happen"}, nil
	})

	for _, loc := range []string{"", "   ", "\t"} {
		if got := enrichDestination(context.Background(), loc, models.DateRange{}); got != nil {
			t.Errorf("location %q: expected nil enrichment, got %+v", loc, got)
		}
	}
	if called {
		t.Fatal("enricher must not be called for an empty location (context-gating broken)")
	}
}

func TestEnrichDestination_SilentDegradeOnError(t *testing.T) {
	withStubEnricher(t, func(context.Context, string, models.DateRange) (*models.DestinationInfo, error) {
		return nil, errors.New("geocode unavailable")
	})

	if got := enrichDestination(context.Background(), "Paris", models.DateRange{}); got != nil {
		t.Errorf("expected nil on provider error (silent degrade), got %+v", got)
	}
}

func TestEnrichDestination_AttachesOnSuccess(t *testing.T) {
	want := &models.DestinationInfo{
		Location: "Paris, France",
		Weather:  models.WeatherInfo{Current: models.WeatherDay{Description: "clear"}},
		Safety:   models.SafetyInfo{},
	}
	var gotLoc string
	var gotDates models.DateRange
	withStubEnricher(t, func(_ context.Context, loc string, dates models.DateRange) (*models.DestinationInfo, error) {
		gotLoc = loc
		gotDates = dates
		return want, nil
	})

	dates := models.DateRange{CheckIn: "2026-07-01", CheckOut: "2026-07-04"}
	got := enrichDestination(context.Background(), "Paris", dates)
	if got == nil {
		t.Fatal("expected enrichment to attach on success, got nil")
	}
	if got.Location != want.Location {
		t.Errorf("location: got %q want %q", got.Location, want.Location)
	}
	if gotLoc != "Paris" {
		t.Errorf("enricher received location %q, want %q", gotLoc, "Paris")
	}
	if gotDates != dates {
		t.Errorf("enricher received dates %+v, want %+v", gotDates, dates)
	}
}
