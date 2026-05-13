// Package match computes how well an offered travel result matches what was requested.
// All scoring is pure (no I/O), deterministic, and safe for concurrent use.
package match

import (
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

// Request describes what was asked for (the user's literal request).
type Request struct {
	OriginIATA       string // e.g. "HEL"
	DestIATA         string // empty = any
	DepartDateCenter string // YYYY-MM-DD, desired depart date
	FlexDays         int    // allowed drift either side (0 = exact)
	DateWindowDays   int    // total search window in days (≥ 0); informational, used by discover
	Nights           int    // desired trip length; 0 = any
	MaxNightsDrift   int    // allowed drift from Nights; 0 means exact
	PreferDirect     bool
	AcceptedAirports []string // non-empty = alternatives accepted with reduced penalty
	Currency         string
	GuestCount       int
}

// Offered describes what is actually being offered.
type Offered struct {
	OriginIATA string
	DestIATA   string
	DepartDate string // YYYY-MM-DD
	ReturnDate string // YYYY-MM-DD; "" means one-way
	Stops      int
	Currency   string
	GuestCount int
}

// Score holds the result of a RequestMatch computation.
type Score struct {
	Total  int    // 0-100
	Reason string // dominant penalty axis
}

// Compute returns a RequestMatch score for an offered result against a request.
// 100 = exact match to literal ask.
// 0   = completely incompatible.
func Compute(req Request, off Offered) Score {
	penalties := map[string]int{
		"date_drift":           dateDriftPenalty(req, off),
		"airport_substitution": airportSubPenalty(req, off),
		"nights_drift":         nightsDriftPenalty(req, off),
		"currency_mismatch":    currencyMismatchPenalty(req, off),
		"direct_preference":    directPrefPenalty(req, off),
	}

	total := 0
	for _, p := range penalties {
		total += p
	}
	if total > 100 {
		total = 100
	}

	score := 100 - total
	if score < 0 {
		score = 0
	}

	reason := dominantAxis(penalties)
	return Score{Total: score, Reason: reason}
}

// dateDriftPenalty applies 5 pts/day outside [center ± flexDays]. Max 40.
func dateDriftPenalty(req Request, off Offered) int {
	if req.DepartDateCenter == "" || off.DepartDate == "" {
		return 0
	}
	center, err1 := time.Parse(dateLayout, req.DepartDateCenter)
	offered, err2 := time.Parse(dateLayout, off.DepartDate)
	if err1 != nil || err2 != nil {
		return 0
	}

	diff := int(offered.Sub(center).Hours() / 24)
	if diff < 0 {
		diff = -diff
	}
	drift := diff - req.FlexDays
	if drift <= 0 {
		return 0
	}
	penalty := drift * 5
	if penalty > 40 {
		penalty = 40
	}
	return penalty
}

// airportSubPenalty: 0 if dest matches or req.DestIATA is empty;
// 20 if not in AcceptedAirports; 10 if an accepted substitute.
func airportSubPenalty(req Request, off Offered) int {
	if req.DestIATA == "" || req.DestIATA == off.DestIATA {
		return 0
	}
	for _, a := range req.AcceptedAirports {
		if a == off.DestIATA {
			return 10
		}
	}
	return 20
}

// nightsDriftPenalty: |offered_nights - req.Nights| × 5; only when Nights > 0
// and drift exceeds MaxNightsDrift. Max 20.
func nightsDriftPenalty(req Request, off Offered) int {
	if req.Nights <= 0 {
		return 0
	}
	offeredNights := offeredNightCount(off)
	if offeredNights < 0 {
		return 0
	}
	drift := offeredNights - req.Nights
	if drift < 0 {
		drift = -drift
	}
	if drift < req.MaxNightsDrift {
		return 0
	}
	penalty := drift * 5
	if penalty > 20 {
		penalty = 20
	}
	return penalty
}

// offeredNightCount returns the number of nights implied by DepartDate/ReturnDate.
// Returns -1 if it cannot be determined.
func offeredNightCount(off Offered) int {
	if off.DepartDate == "" || off.ReturnDate == "" {
		return -1
	}
	dep, err1 := time.Parse(dateLayout, off.DepartDate)
	ret, err2 := time.Parse(dateLayout, off.ReturnDate)
	if err1 != nil || err2 != nil {
		return -1
	}
	nights := int(ret.Sub(dep).Hours() / 24)
	if nights < 0 {
		return 0
	}
	return nights
}

// currencyMismatchPenalty: 10 pts if currencies differ and both non-empty.
func currencyMismatchPenalty(req Request, off Offered) int {
	if req.Currency == "" || off.Currency == "" {
		return 0
	}
	if req.Currency != off.Currency {
		return 10
	}
	return 0
}

// directPrefPenalty: 10 pts if PreferDirect=true and offered has stops.
func directPrefPenalty(req Request, off Offered) int {
	if req.PreferDirect && off.Stops > 0 {
		return 10
	}
	return 0
}

// dominantAxis returns the name of the penalty axis with the highest value.
// Returns "exact_match" if all are zero.
func dominantAxis(penalties map[string]int) string {
	best := ""
	max := 0
	// Deterministic order for ties.
	axes := []string{"date_drift", "airport_substitution", "nights_drift", "currency_mismatch", "direct_preference"}
	for _, axis := range axes {
		v := penalties[axis]
		if v > max {
			max = v
			best = axis
		}
	}
	if best == "" {
		return "exact_match"
	}
	return best
}

// FormatScore returns a human-readable description of a Score.
func FormatScore(s Score) string {
	return fmt.Sprintf("score=%d reason=%s", s.Total, s.Reason)
}
