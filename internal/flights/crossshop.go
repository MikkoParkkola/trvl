// crossshop.go — MIK-4956 Phase C: cross-shop re-pricing of Kiwi itineraries.
//
// Kiwi's routing intelligence finds self-connect / multi-stop itineraries that
// no single GDS sells as one fare. Those same physical segments are often
// cheaper booked direct (separate tickets) on a per-leg basis via Google
// Flights. This file decomposes a Kiwi itinerary into ordered segments, re-
// prices each on Google Flights, and — only when EVERY segment prices — emits
// a synthetic "book-direct" alternative carrying per-segment PriceSources and a
// summed price, so the cheaper of (Kiwi-bundled, book-direct) is visible.
//
// No-fabrication guard (MIK-4956 XSHOP.4): a book-direct alternative appears
// ONLY when all segments priced. If any segment cannot be priced, NO fake total
// is emitted and the enricher reports a non-definitive ProviderStatus so the
// MIK-4950 completeness envelope marks the search partial.
//
// The enricher never touches existing booking flows: it is gated behind
// crossShopEnabled() and only ever APPENDS book-direct alternatives. Rollback =
// revert the wiring commit in search.go.
package flights

import (
	"context"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const (
	crossShopProviderID   = "book_direct"
	crossShopProviderName = "Book-Direct (cross-shop)"
	// crossShopSelfConnectWarning is carried on every book-direct alternative:
	// separate tickets mean the traveller owns missed-connection risk.
	crossShopSelfConnectWarning = "Book-direct: separately ticketed segments — a missed connection is not protected, bags may need re-checking, and a delay on one leg will not rebook the next."
	// crossShopDefaultWindow is the half-width of the departure-time match
	// window. A re-priced segment must depart within +/- this of the Kiwi
	// segment's scheduled departure, else it is a different flight and the
	// segment is treated as unpriceable (no off-time substitution).
	crossShopDefaultWindow = 3 * time.Hour
	// crossShopMinSegments is the routing threshold: only multi-stop / self-
	// connect itineraries (>=2 segments) are cross-shopped. A single-segment
	// itinerary has nothing to re-price separately.
	crossShopMinSegments = 2
)

// segmentPricer re-prices one segment (origin->dest on date) and returns the
// candidate flights for that route. It is the injectable seam that keeps the
// enricher offline-testable: production wires it to Google Flights, tests pass
// a synthetic map-backed fake. Returning (nil, nil) means "queried, no hit".
type segmentPricer func(ctx context.Context, origin, dest, date string, opts SearchOptions) ([]models.FlightResult, error)

// flightSegment is one ordered leg of a decomposed itinerary: a bookable
// origin->dest hop on a specific date with a scheduled departure time used to
// match a re-priced direct flight.
type flightSegment struct {
	Origin   string
	Dest     string
	Date     string    // "YYYY-MM-DD", the segment's local departure date
	DepartAt time.Time // scheduled departure (zero if unparseable)
	HasTime  bool      // DepartAt is meaningful
}

// crossShopEnabled gates the enricher. Off by default so it never alters
// existing flows; opt in with TRVL_CROSSSHOP_ENRICH=1.
func crossShopEnabled() bool {
	switch os.Getenv("TRVL_CROSSSHOP_ENRICH") {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}

// decomposeSegments turns a (mapped) Kiwi itinerary into ordered bookable
// segments derived from its Legs — i.e. flyFrom -> layover0 -> ... -> flyTo —
// using each leg's departure date + time. It relies ONLY on route + time data
// (no carrier/flight-number, which Kiwi does not expose). Returns nil when the
// itinerary is not a self-connect candidate (fewer than crossShopMinSegments).
func decomposeSegments(f models.FlightResult) []flightSegment {
	if len(f.Legs) < crossShopMinSegments {
		return nil
	}
	segs := make([]flightSegment, 0, len(f.Legs))
	for _, leg := range f.Legs {
		origin := leg.DepartureAirport.Code
		dest := leg.ArrivalAirport.Code
		if origin == "" || dest == "" {
			// A leg with no resolvable endpoints cannot be re-priced; surface
			// the gap as an empty-date segment so the enricher fails closed.
			segs = append(segs, flightSegment{Origin: origin, Dest: dest})
			continue
		}
		seg := flightSegment{Origin: origin, Dest: dest}
		if t, err := time.Parse(flightTimeLayout, leg.DepartureTime); err == nil {
			seg.Date = t.Format("2006-01-02")
			seg.DepartAt = t
			seg.HasTime = true
		}
		segs = append(segs, seg)
	}
	return segs
}

// priceSegment re-prices a single segment and returns the cheapest priced
// candidate that departs within +/- window of the segment's scheduled time and
// shares the requested currency. ok=false means the segment is UNPRICEABLE —
// the pricer failed, returned nothing, or returned only off-window / wrong-
// currency / zero-price candidates. We never substitute an off-time flight: an
// itinerary's value is its specific routing, and re-timing it would misrepresent
// what the traveller is being quoted.
func priceSegment(ctx context.Context, price segmentPricer, seg flightSegment, currency string, window time.Duration, opts SearchOptions) (models.PriceSource, bool) {
	if seg.Origin == "" || seg.Dest == "" || seg.Date == "" {
		return models.PriceSource{}, false
	}
	candidates, err := price(ctx, seg.Origin, seg.Dest, seg.Date, opts)
	if err != nil || len(candidates) == 0 {
		return models.PriceSource{}, false
	}
	best := -1
	for i, c := range candidates {
		if c.Price <= 0 {
			continue
		}
		if currency != "" && c.Currency != "" && c.Currency != currency {
			continue // cross-currency sum would be a fabricated total
		}
		if seg.HasTime && !departsWithinWindow(c, seg.DepartAt, window) {
			continue // different flight than the routed segment
		}
		if best == -1 || c.Price < candidates[best].Price {
			best = i
		}
	}
	if best == -1 {
		return models.PriceSource{}, false
	}
	c := candidates[best]
	now := time.Now()
	return models.PriceSource{
		Provider:    c.Provider,
		Price:       c.Price,
		Currency:    c.Currency,
		BookingURL:  c.BookingURL,
		RetrievedAt: now,
		Freshness:   models.ClassifyFreshness(c.Provider, now, now),
	}, true
}

// departsWithinWindow reports whether candidate c departs within +/- window of
// target. A candidate with no parseable departure cannot be matched and is
// rejected (fail closed).
func departsWithinWindow(c models.FlightResult, target time.Time, window time.Duration) bool {
	dep := flightDeparture(c)
	if dep.IsZero() {
		return false
	}
	d := dep.Sub(target)
	if d < 0 {
		d = -d
	}
	return d <= window
}

// buildBookDirectAlternative assembles a synthetic separate-tickets alternative
// from a fully-priced set of per-segment sources. Caller MUST guarantee
// len(sources) == len(base.Legs) and every source priced (XSHOP.4); this
// function does not re-validate the count — see enrichCrossShop.
func buildBookDirectAlternative(base models.FlightResult, sources []models.PriceSource) models.FlightResult {
	var total float64
	currency := ""
	for _, s := range sources {
		total += s.Price
		if currency == "" {
			currency = s.Currency
		}
	}

	alt := models.FlightResult{
		Price:          total,
		Currency:       currency,
		Duration:       base.Duration,
		Stops:          base.Stops,
		Provider:       crossShopProviderID,
		SelfConnect:    true,
		BookDirect:     true,
		Legs:           append([]models.FlightLeg(nil), base.Legs...),
		SegmentSources: append([]models.PriceSource(nil), sources...),
		Warnings:       []string{crossShopSelfConnectWarning},
	}

	// Cheapest-source comparison (XSHOP.5): expose Kiwi-bundled vs book-direct
	// summed price so the cheaper option is visible. Savings is positive when
	// book-direct beats the bundled fare. We never suppress the more-expensive
	// case — surfacing the comparison is the point.
	if base.Price > 0 {
		alt.Savings = base.Price - total
		if alt.Savings > 0 {
			alt.CheapestSource = crossShopProviderID
		} else {
			alt.CheapestSource = base.Provider
		}
	}
	return alt
}

// enrichCrossShop re-prices the eligible itineraries in flights and returns the
// book-direct alternatives plus a ProviderStatus describing the outcome for the
// completeness envelope. Contract:
//   - Not gated / no eligible itineraries -> (nil, StatusSkipped): does not
//     touch completeness.
//   - At least one eligible itinerary, every one of its segments priced ->
//     (alts, StatusCheckedHit/StatusCheckedNoHit): definitive.
//   - An eligible itinerary had a segment that could not be priced ->
//     (alts-so-far, StatusFailed): completeness goes partial, and NO book-
//     direct total is fabricated for that itinerary (XSHOP.4).
func enrichCrossShop(ctx context.Context, flights []models.FlightResult, price segmentPricer, window time.Duration, opts SearchOptions) ([]models.FlightResult, models.ProviderStatus) {
	if window <= 0 {
		window = crossShopDefaultWindow
	}

	var (
		alts        []models.FlightResult
		eligible    int
		unpriceable int
	)

	for _, f := range flights {
		// Only cross-shop genuine self-connect itineraries (Kiwi's moat).
		// Single-ticket through-fares from Google/Ryanair/etc. carry through-
		// fare protection that re-pricing as separate tickets would discard —
		// they are not the cross-shop target. Kiwi marks its self-connects with
		// SelfConnect=true (see mapKiwiItinerary); single-ticket carriers don't.
		if !f.SelfConnect {
			continue
		}
		segs := decomposeSegments(f)
		if len(segs) < crossShopMinSegments {
			continue
		}
		eligible++

		currency := f.Currency
		sources := make([]models.PriceSource, 0, len(segs))
		allPriced := true
		for _, seg := range segs {
			src, ok := priceSegment(ctx, price, seg, currency, window, opts)
			if !ok {
				allPriced = false
				break
			}
			sources = append(sources, src)
		}
		if !allPriced {
			// No-fabrication guard: do NOT emit a partial total.
			unpriceable++
			continue
		}
		alts = append(alts, buildBookDirectAlternative(f, sources))
	}

	if eligible == 0 {
		return nil, models.ProviderStatus{
			ID:     crossShopProviderID,
			Name:   crossShopProviderName,
			Status: models.StatusSkipped,
			Error:  "no self-connect / multi-stop itineraries eligible for cross-shop re-pricing",
		}
	}
	if unpriceable > 0 {
		// Some itinerary could not be fully priced. Report a non-definitive
		// status so ComputeCompleteness marks the search partial (XSHOP.4).
		return alts, models.ProviderStatus{
			ID:      crossShopProviderID,
			Name:    crossShopProviderName,
			Status:  models.StatusFailed,
			Results: len(alts),
			Error:   "one or more itineraries had a segment that could not be re-priced; book-direct total withheld to avoid a fabricated price",
			FixHint: "retry later or widen the departure-time match window",
		}
	}
	status := models.StatusCheckedHit
	if len(alts) == 0 {
		status = models.StatusCheckedNoHit
	}
	return alts, models.ProviderStatus{
		ID:      crossShopProviderID,
		Name:    crossShopProviderName,
		Status:  status,
		Results: len(alts),
	}
}

// googleSegmentPricer is the production segmentPricer: it re-prices a segment
// via Google Flights only (a single bookable direct fare per leg), forcing the
// session currency so per-segment prices sum cleanly. It deliberately does NOT
// recurse into Kiwi (which would re-introduce bundling).
func googleSegmentPricer(currency string) segmentPricer {
	return func(ctx context.Context, origin, dest, date string, opts SearchOptions) ([]models.FlightResult, error) {
		segOpts := SearchOptions{
			Adults:     opts.Adults,
			CabinClass: opts.CabinClass,
			Currency:   currency,
			SortBy:     models.SortCheapest,
		}
		res, err := searchGoogleFlightsWithClient(ctx, DefaultClient(), origin, dest, date, segOpts)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		return res.Flights, nil
	}
}
