package hacks

import (
	"context"
	"fmt"
	"math"
	"time"
)

// routeType classifies the nature of a flight route for advance-purchase
// window analysis.
type routeType int

const (
	routeEuropeanShort    routeType = iota // <3h / <2500km
	routeEuropeanLong                      // transatlantic / >5h / >2500km
	routeBudgetCarrier                     // Ryanair, Wizz, easyJet served
	routeHolidaySeasonal                   // Greek islands, Canaries, ski resorts Dec-Mar
	routeWeekendCityBreak                  // Fri-Sun trip
)

// purchaseWindow defines the optimal advance-purchase range (in days) for
// a route type.
type purchaseWindow struct {
	optimalMin  int // earliest recommended booking (days ahead)
	optimalMax  int // latest recommended booking (days ahead)
	spikeInside int // prices spike when fewer than this many days ahead
	label       string
}

// optimalWindows maps route types to their optimal advance-purchase windows.
var optimalWindows = map[routeType]purchaseWindow{
	routeEuropeanShort:    {optimalMin: 21, optimalMax: 56, spikeInside: 14, label: "European short-haul"},
	routeEuropeanLong:     {optimalMin: 42, optimalMax: 112, spikeInside: 28, label: "European long-haul / transatlantic"},
	routeBudgetCarrier:    {optimalMin: 28, optimalMax: 70, spikeInside: 14, label: "budget carrier route"},
	routeHolidaySeasonal:  {optimalMin: 56, optimalMax: 112, spikeInside: 28, label: "holiday/seasonal destination"},
	routeWeekendCityBreak: {optimalMin: 21, optimalMax: 42, spikeInside: 14, label: "weekend city break"},
}

// budgetCarrierAirports lists airports heavily served by Ryanair, Wizz Air, or
// easyJet. Used for route classification when we can't identify the carrier.
var budgetCarrierAirports = map[string]bool{
	"STN": true, "LTN": true, "BVA": true, "HHN": true, "NYO": true,
	"CRL": true, "EIN": true, "GRO": true, "REU": true, "CIA": true,
	"BGY": true, "TSF": true, "WMI": true, "BUD": true, "KTW": true,
	"GDN": true, "WRO": true, "RZE": true, "SOF": true, "CLJ": true,
	"OTP": true, "BEG": true, "SKG": true, "KUN": true, "RIX": true,
	"TLL": true, "VNO": true, "DEB": true,
}

// holidayDestinations lists IATA codes for seasonal/holiday destinations where
// demand-driven pricing dominates.
var holidayDestinations = map[string]bool{
	// Greek islands
	"JMK": true, "JTR": true, "RHO": true, "CFU": true, "CHQ": true,
	"ZTH": true, "KGS": true, "HER": true, "JKH": true, "SMI": true,
	"PAS": true, "MJT": true, "SKU": true, "JSI": true,
	// Canary Islands
	"TFS": true, "LPA": true, "ACE": true, "FUE": true, "SPC": true,
	// Balearic Islands
	"PMI": true, "MAH": true, "IBZ": true,
	// Ski resort airports (Alps)
	"INN": true, "SZG": true, "GVA": true, "LYS": true, "TRN": true,
	"BRN": true, "FMM": true,
	// Turkish coast
	"AYT": true, "DLM": true, "BJV": true, "ADB": true,
	// Croatian coast
	"DBV": true, "SPU": true, "PUY": true, "ZAD": true,
}

// skiSeasonMonths defines when ski season pricing applies (December-March).
var skiSeasonMonths = map[time.Month]bool{
	time.December: true, time.January: true, time.February: true, time.March: true,
}

// skiAirports lists airports near major ski resorts.
var skiAirports = map[string]bool{
	"INN": true, "SZG": true, "GVA": true, "LYS": true, "TRN": true,
	"BRN": true, "FMM": true, "MUC": true, "ZRH": true, "BGY": true,
}

// airportCoords maps IATA codes to lat/lon for haversine distance calculation.
// Top 50+ European airports by traffic.
var airportCoords = map[string][2]float64{
	"LHR": {51.4700, -0.4543},
	"CDG": {49.0097, 2.5479},
	"AMS": {52.3086, 4.7639},
	"FRA": {50.0333, 8.5706},
	"IST": {41.2753, 28.7519},
	"MAD": {40.4719, -3.5626},
	"BCN": {41.2971, 2.0785},
	"LGW": {51.1537, -0.1821},
	"MUC": {48.3538, 11.7861},
	"FCO": {41.8003, 12.2389},
	"ORY": {48.7233, 2.3794},
	"SVO": {55.9726, 37.4146},
	"DUB": {53.4264, -6.2499},
	"ZRH": {47.4647, 8.5492},
	"CPH": {55.6180, 12.6508},
	"PMI": {39.5517, 2.7388},
	"OSL": {60.1939, 11.1004},
	"VIE": {48.1103, 16.5697},
	"ARN": {59.6519, 17.9186},
	"LIS": {38.7756, -9.1354},
	"HEL": {60.3172, 24.9633},
	"MAN": {53.3537, -2.2750},
	"STN": {51.8860, 0.2389},
	"BRU": {50.9014, 4.4844},
	"AGP": {36.6749, -4.4991},
	"BUD": {47.4369, 19.2556},
	"EDI": {55.9508, -3.3615},
	"PRG": {50.1008, 14.2600},
	"WAW": {52.1657, 20.9671},
	"ATH": {37.9364, 23.9445},
	"LTN": {51.8747, -0.3683},
	"KEF": {63.9850, -22.6056},
	"BGY": {45.6739, 9.7042},
	"NAP": {40.8860, 14.2908},
	"GVA": {46.2381, 6.1089},
	"BER": {52.3667, 13.5033},
	"TXL": {52.5597, 13.2877},
	"NCE": {43.6584, 7.2159},
	"HAM": {53.6304, 9.9882},
	"DUS": {51.2895, 6.7668},
	"STR": {48.6899, 9.2220},
	"LYS": {45.7256, 5.0811},
	"GDN": {54.3776, 18.4662},
	"KRK": {50.0777, 19.7848},
	"RIX": {56.9236, 23.9711},
	"TLL": {59.4133, 24.8328},
	"VNO": {54.6341, 25.2858},
	"SOF": {42.6952, 23.4062},
	"OTP": {44.5711, 26.0850},
	"BEG": {44.8184, 20.3091},
	"SKG": {40.5197, 22.9709},
	"TFS": {28.0445, -16.5725},
	"LPA": {27.9319, -15.3866},
	"JMK": {37.4351, 25.3481},
	"JTR": {36.3992, 25.4793},
	"RHO": {36.4054, 28.0862},
	"CFU": {39.6019, 19.9117},
	"CHQ": {35.5317, 24.1497},
	"HER": {35.3397, 25.1803},
	"INN": {47.2602, 11.3440},
	"SZG": {47.7933, 13.0043},
	"AYT": {36.8987, 30.8005},
	"DBV": {42.5614, 18.2682},
	"SPU": {43.5389, 16.2980},
	"IBZ": {38.8729, 1.3731},
	"MAH": {39.8626, 4.2186},
	"CIA": {41.7994, 12.5949},
	// Major non-European hubs for distance classification
	"JFK": {40.6413, -73.7781},
	"EWR": {40.6895, -74.1745},
	"ORD": {41.9742, -87.9073},
	"BOS": {42.3656, -71.0096},
	"YYZ": {43.6777, -79.6248},
	"YUL": {45.4706, -73.7408},
	// Fare breakpoint hubs and long-haul destinations
	"DOH": {25.2731, 51.6081},
	"DXB": {25.2532, 55.3657},
	"AUH": {24.4431, 54.6511},
	"BOG": {4.7016, -74.1469},
	"ADD": {8.9779, 38.7993},
	"CMN": {33.3675, -7.5898},
	"BKK": {13.6900, 100.7501},
	"SIN": {1.3644, 103.9915},
	"DEL": {28.5562, 77.1000},
	"NRT": {35.7720, 140.3929},
	"GRU": {-23.4356, -46.4731},
	"JNB": {-26.1392, 28.2460},
	"SYD": {-33.9461, 151.1772},
	"LAX": {33.9425, -118.4081},
	"MIA": {25.7959, -80.2870},
	"MEX": {19.4361, -99.0719},
	"LIM": {-12.0219, -77.1143},
	"SCL": {-33.3930, -70.7858},
}

// detectAdvancePurchase checks if the booking timing is optimal for the route type.
func detectAdvancePurchase(_ context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	depart, err := parseDate(in.Date)
	if err != nil {
		return nil
	}

	daysAhead := int(time.Until(depart).Hours() / 24)
	if daysAhead < 0 {
		return nil // departure is in the past
	}

	rt := classifyRoute(in.Origin, in.Destination, depart, in.ReturnDate)
	window := optimalWindows[rt]

	// Timing is optimal — nothing to report.
	if daysAhead >= window.optimalMin && daysAhead <= window.optimalMax {
		return nil
	}

	currency := in.currency()

	if daysAhead > window.optimalMax {
		// Booking too early.
		weeksUntilOptimal := (daysAhead - window.optimalMax + 6) / 7 // ceiling weeks
		optimalDate := time.Now().AddDate(0, 0, daysAhead-window.optimalMax)

		return []Hack{{
			Type:     "advance_purchase",
			Title:    fmt.Sprintf("Too early to book — wait %d more weeks", weeksUntilOptimal),
			Currency: currency,
			Savings:  0, // advisory, no concrete savings estimate
			Description: fmt.Sprintf(
				"For %s routes, the optimal booking window is %d-%d weeks before departure. "+
					"Booking now (%d weeks ahead) is too early — prices may not reflect final availability. "+
					"Wait until around %s for the best fares.",
				window.label, window.optimalMin/7, window.optimalMax/7,
				daysAhead/7, optimalDate.Format("2006-01-02"),
			),
			Risks: []string{
				"Waiting risks prices rising if demand is unusually high",
				"Popular routes may sell out of cheap fare buckets early",
			},
			Steps: []string{
				fmt.Sprintf("Set a reminder to search %s→%s around %s",
					in.Origin, in.Destination, optimalDate.Format("2006-01-02")),
				"Use Google Flights price tracking to monitor fare trends",
				"Book when the price enters your target range within the optimal window",
			},
		}}
	}

	// Booking too late.
	if daysAhead < window.spikeInside {
		// Very late — prices are likely inflated.
		return []Hack{{
			Type:     "advance_purchase",
			Title:    "Last-minute booking — prices likely inflated",
			Currency: currency,
			Savings:  0,
			Description: fmt.Sprintf(
				"For %s routes, prices typically spike inside %d weeks of departure. "+
					"At %d days out, you're in the most expensive booking window. "+
					"Book now if you must travel, or consider alternative dates/routes.",
				window.label, window.spikeInside/7, daysAhead,
			),
			Risks: []string{
				"Prices will likely only increase from here",
				"Fewer seat options remaining",
			},
			Steps: []string{
				"Book now if this date is fixed — waiting further will not help",
				"Check nearby airports for potentially cheaper last-minute fares",
				"Consider flexible dates ±1-2 days for lower prices",
				fmt.Sprintf("For future trips, aim to book %s routes %d-%d weeks ahead",
					window.label, window.optimalMin/7, window.optimalMax/7),
			},
		}}
	}

	// Late but not yet in spike zone.
	return []Hack{{
		Type:     "advance_purchase",
		Title:    fmt.Sprintf("Book soon — only %d days until departure", daysAhead),
		Currency: currency,
		Savings:  0,
		Description: fmt.Sprintf(
			"For %s routes, the optimal booking window is %d-%d weeks ahead. "+
				"At %d days out, you're past the sweet spot. Prices may still be reasonable "+
				"but will spike inside %d weeks. Book within the next few days.",
			window.label, window.optimalMin/7, window.optimalMax/7,
			daysAhead, window.spikeInside/7,
		),
		Risks: []string{
			"Prices increase as departure approaches",
			"Cheap fare buckets may already be sold out",
		},
		Steps: []string{
			fmt.Sprintf("Search %s→%s now and compare current prices", in.Origin, in.Destination),
			"Book within the next 2-3 days to avoid further price increases",
			"Check nearby dates — midweek departures may still have good availability",
		},
	}}
}

// classifyRoute determines the route type based on origin, destination,
// distance, and travel dates.
func classifyRoute(origin, destination string, depart time.Time, returnDate string) routeType {
	// Check for weekend city break (Fri-Sun).
	if returnDate != "" {
		ret, err := parseDate(returnDate)
		if err == nil {
			if depart.Weekday() == time.Friday && ret.Weekday() == time.Sunday {
				return routeWeekendCityBreak
			}
		}
	}

	// Check for holiday/seasonal destinations.
	if holidayDestinations[destination] {
		// Ski resorts are seasonal only in Dec-Mar.
		if skiAirports[destination] && !skiSeasonMonths[depart.Month()] {
			// Not ski season — fall through to distance-based classification.
		} else {
			return routeHolidaySeasonal
		}
	}

	// Check for budget carrier airports.
	if budgetCarrierAirports[origin] || budgetCarrierAirports[destination] {
		return routeBudgetCarrier
	}

	// Distance-based classification.
	dist := airportDistanceKm(origin, destination)
	if dist > 0 && dist > 2500 {
		return routeEuropeanLong
	}

	return routeEuropeanShort
}

// airportDistanceKm returns the great-circle distance between two airports
// in kilometres. Returns 0 if either airport is unknown.
func airportDistanceKm(a, b string) float64 {
	coordA, okA := airportCoords[a]
	coordB, okB := airportCoords[b]
	if !okA || !okB {
		return 0
	}
	return haversineKm(coordA[0], coordA[1], coordB[0], coordB[1])
}

// haversineKm returns the great-circle distance in kilometres between two
// lat/lon points (degrees).
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

// isUpperIATA returns true if s is a 3-letter uppercase string (IATA code format).
func isUpperIATA(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
