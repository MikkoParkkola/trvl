package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// redirectHomeForTests points the home directory this package's tests resolve
// at a throwaway location, so no test can read or mutate the developer's real
// ~/.trvl. Handlers here persist preferences, trips, watches, alerts, price
// history and provider state under os.UserHomeDir with no other injection
// point, so the environment IS the seam.
//
// All three variables move together: os.UserHomeDir reads HOME on unix and
// USERPROFILE on Windows, and os.UserConfigDir reads XDG_CONFIG_HOME on unix.
//
// This is done once in TestMain rather than per test with t.Setenv because
// t.Setenv panics inside a test that has called t.Parallel, and this package
// uses t.Parallel widely. A package-wide floor also covers tests written later,
// which a per-test call cannot. Tests wanting their own isolated home on top of
// this floor still call t.Setenv/t.TempDir.
func redirectHomeForTests() (cleanup func()) {
	dir, err := os.MkdirTemp("", "trvl-test-home-")
	if err != nil {
		panic("test home: " + err.Error())
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return func() { os.RemoveAll(dir) }
}

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
	cleanup := redirectHomeForTests()
	destinationEnricher = func(context.Context, string, models.DateRange) (*models.DestinationInfo, error) {
		return nil, nil
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}
