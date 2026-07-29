package providers

import (
	"os"
	"testing"
)

// TestMain opts this package's tests in to local destinations.
//
// The destination policy in destination.go refuses loopback addresses by
// default, and almost every test in this package points a provider config at
// an httptest server on 127.0.0.1. Setting the opt-in here is the same thing a
// developer running against a local mock does, which is exactly the case the
// opt-in exists for.
//
// It does mean the suite as a whole exercises the permissive path, so the
// tests that pin the DEFAULT clear this variable for their own duration with
// t.Setenv (see destination_test.go). Any future test that cares about the
// refusal has to do the same -- inheriting the opt-in silently is the failure
// mode to watch for here.
func TestMain(m *testing.M) {
	os.Setenv(AllowLocalEnv, "1")
	os.Exit(m.Run())
}
