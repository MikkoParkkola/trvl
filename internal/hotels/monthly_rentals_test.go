package hotels

import (
	"context"
	"math"
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

// TestCoreSearchLeavesMonthlyRentalsOutByDefault runs the real path with
// Spotahome serving its fixture through SearchHotelsWithClient. By default
// (StayNightly, which every caller but search_accommodations uses: CLI, MCP
// hotel tools, trip planning, prices, rooms, watches) the monthly rentals are
// left out and counted. StayIncludeMonthly keeps them, labelled monthly.
func TestCoreSearchLeavesMonthlyRentalsOutByDefault(t *testing.T) {
	const lisLat, lisLon = 38.7223, -9.1393
	stubGoogleHotels(t, []models.HotelResult{{Name: "Hotel Nightly", HotelID: "/g/nightly", Price: 90, Currency: "EUR", Lat: lisLat + 0.005, Lon: lisLon}})
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

	search := func(term StayTerm) *models.HotelSearchResult {
		t.Helper()
		freshHotelBreaker(t)
		res, err := SearchHotelsWithClient(context.Background(), offlineSearchClient(t), "Lisbon", HotelSearchOptions{
			CheckIn: "2027-05-10", CheckOut: "2027-05-12", Currency: "EUR", MaxPages: 1,
			CenterLat: lisLat, CenterLon: lisLon, StayTerm: term,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	long := search(StayIncludeMonthly)
	spot := 0
	for _, h := range long.Hotels {
		if hasSource(h, "spotahome") {
			spot++
			if h.PriceBasis != models.PriceBasisMonthly {
				t.Errorf("long-stay search: %q has basis %q, want monthly", h.Name, h.PriceBasis)
			}
		}
	}
	if spot == 0 || long.MonthlyOmitted != 0 {
		t.Fatalf("long-stay search kept %d Spotahome rows, monthly_omitted %d; want rows and 0", spot, long.MonthlyOmitted)
	}

	nightly := search(StayNightly)
	for _, h := range nightly.Hotels {
		if hasSource(h, "spotahome") {
			t.Errorf("default search kept monthly rental %q", h.Name)
		}
	}
	if nightly.MonthlyOmitted != spot || !slices.Contains(sortedHotelNames(nightly.Hotels), "Hotel Nightly") {
		t.Errorf("default search: monthly_omitted = %d (want %d), hotels = %v", nightly.MonthlyOmitted, spot, sortedHotelNames(nightly.Hotels))
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

// TestNightlySearchStripsMonthlySourcesFromMixedHotels: a hotel merged from a
// nightly and a monthly source stays in a nightly search, but its monthly
// source never competes for the headline, savings or cheapest_source. Any
// stay term other than StayIncludeMonthly counts as nightly (fail closed).
func TestNightlySearchStripsMonthlySourcesFromMixedHotels(t *testing.T) {
	stubGoogleHotels(t, []models.HotelResult{
		{Name: "Mixed", HotelID: "/g/mixed", Price: 90, Currency: "EUR", Lat: viennaLat + 0.009, Lon: viennaLon,
			Sources: []models.PriceSource{
				{Provider: "google_hotels", Price: 90, Currency: "EUR", PriceBasis: models.PriceBasisLeadIn},
				{Provider: "spotahome", Price: 40, Currency: "EUR", PriceBasis: models.PriceBasisMonthly},
			}},
		{Name: "Rental", HotelID: "spot-9", Price: 1500, Currency: "EUR", PriceBasis: models.PriceBasisMonthly, Lat: viennaLat + 0.01, Lon: viennaLon,
			Sources: []models.PriceSource{{Provider: "spotahome", Price: 1500, Currency: "EUR", PriceBasis: models.PriceBasisMonthly}}},
	})
	for _, term := range []StayTerm{StayNightly, StayTerm(99)} {
		freshHotelBreaker(t)
		res, err := SearchHotelsWithClient(context.Background(), offlineSearchClient(t), "Vienna", HotelSearchOptions{
			CheckIn: "2027-06-01", CheckOut: "2027-06-03", Currency: "EUR", MaxPages: 1,
			CenterLat: viennaLat, CenterLon: viennaLon, StayTerm: term,
		})
		if err != nil {
			t.Fatal(err)
		}
		if names := sortedHotelNames(res.Hotels); !slices.Equal(names, []string{"Mixed"}) {
			t.Errorf("term %d: hotels = %v, want only Mixed (Rental is monthly-only)", term, names)
		}
		for _, h := range res.Hotels {
			if h.Name != "Mixed" {
				continue
			}
			if hasSource(h, "spotahome") {
				t.Errorf("term %d: Mixed kept its monthly source", term)
			}
			if h.HeadlineProvider != "google_hotels" || h.Price != 90 || h.CheapestSource == "spotahome" {
				t.Errorf("term %d: Mixed headline = %v from %q, cheapest %q; want 90 from google_hotels", term, h.Price, h.HeadlineProvider, h.CheapestSource)
			}
		}
	}
}

// TestHotelSearchKeyFallbackSeparatesStayTerms: when the key cannot be
// encoded (NaN coordinates), the fallback key still keeps a nightly and a
// long-stay search apart.
func TestHotelSearchKeyFallbackSeparatesStayTerms(t *testing.T) {
	opts := HotelSearchOptions{CheckIn: "2027-04-10", CheckOut: "2027-04-12", CenterLat: math.NaN()}
	nightly := hotelSearchKey("Vienna", opts)
	opts.StayTerm = StayIncludeMonthly
	if hotelSearchKey("Vienna", opts) == nightly {
		t.Errorf("fallback key %q is shared by nightly and long-stay searches", nightly)
	}
}
