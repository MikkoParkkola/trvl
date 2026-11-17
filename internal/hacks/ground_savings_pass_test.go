package hacks

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// pragueViennaBus is the #815 ground search: the cheapest live fare is a
// EUR 15.90 bus.
func pragueViennaBus(naive float64) ground.HackComposeRequest {
	return ground.HackComposeRequest{
		Origin:      "Prague",
		Destination: "Vienna",
		Date:        "2026-11-20",
		Currency:    "EUR",
		NaivePrice:  naive,
	}
}

// ryanairGBPHack is the saving currency arbitrage built in #815: 3.5% of a
// EUR 95 flight, rounded to EUR 3.
func ryanairGBPHack() Hack {
	return Hack{
		Type:     "currency_arbitrage",
		Title:    "Book Ryanair in GBP for ~4% discount",
		Savings:  3,
		Currency: "EUR",
	}
}

func fixedHacks(hs ...Hack) DetectFunc {
	return func(context.Context, DetectorInput) []Hack { return hs }
}

// #815: a saving measured on a flight fare is never subtracted from a ground
// fare. On main this attached price 13, savings_pct 18.9.
func TestBestSaving_GroundRejectsFlightFareSaving(t *testing.T) {
	in := detectorInputFromGround(pragueViennaBus(15.90))
	if got := BestSaving(context.Background(), in, fixedHacks(ryanairGBPHack())); got != nil {
		t.Fatalf("ground search attached a flight-fare saving: type=%s price=%v naive=%v savings=%v pct=%v",
			got.Type, got.Price, got.NaivePrice, got.Savings, got.SavingsPct)
	}
}

// A hotel night saved is not a cheaper fare: subtracting it from a EUR 90
// ground fare claims a EUR 30 ticket nobody sells.
func TestBestSaving_GroundRejectsHotelNightSaving(t *testing.T) {
	in := detectorInputFromGround(pragueViennaBus(90))
	night := Hack{Type: "night_transport", Title: "Overnight Bus saves hotel night", Savings: 60, Currency: "EUR"}
	if got := BestSaving(context.Background(), in, fixedHacks(night)); got != nil {
		t.Fatalf("ground search attached a hotel-night saving as a fare: type=%s price=%v naive=%v savings=%v",
			got.Type, got.Price, got.NaivePrice, got.Savings)
	}
}

// #814: the ground composer never runs a flight detector, so its savings pass
// makes no flight (Google) lookup. The roster seam holds one counting flight
// detector; the ground composer must not call it.
func TestGroundComposer_RunsNoFlightDetector(t *testing.T) {
	var calls atomic.Int32
	prev := currentDetectorRoster()
	setDetectorRoster(func() []detectFn {
		return []detectFn{func(context.Context, DetectorInput) []Hack {
			calls.Add(1)
			return []Hack{ryanairGBPHack()}
		}}
	})
	t.Cleanup(func() { setDetectorRoster(prev) })

	got := groundHackComposer(nil)(context.Background(), pragueViennaBus(15.90))
	if n := calls.Load(); n != 0 {
		t.Errorf("ground composer ran %d flight detector call(s), want 0", n)
	}
	if got != nil {
		t.Errorf("ground composer attached %s: price=%v savings=%v", got.Type, got.Price, got.Savings)
	}
}

// oneAirline returns a flight search stub whose only flight costs EUR 95 on
// the given airline.
func oneAirline(code, name string) flights.SearchFunc {
	return func(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
		return &models.FlightSearchResult{
			Success: true,
			Flights: []models.FlightResult{{
				Price:    95,
				Currency: "EUR",
				Legs:     []models.FlightLeg{{AirlineCode: code, Airline: name}},
			}},
		}, nil
	}
}

// #815 criterion 3: a currency entry applies only where its own note says it
// holds. want is the booking currency the hack must name, "" for no hack.
func TestCurrencyArbitrage_AppliesOnlyWhereItsNoteHolds(t *testing.T) {
	cases := []struct {
		name, origin, dest, code, airline, want string
	}{
		{"Ryanair GBP from the UK", "STN", "VIE", "FR", "Ryanair", "GBP"},
		{"Ryanair GBP from Liverpool", "LPL", "DUB", "FR", "Ryanair", "GBP"},
		{"Ryanair GBP not from Prague", "PRG", "VIE", "FR", "Ryanair", ""},
		{"Ryanair GBP not into the UK", "VIE", "STN", "FR", "Ryanair", ""},
		{"unknown origin fails closed", "XXX", "VIE", "FR", "Ryanair", ""},
		{"Wizz RON from Bucharest", "OTP", "VIE", "W6", "Wizz Air", "RON"},
		{"Wizz HUF elsewhere", "BUD", "VIE", "W6", "Wizz Air", "HUF"},
		{"Pegasus TRY from Turkey", "SAW", "VIE", "PC", "Pegasus", "TRY"},
		{"Pegasus TRY not into Turkey", "VIE", "SAW", "PC", "Pegasus", ""},
		{"Turkish TRY domestic", "IST", "AYT", "TK", "Turkish Airlines", "TRY"},
		{"Turkish TRY not international", "IST", "LHR", "TK", "Turkish Airlines", ""},
		{"Aer Lingus USD transatlantic", "DUB", "JFK", "EI", "Aer Lingus", "USD"},
		{"Aer Lingus USD westbound from Canada", "YYZ", "DUB", "EI", "Aer Lingus", "USD"},
		{"Aer Lingus USD not domestic US", "JFK", "BOS", "EI", "Aer Lingus", ""},
		{"Aer Lingus USD not within Europe", "DUB", "LHR", "EI", "Aer Lingus", ""},
		{"Aer Lingus USD unknown end fails closed", "XXX", "JFK", "EI", "Aer Lingus", ""},
		{"Norwegian NOK has no route rule", "PRG", "VIE", "DY", "Norwegian", "NOK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hs := detectCurrencyArbitrage(context.Background(), DetectorInput{
				Origin:         c.origin,
				Destination:    c.dest,
				Date:           "2026-11-20",
				Currency:       "EUR",
				SearchOverride: oneAirline(c.code, c.airline),
			})
			var got []string
			for _, h := range hs {
				got = append(got, h.Title)
			}
			if c.want == "" {
				if len(hs) != 0 {
					t.Fatalf("%s to %s on %s: want no hack, got %q", c.origin, c.dest, c.code, got)
				}
				return
			}
			if len(hs) != 1 || !strings.Contains(hs[0].Title, " in "+c.want+" ") {
				t.Fatalf("%s to %s on %s: want one hack booking in %s, got %q", c.origin, c.dest, c.code, c.want, got)
			}
		})
	}
}
