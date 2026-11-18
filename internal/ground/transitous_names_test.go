package ground

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestSearchTransitousByName_UsesRequestedCityNames pins D8: MOTIS answers a
// coordinate query with placeholder place names (START/END), so the by-name
// search labels routes with the cities the caller asked for.
func TestSearchTransitousByName_UsesRequestedCityNames(t *testing.T) {
	geoCityCache.Lock()
	origHel, hadHel := geoCityCache.entries["helsinki"]
	origTre, hadTre := geoCityCache.entries["tampere"]
	geoCityCache.entries["helsinki"] = geoCoord{lat: 60.17, lon: 24.94}
	geoCityCache.entries["tampere"] = geoCoord{lat: 61.50, lon: 23.77}
	geoCityCache.Unlock()
	t.Cleanup(func() {
		geoCityCache.Lock()
		defer geoCityCache.Unlock()
		if hadHel {
			geoCityCache.entries["helsinki"] = origHel
		} else {
			delete(geoCityCache.entries, "helsinki")
		}
		if hadTre {
			geoCityCache.entries["tampere"] = origTre
		} else {
			delete(geoCityCache.entries, "tampere")
		}
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(transitousResponse{
			From: transitousPlace{Name: "START"},
			To:   transitousPlace{Name: "END"},
			Itineraries: []transitousItinerary{{
				Duration: 5400, StartTime: "2026-07-01T08:00:00+03:00", EndTime: "2026-07-01T09:30:00+03:00",
				Legs: []transitousLeg{{
					Mode: "RAIL", Duration: 5400,
					From:      transitousPlace{Name: "Helsinki Railway Station"},
					To:        transitousPlace{Name: "Tampere Railway Station"},
					StartTime: "2026-07-01T08:00:00+03:00", EndTime: "2026-07-01T09:30:00+03:00",
					Route: &transitousRoute{Agency: "VR", ShortName: "IC 123"},
				}},
			}},
		})
	}))
	defer srv.Close()
	origClient, origLimiter := transitousClient, transitousLimiter
	transitousClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}
	transitousLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { transitousClient, transitousLimiter = origClient, origLimiter })

	routes, err := searchTransitousByName(context.Background(), "Helsinki", "Tampere", "2026-07-01")
	if err != nil {
		t.Fatalf("searchTransitousByName: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("want a route, got none")
	}
	for i, r := range routes {
		if r.Departure.City != "Helsinki" || r.Arrival.City != "Tampere" {
			t.Errorf("route %d cities = %q -> %q, want Helsinki -> Tampere", i, r.Departure.City, r.Arrival.City)
		}
	}
}
