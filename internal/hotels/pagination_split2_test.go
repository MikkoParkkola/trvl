package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestSearchHotelsWithClient_DeduplicatesAcrossPages(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := reqCount.Add(1)
		w.WriteHeader(200)
		switch page {
		case 1:
			_, _ = w.Write(fakeHotelPageMulti("Hotel A", "Hotel B"))
		case 2:
			// Page 2 has one overlap (Hotel B) and one new (Hotel C).
			_, _ = w.Write(fakeHotelPageMulti("Hotel B", "Hotel C"))
		case 3:
			_, _ = w.Write(fakeHotelPageMulti("Hotel D"))
		default:
			// Subsequent sort orders return dupes -> stop early.
			_, _ = w.Write(fakeHotelPageMulti("Hotel A", "Hotel B"))
		}
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have at least 4 unique: A, B, C, D.
	if result.Count < 4 {
		t.Errorf("expected at least 4 unique hotels, got %d", result.Count)
		for _, h := range result.Hotels {
			t.Logf("  got: %s", h.Name)
		}
	}

	// Verify no duplicates in result.
	seen := make(map[string]bool)
	for _, h := range result.Hotels {
		key := strings.ToLower(h.Name)
		if seen[key] {
			t.Errorf("duplicate hotel in result: %s", h.Name)
		}
		seen[key] = true
	}
}

func TestSearchHotelsWithClient_ContinuesOnSecondPageError(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := reqCount.Add(1)
		switch page {
		case 1:
			w.WriteHeader(200)
			_, _ = w.Write(fakeHotelPageMulti("Hotel A", "Hotel B"))
		default:
			// Subsequent pages fail with 403 (non-retryable, fast).
			w.WriteHeader(403)
		}
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still have the first page's results.
	if result.Count != 2 {
		t.Errorf("expected 2 hotels from first page, got %d", result.Count)
	}
}

func TestSearchHotelsWithClient_FirstPageErrorReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use 403 (non-retryable) to avoid retry delays.
		w.WriteHeader(403)
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	_, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err == nil {
		t.Fatal("expected error when first page fails")
	}
}

func TestSearchHotelsWithClient_CaseInsensitiveDedup(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		// Always return these 3 entries. Dedup should collapse them to 2.
		_, _ = w.Write(fakeHotelPageMulti("Hotel Alpha", "HOTEL ALPHA", "Hotel Beta"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 2 unique: Hotel Alpha, Hotel Beta.
	if result.Count != 2 {
		t.Errorf("expected 2 unique hotels (case-insensitive dedup), got %d", result.Count)
		for _, h := range result.Hotels {
			t.Logf("  got: %s", h.Name)
		}
	}
}

// --- Multi-sort diversity ---

func TestSearchHotelsWithClient_SortDiversityAddsUniqueHotels(t *testing.T) {
	// Simulate a server where different sort orders return different hotels.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		sortParam := r.URL.Query().Get("sort")
		switch sortParam {
		case "":
			// Default sort: Hotels A, B
			_, _ = w.Write(fakeHotelPageMulti("Hotel A", "Hotel B"))
		case "3":
			// Highest rated sort: Hotels B, C (B overlaps, C is new)
			_, _ = w.Write(fakeHotelPageMulti("Hotel B", "Hotel C"))
		case "8":
			// Price sort: Hotels D (all new)
			_, _ = w.Write(fakeHotelPageMulti("Hotel D"))
		default:
			_, _ = w.Write(fakeHotelPageMulti("Hotel A"))
		}
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 4 unique hotels: A, B from default + C from sort=3 + D from sort=8.
	if result.Count != 4 {
		t.Errorf("expected 4 unique hotels from sort diversity, got %d", result.Count)
		for _, h := range result.Hotels {
			t.Logf("  got: %s", h.Name)
		}
	}
}

func TestSearchHotelsWithClient_MaxPages1SkipsSortDiversity(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPageMulti("Hotel A"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	opts := defaultOpts()
	opts.MaxPages = 1

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Count != 1 {
		t.Errorf("expected 1 hotel with MaxPages=1, got %d", result.Count)
	}

	// MaxPages=1 should only make 1 request (no pagination, no sort diversity).
	if got := int(reqCount.Load()); got != 1 {
		t.Errorf("expected 1 request with MaxPages=1, got %d", got)
	}
}

func TestGoogleSortOrders(t *testing.T) {
	// Verify the sort orders slice has expected structure.
	if len(googleSortOrders) < 2 {
		t.Errorf("googleSortOrders should have at least 2 entries, got %d", len(googleSortOrders))
	}
	if googleSortOrders[0] != "" {
		t.Errorf("first sort order should be empty (default), got %q", googleSortOrders[0])
	}
}

// --- helpers ---

// defaultOpts returns valid HotelSearchOptions for testing.

func TestSearchHotelsWithClient_FiltersApplyAfterPagination(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := reqCount.Add(1)
		w.WriteHeader(200)

		// Build hotels with prices: page1 has cheap+expensive, page2 has mid.
		switch page {
		case 1:
			_, _ = w.Write(fakeHotelPageWithPrices(
				hotelWithPrice{"Cheap Hotel", 50},
				hotelWithPrice{"Expensive Hotel", 500},
			))
		case 2:
			_, _ = w.Write(fakeHotelPageWithPrices(
				hotelWithPrice{"Mid Hotel", 150},
			))
		default:
			_, _ = w.Write(fakeHotelPageMulti())
		}
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	opts := defaultOpts()
	opts.MinPrice = 100
	opts.MaxPrice = 400

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only Mid Hotel (150) should pass the price filter.
	// Cheap (50) and Expensive (500) are filtered out.
	if result.Count != 1 {
		t.Errorf("expected 1 hotel after filter, got %d", result.Count)
		for _, h := range result.Hotels {
			t.Logf("  got: %s (price=%.0f)", h.Name, h.Price)
		}
	}
	if result.Count == 1 && result.Hotels[0].Name != "Mid Hotel" {
		t.Errorf("expected Mid Hotel, got %s", result.Hotels[0].Name)
	}
}

func TestSearchHotelsWithClient_SourcesTaggedGoogleHotels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPageWithPrices(
			hotelWithPrice{"Grand Hotel", 150},
			hotelWithPrice{"Sea View", 200},
		))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count == 0 {
		t.Fatal("expected at least one hotel")
	}

	for _, h := range result.Hotels {
		if len(h.Sources) == 0 {
			t.Errorf("hotel %q has no Sources — tagHotelSource not wired", h.Name)
			continue
		}
		found := false
		for _, src := range h.Sources {
			if src.Provider == "google_hotels" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("hotel %q Sources does not contain google_hotels provider: %v", h.Name, h.Sources)
		}
	}
}

// Booking.com merge test removed — Booking.com moved to external provider system.
// (Previously-retained hotelHasSource helper deleted 2026-04-15; if future
// multi-source merge tests need it, reintroduce from git history.)

// TestSearchHotelsWithClient_MergePreservesLowestPrice verifies that when the
// same hotel appears in multiple pages/sort orders, the Sources list accumulates
// and the lowest price is kept as the primary. This validates the MergeHotelResults
// dedup behaviour end-to-end through the real search pipeline.

func TestSearchHotelsWithClient_MergePreservesLowestPrice(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := reqCount.Add(1)
		w.WriteHeader(200)
		// "Overlap Hotel" appears on both page 1 and page 2 with different prices.
		switch page {
		case 1:
			_, _ = w.Write(fakeHotelPageWithPrices(
				hotelWithPrice{"Overlap Hotel", 300},
				hotelWithPrice{"Unique A", 100},
			))
		case 2:
			_, _ = w.Write(fakeHotelPageWithPrices(
				hotelWithPrice{"Overlap Hotel", 250}, // cheaper
			))
		default:
			_, _ = w.Write(fakeHotelPageMulti()) // empty — stop pagination
		}
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	opts := defaultOpts()
	opts.MaxPages = 2 // limit to 2 pages per sort, disable sort diversity

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find Overlap Hotel in results.
	var overlap *struct {
		price   float64
		sources int
	}
	for _, h := range result.Hotels {
		if strings.EqualFold(h.Name, "Overlap Hotel") {
			overlap = &struct {
				price   float64
				sources int
			}{h.Price, len(h.Sources)}
			break
		}
	}
	if overlap == nil {
		t.Fatal("Overlap Hotel not found in results")
	}
	// Lowest price (250) should be primary.
	if overlap.price != 250 {
		t.Errorf("expected lowest price 250, got %.0f", overlap.price)
	}
	// Both source appearances should be preserved.
	if overlap.sources < 2 {
		t.Errorf("expected at least 2 sources for merged hotel, got %d", overlap.sources)
	}
}

// TestTagHotelSource verifies that the helper stamps each hotel with the
// correct provider when Sources is empty, and leaves existing Sources unchanged.

func TestTagHotelSource(t *testing.T) {
	input := []models.HotelResult{
		{Name: "Hotel A", Price: 100, Currency: "EUR"},
		{Name: "Hotel B", Price: 200, Currency: "USD", Sources: []models.PriceSource{{Provider: "existing", Price: 200, Currency: "USD"}}},
	}

	tagged := tagHotelSource(input, "google_hotels")

	if len(tagged[0].Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(tagged[0].Sources))
	}
	if tagged[0].Sources[0].Provider != "google_hotels" {
		t.Errorf("expected provider google_hotels, got %q", tagged[0].Sources[0].Provider)
	}
	if tagged[0].Sources[0].Price != 100 {
		t.Errorf("expected price 100, got %.0f", tagged[0].Sources[0].Price)
	}

	// Hotel B already had Sources — must be unchanged.
	if len(tagged[1].Sources) != 1 || tagged[1].Sources[0].Provider != "existing" {
		t.Errorf("hotel with existing Sources should not be modified, got %v", tagged[1].Sources)
	}

	// Original slice must not be mutated.
	if input[0].Sources != nil {
		t.Error("tagHotelSource must not mutate input slice")
	}
}

// --- Verify booking URLs added to paginated results ---

func TestSearchHotelsWithClient_BookingURLsOnAllPages(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := reqCount.Add(1)
		w.WriteHeader(200)
		switch page {
		case 1:
			_, _ = w.Write(fakeHotelPageMulti("Hotel P1"))
		case 2:
			_, _ = w.Write(fakeHotelPageMulti("Hotel P2"))
		default:
			_, _ = w.Write(fakeHotelPageMulti())
		}
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, h := range result.Hotels {
		if h.BookingURL == "" {
			t.Errorf("hotel %q missing BookingURL", h.Name)
		}
		if !strings.Contains(h.BookingURL, "google.com/travel/hotels") {
			t.Errorf("hotel %q has bad BookingURL: %s", h.Name, h.BookingURL)
		}
	}
}
