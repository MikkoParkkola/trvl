package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
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

// TestMoreMonthlyProvidersTagMonthlyPrices: Anyplace (headline 4,645 USD next
// to a 4,170 USD/mo monthlyPrice) and Blueground (lowestRent 1,120 EUR with a
// 2-month minimum stay) quote monthly rents too.
func TestMoreMonthlyProvidersTagMonthlyPrices(t *testing.T) {
	anyplace, err := parseAnyplaceListings(loadAnyplaceCityFixture(t), "")
	if err != nil {
		t.Fatal(err)
	}
	assertMonthly(t, "anyplace", anyplace)

	listBody, _ := os.ReadFile("testdata/blueground_list_athens.html")
	detailBody, _ := os.ReadFile("testdata/blueground_detail_ath327.html")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/furnished-apartments-athens-gr":
			http.Redirect(w, r, "/furnished-apartments-athens-greece", http.StatusMovedPermanently)
		case r.URL.Path == "/furnished-apartments-athens-greece":
			_, _ = w.Write(listBody)
		case strings.HasPrefix(r.URL.Path, "/p/"):
			_, _ = w.Write(detailBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	prevEnabled, prevURL, prevClient := bluegroundEnabled, bluegroundBaseURL, bluegroundClient
	bluegroundEnabled, bluegroundBaseURL, bluegroundClient = true, ts.URL, ts.Client()
	t.Cleanup(func() { bluegroundEnabled, bluegroundBaseURL, bluegroundClient = prevEnabled, prevURL, prevClient })
	blue, err := SearchBlueground(context.Background(), "Athens, Greece", HotelSearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertMonthly(t, "blueground", blue)
}

// TestMonthlyTagSurvivesTheSearchAndIsDropped runs the real path: Spotahome
// serves its fixture through SearchHotelsWithClient (source tagging, merge,
// finalize), and the nightly drop then removes every Spotahome rental.
func TestMonthlyTagSurvivesTheSearchAndIsDropped(t *testing.T) {
	const lisLat, lisLon = 38.7223, -9.1393
	stubGoogleHotels(t, []models.HotelResult{{Name: "Hotel Nightly", HotelID: "/g/nightly", Price: 90, Currency: "EUR", Lat: lisLat + 0.005, Lon: lisLon}})
	freshHotelBreaker(t)
	body, err := os.ReadFile("testdata/spotahome_lisbon.data")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/s/lisbon/") {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	prevEnabled, prevURL, prevClient := spotahomeEnabled, spotahomeBaseURL, spotahomeClient
	spotahomeEnabled, spotahomeBaseURL, spotahomeClient = true, ts.URL, ts.Client()
	t.Cleanup(func() { spotahomeEnabled, spotahomeBaseURL, spotahomeClient = prevEnabled, prevURL, prevClient })

	res, err := SearchHotelsWithClient(context.Background(), offlineSearchClient(t), "Lisbon", HotelSearchOptions{
		CheckIn: "2027-05-10", CheckOut: "2027-05-12", Currency: "EUR", MaxPages: 1, CenterLat: lisLat, CenterLon: lisLon,
	})
	if err != nil {
		t.Fatal(err)
	}
	spot := 0
	for _, h := range res.Hotels {
		if !hasSource(h, "spotahome") {
			continue
		}
		spot++
		if h.PriceBasis != models.PriceBasisMonthly {
			t.Errorf("%q reached the result with basis %q, want monthly", h.Name, h.PriceBasis)
		}
	}
	if spot == 0 {
		t.Fatalf("no Spotahome rows reached the result: %v", sortedHotelNames(res.Hotels))
	}
	DropMonthlyRentals(res)
	for _, h := range res.Hotels {
		if hasSource(h, "spotahome") {
			t.Errorf("the nightly drop kept %q", h.Name)
		}
	}
	if res.MonthlyOmitted != spot || !slices.Contains(sortedHotelNames(res.Hotels), "Hotel Nightly") {
		t.Errorf("monthly_omitted = %d (want %d), hotels = %v", res.MonthlyOmitted, spot, sortedHotelNames(res.Hotels))
	}
}

func hasSource(h models.HotelResult, provider string) bool {
	for _, s := range h.Sources {
		if s.Provider == provider {
			return true
		}
	}
	return false
}

// TestFlatioTagsMonthlyOnlyWhenTheUnitSaysMonth: Flatio's live JSON-LD prices
// a 30-day stay (UnitPriceSpecification referenceQuantity 30 DAY), so those
// rows are monthly; the older shape with no price specification quoted a
// night (32-200 EUR in the Lisbon fixture) and stays untagged.
func TestFlatioTagsMonthlyOnlyWhenTheUnitSaysMonth(t *testing.T) {
	vienna, err := os.ReadFile("testdata/flatio_vienna_monthly.html")
	if err != nil {
		t.Fatal(err)
	}
	monthly, err := parseFlatioHTML(vienna, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(monthly) != 3 {
		t.Fatalf("vienna fixture parsed %d hotels, want 3", len(monthly))
	}
	assertMonthly(t, "flatio (30-day spec)", monthly)

	lisbon, err := os.ReadFile("testdata/flatio_lisbon.html")
	if err != nil {
		t.Fatal(err)
	}
	nightly, err := parseFlatioHTML(lisbon, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range nightly {
		if h.PriceBasis == models.PriceBasisMonthly {
			t.Errorf("old no-spec shape %q tagged monthly", h.Name)
		}
		for _, s := range h.Sources {
			if s.PriceBasis == models.PriceBasisMonthly {
				t.Errorf("old no-spec shape %q source tagged monthly", h.Name)
			}
		}
	}
}
