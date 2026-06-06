package travelctx

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// countryPrimaryAirport maps an ISO 3166-1 alpha-2 country code to that
// country's primary international gateway IATA code. This is a coarse
// last-resort fallback: used only when geo-IP gives us a country but the city
// could not be mapped to an airport. It covers the countries trvl's airport
// table already knows; unknown countries simply yield "" and the caller
// degrades to "no origin resolved".
//
// Scope is deliberately the busiest hub per country (the one a traveler is
// most likely to actually depart from), not every airport. Kept in sync by
// hand with models.AirportNames.
var countryPrimaryAirport = map[string]string{
	"FI": "HEL", "GB": "LHR", "FR": "CDG", "NL": "AMS", "DE": "FRA",
	"ES": "MAD", "IT": "FCO", "CH": "ZRH", "AT": "VIE", "BE": "BRU",
	"DK": "CPH", "NO": "OSL", "SE": "ARN", "IE": "DUB", "PT": "LIS",
	"GR": "ATH", "PL": "WAW", "CZ": "PRG", "HU": "BUD", "RO": "OTP",
	"BG": "SOF", "TR": "IST", "HR": "ZAG", "RS": "BEG", "EE": "TLL",
	"LV": "RIX", "LT": "VNO", "IS": "KEF",
	"US": "JFK", "CA": "YYZ", "MX": "MEX",
	"JP": "HND", "KR": "ICN", "CN": "PEK", "HK": "HKG", "TW": "TPE",
	"SG": "SIN", "TH": "BKK", "MY": "KUL", "ID": "CGK", "PH": "MNL",
	"VN": "SGN", "IN": "DEL", "LK": "CMB", "NP": "KTM", "KH": "PNH",
	"MM": "RGN",
	"AE": "DXB", "QA": "DOH", "SA": "RUH", "IL": "TLV", "JO": "AMM",
	"BH": "BAH", "OM": "MCT", "KW": "KWI",
	"ZA": "JNB", "KE": "NBO", "EG": "CAI", "MA": "CMN", "ET": "ADD",
	"NG": "LOS", "GH": "ACC", "SN": "DSS", "TN": "TUN",
	"AU": "SYD", "NZ": "AKL", "FJ": "NAN",
	"BR": "GRU", "AR": "EZE", "CL": "SCL", "CO": "BOG", "PE": "LIM",
	"EC": "UIO", "VE": "CCS", "UY": "MVD", "PA": "PTY", "CR": "SJO",
	"CU": "HAV", "DO": "SDQ", "JM": "MBJ",
}

// airportForLocation maps a resolved (city, country) pair to a best-guess
// origin airport IATA code. It tries, in order:
//
//  1. City name → airports serving it (models.ResolveCityToAirports), taking
//     the alphabetically-first for determinism. This reuses trvl's existing
//     243-airport table, so no new data to maintain for cities.
//  2. Country code → that country's primary hub (countryPrimaryAirport).
//
// Returns "" when neither yields a match — the caller treats that as
// "origin unresolved" and falls back to requiring an explicit argument.
func airportForLocation(city, country string) string {
	if c := strings.TrimSpace(city); c != "" {
		if codes := models.ResolveCityToAirports(c); len(codes) > 0 {
			return codes[0] // sorted; deterministic
		}
	}
	if cc := strings.ToUpper(strings.TrimSpace(country)); cc != "" {
		if code, ok := countryPrimaryAirport[cc]; ok {
			return code
		}
	}
	return ""
}
