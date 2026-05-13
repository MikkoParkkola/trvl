// Package itinerary builds deterministic, map-aware day plans from a trip
// workspace. It uses local coordinates when available and falls back to the
// existing order when distance cannot be estimated.
package itinerary

import (
	"math"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

type Options struct {
	MaxRouteMinutesPerDay int
	AverageKPH            float64
}

type Result struct {
	Days     []trips.DayPlan `json:"days"`
	Warnings []string        `json:"warnings,omitempty"`
}

func Optimize(t trips.Trip, opts Options) Result {
	t = trips.NormalizeWorkspace(t)
	if opts.MaxRouteMinutesPerDay <= 0 {
		opts.MaxRouteMinutesPerDay = 180
	}
	if opts.AverageKPH <= 0 {
		opts.AverageKPH = 18
	}

	placesByID := make(map[string]trips.Place, len(t.Workspace.Places))
	for _, p := range t.Workspace.Places {
		placesByID[p.ID] = p
	}

	var days []trips.DayPlan
	if len(t.Workspace.Days) > 0 {
		for _, day := range t.Workspace.Days {
			day.EstimatedRouteMinutes = estimateRouteMinutes(day.PlaceIDs, placesByID, opts.AverageKPH)
			if day.EstimatedRouteMinutes > opts.MaxRouteMinutesPerDay {
				day.Warnings = appendUnique(day.Warnings, "day route time is likely overpacked")
			}
			days = append(days, day)
		}
	} else if len(t.Workspace.Places) > 0 {
		ordered := nearestNeighborOrder(t.Workspace.Places)
		day := trips.DayPlan{
			ID:       trips.StableID("day", "workspace"),
			Title:    "Workspace day plan",
			PlaceIDs: ordered,
		}
		day.EstimatedRouteMinutes = estimateRouteMinutes(day.PlaceIDs, placesByID, opts.AverageKPH)
		if day.EstimatedRouteMinutes > opts.MaxRouteMinutesPerDay {
			day.Warnings = append(day.Warnings, "day route time is likely overpacked")
		}
		days = append(days, day)
	}

	var warnings []string
	for _, day := range days {
		warnings = append(warnings, day.Warnings...)
	}
	return Result{Days: days, Warnings: appendUnique(nil, warnings...)}
}

func estimateRouteMinutes(placeIDs []string, places map[string]trips.Place, averageKPH float64) int {
	if len(placeIDs) < 2 {
		return 0
	}
	var km float64
	for i := 1; i < len(placeIDs); i++ {
		a, okA := places[placeIDs[i-1]]
		b, okB := places[placeIDs[i]]
		if !okA || !okB || !hasCoord(a) || !hasCoord(b) {
			return 0
		}
		km += HaversineKM(a.Lat, a.Lon, b.Lat, b.Lon)
	}
	return int(math.Round(km / averageKPH * 60))
}

func nearestNeighborOrder(places []trips.Place) []string {
	if len(places) == 0 {
		return nil
	}
	remaining := append([]trips.Place(nil), places...)
	order := []string{remaining[0].ID}
	current := remaining[0]
	remaining = remaining[1:]
	for len(remaining) > 0 {
		best := 0
		bestDist := math.Inf(1)
		for i, p := range remaining {
			dist := math.Inf(1)
			if hasCoord(current) && hasCoord(p) {
				dist = HaversineKM(current.Lat, current.Lon, p.Lat, p.Lon)
			}
			if dist < bestDist {
				bestDist = dist
				best = i
			}
		}
		current = remaining[best]
		order = append(order, current.ID)
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return order
}

func HaversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKM = 6371.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func hasCoord(p trips.Place) bool {
	return p.Lat != 0 || p.Lon != 0
}

func appendUnique(existing []string, values ...string) []string {
	seen := make(map[string]bool, len(existing)+len(values))
	out := append([]string(nil), existing...)
	for _, v := range existing {
		seen[v] = true
	}
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
