package hacks

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// LoyaltyProfile is the loyalty-relevant slice of a traveller's profile that
// hack detectors consult. It is intentionally decoupled from the preferences
// package shape so loyalty-aware detectors depend only on this small value;
// map a loaded *preferences.Preferences with LoyaltyFromPreferences.
type LoyaltyProfile struct {
	// Alliances the traveller actively collects status/miles in. Canonical
	// lowercase ids: "star_alliance", "skyteam", "oneworld".
	Alliances []string
	// Airlines is the set of IATA carrier codes the traveller has loyalty with
	// (e.g. "KL", "AY"), uppercased.
	Airlines []string
	// NearStatus is true when the traveller is close to a status threshold and
	// therefore values mileage-earning runs more highly (e.g. a non-zero
	// qualifying-segments-needed snapshot in their loyalty balances).
	NearStatus bool
}

// HasLoyalty reports whether the profile carries any actionable loyalty signal.
// Detectors use it to decide between loyalty-aware filtering and the legacy
// "surface everything" behaviour.
func (p LoyaltyProfile) HasLoyalty() bool {
	return len(p.Alliances) > 0 || len(p.Airlines) > 0
}

// hasAlliance reports whether the profile includes the given canonical alliance
// id (case-insensitive). The empty/zero profile matches nothing.
func (p LoyaltyProfile) hasAlliance(alliance string) bool {
	want := strings.ToLower(strings.TrimSpace(alliance))
	if want == "" {
		return false
	}
	for _, a := range p.Alliances {
		if strings.ToLower(strings.TrimSpace(a)) == want {
			return true
		}
	}
	return false
}

// hasAirline reports whether the profile includes the given IATA carrier code
// (case-insensitive).
func (p LoyaltyProfile) hasAirline(code string) bool {
	want := strings.ToUpper(strings.TrimSpace(code))
	if want == "" {
		return false
	}
	for _, c := range p.Airlines {
		if strings.ToUpper(strings.TrimSpace(c)) == want {
			return true
		}
	}
	return false
}

// LoyaltyFromPreferences projects a loaded preferences profile onto the compact
// LoyaltyProfile that detectors consume. A nil profile yields the zero value,
// which detectors treat as "no loyalty signal" (legacy behaviour).
//
// Alliances come from FrequentFlyerPrograms; airline codes come from both the
// explicit LoyaltyAirlines list and any carrier codes attached to a status
// tier. NearStatus is set when any loyalty balance reports outstanding
// qualifying segments needed for renewal.
func LoyaltyFromPreferences(prefs *preferences.Preferences) LoyaltyProfile {
	if prefs == nil {
		return LoyaltyProfile{}
	}

	var profile LoyaltyProfile
	allianceSeen := make(map[string]struct{})
	airlineSeen := make(map[string]struct{})

	addAlliance := func(raw string) {
		a := strings.ToLower(strings.TrimSpace(raw))
		if a == "" {
			return
		}
		if _, ok := allianceSeen[a]; ok {
			return
		}
		allianceSeen[a] = struct{}{}
		profile.Alliances = append(profile.Alliances, a)
	}
	addAirline := func(raw string) {
		c := strings.ToUpper(strings.TrimSpace(raw))
		if c == "" {
			return
		}
		if _, ok := airlineSeen[c]; ok {
			return
		}
		airlineSeen[c] = struct{}{}
		profile.Airlines = append(profile.Airlines, c)
	}

	for _, code := range prefs.LoyaltyAirlines {
		addAirline(code)
	}
	for _, ff := range prefs.FrequentFlyerPrograms {
		addAlliance(ff.Alliance)
		addAirline(ff.AirlineCode)
	}
	for _, bal := range prefs.LoyaltyBalances {
		if bal.QualSegmentsNeeded > 0 {
			profile.NearStatus = true
		}
	}

	return profile
}
