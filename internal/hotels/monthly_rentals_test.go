package hotels

import (
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// assertMonthly fails unless every hotel and every source carries the monthly
// basis, as HousingAnywhere and Landing already do.
func assertMonthly(t *testing.T, provider string, hs []models.HotelResult) {
	t.Helper()
	if len(hs) == 0 {
		t.Fatalf("%s: no hotels parsed", provider)
	}
	for _, h := range hs {
		if h.PriceBasis != models.PriceBasisMonthly {
			t.Errorf("%s %q: price basis = %q, want monthly", provider, h.Name, h.PriceBasis)
		}
		for _, s := range h.Sources {
			if s.PriceBasis != models.PriceBasisMonthly {
				t.Errorf("%s %q: source basis = %q, want monthly", provider, h.Name, s.PriceBasis)
			}
		}
	}
}

// TestMonthlyRentalProvidersTagMonthlyPrices: Uniplaces, Wunderflats and
// Spotahome quote monthly rents (fixtures: Lisbon rooms 470-950 EUR, Munich
// flats 1,200-1,750 EUR, a Lisbon flat 1,850 EUR with bills).
func TestMonthlyRentalProvidersTagMonthlyPrices(t *testing.T) {
	uni, err := parseUniplacesOffers(loadUniplacesFixture(t), "lisbon", "")
	if err != nil {
		t.Fatal(err)
	}
	assertMonthly(t, "uniplaces", uni)

	pd, err := extractWunderflatsPageData(loadWunderflatsFixture(t, "wunderflats_munich.html"))
	if err != nil {
		t.Fatal(err)
	}
	assertMonthly(t, "wunderflats", parseWunderflatsListings(pd.ListingResults, ""))

	raw, err := os.ReadFile("testdata/spotahome_lisbon.data")
	if err != nil {
		t.Fatal(err)
	}
	spot, err := parseSpotahomeData(raw, "lisbon", "")
	if err != nil {
		t.Fatal(err)
	}
	assertMonthly(t, "spotahome", spot)
}
