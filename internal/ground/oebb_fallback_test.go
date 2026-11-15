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

// A fare the shop quotes for a departure is live; a departure the shop did
// not price stays at 0 and is never labelled live.
func TestSearchOebb_ShopFaresAreLive(t *testing.T) {
	conn := func(id, dep, arr string) oebbShopConnection {
		c := oebbShopConnection{ID: id, Duration: 4 * 3600 * 1000}
		c.From.Departure, c.To.Arrival = dep, arr
		return c
	}
	origClient, origLimiter := oebbClient, oebbLimiter
	oebbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { oebbClient, oebbLimiter = origClient, origLimiter })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/anonymousToken"):
			_ = json.NewEncoder(w).Encode(oebbShopAnonymousTokenResponse{AccessToken: "tok"})
		case strings.HasSuffix(r.URL.Path, "/initUserData"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/timetable"):
			_ = json.NewEncoder(w).Encode(oebbShopTimetableResponse{Connections: []oebbShopConnection{
				conn("c1", "2026-11-17T08:37:00.000", "2026-11-17T12:49:00.000"),
				conn("c2", "2026-11-17T09:37:00.000", "2026-11-17T13:49:00.000"),
			}})
		case strings.HasSuffix(r.URL.Path, "/prices"):
			_ = json.NewEncoder(w).Encode(oebbShopPricesResponse{Offers: []oebbShopOffer{{ConnectionID: "c1", Price: 29.9}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oebbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}

	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	if err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}
	if routes[0].Price != 29.9 || routes[0].PriceProvenance != models.PriceLive {
		t.Errorf("priced departure: %v %q, want 29.9 live", routes[0].Price, routes[0].PriceProvenance)
	}
	if routes[1].Price != 0 || routes[1].PriceProvenance == models.PriceLive || routes[1].PriceProvenance == models.PriceEstimated {
		t.Errorf("unpriced departure: %v %q, want 0 and unavailable", routes[1].Price, routes[1].PriceProvenance)
	}
}

// The shop's bot rule refuses Go's default User-Agent and accepts a browser
// one over the same TLS (live probe, 2026-11-15), so shop calls must send one.
func TestSearchOebb_ShopSendsBrowserUserAgent(t *testing.T) {
	var mu sync.Mutex
	var ua string
	mock := &oebbMock{shopToken: func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ua = r.Header.Get("User-Agent")
		mu.Unlock()
		oebbShopRefuses(w, r)
	}, mgateFile: oebbTripFixture}
	mock.install(t)

	_, _ = SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasPrefix(ua, "Mozilla/5.0") || strings.Contains(ua, "Go-http-client") {
		t.Errorf("shop token request User-Agent = %q, want a browser User-Agent", ua)
	}
}

// The fallback answers the shop's question (trains only): it asks HAFAS for
// rail products, and a bus or U-Bahn row that still arrives is not emitted.
func TestSearchOebb_TimetableFallbackIsRailOnly(t *testing.T) {
	mock := &oebbMock{shopToken: oebbShopRefuses, mgateFile: oebbTripFixture}
	mock.install(t)

	routes, _ := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	if len(routes) == 0 {
		t.Fatal("no routes from the timetable fallback")
	}
	for _, r := range routes {
		for _, l := range r.Legs {
			if strings.HasPrefix(l.Provider, "Bus") || l.Provider == "U3" || l.Provider == "C" {
				t.Errorf("%s: non-rail leg %q emitted as a train", r.Departure.Time, l.Provider)
			}
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
