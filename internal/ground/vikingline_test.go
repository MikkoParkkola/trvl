package ground

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/cookies"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func init() {
	// Disable browser page reading in tests to avoid opening Chrome.
	cookies.SkipBrowserRead = true
}

func TestLookupVikingLinePort(t *testing.T) {
	tests := []struct {
		input    string
		wantCode string
		wantCity string
		wantOK   bool
	}{
		{"Helsinki", "HEL", "Helsinki", true},
		{"helsinki", "HEL", "Helsinki", true},
		{"HEL", "HEL", "Helsinki", true},
		{"Tallinn", "TLL", "Tallinn", true},
		{"tallinn", "TLL", "Tallinn", true},
		{"TLL", "TLL", "Tallinn", true},
		{"tln", "TLL", "Tallinn", true},
		{"Stockholm", "STO", "Stockholm", true},
		{"stockholm", "STO", "Stockholm", true},
		{"STO", "STO", "Stockholm", true},
		{"Turku", "TUR", "Turku", true},
		{"turku", "TUR", "Turku", true},
		{"TUR", "TUR", "Turku", true},
		{"åbo", "TUR", "Turku", true},
		{"abo", "TUR", "Turku", true},
		{"Mariehamn", "MAR", "Mariehamn", true},
		{"mariehamn", "MAR", "Mariehamn", true},
		{"MAR", "MAR", "Mariehamn", true},
		{"oslo", "", "", false},
		{"riga", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			port, ok := LookupVikingLinePort(tt.input)
			if ok != tt.wantOK {
				t.Errorf("LookupVikingLinePort(%q) ok=%v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok {
				if port.Code != tt.wantCode {
					t.Errorf("LookupVikingLinePort(%q).Code=%q, want %q", tt.input, port.Code, tt.wantCode)
				}
				if port.City != tt.wantCity {
					t.Errorf("LookupVikingLinePort(%q).City=%q, want %q", tt.input, port.City, tt.wantCity)
				}
			}
		})
	}
}

func TestHasVikingLinePort(t *testing.T) {
	tests := []struct {
		city string
		want bool
	}{
		{"helsinki", true},
		{"tallinn", true},
		{"stockholm", true},
		{"turku", true},
		{"mariehamn", true},
		{"oslo", false},
		{"london", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			if got := HasVikingLinePort(tt.city); got != tt.want {
				t.Errorf("HasVikingLinePort(%q)=%v, want %v", tt.city, got, tt.want)
			}
		})
	}
}

func TestHasVikingLineRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Helsinki", "Tallinn", true},
		{"Tallinn", "Helsinki", true},
		{"Helsinki", "Stockholm", true},
		{"Stockholm", "Helsinki", true},
		{"Turku", "Stockholm", true},
		{"Stockholm", "Turku", true},
		{"Stockholm", "Mariehamn", true},
		// No route in reverse for STO-MAR
		{"Mariehamn", "Stockholm", false},
		// No Viking Line route between these
		{"Helsinki", "Turku", false},
		{"Oslo", "Copenhagen", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"->"+tt.to, func(t *testing.T) {
			if got := HasVikingLineRoute(tt.from, tt.to); got != tt.want {
				t.Errorf("HasVikingLineRoute(%q, %q)=%v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestSearchVikingLine_HelsinkiTallinn(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Helsinki", "Tallinn", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	// Verify all routes have correct provider, type and price.
	for i, r := range routes {
		if r.Provider != "vikingline" {
			t.Errorf("routes[%d].Provider=%q, want %q", i, r.Provider, "vikingline")
		}
		if r.Type != "ferry" {
			t.Errorf("routes[%d].Type=%q, want %q", i, r.Type, "ferry")
		}
		if r.Price != 22 {
			t.Errorf("routes[%d].Price=%.2f, want 22", i, r.Price)
		}
		if r.Currency != "EUR" {
			t.Errorf("routes[%d].Currency=%q, want EUR", i, r.Currency)
		}
		if r.Duration != 120 {
			t.Errorf("routes[%d].Duration=%d, want 120", i, r.Duration)
		}
		if r.Departure.City != "Helsinki" {
			t.Errorf("routes[%d].Departure.City=%q, want Helsinki", i, r.Departure.City)
		}
		if r.Arrival.City != "Tallinn" {
			t.Errorf("routes[%d].Arrival.City=%q, want Tallinn", i, r.Arrival.City)
		}
		if r.Transfers != 0 {
			t.Errorf("routes[%d].Transfers=%d, want 0", i, r.Transfers)
		}
		if r.BookingURL == "" {
			t.Errorf("routes[%d].BookingURL is empty", i)
		}
		if !strings.Contains(r.BookingURL, "vikingline.fi") {
			t.Errorf("routes[%d].BookingURL=%q does not contain vikingline.fi", i, r.BookingURL)
		}
	}

	// Verify departure times are on the requested date.
	wantDeps := []string{"2026-04-10T09:30:00", "2026-04-10T12:30:00", "2026-04-10T18:30:00"}
	for i, want := range wantDeps {
		if routes[i].Departure.Time != want {
			t.Errorf("routes[%d].Departure.Time=%q, want %q", i, routes[i].Departure.Time, want)
		}
	}

	// Verify arrival times are on the same day (no overnight).
	wantArrs := []string{"2026-04-10T11:30:00", "2026-04-10T14:30:00", "2026-04-10T20:30:00"}
	for i, want := range wantArrs {
		if routes[i].Arrival.Time != want {
			t.Errorf("routes[%d].Arrival.Time=%q, want %q", i, routes[i].Arrival.Time, want)
		}
	}
}

func TestSearchVikingLine_TallinnHelsinki_Overnight(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Tallinn", "Helsinki", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(routes))
	}

	// The 22:45 departure arrives 00:45 next day (+1).
	last := routes[2]
	if last.Departure.Time != "2026-04-10T22:45:00" {
		t.Errorf("last departure=%q, want 2026-04-10T22:45:00", last.Departure.Time)
	}
	if last.Arrival.Time != "2026-04-11T00:45:00" {
		t.Errorf("last arrival=%q, want 2026-04-11T00:45:00", last.Arrival.Time)
	}
}

func TestSearchVikingLine_HelsinkiStockholm_Overnight(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Helsinki", "Stockholm", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Price != 39 {
		t.Errorf("Price=%.2f, want 39", r.Price)
	}
	if r.Departure.Time != "2026-04-10T17:00:00" {
		t.Errorf("Departure.Time=%q, want 2026-04-10T17:00:00", r.Departure.Time)
	}
	// Arrival is next day (+1).
	if r.Arrival.Time != "2026-04-11T10:30:00" {
		t.Errorf("Arrival.Time=%q, want 2026-04-11T10:30:00", r.Arrival.Time)
	}
	if r.Duration != 1050 {
		t.Errorf("Duration=%d, want 1050", r.Duration)
	}
}

func TestSearchVikingLine_TurkuStockholm(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Turku", "Stockholm", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	// Day departure: same day arrival.
	day := routes[0]
	if day.Departure.Time != "2026-04-10T08:45:00" {
		t.Errorf("day dep=%q, want 2026-04-10T08:45:00", day.Departure.Time)
	}
	if day.Arrival.Time != "2026-04-10T18:55:00" {
		t.Errorf("day arr=%q, want 2026-04-10T18:55:00", day.Arrival.Time)
	}

	// Night departure: next-day arrival.
	night := routes[1]
	if night.Departure.Time != "2026-04-10T20:55:00" {
		t.Errorf("night dep=%q, want 2026-04-10T20:55:00", night.Departure.Time)
	}
	if night.Arrival.Time != "2026-04-11T06:10:00" {
		t.Errorf("night arr=%q, want 2026-04-11T06:10:00", night.Arrival.Time)
	}

	// Both have the same price.
	for i, r := range routes {
		if r.Price != 29 {
			t.Errorf("routes[%d].Price=%.2f, want 29", i, r.Price)
		}
	}
}

func TestSearchVikingLine_StockholmMariehamn(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Stockholm", "Mariehamn", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Price != 19 {
		t.Errorf("Price=%.2f, want 19", r.Price)
	}
	if r.Departure.Time != "2026-04-10T18:00:00" {
		t.Errorf("Departure.Time=%q, want 2026-04-10T18:00:00", r.Departure.Time)
	}
	if r.Arrival.Time != "2026-04-10T23:30:00" {
		t.Errorf("Arrival.Time=%q, want 2026-04-10T23:30:00", r.Arrival.Time)
	}
	if r.Duration != 330 {
		t.Errorf("Duration=%d, want 330", r.Duration)
	}
}

func TestSearchVikingLine_NoRoute(t *testing.T) {
	ctx := context.Background()
	// No Viking Line route from Helsinki to Turku.
	// D3: an unscheduled pair is "not served", not an empty answer.
	routes, err := SearchVikingLine(ctx, "Helsinki", "Turku", "2026-04-10", "EUR")
	if !errors.Is(err, models.ErrNotApplicable) {
		t.Fatalf("err = %v, want it to wrap models.ErrNotApplicable", err)
	}
	if routes != nil {
		t.Errorf("expected nil routes for unknown route, got %d", len(routes))
	}
}

func TestSearchVikingLine_UnknownPort(t *testing.T) {
	ctx := context.Background()
	_, err := SearchVikingLine(ctx, "Oslo", "Tallinn", "2026-04-10", "EUR")
	if err == nil {
		t.Error("expected error for unknown port, got nil")
	}
}

func TestSearchVikingLine_InvalidDate(t *testing.T) {
	ctx := context.Background()
	_, err := SearchVikingLine(ctx, "Helsinki", "Tallinn", "not-a-date", "EUR")
	if err == nil {
		t.Error("expected error for invalid date, got nil")
	}
}

func TestSearchVikingLine_BookingURL(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Helsinki", "Tallinn", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no routes returned")
	}

	url := routes[0].BookingURL
	wantContains := []string{"vikingline.fi", "dep=HEL", "arr=TLL", "2026-04-10"}
	for _, want := range wantContains {
		if !strings.Contains(url, want) {
			t.Errorf("BookingURL=%q does not contain %q", url, want)
		}
	}
}

func TestSearchVikingLine_ShipNameInStation(t *testing.T) {
	ctx := context.Background()
	routes, err := SearchVikingLine(ctx, "Helsinki", "Tallinn", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no routes returned")
	}

	// Ship name should appear in the departure station.
	if !strings.Contains(routes[0].Departure.Station, "Viking XPRS") {
		t.Errorf("Departure.Station=%q does not contain ship name", routes[0].Departure.Station)
	}
}

func TestSearchVikingLine_AliasLookup(t *testing.T) {
	ctx := context.Background()
	// Test that "åbo" alias resolves to Turku correctly.
	routes, err := SearchVikingLine(ctx, "åbo", "Stockholm", "2026-04-10", "EUR")
	if err != nil {
		t.Fatalf("SearchVikingLine error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes via åbo alias, got %d", len(routes))
	}
	if routes[0].Departure.City != "Turku" {
		t.Errorf("Departure.City=%q, want Turku", routes[0].Departure.City)
	}
}
