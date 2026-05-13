package profile

import (
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// BuildProfile aggregates a slice of bookings into a TravelProfile.
// All derived stats (averages, top airlines, budget tier, etc.) are computed
// from the raw bookings.
func BuildProfile(bookings []Booking) *TravelProfile {
	p := &TravelProfile{
		Bookings:        bookings,
		SeasonalPattern: make(map[string]int),
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
	}

	if len(bookings) == 0 {
		return p
	}

	// Counters for aggregation.
	airlineFlights := make(map[string]int)   // code -> count
	airlineNames := make(map[string]string)  // code -> name
	routeCounts := make(map[string]int)      // "FROM-TO" -> count
	routePrices := make(map[string]float64)  // "FROM-TO" -> total price
	destCounts := make(map[string]int)       // destination -> count
	originCounts := make(map[string]int)     // origin -> count
	hotelChainNights := make(map[string]int) // chain -> total nights
	modeCounts := make(map[string]int)       // mode -> count
	typeCounts := make(map[string]int)       // booking type -> count
	dayCounts := make(map[string]int)        // weekday -> count

	var totalFlightPrice float64
	var flightCount int
	var totalStars float64
	var starCount int
	var totalNightlyRate float64
	var nightlyRateCount int
	var totalNights int
	var totalTripCost float64
	var totalLeadDays int
	var leadDaysCount int

	for _, b := range bookings {
		totalTripCost += b.Price

		switch b.Type {
		case "flight":
			flightCount++
			totalFlightPrice += b.Price
			from := strings.ToUpper(b.From)
			to := strings.ToUpper(b.To)

			// Track airline by provider code/name.
			code, name := parseAirline(b.Provider)
			if code != "" {
				airlineFlights[code]++
				if name != "" {
					airlineNames[code] = name
				}
			}

			// Route tracking.
			if from != "" && to != "" {
				key := from + "-" + to
				routeCounts[key]++
				routePrices[key] += b.Price
			}

			// Destination tracking.
			if to != "" {
				destCounts[to]++
			}
			if from != "" {
				originCounts[from]++
			}

		case "hotel":
			nights := b.Nights
			if nights <= 0 {
				nights = 1
			}
			totalNights += nights

			if b.Stars > 0 {
				totalStars += float64(b.Stars)
				starCount++
			}
			if b.Price > 0 && nights > 0 {
				totalNightlyRate += b.Price / float64(nights)
				nightlyRateCount++
			}

			chain := normalizeChain(b.Provider)
			if chain != "" {
				hotelChainNights[chain] += nights
			}

			typeCounts["hotel"]++

		case "airbnb":
			nights := b.Nights
			if nights <= 0 {
				nights = 1
			}
			totalNights += nights
			if b.Price > 0 && nights > 0 {
				totalNightlyRate += b.Price / float64(nights)
				nightlyRateCount++
			}
			typeCounts["airbnb"]++

		case "hostel":
			nights := b.Nights
			if nights <= 0 {
				nights = 1
			}
			totalNights += nights
			if b.Price > 0 && nights > 0 {
				totalNightlyRate += b.Price / float64(nights)
				nightlyRateCount++
			}
			typeCounts["hostel"]++

		case "ground":
			mode := strings.ToLower(b.Provider)
			if mode == "" {
				mode = "unknown"
			}
			// Normalize common providers to transport modes.
			mode = normalizeGroundMode(mode)
			modeCounts[mode]++
		}

		// Booking lead time: days between booking date and travel date.
		if b.Date != "" && b.TravelDate != "" {
			bookDate, err1 := models.ParseDate(b.Date)
			travelDate, err2 := models.ParseDate(b.TravelDate)
			if err1 == nil && err2 == nil && travelDate.After(bookDate) {
				lead := int(travelDate.Sub(bookDate).Hours() / 24)
				totalLeadDays += lead
				leadDaysCount++
			}
		}

		// Travel date patterns.
		if b.TravelDate != "" {
			if t, err := models.ParseDate(b.TravelDate); err == nil {
				dayCounts[t.Weekday().String()]++
				p.SeasonalPattern[t.Month().String()]++
			}
		}
	}

	// Populate counts.
	p.TotalFlights = flightCount
	p.TotalHotelNights = totalNights

	// Estimate total trips from unique travel months (rough heuristic).
	p.TotalTrips = estimateTripCount(bookings)

	// Airline stats (sorted by frequency).
	p.TopAirlines = buildAirlineStats(airlineFlights, airlineNames)

	// Preferred alliance.
	p.PreferredAlliance = detectAlliance(p.TopAirlines)

	// Flight averages.
	if flightCount > 0 {
		p.AvgFlightPrice = totalFlightPrice / float64(flightCount)
	}
	if leadDaysCount > 0 {
		p.AvgBookingLead = totalLeadDays / leadDaysCount
	}

	// Route stats (sorted by frequency).
	p.TopRoutes = buildRouteStats(routeCounts, routePrices)

	// Top destinations.
	p.TopDestinations = topKeys(destCounts, 10)

	// Detect home airports (most-departed-from).
	p.HomeDetected = topKeys(originCounts, 3)

	// Hotel stats.
	p.TopHotelChains = buildHotelChainStats(hotelChainNights)
	if starCount > 0 {
		p.AvgStarRating = totalStars / float64(starCount)
	}
	if nightlyRateCount > 0 {
		p.AvgNightlyRate = totalNightlyRate / float64(nightlyRateCount)
	}
	p.PreferredType = detectPreferredType(typeCounts)

	// Ground transport stats.
	p.TopGroundModes = buildModeStats(modeCounts)

	// Timing patterns.
	p.AvgTripLength = estimateAvgTripLength(bookings)
	p.PreferredDays = topKeys(dayCounts, 3)

	// Budget.
	tripCount := p.TotalTrips
	if tripCount == 0 {
		tripCount = 1
	}
	p.AvgTripCost = totalTripCost / float64(tripCount)
	p.BudgetTier = classifyBudget(p.AvgTripCost, p.AvgNightlyRate)

	return p
}

// parseAirline extracts an airline code and name from a provider string.
// Handles formats like "KLM", "KL", "AY (Finnair)", "Finnair".
func parseAirline(provider string) (code, name string) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return "", ""
	}

	// Check for "CODE (Name)" format.
	if idx := strings.Index(provider, "("); idx > 0 {
		code = strings.TrimSpace(provider[:idx])
		name = strings.Trim(provider[idx:], "() ")
		return code, name
	}

	// Known airline names to codes.
	knownAirlines := map[string]string{
		"finnair":            "AY",
		"klm":                "KL",
		"ryanair":            "FR",
		"easyjet":            "U2",
		"norwegian":          "DY",
		"sas":                "SK",
		"lufthansa":          "LH",
		"british airways":    "BA",
		"air france":         "AF",
		"iberia":             "IB",
		"vueling":            "VY",
		"wizzair":            "W6",
		"wizz air":           "W6",
		"transavia":          "HV",
		"eurowings":          "EW",
		"tap":                "TP",
		"tap portugal":       "TP",
		"aegean":             "A3",
		"turkish airlines":   "TK",
		"swiss":              "LX",
		"austrian":           "OS",
		"brussels airlines":  "SN",
		"lot":                "LO",
		"lot polish":         "LO",
		"aer lingus":         "EI",
		"jetblue":            "B6",
		"delta":              "DL",
		"united":             "UA",
		"american":           "AA",
		"southwest":          "WN",
		"emirates":           "EK",
		"qatar":              "QR",
		"qatar airways":      "QR",
		"singapore airlines": "SQ",
		"cathay pacific":     "CX",
		"japan airlines":     "JL",
		"ana":                "NH",
	}

	lower := strings.ToLower(provider)
	if c, ok := knownAirlines[lower]; ok {
		return c, provider
	}

	// If it looks like a 2-letter IATA code, use it directly.
	if len(provider) == 2 && isAlpha(provider) {
		return strings.ToUpper(provider), ""
	}
	// 3-letter could be ICAO — still use it.
	if len(provider) <= 3 && isAlpha(provider) {
		return strings.ToUpper(provider), ""
	}

	// Fallback: use the provider string as-is for the name, empty code.
	return "", provider
}

func isAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// normalizeChain cleans up hotel provider/chain names for aggregation.
func normalizeChain(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return ""
	}

	knownChains := map[string]string{
		"marriott":     "Marriott",
		"hilton":       "Hilton",
		"ihg":          "IHG",
		"hyatt":        "Hyatt",
		"accor":        "Accor",
		"wyndham":      "Wyndham",
		"best western": "Best Western",
		"radisson":     "Radisson",
		"choice":       "Choice Hotels",
		"melia":        "Melia",
		"nh hotels":    "NH Hotels",
		"nh":           "NH Hotels",
		"airbnb":       "Airbnb",
		"booking.com":  "Booking.com",
		"hostelworld":  "Hostelworld",
	}

	lower := strings.ToLower(provider)
	for key, normalized := range knownChains {
		if strings.Contains(lower, key) {
			return normalized
		}
	}
	return provider
}

// normalizeGroundMode maps provider names to transport modes.
func normalizeGroundMode(provider string) string {
	modeMap := map[string]string{
		"flixbus":       "bus",
		"flix":          "bus",
		"eurolines":     "bus",
		"megabus":       "bus",
		"regiojet":      "bus",
		"blablabus":     "bus",
		"greyhound":     "bus",
		"eurostar":      "train",
		"thalys":        "train",
		"sncf":          "train",
		"trenitalia":    "train",
		"renfe":         "train",
		"db":            "train",
		"deutsche bahn": "train",
		"ns":            "train",
		"sj":            "train",
		"vr":            "train",
		"amtrak":        "train",
		"avanti":        "train",
		"lner":          "train",
		"ouigo":         "train",
		"italo":         "train",
		"stena line":    "ferry",
		"viking line":   "ferry",
		"tallink":       "ferry",
		"dfds":          "ferry",
		"irish ferries": "ferry",
		"p&o":           "ferry",
		"brittany":      "ferry",
		"color line":    "ferry",
		"fjord line":    "ferry",
		"uber":          "rideshare",
		"bolt":          "rideshare",
		"lyft":          "rideshare",
		"grab":          "rideshare",
	}

	for key, mode := range modeMap {
		if strings.Contains(provider, key) {
			return mode
		}
	}

	// Check if the provider string IS a mode.
	switch provider {
	case "bus", "train", "ferry", "rideshare", "taxi", "metro", "tram":
		return provider
	}

	return provider
}

// allianceMap maps airline IATA codes to alliances.
var allianceMap = map[string]string{
	// Star Alliance
	"LH": "Star Alliance", "SK": "Star Alliance", "LX": "Star Alliance",
	"OS": "Star Alliance", "SN": "Star Alliance", "TP": "Star Alliance",
	"TK": "Star Alliance", "A3": "Star Alliance", "LO": "Star Alliance",
	"NH": "Star Alliance", "SQ": "Star Alliance", "UA": "Star Alliance",
	"AC": "Star Alliance", "NZ": "Star Alliance", "ET": "Star Alliance",
	"MS": "Star Alliance",
	// SkyTeam
	"AF": "SkyTeam", "KL": "SkyTeam", "AZ": "SkyTeam",
	"DL": "SkyTeam", "KE": "SkyTeam", "SU": "SkyTeam",
	"MU": "SkyTeam", "CZ": "SkyTeam", "CI": "SkyTeam",
	"ME": "SkyTeam", "RO": "SkyTeam", "SV": "SkyTeam",
	// Oneworld
	"BA": "Oneworld", "IB": "Oneworld", "AY": "Oneworld",
	"AA": "Oneworld", "QF": "Oneworld", "CX": "Oneworld",
	"JL": "Oneworld", "MH": "Oneworld", "QR": "Oneworld",
	"RJ": "Oneworld", "S7": "Oneworld", "EI": "Oneworld",
}

// detectAlliance determines the most-used alliance based on airline stats.
func detectAlliance(airlines []AirlineStats) string {
	counts := make(map[string]int)
	for _, a := range airlines {
		if alliance, ok := allianceMap[a.Code]; ok {
			counts[alliance] += a.Flights
		}
	}
	if len(counts) == 0 {
		return ""
	}

	var best string
	var bestCount int
	for alliance, count := range counts {
		if count > bestCount {
			best = alliance
			bestCount = count
		}
	}
	return best
}

// buildAirlineStats converts frequency maps to sorted AirlineStats.
func buildAirlineStats(flights map[string]int, names map[string]string) []AirlineStats {
	stats := make([]AirlineStats, 0, len(flights))
	for code, count := range flights {
		stats = append(stats, AirlineStats{
			Code:    code,
			Name:    names[code],
			Flights: count,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Flights > stats[j].Flights
	})
	if len(stats) > 10 {
		stats = stats[:10]
	}
	return stats
}

// buildRouteStats converts frequency maps to sorted RouteStats.
func buildRouteStats(counts map[string]int, prices map[string]float64) []RouteStats {
	stats := make([]RouteStats, 0, len(counts))
	for key, count := range counts {
		parts := strings.SplitN(key, "-", 2)
		if len(parts) != 2 {
			continue
		}
		avg := 0.0
		if count > 0 {
			avg = prices[key] / float64(count)
		}
		stats = append(stats, RouteStats{
			From:     parts[0],
			To:       parts[1],
			Count:    count,
			AvgPrice: avg,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})
	if len(stats) > 10 {
		stats = stats[:10]
	}
	return stats
}

// buildHotelChainStats converts frequency map to sorted HotelChainStats.
func buildHotelChainStats(nights map[string]int) []HotelChainStats {
	stats := make([]HotelChainStats, 0, len(nights))
	for name, n := range nights {
		stats = append(stats, HotelChainStats{Name: name, Nights: n})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Nights > stats[j].Nights
	})
	if len(stats) > 10 {
		stats = stats[:10]
	}
	return stats
}

// buildModeStats converts frequency map to sorted ModeStats.
func buildModeStats(counts map[string]int) []ModeStats {
	stats := make([]ModeStats, 0, len(counts))
	for mode, count := range counts {
		stats = append(stats, ModeStats{Mode: mode, Count: count})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})
	return stats
}

// topKeys returns the top n keys from a frequency map, sorted by count descending.
func topKeys(counts map[string]int, n int) []string {
	type kv struct {
		key   string
		count int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].count > pairs[j].count
	})
	result := make([]string, 0, n)
	for i, p := range pairs {
		if i >= n {
			break
		}
		result = append(result, p.key)
	}
	return result
}

// estimateTripCount estimates the number of distinct trips by grouping bookings
// within 2-day windows of travel dates.
func estimateTripCount(bookings []Booking) int {
	var dates []time.Time
	for _, b := range bookings {
		if b.TravelDate != "" {
			if t, err := models.ParseDate(b.TravelDate); err == nil {
				dates = append(dates, t)
			}
		}
	}
	if len(dates) == 0 {
		return len(bookings) // fallback: 1 booking = 1 trip
	}

	sort.Slice(dates, func(i, j int) bool {
		return dates[i].Before(dates[j])
	})

	trips := 1
	lastDate := dates[0]
	for _, d := range dates[1:] {
		if d.Sub(lastDate).Hours() > 48 {
			trips++
		}
		lastDate = d
	}
	return trips
}

// estimateAvgTripLength estimates average trip length from hotel bookings.
func estimateAvgTripLength(bookings []Booking) float64 {
	var totalNights int
	var count int
	for _, b := range bookings {
		if (b.Type == "hotel" || b.Type == "airbnb" || b.Type == "hostel") && b.Nights > 0 {
			totalNights += b.Nights
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(totalNights) / float64(count)
}

// detectPreferredType returns the most-used accommodation type.
func detectPreferredType(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	var best string
	var bestCount int
	for typ, count := range counts {
		if count > bestCount {
			best = typ
			bestCount = count
		}
	}
	return best
}

// classifyBudget returns a budget tier based on average costs.
func classifyBudget(avgTripCost, avgNightlyRate float64) string {
	// Use nightly rate as primary signal (more stable than total trip cost).
	if avgNightlyRate > 0 {
		switch {
		case avgNightlyRate < 60:
			return "budget"
		case avgNightlyRate < 150:
			return "mid-range"
		default:
			return "premium"
		}
	}
	// Fallback to trip cost.
	if avgTripCost > 0 {
		switch {
		case avgTripCost < 300:
			return "budget"
		case avgTripCost < 1000:
			return "mid-range"
		default:
			return "premium"
		}
	}
	return ""
}
