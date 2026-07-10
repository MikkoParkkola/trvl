package points

import (
	"math"
	"strings"
)

// EarningEstimate holds the estimated miles earned for a flight.
type EarningEstimate struct {
	// Miles is the estimated miles/points earned.
	Miles int `json:"miles"`
	// Program is the earning programme name (e.g. "Flying Blue").
	Program string `json:"program"`
	// Method describes how the estimate was calculated: "revenue" or "distance".
	Method string `json:"method"`
	// Note is an optional human-readable caveat.
	Note string `json:"note,omitempty"`
}

// airportCoords maps IATA codes to [lat, lon] for great-circle distance
// estimation. Covers major airports; unknown airports yield 0 distance.
var airportCoords = map[string][2]float64{
	// Europe
	"HEL": {60.3172, 24.9633},
	"AMS": {52.3086, 4.7639},
	"CDG": {49.0097, 2.5479},
	"LHR": {51.4700, -0.4543},
	"FRA": {50.0333, 8.5706},
	"MAD": {40.4719, -3.5626},
	"BCN": {41.2971, 2.0785},
	"FCO": {41.8003, 12.2389},
	"MUC": {48.3538, 11.7861},
	"ZRH": {47.4647, 8.5492},
	"CPH": {55.6180, 12.6508},
	"OSL": {60.1939, 11.1004},
	"VIE": {48.1103, 16.5697},
	"ARN": {59.6519, 17.9186},
	"LIS": {38.7756, -9.1354},
	"BRU": {50.9014, 4.4844},
	"ATH": {37.9364, 23.9445},
	"BUD": {47.4369, 19.2556},
	"PRG": {50.1008, 14.2600},
	"WAW": {52.1657, 20.9671},
	"IST": {41.2753, 28.7519},
	"DUB": {53.4264, -6.2499},
	"BER": {52.3667, 13.5033},
	"GVA": {46.2381, 6.1089},
	"TLL": {59.4133, 24.8328},
	"RIX": {56.9236, 23.9711},
	"KEF": {63.9850, -22.6056},
	"EDI": {55.9508, -3.3615},
	"MAN": {53.3537, -2.2750},
	// Middle East
	"AMM": {31.7225, 35.9932},
	"DOH": {25.2731, 51.6081},
	"DXB": {25.2532, 55.3657},
	"AUH": {24.4431, 54.6511},
	// Asia
	"NRT": {35.7720, 140.3929},
	"HND": {35.5494, 139.7798},
	"SIN": {1.3644, 103.9915},
	"BKK": {13.6900, 100.7501},
	"DEL": {28.5562, 77.1000},
	"HKG": {22.3080, 113.9185},
	"ICN": {37.4602, 126.4407},
	"KUL": {2.7456, 101.7099},
	"PEK": {40.0799, 116.6031},
	// Americas
	"JFK": {40.6413, -73.7781},
	"LAX": {33.9425, -118.4081},
	"ORD": {41.9742, -87.9073},
	"MIA": {25.7959, -80.2870},
	"EWR": {40.6895, -74.1745},
	"SFO": {37.6213, -122.3790},
	"YYZ": {43.6777, -79.6248},
	"GRU": {-23.4356, -46.4731},
	"MEX": {19.4361, -99.0719},
	"BOG": {4.7016, -74.1469},
	// Africa
	"CMN": {33.3675, -7.5898},
	"JNB": {-26.1392, 28.2460},
	"ADD": {8.9779, 38.7993},
	"NBO": {-1.3192, 36.9278},
	// Oceania
	"SYD": {-33.9461, 151.1772},
	"MEL": {-37.6690, 144.8410},
	"AKL": {-37.0082, 174.7850},
}

// skyteamAirlines lists IATA codes belonging to SkyTeam.
var skyteamAirlines = map[string]bool{
	"AF": true, "KL": true, "DL": true, "KE": true, "AZ": true,
	"MU": true, "AM": true, "ME": true, "RO": true, "OK": true,
	"SV": true, "VN": true, "GA": true, "CI": true, "CZ": true,
	"KQ": true, "UX": true, "AR": true, "SK": true,
}

// oneworldAirlines lists IATA codes belonging to Oneworld.
var oneworldAirlines = map[string]bool{
	"BA": true, "IB": true, "AY": true, "QF": true, "QR": true,
	"AA": true, "CX": true, "MH": true, "JL": true, "RJ": true,
	"S7": true, "AT": true, "UL": true, "FJ": true,
}

// EstimateMilesEarned returns the estimated miles/points earned for a flight
// based on distance, cabin class, and the earning airline's programme.
//
// For Flying Blue (SkyTeam revenue-based): estimates ~4-8 miles per EUR on
// KL/AF, ~2-4 on partner airlines. Since we often only have the ticket price,
// not the fare-per-segment, we use the total price as an approximation.
//
// For Oneworld programmes (distance-based): economy = 25-100% of distance,
// business = 125-200%.
//
// The cabinClass parameter accepts "economy", "premium_economy", "business",
// or "first" (case-insensitive).
//
// ticketPriceEUR is optional (pass 0 if unknown) and is only used for
// revenue-based programmes like Flying Blue.
func EstimateMilesEarned(origin, dest, cabinClass, airlineCode, ffAlliance string, ticketPriceEUR float64) EarningEstimate {
	alliance := strings.ToLower(strings.TrimSpace(ffAlliance))
	airline := strings.ToUpper(strings.TrimSpace(airlineCode))
	cabin := strings.ToLower(strings.TrimSpace(cabinClass))

	distKm := greatCircleDistanceKm(
		strings.ToUpper(strings.TrimSpace(origin)),
		strings.ToUpper(strings.TrimSpace(dest)),
	)
	distMiles := int(math.Round(float64(distKm) * 0.621371))

	// Flying Blue (SkyTeam) is revenue-based since 2018.
	if alliance == "skyteam" {
		return estimateFlyingBlue(airline, cabin, ticketPriceEUR, distMiles)
	}

	// Most Oneworld and Star Alliance programmes are distance-based.
	return estimateDistanceBased(alliance, airline, cabin, distMiles)
}

// estimateFlyingBlue estimates Flying Blue miles earned.
// KL/AF earn ~4-8 miles/EUR depending on cabin; partners earn ~2-4 miles/EUR.
func estimateFlyingBlue(airline, cabin string, priceEUR float64, distMiles int) EarningEstimate {
	est := EarningEstimate{
		Program: "Flying Blue",
		Method:  "revenue",
	}

	if priceEUR <= 0 {
		// Fallback: use distance with a low multiplier as a rough guess.
		mult := 0.5
		switch cabin {
		case "business":
			mult = 1.5
		case "first":
			mult = 2.0
		case "premium_economy":
			mult = 0.75
		}
		est.Miles = int(math.Round(float64(distMiles) * mult))
		est.Note = "estimate based on distance (no price available)"
		est.Method = "distance"
		return est
	}

	// Revenue-based rates (miles per EUR).
	// KL/AF (home carriers) earn the most; SkyTeam partners earn less;
	// non-SkyTeam airlines earn the least (if creditable at all).
	var rate float64
	isHomeCarrier := airline == "KL" || airline == "AF"
	isSkyTeamPartner := skyteamAirlines[airline]
	switch {
	case isHomeCarrier && (cabin == "business" || cabin == "first"):
		rate = 8.0
	case isHomeCarrier && cabin == "premium_economy":
		rate = 6.0
	case isHomeCarrier:
		rate = 4.0
	case isSkyTeamPartner && (cabin == "business" || cabin == "first"):
		rate = 4.0
	case isSkyTeamPartner && cabin == "premium_economy":
		rate = 3.0
	case isSkyTeamPartner:
		rate = 2.0
	default:
		// Non-SkyTeam airline: minimal or no earning.
		rate = 1.0
	}

	est.Miles = int(math.Round(priceEUR * rate))
	if !isHomeCarrier {
		if isSkyTeamPartner {
			est.Note = "SkyTeam partner: lower earning rate"
		} else {
			est.Note = "non-alliance airline: minimal earning"
		}
	}
	return est
}

// estimateDistanceBased estimates miles for distance-based programmes
// (Oneworld, Star Alliance, etc.).
func estimateDistanceBased(alliance, airline, cabin string, distMiles int) EarningEstimate {
	est := EarningEstimate{
		Method: "distance",
	}

	// Determine programme name.
	switch alliance {
	case "oneworld":
		est.Program = programNameForOneworld(airline)
	case "star_alliance":
		est.Program = "Star Alliance programme"
	default:
		est.Program = "Frequent flyer programme"
	}

	// Cabin multiplier for distance-based earning.
	var mult float64
	switch cabin {
	case "first":
		mult = 2.0 // 200% of distance
	case "business":
		mult = 1.5 // 150%
	case "premium_economy":
		mult = 1.0 // 100%
	default:
		mult = 0.5 // economy: 50% (many programmes' base rate for discounted economy)
	}

	// Oneworld partner earning can vary — apply a small reduction for non-home carriers.
	if alliance == "oneworld" && !oneworldAirlines[airline] {
		mult *= 0.5
	}

	// Minimum earning: many programmes guarantee at least 500 miles.
	miles := int(math.Round(float64(distMiles) * mult))
	if miles < 500 && distMiles > 0 {
		miles = 500
	}

	est.Miles = miles
	return est
}

// programNameForOneworld returns the FF programme name for a Oneworld airline.
func programNameForOneworld(airline string) string {
	switch airline {
	case "BA":
		return "Avios"
	case "AY":
		return "Finnair Plus"
	case "QF":
		return "Qantas Frequent Flyer"
	case "AA":
		return "AAdvantage"
	case "CX":
		return "Asia Miles"
	case "JL":
		return "JAL Mileage Bank"
	case "QR":
		return "Privilege Club"
	case "IB":
		return "Iberia Plus"
	case "RJ":
		return "Royal Plus"
	case "MH":
		return "Enrich"
	default:
		return "Oneworld programme"
	}
}

// MilesRedemptionValue calculates cents-per-mile value for a redemption.
//
// Returns the effective value per mile in cents. For example, if a flight
// costs EUR 150 cash and requires 15,000 miles, the value is 1.0 cents/mile.
//
// cashPriceEUR is the cash price in EUR. milesRequired is the redemption cost
// in miles. Returns 0 if milesRequired is 0.
func MilesRedemptionValue(cashPriceEUR float64, milesRequired int) float64 {
	if milesRequired <= 0 || cashPriceEUR <= 0 {
		return 0
	}
	return (cashPriceEUR * 100) / float64(milesRequired)
}

// IsGoodRedemption reports whether a miles redemption exceeds the programme's
// "good value" threshold.
//
// Flying Blue: > 1.2 cents/mile is considered good.
// Oneworld programmes: > 1.5 cents/mile is considered good.
// Other: > 1.3 cents/mile is considered good.
func IsGoodRedemption(centsPerMile float64, alliance string) bool {
	switch strings.ToLower(strings.TrimSpace(alliance)) {
	case "skyteam":
		return centsPerMile > 1.2
	case "oneworld":
		return centsPerMile > 1.5
	default:
		return centsPerMile > 1.3
	}
}

// greatCircleDistanceKm returns the great-circle distance in km between two
// airports identified by IATA code. Returns 0 if either airport is unknown.
func greatCircleDistanceKm(origin, dest string) int {
	a, okA := airportCoords[origin]
	b, okB := airportCoords[dest]
	if !okA || !okB {
		return 0
	}
	return int(math.Round(haversineKm(a[0], a[1], b[0], b[1])))
}

// haversineKm computes the Haversine great-circle distance in km.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := toRadians(lat2 - lat1)
	dLon := toRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRadians(lat1))*math.Cos(toRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}

func toRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

// GreatCircleDistanceKm is the exported version for use by other packages.
func GreatCircleDistanceKm(origin, dest string) int {
	return greatCircleDistanceKm(
		strings.ToUpper(strings.TrimSpace(origin)),
		strings.ToUpper(strings.TrimSpace(dest)),
	)
}
