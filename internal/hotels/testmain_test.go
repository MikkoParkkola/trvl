package hotels

import (
	"os"
	"testing"
)

// TestMain runs before all tests in the hotels package. It disables live
// auxiliary provider HTTP calls so that unit/integration tests that mock the
// Google Hotels transport do not accidentally fire real requests. Individual
// provider tests that need live or mock-server calls restore the flags
// themselves (or use their own mock transport).
func TestMain(m *testing.M) {
	trivagoEnabled = false
	hometogoEnabled = false
	os.Exit(m.Run())
}
