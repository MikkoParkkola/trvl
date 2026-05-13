package flights

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// allianceMembership maps airline IATA codes to their alliances.
// Only the most common carriers are listed; unknown airlines are treated as
// non-members.
var allianceMembership = map[string]string{
	// Oneworld
	"AA": "oneworld", // American Airlines
	"BA": "oneworld", // British Airways
	"IB": "oneworld", // Iberia
	"AY": "oneworld", // Finnair
	"QF": "oneworld", // Qantas
	"CX": "oneworld", // Cathay Pacific
	"MH": "oneworld", // Malaysia Airlines
	"JL": "oneworld", // Japan Airlines
	"QR": "oneworld", // Qatar Airways
	"RJ": "oneworld", // Royal Jordanian
	"UL": "oneworld", // SriLankan Airlines
	"S7": "oneworld", // S7 Airlines

	// SkyTeam
	"AF": "skyteam", // Air France
	"KL": "skyteam", // KLM
	"DL": "skyteam", // Delta Air Lines
	"KE": "skyteam", // Korean Air
	"AZ": "skyteam", // ITA Airways (ex-Alitalia)
	"MU": "skyteam", // China Eastern
	"SU": "skyteam", // Aeroflot (suspended)
	"AM": "skyteam", // Aeromexico
	"ME": "skyteam", // Middle East Airlines
	"RO": "skyteam", // TAROM
	"OK": "skyteam", // Czech Airlines
	"SV": "skyteam", // Saudi Arabian Airlines
	"VN": "skyteam", // Vietnam Airlines
	"GA": "skyteam", // Garuda Indonesia
	"XF": "skyteam", // Xiamen Air
	"CI": "skyteam", // China Airlines
	"MF": "skyteam", // Xiamen Airlines

	// Star Alliance
	"UA": "star_alliance", // United Airlines
	"LH": "star_alliance", // Lufthansa
	"AC": "star_alliance", // Air Canada
	"SQ": "star_alliance", // Singapore Airlines
	"TG": "star_alliance", // Thai Airways
	"NH": "star_alliance", // ANA
	"OS": "star_alliance", // Austrian Airlines
	"LO": "star_alliance", // LOT Polish Airlines
	"SK": "star_alliance", // SAS Scandinavian Airlines
	"TP": "star_alliance", // TAP Air Portugal
	"TK": "star_alliance", // Turkish Airlines
	"MS": "star_alliance", // EgyptAir
	"ET": "star_alliance", // Ethiopian Airlines
	"SA": "star_alliance", // South African Airways
	"OZ": "star_alliance", // Asiana Airlines
	"ZH": "star_alliance", // Shenzhen Airlines
	"AI": "star_alliance", // Air India
	"CA": "star_alliance", // Air China
	"NZ": "star_alliance", // Air New Zealand
	"BR": "star_alliance", // EVA Air
	"CM": "star_alliance", // Copa Airlines
	"A3": "star_alliance", // Aegean Airlines
	"LX": "star_alliance", // Swiss International
	"EW": "star_alliance", // Eurowings
}

// allianceBagBenefits maps (normalised alliance name -> normalised tier) to a
// free checked-bag count that the tier grants.
//
// Sources: published alliance benefit guides (2024-2025).
//
//   - Oneworld: Ruby = 0 extra bags; Sapphire/Emerald = 1 extra bag
//   - SkyTeam:  Elite = 1 extra bag; Elite Plus = 1 extra bag
//   - Star Alliance: Silver = 1 extra bag; Gold = 1 extra bag
//
// "Extra bag" means the user gets at least 1 free checked bag even on
// basic/no-bag-included fares on the partner carrier. The function interprets
// this as effective CheckedBagsIncluded >= 1.
var allianceBagBenefits = map[string]map[string]int{
	"oneworld": {
		"ruby":     0,
		"sapphire": 1,
		"emerald":  1,
	},
	"skyteam": {
		"elite":      1,
		"elite_plus": 1,
		"eliteplus":  1,
	},
	"star_alliance": {
		"silver": 1,
		"gold":   1,
	},
}

// AdjustBagAllowance enriches each FlightResult's checked-bag allowance
// based on the user's frequent flyer status.
//
// For each flight:
//   - If the user already has ≥1 checked bag included, no change is made.
//   - Otherwise the function checks whether any of the user's loyalty programs
//     grants a free bag on the flight's operating airline (via alliance membership
//     or a direct carrier match via FrequentFlyerStatus.AirlineCode).
//   - If a benefit applies, CheckedBagsIncluded is set to 1.
//
// The input slice is not mutated; a new slice is returned.
func AdjustBagAllowance(flts []models.FlightResult, programs []preferences.FrequentFlyerStatus) []models.FlightResult {
	if len(programs) == 0 {
		return flts
	}

	out := make([]models.FlightResult, len(flts))
	copy(out, flts)

	for i := range out {
		// Already has a free checked bag — nothing to do.
		if out[i].CheckedBagsIncluded != nil && *out[i].CheckedBagsIncluded >= 1 {
			continue
		}

		// Determine the operating airline for the first leg (the one the
		// bag allowance typically applies to on the outbound flight).
		airlineCode := firstLegAirlineCode(out[i])
		if airlineCode == "" {
			continue
		}

		if freeBagGranted(airlineCode, programs) {
			one := 1
			out[i].CheckedBagsIncluded = &one
		}
	}

	return out
}

// freeBagGranted reports whether any of the user's frequent flyer programs
// grants a free checked bag on the given airline (IATA code).
func freeBagGranted(airlineCode string, programs []preferences.FrequentFlyerStatus) bool {
	code := strings.ToUpper(strings.TrimSpace(airlineCode))

	// Resolve the operating airline's alliance.
	airlineAlliance := strings.ToLower(allianceMembership[code])

	for _, p := range programs {
		alliance := strings.ToLower(strings.TrimSpace(p.Alliance))
		tier := normalizeTier(p.Tier)
		carrierCode := strings.ToUpper(strings.TrimSpace(p.AirlineCode))

		// Direct carrier match: user has status with this exact airline.
		if carrierCode != "" && carrierCode == code {
			if bags := bagBenefit(alliance, tier); bags >= 1 {
				return true
			}
		}

		// Alliance match: the flight's airline is a member of the user's alliance.
		if airlineAlliance != "" && airlineAlliance == alliance {
			if bags := bagBenefit(alliance, tier); bags >= 1 {
				return true
			}
		}
	}

	return false
}

// bagBenefit returns the number of free checked bags granted by the given
// alliance tier. Returns 0 when the tier is unknown or grants no free bags.
func bagBenefit(alliance, tier string) int {
	tiers, ok := allianceBagBenefits[alliance]
	if !ok {
		return 0
	}
	return tiers[tier]
}

// normalizeTier lowercases the tier and replaces hyphens and spaces with
// underscores so "Elite Plus", "elite-plus", "ELITE_PLUS" all map to
// "elite_plus".
func normalizeTier(tier string) string {
	t := strings.ToLower(strings.TrimSpace(tier))
	t = strings.ReplaceAll(t, "-", "_")
	t = strings.ReplaceAll(t, " ", "_")
	return t
}

// firstLegAirlineCode returns the AirlineCode of the first leg, or "" if there
// are no legs.
func firstLegAirlineCode(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(f.Legs[0].AirlineCode))
}
