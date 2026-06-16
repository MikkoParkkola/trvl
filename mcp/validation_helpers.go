package mcp

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// validateAirportList parses a comma-separated origin/destination string into
// a validated, comma-joined IATA list. Each token may be an IATA code or a
// city name (resolved to its serving airports via ParseFlightLocations).
// Every resulting code is validated individually, so a valid multi-airport
// list like "ORY,BVA,CDG" is accepted instead of being rejected as one
// malformed IATA code. Returns the canonical comma-joined codes.
func validateAirportList(s string) (string, error) {
	codes := flights.ParseFlightLocations(s)
	if len(codes) == 0 {
		// Fall back to the legacy single-token resolve so the error message and
		// city-resolution behavior are unchanged for non-list input.
		code := resolveMCPLocation(s)
		if err := models.ValidateIATA(code); err != nil {
			return "", err
		}
		return code, nil
	}
	for _, code := range codes {
		if err := models.ValidateIATA(code); err != nil {
			return "", err
		}
	}
	return strings.Join(codes, ","), nil
}

// validateOriginDest extracts and validates origin/destination from tool
// arguments. Accepts either IATA codes ("HEL"), city names ("Helsinki"), or
// comma-separated multi-airport lists ("ORY,BVA,CDG"). City names resolve to
// their primary airport code. Because the callers of this helper drive
// single-airport searches, a multi-airport list degrades to its primary
// (first) airport rather than erroring — use resolveDestOriginOptional +
// SearchMultiAirport for true multi-airport fan-out.
func validateOriginDest(args map[string]any) (origin, dest string, err error) {
	origin = strings.TrimSpace(argString(args, "origin"))
	dest = strings.TrimSpace(argString(args, "destination"))
	if origin == "" || dest == "" {
		return "", "", fmt.Errorf("origin and destination are required")
	}

	origin, err = resolvePrimaryAirport(origin)
	if err != nil {
		return "", "", fmt.Errorf("invalid origin %q: %w", argString(args, "origin"), err)
	}
	dest, err = resolvePrimaryAirport(dest)
	if err != nil {
		return "", "", fmt.Errorf("invalid destination %q: %w", argString(args, "destination"), err)
	}
	return origin, dest, nil
}

// resolvePrimaryAirport validates an origin/destination string and returns the
// primary (first) airport. Single-airport input is byte-identical to the old
// resolveMCPLocation+ValidateIATA path; a multi-airport list is validated and
// collapsed to its first code.
func resolvePrimaryAirport(s string) (string, error) {
	list, err := validateAirportList(s)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(list, ','); i >= 0 {
		return list[:i], nil
	}
	return list, nil
}

// validateDate extracts and validates a date argument (YYYY-MM-DD).
func validateDate(args map[string]any, key string) (string, error) {
	d := argString(args, key)
	if d == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	if err := models.ValidateDate(d); err != nil {
		return "", err
	}
	return d, nil
}

// resolveMCPLocation converts a city name to an IATA code for MCP tool use.
// If already an IATA code, returns it uppercased. If a city name resolves to
// airports, returns the first airport alphabetically — note this may not be
// the most-trafficked airport for a given city (e.g. London → LGW, not LHR).
// Unknown inputs are uppercased and passed through for ValidateIATA to reject.
func resolveMCPLocation(s string) string {
	upper := strings.ToUpper(s)
	if models.IsIATACode(upper) {
		return upper
	}
	airports := models.ResolveCityToAirports(s)
	if len(airports) > 0 {
		return airports[0]
	}
	return upper
}
