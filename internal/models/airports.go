package models

import (
	"sort"
	"strings"
	"sync"
)

// AirportNames maps IATA airport codes to city/airport names for the top 200
// airports worldwide. Used as a fallback when the API response does not include
// city names (e.g., in explore results).
var AirportNames = map[string]string{
	// Europe
	"HEL": "Helsinki",
	"LHR": "London Heathrow",
	"LGW": "London Gatwick",
	"STN": "London Stansted",
	"LTN": "London Luton",
	"SEN": "London Southend",
	"BRS": "Bristol",
	"CDG": "Paris CDG",
	"ORY": "Paris Orly",
	"AMS": "Amsterdam",
	"FRA": "Frankfurt",
	"MUC": "Munich",
	"BER": "Berlin",
	"DUS": "Dusseldorf",
	"HAM": "Hamburg",
	"MAD": "Madrid",
	"BCN": "Barcelona",
	"AGP": "Malaga",
	"PMI": "Palma de Mallorca",
	"ALC": "Alicante",
	"IBZ": "Ibiza",
	"VLC": "Valencia",
	"SVQ": "Seville",
	"FCO": "Rome Fiumicino",
	"MXP": "Milan Malpensa",
	"NAP": "Naples",
	"VCE": "Venice",
	"LIN": "Milan Linate",
	"BGY": "Milan Bergamo",
	"BLQ": "Bologna",
	"OLB": "Olbia",
	"CTA": "Catania",
	"PSA": "Pisa",
	"ZRH": "Zurich",
	"GVA": "Geneva",
	"VIE": "Vienna",
	"BRU": "Brussels",
	"CPH": "Copenhagen",
	"OSL": "Oslo",
	"ARN": "Stockholm",
	"GOT": "Gothenburg",
	"DUB": "Dublin",
	"LIS": "Lisbon",
	"OPO": "Porto",
	"ATH": "Athens",
	"GDN": "Gdansk",
	"WRO": "Wroclaw",
	"KTW": "Katowice",
	"FAO": "Faro",
	"SKG": "Thessaloniki",
	"WAW": "Warsaw",
	"KRK": "Krakow",
	"PRG": "Prague",
	"BUD": "Budapest",
	"OTP": "Bucharest",
	"SOF": "Sofia",
	"IST": "Istanbul",
	"SAW": "Istanbul Sabiha",
	"AYT": "Antalya",
	"ZAG": "Zagreb",
	"BEG": "Belgrade",
	"TLL": "Tallinn",
	"RIX": "Riga",
	"VNO": "Vilnius",
	"KEF": "Reykjavik",
	"EDI": "Edinburgh",
	"MAN": "Manchester",
	"BHX": "Birmingham",
	"NCE": "Nice",
	"LYS": "Lyon",
	"TLS": "Toulouse",
	"MRS": "Marseille",
	"DBV": "Dubrovnik",
	"SPU": "Split",
	"TIV": "Tivat",
	"CFU": "Corfu",
	"HER": "Heraklion",
	"RHO": "Rhodes",
	"JTR": "Santorini",
	"TFS": "Tenerife South",
	"LPA": "Gran Canaria",
	"ACE": "Lanzarote",
	"FUE": "Fuerteventura",

	// North America
	"JFK": "New York JFK",
	"EWR": "Newark",
	"LGA": "New York LaGuardia",
	"LAX": "Los Angeles",
	"SFO": "San Francisco",
	"ORD": "Chicago O'Hare",
	"MDW": "Chicago Midway",
	"ATL": "Atlanta",
	"DFW": "Dallas/Fort Worth",
	"DEN": "Denver",
	"SEA": "Seattle",
	"MIA": "Miami",
	"FLL": "Fort Lauderdale",
	"MCO": "Orlando",
	"TPA": "Tampa",
	"BOS": "Boston",
	"IAD": "Washington Dulles",
	"DCA": "Washington Reagan",
	"PHL": "Philadelphia",
	"MSP": "Minneapolis",
	"DTW": "Detroit",
	"CLT": "Charlotte",
	"PHX": "Phoenix",
	"SAN": "San Diego",
	"IAH": "Houston",
	"AUS": "Austin",
	"SLC": "Salt Lake City",
	"PDX": "Portland",
	"BNA": "Nashville",
	"RDU": "Raleigh-Durham",
	"HNL": "Honolulu",
	"OGG": "Maui",
	"YYZ": "Toronto Pearson",
	"YVR": "Vancouver",
	"YUL": "Montreal",
	"YYC": "Calgary",
	"YOW": "Ottawa",
	"MEX": "Mexico City",
	"CUN": "Cancun",
	"SJD": "San Jose del Cabo",
	"GDL": "Guadalajara",

	// Asia
	"NRT": "Tokyo Narita",
	"HND": "Tokyo Haneda",
	"KIX": "Osaka Kansai",
	"ICN": "Seoul Incheon",
	"PEK": "Beijing Capital",
	"PKX": "Beijing Daxing",
	"PVG": "Shanghai Pudong",
	"HKG": "Hong Kong",
	"TPE": "Taipei",
	"SIN": "Singapore",
	"BKK": "Bangkok Suvarnabhumi",
	"DMK": "Bangkok Don Mueang",
	"KUL": "Kuala Lumpur",
	"CGK": "Jakarta",
	"MNL": "Manila",
	"SGN": "Ho Chi Minh City",
	"HAN": "Hanoi",
	"DEL": "New Delhi",
	"BOM": "Mumbai",
	"BLR": "Bangalore",
	"MAA": "Chennai",
	"CCU": "Kolkata",
	"CMB": "Colombo",
	"KTM": "Kathmandu",
	"DPS": "Bali Denpasar",
	"REP": "Siem Reap",
	"RGN": "Yangon",
	"PNH": "Phnom Penh",

	// Middle East
	"DXB": "Dubai",
	"AUH": "Abu Dhabi",
	"DOH": "Doha",
	"RUH": "Riyadh",
	"JED": "Jeddah",
	"TLV": "Tel Aviv",
	"AMM": "Amman",
	"BAH": "Bahrain",
	"MCT": "Muscat",
	"KWI": "Kuwait City",

	// Africa
	"JNB": "Johannesburg",
	"CPT": "Cape Town",
	"NBO": "Nairobi",
	"CAI": "Cairo",
	"CMN": "Casablanca",
	"RAK": "Marrakech",
	"ADD": "Addis Ababa",
	"LOS": "Lagos",
	"ACC": "Accra",
	"DSS": "Dakar",
	"TUN": "Tunis",

	// Oceania
	"SYD": "Sydney",
	"MEL": "Melbourne",
	"BNE": "Brisbane",
	"PER": "Perth",
	"AKL": "Auckland",
	"CHC": "Christchurch",
	"WLG": "Wellington",
	"NAN": "Nadi Fiji",
	"PPT": "Tahiti",

	// South America
	"GRU": "Sao Paulo",
	"GIG": "Rio de Janeiro",
	"EZE": "Buenos Aires",
	"SCL": "Santiago",
	"BOG": "Bogota",
	"MDE": "Medellin",
	"LIM": "Lima",
	"UIO": "Quito",
	"CCS": "Caracas",
	"MVD": "Montevideo",
	"PTY": "Panama City",
	"SJO": "San Jose Costa Rica",
	"HAV": "Havana",
	"SDQ": "Santo Domingo",
	"MBJ": "Montego Bay",
}

// airportSearchCities maps airport codes with airport-qualified display names to
// provider-friendly city names for broader ground searches.
var airportSearchCities = map[string]string{
	"CDG": "Paris",
	"ORY": "Paris",
	"LHR": "London",
	"LGW": "London",
	"STN": "London",
	"LTN": "London",
	"SEN": "London",
	"FCO": "Rome",
	"MXP": "Milan",
	"LIN": "Milan",
	"BGY": "Milan",
	"SAW": "Istanbul",
	"JFK": "New York",
	"LGA": "New York",
	"NRT": "Tokyo",
	"HND": "Tokyo",
	"KIX": "Osaka",
	"ICN": "Seoul",
	"PEK": "Beijing",
	"PKX": "Beijing",
	"PVG": "Shanghai",
	"BKK": "Bangkok",
	"DMK": "Bangkok",
	"DPS": "Denpasar",
}

// LookupAirportName returns the city/airport name for an IATA code.
// Returns the code itself if no match is found.
func LookupAirportName(code string) string {
	if name, ok := AirportNames[code]; ok {
		return name
	}
	return code
}

// ResolveAirportCity converts an airport code into a provider-friendly city
// name for broader ground transport searches.
func ResolveAirportCity(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	if city, ok := airportSearchCities[code]; ok {
		return city
	}
	return LookupAirportName(code)
}

// ResolveLocationName converts a string that might be an IATA code into a
// city name suitable for hotel/ground searches. If the input is already a
// city name (not a 3-letter uppercase IATA code), it is returned as-is.
// This prevents "PRG" being sent to Google Hotels (which needs "Prague").
func ResolveLocationName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Only resolve if it looks like an IATA code (2-3 uppercase letters).
	upper := strings.ToUpper(s)
	if len(upper) <= 3 && upper == s {
		if name, ok := AirportNames[upper]; ok {
			return name
		}
	}
	return s
}

// ResolveHotelCity returns the best city name for hotel search. For airport
// codes, this returns the broader city (e.g. "Paris" for CDG) instead of the
// airport-qualified name ("Paris CDG") which tends to bias hotel search
// toward airport-area properties.
func ResolveHotelCity(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	upper := strings.ToUpper(s)
	if len(upper) <= 3 && upper == s {
		// Prefer the broader city for hotel search.
		if city, ok := airportSearchCities[upper]; ok {
			return city
		}
		if name, ok := AirportNames[upper]; ok {
			return name
		}
	}
	return s
}

// IsIATACode returns true if s is exactly 3 uppercase ASCII letters.
// Does not validate that the code exists in any airport database.
func IsIATACode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

var (
	cityAirportsOnce sync.Once
	cityAirports     map[string][]string // lowercase city name → sorted []IATA
)

func buildCityAirports() {
	m := make(map[string][]string)

	for iata, city := range airportSearchCities {
		key := strings.ToLower(city)
		m[key] = append(m[key], iata)
	}

	for iata, display := range AirportNames {
		if _, covered := airportSearchCities[iata]; covered {
			continue
		}
		// Strip trailing IATA-code suffix e.g. "New York JFK" → "New York",
		// "Paris CDG" → "Paris". If no suffix, keep display name as-is.
		city := display
		if idx := strings.LastIndex(display, " "); idx >= 0 {
			suffix := display[idx+1:]
			if IsIATACode(suffix) {
				city = display[:idx]
			}
		}
		key := strings.ToLower(strings.TrimSpace(city))
		if key == "" {
			continue
		}
		if _, exists := m[key]; !exists {
			m[key] = append(m[key], iata)
		}
	}

	// Sort each list for deterministic output.
	for k := range m {
		sort.Strings(m[k])
	}
	cityAirports = m
}

// ResolveCityToAirports returns the IATA codes for airports serving the named
// city. Matching is case-insensitive and exact. Returns nil if the city is
// unknown. Results are sorted alphabetically for determinism.
func ResolveCityToAirports(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	cityAirportsOnce.Do(buildCityAirports)
	codes := cityAirports[strings.ToLower(name)]
	if len(codes) == 0 {
		return nil
	}
	out := make([]string, len(codes))
	copy(out, codes)
	return out
}

// SimilarCities returns known city names within edit distance 2 of name,
// closest first, at most limit of them. Used to suggest a fix when a city is
// not in the airport table (e.g. a misspelling); it cannot help with a city
// the table simply lacks.
func SimilarCities(name string, limit int) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || limit <= 0 {
		return nil
	}
	cityAirportsOnce.Do(buildCityAirports)
	type match struct {
		city string
		dist int
	}
	var matches []match
	for city := range cityAirports {
		if d := EditDistance(name, city); d <= 2 {
			matches = append(matches, match{city, d})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].dist != matches[j].dist {
			return matches[i].dist < matches[j].dist
		}
		return matches[i].city < matches[j].city
	})
	var out []string
	for i := 0; i < len(matches) && i < limit; i++ {
		out = append(out, matches[i].city)
	}
	return out
}

// EditDistance is the Levenshtein distance between a and b, by rune.
func EditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
