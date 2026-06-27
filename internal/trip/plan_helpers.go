package trip

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func extractTopFlights(flts []models.FlightResult, n int) []PlanFlight {
	// Sort by price.
	sorted := make([]models.FlightResult, len(flts))
	copy(sorted, flts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Price < sorted[j].Price
	})

	if len(sorted) > n {
		sorted = sorted[:n]
	}

	var result []PlanFlight
	for _, f := range sorted {
		if f.Price <= 0 {
			continue
		}
		pf := PlanFlight{
			Price:           f.Price,
			ComparablePrice: f.PriceForRanking(), // all-in incl. unavoidable bags; == Price when none
			Currency:        f.Currency,
			Stops:           f.Stops,
			Duration:        f.Duration,
		}
		if len(f.Legs) > 0 {
			pf.Airline = f.Legs[0].Airline
			pf.Flight = f.Legs[0].FlightNumber
			pf.Departure = f.Legs[0].DepartureTime
			pf.Arrival = f.Legs[len(f.Legs)-1].ArrivalTime

			parts := []string{f.Legs[0].DepartureAirport.Code}
			for _, leg := range f.Legs {
				parts = append(parts, leg.ArrivalAirport.Code)
			}
			pf.Route = joinRoute(parts)
		}
		result = append(result, pf)
	}
	return result
}

// cheaperPerPersonFlights returns the lower per-person flight cost between the
// summed two-one-way total and a native single-ticket round-trip total. A native
// fare wins only when it is present (> 0) AND strictly cheaper; otherwise the
// two-one-way total is returned unchanged, keeping the summary byte-identical to
// the pre-native behaviour when no cheaper native fare exists. Pure: no I/O.
func cheaperPerPersonFlights(twoOneWays, nativeRoundTrip float64) float64 {
	if nativeRoundTrip > 0 && nativeRoundTrip < twoOneWays {
		return nativeRoundTrip
	}
	return twoOneWays
}

// comparableOrPrice returns a plan flight's all-in fare (ComparablePrice,
// baggage-inclusive) when set, falling back to the headline Price. Mirrors
// models.FlightResult.PriceForRanking for the plan's own flight type.
func comparableOrPrice(f PlanFlight) float64 {
	if f.ComparablePrice > 0 {
		return f.ComparablePrice
	}
	return f.Price
}

// extractTopRoundTripFares maps native single-ticket round-trip fares to
// PlanFlights. Only FareRoundTrip results are kept — a single bookable ticket
// whose Price is the full round-trip total per person. The Route spans both
// directions (e.g. "HEL -> BCN -> HEL"), built from the Direction-tagged legs so
// the inbound leg is never silently dropped. Mirrors extractTopFlights' price
// sort, zero-price filter, and cap.
func extractTopRoundTripFares(flts []models.FlightResult, n int) []PlanFlight {
	native := make([]models.FlightResult, 0, len(flts))
	for _, f := range flts {
		if f.FareType == models.FareRoundTrip {
			native = append(native, f)
		}
	}

	sort.Slice(native, func(i, j int) bool {
		return native[i].Price < native[j].Price
	})

	if len(native) > n {
		native = native[:n]
	}

	var result []PlanFlight
	for _, f := range native {
		if f.Price <= 0 {
			continue
		}
		pf := PlanFlight{
			Price:           f.Price,
			ComparablePrice: f.PriceForRanking(), // all-in incl. unavoidable bags; == Price when none
			Currency:        f.Currency,
			Stops:           f.Stops,
			Duration:        f.Duration,
		}
		if len(f.Legs) > 0 {
			pf.Airline = f.Legs[0].Airline
			pf.Flight = f.Legs[0].FlightNumber
			pf.Departure = f.Legs[0].DepartureTime
			pf.Arrival = f.Legs[len(f.Legs)-1].ArrivalTime
			pf.Route = roundTripRoute(f.Legs)
		}
		result = append(result, pf)
	}
	return result
}

// roundTripRoute builds a both-directions route string from a native round-trip
// fare's legs (e.g. "HEL -> BCN -> HEL"). It walks every leg in order so the
// inbound (return) legs are represented, deduplicating only consecutive repeats
// where one leg's arrival equals the next leg's departure (the normal chained
// case). The inbound leg's distinct airports are always surfaced — the return is
// never silently dropped.
func roundTripRoute(legs []models.FlightLeg) string {
	if len(legs) == 0 {
		return ""
	}
	parts := []string{legs[0].DepartureAirport.Code}
	for _, leg := range legs {
		dep := leg.DepartureAirport.Code
		if dep != "" && dep != parts[len(parts)-1] {
			parts = append(parts, dep)
		}
		parts = append(parts, leg.ArrivalAirport.Code)
	}
	return joinRoute(parts)
}
func trimReview(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Find last space before n.
	cut := n
	for cut > 0 && s[cut] != ' ' {
		cut--
	}
	if cut == 0 {
		cut = n
	}
	return strings.TrimSpace(s[:cut]) + "..."
}

// trimGuideSection cuts a Wikivoyage section to n chars at a sentence boundary.
func trimGuideSection(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Prefer ending at a period.
	cut := n
	for i := n; i > n/2; i-- {
		if i < len(s) && s[i] == '.' {
			cut = i + 1
			break
		}
	}
	return strings.TrimSpace(s[:cut])
}

// firstSectionByKey returns the first section whose key (case-insensitive)
// matches any of the given candidates.
func firstSectionByKey(sections map[string]string, candidates ...string) (string, bool) {
	for _, want := range candidates {
		wl := strings.ToLower(want)
		for k, v := range sections {
			if strings.ToLower(k) == wl && strings.TrimSpace(v) != "" {
				return v, true
			}
		}
	}
	return "", false
}

// findBreakfastNearHotel returns up to 5 cafes and restaurants within 600m of
// the hotel, sorted by distance. Queries multiple POI sources (OSM +
// Google Maps + Foursquare if configured) via GetNearbyPlaces for resilience.
// Returns empty on error so a breakfast search failure does not break the
// trip plan.
func findBreakfastNearHotel(ctx context.Context, lat, lon float64) []PlanBreakfast {
	// 600m = ~7 min walk — what a traveler actually wants for breakfast.
	result, err := destinations.GetNearbyPlaces(ctx, lat, lon, 600, "all")
	if err != nil || result == nil {
		return nil
	}
	return filterBreakfastSpots(result)
}

// filterBreakfastSpots extracts cafes and restaurants from nearby POI data,
// deduplicates by name, sorts by distance, and caps to 5 results.
func filterBreakfastSpots(result *destinations.NearbyResult) []PlanBreakfast {
	// Filter to cafes and restaurants (both can serve breakfast).
	breakfastTypes := map[string]bool{
		"cafe":       true,
		"restaurant": true,
	}

	type spot struct {
		name     string
		poiType  string
		distance int
		cuisine  string
		hours    string
		website  string
	}
	var spots []spot

	// Merge OSM POIs.
	for _, p := range result.POIs {
		if breakfastTypes[p.Type] {
			spots = append(spots, spot{
				name:     p.Name,
				poiType:  p.Type,
				distance: p.Distance,
				cuisine:  p.Cuisine,
				hours:    p.Hours,
				website:  p.Website,
			})
		}
	}

	// Merge rated places (Google Maps / Foursquare) as restaurants.
	for _, rp := range result.RatedPlaces {
		if rp.Distance > 600 {
			continue
		}
		spots = append(spots, spot{
			name:     rp.Name,
			poiType:  "restaurant",
			distance: rp.Distance,
			cuisine:  rp.Cuisine,
		})
	}

	// Deduplicate by name (case insensitive, first-seen wins).
	seen := make(map[string]bool)
	var unique []spot
	for _, s := range spots {
		k := strings.ToLower(strings.TrimSpace(s.name))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		unique = append(unique, s)
	}

	sort.Slice(unique, func(i, j int) bool {
		return unique[i].distance < unique[j].distance
	})
	if len(unique) > 5 {
		unique = unique[:5]
	}

	out := make([]PlanBreakfast, 0, len(unique))
	for _, s := range unique {
		out = append(out, PlanBreakfast{
			Name:     s.name,
			Type:     s.poiType,
			Distance: s.distance,
			Cuisine:  s.cuisine,
			Hours:    s.hours,
			Website:  s.website,
		})
	}
	return out
}

func joinRoute(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " -> "
		}
		out += p
	}
	return out
}

func extractTopHotels(htls []models.HotelResult, nights, n int) []PlanHotel {
	eligible := make([]models.HotelResult, 0, len(htls))
	for _, h := range htls {
		if models.HotelPriceEligibleForFinalTripCost(h) {
			eligible = append(eligible, h)
		}
	}

	// Sort by price.
	sorted := make([]models.HotelResult, len(eligible))
	copy(sorted, eligible)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Price < sorted[j].Price
	})

	if len(sorted) > n {
		sorted = sorted[:n]
	}

	var result []PlanHotel
	for _, h := range sorted {
		if h.Price <= 0 {
			continue
		}
		ph := PlanHotel{
			Name:            h.Name,
			HotelID:         h.HotelID,
			Rating:          h.Rating,
			Reviews:         h.ReviewCount,
			PerNight:        h.Price,
			Total:           h.Price * float64(nights),
			Currency:        h.Currency,
			Lat:             h.Lat,
			Lon:             h.Lon,
			PriceConfidence: h.PriceConfidence,
			PriceSource:     h.CheapestSource,
		}
		if len(h.Amenities) > 0 {
			if len(h.Amenities) > 3 {
				ph.Amenities = fmt.Sprintf("%s +%d more", joinAmenities(h.Amenities[:3]), len(h.Amenities)-3)
			} else {
				ph.Amenities = joinAmenities(h.Amenities)
			}
		}
		result = append(result, ph)
	}
	return result
}

func joinAmenities(amenities []string) string {
	out := ""
	for i, a := range amenities {
		if i > 0 {
			out += ", "
		}
		out += a
	}
	return out
}

func choosePlanSummaryCurrency(requested string, result *PlanResult) string {
	if requested != "" {
		return requested
	}
	if len(result.OutboundFlights) > 0 && result.OutboundFlights[0].Currency != "" {
		return result.OutboundFlights[0].Currency
	}
	if len(result.ReturnFlights) > 0 && result.ReturnFlights[0].Currency != "" {
		return result.ReturnFlights[0].Currency
	}
	if len(result.Hotels) > 0 && result.Hotels[0].Currency != "" {
		return result.Hotels[0].Currency
	}
	return "EUR"
}

func convertedPlanAmount(ctx context.Context, amount float64, from, to string) float64 {
	converted, _ := destinations.ConvertCurrency(ctx, amount, from, to)
	return math.Round(converted*100) / 100
}

func convertPlanFlights(ctx context.Context, flights []PlanFlight, currency string) {
	for i := range flights {
		if flights[i].Price <= 0 || flights[i].Currency == "" || flights[i].Currency == currency {
			continue
		}
		from := flights[i].Currency
		flights[i].Price = convertedPlanAmount(ctx, flights[i].Price, from, currency)
		if flights[i].ComparablePrice > 0 {
			flights[i].ComparablePrice = convertedPlanAmount(ctx, flights[i].ComparablePrice, from, currency)
		}
		flights[i].Currency = currency
	}
}

func convertPlanHotels(ctx context.Context, hotels []PlanHotel, currency string) {
	for i := range hotels {
		if hotels[i].Currency == "" || hotels[i].Currency == currency {
			continue
		}
		if hotels[i].PerNight > 0 {
			hotels[i].PerNight = convertedPlanAmount(ctx, hotels[i].PerNight, hotels[i].Currency, currency)
		}
		if hotels[i].Total > 0 {
			hotels[i].Total = convertedPlanAmount(ctx, hotels[i].Total, hotels[i].Currency, currency)
		}
		hotels[i].Currency = currency
	}
}

// buildReviewSnippets converts raw hotel reviews into plan review snippets.
// Returns up to 3 snippets, skipping reviews with empty text.
func buildReviewSnippets(reviews []models.HotelReview, hotelName string) []PlanReviewSnippet {
	snippets := make([]PlanReviewSnippet, 0, len(reviews))
	for _, r := range reviews {
		if r.Text == "" {
			continue
		}
		snippets = append(snippets, PlanReviewSnippet{
			Rating:    r.Rating,
			Text:      trimReview(r.Text, 180),
			Author:    r.Author,
			Date:      r.Date,
			HotelName: hotelName,
		})
		if len(snippets) >= 3 {
			break
		}
	}
	return snippets
}

// buildDestinationContext extracts a short travel-guide blurb from a
// Wikivoyage guide. Returns nil if no useful content was found.
func buildDestinationContext(guide *models.WikivoyageGuide) *PlanDestinationContext {
	planCtx := &PlanDestinationContext{
		Source: guide.URL,
	}
	if guide.Summary != "" {
		planCtx.Summary = trimGuideSection(guide.Summary, 280)
	}
	if s, ok := firstSectionByKey(guide.Sections, "When to go", "Understand", "Climate"); ok {
		planCtx.WhenToGo = trimGuideSection(s, 220)
	}
	if s, ok := firstSectionByKey(guide.Sections, "Get around", "Getting around"); ok {
		planCtx.GetAround = trimGuideSection(s, 220)
	}
	if planCtx.Summary == "" && planCtx.WhenToGo == "" && planCtx.GetAround == "" {
		return nil
	}
	return planCtx
}

// applyOSMEnrichment merges OpenStreetMap enrichment data into a plan hotel.
func applyOSMEnrichment(hotel *PlanHotel, extra *destinations.HotelEnrichment) {
	if extra.Stars > 0 && hotel.OSMStars == 0 {
		hotel.OSMStars = extra.Stars
	}
	if extra.Website != "" && hotel.Website == "" {
		hotel.Website = extra.Website
	}
	if extra.Wheelchair != "" {
		hotel.Wheelchair = extra.Wheelchair
	}
}

// provSeverity ranks a provider status for union merging: a hard failure
// outranks a retryable rate-limit, which outranks a definitive (succeeded or
// empty) result. Higher wins when the same provider appears on both legs.
func provSeverity(status string) int {
	switch status {
	case models.StatusFailed, models.StatusError, models.StatusTimeout, models.StatusCircuitBroken:
		return 3
	case models.StatusRateLimited:
		return 2
	default:
		return 1
	}
}

// mergeFlightProviders unions the per-provider statuses from the outbound and
// return flight searches. Both legs hit the same upstream providers, so a
// provider is reported once, keeping the worst (most severe) status seen across
// the two legs — the honest signal for "can we trust these prices as complete".
func mergeFlightProviders(legs ...*models.FlightSearchResult) []models.ProviderStatus {
	worst := map[string]models.ProviderStatus{}
	var order []string
	for _, leg := range legs {
		if leg == nil {
			continue
		}
		for _, s := range leg.ProviderStatuses {
			if cur, ok := worst[s.ID]; !ok {
				worst[s.ID] = s
				order = append(order, s.ID)
			} else if provSeverity(s.Status) > provSeverity(cur.Status) {
				worst[s.ID] = s
			}
		}
	}
	out := make([]models.ProviderStatus, 0, len(order))
	for _, id := range order {
		out = append(out, worst[id])
	}
	return out
}
