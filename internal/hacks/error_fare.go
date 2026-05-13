package hacks

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// expectedPriceRange defines the typical EUR price range for a route class.
// Prices significantly below the floor suggest an error fare or flash sale.
type expectedPriceRange struct {
	floorEUR   float64 // below this = likely error fare
	typicalEUR float64 // average expected fare
	label      string
}

// routePriceRanges maps route distance brackets to expected price ranges.
// Based on European market data (EUR, economy, round-trip).
var routePriceRanges = []struct {
	maxDistKm int
	oneway    expectedPriceRange
	roundtrip expectedPriceRange
}{
	{maxDistKm: 1000, // short-haul (HEL→ARN, AMS→LHR)
		oneway:    expectedPriceRange{floorEUR: 15, typicalEUR: 60, label: "short-haul"},
		roundtrip: expectedPriceRange{floorEUR: 25, typicalEUR: 100, label: "short-haul"},
	},
	{maxDistKm: 2500, // medium-haul (HEL→BCN, AMS→ATH)
		oneway:    expectedPriceRange{floorEUR: 30, typicalEUR: 120, label: "medium-haul"},
		roundtrip: expectedPriceRange{floorEUR: 50, typicalEUR: 200, label: "medium-haul"},
	},
	{maxDistKm: 5000, // long-haul European (HEL→IST, LHR→TLV)
		oneway:    expectedPriceRange{floorEUR: 60, typicalEUR: 250, label: "long-haul"},
		roundtrip: expectedPriceRange{floorEUR: 100, typicalEUR: 400, label: "long-haul"},
	},
	{maxDistKm: 10000, // intercontinental (HEL→JFK, AMS→BKK)
		oneway:    expectedPriceRange{floorEUR: 150, typicalEUR: 500, label: "intercontinental"},
		roundtrip: expectedPriceRange{floorEUR: 250, typicalEUR: 800, label: "intercontinental"},
	},
	{maxDistKm: math.MaxInt32, // ultra-long-haul (HEL→SYD, AMS→NRT)
		oneway:    expectedPriceRange{floorEUR: 200, typicalEUR: 700, label: "ultra-long-haul"},
		roundtrip: expectedPriceRange{floorEUR: 350, typicalEUR: 1200, label: "ultra-long-haul"},
	},
}

// CheckErrorFare is an exported lightweight check for use by the optimizer.
// It returns the hack type ("error_fare" or "flash_sale") and ok=true when
// the price is anomalously low for the route distance. Returns "", false
// when the price is normal or the airports are unknown.
func CheckErrorFare(origin, dest string, price float64, isRoundTrip bool) (hackType string, ok bool) {
	if origin == "" || dest == "" || price <= 0 {
		return "", false
	}

	origin = strings.ToUpper(origin)
	dest = strings.ToUpper(dest)

	origCoord, ok1 := airportCoords[origin]
	destCoord, ok2 := airportCoords[dest]
	if !ok1 || !ok2 {
		return "", false
	}

	dist := haversineKm(origCoord[0], origCoord[1], destCoord[0], destCoord[1])

	var expected expectedPriceRange
	for _, r := range routePriceRanges {
		if int(dist) <= r.maxDistKm {
			if isRoundTrip {
				expected = r.roundtrip
			} else {
				expected = r.oneway
			}
			break
		}
	}

	if expected.floorEUR <= 0 {
		return "", false
	}

	errorThreshold := expected.floorEUR * 0.5
	flashThreshold := expected.floorEUR

	if price >= flashThreshold {
		return "", false
	}
	if price <= errorThreshold {
		return "error_fare", true
	}
	return "flash_sale", true
}

// detectErrorFare fires when the NaivePrice is significantly below the
// expected floor for the route distance. Error fares are mispriced tickets
// that airlines sometimes honour — they should be booked immediately.
// Purely advisory — zero API calls.
func detectErrorFare(_ context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.NaivePrice <= 0 {
		return nil
	}

	origin := strings.ToUpper(in.Origin)
	dest := strings.ToUpper(in.Destination)

	// Calculate great-circle distance.
	origCoord, ok1 := airportCoords[origin]
	destCoord, ok2 := airportCoords[dest]
	if !ok1 || !ok2 {
		return nil // unknown airports — can't classify route
	}

	dist := haversineKm(origCoord[0], origCoord[1], destCoord[0], destCoord[1])

	isRoundTrip := in.ReturnDate != ""

	// Find the matching price range.
	var expected expectedPriceRange
	for _, r := range routePriceRanges {
		if int(dist) <= r.maxDistKm {
			if isRoundTrip {
				expected = r.roundtrip
			} else {
				expected = r.oneway
			}
			break
		}
	}

	if expected.floorEUR <= 0 {
		return nil
	}

	price := in.NaivePrice

	// Error fare threshold: price is below 50% of the floor for this route class.
	// This is aggressive — we want to catch genuine anomalies, not just sales.
	errorThreshold := expected.floorEUR * 0.5
	// Flash sale threshold: price is below the floor but above the error threshold.
	flashThreshold := expected.floorEUR

	if price >= flashThreshold {
		return nil // price is normal
	}

	tripType := "one-way"
	if isRoundTrip {
		tripType = "round-trip"
	}

	if price <= errorThreshold {
		// Likely error fare — book immediately.
		discount := math.Round(((expected.typicalEUR - price) / expected.typicalEUR) * 100)
		return []Hack{{
			Type:  "error_fare",
			Title: fmt.Sprintf("Possible error fare: €%.0f for %s %s (%s)", price, expected.label, tripType, fmt.Sprintf("%.0f km", dist)),
			Description: fmt.Sprintf(
				"€%.0f is %.0f%% below the typical €%.0f for %s %s routes (%.0f km). "+
					"This may be an error fare — airlines sometimes honour mispriced tickets. Book immediately if interested.",
				price, discount, expected.typicalEUR, expected.label, tripType, dist),
			Savings:  math.Round(expected.typicalEUR - price),
			Currency: in.currency(),
			Steps: []string{
				"Book immediately — error fares get corrected within hours",
				"Pay with a card that has travel protection (chargeback if cancelled)",
				"Do not call the airline to ask about the price — they may cancel",
				"Book directly on the airline website for better protection",
			},
			Risks: []string{
				"Airline may cancel the ticket and refund within 24-48h",
				"Some airlines have 'obvious error' clauses in their ToS",
				"EU regulation 261/2004 may protect you if the airline cancels",
			},
		}}
	}

	// Flash sale — unusually cheap but not error-level.
	discount := math.Round(((expected.typicalEUR - price) / expected.typicalEUR) * 100)
	return []Hack{{
		Type:  "flash_sale",
		Title: fmt.Sprintf("Flash sale: €%.0f for %s %s (%.0f%% below average)", price, expected.label, tripType, discount),
		Description: fmt.Sprintf(
			"€%.0f is below the typical floor of €%.0f for %s %s routes (%.0f km). "+
				"This is likely a legitimate flash sale or promotional fare — good deal, book soon.",
			price, expected.floorEUR, expected.label, tripType, dist),
		Savings:  math.Round(expected.typicalEUR - price),
		Currency: in.currency(),
		Steps: []string{
			"Book soon — flash sale inventory is limited",
			"Check if the fare includes baggage or is basic economy",
			"Compare with nearby dates — the sale may apply to a date range",
		},
		Risks: []string{
			"Basic economy fares may exclude seat selection and carry-on",
			"Flash sales are often non-refundable and non-changeable",
		},
	}}
}
