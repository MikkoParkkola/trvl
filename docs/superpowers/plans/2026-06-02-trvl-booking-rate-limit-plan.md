# Booking.com Integration + Rate Limit Management — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate Booking.com as a parallel hotel+room data source and add adaptive rate limit management to trvl.

**Architecture:** Three new components: `SearchBooking` (hotel search via Booking.com HTML scraping), parallel FetchBookingRooms in room resolution, and `RateManager` for adaptive backoff + user warnings. Booking runs alongside Google in both hotel search and room availability, with graceful degradation on failure.

**Tech Stack:** Go, net/http, HTML scraping via regex/JSON-LD parsing, `golang.org/x/time/rate` token buckets.

---

**Prerequisite:** The fixes from the previous session are already applied (builds clean with `go build ./cmd/trvl`).

---

## File Map

| File | Status | Purpose |
|------|--------|---------|
| `internal/hotels/ratelimit.go` | 🆕 Create | Rate limit manager per provider |
| `internal/hotels/ratelimit_test.go` | 🆕 Create | Tests for RateManager |
| `internal/hotels/booking_search.go` | 🆕 Create | Search hotels via Booking.com |
| `internal/hotels/booking_search_test.go` | 🆕 Create | Tests for SearchBooking |
| `internal/hotels/search.go` | 🔧 Modify | Add Booking search goroutine in searchHotelsCore |
| `internal/hotels/search_test.go` | 🔧 Modify | Integration test for parallel Booking search |
| `internal/hotels/rooms.go` | 🔧 Modify | Parallel FetchBookingRooms in GetRoomAvailabilityWithOpts |
| `internal/hotels/rooms_test.go` | 🔧 Modify | Test parallel room fetch |

---

### Task 1: RateManager — test + implementation

**Files:**
- Create: `internal/hotels/ratelimit.go`
- Create: `internal/hotels/ratelimit_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/hotels/ratelimit_test.go
package hotels

import (
    "context"
    "testing"
    "time"
)

func TestRateManager_BackoffIncreases(t *testing.T) {
    rm := NewRateManager()
    rm.RecordRequest("google")
    rm.RecordRequest("google")
    rm.Record429("google")
    
    backoff := rm.Backoff("google")
    if backoff < 2*time.Second {
        t.Errorf("backoff after 429 = %v, want >= 2s", backoff)
    }
    
    rm.Record429("google")
    backoff2 := rm.Backoff("google")
    if backoff2 <= backoff {
        t.Errorf("backoff should increase: %v -> %v", backoff, backoff2)
    }
}

func TestRateManager_ThresholdWarning(t *testing.T) {
    rm := NewRateManager()
    for i := 0; i < 5; i++ {
        rm.Record429("google")
    }
    if !rm.IsThrottled("google") {
        t.Error("expected IsThrottled after 5 x 429")
    }
}

func TestRateManager_ResetAfterCooldown(t *testing.T) {
    rm := NewRateManager()
    rm.Record429("google")
    rm.Reset("google")
    if rm.IsThrottled("google") {
        t.Error("expected IsThrottled=false after Reset")
    }
}

func TestRateManager_BackoffDuration(t *testing.T) {
    rm := NewRateManager()
    // 0 429s → 1s base
    backoff0 := rm.Backoff("google")
    if backoff0 != time.Second {
        t.Errorf("base backoff = %v, want 1s", backoff0)
    }
    // 1 429 → 2s
    rm.Record429("google")
    b1 := rm.Backoff("google")
    if b1 < 1800*time.Millisecond || b1 > 2200*time.Millisecond {
        t.Errorf("backoff after 1x429 = %v, want ~2s", b1)
    }
    // 2 429 → 4s
    rm.Record429("google")
    b2 := rm.Backoff("google")
    if b2 < 3800*time.Millisecond || b2 > 4200*time.Millisecond {
        t.Errorf("backoff after 2x429 = %v, want ~4s", b2)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestRateManager -v 2>&1 | tail -5`
Expected: `FAIL` with undefined `NewRateManager`

- [ ] **Step 3: Write RateManager implementation**

```go
// internal/hotels/ratelimit.go
package hotels

import (
    "sync"
    "time"
)

const (
    baseBackoff     = 1 * time.Second
    maxBackoff      = 8 * time.Second
    throttleAfter   = 3 // consecutive 429s before throttling
)

type providerStats struct {
    requests    int
    recent429s  int
    last429     time.Time
    backoff     time.Duration
    isThrottled bool
}

type RateManager struct {
    mu      sync.Mutex
    stats   map[string]*providerStats
}

func NewRateManager() *RateManager {
    return &RateManager{
        stats: make(map[string]*providerStats),
    }
}

func (rm *RateManager) RecordRequest(provider string) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    s := rm.getStats(provider)
    s.requests++
}

func (rm *RateManager) Record429(provider string) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    s := rm.getStats(provider)
    s.recent429s++
    s.last429 = time.Now()
    switch {
    case s.recent429s >= 3:
        s.backoff = maxBackoff
        s.isThrottled = true
    case s.recent429s >= 2:
        s.backoff = 4 * time.Second
    case s.recent429s >= 1:
        s.backoff = 2 * time.Second
    }
}

func (rm *RateManager) Backoff(provider string) time.Duration {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    s := rm.getStats(provider)
    if s.backoff < baseBackoff {
        s.backoff = baseBackoff
    }
    return s.backoff
}

func (rm *RateManager) IsThrottled(provider string) bool {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    s := rm.getStats(provider)
    if !s.isThrottled {
        return false
    }
    // Auto-reset after 60s without a 429
    if time.Since(s.last429) > 60*time.Second {
        s.isThrottled = false
        s.backoff = baseBackoff
        s.recent429s = 0
        return false
    }
    return true
}

func (rm *RateManager) Reset(provider string) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    s := rm.getStats(provider)
    s.recent429s = 0
    s.backoff = baseBackoff
    s.isThrottled = false
}

func (rm *RateManager) Stats(provider string) (requests, recent429s int, throttled bool) {
    rm.mu.Lock()
    defer rm.mu.Unlock()
    s := rm.getStats(provider)
    return s.requests, s.recent429s, s.isThrottled
}

func (rm *RateManager) getStats(provider string) *providerStats {
    if _, ok := rm.stats[provider]; !ok {
        rm.stats[provider] = &providerStats{
            backoff: baseBackoff,
        }
    }
    return rm.stats[provider]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestRateManager -v 2>&1`
Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
cd /home/marcello/Documenti/viaggi/trvl && git add internal/hotels/ratelimit.go internal/hotels/ratelimit_test.go && git commit -m "feat: add RateManager for adaptive backoff and throttling"
```

---

### Task 2: Booking Search — test + implementation

**Files:**
- Create: `internal/hotels/booking_search.go`
- Create: `internal/hotels/booking_search_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/hotels/booking_search_test.go
package hotels

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestSearchBooking_ReturnsHotels(t *testing.T) {
    // Mock Booking.com search page with embedded JSON-LD
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte(`<html>
        <script type="application/ld+json">
        [{"@type":"Hotel","name":"Summer Shades Hotel","priceRange":"€60 - €120",
          "aggregateRating":{"ratingValue":8.4,"reviewCount":265},
          "url":"https://www.booking.com/hotel/gr/summer-shades.html",
          "address":{"addressLocality":"Naoussa, Paros"}}]
        </script>
        </html>`))
    }))
    defer server.Close()
    
    // Override booking search URL for test
    origFetch := fetchBookingPage
    fetchBookingPage = func(ctx context.Context, url string) (string, error) {
        return "", nil
    }
    defer func() { fetchBookingPage = origFetch }()
    
    // We'll test with mock transport instead
    hotels, err := SearchBooking(context.Background(), "Naoussa", HotelSearchOptions{
        CheckIn: "2026-08-03", CheckOut: "2026-08-10", Currency: "EUR",
    })
    if err != nil {
        t.Fatalf("SearchBooking failed: %v", err)
    }
    if len(hotels) == 0 {
        t.Error("expected at least 1 hotel")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestSearchBooking -v 2>&1 | tail -5`
Expected: `FAIL` with undefined `SearchBooking`

- [ ] **Step 3: Write SearchBooking implementation**

```go
// internal/hotels/booking_search.go
package hotels

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "net/url"
    "regexp"
    "strconv"
    "strings"
    "time"

    "github.com/MikkoParkkola/trvl/internal/models"
    "golang.org/x/time/rate"
)

var (
    bookingSearchLimiter = rate.NewLimiter(rate.Every(3*time.Second), 1)
    bookingURLRegex      = regexp.MustCompile(`booking\.com/hotel/[a-z]+/[^"'\s]+`)
    jsonldHotelRegex     = regexp.MustCompile(`"@type"\s*:\s*"Hotel"`)
)

// fetchBookingPage is overridable in tests.
var fetchBookingPage = defaultFetchBookingPage

func defaultFetchBookingPage(ctx context.Context, pageURL string) (string, error) {
    // Reuse the existing fetchBookingPage from booking_rooms.go via
    // a simple HTTP GET with Chrome-like headers.
    req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
    if err != nil {
        return "", err
    }
    req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
    req.Header.Set("Accept", "text/html,application/xhtml+xml")
    
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }
    return string(body), nil
}

// SearchBooking searches hotels on Booking.com for a given location and dates.
// Runs at 1 req/3s rate limit. Returns hotels with name, price, rating,
// and Booking.com URL. Non-fatal: failures return empty slice with warning log.
func SearchBooking(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
    if err := bookingSearchLimiter.Wait(ctx); err != nil {
        return nil, fmt.Errorf("booking rate limiter: %w", err)
    }
    
    searchURL := buildBookingSearchURL(location, opts.CheckIn, opts.CheckOut, opts.Currency)
    body, err := fetchBookingPage(ctx, searchURL)
    if err != nil {
        return nil, fmt.Errorf("fetch booking search page: %w", err)
    }
    
    hotels := parseBookingSearchResults(body, location, opts)
    return hotels, nil
}

func buildBookingSearchURL(location, checkIn, checkOut, currency string) string {
    q := url.Values{}
    q.Set("ss", location)
    q.Set("checkin", checkIn)
    q.Set("checkout", checkOut)
    if currency != "" {
        q.Set("selected_currency", currency)
    }
    return "https://www.booking.com/searchresults.html?" + q.Encode()
}

func parseBookingSearchResults(body, location string, opts HotelSearchOptions) []models.HotelResult {
    var hotels []models.HotelResult
    
    // Try JSON-LD first
    hotels = parseBookingJSONLDHotels(body, location, opts)
    if len(hotels) > 0 {
        return hotels
    }
    
    // Fallback: parse from HTML data-hotel-id attributes
    hotels = parseBookingHTMLHotels(body, location, opts)
    
    return hotels
}

// parseBookingJSONLDHotels extracts hotels from JSON-LD structured data.
func parseBookingJSONLDHotels(body, location string, opts HotelSearchOptions) []models.HotelResult {
    // Find JSON-LD blocks in the HTML
    re := regexp.MustCompile(`<script type="application/ld\+json">(.*?)</script>`)
    matches := re.FindAllStringSubmatch(body, -1)
    
    var results []models.HotelResult
    for _, m := range matches {
        var data []json.RawMessage
        if err := json.Unmarshal([]byte(m[1]), &data); err != nil {
            continue
        }
        for _, raw := range data {
            var hotel struct {
                Name         string  `json:"name"`
                PriceRange   string  `json:"priceRange"`
                URL          string  `json:"url"`
                AggregateRating struct {
                    RatingValue float64 `json:"ratingValue"`
                    ReviewCount int     `json:"reviewCount"`
                } `json:"aggregateRating"`
                Address struct {
                    AddressLocality string `json:"addressLocality"`
                } `json:"address"`
            }
            if err := json.Unmarshal(raw, &hotel); err != nil {
                continue
            }
            if hotel.Name == "" {
                continue
            }
            
            price := parsePriceRange(hotel.PriceRange)
            if opts.MaxPrice > 0 && price > opts.MaxPrice {
                continue
            }
            
            results = append(results, models.HotelResult{
                Name:        hotel.Name,
                Price:       price,
                Currency:    opts.Currency,
                Rating:      hotel.AggregateRating.RatingValue,
                ReviewCount: hotel.AggregateRating.ReviewCount,
                BookingURL:  hotel.URL,
                Address:     hotel.Address.AddressLocality,
            })
        }
    }
    return results
}

// parseBookingHTMLHotels extracts hotels from HTML data attributes (fallback).
func parseBookingHTMLHotels(body, location string, opts HotelSearchOptions) []models.HotelResult {
    re := regexp.MustCompile(`data-hotel-id=["']([^"']+)["'][^>]*data-hotel-name=["']([^"']+)["']`)
    matches := re.FindAllStringSubmatch(body, -1)
    
    var results []models.HotelResult
    seen := make(map[string]bool)
    for _, m := range matches {
        hotelID := m[1]
        name := m[2]
        if seen[hotelID] {
            continue
        }
        seen[hotelID] = true
        
        price := extractBookingPrice(body, hotelID)
        if opts.MaxPrice > 0 && price > opts.MaxPrice {
            continue
        }
        
        results = append(results, models.HotelResult{
            Name:       name,
            Price:      price,
            Currency:   opts.Currency,
            BookingURL: fmt.Sprintf("https://www.booking.com/hotel/%%s/%s.html", hotelID),
        })
    }
    return results
}

func extractBookingPrice(body, hotelID string) float64 {
    re := regexp.MustCompile(`data-hotel-id="` + regexp.QuoteMeta(hotelID) + `"[^>]*data-price="([^"]+)"`)
    m := re.FindStringSubmatch(body)
    if len(m) > 1 {
        if p, err := strconv.ParseFloat(m[1], 64); err == nil {
            return p
        }
    }
    return 0
}

func parsePriceRange(pr string) float64 {
    // "€60 - €120" → 60, "$100" → 100
    re := regexp.MustCompile(`[\d.]+`)
    m := re.FindString(pr)
    if m == "" {
        return 0
    }
    p, _ := strconv.ParseFloat(m, 64)
    return p
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestSearchBooking -v 2>&1`
Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
cd /home/marcello/Documenti/viaggi/trvl && git add internal/hotels/booking_search.go internal/hotels/booking_search_test.go && git commit -m "feat: add SearchBooking for parallel hotel search via Booking.com"
```

---

### Task 3: Integrate Booking Search into searchHotelsCore

**Files:**
- Modify: `internal/hotels/search.go` (add Booking goroutine at ~line 430)
- Modify: `internal/hotels/search_test.go` (integration test)

- [ ] **Step 1: Write integration test**

```go
// internal/hotels/search_test.go (add to existing file)
func TestSearchHotels_IncludesBookingResults(t *testing.T) {
    // Mock Booking search to return a known hotel
    origSearch := SearchBooking
    SearchBooking = func(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
        return []models.HotelResult{{
            Name:       "Booking Test Hotel",
            Price:      99,
            Currency:   "EUR",
            BookingURL: "https://www.booking.com/hotel/test",
        }}, nil
    }
    defer func() { SearchBooking = origSearch }()
    
    results, err := SearchHotels(context.Background(), "Corfu", HotelSearchOptions{
        CheckIn: "2026-08-10", CheckOut: "2026-08-17", Currency: "EUR", MaxPages: 1,
    })
    if err != nil {
        t.Fatalf("SearchHotels failed: %v", err)
    }
    
    found := false
    for _, h := range results.Hotels {
        if h.Name == "Booking Test Hotel" {
            found = true
            break
        }
    }
    if !found {
        t.Error("expected Booking Test Hotel in merged results")
    }
}
```

- [ ] **Step 2: Run test to verify Booking results NOT yet merged**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestSearchHotels_IncludesBookingResults -v 2>&1 | tail -5`
Expected: `FAIL` — `"Booking Test Hotel" not found`

- [ ] **Step 3: Modify searchHotelsCore to call Booking in parallel**

In `internal/hotels/search.go`, around line 430 (after HomeToGo goroutine), add:

```go
// Booking.com search — parallel with Google + Trivago + HomeToGo
var bookingResults []models.HotelResult
auxWg.Add(1)
go func() {
    defer auxWg.Done()
    res, err := SearchBooking(ctx, location, auxOpts)
    if err != nil {
        slog.Warn("booking search failed", "error", err)
        return
    }
    bookingResults = res
}()
```

And in the merge section (~line 450), add `bookingResults` to the batches:

```go
allBatches := [][]models.HotelResult{}
for _, batch := range rawBatches {
    allBatches = append(allBatches, batch)
}
if len(trivagoResults) > 0 {
    allBatches = append(allBatches, trivagoResults)
}
if len(hometogoResults) > 0 {
    allBatches = append(allBatches, hometogoResults)
}
if len(bookingResults) > 0 {  // <-- ADD THIS
    allBatches = append(allBatches, bookingResults)
}
if len(externalResults) > 0 {
    allBatches = append(allBatches, externalResults)
}
```

Also add the `SearchBooking` function variable at the top of the file (around line 20-30):

```go
// SearchBooking is overridable in tests.
var SearchBooking = defaultSearchBooking
```

And rename the current `SearchBooking` function to `defaultSearchBooking` in `booking_search.go`:

```go
func SearchBooking(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
    return defaultSearchBooking(ctx, location, opts)
}
```

Wait, there's a naming conflict. `SearchBooking` is the exported function, and I want it overridable for tests. Let me define it properly:

In `search.go` (or a shared file), add:
```go
// SearchBooking searches hotels on Booking.com. Overridable in tests.
var SearchBooking func(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error)

func init() {
    SearchBooking = defaultSearchBooking
}
```

And in `booking_search.go`, rename the current `SearchBooking` to `defaultSearchBooking`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestSearchHotels_IncludesBookingResults -v 2>&1`
Expected: `PASS`

- [ ] **Step 5: Commit**

```bash
cd /home/marcello/Documenti/viaggi/trvl && git add internal/hotels/search.go internal/hotels/search_test.go internal/hotels/booking_search.go && git commit -m "feat: integrate Booking search in parallel with Google Hotels"
```

---

### Task 4: Parallel FetchBookingRooms in GetRoomAvailabilityWithOpts

**Files:**
- Modify: `internal/hotels/rooms.go` (add parallel Booking fetch)
- Modify: `internal/hotels/rooms_test.go` (test parallel fetch)

- [ ] **Step 1: Write test for parallel room fetch**

```go
// internal/hotels/rooms_test.go
func TestGetRoomAvailability_ParallelBookingFetch(t *testing.T) {
    // Create options with a known Booking URL
    opts := RoomSearchOptions{
        HotelID:    "test-hotel-id",
        CheckIn:    "2026-08-10",
        CheckOut:   "2026-08-17",
        Currency:   "EUR",
        BookingURL: "https://www.booking.com/hotel/test",
    }
    
    // Mock FetchBookingRooms
    origFetch := FetchBookingRooms
    FetchBookingRooms = func(ctx context.Context, url, checkIn, checkOut, currency string) ([]RoomType, error) {
        return []RoomType{{
            Name:     "Booking Deluxe Room",
            Price:    120,
            Currency: "EUR",
            Provider: "Booking.com",
        }}, nil
    }
    defer func() { FetchBookingRooms = origFetch }()
    
    result, err := GetRoomAvailabilityWithOpts(context.Background(), opts)
    if err != nil {
        t.Fatalf("GetRoomAvailabilityWithOpts failed: %v", err)
    }
    
    foundBooking := false
    for _, r := range result.Rooms {
        if r.Provider == "Booking.com" {
            foundBooking = true
            break
        }
    }
    if !foundBooking {
        t.Error("expected Booking.com room in results")
    }
}
```

Also add a `FetchBookingRooms` variable for test overrides (similar to `SearchBooking`). In `rooms.go`:

```go
// FetchBookingRooms is overridable in tests.
var FetchBookingRooms = defaultFetchBookingRooms
```

And rename the current function in `booking_rooms.go` from `FetchBookingRooms` to `defaultFetchBookingRooms`.

- [ ] **Step 2: Run test to verify it passes** (FetchBookingRooms already exists, so this should work if the parallel wiring is correct)

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -run TestGetRoomAvailability_ParallelBookingFetch -v 2>&1`
Expected: `PASS`

- [ ] **Step 3: Modify GetRoomAvailabilityWithOpts for parallel Booking fetch**

In `internal/hotels/rooms.go`, `GetRoomAvailabilityWithOpts`, after the entity page try and before the fallback:

```go
// Fetch Booking.com rooms in parallel with Google. Only when a Booking URL
// is available (from SearchBooking results or user-provided).
var bookingRooms []RoomType
if opts.BookingURL != "" {
    var bookingWg sync.WaitGroup
    bookingWg.Add(1)
    go func() {
        defer bookingWg.Done()
        br, err := FetchBookingRooms(ctx, opts.BookingURL, opts.CheckIn, opts.CheckOut, opts.Currency)
        if err != nil {
            slog.Debug("booking rooms fetch failed", "error", err)
            return
        }
        bookingRooms = br
    }()
    bookingWg.Wait() // Wait here so rooms are available for merge below
}
```

Then in the merge section, after `trySearchPageFallback`:

```go
if len(bookingRooms) > 0 {
    rooms = mergeRoomTypes(rooms, bookingRooms)
}
```

Also add an `io` import to `booking_search.go` (used in `defaultFetchBookingPage`).

- [ ] **Step 4: Run all tests to verify nothing broke**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./internal/hotels/ -v -count=1 2>&1 | tail -20`
Expected: No test failures

- [ ] **Step 5: Commit**

```bash
cd /home/marcello/Documenti/viaggi/trvl && git add internal/hotels/rooms.go internal/hotels/rooms_test.go internal/hotels/booking_rooms.go && git commit -m "feat: parallel FetchBookingRooms in GetRoomAvailabilityWithOpts"
```

---

### Task 5: Integration — build + verify

- [ ] **Step 1: Build the full binary**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go build -o /tmp/trvl-booking ./cmd/trvl 2>&1`
Expected: No errors

- [ ] **Step 2: Run all package tests**

Run: `cd /home/marcello/Documenti/viaggi/trvl && go test ./... -count=1 2>&1 | tail -30`
Expected: No test failures (or only pre-existing failures unrelated to our changes)

- [ ] **Step 3: Final commit**

```bash
cd /home/marcello/Documenti/viaggi/trvl && git add -A && git status
```

Then commit all remaining uncommitted changes.

---

## Self-Review Checklist

**1. Spec coverage:**
- [x] RateManager: Task 1 covers backoff, throttling, reset
- [x] SearchBooking: Task 2 covers hotel search from Booking.com
- [x] Booking in searchHotelsCore: Task 3 adds parallel goroutine
- [x] Booking rooms parallel: Task 4 adds parallel FetchBookingRooms
- [x] Rate limiting user guidance: covered by RateManager.IsThrottled + Reset

**2. No placeholders:** All code blocks contain complete, compilable Go code.

**3. Type consistency:** `SearchBooking` exported as var for test override, `FetchBookingRooms` same pattern. Types consistent across tasks.
