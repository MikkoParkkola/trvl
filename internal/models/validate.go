package models

import (
	"fmt"
	"regexp"
	"time"
)

var iataRegex = regexp.MustCompile(`^[A-Z]{3}$`)

// ValidateIATA checks that code is a valid 3-letter uppercase IATA code.
func ValidateIATA(code string) error {
	if !iataRegex.MatchString(code) {
		return fmt.Errorf("invalid IATA code %q: must be exactly 3 uppercase letters (e.g. HEL, NRT)", code)
	}
	return nil
}

// ParseDate parses a YYYY-MM-DD string into a time.Time.
// It is a thin wrapper around time.Parse so callers don't need to repeat the
// format literal.
func ParseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// ValidateDate checks that date is a valid YYYY-MM-DD string and is not in the past.
func ValidateDate(date string) error {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("invalid date %q: expected YYYY-MM-DD format", date)
	}
	today := time.Now().Truncate(24 * time.Hour)
	if t.Before(today) {
		return fmt.Errorf("date %q is in the past", date)
	}
	return nil
}

// ValidateDateRange checks that from and to are valid, non-past dates and
// from <= to.
func ValidateDateRange(from, to string) error {
	if err := ValidateDate(from); err != nil {
		return fmt.Errorf("invalid start date: %w", err)
	}
	if err := ValidateDate(to); err != nil {
		return fmt.Errorf("invalid end date: %w", err)
	}
	fromT, _ := time.Parse("2006-01-02", from)
	toT, _ := time.Parse("2006-01-02", to)
	if toT.Before(fromT) {
		return fmt.Errorf("end date %s is before start date %s", to, from)
	}
	return nil
}
