// Package daygraph composes a per-day plan of points of interest (POIs) for a
// trip. Given the trip's date span and a set of places (hotels, activities,
// restaurants), it produces one trips.DayPlan per trip day with the place IDs
// assigned to that day and a deterministic estimate of the walking/transit
// time needed to visit them in sequence.
//
// The estimate is derived from a great-circle (haversine) distance matrix and
// an assumed average travel speed, so the same input always yields the same
// output (no network, no clock dependence). Places without coordinates cannot
// contribute to the route estimate, so they are recorded in the day's
// Warnings rather than dropped silently.
package daygraph

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

// assumedSpeedKmh is the average point-to-point travel speed used to convert
// great-circle distance into minutes. It is intentionally conservative
// (urban walking + short transit) and deterministic.
const assumedSpeedKmh = 4.5

// Compose builds one trips.DayPlan per day in the trip's date span, assigning
// the provided places across the days and computing a non-zero
// EstimatedRouteMinutes from a deterministic travel-time matrix.
//
// The day span is taken from the trip legs (earliest start date through the
// latest end/start date). Places are distributed round-robin across days in a
// stable order so the result is reproducible. Places missing Lat/Lon are still
// assigned to a day but recorded in that day's Warnings and excluded from the
// route-time computation.
//
// It returns an error only when the trip has no resolvable date span.
func Compose(t trips.Trip, places []trips.Place) ([]trips.DayPlan, error) {
	dates, err := tripDates(t)
	if err != nil {
		return nil, err
	}

	ordered := stableSortPlaces(places)
	buckets := distribute(ordered, len(dates))

	days := make([]trips.DayPlan, len(dates))
	for i, date := range dates {
		days[i] = composeDay(date, buckets[i])
	}
	return days, nil
}

// composeDay builds a single DayPlan from a date and its assigned places.
func composeDay(date string, dayPlaces []trips.Place) trips.DayPlan {
	plan := trips.DayPlan{
		ID:       trips.StableID("day", date),
		Date:     date,
		Title:    dayTitle(date, dayPlaces),
		PlaceIDs: make([]string, 0, len(dayPlaces)),
	}

	routable := make([]trips.Place, 0, len(dayPlaces))
	for _, p := range dayPlaces {
		plan.PlaceIDs = append(plan.PlaceIDs, placeID(p))
		if hasCoords(p) {
			routable = append(routable, p)
			continue
		}
		plan.Warnings = append(plan.Warnings,
			fmt.Sprintf("place %q has no coordinates; excluded from route estimate", placeLabel(p)))
	}

	plan.EstimatedRouteMinutes = routeMinutes(routable)
	return plan
}

// routeMinutes computes the deterministic travel time, in minutes, for visiting
// the routable places in order. Consecutive legs use the haversine distance
// divided by the assumed speed. A single routable place still yields a non-zero
// estimate (a baseline dwell/approach time) so a populated day never reports
// zero minutes.
func routeMinutes(places []trips.Place) int {
	if len(places) == 0 {
		return 0
	}
	if len(places) == 1 {
		// Baseline approach time for a lone POI: 15 minutes.
		return 15
	}

	var totalMinutes float64
	for i := 1; i < len(places); i++ {
		km := haversineKm(places[i-1].Lat, places[i-1].Lon, places[i].Lat, places[i].Lon)
		totalMinutes += km / assumedSpeedKmh * 60.0
	}
	minutes := int(math.Ceil(totalMinutes))
	if minutes < 1 {
		// Coordinates were valid but coincident; still a populated day.
		minutes = 1
	}
	return minutes
}

// distribute spreads places round-robin across n buckets in stable order.
func distribute(places []trips.Place, n int) [][]trips.Place {
	buckets := make([][]trips.Place, n)
	if n == 0 {
		return buckets
	}
	for i, p := range places {
		idx := i % n
		buckets[idx] = append(buckets[idx], p)
	}
	return buckets
}

// stableSortPlaces returns a copy of places sorted by a stable key so the
// composition is reproducible regardless of input ordering.
func stableSortPlaces(places []trips.Place) []trips.Place {
	out := append([]trips.Place(nil), places...)
	sort.SliceStable(out, func(i, j int) bool {
		return placeID(out[i]) < placeID(out[j])
	})
	return out
}

// tripDates returns the inclusive list of YYYY-MM-DD dates spanned by the trip,
// derived from the leg start/end times. Returns an error when no leg carries a
// parseable date.
func tripDates(t trips.Trip) ([]string, error) {
	var minDate, maxDate string
	for _, leg := range t.Legs {
		for _, raw := range []string{leg.StartTime, leg.EndTime} {
			d, ok := dateOnly(raw)
			if !ok {
				continue
			}
			if minDate == "" || d < minDate {
				minDate = d
			}
			if maxDate == "" || d > maxDate {
				maxDate = d
			}
		}
	}
	if minDate == "" {
		return nil, fmt.Errorf("trip %q has no leg with a parseable date", t.ID)
	}

	start, err := models.ParseDate(minDate)
	if err != nil {
		return nil, fmt.Errorf("parse trip start date %q: %w", minDate, err)
	}
	end, err := models.ParseDate(maxDate)
	if err != nil {
		return nil, fmt.Errorf("parse trip end date %q: %w", maxDate, err)
	}

	var dates []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	return dates, nil
}

// dateOnly extracts the leading YYYY-MM-DD portion of an ISO datetime string.
func dateOnly(s string) (string, bool) {
	if len(s) < 10 {
		return "", false
	}
	head := s[:10]
	if _, err := models.ParseDate(head); err != nil {
		return "", false
	}
	return head, true
}

// dayTitle builds a human-friendly title for a day from its places.
func dayTitle(date string, places []trips.Place) string {
	if len(places) == 0 {
		return "Day in " + date
	}
	names := make([]string, 0, len(places))
	for _, p := range places {
		names = append(names, placeLabel(p))
	}
	return fmt.Sprintf("%s: %s", date, strings.Join(names, ", "))
}

// placeID returns a stable identifier for a place, deriving one when absent.
func placeID(p trips.Place) string {
	if p.ID != "" {
		return p.ID
	}
	return trips.StableID("place", p.Name, p.City, p.Address)
}

// placeLabel returns the best available human label for a place.
func placeLabel(p trips.Place) string {
	if p.Name != "" {
		return p.Name
	}
	return placeID(p)
}

// hasCoords reports whether a place carries usable coordinates.
func hasCoords(p trips.Place) bool {
	return p.Lat != 0 || p.Lon != 0
}

// haversineKm computes the great-circle distance in kilometres between two
// coordinates. Replicated locally rather than widening the route package API.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
