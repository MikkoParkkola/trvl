package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// openJawAlternates lists nearby airports that are reasonable alternate return
// points for an open-jaw itinerary. Keyed by the destination airport: if
// you fly OUT→DEST, you could return from one of these instead.
var openJawAlternates = map[string][]string{
	"PRG": {"BRU", "AMS", "FRA", "VIE", "MUC"},
	"AMS": {"BRU", "FRA", "CDG", "DUS"},
	"CDG": {"BRU", "AMS", "FRA"},
	"BCN": {"MAD", "GRO", "REU"},
	"MAD": {"BCN", "LIS", "VLC"},
	"LIS": {"OPO", "MAD"},
	"FCO": {"MXP", "CIA", "NAP"},
	"MXP": {"FCO", "BGY", "TRN"},
	"MUC": {"NUE", "FMM", "FRA"},
	"FRA": {"MUC", "DUS", "CGN"},
	"BER": {"HAM", "FRA"},
	"VIE": {"PRG", "BUD", "BRN"},
	"BUD": {"VIE", "PRG", "KRK"},
	"WAW": {"KRK", "WRO", "GDN"},
	"CPH": {"GOT", "ARN", "MMX"},
	"ARN": {"CPH", "GOT"},
	"OSL": {"BGO", "SVG"},
	"ATH": {"SKG", "HER"},
	"IST": {"SAW", "ESB"},
	"DUB": {"ORK", "SNN"},
}

// detectOpenJaw detects open-jaw opportunities: fly into city A, return from
// city B. When one end is the user's home airport this is particularly powerful
// because the return ticket from home is not needed.
func detectOpenJaw(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" || in.ReturnDate == "" {
		return nil
	}

	prefs, _ := preferences.Load()

	// Check if origin is a home airport — the strongest open-jaw signal.
	isHome := isHomeAirport(in.Origin, prefs)

	// All user-visible prices (round-trip baseline, one-way legs, and the
	// EUR-denominated ground-cost estimate) are converted into the requested
	// currency; anything that cannot be converted is suppressed rather than
	// shown unconverted or mislabelled.
	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}

	// Baseline: round-trip from origin to destination.
	rtResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{
		ReturnDate:     in.ReturnDate,
		SearchOverride: in.SearchOverride,
	})
	if err != nil || !rtResult.Success || len(rtResult.Flights) == 0 {
		return nil
	}
	rtPrice, ok := cheapestFlightPriceIn(ctx, rtResult, target)
	if !ok {
		return nil
	}
	currency := target

	// One-way outbound (origin → destination).
	owOutResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{SearchOverride: in.SearchOverride})
	if err != nil || !owOutResult.Success || len(owOutResult.Flights) == 0 {
		return nil
	}
	owOutPrice, ok := cheapestFlightPriceIn(ctx, owOutResult, target)
	if !ok {
		return nil
	}

	// Try each alternate return city.
	alts, ok := openJawAlternates[in.Destination]
	if !ok {
		return nil
	}

	type ch struct {
		alt   string
		price float64
		ok    bool
	}
	results := make(chan ch, len(alts))

	for _, alt := range alts {
		if alt == in.Origin {
			continue
		}
		alt := alt
		go func() {
			r, err := flights.SearchFlights(ctx, alt, in.Origin, in.ReturnDate, flights.SearchOptions{SearchOverride: in.SearchOverride})
			if err != nil || !r.Success || len(r.Flights) == 0 {
				results <- ch{alt: alt}
				return
			}
			price, ok := cheapestFlightPriceIn(ctx, r, target)
			results <- ch{alt: alt, price: price, ok: ok}
		}()
	}

	var hacks []Hack
	for range alts {
		res := <-results
		if !res.ok || res.price <= 0 {
			continue
		}

		// Estimated ground cost (EUR) to reach alternate return city, converted
		// into the requested currency; suppress this candidate rather than mix
		// currencies when it cannot be converted.
		groundCostEUR := groundCostBetween(in.Destination, res.alt)
		groundCost, gcur := destinations.ConvertCurrency(ctx, groundCostEUR, "EUR", target)
		if gcur != target {
			continue
		}
		totalOpenJaw := owOutPrice + res.price + groundCost
		savings := rtPrice - totalOpenJaw

		// Require at least EUR 20 saving, or strong home-airport signal.
		minSaving := 20.0
		if isHome {
			minSaving = 10.0
		}
		if savings < minSaving {
			continue
		}

		h := Hack{
			Type:     "open_jaw",
			Title:    fmt.Sprintf("Open-jaw: fly into %s, return from %s", in.Destination, res.alt),
			Currency: currency,
			Savings:  roundSavings(savings),
			Description: fmt.Sprintf(
				"Fly %s→%s one-way (%.0f) + travel to %s + fly %s→%s (%.0f) = %.0f total, vs round-trip %.0f. Saves %s %.0f and lets you visit two areas.",
				in.Origin, in.Destination, owOutPrice,
				res.alt, res.alt, in.Origin, res.price,
				totalOpenJaw, rtPrice, currency, savings,
			),
			Risks: []string{
				"You must make your own way from " + in.Destination + " to " + res.alt + " (train/bus)",
				"One-way tickets may have different fare conditions than round-trips",
				"Prices are for separate bookings — lock in both at the same time",
			},
			Steps: []string{
				fmt.Sprintf("Book one-way %s→%s on %s (%s %.0f)", in.Origin, in.Destination, in.Date, currency, owOutPrice),
				fmt.Sprintf("Travel from %s to %s by ground transport", in.Destination, res.alt),
				fmt.Sprintf("Book one-way %s→%s on %s (%s %.0f)", res.alt, in.Origin, in.ReturnDate, currency, res.price),
			},
			Citations: []string{
				googleFlightsURL(in.Destination, in.Origin, in.Date),
				googleFlightsURL(in.Origin, res.alt, in.ReturnDate),
			},
		}
		hacks = append(hacks, h)
	}

	return hacks
}

// isHomeAirport returns true when the given airport code is in the user's home airports.
func isHomeAirport(code string, prefs *preferences.Preferences) bool {
	if prefs == nil {
		return false
	}
	for _, ha := range prefs.HomeAirports {
		if ha == code {
			return true
		}
	}
	return false
}

// groundCostBetween returns a conservative estimate (EUR) of ground transport
// cost between two nearby airports/cities.
func groundCostBetween(from, to string) float64 {
	// Known short ground connections.
	pairs := map[[2]string]float64{
		{"PRG", "BRN"}: 10,
		{"PRG", "VIE"}: 15,
		{"VIE", "BUD"}: 15,
		{"AMS", "BRU"}: 20,
		{"AMS", "FRA"}: 25,
		{"AMS", "DUS"}: 20,
		{"BCN", "MAD"}: 30,
		{"FCO", "MXP"}: 30,
		{"CPH", "ARN"}: 30,
		{"CPH", "GOT"}: 20,
	}
	if v, ok := pairs[[2]string{from, to}]; ok {
		return v
	}
	if v, ok := pairs[[2]string{to, from}]; ok {
		return v
	}
	// Default: assume EUR 25 for any nearby pair.
	return 25
}
