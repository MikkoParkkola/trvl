package hotels

import (
	"context"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// fetchRoomAvailability is the seam used to drill into a single hotel's
// room-level inventory. Tests set it to a deterministic offline stub. It is
// declared nil (not initialized to GetRoomAvailabilityWithOpts) to avoid a
// package-level initialization cycle: the drill-down transitively references
// core search, which references this enrichment. enrichHotelRooms falls back to
// GetRoomAvailabilityWithOpts when the seam is unset.
var fetchRoomAvailability func(ctx context.Context, opts RoomSearchOptions) (*RoomAvailability, error)

// bookingSourceURL returns the Booking.com hotel URL from a result's price
// sources, if one is present. It deliberately ignores HotelResult.BookingURL,
// which is a city-level link backfilled for display and not a per-hotel detail
// page suitable for room drill-down.
func bookingSourceURL(h models.HotelResult) string {
	for _, s := range h.Sources {
		if s.Provider == "booking" && s.BookingURL != "" {
			return s.BookingURL
		}
	}
	return ""
}

// roomTypeToModelRoom converts an internal RoomType (the drill-down shape) into
// the models.Room carried on a HotelResult. It maps the price-trust fields so a
// tax-inclusive room is surfaced as a verified all-in rate, and a pre-tax room
// stays room-level.
func roomTypeToModelRoom(rt RoomType) models.Room {
	inclusive := rt.TaxesFeesIncluded != nil && *rt.TaxesFeesIncluded

	priceConfidence := models.PriceConfidenceRoomLevel
	priceBasis := models.PriceBasisRoomNightly
	if inclusive {
		priceConfidence = models.PriceConfidenceVerified
		priceBasis = models.PriceBasisTaxInclusiveTotal
	}

	return models.Room{
		Name:               rt.Name,
		Price:              rt.Price,
		NightlyPrice:       rt.NightlyPrice,
		TotalPrice:         rt.TotalPrice,
		TaxesAndFees:       rt.TaxesAndFees,
		TaxesFeesIncluded:  rt.TaxesFeesIncluded,
		Currency:           rt.Currency,
		Provider:           rt.Provider,
		ProviderURL:        rt.ProviderURL,
		RateID:             rt.RateID,
		RatePlanName:       rt.RatePlanName,
		MatchConfidence:    rt.MatchConfidence,
		PriceBasis:         priceBasis,
		PriceConfidence:    priceConfidence,
		SizeM2:             rt.SizeM2,
		MaxGuests:          rt.MaxGuests,
		BedType:            rt.BedType,
		Amenities:          rt.Amenities,
		FreeCancellation:   rt.FreeCancellation != nil && *rt.FreeCancellation,
		Refundable:         rt.Refundable,
		CancellationPolicy: rt.CancellationPolicy,
		BreakfastIncluded:  rt.BreakfastIncluded != nil && *rt.BreakfastIncluded,
		Board:              rt.Board,
		Description:        rt.Description,
	}
}

// hotelHasVerifiedRoom reports whether a hotel already carries a verified
// (tax-inclusive) room-level price — e.g. from Agoda's city search — so room
// enrichment never clobbers a stronger existing price.
func hotelHasVerifiedRoom(h models.HotelResult) bool {
	return len(h.RoomTypes) > 0 && h.RoomTypes[0].PriceConfidence == models.PriceConfidenceVerified
}

// enrichHotelRooms drills into the top-N ranked hotels to attach real
// room-level pricing, mirroring enrichHotelAmenities. It is opt-in
// (opts.EnrichRooms) and rate-limit aware: it skips a fetch when the room
// provider is throttled rather than provoking a persistent 429 block. Hotels
// that already carry a verified room price are left untouched.
func enrichHotelRooms(ctx context.Context, hotels []models.HotelResult, opts HotelSearchOptions) []models.HotelResult {
	limit := opts.EnrichRoomsLimit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}

	var indices []int
	for i := range hotels {
		if hotels[i].HotelID != "" && !hotelHasVerifiedRoom(hotels[i]) && len(indices) < limit {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return hotels
	}

	const concurrency = 3
	type result struct {
		index int
		rooms []models.Room
	}

	fetch := fetchRoomAvailability
	if fetch == nil {
		fetch = GetRoomAvailabilityWithOpts
	}

	results := make(chan result, len(indices))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for _, idx := range indices {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Respect the shared rate limiter: a throttled provider stays
			// throttled, so skip rather than deepen the block.
			if HotelRateManager.IsThrottled("google") {
				return
			}
			HotelRateManager.RecordRequest("google")

			av, err := fetch(ctx, RoomSearchOptions{
				HotelID:      hotels[i].HotelID,
				CheckIn:      opts.CheckIn,
				CheckOut:     opts.CheckOut,
				Currency:     opts.Currency,
				Guests:       opts.Guests,
				ChildrenAges: opts.ChildrenAges,
				Rooms:        opts.Rooms,
				BookingURL:   bookingSourceURL(hotels[i]),
			})
			if err != nil || av == nil || len(av.Rooms) == 0 {
				return
			}

			rooms := make([]models.Room, 0, len(av.Rooms))
			for _, rt := range av.Rooms {
				rooms = append(rooms, roomTypeToModelRoom(rt))
			}
			results <- result{index: i, rooms: rooms}
		}(idx)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		hotels[r.index].RoomTypes = r.rooms
		// Feed the strongest drilled room into the price sources so the
		// verified room price can win the headline when the caller re-runs
		// FinalizeHotelPriceTrust (the "verified leads" rule).
		if src, ok := bestRoomSource(r.rooms); ok {
			hotels[r.index].Sources = append(hotels[r.index].Sources, src)
		}
	}

	return hotels
}

// bestRoomSource builds a PriceSource from the strongest drilled room (highest
// price-confidence tier, then cheapest nightly within tier) so a verified,
// all-in room price can become the headline. Returns false when no room carries
// a usable price.
func bestRoomSource(rooms []models.Room) (models.PriceSource, bool) {
	var best models.Room
	bestRank := 0
	found := false
	for _, rm := range rooms {
		price := roomHeadlinePrice(rm)
		if price <= 0 {
			continue
		}
		rank := priceConfidenceRank(rm.PriceConfidence)
		if !found || rank > bestRank || (rank == bestRank && price < roomHeadlinePrice(best)) {
			best = rm
			bestRank = rank
			found = true
		}
	}
	if !found {
		return models.PriceSource{}, false
	}
	provider := best.Provider
	if provider == "" {
		provider = "google"
	}
	return models.PriceSource{
		Provider:        provider,
		Price:           roomHeadlinePrice(best),
		Currency:        best.Currency,
		BookingURL:      best.ProviderURL,
		PriceBasis:      best.PriceBasis,
		PriceConfidence: best.PriceConfidence,
		RetrievedAt:     time.Now(),
	}, true
}

// roomHeadlinePrice is the per-night price a room contributes to the headline:
// the nightly rate when present, otherwise the room's single price field.
func roomHeadlinePrice(rm models.Room) float64 {
	if rm.NightlyPrice > 0 {
		return rm.NightlyPrice
	}
	return rm.Price
}
