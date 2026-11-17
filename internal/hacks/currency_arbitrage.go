package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// currencyArbitrageNote describes a known currency booking advantage for an
// airline. These are based on reported real-world observations where booking
// directly on the airline's website in the home currency yields a lower price
// due to FX markup, rounding, or regional pricing.
type currencyArbitrageNote struct {
	AirlineCode  string
	AirlineName  string
	HomeCurrency string
	MarkupPct    float64 // typical markup when NOT booking in home currency
	Notes        string
	// Applies is the route condition Notes states, on ISO country codes
	// (models.AirportCountry; "" when the airport is unknown). nil means the
	// note states none. A conditioned entry never matches an unknown airport.
	Applies func(originCC, destCC string) bool
}

// fromCountry holds on routes departing the given country.
func fromCountry(cc string) func(string, string) bool {
	return func(o, _ string) bool { return o == cc }
}

// europeCountries are the European countries in models.AirportCountry.
var europeCountries = map[string]bool{
	"AT": true, "BE": true, "BG": true, "CH": true, "CZ": true, "DE": true,
	"DK": true, "EE": true, "ES": true, "FI": true, "FR": true, "GB": true,
	"GR": true, "HR": true, "HU": true, "IE": true, "IS": true, "IT": true,
	"LT": true, "LV": true, "ME": true, "NL": true, "NO": true, "PL": true,
	"PT": true, "RO": true, "RS": true, "SE": true, "TR": true,
}

// transatlantic holds between Europe and the US or Canada, in either direction.
func transatlantic(o, d string) bool {
	northAmerica := func(cc string) bool { return cc == "US" || cc == "CA" }
	return (europeCountries[o] && northAmerica(d)) || (northAmerica(o) && europeCountries[d])
}

// knownArbitrageAirlines lists airlines with documented currency pricing gaps.
var knownArbitrageAirlines = []currencyArbitrageNote{
	// RON precedes HUF: both are Wizz Air, the first matching entry wins, and
	// only the RON entry carries a route condition.
	{"W6", "Wizz Air", "RON", 4.0, "Romanian leu pricing for routes from Bucharest can be cheaper", fromCountry("RO")},
	{"W6", "Wizz Air", "HUF", 5.0, "Hungarian forint pricing avoids EUR FX markup on direct bookings", nil},
	{"FR", "Ryanair", "GBP", 3.5, "GBP pricing on UK-origin routes sometimes bypasses EUR conversion fee", fromCountry("GB")},
	{"DY", "Norwegian", "NOK", 5.0, "Norwegian krone pricing is the native currency; EUR rates add FX margin", nil},
	{"SK", "SAS", "SEK", 4.5, "SAS home currency SEK avoids Scandinavian FX markup on EUR bookings", nil},
	{"PC", "Pegasus", "TRY", 6.0, "Turkish lira pricing on domestic/short-haul Turkish routes is notably cheaper", fromCountry("TR")},
	{"TK", "Turkish Airlines", "TRY", 5.5, "Book in TRY on turkishairlines.com for domestic legs; significant saving", func(o, d string) bool { return o == "TR" && d == "TR" }},
	{"VY", "Vueling", "EUR", 0, "EUR is already native — no arbitrage", nil},
	{"EI", "Aer Lingus", "USD", 3.0, "USD pricing on transatlantic fares can be 2-4% cheaper due to revenue management", transatlantic},
}

// detectCurrencyArbitrage identifies airlines on the found flights that have
// known currency booking advantages, then flags the saving opportunity.
// Since the flight search API returns prices in a single currency, we use
// static knowledge about typical FX markup percentages rather than live
// multi-currency quotes.
func detectCurrencyArbitrage(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	baseResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{
		ReturnDate:     in.ReturnDate,
		SearchOverride: in.SearchOverride,
	})
	if err != nil || !baseResult.Success || len(baseResult.Flights) == 0 {
		return nil
	}
	basePrice := minFlightPrice(baseResult)
	if basePrice <= 0 {
		return nil
	}
	currency := flightCurrency(baseResult, in.currency())

	// Collect airline codes present in the results.
	airlinesFound := map[string]string{} // code -> name
	for _, f := range baseResult.Flights {
		for _, leg := range f.Legs {
			if leg.AirlineCode != "" {
				airlinesFound[leg.AirlineCode] = leg.Airline
			}
		}
	}

	seen := map[string]bool{}
	var hacks []Hack

	for _, note := range knownArbitrageAirlines {
		if note.MarkupPct <= 0 {
			continue
		}
		if _, found := airlinesFound[note.AirlineCode]; !found {
			continue
		}
		if seen[note.AirlineCode] {
			continue
		}
		// Skip if the user is already booking in the airline's home currency.
		if note.HomeCurrency == currency {
			continue
		}
		if note.Applies != nil && !note.Applies(models.AirportCountry(strings.ToUpper(in.Origin)), models.AirportCountry(strings.ToUpper(in.Destination))) {
			continue // outside the route the note describes (#815)
		}
		seen[note.AirlineCode] = true

		estimatedSaving := basePrice * note.MarkupPct / 100

		hacks = append(hacks, Hack{
			Type:     "currency_arbitrage",
			Title:    fmt.Sprintf("Book %s in %s for ~%.0f%% discount", note.AirlineName, note.HomeCurrency, note.MarkupPct),
			Currency: currency,
			Savings:  roundSavings(estimatedSaving),
			Description: fmt.Sprintf(
				"%s is on your route. Booking directly on %s's website in %s (home currency) "+
					"avoids FX markup — typically saves %.0f%% (~%s %.0f on a %.0f fare). %s",
				note.AirlineName, note.AirlineName, note.HomeCurrency,
				note.MarkupPct, currency, estimatedSaving, basePrice,
				note.Notes,
			),
			Risks: []string{
				"This is an estimate based on reported typical savings — verify on the airline's website",
				"Credit card FX fees may offset some or all of the saving",
				"Use a no-FX-fee card (Revolut, Wise, N26) to maximise the benefit",
				"Price difference may vary depending on route and booking date",
			},
			Steps: []string{
				fmt.Sprintf("Visit %s's website directly (not an aggregator)", note.AirlineName),
				fmt.Sprintf("Switch the booking currency to %s", note.HomeCurrency),
				fmt.Sprintf("Compare against %s price — expect ~%.0f%% saving", currency, note.MarkupPct),
				"Pay with a card that has no foreign transaction fees",
			},
			Citations: []string{
				googleFlightsURL(in.Destination, in.Origin, in.Date),
			},
		})
	}

	return hacks
}

// toEUR converts a price in the given currency to an approximate EUR equivalent
// using fixed exchange rates. Intentionally conservative — only used for
// comparison, not for financial accuracy.
func toEUR(price float64, currency string) float64 {
	rates := map[string]float64{
		"EUR": 1.00,
		"USD": 0.93,
		"GBP": 1.17,
		"SEK": 0.087,
		"NOK": 0.086,
		"DKK": 0.134,
		"CHF": 1.04,
		"PLN": 0.23,
		"HUF": 0.0026,
		"RON": 0.20,
		"TRY": 0.028,
		"CZK": 0.040,
		"HRK": 0.133,
	}
	rate, ok := rates[currency]
	if !ok {
		return price
	}
	return price * rate
}
