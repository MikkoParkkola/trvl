package hotels

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// sacherPlaceID is the hotel's own place ID at [26][4] of the captured detail
// entry; the entry also nests related hotels' IDs deeper inside its price block.
const sacherPlaceID = "ChIJI85wbpwHbUcRiJL_xm_zLE4"

// detailViewPage wraps the trimmed capture of Google's single-hotel answer to
// "Hotel Sacher Wien" (testdata/google_detail_view_sacher.json) in a page.
func detailViewPage(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/google_detail_view_sacher.json")
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("<!-- padding -->", 100)
	return []byte(`<html>` + padding + `AF_initDataCallback({key: 'ds:0', data:` + strings.TrimSpace(string(data)) + `});</html>`)
}

// TestParseHotelsFromPage_DetailView: the detail view's hotel is parsed with
// its own place ID and no price (the entry's price block was for other
// dates), and the same-name ad without an ID is dropped. Other ads stay.
func TestParseHotelsFromPage_DetailView(t *testing.T) {
	pr := parseHotelsFromPageFull(string(detailViewPage(t)), "EUR")

	var sacher []models.HotelResult
	for _, h := range pr.Hotels {
		if h.Name == "Hotel Sacher Vienna" {
			sacher = append(sacher, h)
		}
	}
	if len(sacher) != 1 {
		t.Fatalf("Hotel Sacher Vienna rows = %+v, want exactly one", sacher)
	}
	got := sacher[0]
	if got.HotelID != sacherPlaceID {
		t.Errorf("HotelID = %q, want the entry's own place ID %q", got.HotelID, sacherPlaceID)
	}
	if got.Lat != 48.2040009 || got.Lon != 16.3697963 || got.Stars != 5 {
		t.Errorf("identity = %v,%v %d stars, want 48.2040009,16.3697963 5 stars", got.Lat, got.Lon, got.Stars)
	}
	if got.Price != 0 {
		t.Errorf("Price = %v, want 0: the detail entry's price block is for other dates", got.Price)
	}
	if names := hotelNames(pr.Hotels); len(names) != 2 {
		t.Errorf("hotels = %v, want the Sacher detail hotel and the Stefanie ad", names)
	}
}

// TestSearchHotelByName_DetailViewKeepsPlaceID runs the H1 lookup end to end:
// Google answers with the detail view and Booking with its own row for the
// same hotel. The match must carry the Google place ID the rooms flow needs.
func TestSearchHotelByName_DetailViewKeepsPlaceID(t *testing.T) {
	freshRateManager(t)
	page := detailViewPage(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	}))
	withBookingResult(t, models.HotelResult{
		Name: "Hotel Sacher Wien", Address: "Philharmoniker-Straße 4, 1010 Wien, Austria", HotelID: "4711",
		Price: 480, Currency: "EUR", Lat: 48.2040009, Lon: 16.3697963,
		Sources: []models.PriceSource{{Provider: "booking", Price: 480, Currency: "EUR"}},
	})
	seedGeoAt(t, "Hotel Sacher Wien", 48.2040009, 16.3697963)

	got, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "EUR")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.HotelID != sacherPlaceID {
		t.Fatalf("matched %q with HotelID %q, want the Google place ID %q", got.Name, got.HotelID, sacherPlaceID)
	}
}

// TestSearchHotelByName_DetailViewGoogleOnly: without a Booking row (no
// browser cookies, the common case) the detail view alone resolves the
// place ID.
func TestSearchHotelByName_DetailViewGoogleOnly(t *testing.T) {
	freshRateManager(t)
	page := detailViewPage(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	}))
	seedGeoAt(t, "Hotel Sacher Wien", 48.2040009, 16.3697963)

	got, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "EUR")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.Name != "Hotel Sacher Vienna" || got.HotelID != sacherPlaceID {
		t.Fatalf("matched %q with HotelID %q, want Hotel Sacher Vienna with %q", got.Name, got.HotelID, sacherPlaceID)
	}
}
