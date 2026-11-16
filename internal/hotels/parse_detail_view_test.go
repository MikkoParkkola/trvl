package hotels

import (
	"context"
	"encoding/json"
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

// TestParseHotelsFromPage_DetailView: the detail view's place ID lands on the
// same-name ad, which keeps its own price (the entry's price block was for
// other dates and is never read). One Sacher row; other ads stay.
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
	if got.Price != 1002 {
		t.Errorf("Price = %v, want the ad's own 1002, not the detail entry's 478", got.Price)
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

// detailViewPageWith is detailViewPage after mutate edits the decoded data.
func detailViewPageWith(t *testing.T, mutate func(data []any)) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/google_detail_view_sacher.json")
	if err != nil {
		t.Fatal(err)
	}
	var data []any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	mutate(data)
	out, _ := json.Marshal(data)
	return `<html>` + strings.Repeat("<!-- padding -->", 100) + `AF_initDataCallback({key: 'ds:0', data:` + string(out) + `});</html>`
}

func detailEntry(data []any) []any {
	return data[1].([]any)[7].(map[string]any)["441552390"].([]any)
}

// TestParseHotelsFromPage_DetailViewNeedsObservedID: only [26][4] holds the
// place ID. A ChIJ string in another field is not it, and a detail view
// without one adds nothing.
func TestParseHotelsFromPage_DetailViewNeedsObservedID(t *testing.T) {
	page := detailViewPageWith(t, func(data []any) {
		entry := detailEntry(data)
		entry[26] = nil
		entry[20] = []any{"ChIJdecoyInAnotherField"}
	})
	for _, h := range parseHotelsFromPageFull(page, "EUR").Hotels {
		if h.HotelID != "" {
			t.Errorf("%q got HotelID %q, want none without [26][4]", h.Name, h.HotelID)
		}
	}
}

// TestParseHotelsFromPage_DetailViewKeepsSameNameRowID: a same-name organic
// row with its own Google ID and price is not displaced or rewritten.
func TestParseHotelsFromPage_DetailViewKeepsSameNameRowID(t *testing.T) {
	page := detailViewPageWith(t, func(data []any) {
		organic := make([]any, 10)
		organic[1] = "Hotel Sacher Vienna"
		organic[9] = "/g/sacher_organic"
		list := data[0].([]any)[0].([]any)[0].([]any)
		list[1] = append(list[1].([]any), []any{nil, map[string]any{"397419284": []any{organic}}})
	})
	var ids []string
	for _, h := range parseHotelsFromPageFull(page, "EUR").Hotels {
		if h.Name == "Hotel Sacher Vienna" {
			ids = append(ids, h.HotelID)
		}
	}
	if len(ids) != 1 || ids[0] != "/g/sacher_organic" {
		t.Errorf("Hotel Sacher Vienna IDs = %v, want only the organic row's own /g/sacher_organic", ids)
	}
}
