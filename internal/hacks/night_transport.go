package hacks

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// averageHotelCost is a conservative estimate of a mid-range hotel night (EUR).
// Used when no hotel price data is available.
const averageHotelCost = 60.0

// detectNightTransport searches ground transport for overnight routes, then
// adds the notional hotel saving to the total benefit.
func detectNightTransport(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	result, err := ground.SearchByName(ctx, cityFromCode(in.Origin), cityFromCode(in.Destination), in.Date, ground.SearchOptions{
		Currency: in.currency(),
	})
	if err != nil || !result.Success || len(result.Routes) == 0 {
		return nil
	}

	var hacks []Hack
	for _, r := range result.Routes {
		if !isOvernightRoute(r.Departure.Time, r.Arrival.Time) {
			continue
		}

		totalBenefit := averageHotelCost
		h := buildNightHack(in, r, totalBenefit)
		hacks = append(hacks, h)

		// Report only the best (cheapest) night option.
		break
	}

	return hacks
}

func buildNightHack(in DetectorInput, r models.GroundRoute, hotelSaving float64) Hack {
	currency := in.currency()
	if r.Currency != "" {
		currency = r.Currency
	}

	// cases.Caser is stateful and NOT safe for concurrent use; construct a
	// per-call caser so concurrent hack detection (DetectAll fans out across
	// detectors) never races on a shared instance.
	titleCaser := cases.Title(language.English)
	providerStr := titleCaser.String(r.Provider)
	typeStr := titleCaser.String(r.Type)

	depTime := r.Departure.Time
	arrTime := r.Arrival.Time
	// Trim to HH:MM if ISO 8601.
	if len(depTime) >= 16 {
		depTime = depTime[11:16]
	}
	if len(arrTime) >= 16 {
		arrTime = arrTime[11:16]
	}

	return Hack{
		Type:     "night_transport",
		Title:    fmt.Sprintf("Overnight %s saves hotel night", typeStr),
		Currency: currency,
		Savings:  roundSavings(hotelSaving),
		Description: fmt.Sprintf(
			"%s %s→%s departs %s, arrives %s — travel overnight and arrive rested without paying for a hotel.",
			providerStr,
			r.Departure.City, r.Arrival.City,
			depTime, arrTime,
		),
		Risks: []string{
			"Sleep quality on buses/trains varies; bring a travel pillow",
			"Night routes often have fewer connections if you miss the service",
			"Arrival time may be early; check luggage storage at destination",
		},
		Steps: []string{
			fmt.Sprintf("Book %s night route %s→%s departing %s on %s", typeStr, r.Departure.City, r.Arrival.City, depTime, in.Date),
			"Pack carry-on only for easy overnight travel",
			fmt.Sprintf("Arrive at %s around %s — save ~%s %.0f on hotel", r.Arrival.City, arrTime, currency, hotelSaving),
		},
		Citations: []string{r.BookingURL},
	}
}

// cityFromCode maps an IATA code to a city name for ground-transport search.
// Falls back to the code itself if no mapping is found.
func cityFromCode(code string) string {
	// Reuse the airport names from models package.
	knownCities := map[string]string{
		"HEL": "Helsinki",
		"TLL": "Tallinn",
		"RIX": "Riga",
		"VNO": "Vilnius",
		"AMS": "Amsterdam",
		"BRU": "Brussels",
		"CDG": "Paris",
		"ORY": "Paris",
		"LHR": "London",
		"STN": "London",
		"LGW": "London",
		"PRG": "Prague",
		"VIE": "Vienna",
		"BUD": "Budapest",
		"WAW": "Warsaw",
		"KRK": "Krakow",
		"BCN": "Barcelona",
		"MAD": "Madrid",
		"FCO": "Rome",
		"MXP": "Milan",
		"ZRH": "Zurich",
		"GVA": "Geneva",
		"MUC": "Munich",
		"FRA": "Frankfurt",
		"BER": "Berlin",
		"HAM": "Hamburg",
		"CPH": "Copenhagen",
		"ARN": "Stockholm",
		"OSL": "Oslo",
		"GOT": "Gothenburg",
		"DUB": "Dublin",
		"LIS": "Lisbon",
		"ATH": "Athens",
		"IST": "Istanbul",
		"DBV": "Dubrovnik",
		"SPU": "Split",
	}
	if city, ok := knownCities[code]; ok {
		return city
	}
	return code
}
