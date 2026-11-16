package ground

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// TestSnalltagetStations pins the apiv2 stop codes (a UIC number or a
// literal stop name, as /navigation/stops returns them).
func TestSnalltagetStations(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantOK   bool
	}{
		{"Stockholm", "740000001", true},
		{"stockholm", "740000001", true},
		{"STOCKHOLM", "740000001", true},
		{"  Stockholm  ", "740000001", true},
		{"Malmö", "Malmö C", true},
		{"malmo", "Malmö C", true},
		{"malmö c", "Malmö C", true},
		{"Norrköping", "740000007", true},
		{"norrkoping", "740000007", true},
		{"Linköping", "740000009", true},
		{"linkoping", "740000009", true},
		{"Alvesta", "740000004", true},
		{"Hässleholm", "740000006", true},
		{"hassleholm", "740000006", true},
		{"Lund", "740000120", true},
		{"Åre", "740000115", true},
		{"are", "740000115", true},
		{"Duved", "740000308", true},
		{"Berlin", "Berlin", true},
		{"Hamburg", "Hamburg", true},
		{"Copenhagen", "Köpenhamn", true},
		{"København", "Köpenhamn", true},
		{"Köpenhamn", "Köpenhamn", true},
		{"", "", false},
		{"Nonexistent", "", false},
		{"London", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupSnalltagetStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupSnalltagetStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.UIC != tt.wantCode {
				t.Errorf("code = %q, want %q", station.UIC, tt.wantCode)
			}
		})
	}
}

func TestSnalltagetStations_Metadata(t *testing.T) {
	station, ok := LookupSnalltagetStation("Stockholm")
	if !ok {
		t.Fatal("expected Stockholm to be found")
	}
	if station.Name != "Stockholm C" {
		t.Errorf("Name = %q, want %q", station.Name, "Stockholm C")
	}
	if station.City != "Stockholm" {
		t.Errorf("City = %q, want %q", station.City, "Stockholm")
	}
	if station.Country != "SE" {
		t.Errorf("Country = %q, want %q", station.Country, "SE")
	}
}

func TestHasSnalltagetStation(t *testing.T) {
	if !HasSnalltagetStation("Stockholm") {
		t.Error("Stockholm should have a Snälltåget station")
	}
	if HasSnalltagetStation("Atlantis") {
		t.Error("Atlantis should not have a Snälltåget station")
	}
}

func TestHasSnalltagetRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Stockholm", "Malmö", true},
		{"Stockholm", "Åre", true},
		{"Stockholm", "Berlin", true},
		{"Stockholm", "SomeCity", true}, // one end matches
		{"SomeCity", "Malmö", true},     // one end matches
		{"Atlantis", "Mordor", false},   // neither matches
		{"", "Stockholm", true},
		{"Stockholm", "", true},
		{"", "", false},
	}

	for _, tt := range tests {
		name := tt.from + "->" + tt.to
		t.Run(name, func(t *testing.T) {
			got := HasSnalltagetRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasSnalltagetRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestAllSnalltagetStationsHaveRequiredFields(t *testing.T) {
	for city, station := range snalltagetStations {
		if station.UIC == "" {
			t.Errorf("station %q has empty UIC", city)
		}
		if station.Name == "" {
			t.Errorf("station %q has empty Name", city)
		}
		if station.City == "" {
			t.Errorf("station %q has empty City", city)
		}
		if station.Country == "" {
			t.Errorf("station %q has empty Country", city)
		}
		if len(station.Country) != 2 {
			t.Errorf("station %q Country %q should be 2 letters", city, station.Country)
		}
	}
}

func TestSnalltagetRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, snalltagetLimiter, 6*time.Second, 1)
}

// TestBuildSnalltagetBookingURL pins the link the booking app reads: any page
// holding #booking-app takes from, to and date, matches from and to by exact
// stop code, and treats single as one-way. The old /en/booking path is a 404.
func TestBuildSnalltagetBookingURL(t *testing.T) {
	from, _ := LookupSnalltagetStation("Stockholm")
	to, _ := LookupSnalltagetStation("Malmö")
	raw := buildSnalltagetBookingURL(from, to, "2026-11-20")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if u.Scheme != "https" || u.Host != "www.snalltaget.se" || u.Path != "/en" {
		t.Errorf("link = %s, want https://www.snalltaget.se/en?...", raw)
	}
	q := u.Query()
	if q.Get("from") != "740000001" || q.Get("to") != "Malmö C" || q.Get("date") != "2026-11-20" || !q.Has("single") {
		t.Errorf("query = %v, want from=740000001 to=Malmö C date=2026-11-20 single", q)
	}
}

func TestSnalltagetSwedishAliases(t *testing.T) {
	pairs := [][2]string{
		{"malmö", "malmo"},
		{"norrköping", "norrkoping"},
		{"linköping", "linkoping"},
		{"hässleholm", "hassleholm"},
		{"åre", "are"},
	}

	for _, p := range pairs {
		s1, ok1 := LookupSnalltagetStation(p[0])
		s2, ok2 := LookupSnalltagetStation(p[1])
		if !ok1 || !ok2 {
			t.Errorf("both %q and %q should be found", p[0], p[1])
			continue
		}
		if s1.UIC != s2.UIC {
			t.Errorf("%q UIC=%q != %q UIC=%q", p[0], s1.UIC, p[1], s2.UIC)
		}
	}
}

// TestSnalltagetStationCodesMatchLiveStops checks every station code against
// the apiv2 /navigation/stops list captured 2026-11-15, so a code the search
// endpoint would reject cannot ship.
func TestSnalltagetStationCodesMatchLiveStops(t *testing.T) {
	var stops struct {
		Items []struct {
			Code string `json:"code"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(loadGroundFixture(t, "snalltaget_stops.json")), &stops); err != nil {
		t.Fatalf("decode stops fixture: %v", err)
	}
	known := map[string]bool{}
	for _, s := range stops.Items {
		known[s.Code] = true
	}
	aliases := make([]string, 0, len(snalltagetStations))
	for alias := range snalltagetStations {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if code := snalltagetStations[alias].UIC; !known[code] {
			t.Errorf("station %q code %q is not in the live stops list", alias, code)
		}
	}
}

func TestSearchSnalltaget_NotApplicableWrapsSentinel(t *testing.T) {
	for _, pair := range [][2]string{{"Narnia", "Malmö"}, {"Stockholm", "Narnia"}} {
		_, err := SearchSnalltaget(context.Background(), pair[0], pair[1], "2026-11-20", "SEK")
		if !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("%s -> %s: error %v does not wrap models.ErrNotApplicable", pair[0], pair[1], err)
		}
	}
}

// snalltagetHostTransport sends every request to the test server with its
// path intact and passes the host the client dialled in X-Test-Host, so the
// handlers can check which real host each call was meant for.
type snalltagetHostTransport struct{ target *url.URL }

func (tr snalltagetHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host, out.Host = tr.target.Scheme, tr.target.Host, ""
	out.Header.Set("X-Test-Host", req.URL.Host)
	return http.DefaultTransport.RoundTrip(out)
}

const snalltagetTestToken = "tok-123"

func snalltagetTokenOK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"` + snalltagetTestToken + `","expires_in":900}`))
}

// useSnalltagetServer serves token and search from one test server, records
// each call as "METHOD host/path", and fails the test on a missing or Go
// default User-Agent (the token endpoint answers 403 to Go-http-client). A nil
// search handler means the search must never be reached.
func useSnalltagetServer(t *testing.T, token, search http.HandlerFunc) func() []string {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	record := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls = append(calls, r.Method+" "+r.Header.Get("X-Test-Host")+r.URL.Path)
			mu.Unlock()
			if ua := r.UserAgent(); ua == "" || strings.HasPrefix(ua, "Go-http-client") {
				t.Errorf("%s: User-Agent = %q, want trvl's own", r.URL.Path, ua)
			}
			h(w, r)
		}
	}
	if search == nil {
		search = func(w http.ResponseWriter, _ *http.Request) {
			t.Error("journey search reached after a token failure")
			w.WriteHeader(http.StatusTeapot)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token/v2", record(token))
	mux.HandleFunc("/orientation/searchjourney", record(search))
	srv := httptest.NewServer(mux)
	target, _ := url.Parse(srv.URL)
	origClient, origLimiter := snalltagetClient, snalltagetLimiter
	forgetCachedToken := func() {
		snalltagetTokenCache.Lock()
		snalltagetTokenCache.token = ""
		snalltagetTokenCache.Unlock()
	}
	forgetCachedToken() // each test starts from the token endpoint
	t.Cleanup(func() {
		srv.Close()
		snalltagetClient, snalltagetLimiter = origClient, origLimiter
		forgetCachedToken()
	})
	snalltagetLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	snalltagetClient = &http.Client{Transport: snalltagetHostTransport{target: target}, Timeout: 5 * time.Second}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

// snalltagetServe answers the journey search with body after checking that
// it carries the guest token and the expected stop codes.
func snalltagetServe(t *testing.T, body, origin, destination, date string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+snalltagetTestToken {
			t.Errorf("Authorization = %q, want the token from /token/v2", got)
		}
		var req struct {
			Origin      string `json:"origin"`
			Destination string `json:"destination"`
			Departure   string `json:"departure"`
			Passengers  []struct {
				Type string `json:"type"`
			} `json:"passengers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode search body: %v", err)
		}
		if req.Origin != origin || req.Destination != destination || req.Departure != date {
			t.Errorf("search = %s -> %s on %s, want %s -> %s on %s", req.Origin, req.Destination, req.Departure, origin, destination, date)
		}
		if len(req.Passengers) != 1 || req.Passengers[0].Type != "AD" {
			t.Errorf("passengers = %+v, want one AD (adult)", req.Passengers)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// TestSearchSnalltaget_LiveSearchJourneyFixture replays the apiv2 journey
// search captured 2026-11-15 (Stockholm C -> Malmö C, one adult). The booking
// app at www.snalltaget.se fetches a guest token from /token/v2 and POSTs the
// search to apiv2.snalltaget.se; each route carries its own bundle fares.
func TestSearchSnalltaget_LiveSearchJourneyFixture(t *testing.T) {
	fixture := loadGroundFixture(t, "snalltaget_stockholm_malmo_searchjourney.json")
	calls := useSnalltagetServer(t, snalltagetTokenOK, snalltagetServe(t, fixture, "740000001", "Malmö C", "2026-11-20"))

	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchSnalltaget: %v", err)
	}
	wantCalls := []string{"GET www.snalltaget.se/token/v2", "POST apiv2.snalltaget.se/orientation/searchjourney"}
	if got := calls(); strings.Join(got, ",") != strings.Join(wantCalls, ",") {
		t.Errorf("calls = %q, want %q", got, wantCalls)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2 departures", len(routes))
	}
	want := []struct {
		dep, arr string
		dur      int
	}{
		{"2026-11-20T10:43:00", "2026-11-20T15:41:00", 298},
		{"2026-11-20T16:25:00", "2026-11-20T21:30:00", 305},
	}
	for i, w := range want {
		r := routes[i]
		if r.Departure.Time != w.dep || r.Arrival.Time != w.arr {
			t.Errorf("route %d = %s -> %s, want %s -> %s", i, r.Departure.Time, r.Arrival.Time, w.dep, w.arr)
		}
		if r.Departure.Station != "Stockholm C" || r.Arrival.Station != "Malmö C" {
			t.Errorf("route %d stations = %q -> %q, want the response's Stockholm C -> Malmö C", i, r.Departure.Station, r.Arrival.Station)
		}
		if r.Duration != w.dur {
			t.Errorf("route %d duration = %d, want %d", i, r.Duration, w.dur)
		}
		// The cheapest bundle; the booking app charges SEK whatever was asked.
		if r.Price != 849 || r.Currency != "SEK" {
			t.Errorf("route %d fare = %v %s, want 849 SEK", i, r.Price, r.Currency)
		}
		if r.PriceProvenance != models.PriceLive {
			t.Errorf("route %d provenance = %q, want %q", i, r.PriceProvenance, models.PriceLive)
		}
	}
}

// TestSearchSnalltaget_CopenhagenFixture replays Stockholm C -> Köpenhamn
// captured 2026-11-15. The train ends at København Syd, so station names come
// from the answer's legs, not the static table.
func TestSearchSnalltaget_CopenhagenFixture(t *testing.T) {
	fixture := loadGroundFixture(t, "snalltaget_stockholm_copenhagen_searchjourney.json")
	useSnalltagetServer(t, snalltagetTokenOK, snalltagetServe(t, fixture, "740000001", "Köpenhamn", "2026-12-01"))

	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Copenhagen", "2026-12-01", "SEK")
	if err != nil {
		t.Fatalf("SearchSnalltaget: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	r := routes[0]
	if r.Arrival.City != "Copenhagen" || r.Arrival.Station != "København Syd" {
		t.Errorf("arrival = %q / %q, want Copenhagen / København Syd", r.Arrival.City, r.Arrival.Station)
	}
	if r.Departure.Time != "2026-12-01T10:43:00" || r.Arrival.Time != "2026-12-01T16:44:00" || r.Duration != 361 {
		t.Errorf("times = %s -> %s (%d min), want 10:43 -> 16:44 (361)", r.Departure.Time, r.Arrival.Time, r.Duration)
	}
	// Bundles are 598, 499, 2495, 3245: neither the first nor the last.
	if r.Price != 499 {
		t.Errorf("price = %v, want the cheapest bundle 499", r.Price)
	}
}

// TestSearchSnalltaget_EmptyDay replays a live answer with an offer but no
// routes (Stockholm -> Berlin outside the summer season): an honest empty
// day, not a parse failure.
func TestSearchSnalltaget_EmptyDay(t *testing.T) {
	fixture := loadGroundFixture(t, "snalltaget_stockholm_berlin_noroutes.json")
	useSnalltagetServer(t, snalltagetTokenOK, snalltagetServe(t, fixture, "740000001", "Berlin", "2027-07-01"))

	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Berlin", "2027-07-01", "SEK")
	if err != nil {
		t.Errorf("err = %v, want nil for a day without departures", err)
	}
	if len(routes) != 0 {
		t.Errorf("routes = %d, want 0", len(routes))
	}
}

// TestSearchSnalltaget_PriceIsCheapestBookableBundle pins the price rule on a
// route where the first, cheapest and last bundle and lowestPrice all differ.
func TestSearchSnalltaget_PriceIsCheapestBookableBundle(t *testing.T) {
	const body = `{"offer":{"travels":[{"routes":[{"lowestPrice":250.0,"travelDuration":"PT5H",
		"legs":[{"departureStation":{"name":"Stockholm C","departureTimestamp":"2026-11-20T10:00:00"},
		"arrivalStation":{"name":"Malmö C","arrivalTimestamp":"2026-11-20T15:00:00"},
		"serviceType":{"modality":"LONG_DISTANCE_TRAINS","name":"Snälltåget"}}],
		"bundleAvailability":{"logicalAvailability":true,"availability":true},
		"bundles":[{"price":600.0},{"price":0},{"price":300.0},{"price":450.0}]}]}]},"messages":[]}`
	useSnalltagetServer(t, snalltagetTokenOK, snalltagetServe(t, body, "740000001", "Malmö C", "2026-11-20"))

	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK")
	if err != nil {
		t.Fatalf("SearchSnalltaget: %v", err)
	}
	if len(routes) != 1 || routes[0].Price != 300 {
		t.Fatalf("routes = %+v, want one route priced 300 (cheapest priced bundle)", routes)
	}
}

// TestSearchSnalltaget_TypedErrors pins that a refusal or a changed answer is
// an error the status layer can classify, never an empty success, and that a
// token failure stops before the search.
func TestSearchSnalltaget_TypedErrors(t *testing.T) {
	status := func(code int) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
	}
	body := func(s string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(s)) }
	}
	cases := []struct {
		name          string
		token, search http.HandlerFunc // nil search: must not be reached
		want          error
	}{
		{"token refused", status(http.StatusServiceUnavailable), nil, models.ErrRateLimited},
		{"token rate limited", status(http.StatusTooManyRequests), nil, models.ErrRateLimited},
		{"bot wall on token", status(http.StatusForbidden), nil, models.ErrRateLimited},
		{"token without access_token", body(`{}`), nil, models.ErrParseFailed},
		{"token malformed JSON", body(`{"access_token":`), nil, models.ErrParseFailed},
		{"search refused", snalltagetTokenOK, status(http.StatusServiceUnavailable), models.ErrRateLimited},
		{"search rate limited", snalltagetTokenOK, status(http.StatusTooManyRequests), models.ErrRateLimited},
		{"search moved", snalltagetTokenOK, status(http.StatusNotFound), models.ErrParseFailed},
		{"search not JSON", snalltagetTokenOK, body(`<html>maintenance</html>`), models.ErrParseFailed},
		{"retired Sqills offers document", snalltagetTokenOK,
			body(`{"trips":[{"departureTime":"2026-11-20T18:00:00","arrivalTime":"2026-11-21T06:30:00","prices":[{"amount":299,"currency":"SEK"}]}]}`),
			models.ErrParseFailed},
		{"retired Sqills data document", snalltagetTokenOK,
			body(`{"data":[{"departureTime":"2026-11-20T18:00:00","fares":[{"amount":199,"currency":"SEK"}]}]}`),
			models.ErrParseFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useSnalltagetServer(t, c.token, c.search)
			routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK")
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want it to wrap %v", err, c.want)
			}
			if len(routes) != 0 {
				t.Errorf("routes = %d, want none alongside an error", len(routes))
			}
		})
	}
}

// TestSearchSnalltaget_UnavailableRouteHasNoFare mirrors the booking app,
// which disables a route whose bundleAvailability.availability is false: its
// bundles are not bookable, so the route carries no fare and is never live.
func TestSearchSnalltaget_UnavailableRouteHasNoFare(t *testing.T) {
	const body = `{"offer":{"travels":[{"routes":[{"lowestPrice":849.0,
		"legs":[{"departureStation":{"name":"Stockholm C","departureTimestamp":"2026-11-20T10:43:00"},
		"arrivalStation":{"name":"Malmö C","arrivalTimestamp":"2026-11-20T15:41:00"},
		"serviceType":{"modality":"LONG_DISTANCE_TRAINS","name":"Snälltåget"}}],
		"bundleAvailability":{"logicalAvailability":true,"availability":false},
		"bundles":[{"price":948.0},{"price":849.0}]}]}]},"messages":[]}`
	useSnalltagetServer(t, snalltagetTokenOK, snalltagetServe(t, body, "740000001", "Malmö C", "2026-11-20"))

	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK")
	if err != nil {
		t.Fatalf("SearchSnalltaget: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want the departure kept and shown unavailable", len(routes))
	}
	if r := routes[0]; r.Price != 0 || r.PriceProvenance == models.PriceLive {
		t.Errorf("fare = %v (%q), want 0 and not live for an unavailable route", r.Price, r.PriceProvenance)
	}
}

const (
	snalltagetTokenCall  = "GET www.snalltaget.se/token/v2"
	snalltagetSearchCall = "POST apiv2.snalltaget.se/orientation/searchjourney"
)

// snalltagetSeqTokens hands out tok-1, tok-2, ... valid for expiresIn seconds.
func snalltagetSeqTokens(expiresIn int) http.HandlerFunc {
	var mu sync.Mutex
	n := 0
	return func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		tok := fmt.Sprintf("tok-%d", n)
		mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d}`, tok, expiresIn)
	}
}

// snalltagetAnswerFor serves the Malmö fixture to the given bearer token and
// a 401 to any other.
func snalltagetAnswerFor(t *testing.T, token string) http.HandlerFunc {
	fixture := loadGroundFixture(t, "snalltaget_stockholm_malmo_searchjourney.json")
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(fixture))
	}
}

func snalltagetSearchTwice(t *testing.T) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if _, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK"); err != nil {
			t.Fatalf("search %d: %v", i+1, err)
		}
	}
}

func TestSearchSnalltaget_ReusesGuestToken(t *testing.T) {
	calls := useSnalltagetServer(t, snalltagetSeqTokens(900), snalltagetAnswerFor(t, "tok-1"))
	snalltagetSearchTwice(t)
	want := []string{snalltagetTokenCall, snalltagetSearchCall, snalltagetSearchCall}
	if got := calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %q, want one token fetch reused by both searches %q", got, want)
	}
}

// TestSearchSnalltaget_RefetchesTokenNearExpiry: a token about to expire
// (30 s left, inside the safety margin) is not reused.
func TestSearchSnalltaget_RefetchesTokenNearExpiry(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	tokens := snalltagetSeqTokens(30)
	fixture := loadGroundFixture(t, "snalltaget_stockholm_malmo_searchjourney.json")
	calls := useSnalltagetServer(t, tokens, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get("Authorization")] = true
		mu.Unlock()
		_, _ = w.Write([]byte(fixture))
	})
	snalltagetSearchTwice(t)
	want := []string{snalltagetTokenCall, snalltagetSearchCall, snalltagetTokenCall, snalltagetSearchCall}
	if got := calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %q, want a fresh token per search %q", got, want)
	}
	if !seen["Bearer tok-1"] || !seen["Bearer tok-2"] {
		t.Errorf("bearers seen = %v, want tok-1 then tok-2", seen)
	}
}

func TestSearchSnalltaget_RefreshesTokenOnceOn401(t *testing.T) {
	calls := useSnalltagetServer(t, snalltagetSeqTokens(900), snalltagetAnswerFor(t, "tok-2"))
	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK")
	if err != nil {
		t.Fatalf("SearchSnalltaget: %v", err)
	}
	if len(routes) != 2 {
		t.Errorf("routes = %d, want 2 after the refreshed token", len(routes))
	}
	want := []string{snalltagetTokenCall, snalltagetSearchCall, snalltagetTokenCall, snalltagetSearchCall}
	if got := calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestSearchSnalltaget_Second401IsAnError(t *testing.T) {
	calls := useSnalltagetServer(t, snalltagetSeqTokens(900), snalltagetAnswerFor(t, "never-issued"))
	routes, err := SearchSnalltaget(context.Background(), "Stockholm", "Malmö", "2026-11-20", "SEK")
	if !errors.Is(err, models.ErrParseFailed) || len(routes) != 0 {
		t.Errorf("routes = %d, err = %v; want none and an error wrapping ErrParseFailed", len(routes), err)
	}
	want := []string{snalltagetTokenCall, snalltagetSearchCall, snalltagetTokenCall, snalltagetSearchCall}
	if got := calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %q, want exactly one refresh %q", got, want)
	}
}
