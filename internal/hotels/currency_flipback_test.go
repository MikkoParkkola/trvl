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

// stubNoFX makes every cross-currency conversion fail.
func stubNoFX(t *testing.T) {
	t.Helper()
	orig := convertCurrencyFn
	convertCurrencyFn = func(_ context.Context, amount float64, from, _ string) (float64, string) {
		return amount, from
	}
	t.Cleanup(func() { convertCurrencyFn = orig })
}

func requireNotConverted(t *testing.T, h models.HotelResult) {
	t.Helper()
	if h.Price != 6000 || h.Currency != "CZK" {
		t.Errorf("headline = %v %s, want 6000 CZK left as quoted", h.Price, h.Currency)
	}
	if !containsWarning(h, models.PriceWarningCurrencyNotConverted) {
		t.Errorf("warnings = %v, want %s", h.PriceWarnings, models.PriceWarningCurrencyNotConverted)
	}
}

func containsWarning(h models.HotelResult, w string) bool {
	for _, got := range h.PriceWarnings {
		if got == w {
			return true
		}
	}
	return false
}

// TestSearchHotelsWithClient_FailedConversionFlagsHeadline: with no exchange
// rate the CZK headline stays CZK and says it was not converted.
func TestSearchHotelsWithClient_FailedConversionFlagsHeadline(t *testing.T) {
	stubNoFX(t)
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
	if err != nil || len(res.Hotels) != 1 {
		t.Fatalf("search: %v (%d hotels), want 1 hotel", err, len(res.Hotels))
	}
	requireNotConverted(t, res.Hotels[0])
}

// TestSearchHotelsByName_FailedConversionFlagsHeadline: same on the by-name
// path, whose extra merge must not drop the warning.
func TestSearchHotelsByName_FailedConversionFlagsHeadline(t *testing.T) {
	stubNoFX(t)
	freshRateManager(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fakeHotelPageInCurrency("CZK", hotelWithPrice{"Hotel Paris Praha", 6000}))
	}))
	seedGeo(t, "Hotel Paris Praha, Prague")

	got, err := SearchHotelsByName(context.Background(), "Hotel Paris Praha", "Prague", "2026-06-15", "2026-06-18", "EUR")
	if err != nil || len(got) != 1 {
		t.Fatalf("search by name: %v (%d hotels), want 1 hotel", err, len(got))
	}
	requireNotConverted(t, got[0])
}

// TestSearchHotelsWithClient_WarningClearedWhenLaterPassWinsInTarget: the
// first finalize leaves a CZK headline (no rate); enrichment then adds a
// verified EUR room and the second pass picks it. The final headline is in
// the requested currency, so no stale currency_not_converted may remain.
func TestSearchHotelsWithClient_WarningClearedWhenLaterPassWinsInTarget(t *testing.T) {
	stubNoFX(t)
	freshRateManager(t)
	orig := fetchRoomAvailability
	t.Cleanup(func() { fetchRoomAvailability = orig })
	taxIncluded := true
	fetchRoomAvailability = func(context.Context, RoomSearchOptions) (*RoomAvailability, error) {
		return &RoomAvailability{Success: true, Rooms: []RoomType{{
			Name: "Double Room", Price: 150, NightlyPrice: 150, TotalPrice: 450,
			Currency: "EUR", TaxesFeesIncluded: &taxIncluded,
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
	if err != nil || len(res.Hotels) != 1 {
		t.Fatalf("search: %v (%d hotels), want 1 hotel", err, len(res.Hotels))
	}
	h := res.Hotels[0]
	if h.Price != 150 || h.Currency != "EUR" {
		t.Fatalf("headline = %v %s, want the verified 150 EUR room", h.Price, h.Currency)
	}
	if containsWarning(h, models.PriceWarningCurrencyNotConverted) {
		t.Errorf("warnings = %v, stale %s after the EUR room won", h.PriceWarnings, models.PriceWarningCurrencyNotConverted)
	}
}

// TestTrustConverter_LooksUpEachPairOnce: one search finalizes many hotels.
// The rate for a currency pair, including a failed lookup, is fetched once
// per converter, so an unreachable rate service is not retried per hotel.
func TestTrustConverter_LooksUpEachPairOnce(t *testing.T) {
	var calls int
	orig := convertCurrencyFn
	convertCurrencyFn = func(_ context.Context, amount float64, from, to string) (float64, string) {
		calls++
		if from == "CZK" && to == "EUR" {
			return amount / 25, "EUR"
		}
		return amount, from // no rate
	}
	t.Cleanup(func() { convertCurrencyFn = orig })

	conv := trustConverter(context.Background())
	for _, amount := range []float64{6000, 2500, 3000} {
		if got, ok := conv(amount, "CZK", "EUR"); !ok || got != amount/25 {
			t.Fatalf("conv(%v CZK) = %v, %v; want %v EUR", amount, got, ok, amount/25)
		}
	}
	for range 3 {
		if _, ok := conv(10, "GBP", "EUR"); ok {
			t.Fatal("GBP conversion succeeded with no rate")
		}
	}
	if calls != 2 {
		t.Fatalf("rate lookups = %d, want 2 (one per currency pair)", calls)
	}
}

// TestTrustConverter_ZeroAmountDoesNotPoisonPair: a zero or negative amount
// says nothing about the rate, so it must not be remembered as a failure.
func TestTrustConverter_ZeroAmountDoesNotPoisonPair(t *testing.T) {
	stubCZKToEUR(t)
	conv := trustConverter(context.Background())
	_, _ = conv(0, "CZK", "EUR")
	_, _ = conv(-5, "CZK", "EUR")
	if got, ok := conv(6000, "CZK", "EUR"); !ok || got != 240 {
		t.Fatalf("conv(6000 CZK) after zero probes = %v, %v; want 240, true", got, ok)
	}
}
