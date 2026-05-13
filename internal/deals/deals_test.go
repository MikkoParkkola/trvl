package deals

import (
	"strings"
	"testing"
	"time"
)

// --- RSS XML parsing ---

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item>
      <title>Non-stop flights from Rome to Taiwan from EUR595</title>
      <link>https://example.com/deal1</link>
      <pubDate>Thu, 03 Apr 2026 10:00:00 +0000</pubDate>
      <description>&lt;p&gt;Great deal on flights!&lt;/p&gt;</description>
    </item>
    <item>
      <title>Error fare: Helsinki to Tokyo $299 round trip</title>
      <link>https://example.com/deal2</link>
      <pubDate>Thu, 03 Apr 2026 09:00:00 +0000</pubDate>
      <description>Grab this error fare before it is gone</description>
    </item>
    <item>
      <title>$89 — Barcelona to Prague (nonstop)</title>
      <link>https://example.com/deal3</link>
      <pubDate>Thu, 03 Apr 2026 08:00:00 +0000</pubDate>
      <description>Budget flight deal</description>
    </item>
    <item>
      <title>Flash sale: Ryanair HEL-BCN from EUR29</title>
      <link>https://example.com/deal4</link>
      <pubDate>Thu, 03 Apr 2026 07:00:00 +0000</pubDate>
      <description>Flash sale ending soon</description>
    </item>
    <item>
      <title>Holiday package to Bali including hotel + flights from GBP499</title>
      <link>https://example.com/deal5</link>
      <pubDate>Thu, 03 Apr 2026 06:00:00 +0000</pubDate>
      <description>All inclusive package</description>
    </item>
  </channel>
</rss>`

func TestParseRSS_Basic(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}
	if len(deals) != 5 {
		t.Fatalf("expected 5 deals, got %d", len(deals))
	}

	// All deals should have source set.
	for _, d := range deals {
		if d.Source != "test" {
			t.Errorf("source = %q, want test", d.Source)
		}
		if d.URL == "" {
			t.Error("URL should not be empty")
		}
	}
}

func TestParseRSS_PriceExtraction(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}

	tests := []struct {
		idx      int
		price    float64
		currency string
	}{
		{0, 595, "EUR"}, // "from EUR595"
		{1, 299, "USD"}, // "$299"
		{2, 89, "USD"},  // "$89"
		{3, 29, "EUR"},  // "EUR29"
		{4, 499, "GBP"}, // "GBP499"
	}

	for _, tt := range tests {
		d := deals[tt.idx]
		if d.Price != tt.price {
			t.Errorf("deal[%d] (%q): price = %.2f, want %.2f", tt.idx, d.Title, d.Price, tt.price)
		}
		if d.Currency != tt.currency {
			t.Errorf("deal[%d] (%q): currency = %q, want %q", tt.idx, d.Title, d.Currency, tt.currency)
		}
	}
}

func TestParseRSS_RouteExtraction(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}

	tests := []struct {
		idx    int
		origin string
		dest   string
	}{
		{0, "Rome", "Taiwan"},
		{1, "Helsinki", "Tokyo"},
		{2, "Barcelona", "Prague"},
		{3, "HEL", "BCN"},
	}

	for _, tt := range tests {
		d := deals[tt.idx]
		if d.Origin != tt.origin {
			t.Errorf("deal[%d] (%q): origin = %q, want %q", tt.idx, d.Title, d.Origin, tt.origin)
		}
		if d.Destination != tt.dest {
			t.Errorf("deal[%d] (%q): destination = %q, want %q", tt.idx, d.Title, d.Destination, tt.dest)
		}
	}
}

func TestParseRSS_TypeClassification(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}

	tests := []struct {
		idx      int
		dealType string
	}{
		{0, "deal"},
		{1, "error_fare"},
		{2, "deal"},
		{3, "flash_sale"},
		{4, "package"},
	}

	for _, tt := range tests {
		d := deals[tt.idx]
		if d.Type != tt.dealType {
			t.Errorf("deal[%d] (%q): type = %q, want %q", tt.idx, d.Title, d.Type, tt.dealType)
		}
	}
}

func TestParseRSS_AirlineExtraction(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}

	// Deal 3 mentions Ryanair.
	if deals[3].Airline != "Ryanair" {
		t.Errorf("deal[3] airline = %q, want Ryanair", deals[3].Airline)
	}
}

func TestParseRSS_DateParsing(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}

	for i, d := range deals {
		if d.Published.IsZero() {
			t.Errorf("deal[%d] published date is zero", i)
		}
	}

	// First deal should be the earliest (10:00).
	if deals[0].Published.Hour() != 10 {
		t.Errorf("deal[0] hour = %d, want 10", deals[0].Published.Hour())
	}
}

func TestParseRSS_HTMLStripping(t *testing.T) {
	deals, err := ParseRSS([]byte(sampleRSS), "test")
	if err != nil {
		t.Fatalf("ParseRSS error: %v", err)
	}

	// First item has HTML in description.
	if strings.Contains(deals[0].Summary, "<p>") {
		t.Error("summary should not contain HTML tags")
	}
	if !strings.Contains(deals[0].Summary, "Great deal") {
		t.Error("summary should contain stripped text")
	}
}

func TestParseRSS_InvalidXML(t *testing.T) {
	_, err := ParseRSS([]byte("not xml at all"), "test")
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}

func TestParseRSS_EmptyFeed(t *testing.T) {
	xml := `<?xml version="1.0"?><rss version="2.0"><channel><title>Empty</title></channel></rss>`
	deals, err := ParseRSS([]byte(xml), "test")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(deals) != 0 {
		t.Errorf("expected 0 deals, got %d", len(deals))
	}
}

// --- Filtering ---

func TestFilterDeals_ByOrigin(t *testing.T) {
	deals := []Deal{
		{Origin: "HEL", Destination: "BCN", Price: 100, Published: time.Now()},
		{Origin: "AMS", Destination: "BCN", Price: 200, Published: time.Now()},
		{Origin: "HEL", Destination: "ROM", Price: 300, Published: time.Now()},
	}

	filtered := FilterDeals(deals, DealFilter{Origins: []string{"HEL"}})
	if len(filtered) != 2 {
		t.Errorf("expected 2 deals from HEL, got %d", len(filtered))
	}
	for _, d := range filtered {
		if d.Origin != "HEL" {
			t.Errorf("origin = %q, want HEL", d.Origin)
		}
	}
}

func TestFilterDeals_ByOriginCaseInsensitive(t *testing.T) {
	deals := []Deal{
		{Origin: "hel", Destination: "BCN", Price: 100, Published: time.Now()},
	}

	filtered := FilterDeals(deals, DealFilter{Origins: []string{"HEL"}})
	if len(filtered) != 1 {
		t.Errorf("expected 1 deal (case-insensitive), got %d", len(filtered))
	}
}

func TestFilterDeals_ByMaxPrice(t *testing.T) {
	deals := []Deal{
		{Price: 50, Published: time.Now()},
		{Price: 150, Published: time.Now()},
		{Price: 250, Published: time.Now()},
	}

	filtered := FilterDeals(deals, DealFilter{MaxPrice: 200})
	if len(filtered) != 2 {
		t.Errorf("expected 2 deals under 200, got %d", len(filtered))
	}
}

func TestFilterDeals_ByMaxPrice_NoPriceDealsIncluded(t *testing.T) {
	deals := []Deal{
		{Price: 0, Published: time.Now()},   // no price
		{Price: 150, Published: time.Now()}, // under max
		{Price: 250, Published: time.Now()}, // over max
	}

	filtered := FilterDeals(deals, DealFilter{MaxPrice: 200})
	if len(filtered) != 2 {
		t.Errorf("expected 2 deals (no-price + under-max), got %d", len(filtered))
	}
}

func TestFilterDeals_ByType(t *testing.T) {
	deals := []Deal{
		{Type: "error_fare", Published: time.Now()},
		{Type: "deal", Published: time.Now()},
		{Type: "error_fare", Published: time.Now()},
	}

	filtered := FilterDeals(deals, DealFilter{Type: "error_fare"})
	if len(filtered) != 2 {
		t.Errorf("expected 2 error_fare deals, got %d", len(filtered))
	}
}

func TestFilterDeals_ByHoursAgo(t *testing.T) {
	now := time.Now()
	deals := []Deal{
		{Published: now.Add(-1 * time.Hour)},
		{Published: now.Add(-25 * time.Hour)},
		{Published: now.Add(-72 * time.Hour)},
	}

	filtered := FilterDeals(deals, DealFilter{HoursAgo: 24})
	if len(filtered) != 1 {
		t.Errorf("expected 1 deal within 24h, got %d", len(filtered))
	}
}

func TestFilterDeals_DefaultHoursAgo(t *testing.T) {
	now := time.Now()
	deals := []Deal{
		{Published: now.Add(-1 * time.Hour)},
		{Published: now.Add(-72 * time.Hour)},
	}

	// HoursAgo=0 should default to 48.
	filtered := FilterDeals(deals, DealFilter{})
	if len(filtered) != 1 {
		t.Errorf("expected 1 deal within default 48h, got %d", len(filtered))
	}
}

func TestFilterDeals_OriginSkipsNoOriginDeals(t *testing.T) {
	deals := []Deal{
		{Origin: "HEL", Published: time.Now()},
		{Origin: "", Published: time.Now()}, // no origin
	}

	filtered := FilterDeals(deals, DealFilter{Origins: []string{"HEL"}})
	if len(filtered) != 1 {
		t.Errorf("expected 1 deal (skip no-origin), got %d", len(filtered))
	}
}

func TestFilterDeals_NoFilter(t *testing.T) {
	deals := []Deal{
		{Published: time.Now()},
		{Published: time.Now()},
	}

	filtered := FilterDeals(deals, DealFilter{})
	if len(filtered) != 2 {
		t.Errorf("expected 2 deals with no filter, got %d", len(filtered))
	}
}

// --- Price extraction edge cases ---

func TestExtractPrice_DollarVariants(t *testing.T) {
	tests := []struct {
		title    string
		price    float64
		currency string
	}{
		{"Flights from $299", 299, "USD"},
		{"CA$450 to Tokyo", 450, "CAD"},
		{"AU$599 return flights", 599, "AUD"},
		{"From EUR 100 one way", 100, "EUR"},
		{"Flights 299 EUR round trip", 299, "EUR"},
		{"From \u00a3199 return", 199, "GBP"},
	}

	for _, tt := range tests {
		d := Deal{Title: tt.title}
		extractPriceAndRoute(&d)
		if d.Price != tt.price {
			t.Errorf("%q: price = %.2f, want %.2f", tt.title, d.Price, tt.price)
		}
		if d.Currency != tt.currency {
			t.Errorf("%q: currency = %q, want %q", tt.title, d.Currency, tt.currency)
		}
	}
}

func TestExtractRoute_DashPattern(t *testing.T) {
	d := Deal{Title: "Flash sale HEL-BCN from EUR 29"}
	extractPriceAndRoute(&d)
	if d.Origin != "HEL" {
		t.Errorf("origin = %q, want HEL", d.Origin)
	}
	if d.Destination != "BCN" {
		t.Errorf("destination = %q, want BCN", d.Destination)
	}
}

func TestExtractRoute_FromToPattern(t *testing.T) {
	d := Deal{Title: "Cheap flights from London to Barcelona from $99"}
	extractPriceAndRoute(&d)
	if d.Origin != "London" {
		t.Errorf("origin = %q, want London", d.Origin)
	}
	if d.Destination != "Barcelona" {
		t.Errorf("destination = %q, want Barcelona", d.Destination)
	}
}
