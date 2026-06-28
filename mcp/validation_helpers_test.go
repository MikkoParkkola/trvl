package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestValidateOriginDest_Valid(t *testing.T) {
	t.Parallel()
	args := map[string]any{"origin": "hel", "destination": "nrt"}
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if origin != "HEL" {
		t.Errorf("expected HEL, got %s", origin)
	}
	if dest != "NRT" {
		t.Errorf("expected NRT, got %s", dest)
	}
}

func TestValidateOriginDest_MissingOrigin(t *testing.T) {
	t.Parallel()
	args := map[string]any{"destination": "NRT"}
	_, _, err := validateOriginDest(args)
	if err == nil {
		t.Fatal("expected error for missing origin")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error should mention required: %v", err)
	}
}

func TestValidateOriginDest_MissingDest(t *testing.T) {
	t.Parallel()
	args := map[string]any{"origin": "HEL"}
	_, _, err := validateOriginDest(args)
	if err == nil {
		t.Fatal("expected error for missing destination")
	}
}

func TestValidateOriginDest_InvalidOrigin(t *testing.T) {
	t.Parallel()
	args := map[string]any{"origin": "XX", "destination": "NRT"}
	_, _, err := validateOriginDest(args)
	if err == nil {
		t.Fatal("expected error for invalid origin")
	}
	if !strings.Contains(err.Error(), "invalid origin") {
		t.Errorf("error should mention invalid origin: %v", err)
	}
}

func TestValidateOriginDest_InvalidDest(t *testing.T) {
	t.Parallel()
	args := map[string]any{"origin": "HEL", "destination": "1234"}
	_, _, err := validateOriginDest(args)
	if err == nil {
		t.Fatal("expected error for invalid destination")
	}
	if !strings.Contains(err.Error(), "invalid destination") {
		t.Errorf("error should mention invalid destination: %v", err)
	}
}

func TestValidateDate_Valid(t *testing.T) {
	t.Parallel()
	// Use a relative-future date so this test does not rot once
	// 2026-05-01 (the original literal) becomes the past.
	future := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	args := map[string]any{"departure_date": future}
	d, err := validateDate(args, "departure_date")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d != future {
		t.Errorf("expected %s, got %s", future, d)
	}
}

func TestValidateDate_Missing(t *testing.T) {
	t.Parallel()
	args := map[string]any{}
	_, err := validateDate(args, "departure_date")
	if err == nil {
		t.Fatal("expected error for missing date")
	}
	if !strings.Contains(err.Error(), "departure_date is required") {
		t.Errorf("error should mention field name: %v", err)
	}
}

func TestValidateDate_Invalid(t *testing.T) {
	t.Parallel()
	args := map[string]any{"departure_date": "not-a-date"}
	_, err := validateDate(args, "departure_date")
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestValidateDate_NilArgs(t *testing.T) {
	t.Parallel()
	_, err := validateDate(nil, "date")
	if err == nil {
		t.Fatal("expected error for nil args")
	}
}

func TestValidateOriginDest_CityNames(t *testing.T) {
	t.Parallel()
	// City names resolve to IATA codes.
	origin, dest, err := validateOriginDest(map[string]any{
		"origin":      "Paris",
		"destination": "Tokyo",
	})
	if err != nil {
		t.Fatalf("validateOriginDest(Paris, Tokyo) = error: %v", err)
	}
	if origin != "CDG" {
		t.Errorf("origin = %q, want CDG (first airport for Paris alphabetically)", origin)
	}
	if dest != "HND" {
		t.Errorf("dest = %q, want HND (first airport for Tokyo alphabetically)", dest)
	}
}

func TestValidateAirportListAndResolveLocationVariants(t *testing.T) {
	t.Parallel()

	list, err := validateAirportList(" hel, london ")
	if err != nil {
		t.Fatalf("validateAirportList city list: %v", err)
	}
	if !strings.HasPrefix(list, "HEL,") {
		t.Fatalf("list = %q, want HEL first", list)
	}

	primary, err := resolvePrimaryAirport("London")
	if err != nil {
		t.Fatalf("resolvePrimaryAirport London: %v", err)
	}
	if primary == "" || len(primary) != 3 {
		t.Fatalf("primary = %q, want IATA code", primary)
	}

	if got := resolveMCPLocation("hel"); got != "HEL" {
		t.Fatalf("resolveMCPLocation IATA = %q", got)
	}
	if got := resolveMCPLocation("Tokyo"); got != "HND" {
		t.Fatalf("resolveMCPLocation Tokyo = %q, want HND", got)
	}
	if got := resolveMCPLocation("not-an-airport"); got != "NOT-AN-AIRPORT" {
		t.Fatalf("resolveMCPLocation unknown = %q", got)
	}

	if _, err := validateAirportList("not-an-airport"); err == nil {
		t.Fatal("expected invalid airport list error")
	}
}
