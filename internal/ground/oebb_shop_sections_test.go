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

// serveOebbShopCapture answers the shop with the Prague -> Vienna timetable
// and prices recorded on 2026-11-17 (travel date 2026-11-20). mutate, when
// set, edits the decoded connections before they are served; a non-nil
// prices handler replaces the recorded price answer.
func serveOebbShopCapture(t *testing.T, mutate func(conns []map[string]any), prices http.HandlerFunc) {
	t.Helper()
	var timetable struct {
		Connections []map[string]any `json:"connections"`
	}
	if err := json.Unmarshal([]byte(loadGroundFixture(t, "oebb_shop_timetable_praha_wien_2026-11-17.json")), &timetable); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(timetable.Connections)
	}
	timetableBody, err := json.Marshal(timetable)
	if err != nil {
		t.Fatal(err)
	}
	pricesBody := loadGroundFixture(t, "oebb_shop_prices_praha_wien_2026-11-17.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/domain/v1/anonymousToken":
			_, _ = w.Write([]byte(`{"access_token":"tok"}`))
		case "/api/domain/v1/initUserData":
			w.WriteHeader(http.StatusNoContent)
		case "/api/hafas/v4/timetable":
			_, _ = w.Write(timetableBody)
		case "/api/offer/v1/prices":
			if prices != nil {
				prices(w, r)
				return
			}
			_, _ = w.Write([]byte(pricesBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	origClient, origLimiter := oebbClient, oebbLimiter
	t.Cleanup(func() {
		srv.Close()
		oebbClient, oebbLimiter = origClient, origLimiter
	})
	oebbLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	oebbClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
}

func oebbRoutesByDeparture(t *testing.T, routes []models.GroundRoute) map[string]models.GroundRoute {
	t.Helper()
	by := make(map[string]models.GroundRoute, len(routes))
	for _, r := range routes {
		by[r.Departure.Time[11:16]] = r
	}
	if len(routes) != 5 || len(by) != 5 {
		t.Fatalf("got %d routes (%d departures), want the 5 recorded connections", len(routes), len(by))
	}
	return by
}

// connectionAt finds the decoded connection leaving at hhmm.
func connectionAt(conns []map[string]any, hhmm string) map[string]any {
	for _, c := range conns {
		if from, ok := c["from"].(map[string]any); ok && strings.Contains(from["departure"].(string), "T"+hhmm) {
			return c
		}
	}
	return nil
}

// TestSearchOebbShop_TransfersAndLegs: the shop's timetable carries each
// train section; four of the five recorded trains change once.
func TestSearchOebbShop_TransfersAndLegs(t *testing.T) {
	serveOebbShopCapture(t, nil, nil)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	by := oebbRoutesByDeparture(t, routes)

	for dep, want := range map[string]int{"08:37": 0, "09:37": 1, "10:08": 1, "10:21": 1, "11:37": 1} {
		if r := by[dep]; r.Transfers != want || r.TransfersUnknown {
			t.Errorf("%s: transfers = %d (unknown %v), want %d known", dep, r.Transfers, r.TransfersUnknown, want)
		}
	}
	if r := by["09:37"]; r.Price != 22.8 {
		t.Errorf("09:37 price = %v, want 22.8 from the recorded offers", r.Price)
	}

	direct := by["08:37"].Legs
	if len(direct) != 1 || direct[0].Provider != "RJ 55" || direct[0].Type != "train" {
		t.Errorf("08:37 legs = %+v, want one train leg RJ 55", direct)
	}

	want := []models.GroundLeg{
		{Type: "train", Provider: "EC 333", Duration: 225,
			Departure: models.GroundStop{City: "Prague", Station: "Praha hl.n.", Time: "2026-11-20T10:21:00"},
			Arrival:   models.GroundStop{Station: "Linz/Donau Hbf", Time: "2026-11-20T14:06:00"}},
		{Type: "train", Provider: "RJX 65", Duration: 75,
			Departure: models.GroundStop{Station: "Linz/Donau Hbf", Time: "2026-11-20T14:17:00"},
			Arrival:   models.GroundStop{City: "Vienna", Station: "Wien Hbf (Bahnsteige 3-12)", Time: "2026-11-20T15:32:00"}},
	}
	got := by["10:21"].Legs
	if len(got) != len(want) {
		t.Fatalf("10:21 legs = %+v, want %d", got, len(want))
	}
	for i := range want {
		if got[i].Type != want[i].Type || got[i].Provider != want[i].Provider || got[i].Duration != want[i].Duration ||
			got[i].Departure != want[i].Departure || got[i].Arrival != want[i].Arrival {
			t.Errorf("10:21 leg %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestSearchOebbShop_ZeroDurationSectionUsesTimes: a section without a
// duration still gets one, from its own times.
func TestSearchOebbShop_ZeroDurationSectionUsesTimes(t *testing.T) {
	serveOebbShopCapture(t, func(conns []map[string]any) {
		sections := connectionAt(conns, "10:21")["sections"].([]any)
		sections[0].(map[string]any)["duration"] = 0
	}, nil)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	legs := oebbRoutesByDeparture(t, routes)["10:21"].Legs
	if len(legs) != 2 || legs[0].Duration != 225 {
		t.Errorf("10:21 legs = %+v, want the first leg's 225 minutes from 10:21 -> 14:06", legs)
	}
}

func stripSectionData(c map[string]any, keepSwitches bool) {
	delete(c, "sections")
	if !keepSwitches {
		delete(c, "switches")
	}
}

func oebbNotice(t *testing.T, err error) *models.ProviderNotice {
	t.Helper()
	var notice *models.ProviderNotice
	if !errors.As(err, &notice) {
		t.Fatalf("err = %v, want a *models.ProviderNotice", err)
	}
	return notice
}

// TestSearchOebbShop_NoSectionsSaysTransfersUnknown: a shop answer without
// sections or switches cannot tell a direct train from a change, so every
// route says its count is unknown, carries no legs, and the notice says why.
func TestSearchOebbShop_NoSectionsSaysTransfersUnknown(t *testing.T) {
	serveOebbShopCapture(t, func(conns []map[string]any) {
		for _, c := range conns {
			stripSectionData(c, false)
		}
	}, nil)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	for dep, r := range oebbRoutesByDeparture(t, routes) {
		if !r.TransfersUnknown || r.Transfers != 0 || r.Legs != nil {
			t.Errorf("%s: transfers %d unknown %v legs %d, want unknown, 0 and no legs", dep, r.Transfers, r.TransfersUnknown, len(r.Legs))
		}
	}
	if n := oebbNotice(t, err); !strings.Contains(n.FixHint, "shop timetable carried no train sections") || strings.Contains(n.FixHint, " of ") {
		t.Errorf("FixHint = %q, want the whole-timetable note", n.FixHint)
	}
}

// TestSearchOebbShop_OneSectionlessConnection: only the connection without
// data is unknown; the others keep their real counts.
func TestSearchOebbShop_OneSectionlessConnection(t *testing.T) {
	serveOebbShopCapture(t, func(conns []map[string]any) {
		stripSectionData(connectionAt(conns, "10:21"), false)
	}, nil)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	by := oebbRoutesByDeparture(t, routes)
	if r := by["10:21"]; !r.TransfersUnknown {
		t.Errorf("10:21 TransfersUnknown = false, want true")
	}
	if r := by["10:08"]; r.TransfersUnknown || r.Transfers != 1 || len(r.Legs) != 2 {
		t.Errorf("10:08: transfers %d unknown %v legs %d, want 1 known with 2 legs", r.Transfers, r.TransfersUnknown, len(r.Legs))
	}
	if n := oebbNotice(t, err); !strings.Contains(n.FixHint, "transfer counts unknown") || !strings.Contains(n.FixHint, "1 of 5") {
		t.Errorf("FixHint = %q, want it to name the 1 of 5 connections whose counts are unknown", n.FixHint)
	}
}

// TestSearchOebbShop_WalkBetweenTrainsIsNoLeg: a walk between two trains is
// neither a leg nor an extra change.
func TestSearchOebbShop_WalkBetweenTrainsIsNoLeg(t *testing.T) {
	serveOebbShopCapture(t, func(conns []map[string]any) {
		c := connectionAt(conns, "10:21")
		secs := c["sections"].([]any)
		walk := map[string]any{"type": "walk", "duration": 300000,
			"from": map[string]any{"name": "Linz/Donau Hbf", "departure": "2026-11-20T14:06:00.000"},
			"to":   map[string]any{"name": "Linz/Donau Hbf", "arrival": "2026-11-20T14:11:00.000"}}
		c["sections"] = []any{secs[0], walk, secs[1]}
	}, nil)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	r := oebbRoutesByDeparture(t, routes)["10:21"]
	if r.Transfers != 1 || r.TransfersUnknown || len(r.Legs) != 2 || r.Legs[0].Provider != "EC 333" || r.Legs[1].Provider != "RJX 65" {
		t.Errorf("10:21: transfers %d (not known %v) legs %+v, want 1 known change and the two trains only", r.Transfers, r.TransfersUnknown, r.Legs)
	}
}

// TestSearchOebbShop_UnknownTransfersSurviveFailedPrices: when the price step
// also fails, the prices notice is returned and still says transfer counts
// are unknown; the routes keep their flags.
func TestSearchOebbShop_UnknownTransfersSurviveFailedPrices(t *testing.T) {
	serveOebbShopCapture(t, func(conns []map[string]any) {
		stripSectionData(connectionAt(conns, "10:21"), false)
	}, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	if r := oebbRoutesByDeparture(t, routes)["10:21"]; !r.TransfersUnknown {
		t.Errorf("10:21 TransfersUnknown = false, want true")
	}
	n := oebbNotice(t, err)
	if !strings.Contains(n.FixHint, "prices unavailable") || !strings.Contains(n.FixHint, "transfer counts unknown") {
		t.Errorf("FixHint = %q, want both the prices and the transfer-count note", n.FixHint)
	}
}

// TestSearchOebbShop_SwitchesWithoutSections: the shop's own change count
// stands when sections are missing; nothing is unknown.
func TestSearchOebbShop_SwitchesWithoutSections(t *testing.T) {
	serveOebbShopCapture(t, func(conns []map[string]any) {
		for _, c := range conns {
			stripSectionData(c, true)
		}
	}, nil)
	routes, err := SearchOebb(context.Background(), "Prague", "Vienna", "2026-11-20", "EUR")
	if err != nil {
		t.Fatalf("SearchOebb: %v", err)
	}
	by := oebbRoutesByDeparture(t, routes)
	if r := by["10:21"]; r.Transfers != 1 || r.TransfersUnknown || r.Legs != nil {
		t.Errorf("10:21: transfers %d unknown %v legs %d, want 1 from switches, known, no legs", r.Transfers, r.TransfersUnknown, len(r.Legs))
	}
	if r := by["08:37"]; r.Transfers != 0 || r.TransfersUnknown {
		t.Errorf("08:37: transfers %d unknown %v, want 0 known", r.Transfers, r.TransfersUnknown)
	}
}

// TestSortGroundRoutes_KnownTransfersBeforeUnknown: two routes equal in every
// earlier sort key reach the transfers tie-break; an unknown count must not
// rank as direct.
func TestSortGroundRoutes_KnownTransfersBeforeUnknown(t *testing.T) {
	base := models.GroundRoute{Provider: "oebb", Type: "train", Price: 30, Currency: "EUR", PriceProvenance: models.PriceLive,
		Duration: 252, BookingURL: "https://tickets.oebb.at/x",
		Departure: models.GroundStop{City: "Prague", Time: "2026-11-20T09:37:00"},
		Arrival:   models.GroundStop{City: "Vienna", Time: "2026-11-20T13:49:00"}}
	oneChange := base
	oneChange.Transfers = 1
	for _, c := range []struct {
		name  string
		known models.GroundRoute
	}{
		{"both zero", base},
		{"zero not known against a known change", oneChange},
	} {
		t.Run(c.name, func(t *testing.T) {
			notKnown := base
			notKnown.TransfersUnknown = true
			routes := []models.GroundRoute{notKnown, c.known}
			sortGroundRoutes(routes)
			if routes[0].TransfersUnknown {
				t.Errorf("first route's count is not known; want the known one first")
			}
		})
	}
}
