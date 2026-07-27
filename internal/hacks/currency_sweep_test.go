package hacks

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// gr builds a minimal GroundRoute for the currency-conversion tests.
func gr(price float64, currency, depTime, arrTime string) models.GroundRoute {
	return models.GroundRoute{
		Provider:  "flixbus",
		Type:      "bus",
		Price:     price,
		Currency:  currency,
		Departure: models.GroundStop{City: "A", Time: depTime},
		Arrival:   models.GroundStop{City: "B", Time: arrTime},
	}
}

// offlineCtx returns a context guaranteed to make any outbound currency-rate
// fetch fail immediately, so no new network traffic originates from this test.
// Determinism does NOT rely on offline-ness alone: the "inconvertible" cases use
// placeholder currency codes (ZZZ/ZZY) that appear in no real exchange-rate
// table, so ConvertCurrency returns the original currency (!= target) and the
// route is skipped whether the shared rate cache is warm or cold. Same-currency
// (EUR→EUR) conversions short-circuit before any HTTP call.
func offlineCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

const (
	dayDep, dayArr     = "2026-04-13T09:00", "2026-04-13T14:00"
	nightDep, nightArr = "2026-04-13T22:00", "2026-04-14T06:00"
)

// TestSelectCheapestGroundConverted is the regression guard for the currency
// honesty of the multimodal detectors (skip_flight, open_jaw_ground,
// positioning, ferry_positioning). Every price is FX-converted into the
// requested target currency; a route that cannot be converted is skipped rather
// than mislabelled. The "inconvertible" cases use placeholder currency codes
// (ZZZ/ZZY) so they are deterministically unconvertible regardless of the shared
// rate cache's state.
func TestSelectCheapestGroundConverted(t *testing.T) {
	tests := []struct {
		name          string
		routes        []models.GroundRoute
		target        string
		overnightOnly bool
		offline       bool
		wantOK        bool
		wantPrice     float64
	}{
		{
			// A foreign route that cannot be converted is skipped; the
			// same-currency EUR route is selected and labelled honestly.
			name:      "foreign inconvertible skipped, EUR selected",
			routes:    []models.GroundRoute{gr(20, "ZZZ", dayDep, dayArr), gr(30, "EUR", dayDep, dayArr)},
			target:    "EUR",
			offline:   true,
			wantOK:    true,
			wantPrice: 30,
		},
		{
			name:      "single EUR route selected",
			routes:    []models.GroundRoute{gr(45, "EUR", dayDep, dayArr)},
			target:    "EUR",
			wantOK:    true,
			wantPrice: 45,
		},
		{
			// No convertible route => suppress rather than mislabel.
			name:    "all-foreign inconvertible suppresses",
			routes:  []models.GroundRoute{gr(20, "ZZZ", dayDep, dayArr), gr(25, "ZZY", dayDep, dayArr)},
			target:  "EUR",
			offline: true,
			wantOK:  false,
		},
		{
			name:          "overnightOnly filters out daytime route",
			routes:        []models.GroundRoute{gr(40, "EUR", dayDep, dayArr)},
			target:        "EUR",
			overnightOnly: true,
			wantOK:        false,
		},
		{
			name:          "overnightOnly keeps overnight route",
			routes:        []models.GroundRoute{gr(40, "EUR", nightDep, nightArr)},
			target:        "EUR",
			overnightOnly: true,
			wantOK:        true,
			wantPrice:     40,
		},
		{
			name:      "non-positive prices ignored, positive EUR picked",
			routes:    []models.GroundRoute{gr(0, "EUR", dayDep, dayArr), gr(-5, "EUR", dayDep, dayArr), gr(55, "EUR", dayDep, dayArr)},
			target:    "EUR",
			wantOK:    true,
			wantPrice: 55,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.offline {
				ctx = offlineCtx()
			}
			route, price, ok := selectCheapestGroundConverted(ctx, tc.routes, tc.target, tc.overnightOnly)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (route=%+v price=%.0f)", ok, tc.wantOK, route, price)
			}
			if !tc.wantOK {
				if route != nil {
					t.Fatalf("expected nil route on suppression, got %+v", route)
				}
				return
			}
			if route == nil {
				t.Fatalf("expected a route at %.0f, got nil", tc.wantPrice)
			}
			if price != tc.wantPrice {
				t.Errorf("price = %.0f, want %.0f", price, tc.wantPrice)
			}
			if route.price != tc.wantPrice {
				t.Errorf("route.price = %.0f, want %.0f", route.price, tc.wantPrice)
			}
			if route.currency != tc.target {
				t.Errorf("route.currency = %q, want %q", route.currency, tc.target)
			}
		})
	}
}

// flightResult builds a FlightSearchResult from (price) pairs, all in currency.
func flightResult(success bool, currency string, prices ...float64) *models.FlightSearchResult {
	flights := make([]models.FlightResult, 0, len(prices))
	for _, p := range prices {
		flights = append(flights, models.FlightResult{Price: p, Currency: currency})
	}
	return &models.FlightSearchResult{Success: success, Flights: flights}
}

// TestMinFlightPriceConverted pins the flight-side helper: same-currency flights
// convert trivially and the minimum positive price wins; nil/failed/empty
// results yield ok=false.
func TestMinFlightPriceConverted(t *testing.T) {
	ctx := context.Background()

	t.Run("same-currency min selected", func(t *testing.T) {
		r := flightResult(true, "EUR", 120, 90, 150)
		got, ok := minFlightPriceConverted(ctx, r, "EUR")
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if got != 90 {
			t.Errorf("got = %.0f, want 90", got)
		}
	})

	t.Run("nil result", func(t *testing.T) {
		if _, ok := minFlightPriceConverted(ctx, nil, "EUR"); ok {
			t.Errorf("ok = true, want false for nil result")
		}
	})

	t.Run("unsuccessful result", func(t *testing.T) {
		r := flightResult(false, "EUR", 100)
		if _, ok := minFlightPriceConverted(ctx, r, "EUR"); ok {
			t.Errorf("ok = true, want false for unsuccessful result")
		}
	})

	t.Run("no positive-priced flights", func(t *testing.T) {
		r := flightResult(true, "EUR", 0, -10)
		if _, ok := minFlightPriceConverted(ctx, r, "EUR"); ok {
			t.Errorf("ok = true, want false when no positive prices")
		}
	})

	t.Run("foreign inconvertible", func(t *testing.T) {
		r := flightResult(true, "ZZZ", 100)
		if _, ok := minFlightPriceConverted(offlineCtx(), r, "EUR"); ok {
			t.Errorf("ok = true, want false when foreign fare is inconvertible")
		}
	})
}
