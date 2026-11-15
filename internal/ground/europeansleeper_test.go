package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// esCall is one request the provider sent upstream, captured before the
// redirect to the test server so the real host stays visible.
type esCall struct {
	Host, Method, Path string
	Body               map[string]any
}

type esRecorder struct {
	mu    sync.Mutex
	calls []esCall
	next  http.RoundTripper
}

func (r *esRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &body)
		req.Body = io.NopCloser(bytes.NewReader(raw))
	}
	r.mu.Lock()
	r.calls = append(r.calls, esCall{Host: req.URL.Host, Method: req.Method, Path: req.URL.Path, Body: body})
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

func (r *esRecorder) snapshot() []esCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]esCall(nil), r.calls...)
}

func esFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// esReply is a canned upstream answer: a status code and a body.
type esReply struct {
	status int
	body   []byte
}

// useEuropeanSleeperFake points the provider at a fake upstream that answers
// the search and availability endpoints with the given replies. The limiter
// is opened up so tests do not wait 6s per call.
func useEuropeanSleeperFake(t *testing.T, search, availability esReply) *esRecorder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply esReply
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/search/trains":
			reply = search
		case r.Method == http.MethodPost && r.URL.Path == "/api/search/availability":
			reply = availability
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("<html>404</html>"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = w.Write(reply.body)
	}))
	t.Cleanup(srv.Close)

	rec := &esRecorder{next: &redirectTransport{target: srv.URL}}
	origClient, origLimiter := europeanSleeperClient, europeanSleeperLimiter
	t.Cleanup(func() {
		europeanSleeperClient = origClient
		europeanSleeperLimiter = origLimiter
	})
	europeanSleeperLimiter = rate.NewLimiter(rate.Inf, 1)
	europeanSleeperClient = &http.Client{Transport: rec, Timeout: 5 * time.Second}
	return rec
}

const esAPIHost = "europeansleeperprod-api.azurewebsites.net"

func esOK(t *testing.T, fixture string) esReply {
	return esReply{status: http.StatusOK, body: esFixture(t, fixture)}
}

// Design: search POSTs /api/search/trains on the booking API with booking-system
// ids; price comes from /api/search/availability per kept trip; fares are per
// departure (provenance live); the booking link pre-fills the search.
func TestEuropeanSleeperSearch_AmsterdamPragueFixture(t *testing.T) {
	rec := useEuropeanSleeperFake(t,
		esOK(t, "europeansleeper_search_amsterdam_prague.json"),
		esOK(t, "europeansleeper_availability_amsterdam_prague.json"))

	routes, err := SearchEuropeanSleeper(context.Background(), "Amsterdam", "Prague", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1 (train 453 boarding 2026-11-20)", len(routes))
	}
	r := routes[0]
	if r.Provider != "european_sleeper" || r.Type != "train" {
		t.Errorf("provider/type = %q/%q, want european_sleeper/train", r.Provider, r.Type)
	}
	if r.Departure.Time != "2026-11-20T21:34:00" || r.Arrival.Time != "2026-11-21T10:54:00" {
		t.Errorf("times = %s -> %s, want 2026-11-20T21:34:00 -> 2026-11-21T10:54:00", r.Departure.Time, r.Arrival.Time)
	}
	if r.Duration != 800 {
		t.Errorf("Duration = %d, want 800", r.Duration)
	}
	if r.Price != 79.99 || r.Currency != "EUR" {
		t.Errorf("price = %v %s, want 79.99 EUR (cheapest bookable fare)", r.Price, r.Currency)
	}
	if r.PriceProvenance != models.PriceLive {
		t.Errorf("PriceProvenance = %q, want live", r.PriceProvenance)
	}
	if r.Departure.City != "Amsterdam" || r.Arrival.City != "Prague" {
		t.Errorf("cities = %q -> %q, want Amsterdam -> Prague", r.Departure.City, r.Arrival.City)
	}
	if r.Arrival.Station == "PRAHA HL. N." || r.Arrival.Station == "" {
		t.Errorf("Arrival.Station = %q, want the table name when there is no detour", r.Arrival.Station)
	}
	wantURL := "https://booking.europeansleeper.eu/en?departureStation=8400058&arrivalStation=5457076&departureDate=2026-11-20&passengerTypes=72&isSelected=true"
	if r.BookingURL != wantURL {
		t.Errorf("BookingURL = %q, want %q", r.BookingURL, wantURL)
	}

	calls := rec.snapshot()
	if len(calls) != 2 {
		t.Fatalf("upstream calls = %d, want 2 (search + one availability)", len(calls))
	}
	search, avail := calls[0], calls[1]
	if search.Host != esAPIHost || search.Method != http.MethodPost || search.Path != "/api/search/trains" {
		t.Errorf("search call = %s %s%s, want POST %s/api/search/trains", search.Method, search.Host, search.Path, esAPIHost)
	}
	for k, want := range map[string]any{
		"fromLocationId": "8400058", "toLocationId": "5457076",
		"departureDate": "2026-11-20T00:00:00.000Z", "bicycleCount": float64(0), "petsCount": float64(0),
	} {
		if got := search.Body[k]; got != want {
			t.Errorf("search body %s = %v, want %v", k, got, want)
		}
	}
	if pt, _ := search.Body["passengerTypes"].([]any); len(pt) != 1 || pt[0] != float64(72) {
		t.Errorf("search body passengerTypes = %v, want [72]", search.Body["passengerTypes"])
	}
	if avail.Host != esAPIHost || avail.Method != http.MethodPost || avail.Path != "/api/search/availability" {
		t.Errorf("availability call = %s %s%s, want POST %s/api/search/availability", avail.Method, avail.Host, avail.Path, esAPIHost)
	}
	for k, want := range map[string]any{
		"trainRouteId": "77ab1c5a-ea0b-4634-7cdd-08db0daabe3f", "trainNumber": "453", "travelDate": "2026-11-20",
		"fromLocationId": "8400058", "toLocationId": "5457076",
	} {
		if got := avail.Body[k]; got != want {
			t.Errorf("availability body %s = %v, want %v", k, got, want)
		}
	}
}

// Design: the answer covers the requested day -1..+3; only trips whose
// travelDate (boarding date) equals the requested date are kept.
func TestEuropeanSleeperSearch_KeepsOnlyRequestedBoardingDate(t *testing.T) {
	rec := useEuropeanSleeperFake(t,
		esOK(t, "europeansleeper_search_amsterdam_prague.json"),
		esOK(t, "europeansleeper_availability_amsterdam_prague.json"))

	routes, err := SearchEuropeanSleeper(context.Background(), "Amsterdam", "Prague", "2026-11-21", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("routes = %d, want 0 (no train boards on 2026-11-21)", len(routes))
	}
	if calls := rec.snapshot(); len(calls) != 1 || calls[0].Path != "/api/search/trains" {
		t.Errorf("upstream calls = %+v, want one search call and no availability call", calls)
	}
}

// Design: price in the requested currency when the fare carries it, else EUR.
func TestEuropeanSleeperSearch_RequestedCurrency(t *testing.T) {
	for _, tc := range []struct {
		currency, wantCurrency string
		wantPrice              float64
	}{
		{"CZK", "CZK", 2012},
		{"czk", "CZK", 2012},
		{"SEK", "EUR", 79.99},
		{"", "EUR", 79.99},
	} {
		t.Run(tc.currency, func(t *testing.T) {
			useEuropeanSleeperFake(t,
				esOK(t, "europeansleeper_search_amsterdam_prague.json"),
				esOK(t, "europeansleeper_availability_amsterdam_prague.json"))
			routes, err := SearchEuropeanSleeper(context.Background(), "Amsterdam", "Prague", "2026-11-20", tc.currency)
			if err != nil {
				t.Fatalf("SearchEuropeanSleeper: %v", err)
			}
			if len(routes) != 1 {
				t.Fatalf("routes = %d, want 1", len(routes))
			}
			if routes[0].Price != tc.wantPrice || routes[0].Currency != tc.wantCurrency {
				t.Errorf("price = %v %s, want %v %s", routes[0].Price, routes[0].Currency, tc.wantPrice, tc.wantCurrency)
			}
		})
	}
}

// Design: an availability failure for one trip degrades that trip to price 0,
// provenance unavailable; the search itself still succeeds. The fixture also
// carries null trips on the other two lines, which must be skipped.
func TestEuropeanSleeperSearch_AvailabilityFailureDegradesTrip(t *testing.T) {
	useEuropeanSleeperFake(t,
		esOK(t, "europeansleeper_search_brussels_milan.json"),
		esReply{status: http.StatusInternalServerError, body: []byte(`{"error":"boom"}`)})

	routes, err := SearchEuropeanSleeper(context.Background(), "Brussels", "Milan", "2026-11-19", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1 (train 401 boarding 2026-11-19)", len(routes))
	}
	r := routes[0]
	if r.Price != 0 || r.PriceProvenance != models.PriceUnavailable {
		t.Errorf("price = %v provenance %q, want 0 unavailable", r.Price, r.PriceProvenance)
	}
	if r.Departure.Time != "2026-11-19T19:06:00" || r.Arrival.Time != "2026-11-20T11:40:00" {
		t.Errorf("times = %s -> %s, want 2026-11-19T19:06:00 -> 2026-11-20T11:40:00", r.Departure.Time, r.Arrival.Time)
	}
}

// Design (lead, 2026-11-15): a detoured trip names its real station only in
// the availability answer; the route shows that name (Berlin-Gesundbrunnen)
// and keeps the table name for the undetoured end.
func TestEuropeanSleeperSearch_DetourStationFromAvailability(t *testing.T) {
	useEuropeanSleeperFake(t,
		esOK(t, "europeansleeper_search_amsterdam_berlin.json"),
		esOK(t, "europeansleeper_availability_amsterdam_berlin.json"))

	routes, err := SearchEuropeanSleeper(context.Background(), "Amsterdam", "Berlin", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	r := routes[0]
	if r.Arrival.Station != "Berlin-Gesundbrunnen" {
		t.Errorf("Arrival.Station = %q, want Berlin-Gesundbrunnen (detour)", r.Arrival.Station)
	}
	if r.Departure.Station == "Amsterdam CS" || r.Departure.Station == "" {
		t.Errorf("Departure.Station = %q, want the table name (no detour at Amsterdam)", r.Departure.Station)
	}
	if r.Price != 59.99 {
		t.Errorf("Price = %v, want 59.99", r.Price)
	}
}

// Design: the availability call waits behind the shared limiter like the
// search call. With one token and a short deadline the second call cannot
// run, so the trip degrades instead of bypassing the limiter.
func TestEuropeanSleeperSearch_AvailabilityWaitsOnLimiter(t *testing.T) {
	rec := useEuropeanSleeperFake(t,
		esOK(t, "europeansleeper_search_amsterdam_prague.json"),
		esOK(t, "europeansleeper_availability_amsterdam_prague.json"))
	europeanSleeperLimiter = rate.NewLimiter(rate.Every(time.Hour), 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	routes, err := SearchEuropeanSleeper(ctx, "Amsterdam", "Prague", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchEuropeanSleeper: %v", err)
	}
	if calls := rec.snapshot(); len(calls) != 1 {
		t.Errorf("upstream calls = %d, want 1 (availability must wait on the limiter)", len(calls))
	}
	if len(routes) != 1 || routes[0].PriceProvenance != models.PriceUnavailable {
		t.Errorf("routes = %+v, want one route with provenance unavailable", routes)
	}
}

// Design: 429/5xx wrap ErrRateLimited (nightly maintenance 23:00-03:30);
// other non-2xx and undecodable bodies wrap ErrParseFailed.
func TestEuropeanSleeperSearch_ErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply esReply
		want  error
	}{
		{"429", esReply{http.StatusTooManyRequests, []byte(`{}`)}, models.ErrRateLimited},
		{"503 maintenance", esReply{http.StatusServiceUnavailable, []byte(`<html>maintenance</html>`)}, models.ErrRateLimited},
		{"404 html", esReply{http.StatusNotFound, []byte(`<html>not found</html>`)}, models.ErrParseFailed},
		{"400 domestic", esReply{http.StatusBadRequest, esFixture(t, "europeansleeper_search_domestic_400.json")}, models.ErrParseFailed},
		{"200 not json", esReply{http.StatusOK, []byte(`<html>ok</html>`)}, models.ErrParseFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useEuropeanSleeperFake(t, tc.reply, esReply{http.StatusOK, []byte(`{}`)})
			routes, err := SearchEuropeanSleeper(context.Background(), "Amsterdam", "Prague", "2026-11-20", "EUR")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want wrapping %v", err, tc.want)
			}
			if len(routes) != 0 {
				t.Errorf("routes = %d, want 0 on error", len(routes))
			}
		})
	}
}

// esConstants is the trimmed booking-site constants block (__NEXT_DATA__
// props.pageProps.constants) captured 2026-11-15.
type esConstants struct {
	DomesticDisabled []string `json:"domesticJourneyDisabledCountries"`
	Routes           []struct {
		ID       string `json:"id"`
		Stations []struct {
			ID     string  `json:"id"`
			Mapped *string `json:"mappedStationId"`
		} `json:"stations"`
	} `json:"routes"`
	Stations []struct {
		ID        string `json:"id"`
		CountryID string `json:"countryId"`
	} `json:"stations"`
}

// esConfirmed lists the cities whose booking-system id a live search
// confirmed as a stop on 2026-11-15.
var esConfirmed = map[string]string{
	"Brussels": "8800104", "Antwerp": "8800210", "Rotterdam": "8400530",
	"Amsterdam": "8400058", "Berlin": "8010100", "Dresden": "8001305",
	"Prague": "5457076", "Paris": "8700015", "Hamburg": "8020401",
	"Köln": "8050500", "Milan": "8300112", "Breda": "8400131", "Eindhoven": "8400206",
}

// Design: the gate serves a pair only when both stations stop on one line
// (mapped stops excluded) and the site does not refuse it as a domestic
// journey (domesticJourneyDisabledCountries). Expectations come from the
// captured constants, so a site change shows up as a failing pair.
func TestHasEuropeanSleeperRoute_MatchesSiteConstants(t *testing.T) {
	var c esConstants
	if err := json.Unmarshal(esFixture(t, "europeansleeper_constants.json"), &c); err != nil {
		t.Fatalf("decode constants: %v", err)
	}
	country := map[string]string{}
	for _, s := range c.Stations {
		country[s.ID] = s.CountryID
	}
	disabled := map[string]bool{}
	for _, cc := range c.DomesticDisabled {
		disabled[cc] = true
	}
	sameLine := func(a, b string) bool {
		for _, r := range c.Routes {
			stops := map[string]bool{}
			for _, s := range r.Stations {
				if s.Mapped == nil {
					stops[s.ID] = true
				}
			}
			if stops[a] && stops[b] {
				return true
			}
		}
		return false
	}

	served := 0
	for fromCity, fromID := range esConfirmed {
		for toCity, toID := range esConfirmed {
			domestic := country[fromID] == country[toID] && disabled[country[fromID]]
			want := fromID != toID && sameLine(fromID, toID) && !domestic
			if want {
				served++
			}
			if got := HasEuropeanSleeperRoute(fromCity, toCity); got != want {
				t.Errorf("HasEuropeanSleeperRoute(%q, %q) = %v, want %v", fromCity, toCity, got, want)
			}
		}
	}

	// Anchors so the derived table cannot pass vacuously.
	for _, a := range []struct {
		from, to string
		want     bool
	}{
		{"Berlin", "Dresden", true}, {"Amsterdam", "Prague", true}, {"Paris", "Hamburg", true},
		{"Brussels", "Antwerp", false}, {"Amsterdam", "Milan", false}, {"Paris", "Amsterdam", false},
	} {
		fromID, toID := esConfirmed[a.from], esConfirmed[a.to]
		domestic := country[fromID] == country[toID] && disabled[country[fromID]]
		if got := sameLine(fromID, toID) && !domestic; got != a.want {
			t.Errorf("constants fixture: %s-%s served = %v, want %v", a.from, a.to, got, a.want)
		}
	}
	if served == 0 {
		t.Fatal("constants fixture produced no served pair")
	}
}

type esNoNetwork struct{ t *testing.T }

func (n esNoNetwork) RoundTrip(req *http.Request) (*http.Response, error) {
	n.t.Errorf("unexpected upstream call to %s", req.URL)
	return nil, errors.New("network disabled in test")
}

// Design: unserved pairs wrap ErrNotApplicable before any network call; a
// single matching endpoint no longer launches the provider.
func TestSearchEuropeanSleeper_UnservedPairsSkipNetwork(t *testing.T) {
	origClient := europeanSleeperClient
	t.Cleanup(func() { europeanSleeperClient = origClient })
	europeanSleeperClient = &http.Client{Transport: esNoNetwork{t}}

	for _, p := range [][2]string{
		{"Brussels", "Atlantis"}, {"Atlantis", "Prague"}, {"Atlantis", "Mordor"},
		{"Paris", "Amsterdam"},                              // different lines
		{"Brussels", "Antwerp"}, {"Rotterdam", "Amsterdam"}, // domestic, refused by the site
		{"Zürich", "Köln"}, {"Amsterdam", "Milan"}, // mapped stops: the train stops at Aarau / Eindhoven
		{"Prague", "Prague"},
	} {
		if HasEuropeanSleeperRoute(p[0], p[1]) {
			t.Errorf("HasEuropeanSleeperRoute(%q, %q) = true, want false", p[0], p[1])
		}
		_, err := SearchEuropeanSleeper(context.Background(), p[0], p[1], "2026-11-20", "EUR")
		if !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("SearchEuropeanSleeper(%q, %q) err = %v, want ErrNotApplicable", p[0], p[1], err)
		}
	}
}

func TestEuropeanSleeperStationLookup(t *testing.T) {
	for _, tc := range []struct{ in, city, country string }{
		{"Prague", "Prague", "CZ"}, {"  praha ", "Prague", "CZ"}, {"BRUSSELS", "Brussels", "BE"},
		{"Köln", "Köln", "DE"}, {"Cologne", "Köln", "DE"}, {"Milano", "Milan", "IT"},
		{"Hamburg", "Hamburg", "DE"}, {"Eindhoven", "Eindhoven", "NL"},
	} {
		s, ok := LookupEuropeanSleeperStation(tc.in)
		if !ok || s.City != tc.city || s.Country != tc.country || s.Name == "" {
			t.Errorf("Lookup(%q) = %+v, %v; want city %s, country %s, a station name", tc.in, s, ok, tc.city, tc.country)
		}
	}
	for _, in := range []string{"", "London", "Zürich", "Atlantis"} {
		if HasEuropeanSleeperStation(in) {
			t.Errorf("HasEuropeanSleeperStation(%q) = true, want false", in)
		}
	}
}

func TestEuropeanSleeperRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, europeanSleeperLimiter, 6*time.Second, 1)
}

func TestNormaliseTimeString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-07-15T19:22:00+02:00", "2026-07-15T19:22:00"},
		{"2026-07-15T08:04:00Z", "2026-07-15T08:04:00"},
		{"2026-07-15T08:04:00", "2026-07-15T08:04:00"},
		{"2026-07-15T08:04:00.000", "2026-07-15T08:04:00"},
		{"", ""},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		got := normaliseTimeString(tt.input)
		if got != tt.want {
			t.Errorf("normaliseTimeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
