package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// mkFlightMikko builds a test flight with the given legs. Each leg tuple is
// (depCode, arrCode, depTime, layoverMinutes).
func mkFlightMikko(price float64, legs ...[4]any) models.FlightResult {
	flt := models.FlightResult{Price: price, Currency: "EUR"}
	for _, l := range legs {
		leg := models.FlightLeg{
			DepartureAirport: models.AirportInfo{Code: l[0].(string)},
			ArrivalAirport:   models.AirportInfo{Code: l[1].(string)},
			DepartureTime:    l[2].(string),
			LayoverMinutes:   l[3].(int),
		}
		flt.Legs = append(flt.Legs, leg)
	}
	return flt
}

func TestFilterByLongLayover_NoFilter(t *testing.T) {
	flts := []models.FlightResult{
		mkFlightMikko(100, [4]any{"HEL", "AMS", "2026-06-01T18:00", 0}),
	}
	got := FilterByLongLayover(flts, 0, nil)
	if len(got) != 1 {
		t.Fatalf("no filter should pass all: got %d", len(got))
	}
}

func TestFilterByLongLayover_DurationAndAirport(t *testing.T) {
	flts := []models.FlightResult{
		// 12h at AMS -- passes
		mkFlightMikko(100,
			[4]any{"HEL", "AMS", "2026-06-01T18:00", 0},
			[4]any{"AMS", "PRG", "2026-06-02T06:00", 720}),
		// 3h at AMS -- too short
		mkFlightMikko(120,
			[4]any{"HEL", "AMS", "2026-06-01T14:00", 0},
			[4]any{"AMS", "PRG", "2026-06-01T17:00", 180}),
		// 12h at CDG -- wrong airport
		mkFlightMikko(130,
			[4]any{"HEL", "CDG", "2026-06-01T18:00", 0},
			[4]any{"CDG", "PRG", "2026-06-02T06:00", 720}),
	}
	got := FilterByLongLayover(flts, 12*60, []string{"AMS"})
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
	if got[0].Price != 100 {
		t.Errorf("wrong flight kept: price %.0f", got[0].Price)
	}
}

func TestFilterByEarlyConnection_Default10AM(t *testing.T) {
	_ = testing.Short // anchor
	flts := []models.FlightResult{
		// Overnight 10h at AMS, next leg 08:00 -- too early
		mkFlightMikko(100,
			[4]any{"HEL", "AMS", "2026-06-01T22:00", 0},
			[4]any{"AMS", "PRG", "2026-06-02T08:00", 600}),
		// Overnight 10h at AMS, next leg 11:00 -- ok
		mkFlightMikko(120,
			[4]any{"HEL", "AMS", "2026-06-01T22:00", 0},
			[4]any{"AMS", "PRG", "2026-06-02T11:00", 780}),
		// Short 2h layover, next leg 07:00 -- aamuyo rule does not apply
		mkFlightMikko(130,
			[4]any{"HEL", "AMS", "2026-06-01T05:00", 0},
			[4]any{"AMS", "PRG", "2026-06-01T07:00", 120}),
	}
	got := FilterByEarlyConnection(flts, "")
	if len(got) != 2 {
		t.Fatalf("expected 2 flights, got %d", len(got))
	}
}

func TestFilterByLoungeAccess_AllAirportsCovered(t *testing.T) {
	q := func(ap string) map[string]bool {
		return map[string]bool{"Priority Pass": true}
	}
	flts := []models.FlightResult{
		mkFlightMikko(100,
			[4]any{"HEL", "AMS", "2026-06-01T18:00", 0},
			[4]any{"AMS", "PRG", "2026-06-01T21:00", 180}),
	}
	got := FilterByLoungeAccess(flts, []string{"PP"}, q)
	if len(got) != 1 {
		t.Fatalf("all airports covered should pass: got %d", len(got))
	}
}

func TestFilterByLoungeAccess_NoCoverageAtLayover(t *testing.T) {
	q := func(ap string) map[string]bool {
		// No coverage anywhere
		return map[string]bool{}
	}
	flts := []models.FlightResult{
		mkFlightMikko(100,
			[4]any{"HEL", "AMS", "2026-06-01T18:00", 0},
			[4]any{"AMS", "PRG", "2026-06-01T21:00", 180}),
	}
	got := FilterByLoungeAccess(flts, []string{"PP"}, q)
	if len(got) != 0 {
		t.Fatalf("no coverage should drop flight: got %d", len(got))
	}
}

func TestExtractLegDepartureHHMM(t *testing.T) {
	cases := []struct{ in, want string }{
		{"2026-06-01T18:00", "18:00"},
		{"2026-06-01T18:00:00", "18:00"},
		{"2026-06-01 18:00", "18:00"},
		{"18:00", "18:00"},
		{"", ""},
	}
	for _, c := range cases {
		got := extractLegDepartureHHMM(models.FlightLeg{DepartureTime: c.in})
		if got != c.want {
			t.Errorf("extract(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
