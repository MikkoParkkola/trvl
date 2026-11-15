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

// A fare DB quotes for the journey is live; the day's best price copied onto
// an unpriced journey is an estimate for that departure, never live.
func TestSearchDeutscheBahn_PriceProvenance(t *testing.T) {
	serveDB(t, 19.9)
	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDeutscheBahn: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3", len(routes))
	}
	want := []struct {
		price float64
		prov  models.PriceProvenance
	}{
		{49.9, models.PriceLive},
		{59.9, models.PriceLive},
		{19.9, models.PriceEstimated},
	}
	for i, w := range want {
		if routes[i].Price != w.price || routes[i].PriceProvenance != w.prov {
			t.Errorf("route %d: price %v provenance %q, want %v %q", i, routes[i].Price, routes[i].PriceProvenance, w.price, w.prov)
		}
	}
}

// Without any fare the journey stays unpriced and is never labelled live or
// estimated.
func TestSearchDeutscheBahn_NoFareIsUnavailable(t *testing.T) {
	serveDB(t, 0)
	routes, err := SearchDeutscheBahn(context.Background(), "Berlin", "Munich", "2026-07-01", "EUR")
	if err != nil {
		t.Fatalf("SearchDeutscheBahn: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3", len(routes))
	}
	r := routes[2]
	if r.Price != 0 || (r.PriceProvenance != "" && r.PriceProvenance != models.PriceUnavailable) {
		t.Errorf("unpriced journey: price %v provenance %q, want 0 and unavailable", r.Price, r.PriceProvenance)
	}
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
