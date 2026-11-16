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
// day-best endpoint with no intervals; it returns the day-best request count.
func serveDBFixture(t *testing.T, fixture string) *atomic.Int32 {
	t.Helper()
	body := loadGroundFixture(t, fixture)
	var bestPriceHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/tagesbestpreis") {
			bestPriceHits.Add(1)
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
	hits := serveDBFixture(t, "db_fahrplan_praha_8103000_2026-11-15.json")

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
	hits := serveDBFixture(t, "db_fahrplan_berlin_5100065_2026-11-15.json")

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
		{"Athens", "Mykonos", true},      // ATHENS (ALL PORTS)
		{"Rome", "Barcelona", true},      // Rome resolves to Civitavecchia
		{"Prague", "Vienna", false},      // Prague is no port
		{"Prague", "Berlin", false},
		{"", "Piraeus", false},
	} {
		if got := HasFerryhopperRoute(c.from, c.to); got != c.want {
			t.Errorf("HasFerryhopperRoute(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
