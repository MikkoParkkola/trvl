package trip

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/dailyspend"
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

// convertPlanFlights converts each flight's Price and ComparablePrice into
// currency. An item is relabelled only when every one of its conversions came
// back in the target currency; otherwise it keeps its source amounts and
// currency, so a failed rate never shows a foreign price under the target label
// (MIK-8138).
func convertPlanFlights(ctx context.Context, flights []PlanFlight, currency string, conv tripCostCurrencyConverter) {
	for i := range flights {
		f := &flights[i]
		if f.Price <= 0 || f.Currency == "" || f.Currency == currency {
			continue
		}
		price, ok := convertPlanAmount(ctx, conv, f.Price, f.Currency, currency)
		if !ok {
			continue
		}
		comparable := f.ComparablePrice
		if comparable > 0 {
			if comparable, ok = convertPlanAmount(ctx, conv, comparable, f.Currency, currency); !ok {
				continue
			}
		}
		f.Price, f.ComparablePrice, f.Currency = price, comparable, currency
	}
}

// convertPlanHotels converts PerNight and Total with the same all-or-nothing
// rule as convertPlanFlights.
func convertPlanHotels(ctx context.Context, hotels []PlanHotel, currency string, conv tripCostCurrencyConverter) {
	for i := range hotels {
		h := &hotels[i]
		if h.Currency == "" || h.Currency == currency {
			continue
		}
		perNight, total := h.PerNight, h.Total
		ok := true
		if perNight > 0 {
			perNight, ok = convertPlanAmount(ctx, conv, perNight, h.Currency, currency)
		}
		if ok && total > 0 {
			total, ok = convertPlanAmount(ctx, conv, total, h.Currency, currency)
		}
		if !ok {
			continue
		}
		h.PerNight, h.Total, h.Currency = perNight, total, currency
	}
}

// convertPlanAmount converts amount from -> to, rounded to cents, and reports
// whether the result is really in to. A blank source currency never counts as
// converted: destinations.ConvertCurrency echoes the target back for it.
func convertPlanAmount(ctx context.Context, conv tripCostCurrencyConverter, amount float64, from, to string) (float64, bool) {
	if amount == 0 || from == to {
		return amount, from == to || amount == 0
	}
	if from == "" || to == "" {
		return amount, false
	}
	converted, cur := convertedTripCostAmount(ctx, amount, from, to, conv)
	return converted, cur == to
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

// buildPlanSummary prices the cheapest plan in cur. Every component converts
// through conv; one that cannot be expressed in cur is left out of the sums and
// named in Unconverted, and an incomplete summary withholds the grand total
// rather than adding amounts in different currencies (MIK-8138).
func buildPlanSummary(ctx context.Context, result *PlanResult, input PlanInput, nights int, conv tripCostCurrencyConverter) PlanSummary {
	cur := choosePlanSummaryCurrency(input.Currency, result)
	var unconverted []string
	amount := func(name string, v float64, from string) (float64, bool) {
		out, ok := convertPlanAmount(ctx, conv, v, from, cur)
		if !ok {
			unconverted = append(unconverted, name)
		}
		return out, ok
	}
	// leg returns a flight's all-in and headline fares in cur.
	leg := func(f PlanFlight) (allIn, headline float64, ok bool) {
		allIn, ok1 := convertPlanAmount(ctx, conv, comparableOrPrice(f), f.Currency, cur)
		headline, ok2 := convertPlanAmount(ctx, conv, f.Price, f.Currency, cur)
		return allIn, headline, ok1 && ok2
	}

	// Two parallel figures per leg: the headline fare (Price) and the all-in
	// fare (ComparablePrice, incl. bags). The one-way pair counts only when
	// every leg it needs converted.
	var cheapOut, cheapRet, cheapOutHl, cheapRetHl float64
	oneWayOK := len(result.OutboundFlights) > 0 || len(result.ReturnFlights) > 0
	if oneWayOK && len(result.OutboundFlights) > 0 {
		cheapOut, cheapOutHl, oneWayOK = leg(result.OutboundFlights[0])
	}
	if oneWayOK && len(result.ReturnFlights) > 0 {
		cheapRet, cheapRetHl, oneWayOK = leg(result.ReturnFlights[0])
	}

	// Prefer the cheaper of {two one-ways, native single-ticket round-trip}. The
	// native RT price is ALREADY a full round-trip per person, so it competes
	// directly against cheapOut+cheapRet -- no doubling. The cheaper-of decision
	// is made on the all-in cost, and the matching headline figure is tracked so
	// the baggage delta is consistent with the branch actually chosen.
	var cheapRT, cheapRTHl float64
	rtOK := false
	if len(result.RoundTripFares) > 0 {
		cheapRT, cheapRTHl, rtOK = leg(result.RoundTripFares[0])
	}

	var perPersonAllIn, perPersonHeadline float64
	switch {
	case oneWayOK && rtOK && cheapRT > 0 && cheapRT < cheapOut+cheapRet:
		perPersonAllIn, perPersonHeadline = cheapRT, cheapRTHl
	case oneWayOK:
		perPersonAllIn, perPersonHeadline = cheapOut+cheapRet, cheapOutHl+cheapRetHl
	case rtOK:
		perPersonAllIn, perPersonHeadline = cheapRT, cheapRTHl
	case len(result.OutboundFlights) > 0 || len(result.ReturnFlights) > 0 || len(result.RoundTripFares) > 0:
		unconverted = append(unconverted, "flights")
	}

	flightsHeadline := perPersonHeadline * float64(input.Guests)
	flightsAllIn := perPersonAllIn * float64(input.Guests)
	baggageTotal := flightsAllIn - flightsHeadline
	if baggageTotal < 0 {
		baggageTotal = 0 // never let a stale comparable under-report fares
	}

	var cheapHotel float64
	if len(result.Hotels) > 0 {
		if v, ok := amount("hotel", result.Hotels[0].Total, result.Hotels[0].Currency); ok {
			cheapHotel = v
		}
	}

	// On-the-ground daily spend (meals, local transport, incidentals): a coarse
	// offline estimate, never a live quote, so it is always tagged via
	// MealsEstimated. Folding it in makes GrandTotal a no-surprise landed cost.
	meals := dailyspend.Lookup(models.ResolveLocationName(input.Destination))
	var mealsTotal float64
	if v, ok := amount("meals", meals.Total(input.Guests, nights), meals.Currency); ok {
		mealsTotal = v
	}
	// Airport<->city transfer and tourist/city tax from bundled offline EUR
	// tables. A city not in the table degrades to a typed not-found status (zero
	// + Estimated=false) rather than a fabricated figure (MIK-6530 PLANCOMP.1).
	var transfersTotal, taxesTotal float64
	transfersKnown, taxesKnown := false, false
	if eur, known := transferCost(input.Destination, input.Guests); known {
		if v, ok := amount("transfers", eur, "EUR"); ok {
			transfersTotal, transfersKnown = v, true
		}
	}
	if eur, known := cityTax(input.Destination, input.Guests, nights); known {
		if v, ok := amount("city tax", eur, "EUR"); ok {
			taxesTotal, taxesKnown = v, true
		}
	}

	summary := PlanSummary{
		FlightsTotal:       flightsHeadline,
		BaggageTotal:       baggageTotal,
		HotelTotal:         cheapHotel,
		MealsTotal:         mealsTotal,
		MealsEstimated:     mealsTotal > 0,
		TransfersTotal:     transfersTotal,
		TransfersEstimated: transfersKnown,
		TaxesTotal:         taxesTotal,
		TaxesEstimated:     taxesKnown,
		Currency:           cur,
		Budget:             input.Budget,
	}
	if len(unconverted) > 0 {
		summary.Incomplete = true
		summary.Unconverted = unconverted
		return summary
	}

	grandTotal := flightsAllIn + cheapHotel + mealsTotal + transfersTotal + taxesTotal
	summary.GrandTotal = grandTotal
	// PLANCOMP.2: if a budget was set and nothing fits under it, say so plainly,
	// carrying the cheapest total and the overage.
	summary.OverBudget, summary.Overage, summary.BudgetMessage = budgetVerdict(grandTotal, input.Budget, cur)
	if input.Guests > 0 {
		summary.PerPerson = grandTotal / float64(input.Guests)
	}
	if nights > 0 {
		summary.PerDay = grandTotal / float64(nights)
	}
	return summary
}
