// Package watch — opportunity.go provides rolling-window opportunity scoring.
package watch

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dealquality"
	"github.com/MikkoParkkola/trvl/internal/match"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/scoring"
)

const windowDateLayout = "2006-01-02"

// ResolveWindowDates interprets window_from/window_to using "next_Nd" syntax.
// Returns (from, to time.Time, error).
// "next_30d" from now → (today, today+30d).
// YYYY-MM-DD → parsed as literal date.
func ResolveWindowDates(from, to string, now time.Time) (time.Time, time.Time, error) {
	fromT, err := resolveWindowDate(from, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("window_from: %w", err)
	}
	toT, err := resolveWindowDate(to, now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("window_to: %w", err)
	}
	if fromT.After(toT) {
		return time.Time{}, time.Time{}, fmt.Errorf("window_from must be before window_to")
	}
	return fromT, toT, nil
}

// resolveWindowDate parses a single window boundary.
func resolveWindowDate(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return now, nil
	}
	if strings.HasPrefix(s, "next_") && strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSuffix(s, "d"), "next_"))
		if err != nil || days < 0 {
			return time.Time{}, fmt.Errorf("invalid next_Nd format: %q", s)
		}
		return now.AddDate(0, 0, days), nil
	}
	t, err := time.Parse(windowDateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected YYYY-MM-DD or next_Nd, got %q", s)
	}
	return t, nil
}

// ResolveFavourites returns the effective favourites list for an opportunity watch.
// If w.Favourites is non-empty, returns it as-is.
// Otherwise: union of prefs.BucketList + prefs.PreviousTrips, intersect with
// keys where prefs.AirportAffinity[code] >= 0.3. Falls back to prefs.BucketList
// if AirportAffinity is empty.
func ResolveFavourites(w Watch, prefs *preferences.Preferences) []string {
	if len(w.Favourites) > 0 {
		return w.Favourites
	}
	if prefs == nil {
		return nil
	}

	// Union of BucketList and PreviousTrips.
	seen := map[string]bool{}
	var candidates []string
	for _, code := range prefs.BucketList {
		if !seen[code] {
			seen[code] = true
			candidates = append(candidates, code)
		}
	}
	for _, code := range prefs.PreviousTrips {
		if !seen[code] {
			seen[code] = true
			candidates = append(candidates, code)
		}
	}

	if len(prefs.AirportAffinity) == 0 {
		// Fall back to BucketList only.
		return prefs.BucketList
	}

	// Intersect with codes having affinity >= 0.3.
	var result []string
	for _, code := range candidates {
		if aff, ok := prefs.AirportAffinity[code]; ok && aff >= 0.3 {
			result = append(result, code)
		}
	}
	return result
}

// OpportunityScore is the composite score for one (destination, depart, return) tuple.
type OpportunityScore struct {
	Destination  string
	DepartDate   string
	ReturnDate   string
	Nights       int
	OverallScore int // 0-100: 0.4*ProfileMatch + 0.2*RequestMatch + 0.4*DealQuality
	ProfileMatch int
	RequestMatch int
	DealQuality  int
	Reason       string
}

// ScoreOpportunity computes the composite opportunity score for one tuple.
// pricePerNight is the estimated price (use 0 if unknown — DealQuality returns 50).
// dqSamples are the historical samples for the route+season from DealQuality store.
func ScoreOpportunity(
	dest, departDate, returnDate string,
	prefs *preferences.Preferences,
	dqSamples []dealquality.Sample,
	price float64,
) OpportunityScore {
	nights := nightsBetween(departDate, returnDate)

	// 1. ProfileMatch using existing scoring package.
	pmScore, _ := scoring.ComputeProfileMatch(prefs, scoring.DiscoverInput{
		AirportCode: dest,
		FlightPrice: price,
		Total:       price,
		Budget:      prefs.BudgetFlightMax,
	})

	// 2. RequestMatch — score how well the offer fits a generic "any dest" request.
	rmScore := match.Compute(match.Request{
		DestIATA:       dest,
		Nights:         nights,
		MaxNightsDrift: 2,
	}, match.Offered{
		DestIATA:   dest,
		DepartDate: departDate,
		ReturnDate: returnDate,
	})

	// 3. DealQuality.
	dqScore := dealquality.ScoreAgainst(price, dqSamples)

	// Composite: 0.4*PM + 0.2*RM + 0.4*DQ
	overall := int(0.4*float64(pmScore) + 0.2*float64(rmScore.Total) + 0.4*float64(dqScore.Total))
	if overall > 100 {
		overall = 100
	}
	if overall < 0 {
		overall = 0
	}

	reason := dqScore.Reason
	if reason == "" {
		reason = rmScore.Reason
	}

	return OpportunityScore{
		Destination:  dest,
		DepartDate:   departDate,
		ReturnDate:   returnDate,
		Nights:       nights,
		OverallScore: overall,
		ProfileMatch: pmScore,
		RequestMatch: rmScore.Total,
		DealQuality:  dqScore.Total,
		Reason:       reason,
	}
}

// nightsBetween returns the number of nights between two YYYY-MM-DD dates.
func nightsBetween(from, to string) int {
	dep, err1 := time.Parse(windowDateLayout, from)
	ret, err2 := time.Parse(windowDateLayout, to)
	if err1 != nil || err2 != nil {
		return 0
	}
	n := int(ret.Sub(dep).Hours() / 24)
	if n < 0 {
		return 0
	}
	return n
}
