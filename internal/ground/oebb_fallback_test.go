package ground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

const oebbTripFixture = "oebb_hafas_tripsearch_praha_wien_2026-11-17.json"

// assertOebbBlockedErr checks the shop refusal is typed (retryable, reason
// blocked) and its text keeps the page title but none of the HTML.
func assertOebbBlockedErr(t *testing.T, err error) *models.ProviderNotice {
	t.Helper()
	if !errors.Is(err, models.ErrRateLimited) {
		t.Errorf("err = %v, want it to wrap ErrRateLimited", err)
	}
	var notice *models.ProviderNotice
	if !errors.As(err, &notice) {
		t.Fatalf("err = %v (%T), want a *models.ProviderNotice", err, err)
	}
	if notice.Reason != models.ReasonBlocked {
		t.Errorf("notice reason = %q, want %q", notice.Reason, models.ReasonBlocked)
	}
	msg := err.Error()
	if strings.Contains(msg, "<") || strings.Contains(msg, "--brand") {
		t.Errorf("error text carries HTML: %q", msg)
	}
	if !strings.Contains(msg, "Access temporarily restricted") {
		t.Errorf("error text lost the page title: %q", msg)
	}
	return notice
}

// When the shop refuses this client, SearchOebb still answers from the HAFAS
// timetable: departures without fares, booked through the shop clickout.
func TestSearchOebb_ShopBlockedFallsBackToTimetable(t *testing.T) {
	mock := &oebbMock{shopToken: oebbShopRefuses, mgateFile: oebbTripFixture}
	mock.install(t)

	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	notice := assertOebbBlockedErr(t, err)
	if notice.FixHint != "shop refused: prices unavailable" {
		t.Errorf("FixHint = %q", notice.FixHint)
	}

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("HAFAS calls = %d, want 1", len(calls))
	}
	raw, _ := json.Marshal(calls[0])
	// 8103000 is Wien Hbf itself; 1190100 is all of Vienna and lets HAFAS end
	// a journey at Franz-Josefs-Bahnhof (live, 2026-11-15).
	if got := string(raw); !strings.Contains(got, `"extId":"5400014"`) ||
		!strings.Contains(got, `"extId":"8103000"`) || !strings.Contains(got, `"outDate":"20261117"`) {
		t.Errorf("HAFAS request does not ask Prague -> Wien Hbf on 2026-11-17: %s", got)
	}
	if strings.Contains(string(raw), "trfReq") {
		t.Errorf("HAFAS request asks for fares, which it never returns: %s", raw)
	}
	if !strings.Contains(string(raw), `"ver":"1.45"`) {
		t.Errorf("HAFAS envelope is not mgate version 1.45: %s", raw)
	}

	// Two connections leave at 06:01, so departure and arrival key a route.
	byTimes := map[string]models.GroundRoute{}
	for _, r := range routes {
		// Unpriced: no fare, and never labelled live or estimated (the merge
		// pass labels an unlabelled 0 unavailable).
		if r.Price != 0 || (r.PriceProvenance != "" && r.PriceProvenance != models.PriceUnavailable) {
			t.Errorf("%s: price %v provenance %q, want 0 and unavailable", r.Departure.Time, r.Price, r.PriceProvenance)
		}
		byTimes[r.Departure.Time+" "+r.Arrival.Time] = r
	}
	from, _ := LookupOebbStation("prague")
	to, _ := LookupOebbStation("vienna")
	for _, w := range []struct{ dep, arr, train, booking string }{
		{"2026-11-17T06:01:00", "2026-11-17T10:21:00", "RGJ 1031", buildOebbBookingURL(from, to, "2026-11-17")},
		{"2026-11-17T06:37:00", "2026-11-17T10:54:00", "RJ 53", oebbFixtureClickout(t, oebbTripFixture, "063700")},
	} {
		r, ok := byTimes[w.dep+" "+w.arr]
		if !ok {
			t.Errorf("no %s %s -> %s", w.train, w.dep, w.arr)
			continue
		}
		if r.Transfers != 0 || len(r.Legs) != 1 || r.Legs[0].Provider != w.train {
			t.Errorf("%s: transfers %d legs %+v, want direct %s", w.dep, r.Transfers, r.Legs, w.train)
		}
		if r.BookingURL != w.booking {
			t.Errorf("%s booking URL = %q, want %q", w.train, r.BookingURL, w.booking)
		}
	}
}

// HAFAS reports failures inside an HTTP 200 body. With the shop refusing too,
// SearchOebb returns no routes and an error naming both causes.
func TestSearchOebb_ShopBlockedAndTimetableErrorFails(t *testing.T) {
	mock := &oebbMock{shopToken: oebbShopRefuses, mgateFile: "oebb_hafas_tripsearch_error_2026-11-15.json"}
	mock.install(t)

	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	if len(routes) != 0 {
		t.Errorf("got %d routes, want none", len(routes))
	}
	assertOebbBlockedErr(t, err)
	if err != nil && !strings.Contains(err.Error(), "LOCATION") {
		t.Errorf("error text does not name the HAFAS failure: %q", err.Error())
	}
}

// A city outside the ÖBB table is not served: skipped before any network call.
func TestSearchOebb_UnservedCityNotApplicable(t *testing.T) {
	for _, pair := range [][2]string{{"NoSuchCityXYZ", "Munich"}, {"Vienna", "NoSuchCityXYZ"}} {
		_, err := SearchOebb(context.Background(), pair[0], pair[1], "2026-07-01", "EUR")
		if !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("%s -> %s: err = %v, want ErrNotApplicable", pair[0], pair[1], err)
		}
	}
}

// serveOebbShop answers the four shop calls of a successful search: two
// connections, only c1 priced. It returns the User-Agent each call carried.
func serveOebbShop(t *testing.T) func() map[string]string {
	t.Helper()
	origClient, origLimiter := oebbClient, oebbLimiter
	oebbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { oebbClient, oebbLimiter = origClient, origLimiter })
	conn := func(id, dep, arr string) oebbShopConnection {
		c := oebbShopConnection{ID: id, Duration: 4 * 3600 * 1000}
		c.From.Departure, c.To.Arrival = dep, arr
		return c
	}
	var mu sync.Mutex
	uas := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uas[r.URL.Path] = r.Header.Get("User-Agent")
		mu.Unlock()
		switch r.URL.Path {
		case "/api/domain/v1/anonymousToken":
			_ = json.NewEncoder(w).Encode(oebbShopAnonymousTokenResponse{AccessToken: "tok"})
		case "/api/domain/v1/initUserData":
			w.WriteHeader(http.StatusNoContent)
		case "/api/hafas/v4/timetable":
			_ = json.NewEncoder(w).Encode(oebbShopTimetableResponse{Connections: []oebbShopConnection{
				conn("c1", "2026-11-17T08:37:00.000", "2026-11-17T12:49:00.000"),
				conn("c2", "2026-11-17T09:37:00.000", "2026-11-17T13:49:00.000"),
			}})
		case "/api/offer/v1/prices":
			_ = json.NewEncoder(w).Encode(oebbShopPricesResponse{Offers: []oebbShopOffer{{ConnectionID: "c1", Price: 29.9}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oebbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	return func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]string, len(uas))
		for k, v := range uas {
			out[k] = v
		}
		return out
	}
}

// A fare the shop quotes for a departure is live; a departure the shop did
// not price stays at 0 and is never labelled live or estimated.
func TestSearchOebb_ShopFaresAreLive(t *testing.T) {
	serveOebbShop(t)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	if err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	byDep := map[string]models.GroundRoute{}
	for _, r := range routes {
		byDep[r.Departure.Time] = r
	}
	if len(routes) != 2 || len(byDep) != 2 {
		t.Fatalf("got %d routes (%d departures), want 2", len(routes), len(byDep))
	}
	if r := byDep["2026-11-17T08:37:00"]; r.Price != 29.9 || r.PriceProvenance != models.PriceLive {
		t.Errorf("priced 08:37: %v %q, want 29.9 live", r.Price, r.PriceProvenance)
	}
	if r := byDep["2026-11-17T09:37:00"]; r.Price != 0 || (r.PriceProvenance != "" && r.PriceProvenance != models.PriceUnavailable) {
		t.Errorf("unpriced 09:37: %v %q, want 0 and unavailable", r.Price, r.PriceProvenance)
	}
}

// The shop's bot rule refuses Go's default User-Agent and accepts a browser
// one over the same TLS (live probe, 2026-11-15), so every shop call sends one.
func TestSearchOebb_ShopCallsSendBrowserUserAgent(t *testing.T) {
	uas := serveOebbShop(t)
	if _, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR"); err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	got := uas()
	for _, path := range []string{
		"/api/domain/v1/anonymousToken",
		"/api/domain/v1/initUserData",
		"/api/hafas/v4/timetable",
		"/api/offer/v1/prices",
	} {
		ua, called := got[path]
		switch {
		case !called:
			t.Errorf("%s was not called", path)
		case !strings.HasPrefix(ua, "Mozilla/5.0") || strings.Contains(ua, "Go-http-client"):
			t.Errorf("%s User-Agent = %q, want a browser User-Agent", path, ua)
		}
	}
}

// The fallback answers the shop's question (trains only): it asks HAFAS for
// rail products, and drops a bus or U-Bahn connection that arrives anyway.
// The fixture was captured with the all-products mask, so it holds three rail
// connections (one RGJ + REX change) and two bus/U-Bahn ones.
func TestSearchOebb_TimetableFallbackIsRailOnly(t *testing.T) {
	mock := &oebbMock{shopToken: oebbShopRefuses, mgateFile: oebbTripFixture}
	mock.install(t)

	routes, _ := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	got := map[string]string{} // "dep arr" -> leg providers
	for _, r := range routes {
		var names []string
		for _, l := range r.Legs {
			names = append(names, l.Provider)
		}
		got[r.Departure.Time+" "+r.Arrival.Time] = strings.Join(names, "+")
	}
	want := map[string]string{
		"2026-11-17T06:01:00 2026-11-17T10:21:00": "RGJ 1031",
		"2026-11-17T06:01:00 2026-11-17T10:51:00": "RGJ 1031+REX 1 (Train-No. 22327)",
		"2026-11-17T06:37:00 2026-11-17T10:54:00": "RJ 53",
	}
	for k, legs := range want {
		if got[k] != legs {
			t.Errorf("rail connection %s: legs %q, want %q", k, got[k], legs)
		}
	}
	for k, legs := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("non-rail connection %s (%s) emitted as a train", k, legs)
		}
	}

	calls := mock.calls()
	if len(calls) == 0 {
		t.Fatal("no HAFAS request")
	}
	raw, _ := json.Marshal(calls[0])
	// Position i of the BIT filter selects HAFAS product class 2^i; rail is
	// classes 1-32 and 4096 (verified live: no bus or U-Bahn rows returned).
	if !strings.Contains(string(raw), `"value":"1111110000001000"`) {
		t.Errorf("HAFAS product filter is not rail-only: %s", raw)
	}
}
