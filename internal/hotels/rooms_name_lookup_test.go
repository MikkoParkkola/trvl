package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Coordinates of Hotel Sacher for the geocode cache (Vienna's centre is
// viennaLat/viennaLon in hotel_labels_providers_test.go).
const sacherLat, sacherLon = 48.2040009, 16.3697963

// sacherBookingRow is Booking.com's own row for Hotel Sacher: no Google ID.
var sacherBookingRow = models.HotelResult{
	Name: "Hotel Sacher Wien", Address: "Philharmoniker-Straße 4, 1010 Wien, Austria", HotelID: "4711",
	Price: 480, Currency: "EUR", Lat: sacherLat, Lon: sacherLon,
	Sources: []models.PriceSource{{Provider: "booking", Price: 480, Currency: "EUR"}},
}

// serveGoogleRecording answers every Google request with page and returns a
// function listing the q values of the hotel searches received so far.
func serveGoogleRecording(t *testing.T, page []byte) func() []string {
	t.Helper()
	freshRateManager(t)
	stubNoFX(t)
	var mu sync.Mutex
	var qs []string
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/travel/hotels/") && !strings.HasPrefix(r.URL.Path, "/travel/hotels/entity/") {
			mu.Lock()
			qs = append(qs, r.URL.Query().Get("q"))
			mu.Unlock()
		}
		_, _ = w.Write(page)
	}))
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), qs...)
	}
}

// countBlueground enables Blueground, a city-slug rental site, against a local
// server answering 404 (what the live site answers for a hotel name) and
// returns a function listing the paths it was asked for.
func countBlueground(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	prevEnabled, prevURL, prevClient, prevBreaker := bluegroundEnabled, bluegroundBaseURL, bluegroundClient, hotelBreaker
	bluegroundEnabled, bluegroundBaseURL, bluegroundClient, hotelBreaker = true, ts.URL, ts.Client(), breaker.New()
	t.Cleanup(func() {
		bluegroundEnabled, bluegroundBaseURL, bluegroundClient, hotelBreaker = prevEnabled, prevURL, prevClient, prevBreaker
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func distinctSorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// TestSearchHotelByName_NoCommaSendsTheNameAlone is #812's fail-fast. Booking
// finds the hotel without a Google ID, so the lookup asks Google again; with no
// separate location that query is the name alone, never "name, name".
func TestSearchHotelByName_NoCommaSendsTheNameAlone(t *testing.T) {
	queries := serveGoogleRecording(t, fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
	withBookingResult(t, sacherBookingRow)
	seedGeoAt(t, "Hotel Sacher Wien", sacherLat, sacherLon)
	// Keeps main's doubled geocode offline; the fix never asks for it.
	seedGeoAt(t, "Hotel Sacher Wien, Hotel Sacher Wien", sacherLat, sacherLon)

	_, _ = SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "EUR")

	if got, want := distinctSorted(queries()), []string{"Hotel Sacher Wien"}; !slices.Equal(got, want) {
		t.Fatalf("Google q values = %q, want exactly %q", got, want)
	}
}

// TestSearchHotelByName_NameNeverReachesRentalSites: a lookup by hotel name
// with no city must not ask a city-slug rental site for the hotel's name.
func TestSearchHotelByName_NameNeverReachesRentalSites(t *testing.T) {
	serveGoogleRecording(t, fakeHotelPageWithPrices(hotelWithPrice{"Hotel Sacher Wien", 300}))
	paths := countBlueground(t)
	seedGeo(t, "Hotel Sacher Wien")

	if _, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "USD"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got := paths(); len(got) != 0 {
		t.Fatalf("Blueground was asked for %q, want no request: the query names a hotel, not a city", got)
	}
}

// TestSearchHotelByName_CommaCityStillReachesRentalSites (preservation): the
// city after the comma is a real place, so the rental sites still get it.
func TestSearchHotelByName_CommaCityStillReachesRentalSites(t *testing.T) {
	serveGoogleRecording(t, fakeHotelPageWithPrices(hotelWithPrice{"Hotel Sacher", 300}))
	paths := countBlueground(t)
	// The fake page's rows sit at seedGeo's point, inside the city radius.
	seedGeo(t, "Vienna")
	seedGeo(t, "Wien")

	if _, err := SearchHotelByName(context.Background(), "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	got := paths()
	if len(got) == 0 {
		t.Fatal("Blueground got no request, want the city search for Vienna")
	}
	for _, p := range got {
		if p != "/furnished-apartments-vienna" {
			t.Fatalf("Blueground paths = %q, want only the Vienna city page", got)
		}
	}
}
