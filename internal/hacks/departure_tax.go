package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
)

// departureTaxEUR maps country ISO codes to approximate aviation departure tax
// in EUR. Only European countries with meaningful tax differences are included.
// Sources: national aviation tax legislation as of 2025-2026.
var departureTaxEUR = map[string]float64{
	// ZERO tax countries
	"IE": 0, "PT": 0, "CY": 0, "MT": 0, "FI": 0,
	"EE": 0, "LV": 0, "LT": 0, "CZ": 0, "PL": 0,
	"HU": 0, "RO": 0, "BG": 0, "HR": 0, "SE": 0, // Sweden abolished 2025
	// HIGH tax countries
	"GB": 14, // APD short-haul economy avg
	"DE": 15, // Germany short-haul (reduced from 2026)
	"FR": 7,  // Solidarity tax short-haul economy
	"NL": 26, // Per departure
	"AT": 12, // Austrian aviation tax minimum
	"NO": 8,  // Norwegian CO2 tax minimum
	"IT": 6,  // Municipal tax avg
	"BE": 3,  // New Charleroi tax (2025)
	"DK": 5,  // New from Jan 2025
}

// countryNames maps ISO 3166-1 alpha-2 codes to display names for departure
// tax messaging.
var countryNames = map[string]string{
	"GB": "United Kingdom", "DE": "Germany", "FR": "France", "NL": "Netherlands",
	"AT": "Austria", "NO": "Norway", "IT": "Italy", "BE": "Belgium", "DK": "Denmark",
	"IE": "Ireland", "PT": "Portugal", "CY": "Cyprus", "MT": "Malta", "FI": "Finland",
	"EE": "Estonia", "LV": "Latvia", "LT": "Lithuania", "CZ": "Czech Republic",
	"PL": "Poland", "HU": "Hungary", "RO": "Romania", "BG": "Bulgaria",
	"HR": "Croatia", "SE": "Sweden", "ES": "Spain", "CH": "Switzerland",
	"GR": "Greece", "IS": "Iceland", "TR": "Turkey", "RS": "Serbia",
}

// countryName returns a human-readable country name for an ISO alpha-2 code.
func countryName(cc string) string {
	if name, ok := countryNames[cc]; ok {
		return name
	}
	return cc
}

// detectDepartureTax fires when the origin airport is in a country with
// significant aviation departure tax and a nearby alternative airport sits
// in a zero-tax country. Purely advisory — zero API calls.
func detectDepartureTax(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() {
		return nil
	}

	originCountry := iataToCountry[strings.ToUpper(in.Origin)]
	if originCountry == "" {
		return nil
	}

	originTax, hasOrigin := departureTaxEUR[originCountry]
	if !hasOrigin || originTax == 0 {
		return nil // already in zero-tax country or unknown
	}

	// Check if any nearby airports are in zero-tax countries.
	alternatives := NearbyAirports(strings.ToUpper(in.Origin))

	type zeroTaxAlt struct {
		iata       string
		city       string
		country    string
		groundCost float64
	}

	var zeroTaxAlts []zeroTaxAlt
	for _, alt := range alternatives {
		altCountry := iataToCountry[alt.IATA]
		if altCountry == "" {
			continue
		}
		altTax, has := departureTaxEUR[altCountry]
		if has && altTax == 0 {
			zeroTaxAlts = append(zeroTaxAlts, zeroTaxAlt{
				iata:       alt.IATA,
				city:       alt.City,
				country:    altCountry,
				groundCost: alt.Cost,
			})
		}
	}

	if len(zeroTaxAlts) == 0 {
		return nil
	}

	// Pick the alternative with lowest ground transport cost.
	best := zeroTaxAlts[0]
	for _, alt := range zeroTaxAlts[1:] {
		if alt.groundCost < best.groundCost {
			best = alt
		}
	}

	savings := originTax // per person per departure

	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}
	convTax, taxCur := destinations.ConvertCurrency(ctx, originTax, "EUR", target)
	if taxCur != target {
		return nil
	}
	convGround, groundCur := destinations.ConvertCurrency(ctx, best.groundCost, "EUR", target)
	if groundCur != target {
		return nil
	}
	savings = convTax

	return []Hack{{
		Type: "departure_tax",
		Title: fmt.Sprintf("Save ~%s %.0f tax — fly from %s (%s) instead",
			target, savings, best.city, best.iata),
		Description: fmt.Sprintf(
			"%s charges %s %.0f aviation tax per departure. %s (%s, %s) has zero aviation tax. "+
				"Ground transport to %s costs ~%s %.0f.",
			countryName(originCountry), target, convTax,
			best.city, best.iata, countryName(best.country),
			best.city, target, convGround),
		Savings:  savings,
		Currency: target,
		Steps: []string{
			fmt.Sprintf("Compare flight prices from %s vs %s to %s",
				strings.ToUpper(in.Origin), best.iata, strings.ToUpper(in.Destination)),
			fmt.Sprintf("Add ~%s %.0f ground transport cost from %s to %s",
				target, convGround, strings.ToUpper(in.Origin), best.iata),
			fmt.Sprintf("Savings: %s %.0f per person in avoided departure tax (minus transport cost)",
				target, savings),
		},
		Risks: []string{
			"Ground transport adds travel time and complexity",
			"Flight prices from the alternative airport may offset tax savings",
			"Tax amounts are approximate and may change",
		},
	}}
}
