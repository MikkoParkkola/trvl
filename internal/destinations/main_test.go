package destinations

import (
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// TestMain opts this package's tests back in to local destinations.
//
// These clients now route through providers.GuardedTransport (trvl#539,
// TRVL.HARDEN.1), whose dial policy refuses loopback, private, link-local,
// unspecified and multicast addresses. Every test in this package points a
// client at an httptest server, which listens on loopback -- so without this
// the policy correctly refuses them all and the suite fails wholesale. It did:
// routing the clients through the guard turned ten passing tests red at once,
// which is how this file came to exist.
//
// Package-wide via os.Setenv in TestMain rather than t.Setenv per test, for the
// same reason the other packages here do it: t.Setenv panics inside a test that
// has called t.Parallel, and a package-wide floor also covers tests written
// later, which a per-test call cannot.
//
// The one test that must NOT inherit this floor is
// TestDestinationsClientsRefuseLoopback, which exercises the production policy
// and clears the variable itself. That is deliberate and load-bearing: a guard
// test running under an opt-out proves nothing, and the whole point of #539 is
// that a safety property nobody enforces is not a safety property.
func TestMain(m *testing.M) {
	if err := os.Setenv(providers.AllowLocalEnv, "1"); err != nil {
		panic("destinations test setup: " + err.Error())
	}
	os.Exit(m.Run())
}
