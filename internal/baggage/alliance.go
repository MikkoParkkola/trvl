// Package baggage — alliance membership and FF status baggage benefit resolution.
package baggage

import (
	"fmt"
	"strings"
)

// allianceMembers maps alliance name to member airline IATA codes.
var allianceMembers = map[string][]string{
	"skyteam": {
		"KL", // KLM
		"AF", // Air France
		"DL", // Delta
		"KE", // Korean Air
		"VN", // Vietnam Airlines
		"GA", // Garuda Indonesia
		"MU", // China Eastern
		"CI", // China Airlines
		"CZ", // China Southern
		"ME", // MEA
		"AR", // Aerolíneas Argentinas
		"SV", // Saudia
		"OK", // Czech Airlines
		"RO", // TAROM
		"KQ", // Kenya Airways
		"UX", // Air Europa
		"AZ", // ITA Airways
		"SU", // Aeroflot
	},
	"oneworld": {
		"BA", // British Airways
		"IB", // Iberia
		"AY", // Finnair
		"JL", // JAL
		"QF", // Qantas
		"QR", // Qatar Airways
		"AA", // American Airlines
		"CX", // Cathay Pacific
		"MH", // Malaysia Airlines
		"RJ", // Royal Jordanian
		"S7", // S7 Airlines
		"AT", // Royal Air Maroc
		"UL", // SriLankan Airlines
		"FJ", // Fiji Airways
	},
	"star_alliance": {
		"LH", // Lufthansa
		"LX", // Swiss
		"SK", // SAS
		"TK", // Turkish Airlines
		"NH", // ANA
		"UA", // United Airlines
		"AC", // Air Canada
		"SQ", // Singapore Airlines
		"ET", // Ethiopian Airlines
		"TP", // TAP Portugal
		"MS", // EgyptAir
		"OS", // Austrian Airlines
		"SN", // Brussels Airlines
		"LO", // LOT Polish
		"CA", // Air China
		"AI", // Air India
		"OZ", // Asiana Airlines
		"NZ", // Air New Zealand
		"SA", // South African Airways
		"TG", // Thai Airways
	},
}

// BagBenefit describes the baggage benefit from an FF status tier.
type BagBenefit struct {
	ExtraCheckedBags int     // additional checked bags beyond ticket inclusion
	CheckedWeightKg  float64 // weight per bag (23 or 32)
	FreeCarryOn      bool    // overhead carry-on guaranteed (overrides LCC restrictions)
	PriorityBoarding bool
	LoungeAccess     bool
}

// allianceTierBenefits maps alliance→tier→benefit.
var allianceTierBenefits = map[string]map[string]BagBenefit{
	"skyteam": {
		"elite": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  23,
			FreeCarryOn:      true,
			PriorityBoarding: true,
		},
		"elite_plus": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  32,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
		// Flying Blue tier aliases → SkyTeam equivalents
		"silver": { // Flying Blue Silver = SkyTeam Elite
			ExtraCheckedBags: 1,
			CheckedWeightKg:  23,
			FreeCarryOn:      true,
			PriorityBoarding: true,
		},
		"gold": { // Flying Blue Gold = SkyTeam Elite Plus
			ExtraCheckedBags: 1,
			CheckedWeightKg:  32,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
		"platinum": { // Flying Blue Platinum = SkyTeam Elite Plus
			ExtraCheckedBags: 1,
			CheckedWeightKg:  32,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
	},
	"oneworld": {
		"ruby": {
			ExtraCheckedBags: 0,
			FreeCarryOn:      true,
			PriorityBoarding: true,
		},
		"sapphire": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  23,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
		"emerald": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  32,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
		// Airline-tier aliases: many oneworld carriers label tiers Bronze/Silver/
		// Gold (e.g. Finnair Plus, BA Executive Club), mapping to Ruby/Sapphire/
		// Emerald. Silver (Sapphire) and above grant a free checked bag.
		"bronze": {
			ExtraCheckedBags: 0,
			FreeCarryOn:      true,
			PriorityBoarding: true,
		},
		"silver": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  23,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
		"gold": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  32,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
	},
	"star_alliance": {
		"silver": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  23,
			FreeCarryOn:      true,
			PriorityBoarding: true,
		},
		"gold": {
			ExtraCheckedBags: 1,
			CheckedWeightKg:  32,
			FreeCarryOn:      true,
			PriorityBoarding: true,
			LoungeAccess:     true,
		},
	},
}

// IsAllianceMember reports whether the given airline IATA code belongs to the
// named alliance.
func IsAllianceMember(airlineCode, alliance string) bool {
	members, ok := allianceMembers[strings.ToLower(alliance)]
	if !ok {
		return false
	}
	code := strings.ToUpper(airlineCode)
	for _, m := range members {
		if m == code {
			return true
		}
	}
	return false
}

// AllianceForAirline returns the alliance name for a given airline, or "" if
// the airline is not in any alliance (e.g., LCCs).
func AllianceForAirline(airlineCode string) string {
	code := strings.ToUpper(airlineCode)
	for alliance, members := range allianceMembers {
		for _, m := range members {
			if m == code {
				return alliance
			}
		}
	}
	return ""
}

// ResolveBagBenefit returns the baggage benefit for a given alliance+tier.
// Returns a zero BagBenefit if the alliance/tier is unknown.
func ResolveBagBenefit(alliance, tier string) BagBenefit {
	tiers, ok := allianceTierBenefits[strings.ToLower(alliance)]
	if !ok {
		return BagBenefit{}
	}
	benefit, ok := tiers[strings.ToLower(tier)]
	if !ok {
		return BagBenefit{}
	}
	return benefit
}

// FFStatus represents a single FF programme membership. A traveller may hold
// status in multiple alliances (e.g., SkyTeam Gold via Flying Blue AND
// Oneworld Sapphire via Royal Jordanian).
type FFStatus struct {
	Alliance string // "skyteam", "oneworld", "star_alliance"
	Tier     string // "gold", "sapphire", "silver", "elite_plus", etc.
}

// AllInCost computes the total flight cost including baggage fees, adjusted
// for the traveller's FF status benefits across all their programmes.
//
// A user with SkyTeam Gold + Oneworld Sapphire gets free bags on both
// KLM (SkyTeam) and Finnair (Oneworld). The function finds the best
// matching status for the given airline's alliance.
func AllInCost(baseFare float64, airlineCode string, needCheckedBag, needCarryOn bool, ffStatuses []FFStatus) (float64, string) {
	if baseFare <= 0 {
		return 0, ""
	}

	ab, hasRules := Get(airlineCode)
	if !hasRules {
		return baseFare, ""
	}

	total := baseFare
	var extras []string

	benefit := bestBenefitForAirline(airlineCode, ffStatuses)

	if needCarryOn && ab.OverheadOnly && !benefit.FreeCarryOn {
		total += 15
		extras = append(extras, "+€15 carry-on")
	}

	if needCheckedBag {
		freeChecked := ab.CheckedIncluded + benefit.ExtraCheckedBags
		if freeChecked <= 0 && ab.CheckedFee > 0 {
			total += ab.CheckedFee
			extras = append(extras, fmt.Sprintf("+€%.0f checked bag", ab.CheckedFee))
		}
		if freeChecked > 1 && benefit.ExtraCheckedBags > 0 {
			extras = append(extras, "extra bag free (FF status)")
		}
	}

	if len(extras) == 0 {
		if benefit.ExtraCheckedBags > 0 {
			return total, "bags included + FF extra bag"
		}
		return total, "bags included"
	}

	return total, strings.Join(extras, ", ")
}

// bestBenefitForAirline finds the best baggage benefit from any of the user's
// FF statuses that applies to the given airline's alliance.
func bestBenefitForAirline(airlineCode string, statuses []FFStatus) BagBenefit {
	airlineAlliance := AllianceForAirline(airlineCode)
	if airlineAlliance == "" {
		return BagBenefit{}
	}
	best := BagBenefit{}
	for _, s := range statuses {
		if !strings.EqualFold(s.Alliance, airlineAlliance) {
			continue
		}
		b := ResolveBagBenefit(s.Alliance, s.Tier)
		if b.ExtraCheckedBags > best.ExtraCheckedBags || (b.FreeCarryOn && !best.FreeCarryOn) {
			best = b
		}
	}
	return best
}

// FFStatusesFromPrefs converts preferences.FrequentFlyerStatus entries into
// the baggage package's FFStatus type for AllInCost lookups.
func FFStatusesFromPrefs(prefs []struct{ Alliance, Tier string }) []FFStatus {
	out := make([]FFStatus, len(prefs))
	for i, p := range prefs {
		out[i] = FFStatus{Alliance: p.Alliance, Tier: p.Tier}
	}
	return out
}

// AllianceMembers returns the IATA codes for all member airlines of the given alliance.
func AllianceMembers(alliance string) []string {
	members, ok := allianceMembers[strings.ToLower(alliance)]
	if !ok {
		return nil
	}
	result := make([]string, len(members))
	copy(result, members)
	return result
}

// HasFFBenefitForAirline reports whether any of the user's FF statuses
// provides baggage benefits on the given airline.
func HasFFBenefitForAirline(airlineCode string, statuses []FFStatus) bool {
	b := bestBenefitForAirline(airlineCode, statuses)
	return b.ExtraCheckedBags > 0 || b.FreeCarryOn
}
