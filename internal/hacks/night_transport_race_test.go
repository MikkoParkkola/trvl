package hacks

import (
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestBuildNightHack_ConcurrentNoRace guards against the data race fixed when
// the shared package-level cases.Caser was replaced with a per-call caser.
// DetectAll fans detectors out concurrently, so buildNightHack must be safe to
// call from many goroutines at once. Under `go test -race` this fails if a
// shared, non-concurrency-safe caser is reintroduced.
func TestBuildNightHack_ConcurrentNoRace(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "PRG"}
	route := models.GroundRoute{
		Provider:  "flixbus",
		Type:      "bus",
		Currency:  "EUR",
		Departure: models.GroundStop{City: "Helsinki", Time: "2026-07-01T23:30:00Z"},
		Arrival:   models.GroundStop{City: "Prague", Time: "2026-07-02T07:15:00Z"},
	}

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			h := buildNightHack(in, route, averageHotelCost)
			if h.Type == "" {
				t.Error("buildNightHack returned empty hack type")
			}
		}()
	}
	wg.Wait()
}
