package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// serveFixture spins up an httptest server returning the given fixture file for
// any path, and returns the server (caller closes it).
func serveFixtureFile(t *testing.T, path, contentType string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}))
}

func TestParseSpotahomeData_Fixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/spotahome_lisbon.data")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	results, err := parseSpotahomeData(raw, "lisbon", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) != 48 {
		t.Fatalf("expected 48 cards, got %d", len(results))
	}
	h := results[0]
	if h.HotelID == "" || h.Price <= 0 {
		t.Fatalf("first result missing id/price: %+v", h)
	}
	if h.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", h.Currency)
	}
	// coord was [lon, lat]; Lisbon lat ~38.7, lon ~-9.1 after swap.
	if !(h.Lat > 38 && h.Lat < 39) || !(h.Lon > -10 && h.Lon < -8) {
		t.Errorf("lat/lon not swapped correctly: lat=%f lon=%f", h.Lat, h.Lon)
	}
	if !strings.HasPrefix(h.BookingURL, "https://www.spotahome.com/lisbon/for-rent:apartments/") {
		t.Errorf("booking url = %q", h.BookingURL)
	}
	if h.PropertyType != "apartment" {
		t.Errorf("property type = %q", h.PropertyType)
	}
	if len(h.Sources) != 1 || h.Sources[0].Provider != "spotahome" {
		t.Errorf("sources = %+v", h.Sources)
	}
}

func TestSearchSpotahome_MockServer(t *testing.T) {
	ts := serveFixtureFile(t, "testdata/spotahome_lisbon.data", "text/x-script; charset=utf-8")
	defer ts.Close()
	prevEnabled, prevURL, prevClient := spotahomeEnabled, spotahomeBaseURL, spotahomeClient
	spotahomeEnabled, spotahomeBaseURL, spotahomeClient = true, ts.URL, ts.Client()
	defer func() { spotahomeEnabled, spotahomeBaseURL, spotahomeClient = prevEnabled, prevURL, prevClient }()

	res, err := SearchSpotahome(context.Background(), "Lisbon, Portugal", HotelSearchOptions{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 48 {
		t.Fatalf("expected 48 results, got %d", len(res))
	}
}

func TestParseFlatioHTML_Fixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/flatio_lisbon.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	results, err := parseFlatioHTML(raw, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected listings, got 0")
	}
	for _, h := range results {
		if h.HotelID == "" || h.Price <= 0 || h.Currency != "EUR" {
			t.Fatalf("bad result: %+v", h)
		}
		if !strings.HasPrefix(h.BookingURL, "https://www.flatio.com/") {
			t.Errorf("booking url = %q", h.BookingURL)
		}
		if !strings.Contains(h.BookingURL, "-lisbon") {
			t.Errorf("Lisbon fixture contains an out-of-destination booking URL: %q", h.BookingURL)
		}
		if h.Name == "" {
			t.Errorf("missing name: %+v", h)
		}
	}
	// listing id 125151 / price 200 from the captured first JSON-LD item.
	var found bool
	for _, h := range results {
		if h.HotelID == "125151" && h.Price == 200 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected listing id=125151 price=200")
	}
}

func TestSearchFlatio_MockServer(t *testing.T) {
	ts := serveFixtureFile(t, "testdata/flatio_lisbon.html", "text/html; charset=utf-8")
	defer ts.Close()
	prevEnabled, prevURL, prevClient := flatioEnabled, flatioBaseURL, flatioClient
	flatioEnabled, flatioBaseURL, flatioClient = true, ts.URL, ts.Client()
	defer func() { flatioEnabled, flatioBaseURL, flatioClient = prevEnabled, prevURL, prevClient }()

	res, err := SearchFlatio(context.Background(), "Lisbon", HotelSearchOptions{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected results, got 0")
	}
}

func TestSearchFlatio_RejectsGenericFallbackRedirect(t *testing.T) {
	body, err := os.ReadFile("testdata/flatio_lisbon.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/s/Ischia_Italy":
			http.Redirect(w, r, "/s", http.StatusFound)
		case "/s":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer ts.Close()

	prevEnabled, prevURL, prevClient := flatioEnabled, flatioBaseURL, flatioClient
	flatioEnabled, flatioBaseURL, flatioClient = true, ts.URL, ts.Client()
	defer func() { flatioEnabled, flatioBaseURL, flatioClient = prevEnabled, prevURL, prevClient }()

	res, err := SearchFlatio(context.Background(), "Ischia Italy", HotelSearchOptions{})
	if err == nil {
		t.Fatalf("expected destination-scope error, got %d unrelated results", len(res))
	}
	if len(res) != 0 {
		t.Fatalf("destination-scope error returned %d results, want 0", len(res))
	}
}

func TestParseBluegroundList_Fixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/blueground_list_athens.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	props, err := parseBluegroundList(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(props) != 18 {
		t.Fatalf("expected 18 properties, got %d", len(props))
	}
	p := props[0]
	if p.Code == "" || p.Path == "" {
		t.Fatalf("missing code/path: %+v", p)
	}
	if !(p.Address.Lat > 37 && p.Address.Lat < 39) {
		t.Errorf("athens lat out of range: %f", p.Address.Lat)
	}
	h := bluegroundBaseResult(p)
	if h.Lat == 0 || h.Lon == 0 || h.PropertyType != "apartment" {
		t.Errorf("base result bad: %+v", h)
	}
	if !strings.HasPrefix(h.BookingURL, "https://www.theblueground.com/p/furnished-apartments/") {
		t.Errorf("booking url = %q", h.BookingURL)
	}
}

func TestParseBluegroundDetail_Fixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/blueground_detail_ath327.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	price, currency, minStay := parseBluegroundDetail(raw)
	if price != 1120 {
		t.Errorf("price = %f, want 1120", price)
	}
	if currency != "EUR" {
		t.Errorf("currency = %q, want EUR", currency)
	}
	if minStay != 2 {
		t.Errorf("minStayMonths = %d, want 2", minStay)
	}
}

// bluegroundRouteServer serves the list fixture for the list path and the detail
// fixture for /p/ paths, so the full two-hop flow can be exercised.
func TestSearchBlueground_MockServer(t *testing.T) {
	listBody, _ := os.ReadFile("testdata/blueground_list_athens.html")
	detailBody, _ := os.ReadFile("testdata/blueground_detail_ath327.html")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.HasPrefix(r.URL.Path, "/p/") {
			_, _ = w.Write(detailBody)
			return
		}
		_, _ = w.Write(listBody)
	}))
	defer ts.Close()
	prevEnabled, prevURL, prevClient := bluegroundEnabled, bluegroundBaseURL, bluegroundClient
	bluegroundEnabled, bluegroundBaseURL, bluegroundClient = true, ts.URL, ts.Client()
	defer func() { bluegroundEnabled, bluegroundBaseURL, bluegroundClient = prevEnabled, prevURL, prevClient }()

	res, err := SearchBlueground(context.Background(), "Athens, Greece", HotelSearchOptions{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// detail hop yields price 1120 for every property (same detail fixture),
	// capped at bluegroundDetailLimit.
	if len(res) == 0 {
		t.Fatalf("expected results, got 0")
	}
	if len(res) > bluegroundDetailLimit {
		t.Fatalf("results %d exceed detail limit %d", len(res), bluegroundDetailLimit)
	}
	for _, h := range res {
		if h.Price != 1120 || h.Currency != "EUR" {
			t.Errorf("bad price: %+v", h)
		}
		if !strings.Contains(h.Description, "2-month min") {
			t.Errorf("missing minStay in description: %q", h.Description)
		}
	}
}

func TestBluegroundSlug(t *testing.T) {
	cases := map[string]string{
		"Athens, Greece": "furnished-apartments-athens-gr",
		"Paris, France":  "furnished-apartments-paris-fr",
		"Athens":         "furnished-apartments-athens",
		"":               "",
	}
	for in, want := range cases {
		if got := bluegroundSlug(in); got != want {
			t.Errorf("bluegroundSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSpotahomeCitySlug(t *testing.T) {
	cases := map[string]string{
		"Lisbon, Portugal": "lisbon",
		"New York":         "new-york",
	}
	for in, want := range cases {
		if got := spotahomeCitySlug(in); got != want {
			t.Errorf("spotahomeCitySlug(%q) = %q, want %q", in, got, want)
		}
	}
}
