package mcp

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/travelctx"
)

// TestResolveDestOriginOptional_MultiOrigin verifies a comma-separated
// multi-airport origin is accepted (not rejected as a single invalid IATA
// code) and returned verbatim for downstream multi-airport routing.
// Regression for the MCP search_flights "invalid IATA code" bug on input
// like "ORY,BVA,CDG".
func TestResolveDestOriginOptional_MultiOrigin(t *testing.T) {
	args := map[string]any{"origin": "ORY,BVA,CDG", "destination": "LHR"}
	origin, dest, src, err := resolveDestOriginOptional(context.Background(), args, false)
	if err != nil {
		t.Fatalf("unexpected error for multi-airport origin: %v", err)
	}
	if origin != "ORY,BVA,CDG" {
		t.Errorf("origin = %q, want ORY,BVA,CDG", origin)
	}
	if dest != "LHR" {
		t.Errorf("dest = %q, want LHR", dest)
	}
	if src != travelctx.SourceExplicit {
		t.Errorf("source = %q, want explicit", src)
	}
}

// TestResolveDestOriginOptional_MultiDest verifies a comma-separated
// multi-airport destination is accepted and returned verbatim.
func TestResolveDestOriginOptional_MultiDest(t *testing.T) {
	args := map[string]any{"origin": "HEL", "destination": "LHR,LGW,STN"}
	origin, dest, _, err := resolveDestOriginOptional(context.Background(), args, false)
	if err != nil {
		t.Fatalf("unexpected error for multi-airport destination: %v", err)
	}
	if origin != "HEL" {
		t.Errorf("origin = %q, want HEL", origin)
	}
	if dest != "LHR,LGW,STN" {
		t.Errorf("dest = %q, want LHR,LGW,STN", dest)
	}
}

// TestResolveDestOriginOptional_MultiAirportRejectsBadToken verifies each
// token in a comma list is validated individually — a bad token fails fast.
func TestResolveDestOriginOptional_MultiAirportRejectsBadToken(t *testing.T) {
	args := map[string]any{"origin": "ORY,XX,CDG", "destination": "LHR"}
	if _, _, _, err := resolveDestOriginOptional(context.Background(), args, false); err == nil {
		t.Fatal("expected error for invalid token in multi-airport origin")
	}
}

// TestValidateOriginDest_MultiAirportPrimary verifies the shared validator
// accepts comma-separated multi-airport input (no longer rejecting it as one
// IATA code) and degrades to the primary airport for the single-airport
// callers that do not support multi-airport fan-out.
func TestValidateOriginDest_MultiAirportPrimary(t *testing.T) {
	args := map[string]any{"origin": "ORY,BVA,CDG", "destination": "LHR,LGW"}
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		t.Fatalf("unexpected error for multi-airport input: %v", err)
	}
	if origin != "ORY" {
		t.Errorf("origin = %q, want primary ORY", origin)
	}
	if dest != "LHR" {
		t.Errorf("dest = %q, want primary LHR", dest)
	}
}

// TestValidateOriginDest_SingleUnchanged guards backward compatibility: a
// single IATA code still validates and returns unchanged.
func TestValidateOriginDest_SingleUnchanged(t *testing.T) {
	args := map[string]any{"origin": "HEL", "destination": "BCN"}
	origin, dest, err := validateOriginDest(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if origin != "HEL" || dest != "BCN" {
		t.Errorf("got %q -> %q, want HEL -> BCN", origin, dest)
	}
}

// TestMultiAirportRoute verifies the routing predicate that decides between
// SearchFlights (single) and SearchMultiAirport (multi).
func TestMultiAirportRoute(t *testing.T) {
	cases := []struct {
		origin, dest string
		multi        bool
	}{
		{"HEL", "BCN", false},
		{"ORY,BVA,CDG", "LHR", true},
		{"HEL", "LHR,LGW", true},
		{"ORY,CDG", "LHR,LGW", true},
	}
	for _, c := range cases {
		origins := flights.ParseAirports(c.origin)
		dests := flights.ParseAirports(c.dest)
		if got := multiAirportRoute(origins, dests); got != c.multi {
			t.Errorf("multiAirportRoute(%q,%q) = %v, want %v", c.origin, c.dest, got, c.multi)
		}
	}
}
