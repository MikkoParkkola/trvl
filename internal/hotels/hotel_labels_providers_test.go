package hotels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// stubGoogleHotels makes the Google fetch return hotels without any network
// and records the location each fetch was asked for.
func stubGoogleHotels(t *testing.T, hotels []models.HotelResult) *[]string {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	orig := fetchHotelPageFullFn
	t.Cleanup(func() { fetchHotelPageFullFn = orig })
	fetchHotelPageFullFn = func(_ context.Context, _ *batchexec.Client, location string, _ HotelSearchOptions, _ int, _ string) (parseResult, error) {
		mu.Lock()
		asked = append(asked, location)
		mu.Unlock()
		return parseResult{Hotels: slices.Clone(hotels)}, nil
	}
	return &asked
}

// freshHotelBreaker gives the test its own breaker that trips on one failure.
func freshHotelBreaker(t *testing.T) {
	t.Helper()
	orig := hotelBreaker
	t.Cleanup(func() { hotelBreaker = orig })
	hotelBreaker = breaker.NewWithConfig(1, time.Minute)
}

func offlineSearchClient(t *testing.T) *batchexec.Client {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	c := newTestClient(srv.URL)
	c.SetNoCache(true)
	return c
}

// Vienna's centre, and points about 1 km and 26 km north of it.
const viennaLat, viennaLon = 48.2082, 16.3738

func viennaHotel(id string, lat, lon float64) models.HotelResult {
	return models.HotelResult{Name: id, HotelID: "/g/" + id, Price: 80, Currency: "EUR", Lat: lat, Lon: lon}
}

func sortedHotelNames(hs []models.HotelResult) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	slices.Sort(out)
	return out
}

// TestParseBookingApolloDividesStayTotalByNights: blocks[0].finalPrice is the
// stay total, while every other provider quotes a night.
func TestParseBookingApolloDividesStayTotalByNights(t *testing.T) {
	blob := `{"ROOT_QUERY":{"searchQueries":{"search({})":{"results":[` +
		`{"displayName":{"text":"Hotel Sacher"},"basicPropertyData":{"id":"1"},` +
		`"blocks":[{"finalPrice":{"amount":400,"currency":"EUR"}}]}]}}}}`
	for nights, want := range map[int]float64{1: 400, 2: 200} {
		hotels, err := parseBookingApollo(blob, "EUR", nights)
		if err != nil || len(hotels) != 1 {
			t.Fatalf("%d nights: hotels = %v, err = %v", nights, hotels, err)
		}
		if hotels[0].Price != want {
			t.Errorf("%d nights: price = %v, want %v a night", nights, hotels[0].Price, want)
		}
	}
}

// TestSearchHotelsAppliesTheCapItIsGiven: the search applies the distance cap
// it is handed (the 25 km default is set at the list entry points), counts the
// hotels it leaves out, and keeps hotels without coordinates. No cap, no count.
func TestSearchHotelsAppliesTheCapItIsGiven(t *testing.T) {
	stubGoogleHotels(t, []models.HotelResult{
		viennaHotel("near", viennaLat+0.009, viennaLon),
		viennaHotel("far", viennaLat+0.234, viennaLon),
		viennaHotel("nocoords", 0, 0),
	})
	client := offlineSearchClient(t)
	for _, c := range []struct {
		capKm       float64
		want        []string
		wantFarther int
	}{
		{25, []string{"near", "nocoords"}, 1},
		{30, []string{"far", "near", "nocoords"}, 0},
		{0, []string{"far", "near", "nocoords"}, 0},
	} {
		res, err := SearchHotelsWithClient(context.Background(), client, "Vienna", HotelSearchOptions{
			CheckIn: "2027-04-10", CheckOut: "2027-04-12", Currency: "EUR", MaxPages: 1,
			CenterLat: viennaLat, CenterLon: viennaLon, MaxDistanceKm: c.capKm,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedHotelNames(res.Hotels); !slices.Equal(got, c.want) {
			t.Errorf("cap %v: hotels = %v, want %v", c.capKm, got, c.want)
		}
		if res.FartherOmitted != c.wantFarther || res.MaxDistanceKm != c.capKm {
			t.Errorf("cap %v: farther_omitted = %d, max_distance_km = %v, want %d and %v",
				c.capKm, res.FartherOmitted, res.MaxDistanceKm, c.wantFarther, c.capKm)
		}
	}
}

// TestSideProvidersGetEnglishCityGoogleGetsLocal: "Wien" and "Vienna" both
// reach the side providers as Vienna (Spotahome asks for /s/vienna/...),
// while only the Google fetch gets the local form Google resolves.
func TestSideProvidersGetEnglishCityGoogleGetsLocal(t *testing.T) {
	asked := stubGoogleHotels(t, []models.HotelResult{viennaHotel("near", viennaLat+0.009, viennaLon)})
	var mu sync.Mutex
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	prevEnabled, prevURL, prevClient := spotahomeEnabled, spotahomeBaseURL, spotahomeClient
	spotahomeEnabled, spotahomeBaseURL, spotahomeClient = true, ts.URL, ts.Client()
	t.Cleanup(func() { spotahomeEnabled, spotahomeBaseURL, spotahomeClient = prevEnabled, prevURL, prevClient })
	client := offlineSearchClient(t)

	for _, city := range []string{"Wien", "Vienna"} {
		freshHotelBreaker(t)
		mu.Lock()
		paths = nil
		mu.Unlock()
		*asked = nil
		_, _ = SearchHotelsWithClient(context.Background(), client, city, HotelSearchOptions{
			CheckIn: "2027-04-14", CheckOut: "2027-04-16", Currency: "EUR", MaxPages: 1,
			CenterLat: viennaLat, CenterLon: viennaLon,
		})
		mu.Lock()
		if len(paths) == 0 || !strings.HasPrefix(paths[0], "/s/vienna/") {
			t.Errorf("%s: spotahome paths = %v, want /s/vienna/...", city, paths)
		}
		mu.Unlock()
		if len(*asked) == 0 || (*asked)[0] != "Wien" {
			t.Errorf("%s: Google fetch asked for %v, want Wien", city, *asked)
		}
	}
}

// TestHotelAuxNotApplicableIsSkippedWithoutStrike: a provider that does not
// serve the place answers with a wrapped ErrNotApplicable; that is a skipped
// row, never a failure, and the circuit breaker records nothing.
func TestHotelAuxNotApplicableIsSkippedWithoutStrike(t *testing.T) {
	wrapped := fmt.Errorf("booking: no market for this city: %w", models.ErrNotApplicable)
	row := hotelProviderStatusFromError("booking", "Booking.com", wrapped)
	if row.Status != models.StatusSkipped || row.Reason != "city_not_served" {
		t.Errorf("status row = %+v, want skipped with reason city_not_served", row)
	}

	stubGoogleHotels(t, []models.HotelResult{viennaHotel("near", viennaLat+0.009, viennaLon)})
	freshHotelBreaker(t)
	origBooking := SearchBooking
	t.Cleanup(func() { SearchBooking = origBooking })
	SearchBooking = func(context.Context, string, HotelSearchOptions) ([]models.HotelResult, error) {
		return nil, wrapped
	}
	res, err := SearchHotelsWithClient(context.Background(), offlineSearchClient(t), "Vienna", HotelSearchOptions{
		CheckIn: "2027-04-18", CheckOut: "2027-04-20", Currency: "EUR", MaxPages: 1,
		CenterLat: viennaLat, CenterLon: viennaLon,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range res.ProviderStatuses {
		if s.ID == "booking" {
			found = true
			if s.Status != models.StatusSkipped {
				t.Errorf("booking status = %q, want skipped", s.Status)
			}
		}
	}
	if !found {
		t.Errorf("no booking status row in %+v", res.ProviderStatuses)
	}
	if hotelBreaker.Tripped("booking") {
		t.Error("a not-applicable answer struck the circuit breaker")
	}
}

// TestSearchLandingOutsideUSIsNotApplicable: Landing only lists US homes, so a
// European city is not applicable and no request leaves trvl.
func TestSearchLandingOutsideUSIsNotApplicable(t *testing.T) {
	var hits int
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	prevEnabled, prevURL, prevClient := landingEnabled, landingBaseURL, landingHTTPClient
	landingEnabled, landingBaseURL, landingHTTPClient = true, ts.URL, ts.Client()
	t.Cleanup(func() { landingEnabled, landingBaseURL, landingHTTPClient = prevEnabled, prevURL, prevClient })

	_, err := SearchLanding(context.Background(), "Vienna", HotelSearchOptions{CheckIn: "2027-04-10", CheckOut: "2027-05-10"})
	if !errors.Is(err, models.ErrNotApplicable) {
		t.Errorf("err = %v, want ErrNotApplicable", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("Landing was asked %d times for a city outside the US", hits)
	}
}

// TestAnnotateHotelConfidenceScoresTheHeadlineProvider: confidence judges the
// headline quote's provider, not the cheapest one. Google is profiled as a
// scrape; crediting it for an Agoda headline would add provider:scrape.
func TestAnnotateHotelConfidenceScoresTheHeadlineProvider(t *testing.T) {
	hs := []models.HotelResult{{
		Name: "St Christopher's", Price: 66.83, Currency: "EUR",
		HeadlineProvider: "agoda", CheapestSource: "google_hotels",
		Sources: []models.PriceSource{
			{Provider: "google_hotels", Price: 909, Currency: "CZK"},
			{Provider: "agoda", Price: 66.83, Currency: "EUR"},
		},
	}}
	annotateHotelConfidence(hs, time.Now())
	if hs[0].Confidence == nil {
		t.Fatal("no confidence")
	}
	if slices.Contains(hs[0].Confidence.Signals, "provider:scrape") {
		t.Errorf("signals = %v: scored google_hotels instead of the agoda headline", hs[0].Confidence.Signals)
	}
}

// TestGoogleRadiusOnlyForTightCaps: Google gets lrad only when the cap is
// tighter than the 25 km default. At the default and wider, Google returns its
// normal set and trvl filters and counts every farther hotel itself.
func TestGoogleRadiusOnlyForTightCaps(t *testing.T) {
	for capKm, wantLrad := range map[float64]string{2: "lrad=2000", DefaultMaxDistanceKm: "", 30: ""} {
		u := buildTravelURL("Vienna", HotelSearchOptions{CheckIn: "2027-04-10", CheckOut: "2027-04-12", MaxDistanceKm: capKm})
		switch {
		case wantLrad != "" && !strings.Contains(u, wantLrad):
			t.Errorf("cap %v km: URL %s lacks %s", capKm, u, wantLrad)
		case wantLrad == "" && strings.Contains(u, "lrad="):
			t.Errorf("cap %v km: URL %s sends lrad; trvl must filter and count itself", capKm, u)
		}
	}
}
