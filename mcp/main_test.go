package mcp

import (
	"context"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestMain installs a fast no-op destination enricher for the whole package so
// unit tests never reach the live destination-intelligence APIs.
//
// The production seam (destinationEnricher = destinations.GetDestinationInfo)
// makes real best-effort HTTP calls bounded by destinationEnrichTimeout (8s).
// It sits on every default search path — accommodations, flights, ground — so
// any handler test transitively triggers it. On a network-restricted CI runner
// the call cannot resolve and burns the full 8s budget, which both slows the
// suite and breaks latency-bounded tests (e.g. the slow-room-lookup bound that
// asserts the handler returns in under a second).
//
// Tests that exercise enrichment behaviour install their own stub via the same
// package-level seam (see enrich_destination_test.go), so this default never
// gets in their way; it only governs the tests that would otherwise hit the
// network by accident.
func TestMain(m *testing.M) {
	// Redirect HOME so nothing in this package can reach the developer's real
	// ~/.trvl. watch.DefaultStore() resolves it from os.UserHomeDir(); an
	// unguarded run wrote to a maintainer's live watch store on 2026-07-26.
	if dir, err := os.MkdirTemp("", "trvl-test-home-"); err == nil {
		defer func() { _ = os.RemoveAll(dir) }()
		_ = os.Setenv("HOME", dir)
		_ = os.Setenv("USERPROFILE", dir) // windows
	}

	destinationEnricher = func(context.Context, string, models.DateRange) (*models.DestinationInfo, error) {
		return nil, nil
	}
	os.Exit(m.Run())
}
