package inboxparser

import (
	"time"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

// IngestSummary reports what an ingest run added to a trip.
type IngestSummary struct {
	trips.MergeSummary
	// BookingsAdded counts top-level bookings appended to trip.Bookings.
	BookingsAdded int `json:"bookings_added"`
	// Parsed counts raw messages that were recognised and parsed.
	Parsed int `json:"parsed"`
	// Unrecognised counts raw messages no provider matched.
	Unrecognised int `json:"unrecognised"`
}

// IngestConfirmations parses each raw confirmation email and merges the results
// into the trip, populating trip.Legs and trip.Workspace.ImportedRecords via
// trips.MergeReservationArtifacts and trip.Bookings directly from the parsed
// records. Unrecognised messages are counted and skipped (no error, no panic).
//
// This is the no-manual-entry ingest path: the caller hands over raw bytes and
// receives a fully populated trip plus a summary of what changed.
func IngestConfirmations(t trips.Trip, raws [][]byte) (trips.Trip, IngestSummary) {
	var (
		records []trips.ImportedRecord
		legs    []trips.TripLeg
		summary IngestSummary
	)

	for _, raw := range raws {
		rec, recLegs, ok := ParseConfirmation(raw)
		if !ok {
			summary.Unrecognised++
			continue
		}
		summary.Parsed++
		records = append(records, rec)
		legs = append(legs, recLegs...)
	}

	merged, ms := trips.MergeReservationArtifacts(t, records, legs, nil)
	summary.MergeSummary = ms

	before := len(merged.Bookings)
	merged.Bookings = mergeBookings(merged.Bookings, records)
	summary.BookingsAdded = len(merged.Bookings) - before

	return merged, summary
}

// mergeBookings appends a top-level Booking for each parsed record, skipping any
// whose provider+reference pair is already present so re-ingesting the same mail
// is idempotent.
func mergeBookings(existing []trips.Booking, records []trips.ImportedRecord) []trips.Booking {
	seen := make(map[string]bool, len(existing))
	for _, b := range existing {
		seen[bookingKey(b.Provider, b.Reference)] = true
	}
	out := append([]trips.Booking(nil), existing...)
	for _, r := range records {
		key := bookingKey(r.Provider, r.Reference)
		if r.Reference == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trips.Booking{
			Type:        r.Type,
			Provider:    r.Provider,
			Reference:   r.Reference,
			Price:       r.Price,
			Currency:    r.Currency,
			ConfirmedAt: time.Now(),
			Notes:       r.Notes,
		})
	}
	return out
}

func bookingKey(provider, reference string) string {
	return provider + "\x00" + reference
}
