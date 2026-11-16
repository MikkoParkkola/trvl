package ground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// serveDB answers the DB journey search with three journeys (a nested fare, a
// top-level fare, no fare) and the day-best endpoint with bestPrice.
func serveDB(t *testing.T, bestPrice float64) {
	t.Helper()
	origClient, origLimiter := dbClient, dbLimiter
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { dbClient, dbLimiter = origClient, origLimiter })
	leg := func(dep, arr string) []dbAbschnitt {
		return []dbAbschnitt{{AbgangsDatum: dep, AnkunftsDatum: arr, Typ: "FAHRZEUG"}}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/tagesbestpreis") {
			var itv []dbBestPriceInterval
			if bestPrice > 0 {
				itv = append(itv, dbBestPriceInterval{AngebotsPreis: &dbPreis{Betrag: bestPrice, Waehrung: "EUR"}})
			}
			_ = json.NewEncoder(w).Encode(dbBestPriceResponse{TagesbestPreisIntervalle: itv})
			return
		}
		_ = json.NewEncoder(w).Encode(dbJourneysResponse{Verbindungen: []dbVerbindung{
			{
				VerbindungsAbschnitte: leg("2026-07-01T06:30:00", "2026-07-01T10:30:00"),
				Angebote:              &dbAngebote{Preise: &dbPreise{Gesamt: &dbGesamt{Ab: &dbPreis{Betrag: 49.9, Waehrung: "EUR"}}}},
			},
			{
				VerbindungsAbschnitte: leg("2026-07-01T07:30:00", "2026-07-01T11:30:00"),
				AngebotsPreis:         &dbPreis{Betrag: 59.9, Waehrung: "EUR"},
			},
			{VerbindungsAbschnitte: leg("2026-07-01T08:30:00", "2026-07-01T12:30:00")},
		}})
	}))
	t.Cleanup(srv.Close)
	dbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
}

// assertProvenanceByFare matches routes to expectations by fare, so the test
// does not depend on route order; every expected fare must appear once.
// PriceUnavailable also accepts an unlabelled route, which the merge pass
// labels unavailable.
func assertProvenanceByFare(t *testing.T, routes []models.GroundRoute, want map[float64]models.PriceProvenance) {
	t.Helper()
	if len(routes) != len(want) {
		t.Fatalf("got %d routes, want %d", len(routes), len(want))
	}
	seen := map[float64]bool{}
	for _, r := range routes {
		w, ok := want[r.Price]
		if !ok || seen[r.Price] {
			t.Errorf("unexpected fare %v (provenance %q)", r.Price, r.PriceProvenance)
			continue
		}
		seen[r.Price] = true
		if r.PriceProvenance != w && (w != models.PriceUnavailable || r.PriceProvenance != "") {
			t.Errorf("fare %v: provenance %q, want %q", r.Price, r.PriceProvenance, w)
		}
	}
}

// A fare DB quotes for the journey is live; the day's best price copied onto
// an unpriced journey is an estimate for that departure, never live.
func TestSearchDeutscheBahn_PriceProvenance(t *testing.T) {
	serveDB(t, 19.9)
	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDeutscheBahn: %v", err)
	}
	assertProvenanceByFare(t, routes, map[float64]models.PriceProvenance{
		49.9: models.PriceLive,
		59.9: models.PriceLive,
		19.9: models.PriceEstimated,
	})
}

// With an empty day-best answer the quoted fares stay live and the unpriced
// journey stays at 0, never labelled live or estimated.
func TestSearchDeutscheBahn_NoFareIsUnavailable(t *testing.T) {
	serveDB(t, 0)
	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDeutscheBahn: %v", err)
	}
	assertProvenanceByFare(t, routes, map[float64]models.PriceProvenance{
		49.9: models.PriceLive,
		59.9: models.PriceLive,
		0:    models.PriceUnavailable,
	})
}

// A city outside the DB table is not served: skipped before any network call.
func TestSearchDeutscheBahn_UnservedCityNotApplicable(t *testing.T) {
	for _, pair := range [][2]string{{"NoSuchCityXYZ", "Munich"}, {"Berlin", "NoSuchCityXYZ"}} {
		_, err := SearchDeutscheBahn(context.Background(), pair[0], pair[1], "2026-07-01", "EUR")
		if !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("%s -> %s: err = %v, want ErrNotApplicable", pair[0], pair[1], err)
		}
	}
}

// DB's bot wall answers HTTP 452 {"code":"OPS_BLOCKED"} (seen live from a
// plain client on 2026-11-15). It is a retryable refusal, typed as such.
func TestSearchDeutscheBahn_OpsBlockedIsRateLimited(t *testing.T) {
	origClient, origLimiter := dbClient, dbLimiter
	dbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { dbClient, dbLimiter = origClient, origLimiter })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(452)
		_, _ = w.Write([]byte(`{"status":"ERROR","code":"OPS_BLOCKED","errorRefId": "0.fd72c17.1794780601.4051813"}`))
	}))
	t.Cleanup(srv.Close)
	dbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}

	_, err := SearchDeutscheBahn(context.Background(), "Prague", "Vienna", "2026-11-17", "EUR")
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want it to wrap ErrRateLimited", err)
	}
	if got := models.ClassifyProviderReason(err.Error()); got != models.ReasonBlocked {
		t.Errorf("reason = %q, want %q", got, models.ReasonBlocked)
	}
}
