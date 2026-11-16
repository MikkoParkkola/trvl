package ground

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// TestSearchByNameCore_FerryhopperGateSkipsInlandPair: Prague–Vienna has no
// ferry port, so Ferryhopper is never asked (its resolver would answer with a
// fuzzy foreign port) and the requested provider gets a skipped row.
func TestSearchByNameCore_FerryhopperGateSkipsInlandPair(t *testing.T) {
	isolateGroundBreaker(t)
	var hits atomic.Int32
	withFerryhopperServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ferryhopperFrame(ferryhopperEmptyFrame)(w, r)
	})
	res := runCore(t, context.Background(), "Prague", "Vienna", "ferryhopper")
	if n := hits.Load(); n != 0 {
		t.Errorf("ferryhopper requests = %d, want 0 for a pair without ferry ports", n)
	}
	st, ok := statusByID(res.ProviderStatuses)["ferryhopper"]
	if !ok || st.Status != models.StatusSkipped || st.Reason != models.ReasonRouteNotServed {
		t.Errorf("ferryhopper row = %+v (present %v), want skipped / route_not_served", st, ok)
	}
}

// failOnNetwork returns a client that counts requests and fails each one, so a
// test can prove a call never left the process.
func failOnNetwork(hits *atomic.Int32) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		hits.Add(1)
		return nil, errors.New("network call for a pair the provider does not sail")
	})}
}

// TestFinnlines_UnservedPortPairNotApplicable: both ports are Finnlines ports
// but no Finnlines line joins them, so the pair is not served and no request
// is made. Every line the provider sails stays served.
func TestFinnlines_UnservedPortPairNotApplicable(t *testing.T) {
	origClient, origLimiter := finnlinesClient, finnlinesLimiter
	t.Cleanup(func() { finnlinesClient, finnlinesLimiter = origClient, origLimiter })
	finnlinesLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	var hits atomic.Int32
	finnlinesClient = failOnNetwork(&hits)

	for _, p := range [][2]string{{"Naantali", "Travemünde"}, {"Malmö", "Kapellskär"}} {
		if HasFinnlinesRoute(p[0], p[1]) {
			t.Errorf("HasFinnlinesRoute(%s, %s) = true, want false", p[0], p[1])
		}
		if _, err := SearchFinnlines(context.Background(), p[0], p[1], "2026-07-15", "EUR"); !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("SearchFinnlines(%s, %s) err = %v, want models.ErrNotApplicable", p[0], p[1], err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("finnlines requests = %d, want 0 for unserved pairs", n)
	}
	for _, p := range [][2]string{
		{"Helsinki", "Travemünde"}, {"Travemünde", "Helsinki"},
		{"Naantali", "Kapellskär"}, {"Kapellskär", "Naantali"},
		{"Malmö", "Travemünde"}, {"Malmö", "Świnoujście"},
	} {
		if !HasFinnlinesRoute(p[0], p[1]) {
			t.Errorf("HasFinnlinesRoute(%s, %s) = false, want a served line", p[0], p[1])
		}
	}
}

// TestTallink_UnservedPortPairNotApplicable: Helsinki–Riga and Turku–Tallinn
// join Tallink ports but no Tallink line, so the pair is not served and no
// request is made. The sailed lines stay served.
func TestTallink_UnservedPortPairNotApplicable(t *testing.T) {
	origClient, origLimiter := tallinkClient, tallinkLimiter
	t.Cleanup(func() { tallinkClient, tallinkLimiter = origClient, origLimiter })
	tallinkLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	var hits atomic.Int32
	tallinkClient = failOnNetwork(&hits)

	for _, p := range [][2]string{{"Helsinki", "Riga"}, {"Turku", "Tallinn"}} {
		if HasTallinkRoute(p[0], p[1]) {
			t.Errorf("HasTallinkRoute(%s, %s) = true, want false", p[0], p[1])
		}
		if _, err := SearchTallink(context.Background(), p[0], p[1], "2026-07-15", "EUR"); !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("SearchTallink(%s, %s) err = %v, want models.ErrNotApplicable", p[0], p[1], err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("tallink requests = %d, want 0 for unserved pairs", n)
	}
	for _, p := range [][2]string{
		{"Helsinki", "Tallinn"}, {"Stockholm", "Riga"}, {"Stockholm", "Helsinki"},
		{"Turku", "Stockholm"}, {"Kapellskär", "Paldiski"}, {"Helsinki", "Mariehamn"},
	} {
		if !HasTallinkRoute(p[0], p[1]) {
			t.Errorf("HasTallinkRoute(%s, %s) = false, want a served line", p[0], p[1])
		}
	}
}

// serveDBFixture answers the DB journey search with a captured fixture and the
// day-best endpoint with bestPrice (nil: a decoded 200 with no intervals); it
// returns the day-best request count.
func serveDBFixture(t *testing.T, fixture string, bestPrice http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	body := loadGroundFixture(t, fixture)
	var bestPriceHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/tagesbestpreis") {
			bestPriceHits.Add(1)
			if bestPrice != nil {
				bestPrice(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"tagesbestPreisIntervalle":[]}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	origClient, origLimiter := dbClient, dbLimiter
	t.Cleanup(func() {
		srv.Close()
		dbClient, dbLimiter = origClient, origLimiter
	})
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	dbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	return &bestPriceHits
}

// TestSearchDeutscheBahn_NoFaresNoticeNamesCarriers replays DB's Praha–Wien
// answer (captured 2026-11-15): five journeys, none with a fare. DB sells no
// ticket here, so the routes come back with a notice that names the carriers
// in DB's own segments, after both day-best request bodies came back empty.
func TestSearchDeutscheBahn_NoFaresNoticeNamesCarriers(t *testing.T) {
	hits := serveDBFixture(t, "db_fahrplan_praha_8103000_2026-11-15.json", nil)

	routes, err := SearchDeutscheBahn(context.Background(), "Prague", "Vienna", "2026-11-15", "EUR")
	if len(routes) != 5 {
		t.Errorf("routes = %d, want the 5 journeys kept", len(routes))
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("day-best requests = %d, want 2 (body A empty, then body B)", n)
	}
	var notice *models.ProviderNotice
	if !errors.As(err, &notice) {
		t.Fatalf("err = %v, want a *models.ProviderNotice", err)
	}
	for _, carrier := range []string{"Ceske Drahy", "RegioJet", "Österreichische Bundesbahnen"} {
		if !strings.Contains(notice.FixHint, carrier) {
			t.Errorf("FixHint = %q, want it to name %s", notice.FixHint, carrier)
		}
	}
}

// TestSearchDeutscheBahn_PricedJourneysCarryNoNotice: Berlin–Warsaw journeys
// all carry fares, so there is no notice and no day-best request.
func TestSearchDeutscheBahn_PricedJourneysCarryNoNotice(t *testing.T) {
	hits := serveDBFixture(t, "db_fahrplan_berlin_5100065_2026-11-15.json", nil)

	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Warsaw", "2026-11-15", "EUR")
	if err != nil || len(routes) == 0 {
		t.Fatalf("routes = %d, err = %v; want priced routes and no error", len(routes), err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("day-best requests = %d, want 0 when every journey is priced", n)
	}
}

// TestHasFerryhopperRoute pins the launch gate: both ends must match a
// Ferryhopper port name, its parts, or an alias; the pair is left to the live
// search.
func TestHasFerryhopperRoute(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"Piraeus", "Santorini", true},   // THIRA (SANTORINI)
		{"Helsinki", "Travemünde", true}, // accents folded
		{"Athens", "Santorini", true},    // ATHENS (ALL PORTS), THIRA (SANTORINI)
		{"Athens", "Mykonos", true},
		{"Rome", "Barcelona", true},       // Rome resolves to Civitavecchia
		{"Vuosaari", "Travemünde", true},  // HELSINKI - VUOSAARI
		{"Piazzale Roma", "Athens", true}, // SANTA LUCIA (PIAZZALE ROMA), VENICE
		{"Pozzallo", "Malta", true},       // MALTA (ALL PORTS): island countries stay
		{"Malta", "Pozzallo", true},
		{"Germany", "Denmark", false}, // country groups are no place
		{"Helsinki", "Germany", false},
		{"Prague", "Vienna", false}, // Prague is no port
		{"Prague", "Berlin", false},
		{"", "Piraeus", false},
	} {
		if got := HasFerryhopperRoute(c.from, c.to); got != c.want {
			t.Errorf("HasFerryhopperRoute(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// TestSearchByNameCore_FerryhopperGateLaunchesPortPairs: pairs whose ends are
// Ferryhopper ports are searched, and an empty answer there stays a valid
// empty day (checked_no_hit), never "not served".
func TestSearchByNameCore_FerryhopperGateLaunchesPortPairs(t *testing.T) {
	for _, pair := range [][2]string{{"Athens", "Santorini"}, {"Helsinki", "Travemünde"}, {"Pozzallo", "Malta"}} {
		t.Run(pair[0]+"-"+pair[1], func(t *testing.T) {
			isolateGroundBreaker(t)
			var hits atomic.Int32
			withFerryhopperServer(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				ferryhopperFrame(ferryhopperEmptyFrame)(w, r)
			})
			res := runCore(t, context.Background(), pair[0], pair[1], "ferryhopper")
			if hits.Load() == 0 {
				t.Error("ferryhopper was not asked for a pair of its ports")
			}
			if st := statusByID(res.ProviderStatuses)["ferryhopper"]; st.Status != models.StatusCheckedNoHit {
				t.Errorf("ferryhopper status = %q, want checked_no_hit", st.Status)
			}
		})
	}
}

// TestTallink_SiljaMariehamnCallsServed: Silja ships call at Mariehamn on the
// Stockholm and Turku lines (live timetables, 2026-12-04..10).
func TestTallink_SiljaMariehamnCallsServed(t *testing.T) {
	for _, p := range [][2]string{
		{"Stockholm", "Mariehamn"}, {"Mariehamn", "Stockholm"},
		{"Turku", "Mariehamn"}, {"Mariehamn", "Turku"},
	} {
		if !HasTallinkRoute(p[0], p[1]) {
			t.Errorf("HasTallinkRoute(%s, %s) = false, want a served line", p[0], p[1])
		}
	}
}

// TestSearchDeutscheBahn_FailedPriceLookupIsNotNoFares: when every day-best
// call fails, DB has not said it sells nothing; the notice reports the failed
// lookup instead of "DB sells no fares".
func TestSearchDeutscheBahn_FailedPriceLookupIsNotNoFares(t *testing.T) {
	for _, c := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"HTTP 500", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }},
		{"undecodable 200", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>busy</html>")) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			serveDBFixture(t, "db_fahrplan_praha_8103000_2026-11-15.json", c.h)
			routes, err := SearchDeutscheBahn(context.Background(), "Prague", "Vienna", "2026-11-15", "EUR")
			if len(routes) != 5 {
				t.Errorf("routes = %d, want the 5 journeys kept", len(routes))
			}
			var notice *models.ProviderNotice
			if !errors.As(err, &notice) {
				t.Fatalf("err = %v, want a *models.ProviderNotice", err)
			}
			if strings.Contains(notice.FixHint, "sells no fares") {
				t.Errorf("FixHint = %q, want a failed-lookup hint, not no-fares", notice.FixHint)
			}
			if notice.Err == nil {
				t.Error("notice carries no cause")
			}
		})
	}
}

// TestFerryhopperPortListForm pins the embedded list to the generator's form:
// trimmed, unique, byte-sorted names, newline-terminated, and no country group
// such as "GERMANY (ALL PORTS)".
func TestFerryhopperPortListForm(t *testing.T) {
	if !strings.HasSuffix(ferryhopperPortList, "\n") {
		t.Fatal("ferryhopper_ports.txt must end with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(ferryhopperPortList, "\n"), "\n")
	for i, line := range lines {
		if line == "" || line != strings.TrimSpace(line) {
			t.Errorf("line %d = %q, want a trimmed, non-empty name", i+1, line)
		}
		if i > 0 && lines[i-1] >= line {
			t.Errorf("line %d %q is not after %q: want unique, byte-sorted names", i+1, line, lines[i-1])
		}
	}
	if !strings.Contains(ferryhopperPortList, "\nMALTA (ALL PORTS)\n") {
		t.Error("MALTA (ALL PORTS) is an island destination and must stay in the list")
	}
	for _, country := range []string{"GERMANY", "DENMARK", "SPAIN", "FINLAND"} {
		if strings.Contains(ferryhopperPortList, "\n"+country+" (ALL PORTS)\n") {
			t.Errorf("%s (ALL PORTS) is a country group and must not be in the list", country)
		}
	}
}

// sequencedDayBest answers the day-best endpoint with first, then second.
func sequencedDayBest(first, second http.HandlerFunc) http.HandlerFunc {
	var n atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			first(w, r)
			return
		}
		second(w, r)
	}
}

var (
	dayBestEmpty = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tagesbestPreisIntervalle":[]}`))
	}
	dayBestFailed  = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	dayBestNoField = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"fehlerNachricht":{"code":"X"}}`))
	}
	dayBestPriced = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tagesbestPreisIntervalle":[{"angebotsPreis":{"betrag":27.99,"waehrung":"eur"}}]}`))
	}
)

// TestFetchDBBestPrice_NoFaresNeedsEveryBodyClean: "DB sells no fares" needs
// every attempted request body to answer cleanly with an empty interval list.
// One failed body (or a 200 without the intervals field) makes it a failed
// lookup; a price from either body wins.
func TestFetchDBBestPrice_NoFaresNeedsEveryBodyClean(t *testing.T) {
	for _, c := range []struct {
		name          string
		first, second http.HandlerFunc
		wantPrice     float64
		wantErr       error // nil: a clean answer
	}{
		{"clean empty, then failed", dayBestEmpty, dayBestFailed, 0, models.ErrRateLimited},
		{"clean empty twice", dayBestEmpty, dayBestEmpty, 0, nil},
		{"failed, then priced", dayBestFailed, dayBestPriced, 27.99, nil},
		{"200 without the intervals field", dayBestNoField, dayBestNoField, 0, models.ErrParseFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			origClient, origLimiter := dbClient, dbLimiter
			t.Cleanup(func() { dbClient, dbLimiter = origClient, origLimiter })
			dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
			srv := httptest.NewServer(sequencedDayBest(c.first, c.second))
			t.Cleanup(srv.Close)
			dbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}

			price, _, err := fetchDBBestPrice(context.Background(), "8011160", "8010205", "2026-07-01")
			if price != c.wantPrice {
				t.Errorf("price = %v, want %v", price, c.wantPrice)
			}
			if c.wantErr == nil && err != nil {
				t.Errorf("err = %v, want a clean answer", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, c.wantErr)
			}
		})
	}
}

// TestSearchDeutscheBahn_HalfFailedLookupIsNotNoFares: body A answers cleanly
// empty and body B fails, so DB has not shown it sells nothing.
func TestSearchDeutscheBahn_HalfFailedLookupIsNotNoFares(t *testing.T) {
	serveDBFixture(t, "db_fahrplan_praha_8103000_2026-11-15.json", sequencedDayBest(dayBestEmpty, dayBestFailed))
	_, err := SearchDeutscheBahn(context.Background(), "Prague", "Vienna", "2026-11-15", "EUR")
	var notice *models.ProviderNotice
	if !errors.As(err, &notice) {
		t.Fatalf("err = %v, want a *models.ProviderNotice", err)
	}
	if strings.Contains(notice.FixHint, "sells no fares") {
		t.Errorf("FixHint = %q, want the failed-lookup hint", notice.FixHint)
	}
}

// TestFerryhopperPortNames_CRLF: a Windows checkout may turn the embedded list
// into CRLF; the reader must give the same names and places either way.
func TestFerryhopperPortNames_CRLF(t *testing.T) {
	lf := ferryhopperPortNames(ferryhopperPortList)
	crlf := ferryhopperPortNames(strings.ReplaceAll(ferryhopperPortList, "\n", "\r\n"))
	if len(lf) != len(crlf) {
		t.Fatalf("names: %d from LF, %d from CRLF", len(lf), len(crlf))
	}
	for i := range lf {
		if lf[i] != crlf[i] {
			t.Fatalf("name %d = %q from CRLF, want %q", i, crlf[i], lf[i])
		}
	}
	if a, b := ferryhopperPlacesFrom(lf), ferryhopperPlacesFrom(crlf); len(a) != len(b) {
		t.Errorf("places: %d from LF, %d from CRLF", len(a), len(b))
	}
}
