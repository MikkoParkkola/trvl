package hacks

import (
	"fmt"
	"math"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// googleFlightsURL builds a Google Flights search citation URL.
func googleFlightsURL(dest, origin, date string) string {
	return fmt.Sprintf("https://www.google.com/travel/flights?q=Flights+to+%s+from+%s+on+%s", dest, origin, date)
}

// minFlightPrice returns the cheapest positive price across all flights.
// Returns 0 if no valid price found.
func minFlightPrice(r *models.FlightSearchResult) float64 {
	if r == nil || !r.Success {
		return 0
	}
	min := math.MaxFloat64
	found := false
	for _, f := range r.Flights {
		if f.Price > 0 && f.Price < min {
			min = f.Price
			found = true
		}
	}
	if !found {
		return 0
	}
	return min
}

// minFlightPriceWithCurrency returns the cheapest positive price AND the
// Currency of THAT SAME flight (not Flights[0]). Returns (0, "") if none.
func minFlightPriceWithCurrency(r *models.FlightSearchResult) (float64, string) {
	if r == nil || !r.Success {
		return 0, ""
	}
	min := math.MaxFloat64
	var currency string
	found := false
	for _, f := range r.Flights {
		if f.Price > 0 && f.Price < min {
			min = f.Price
			currency = f.Currency
			found = true
		}
	}
	if !found {
		return 0, ""
	}
	return min, currency
}

// flightCurrency returns the currency of the first flight result, or a fallback.
func flightCurrency(r *models.FlightSearchResult, fallback string) string {
	if r == nil || !r.Success || len(r.Flights) == 0 {
		return fallback
	}
	if c := r.Flights[0].Currency; c != "" {
		return c
	}
	return fallback
}

// parseDate parses YYYY-MM-DD into a time.Time.
func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// addDays adds n calendar days to a YYYY-MM-DD string.
// Returns empty string on parse error.
func addDays(date string, n int) string {
	t, err := parseDate(date)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// isOvernightRoute returns true when departure is in the evening and arrival
// is the next morning — a heuristic for night buses/trains.
func isOvernightRoute(departureISO, arrivalISO string) bool {
	dep, err1 := time.Parse("2006-01-02T15:04", departureISO)
	arr, err2 := time.Parse("2006-01-02T15:04", arrivalISO)
	if err1 != nil || err2 != nil {
		// Try without date part (HH:MM only strings from ground providers).
		// Treat as overnight if dep hour >= 20 or dep hour <= 2.
		var h int
		if _, err := parseHour(departureISO, &h); err == nil {
			return h >= 20 || h <= 2
		}
		return false
	}
	// Overnight = departs after 19:00 and arrives before 12:00 next day.
	depHour := dep.Hour()
	arrHour := arr.Hour()
	nightDepart := depHour >= 19 || depHour <= 2
	morningArrive := arrHour >= 4 && arrHour <= 13
	spansDays := arr.After(dep.Add(6 * time.Hour))
	return nightDepart && morningArrive && spansDays
}

// parseHour extracts the hour from "HH:MM" or "YYYY-MM-DDTHH:MM" strings.
func parseHour(s string, hour *int) (string, error) {
	if len(s) >= 16 {
		t, err := time.Parse("2006-01-02T15:04", s[:16])
		if err == nil {
			*hour = t.Hour()
			return s, nil
		}
	}
	if len(s) == 5 {
		t, err := time.Parse("15:04", s)
		if err == nil {
			*hour = t.Hour()
			return s, nil
		}
	}
	return s, &parseHourError{s}
}

type parseHourError struct{ s string }

func (e *parseHourError) Error() string { return "cannot parse hour from: " + e.s }
