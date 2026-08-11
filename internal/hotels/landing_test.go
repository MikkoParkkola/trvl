package hotels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
	"golang.org/x/time/rate"
)

// loadLandingFixture reads the saved Landing _next/data search payload.
func loadLandingFixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/landing_search_austin.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return json.RawMessage(b)
}

func TestResolveLandingBuildRejectsUnrelatedCanonicalDestination(t *testing.T) {
	originalClient, originalLimiter := landingHTTPClient, landingLimiter
	t.Cleanup(func() {
		landingHTTPClient, landingLimiter = originalClient, originalLimiter
	})

	tests := []struct {
		name          string
		effectivePath string
		market        string
		wantError     bool
	}{
		{name: "exact", effectivePath: "/s/austin/apartments/furnished", market: "austin"},
		{name: "canonical suffix", effectivePath: "/s/austin-tx/apartments/furnished", market: "austin-tx"},
		{name: "unrecognized canonical suffix", effectivePath: "/s/austin-heights/apartments/furnished", market: "austin-heights", wantError: true},
		{name: "generic parent", effectivePath: "/s", market: "austin", wantError: true},
		{name: "sibling destination", effectivePath: "/s/dallas-tx/apartments/furnished", market: "dallas-tx", wantError: true},
		{name: "lookalike prefix", effectivePath: "/s/austinite/apartments/furnished", market: "austinite", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			landingLimiter = rate.NewLimiter(rate.Inf, 1)
			landingHTTPClient = &http.Client{Transport: destinationScopeRoundTripper(func(req *http.Request) (*http.Response, error) {
				effective := *req.URL
				effective.Path = tt.effectivePath
				body := `<script id="__NEXT_DATA__" type="application/json">{"buildId":"build-123"}</script>` +
					`{"market":"` + tt.market + `"}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    &http.Request{URL: &effective},
				}, nil
			})}

			buildID, market, err := resolveLandingBuild(context.Background(), "austin")
			if tt.wantError {
				if err == nil {
					t.Fatalf("accepted effective path %q with build=%q market=%q", tt.effectivePath, buildID, market)
				}
				if count := strings.Count(err.Error(), "destination scope:"); count != 1 {
					t.Fatalf("destination scope prefix count = %d in %q, want 1", count, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveLandingBuild: %v", err)
			}
			if buildID != "build-123" || market != tt.market {
				t.Fatalf("got build=%q market=%q", buildID, market)
			}
		})
	}
}

// TestParseLandingHomesFixture asserts the saved Austin search payload maps to
// well-formed HotelResults: name, numeric USD price, address, absolute booking
// + image URLs, apartment/monthly classification, and a "landing" price source.
func TestParseLandingHomesFixture(t *testing.T) {
	hotels, err := parseLandingHomes(loadLandingFixture(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected at least one mapped hotel, got 0")
	}
	for i, h := range hotels {
		if h.Name == "" {
			t.Errorf("hotel %d: empty name", i)
		}
		if h.Price <= 0 {
			t.Errorf("hotel %d (%s): non-positive price %v", i, h.Name, h.Price)
		}
		if h.Currency != "USD" {
			t.Errorf("hotel %d (%s): currency = %q, want USD", i, h.Name, h.Currency)
		}
		if h.PropertyType != "apartment" {
			t.Errorf("hotel %d (%s): property type = %q, want apartment", i, h.Name, h.PropertyType)
		}
		if h.PriceBasis != "monthly" {
			t.Errorf("hotel %d (%s): price basis = %q, want monthly", i, h.Name, h.PriceBasis)
		}
		if !strings.HasPrefix(h.BookingURL, "https://www.hellolanding.com/homes/") {
			t.Errorf("hotel %d (%s): booking URL not a homes link: %q", i, h.Name, h.BookingURL)
		}
		if h.ImageURL != "" && !strings.HasPrefix(h.ImageURL, "https://") {
			t.Errorf("hotel %d (%s): image URL not absolute: %q", i, h.Name, h.ImageURL)
		}
		if len(h.Sources) != 1 || h.Sources[0].Provider != "landing" {
			t.Errorf("hotel %d (%s): want single landing source, got %+v", i, h.Name, h.Sources)
		}
	}

	// First Austin home: fixed deterministic assertions (recon's 1812 example).
	first := hotels[0]
	if first.Name != "Waters Edge 2308" {
		t.Errorf("first name = %q, want %q", first.Name, "Waters Edge 2308")
	}
	if first.Price != 1812 {
		t.Errorf("first price = %v, want 1812", first.Price)
	}
	if first.HotelID != "landing-1416687" {
		t.Errorf("first id = %q, want landing-1416687", first.HotelID)
	}
	if first.BookingURL != "https://www.hellolanding.com/homes/apartment-in-salado-tx-waters-edge-2308" {
		t.Errorf("first booking URL = %q", first.BookingURL)
	}
	if !strings.Contains(first.Description, "1 bedroom") {
		t.Errorf("first description = %q, want bedroom info", first.Description)
	}
}

// TestParseLandingHomesSkipsUnpriced ensures homes with no usable price and
// empty groups are dropped, and price preference (price > discount > rent) holds.
func TestParseLandingHomesSkipsUnpriced(t *testing.T) {
	raw := json.RawMessage(`{"pageProps":{"serverData":{"home_groups":[
		{"key":"main","homes":[
			{"id":1,"name":"Priced","price":1500,"slug":"a","bedrooms":1},
			{"id":2,"name":"Discount only","discount_price":1400,"slug":"b","bedrooms":2},
			{"id":3,"name":"Rent only","monthly_rent":1300,"slug":"c","bedrooms":0},
			{"id":4,"name":"No price","slug":"d"},
			{"id":5,"name":"Zero","price":0,"slug":"e"}
		]},
		{"key":"future","homes":[]}
	]}}}`)
	hotels, err := parseLandingHomes(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hotels) != 3 {
		t.Fatalf("want 3 priced homes, got %d", len(hotels))
	}
	if hotels[0].Price != 1500 || hotels[1].Price != 1400 || hotels[2].Price != 1300 {
		t.Errorf("price preference wrong: %v / %v / %v", hotels[0].Price, hotels[1].Price, hotels[2].Price)
	}
	if hotels[2].Description != "Studio" {
		t.Errorf("zero-bedroom should read Studio, got %q", hotels[2].Description)
	}
}

func TestLandingSlug(t *testing.T) {
	cases := map[string]string{
		"Austin":        "austin",
		"Austin, TX":    "austin-tx",
		"  Austin  ":    "austin",
		"New York, NY":  "new-york-ny",
		"St. Louis, MO": "st-louis-mo",
		"":              "",
	}
	for in, want := range cases {
		if got := landingSlug(in); got != want {
			t.Errorf("landingSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLandingPrice(t *testing.T) {
	cases := []struct {
		h    landingHome
		want float64
	}{
		{landingHome{Price: 1812, DiscountPrice: 1700, MonthlyRent: 2000}, 1812},
		{landingHome{DiscountPrice: 1700, MonthlyRent: 2000}, 1700},
		{landingHome{MonthlyRent: 2000}, 2000},
		{landingHome{}, 0},
	}
	for i, c := range cases {
		if got := landingPrice(c.h); got != c.want {
			t.Errorf("case %d: landingPrice = %v, want %v", i, got, c.want)
		}
	}
}

func TestLandingImageURL(t *testing.T) {
	if got := landingImageURL(landingHome{HomeImage: "https://files/x.jpg"}); got != "https://files/x.jpg" {
		t.Errorf("home_image preferred, got %q", got)
	}
	if got := landingImageURL(landingHome{HomeImages: []landingImage{{URL: "https://files/y.jpg"}}}); got != "https://files/y.jpg" {
		t.Errorf("home_images fallback, got %q", got)
	}
	if got := landingImageURL(landingHome{DynamicImages: []landingDynImage{{DynamicImageURL: "https://files/z.jpg"}}}); got != "https://files/z.jpg" {
		t.Errorf("dynamic_images fallback, got %q", got)
	}
	if got := landingImageURL(landingHome{}); got != "" {
		t.Errorf("no image -> empty, got %q", got)
	}
}

func TestLandingExtractBuildID(t *testing.T) {
	html, err := os.ReadFile("testdata/landing_city_austin.html")
	if err != nil {
		t.Fatalf("read html fixture: %v", err)
	}
	if got := landingExtractBuildID(html); got != "qUBGx-cGXFChLQ0lNmkV2" {
		t.Errorf("buildId = %q, want qUBGx-cGXFChLQ0lNmkV2", got)
	}
	// Regex fallback when the script blob shape is unexpected.
	if got := landingExtractBuildID([]byte(`x "buildId":"abc123XYZ" y`)); got != "abc123XYZ" {
		t.Errorf("regex fallback buildId = %q, want abc123XYZ", got)
	}
	if got := landingExtractBuildID([]byte(`no build id here`)); got != "" {
		t.Errorf("missing buildId -> empty, got %q", got)
	}
}

// TestSearchLandingEndToEnd wires resolve (city HTML) + fetch (_next/data JSON)
// against a mock server, exercising the full SearchLanding pipeline.
func TestSearchLandingEndToEnd(t *testing.T) {
	html, err := os.ReadFile("testdata/landing_city_austin.html")
	if err != nil {
		t.Fatalf("read html fixture: %v", err)
	}
	searchJSON, err := os.ReadFile("testdata/landing_search_austin.json")
	if err != nil {
		t.Fatalf("read search fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/s/austin/apartments/furnished":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(html)
		case strings.HasPrefix(r.URL.Path, "/_next/data/qUBGx-cGXFChLQ0lNmkV2/s/austin-tx/apartments/furnished.json"):
			if r.URL.Query().Get("searchType") != "furnished" {
				http.Error(w, "want searchType=furnished", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(searchJSON)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	origURL, origEnabled := landingBaseURL, landingEnabled
	landingBaseURL = srv.URL
	landingEnabled = true
	defer func() { landingBaseURL, landingEnabled = origURL, origEnabled }()

	hotels, err := SearchLanding(context.Background(), "Austin", HotelSearchOptions{})
	if err != nil {
		t.Fatalf("SearchLanding: %v", err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected results from end-to-end search")
	}
	if hotels[0].Sources[0].Provider != "landing" {
		t.Errorf("source provider = %q, want landing", hotels[0].Sources[0].Provider)
	}
	if hotels[0].Price != 1812 {
		t.Errorf("first price = %v, want 1812", hotels[0].Price)
	}

	// Disabled -> nil, nil.
	landingEnabled = false
	got, err := SearchLanding(context.Background(), "Austin", HotelSearchOptions{})
	if err != nil || got != nil {
		t.Errorf("disabled SearchLanding = (%v,%v), want (nil,nil)", got, err)
	}
}

// TestSearchLandingLive hits the real Landing endpoints. Opt-in only: gated
// behind TRVL_TEST_LIVE_INTEGRATIONS=1 and skipped by -short.
func TestSearchLandingLive(t *testing.T) {
	testutil.RequireLiveIntegration(t)

	origEnabled := landingEnabled
	landingEnabled = true
	defer func() { landingEnabled = origEnabled }()

	hotels, err := SearchLanding(context.Background(), "Austin, TX", HotelSearchOptions{})
	if err != nil {
		// Landing is a non-fatal aux provider (see search.go runAux): a live
		// transport block — hellolanding.com now serves 403 anti-bot
		// challenges to non-browser clients — is isolated in production and
		// contributes zero results, never breaking the multi-provider hotel
		// search. Mirror the easyJet AKAMAI_BLOCK / Anyplace precedent: skip on
		// an upstream transport failure we do not control rather than fail CI on
		// it. The shipped parser contract stays guarded offline by
		// TestSearchLandingEndToEnd + TestParseLandingHomesFixture.
		t.Skipf("landing live endpoint unavailable (likely anti-bot block): %v", err)
	}
	if len(hotels) == 0 {
		// A 200 that parses to zero homes is a real parser regression, not an
		// upstream block — keep failing hard on it.
		t.Fatal("live Landing returned 200 but zero hotels — parser regression")
	}
	t.Logf("live Landing returned %d homes; first: %s @ %.0f %s/mo — candidate to confirm still healthy",
		len(hotels), hotels[0].Name, hotels[0].Price, hotels[0].Currency)
}
