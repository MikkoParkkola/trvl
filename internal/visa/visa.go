// Package visa provides visa and entry requirement lookups for passport→destination pairs.
// Uses a curated static dataset of common visa-free arrangements, visa-on-arrival,
// and e-visa policies. Data sourced from IATA Timatic and government travel advisories.
package visa

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Requirement describes the visa/entry requirement for a passport→destination pair.
type Requirement struct {
	Passport    string `json:"passport"`    // ISO 3166-1 alpha-2 (e.g. "FI")
	Destination string `json:"destination"` // ISO 3166-1 alpha-2 (e.g. "JP")
	Status      string `json:"status"`      // "visa-free", "visa-required", "visa-on-arrival", "e-visa", "freedom-of-movement"
	MaxStay     string `json:"max_stay"`    // e.g. "90 days", "30 days", "unlimited"
	Notes       string `json:"notes"`       // additional info
}

// Result is the output of a visa requirement lookup.
type Result struct {
	Success     bool        `json:"success"`
	Requirement Requirement `json:"requirement"`
	Error       string      `json:"error,omitempty"`
}

// Lookup returns the visa requirement for the given passport and destination country codes.
func Lookup(passport, destination string) Result {
	passport = strings.ToUpper(strings.TrimSpace(passport))
	destination = strings.ToUpper(strings.TrimSpace(destination))

	if passport == "" || destination == "" {
		return Result{Error: "both passport and destination country codes are required"}
	}

	if _, ok := models.CountryNames[passport]; !ok {
		return Result{Error: fmt.Sprintf("unknown passport country code %q", passport)}
	}
	if _, ok := models.CountryNames[destination]; !ok {
		return Result{Error: fmt.Sprintf("unknown destination country code %q", destination)}
	}

	if passport == destination {
		return Result{
			Success: true,
			Requirement: Requirement{
				Passport:    passport,
				Destination: destination,
				Status:      "freedom-of-movement",
				MaxStay:     "unlimited",
				Notes:       "Citizens have unrestricted access to their own country.",
			},
		}
	}

	// Check EU/EEA/Schengen freedom of movement.
	if euCountries[passport] && euCountries[destination] {
		return Result{
			Success: true,
			Requirement: Requirement{
				Passport:    passport,
				Destination: destination,
				Status:      "freedom-of-movement",
				MaxStay:     "unlimited",
				Notes:       "EU/EEA freedom of movement — no visa required, right to live and work.",
			},
		}
	}

	// Check specific bilateral agreements.
	key := passport + "→" + destination
	if req, ok := bilateral[key]; ok {
		return Result{Success: true, Requirement: req}
	}

	// Check passport group → destination rules.
	for _, rule := range groupRules {
		if rule.passports[passport] && rule.destinations[destination] {
			return Result{
				Success: true,
				Requirement: Requirement{
					Passport:    passport,
					Destination: destination,
					Status:      rule.status,
					MaxStay:     rule.maxStay,
					Notes:       rule.notes,
				},
			}
		}
	}

	// Default: visa required.
	return Result{
		Success: true,
		Requirement: Requirement{
			Passport:    passport,
			Destination: destination,
			Status:      "visa-required",
			MaxStay:     "",
			Notes:       "Visa likely required. Check the destination country's embassy for current requirements.",
		},
	}
}

// CountryName returns the display name for an ISO country code.
func CountryName(code string) string {
	if name, ok := models.CountryNames[strings.ToUpper(code)]; ok {
		return name
	}
	return code
}

// ListCountries returns all known country codes sorted alphabetically.
func ListCountries() []string {
	codes := make([]string, 0, len(models.CountryNames))
	for code := range models.CountryNames {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// StatusEmoji returns a display emoji for a visa status.
func StatusEmoji(status string) string {
	switch status {
	case "visa-free", "freedom-of-movement":
		return "✅"
	case "visa-on-arrival":
		return "🟡"
	case "e-visa":
		return "🟠"
	case "visa-required":
		return "🔴"
	default:
		return "❓"
	}
}

// --- Static data ---

// EU/EEA/Schengen countries with freedom of movement.
var euCountries = map[string]bool{
	"AT": true, "BE": true, "BG": true, "HR": true, "CY": true,
	"CZ": true, "DK": true, "EE": true, "FI": true, "FR": true,
	"DE": true, "GR": true, "HU": true, "IE": true, "IT": true,
	"LV": true, "LT": true, "LU": true, "MT": true, "NL": true,
	"PL": true, "PT": true, "RO": true, "SK": true, "SI": true,
	"ES": true, "SE": true,
	// EEA
	"IS": true, "LI": true, "NO": true,
	// Schengen-associated
	"CH": true,
}

// Strong passport countries (visa-free to many destinations).
var strongPassports = map[string]bool{
	"FI": true, "SE": true, "NO": true, "DK": true, "DE": true,
	"FR": true, "NL": true, "IT": true, "ES": true, "PT": true,
	"AT": true, "BE": true, "LU": true, "IE": true, "CH": true,
	"GB": true, "US": true, "CA": true, "AU": true, "NZ": true,
	"JP": true, "KR": true, "SG": true,
}

type groupRule struct {
	passports    map[string]bool
	destinations map[string]bool
	status       string
	maxStay      string
	notes        string
}

var groupRules = []groupRule{
	// Strong passports → popular tourist destinations (visa-free).
	{
		passports: strongPassports,
		destinations: map[string]bool{
			"JP": true, "KR": true, "SG": true, "TH": true, "MY": true,
			"MX": true, "BR": true, "AR": true, "CL": true, "CO": true,
			"PE": true, "CR": true, "PA": true, "IL": true, "MA": true,
			"ZA": true, "GE": true, "UA": true, "RS": true, "ME": true,
			"MK": true, "AL": true, "BA": true, "TR": true, "TW": true,
			"HK": true, "PH": true,
		},
		status:  "visa-free",
		maxStay: "90 days",
		notes:   "Tourist visa waiver — 90-day stay without visa for most strong passport holders.",
	},
	// US/CA/AU/NZ/GB → EU/Schengen (visa-free 90 days).
	{
		passports: map[string]bool{"US": true, "CA": true, "AU": true, "NZ": true, "GB": true},
		destinations: map[string]bool{
			"AT": true, "BE": true, "BG": true, "HR": true, "CY": true,
			"CZ": true, "DK": true, "EE": true, "FI": true, "FR": true,
			"DE": true, "GR": true, "HU": true, "IE": true, "IT": true,
			"LV": true, "LT": true, "LU": true, "MT": true, "NL": true,
			"PL": true, "PT": true, "RO": true, "SK": true, "SI": true,
			"ES": true, "SE": true, "IS": true, "NO": true, "CH": true,
		},
		status:  "visa-free",
		maxStay: "90 days in 180-day period",
		notes:   "Schengen area visa-free entry. ETIAS pre-authorization may be required from 2026.",
	},
	// Strong passports → visa-on-arrival destinations.
	{
		passports: strongPassports,
		destinations: map[string]bool{
			"ID": true, "KH": true, "LA": true, "MM": true, "NP": true,
			"LK": true, "MV": true, "JO": true, "EG": true, "ET": true,
			"KE": true, "TZ": true, "MG": true, "MZ": true, "RW": true,
		},
		status:  "visa-on-arrival",
		maxStay: "30 days",
		notes:   "Visa issued at port of entry. Fee typically $20-50 USD. Passport must be valid 6+ months.",
	},
	// Strong passports → e-visa destinations.
	{
		passports: strongPassports,
		destinations: map[string]bool{
			"IN": true, "VN": true, "AZ": true, "UZ": true, "OM": true,
			"SA": true, "AE": true, "QA": true, "BH": true, "KW": true,
		},
		status:  "e-visa",
		maxStay: "30 days",
		notes:   "Electronic visa required — apply online before travel. Processing typically 3-5 business days.",
	},
}

// bilateral holds specific passport→destination overrides.
var bilateral = map[string]Requirement{
	// US specifics
	"US→JP": {Passport: "US", Destination: "JP", Status: "visa-free", MaxStay: "90 days", Notes: "Reciprocal visa waiver. Business and tourism."},
	"US→GB": {Passport: "US", Destination: "GB", Status: "visa-free", MaxStay: "6 months", Notes: "Standard visitor visa waiver. No work permitted."},
	"US→AU": {Passport: "US", Destination: "AU", Status: "e-visa", MaxStay: "90 days", Notes: "ETA (Electronic Travel Authority) required. Apply online, usually approved instantly."},
	"US→NZ": {Passport: "US", Destination: "NZ", Status: "e-visa", MaxStay: "90 days", Notes: "NZeTA required. Apply via app or online ($12-23 NZD + $35 IVL)."},
	"US→CN": {Passport: "US", Destination: "CN", Status: "visa-required", MaxStay: "varies", Notes: "Visa required from Chinese embassy/consulate. 10-year multiple-entry available."},
	"US→RU": {Passport: "US", Destination: "RU", Status: "visa-required", MaxStay: "varies", Notes: "Visa required. Apply via embassy. 3-year multiple-entry tourist visa available."},
	"US→CU": {Passport: "US", Destination: "CU", Status: "visa-required", MaxStay: "30 days", Notes: "Tourist card required. US citizens must travel under specific OFAC license categories."},

	// FI specifics
	"FI→JP": {Passport: "FI", Destination: "JP", Status: "visa-free", MaxStay: "90 days", Notes: "Reciprocal visa waiver for tourism and business."},
	"FI→US": {Passport: "FI", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program. Apply online ($21). Valid 2 years."},
	"FI→AU": {Passport: "FI", Destination: "AU", Status: "e-visa", MaxStay: "90 days", Notes: "ETA (Electronic Travel Authority) required."},
	"FI→NZ": {Passport: "FI", Destination: "NZ", Status: "e-visa", MaxStay: "90 days", Notes: "NZeTA required ($12-23 NZD + $35 IVL)."},
	"FI→CN": {Passport: "FI", Destination: "CN", Status: "visa-required", MaxStay: "varies", Notes: "Visa required. 15-day transit visa-free for some entry points."},
	"FI→RU": {Passport: "FI", Destination: "RU", Status: "visa-required", MaxStay: "varies", Notes: "Visa required. Apply via embassy or e-visa for specific regions."},

	// GB specifics
	"GB→US": {Passport: "GB", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program ($21). Valid 2 years."},
	"GB→AU": {Passport: "GB", Destination: "AU", Status: "e-visa", MaxStay: "90 days", Notes: "ETA required."},
	"GB→JP": {Passport: "GB", Destination: "JP", Status: "visa-free", MaxStay: "90 days", Notes: "Reciprocal visa waiver."},
	"GB→CN": {Passport: "GB", Destination: "CN", Status: "visa-required", MaxStay: "varies", Notes: "Visa required from embassy/consulate."},

	// JP specifics
	"JP→US": {Passport: "JP", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"JP→AU": {Passport: "JP", Destination: "AU", Status: "e-visa", MaxStay: "90 days", Notes: "ETA required."},

	// AU/NZ specifics
	"AU→NZ": {Passport: "AU", Destination: "NZ", Status: "visa-free", MaxStay: "unlimited", Notes: "Trans-Tasman Travel Arrangement. Right to live and work."},
	"NZ→AU": {Passport: "NZ", Destination: "AU", Status: "visa-free", MaxStay: "unlimited", Notes: "Trans-Tasman Travel Arrangement. Right to live and work."},

	// Schengen → US (ESTA)
	"DE→US": {Passport: "DE", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"FR→US": {Passport: "FR", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"NL→US": {Passport: "NL", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"IT→US": {Passport: "IT", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"ES→US": {Passport: "ES", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"SE→US": {Passport: "SE", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"NO→US": {Passport: "NO", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"DK→US": {Passport: "DK", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
	"CH→US": {Passport: "CH", Destination: "US", Status: "e-visa", MaxStay: "90 days", Notes: "ESTA required under Visa Waiver Program."},
}
