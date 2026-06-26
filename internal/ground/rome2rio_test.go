package ground

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// loadR2RFixture reads a frozen Rome2Rio SSR capture from testdata.
func loadR2RFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// TestParseRome2Rio_LondonParis asserts the parser extracts the multimodal
// option set from a real SSR capture, with durations and indicative price
// ranges, and that single-mode options are typed by their mode.
func TestParseRome2Rio_LondonParis(t *testing.T) {
	body := loadR2RFixture(t, "rome2rio_london_paris.html")
	routes, _, err := parseRome2Rio(body, "London", "Paris")
	if err != nil {
		t.Fatalf("parseRome2Rio: %v", err)
	}
	if len(routes) < 5 {
		t.Fatalf("expected at least 5 route options, got %d", len(routes))
	}

	// Every option must carry a Rome2Rio booking/deeplink, a provider tag, and at
	// least one of duration or a price hint (otherwise it is not a real option).
	for i, r := range routes {
		if r.Provider != "rome2rio" {
			t.Errorf("route %d provider = %q, want rome2rio", i, r.Provider)
		}
		if r.BookingURL == "" {
			t.Errorf("route %d missing BookingURL", i)
		}
		if r.Duration == 0 && r.Price == 0 {
			t.Errorf("route %d has neither duration nor price: %+v", i, r)
		}
		if len(r.Legs) == 0 {
			t.Errorf("route %d has no legs/mode chain", i)
		}
	}

	// There must be a direct train option (Eurostar) with a sensible duration and
	// a EUR price range.
	var foundTrain bool
	for _, r := range routes {
		if r.Type == "train" && r.Duration > 60 && r.Duration < 360 {
			foundTrain = true
			if r.Currency != "EUR" {
				t.Errorf("train currency = %q, want EUR", r.Currency)
			}
			if r.Price <= 0 || r.PriceMax < r.Price {
				t.Errorf("train price range invalid: lo=%v hi=%v", r.Price, r.PriceMax)
			}
		}
	}
	if !foundTrain {
		t.Error("expected a direct train (Eurostar) option in London->Paris")
	}

	// There must be at least one MULTI-LEG option (e.g. train-to-airport then
	// fly), proving discovery surfaces combinations single-mode providers can't.
	var foundMultiLeg bool
	for _, r := range routes {
		if r.Type == "mixed" && len(r.Legs) >= 2 && r.Transfers >= 1 {
			foundMultiLeg = true
		}
	}
	if !foundMultiLeg {
		t.Error("expected at least one multi-leg (mixed) option in London->Paris")
	}
}

// TestParseRome2Rio_HelsinkiTromso exercises a hard route whose best options are
// genuinely multimodal (ferry+fly, train+night train+bus).
func TestParseRome2Rio_HelsinkiTromso(t *testing.T) {
	body := loadR2RFixture(t, "rome2rio_helsinki_tromso.html")
	routes, _, err := parseRome2Rio(body, "Helsinki", "Tromso")
	if err != nil {
		t.Fatalf("parseRome2Rio: %v", err)
	}
	if len(routes) < 3 {
		t.Fatalf("expected at least 3 options, got %d", len(routes))
	}

	// At least one ferry+fly or train+bus style multi-leg chain.
	var multiLeg int
	for _, r := range routes {
		if r.Type == "mixed" && len(r.Legs) >= 2 {
			multiLeg++
		}
	}
	if multiLeg == 0 {
		t.Fatalf("expected a multi-leg multimodal option for Helsinki->Tromso, got none in %d routes", len(routes))
	}
}

// TestParseRome2Rio_ThinRenderHasNoRoutes asserts that a partial/thin SSR body
// (missing the route data) parses to zero routes rather than fabricating any —
// this is what SearchRome2Rio's retry loop keys off.
func TestParseRome2Rio_ThinRenderHasNoRoutes(t *testing.T) {
	thin := "<html><body><h1>Loading…</h1></body></html>"
	routes, matched, err := parseRome2Rio(thin, "London", "Paris")
	if err != nil {
		t.Fatalf("parseRome2Rio(thin): %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("thin render should yield 0 routes, got %d", len(routes))
	}
	if matched != 0 {
		t.Errorf("thin render should match 0 route anchors, got %d", matched)
	}
}

// TestRome2RioModes verifies the mode-chain derivation from encoded route names.
func TestRome2RioModes(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"Train", []string{"train"}},
		{"Bus", []string{"bus"}},
		{"Drive-Eurotunnel", []string{"drive"}},
		{"Train-to-London-Gatwick-fly-to-Paris-Charles-de-Gaulle", []string{"train", "fly"}},
		{"Ferry-to-Lennart-Meri-fly", []string{"ferry", "fly"}},
	}
	for _, c := range cases {
		got := rome2rioModes(c.name)
		if len(got) != len(c.want) {
			t.Errorf("modes(%q) = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("modes(%q) = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

// TestRome2RioPrice verifies range and single-price parsing incl. comma amounts.
func TestRome2RioPrice(t *testing.T) {
	cases := []struct {
		text     string
		lo, hi   float64
		currency string
	}{
		{"2h 28m €140–250", 140, 250, "EUR"},
		{"€30 - €65", 30, 65, "EUR"},
		{"£1,234", 1234, 1234, "GBP"},
		{"no price here", 0, 0, ""},
	}
	for _, c := range cases {
		lo, hi, cur := rome2rioPrice(c.text)
		if lo != c.lo || hi != c.hi || cur != c.currency {
			t.Errorf("price(%q) = (%v,%v,%q), want (%v,%v,%q)", c.text, lo, hi, cur, c.lo, c.hi, c.currency)
		}
	}
}

// TestRome2RioDuration verifies duration parsing across formats.
func TestRome2RioDuration(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"2h 28m", 148},
		{"23h 28m", 1408},
		{"45 min", 45},
		{"no duration", 0},
	}
	for _, c := range cases {
		if got := rome2rioDuration(c.text); got != c.want {
			t.Errorf("duration(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

// TestSearchRome2Rio_LiveBrowser is a gated live integration test proving the
// --allow-browser-fallbacks path (Tier1 Chrome JA3 + kooky cf_clearance + matching
// UA) actually returns multimodal routes past Cloudflare. Requires
// TRVL_TEST_LIVE_INTEGRATIONS=1 and TRVL_ALLOW_BROWSER_COOKIES=1 (the operator
// must have visited rome2rio.com in an installed browser). Skipped by default.
func TestSearchRome2Rio_LiveBrowser(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("live integration")
	}
	routes, err := SearchRome2Rio(context.Background(), "London", "Paris", true)
	if err != nil {
		t.Fatalf("SearchRome2Rio(allowBrowser=true): %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected live multimodal routes via browser fallback, got none")
	}
	t.Logf("live: %d Rome2Rio route options", len(routes))
}
