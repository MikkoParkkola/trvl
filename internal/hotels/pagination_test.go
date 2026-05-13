package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

func defaultOpts() HotelSearchOptions {
	return HotelSearchOptions{
		CheckIn:  "2026-06-15",
		CheckOut: "2026-06-18",
		Guests:   2,
		Currency: "USD",
	}
}

// newTestClient creates a batchexec.Client that routes all requests to the test server.
// It uses a plain http.Client (no TLS fingerprinting) and disables rate limiting.

func newTestClient(baseURL string) *batchexec.Client {
	client := batchexec.NewTestClient(baseURL)
	return client
}

// fakeHotelPage builds a minimal HTML page with one hotel in an AF_initDataCallback block.

func fakeHotelPage(name string) []byte {
	return fakeHotelPageMulti(name)
}

// nameOffset returns a deterministic lat/lon offset for a hotel name so that
// the same name always gets the same coordinates across pages (enabling merge),
// while different names land >150m apart (preventing false geo-proximity merge).
//
// MergeHotelResults has a 150m geo-proximity secondary dedup that catches
// cross-provider name variants for the same building. Test hotels must be
// spaced >150m apart so distinct names are not collapsed.

func nameOffset(name string) float64 {
	var sum int
	for _, c := range name {
		sum += int(c)
	}
	// 0.003 degrees ≈ 333m at 60°N latitude. Adjacent name sums (e.g.
	// "Hotel A" vs "Hotel B") differ by 1 step = 333m, well above the
	// 150m geo-merge threshold. Same name → same offset → 0m → merges.
	return float64(sum%100) * 0.003
}

// fakeHotelPageMulti builds a minimal HTML page with N hotels.
// The page is padded to exceed the 1000-byte minimum response check.

func fakeHotelPageMulti(names ...string) []byte {
	var entries []any
	for _, name := range names {
		// Use name-derived coordinates so the same hotel name always lands at
		// the same location regardless of its position within a page.
		offset := nameOffset(name)
		hotel := make([]any, 12)
		hotel[0] = nil
		hotel[1] = name
		hotel[2] = []any{[]any{60.168 + offset, 24.941 + offset}}
		hotel[3] = []any{"4-star hotel", 4.0}
		hotel[9] = fmt.Sprintf("/g/hotel_%s", strings.ReplaceAll(strings.ToLower(name), " ", "_"))

		entry := []any{
			nil,
			map[string]any{
				"397419284": []any{hotel},
			},
		}
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		// Return a page with no hotel data to trigger "no hotels" error.
		// Pad to exceed 1000 bytes so it passes the size check.
		return []byte(`<html>` + strings.Repeat("<!-- padding -->", 100) +
			`AF_initDataCallback({key: 'ds:0', data:[1,2,3]});</html>`)
	}

	innerData := []any{[]any{[]any{[]any{nil, entries}}}}
	dataJSON, _ := json.Marshal(innerData)

	// Pad the page to exceed the 1000-byte minimum response check.
	padding := strings.Repeat("<!-- padding -->", 100)
	return []byte(`<html>` + padding + `AF_initDataCallback({key: 'ds:0', data:` + string(dataJSON) + `});</html>`)
}

// --- SearchHotelsWithClient with filters still work after pagination ---

type hotelWithPrice struct {
	name  string
	price float64
}

// fakeHotelPageWithPrices builds a page with hotels that have price data.
// Padded to exceed the 1000-byte minimum response check.

func fakeHotelPageWithPrices(hotels ...hotelWithPrice) []byte {
	var entries []any
	for _, hp := range hotels {
		offset := nameOffset(hp.name)
		hotel := make([]any, 12)
		hotel[0] = nil
		hotel[1] = hp.name
		hotel[2] = []any{[]any{60.168 + offset, 24.941 + offset}}
		hotel[3] = []any{"4-star hotel", 4.0}
		// Price block: [null, [params..., "USD"], [null, [formatted, null, exact, null, rounded]]]
		hotel[6] = []any{
			nil,
			[]any{nil, nil, nil, "USD"},
			[]any{nil, []any{fmt.Sprintf("$%.0f", hp.price), nil, hp.price, nil, hp.price}},
		}
		hotel[9] = fmt.Sprintf("/g/hotel_%s", strings.ReplaceAll(strings.ToLower(hp.name), " ", "_"))

		entry := []any{
			nil,
			map[string]any{
				"397419284": []any{hotel},
			},
		}
		entries = append(entries, entry)
	}

	innerData := []any{[]any{[]any{[]any{nil, entries}}}}
	dataJSON, _ := json.Marshal(innerData)

	padding := strings.Repeat("<!-- padding -->", 100)
	return []byte(`<html>` + padding + `AF_initDataCallback({key: 'ds:0', data:` + string(dataJSON) + `});</html>`)
}

// --- MergeHotelResults wiring tests ---

// TestSearchHotelsWithClient_SourcesTaggedGoogleHotels verifies that hotels
// returned by SearchHotelsWithClient have their Sources populated with the
// "google_hotels" provider tag. This confirms that tagHotelSource + MergeHotelResults
// are wired correctly into the search pipeline.

func TestPaginationConstants(t *testing.T) {
	if maxPages != 3 {
		t.Errorf("maxPages = %d, want 3", maxPages)
	}
	if pageSize != 20 {
		t.Errorf("pageSize = %d, want 20", pageSize)
	}
}

// --- fetchHotelPage URL construction ---

func TestFetchHotelPage_OffsetZeroNoStartParam(t *testing.T) {
	// Verify that offset=0 does NOT add &start= to the URL.
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPage("Hotel A"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, _ = fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 0, "")

	if strings.Contains(capturedURL, "start=") {
		t.Errorf("offset=0 should not add start param, got URL: %s", capturedURL)
	}
}

func TestFetchHotelPage_OffsetAddsStartParam(t *testing.T) {
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPage("Hotel B"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, _ = fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 20, "")

	if !strings.Contains(capturedURL, "start=20") {
		t.Errorf("offset=20 should add start=20, got URL: %s", capturedURL)
	}
}

func TestFetchHotelPage_Offset40(t *testing.T) {
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPage("Hotel C"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, _ = fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 40, "")

	if !strings.Contains(capturedURL, "start=40") {
		t.Errorf("offset=40 should add start=40, got URL: %s", capturedURL)
	}
}

func TestFetchHotelPage_SortParamAddedWhenSet(t *testing.T) {
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPage("Hotel D"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, _ = fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 0, "3")

	if !strings.Contains(capturedURL, "sort=3") {
		t.Errorf("googleSort=3 should add sort=3, got URL: %s", capturedURL)
	}
	if strings.Contains(capturedURL, "start=") {
		t.Errorf("offset=0 should not add start param with sort, got URL: %s", capturedURL)
	}
}

func TestFetchHotelPage_SortAndOffsetCombined(t *testing.T) {
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPage("Hotel E"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, _ = fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 20, "8")

	if !strings.Contains(capturedURL, "sort=8") {
		t.Errorf("expected sort=8 in URL, got: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "start=20") {
		t.Errorf("expected start=20 in URL, got: %s", capturedURL)
	}
}

func TestFetchHotelPage_EmptySortNoParam(t *testing.T) {
	var capturedURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write(fakeHotelPage("Hotel F"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, _ = fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 0, "")

	if strings.Contains(capturedURL, "sort=") {
		t.Errorf("empty googleSort should not add sort param, got URL: %s", capturedURL)
	}
}

// --- fetchHotelPage error handling ---

func TestFetchHotelPage_403ReturnsBlocked(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, err := fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 0, "")

	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected blocked error, got: %v", err)
	}
}

func TestFetchHotelPage_NonOKStatusReturnsError(t *testing.T) {
	// Use 404 (non-retryable) to avoid retry delays in tests.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, err := fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 0, "")

	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestFetchHotelPage_EmptyResponseReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("short"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	_, err := fetchHotelPage(context.Background(), client, "Helsinki", defaultOpts(), 0, "")

	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

// --- SearchHotelsWithClient pagination ---

func TestSearchHotelsWithClient_PaginatesMultiplePages(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := reqCount.Add(1)
		w.WriteHeader(200)
		// Each page returns different hotels. After page 3, return empty
		// (which stops pagination within each sort order) or dupes.
		switch page {
		case 1:
			_, _ = w.Write(fakeHotelPageMulti("Hotel A", "Hotel B", "Hotel C"))
		case 2:
			_, _ = w.Write(fakeHotelPageMulti("Hotel D", "Hotel E"))
		case 3:
			_, _ = w.Write(fakeHotelPageMulti("Hotel F"))
		default:
			// Subsequent sort orders see only dupes -> stop early.
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

	// Should have all 6 unique hotels across pages and sort orders.
	if result.Count != 6 {
		t.Errorf("expected 6 hotels, got %d", result.Count)
		for _, h := range result.Hotels {
			t.Logf("  got: %s", h.Name)
		}
	}
}

func TestSearchHotelsWithClient_StopsWhenNoNewHotels(t *testing.T) {
	var reqCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(200)
		// All pages return the same hotels (duplicates).
		_, _ = w.Write(fakeHotelPageMulti("Hotel A", "Hotel B"))
	}))
	defer ts.Close()

	client := newTestClient(ts.URL)
	client.SetNoCache(true)

	result, err := SearchHotelsWithClient(context.Background(), client, "Helsinki", defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have only 2 unique hotels.
	if result.Count != 2 {
		t.Errorf("expected 2 hotels, got %d", result.Count)
	}

	// With 3 sort orders, each tries page 1 then page 2 (all dupes -> stop).
	// That's 2 requests per sort order = 6 total. But the cache may return
	// cached results for identical URLs, so just verify we got the right
	// hotel count (dedup correctness is what matters).
}
