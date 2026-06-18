package hotels

import (
	"context"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestMain runs before all tests in the hotels package. It disables live
// auxiliary provider HTTP calls so that unit/integration tests that mock the
// Google Hotels transport do not accidentally fire real requests. Individual
// provider tests that need live or mock-server calls restore the flags
// themselves (or use their own mock transport).
func TestMain(m *testing.M) {
	trivagoEnabled = false
	hometogoEnabled = false
	anyplaceEnabled = false
	uniplacesEnabled = false
	wunderflatsEnabled = false
	housinganywhereEnabled = false
	landingEnabled = false
	SearchBooking = func(_ context.Context, _ string, _ HotelSearchOptions) ([]models.HotelResult, error) {
		return nil, nil
	}
	os.Exit(m.Run())
}
