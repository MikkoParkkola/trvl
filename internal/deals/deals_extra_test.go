package deals

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClassifyDeal_ErrorFare(t *testing.T) {
	d := Deal{Title: "Mistake fare: New York to Paris $150"}
	classifyDeal(&d)
	if d.Type != "error_fare" {
		t.Errorf("type = %q, want error_fare", d.Type)
	}
}

func TestClassifyDeal_FlashSale(t *testing.T) {
	d := Deal{Title: "Flash deal: London to Rome from EUR49"}
	classifyDeal(&d)
	if d.Type != "flash_sale" {
		t.Errorf("type = %q, want flash_sale", d.Type)
	}
}

func TestClassifyDeal_Package(t *testing.T) {
	d := Deal{Title: "Holiday in Greece including hotel + flights"}
	classifyDeal(&d)
	if d.Type != "package" {
		t.Errorf("type = %q, want package", d.Type)
	}
}

func TestClassifyDeal_Default(t *testing.T) {
	d := Deal{Title: "Cheap flights to Tokyo"}
	classifyDeal(&d)
	if d.Type != "deal" {
		t.Errorf("type = %q, want deal", d.Type)
	}
}

// --- Concurrent fetch (mock HTTP) ---

func TestFetchDeals_MockHTTP(t *testing.T) {
	// Create a mock RSS server.
	var hitCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, sampleRSS)
	}))
	defer srv.Close()

	// Override source feeds to point to mock server.
	origFeeds := make(map[string]string)
	for k, v := range SourceFeeds {
		origFeeds[k] = v
	}
	for k := range SourceFeeds {
		SourceFeeds[k] = srv.URL
	}
	defer func() {
		for k, v := range origFeeds {
			SourceFeeds[k] = v
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Exclude "google" source — it uses live API, not the mock HTTP server.
	rssSources := make([]string, 0, len(AllSources))
	for _, s := range AllSources {
		if s != "google" {
			rssSources = append(rssSources, s)
		}
	}
	result, err := FetchDeals(ctx, rssSources, DealFilter{HoursAgo: 999999})
	if err != nil {
		t.Fatalf("FetchDeals error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result not successful: %s", result.Error)
	}

	// Should have fetched from all 4 sources.
	if hitCount.Load() != 4 {
		t.Errorf("expected 4 HTTP requests, got %d", hitCount.Load())
	}

	// Each source returns 5 items, total = 20.
	if result.Count != 20 {
		t.Errorf("expected 20 deals, got %d", result.Count)
	}

	// Verify deals are sorted by published date descending.
	for i := 1; i < len(result.Deals); i++ {
		if result.Deals[i].Published.After(result.Deals[i-1].Published) {
			t.Error("deals should be sorted newest first")
			break
		}
	}
}

func TestFetchDeals_UnknownSource(t *testing.T) {
	ctx := context.Background()
	result, err := FetchDeals(ctx, []string{"nonexistent"}, DealFilter{HoursAgo: 999999})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Success {
		t.Error("should not be successful with unknown source and no deals")
	}
}

func TestFetchDeals_EmptySourcesDefaultsToAll(t *testing.T) {
	// Create a mock server that returns empty feed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`)
	}))
	defer srv.Close()

	origFeeds := make(map[string]string)
	for k, v := range SourceFeeds {
		origFeeds[k] = v
	}
	for k := range SourceFeeds {
		SourceFeeds[k] = srv.URL
	}
	defer func() {
		for k, v := range origFeeds {
			SourceFeeds[k] = v
		}
	}()

	ctx := context.Background()
	result, err := FetchDeals(ctx, nil, DealFilter{HoursAgo: 999999})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// Should succeed even with empty feeds.
	if !result.Success {
		t.Error("should succeed with empty feeds")
	}
}

// --- Helper tests ---

func TestStripHTML(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<b>Bold</b> &amp; <i>italic</i>", "Bold & italic"},
		{"No tags here", "No tags here"},
		{"  Multiple   spaces  ", "Multiple spaces"},
	}

	for _, tt := range tests {
		got := stripHTML(tt.input)
		if got != tt.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseRSSDate(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"Thu, 03 Apr 2026 10:00:00 +0000", true},
		{"Mon, 2 Jan 2006 15:04:05 -0700", true},
		{"2026-04-03T10:00:00Z", true},
		{"not a date", false},
		{"", false},
	}

	for _, tt := range tests {
		got := parseRSSDate(tt.input)
		if tt.valid && got.IsZero() {
			t.Errorf("parseRSSDate(%q) returned zero time, expected valid", tt.input)
		}
		if !tt.valid && !got.IsZero() {
			t.Errorf("parseRSSDate(%q) returned non-zero time, expected zero", tt.input)
		}
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"299", 299},
		{"99.99", 99.99},
		{"0", 0},
		{"abc", 0},
	}

	for _, tt := range tests {
		got := parseFloat(tt.input)
		if got != tt.want {
			t.Errorf("parseFloat(%q) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

// --- Data types ---

func TestAllSources(t *testing.T) {
	if len(AllSources) != 5 {
		t.Errorf("AllSources length = %d, want 5", len(AllSources))
	}

	for _, src := range AllSources {
		// "google" uses fetchGoogleExplore, not SourceFeeds.
		if src != "google" {
			if _, ok := SourceFeeds[src]; !ok {
				t.Errorf("source %q missing from SourceFeeds", src)
			}
		}
		if _, ok := SourceNames[src]; !ok {
			t.Errorf("source %q missing from SourceNames", src)
		}
	}
}

func TestDealFilter_ZeroValue(t *testing.T) {
	f := DealFilter{}
	if f.MaxPrice != 0 || f.Type != "" || f.HoursAgo != 0 || len(f.Origins) != 0 {
		t.Error("zero-value DealFilter should have all defaults")
	}
}

// --- Category extraction ---

func TestExtractFromCategories_Route(t *testing.T) {
	d := Deal{Title: "Cheap flights"}
	extractFromCategories(&d, []string{"Burbank, USA → Vancouver, Canada"})
	if d.Origin != "Burbank" {
		t.Errorf("origin = %q, want Burbank", d.Origin)
	}
	if d.Destination != "Vancouver" {
		t.Errorf("destination = %q, want Vancouver", d.Destination)
	}
}

func TestExtractFromCategories_RouteDoesNotOverwriteTitle(t *testing.T) {
	d := Deal{Title: "Flights from Rome to Tokyo $299"}
	extractPriceAndRoute(&d)
	// Title already extracted Rome->Tokyo.
	extractFromCategories(&d, []string{"Rome, Italy → Tokyo, Japan"})
	if d.Origin != "Rome" {
		t.Errorf("origin = %q, want Rome (should not overwrite)", d.Origin)
	}
	if d.Destination != "Tokyo" {
		t.Errorf("destination = %q, want Tokyo (should not overwrite)", d.Destination)
	}
}

func TestExtractFromCategories_Airline(t *testing.T) {
	d := Deal{Title: "Cheap flights"}
	extractFromCategories(&d, []string{"United", "Deal"})
	// "United" alone doesn't match the full airline regex; the category should be exact.
	// But "United Airlines" style entries should work:
	d2 := Deal{Title: "Cheap flights"}
	extractFromCategories(&d2, []string{"Qatar Airways", "Deal"})
	if d2.Airline != "Qatar Airways" {
		t.Errorf("airline = %q, want Qatar Airways", d2.Airline)
	}
}

func TestExtractFromCategories_Stops(t *testing.T) {
	tests := []struct {
		cat  string
		want string
	}{
		{"Non-stop", "nonstop"},
		{"Nonstop", "nonstop"},
		{"1 Stop", "1 stop"},
		{"2 Stops", "2 stops"},
	}
	for _, tt := range tests {
		d := Deal{Title: "Flights"}
		extractFromCategories(&d, []string{tt.cat})
		if d.Stops != tt.want {
			t.Errorf("category %q: stops = %q, want %q", tt.cat, d.Stops, tt.want)
		}
	}
}

func TestExtractFromCategories_CabinClass(t *testing.T) {
	tests := []struct {
		cat  string
		want string
	}{
		{"Economy", "economy"},
		{"Business Class", "business"},
		{"First Class", "first"},
		{"Premium Economy", "premium_economy"},
	}
	for _, tt := range tests {
		d := Deal{Title: "Flights"}
		extractFromCategories(&d, []string{tt.cat})
		if d.CabinClass != tt.want {
			t.Errorf("category %q: cabin = %q, want %q", tt.cat, d.CabinClass, tt.want)
		}
	}
}

func TestExtractFromCategories_ErrorFareType(t *testing.T) {
	d := Deal{Title: "Flights to Paris"}
	extractFromCategories(&d, []string{"Error Fare"})
	classifyDeal(&d)
	if d.Type != "error_fare" {
		t.Errorf("type = %q, want error_fare", d.Type)
	}
}

func TestExtractStopsFromTitle(t *testing.T) {
	d := Deal{Title: "Non-stop flights from Rome to Taiwan from EUR595"}
	extractFromCategories(&d, nil)
	if d.Stops != "nonstop" {
		t.Errorf("stops = %q, want nonstop", d.Stops)
	}
}

func TestExtractCabinFromTitle(t *testing.T) {
	d := Deal{Title: "Business class flights HEL-NRT from EUR1200"}
	extractFromCategories(&d, nil)
	if d.CabinClass != "business" {
		t.Errorf("cabin = %q, want business", d.CabinClass)
	}
}

// --- Date range extraction ---

func TestExtractDateRange(t *testing.T) {
	tests := []struct {
		desc string
		want string
	}{
		{
			"Great deal! Travel from April 2026 to January 2027 for cheap.",
			"April 2026 to January 2027",
		},
		{
			"Fly from May to September 2026.",
			"May to September 2026",
		},
		{
			"No date info here",
			"",
		},
	}
	for _, tt := range tests {
		d := Deal{}
		extractDateRange(&d, tt.desc)
		if d.DateRange != tt.want {
			t.Errorf("desc=%q: dateRange = %q, want %q", tt.desc, d.DateRange, tt.want)
		}
	}
}

// --- RSS with categories ---

const sampleRSSWithCategories = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>Cheap flights from $399</title>
      <link>https://example.com/deal1</link>
      <pubDate>Thu, 03 Apr 2026 10:00:00 +0000</pubDate>
      <description>Travel April 2026 to January 2027</description>
      <category>Burbank, USA → Vancouver, Canada</category>
      <category>Qatar Airways</category>
      <category>Non-stop</category>
      <category>Business Class</category>
      <category>Error Fare</category>
    </item>
  </channel>
</rss>`

func TestParseRSS_WithCategories(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSSWithCategories), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}
	if len(deals) != 1 {
		t.Fatalf("expected 1 deal, got %d", len(deals))
	}

	d := deals[0]
	if d.Origin != "Burbank" {
		t.Errorf("origin = %q, want Burbank", d.Origin)
	}
	if d.Destination != "Vancouver" {
		t.Errorf("destination = %q, want Vancouver", d.Destination)
	}
	if d.Airline != "Qatar Airways" {
		t.Errorf("airline = %q, want Qatar Airways", d.Airline)
	}
	if d.Stops != "nonstop" {
		t.Errorf("stops = %q, want nonstop", d.Stops)
	}
	if d.CabinClass != "business" {
		t.Errorf("cabin = %q, want business", d.CabinClass)
	}
	if d.Type != "error_fare" {
		t.Errorf("type = %q, want error_fare", d.Type)
	}
	if d.DateRange != "April 2026 to January 2027" {
		t.Errorf("dateRange = %q, want 'April 2026 to January 2027'", d.DateRange)
	}
	if d.Price != 399 {
		t.Errorf("price = %.2f, want 399", d.Price)
	}
}
