package hacks

import (
	"context"
	"fmt"
	"strings"
)

// ferryCabinRoute describes an overnight ferry where a cabin replaces a hotel night.
type ferryCabinRoute struct {
	operator     string
	cabinFromEUR float64
	hotelAvgEUR  float64
	durationHrs  int
	frequency    string // "daily", "1x/day 17:00", etc.
}

// overnightFerries maps origin→destination to overnight ferry details.
var overnightFerries = map[string]map[string]ferryCabinRoute{
	"HEL": {"ARN": {operator: "Viking Line / Tallink Silja", cabinFromEUR: 35, hotelAvgEUR: 120, durationHrs: 16, frequency: "1x/day ~17:00"}},
	"ARN": {
		"HEL": {operator: "Viking Line / Tallink Silja", cabinFromEUR: 35, hotelAvgEUR: 100, durationHrs: 16, frequency: "1x/day ~17:00"},
		"TLL": {operator: "Tallink", cabinFromEUR: 40, hotelAvgEUR: 60, durationHrs: 18, frequency: "1x/day"},
		"RIX": {operator: "Tallink", cabinFromEUR: 45, hotelAvgEUR: 50, durationHrs: 17, frequency: "every other day"},
	},
	"CPH": {"OSL": {operator: "DFDS", cabinFromEUR: 45, hotelAvgEUR: 150, durationHrs: 17, frequency: "daily 17:00"}},
	"TKU": {"ARN": {operator: "Viking Line", cabinFromEUR: 30, hotelAvgEUR: 120, durationHrs: 11, frequency: "daily 20:45"}},
}

// detectFerryCabin fires when an overnight ferry exists between origin and
// destination, and the cabin price is meaningfully cheaper than a hotel night
// at the destination.
func detectFerryCabin(_ context.Context, in DetectorInput) []Hack {
	if !in.valid() {
		return nil
	}

	origin := strings.ToUpper(in.Origin)
	dest := strings.ToUpper(in.Destination)

	destRoutes, ok := overnightFerries[origin]
	if !ok {
		return nil
	}
	ferry, ok := destRoutes[dest]
	if !ok {
		return nil
	}

	savings := ferry.hotelAvgEUR - ferry.cabinFromEUR
	if savings < 10 {
		return nil
	}

	return []Hack{{
		Type:  "ferry_cabin_hotel",
		Title: fmt.Sprintf("Ferry cabin saves €%.0f vs hotel", savings),
		Description: fmt.Sprintf(
			"%s operates %s→%s overnight (%dh). Cabin from €%.0f replaces a hotel night (avg €%.0f in %s). You travel AND sleep.",
			ferry.operator, in.Origin, in.Destination, ferry.durationHrs, ferry.cabinFromEUR, ferry.hotelAvgEUR, in.Destination),
		Savings:  roundSavings(savings),
		Currency: "EUR",
		basis:    hotelNight, // hotel average minus the cabin, not a fare
		Steps: []string{
			fmt.Sprintf("Book %s %s→%s with cabin (from €%.0f)", ferry.operator, in.Origin, in.Destination, ferry.cabinFromEUR),
			fmt.Sprintf("Departs %s", ferry.frequency),
			"Arrive next morning — no hotel needed",
		},
		Risks: []string{
			fmt.Sprintf("Only departs %s — if you miss it, you need a hotel anyway", ferry.frequency),
			"Book cabin in advance — they sell out in peak season",
		},
	}}
}
