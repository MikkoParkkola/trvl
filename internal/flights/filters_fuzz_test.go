package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// FuzzBuildFilters exercises the Google Flights protobuf filter builder with
// arbitrary origin/destination/date strings and option values, asserting it
// never panics on malformed input (the encoder must be robust to junk the
// upstream layers might pass through). MIK-3090.
func FuzzBuildFilters(f *testing.F) {
	f.Add("HEL", "CDG", "2026-07-01", 0, 0, 1)
	f.Add("", "", "", 0, 0, 0)
	f.Add("hel", "cdg", "bad-date", 2, 600, 9)
	f.Add("QXQXQX", "plane", "2026-13-99", 3, 1, 1)
	f.Add("LON", "NYC", "2026-12-31", 1, 99999, 4)

	f.Fuzz(func(t *testing.T, origin, dest, date string, maxStops, maxPrice, adults int) {
		if maxStops < 0 {
			maxStops = -maxStops
		}
		opts := SearchOptions{
			MaxStops:   models.MaxStops(maxStops % 4),
			MaxPrice:   maxPrice,
			Adults:     adults,
			CabinClass: models.Economy,
		}
		_ = buildFilters(origin, dest, date, opts)
	})
}
