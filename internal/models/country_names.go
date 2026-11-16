package models

import "strings"

// CountryNames maps ISO 3166-1 alpha-2 codes to country names.
var CountryNames = map[string]string{
	"AL": "Albania", "AR": "Argentina", "AT": "Austria", "AU": "Australia",
	"AZ": "Azerbaijan", "BA": "Bosnia and Herzegovina", "BE": "Belgium",
	"BG": "Bulgaria", "BH": "Bahrain", "BR": "Brazil", "CA": "Canada",
	"CH": "Switzerland", "CL": "Chile", "CN": "China", "CO": "Colombia",
	"CR": "Costa Rica", "CU": "Cuba", "CY": "Cyprus", "CZ": "Czech Republic",
	"DE": "Germany", "DK": "Denmark", "EE": "Estonia", "EG": "Egypt",
	"ES": "Spain", "ET": "Ethiopia", "FI": "Finland", "FR": "France",
	"GB": "United Kingdom", "GE": "Georgia", "GR": "Greece", "HK": "Hong Kong",
	"HR": "Croatia", "HU": "Hungary", "ID": "Indonesia", "IE": "Ireland",
	"IL": "Israel", "IN": "India", "IS": "Iceland", "IT": "Italy",
	"JO": "Jordan", "JP": "Japan", "KE": "Kenya", "KH": "Cambodia",
	"KR": "South Korea", "KW": "Kuwait", "LA": "Laos", "LI": "Liechtenstein",
	"LK": "Sri Lanka", "LT": "Lithuania", "LU": "Luxembourg", "LV": "Latvia",
	"MA": "Morocco", "ME": "Montenegro", "MG": "Madagascar", "MK": "North Macedonia",
	"MM": "Myanmar", "MT": "Malta", "MV": "Maldives", "MX": "Mexico",
	"MY": "Malaysia", "MZ": "Mozambique", "NL": "Netherlands", "NO": "Norway",
	"NP": "Nepal", "NZ": "New Zealand", "OM": "Oman", "PA": "Panama",
	"PE": "Peru", "PH": "Philippines", "PL": "Poland", "PT": "Portugal",
	"QA": "Qatar", "RO": "Romania", "RS": "Serbia", "RU": "Russia",
	"RW": "Rwanda", "SA": "Saudi Arabia", "SE": "Sweden", "SG": "Singapore",
	"SI": "Slovenia", "SK": "Slovakia", "TH": "Thailand", "TR": "Turkey",
	"TW": "Taiwan", "TZ": "Tanzania", "UA": "Ukraine", "AE": "United Arab Emirates",
	"US": "United States", "UZ": "Uzbekistan", "VN": "Vietnam", "ZA": "South Africa",
}

// localCountryNames adds the local names (and Czechia) of the countries
// whose cities are in CityAliases, so "1010 Wien, Österreich" reads as
// city-only like "1010 Wien, Austria".
var localCountryNames = map[string]string{
	"AT": "Österreich", "CZ": "Česko", "DE": "Deutschland", "DK": "Danmark",
	"PL": "Polska", "RO": "România", "SE": "Sverige",
}

// IsCountryName reports whether s names a country in CountryNames, in its
// local form from localCountryNames, or as "Czechia", ignoring case.
func IsCountryName(s string) bool {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "Czechia") {
		return true
	}
	for code, name := range CountryNames {
		if strings.EqualFold(s, name) || strings.EqualFold(s, localCountryNames[code]) {
			return true
		}
	}
	return false
}
