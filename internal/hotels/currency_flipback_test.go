package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// stubCZKToEUR replaces the live exchange-rate lookup: 25 CZK = 1 EUR, same
// currency passes through, anything else reports failure (status = from).
func stubCZKToEUR(t *testing.T) {
	t.Helper()
	orig := convertCurrencyFn
	convertCurrencyFn = func(_ context.Context, amount float64, from, to string) (float64, string) {
		switch {
		case from == to:
			return amount, to
		case from == "CZK" && to == "EUR":
			return amount / 25, "EUR"
		}
		return amount, from
	}
	t.Cleanup(func() { convertCurrencyFn = orig })
}

// seedGeo pins ResolveLocation for query so no geocoder is called. The
// coordinates match the fixture hotels (Helsinki), keeping them inside the
// 100 km outlier ceiling.
func seedGeo(t *testing.T, query string) {
	t.Helper()
	geoCache.Lock()
	geoCache.entries[query] = geoEntry{lat: 60.1699, lon: 24.9384}
	geoCache.Unlock()
	t.Cleanup(func() {
		geoCache.Lock()
		delete(geoCache.entries, query)
		geoCache.Unlock()
	})
}

func freshRateManager(t *testing.T) {
	t.Helper()
	orig := HotelRateManager
	HotelRateManager = NewRateManager()
	t.Cleanup(func() { HotelRateManager = orig })
}

func requireEURHeadlineCZKSource(t *testing.T, h models.HotelResult) {
	t.Helper()
	if h.Price != 240 || h.Currency != "EUR" {
		t.Errorf("headline = %v %s, want 240 EUR (the 6000 CZK quote converted)", h.Price, h.Currency)
	}
	var found bool
	for _, s := range h.Sources {
		if s.Provider == "google_hotels" {
			found = true
			if s.Price != 6000 || s.Currency != "CZK" {
				t.Errorf("google_hotels source = %v %s, want the provider quote 6000 CZK", s.Price, s.Currency)
			}
		}
	}
	if !found {
		t.Errorf("sources = %+v, want a google_hotels source", h.Sources)
	}
}

// TestSearchHotelsWithClient_HeadlineStaysInRequestedCurrency: Google prices
// the hotel in CZK, the user asked for EUR. The headline is EUR; the source
// stays the CZK provider quote. Before D12 the post-merge finalize restored
// 6000 CZK as the headline because no EUR source existed.
func TestSearchHotelsWithClient_HeadlineStaysInRequestedCurrency(t *testing.T) {
	stubCZKToEUR(t)
	freshRateManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakeHotelPageInCurrency("CZK", hotelWithPrice{"Hotel Paris Praha", 6000}))
	}))
	defer srv.Close()
	client := newTestClient(srv.URL)
	client.SetNoCache(true)

	opts := defaultOpts()
	opts.Currency = "EUR"
	opts.MaxPages = 1
	res, err := SearchHotelsWithClient(context.Background(), client, "Prague", opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(res.Hotels))
	}
	requireEURHeadlineCZKSource(t, res.Hotels[0])
}

// TestSearchHotelsByName_HeadlineStaysInRequestedCurrency: the by-name path
// merges again after the search (MergeHotelResults finalizes with no
// requested currency), so it must finalize in the requested currency too.
func TestSearchHotelsByName_HeadlineStaysInRequestedCurrency(t *testing.T) {
	stubCZKToEUR(t)
	freshRateManager(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakeHotelPageInCurrency("CZK", hotelWithPrice{"Hotel Paris Praha", 6000}))
	}))
	seedGeo(t, "Hotel Paris Praha, Prague")

	got, err := SearchHotelsByName(context.Background(), "Hotel Paris Praha", "Prague", "2026-06-15", "2026-06-18", "EUR")
	if err != nil {
		t.Fatalf("search by name: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d hotels, want 1", len(got))
	}
	requireEURHeadlineCZKSource(t, got[0])
}

// TestSearchHotelsWithClient_EnrichedRoomHeadlineStaysInRequestedCurrency:
// room enrichment adds a verified CZK room quote after the first finalize,
// and the search finalizes again. That second pass must also convert: the
// verified 3000 CZK nightly room becomes a 120 EUR headline.
func TestSearchHotelsWithClient_EnrichedRoomHeadlineStaysInRequestedCurrency(t *testing.T) {
	stubCZKToEUR(t)
	freshRateManager(t)
	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })
	taxIncluded := true
	fetchRoomAvailability = func(context.Context, RoomSearchOptions) (*RoomAvailability, error) {
		return &RoomAvailability{Success: true, Rooms: []RoomType{{
			Name: "Double Room", Price: 3000, NightlyPrice: 3000, TotalPrice: 9000,
			Currency: "CZK", TaxesFeesIncluded: &taxIncluded,
		}}}, nil
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakeHotelPageInCurrency("CZK", hotelWithPrice{"Hotel Paris Praha", 6000}))
	}))
	defer srv.Close()
	client := newTestClient(srv.URL)
	client.SetNoCache(true)

	opts := defaultOpts()
	opts.Currency = "EUR"
	opts.MaxPages = 1
	opts.EnrichRooms = true
	opts.EnrichLimit = 1
	res, err := SearchHotelsWithClient(context.Background(), client, "Prague", opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Hotels) != 1 {
		t.Fatalf("got %d hotels, want 1", len(res.Hotels))
	}
	h := res.Hotels[0]
	var roomSource bool
	for _, s := range h.Sources {
		if s.Price == 3000 && s.Currency == "CZK" && s.PriceConfidence == models.PriceConfidenceVerified {
			roomSource = true
		}
	}
	if !roomSource {
		t.Fatalf("precondition: sources = %+v, want the verified 3000 CZK room quote from enrichment", h.Sources)
	}
	if h.Price != 120 || h.Currency != "EUR" || h.PriceConfidence != models.PriceConfidenceVerified {
		t.Errorf("headline = %v %s/%s, want 120 EUR verified (the 3000 CZK room converted)", h.Price, h.Currency, h.PriceConfidence)
	}
}
