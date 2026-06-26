// Package trip implements trip planning by combining flight and hotel searches.
package trip

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// PlanInput configures a trip plan search.
type PlanInput struct {
	Origin      string
	Destination string
	DepartDate  string
	ReturnDate  string
	Guests      int
	Currency    string
}

// PlanFlight is a flight option in the trip plan.
type PlanFlight struct {
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Airline   string  `json:"airline"`
	Flight    string  `json:"flight_number"`
	Stops     int     `json:"stops"`
	Duration  int     `json:"duration_min"`
	Departure string  `json:"departure"`
	Arrival   string  `json:"arrival"`
	Route     string  `json:"route"`
}

// PlanHotel is a hotel option in the trip plan.
type PlanHotel struct {
	Name       string  `json:"name"`
	HotelID    string  `json:"hotel_id,omitempty"`
	Rating     float64 `json:"rating"`
	Reviews    int     `json:"reviews"`
	PerNight   float64 `json:"per_night"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency"`
	Amenities  string  `json:"amenities,omitempty"`
	Lat        float64 `json:"lat,omitempty"`
	Lon        float64 `json:"lon,omitempty"`
	OSMStars   int     `json:"osm_stars,omitempty"`
	Website    string  `json:"website,omitempty"`
	Wheelchair string  `json:"wheelchair,omitempty"`
	// PriceConfidence mirrors models.HotelResult.PriceConfidence for the chosen
	// headline Price: "verified" / "room_level" are real bookable rates, while
	// "unverified" (or empty) is an indicative headline lead-in. The renderer
	// turns this into an honest label so users can see whether a shown price is
	// a real per-night rate or just a search-result teaser.
	PriceConfidence string `json:"price_confidence,omitempty"`
	// PriceSource is the provider name behind the chosen Price (e.g. "Agoda",
	// "Booking.com"), carried through for display alongside the confidence label.
	PriceSource string `json:"price_source,omitempty"`
}

// PlanBreakfast is a breakfast spot within walking distance of the chosen hotel.
type PlanBreakfast struct {
	Name      string `json:"name"`
	Type      string `json:"type"`       // cafe, restaurant
	Distance  int    `json:"distance_m"` // meters from hotel
	Cuisine   string `json:"cuisine,omitempty"`
	Hours     string `json:"opening_hours,omitempty"`
	Website   string `json:"website,omitempty"`
	HotelName string `json:"hotel_name,omitempty"` // which hotel this is walkable from
}

// PlanReviewSnippet is a short review excerpt for the chosen hotel.
type PlanReviewSnippet struct {
	Rating    float64 `json:"rating"`
	Text      string  `json:"text"`
	Author    string  `json:"author,omitempty"`
	Date      string  `json:"date,omitempty"`
	HotelName string  `json:"hotel_name,omitempty"`
}

// PlanDestinationContext is a short travel-guide blurb about the destination,
// extracted from Wikivoyage.
type PlanDestinationContext struct {
	Summary   string `json:"summary,omitempty"`
	WhenToGo  string `json:"when_to_go,omitempty"`
	GetAround string `json:"get_around,omitempty"`
	Source    string `json:"source,omitempty"`
}

// PlanSummary shows the cheapest combination.
type PlanSummary struct {
	FlightsTotal float64 `json:"flights_total"`
	HotelTotal   float64 `json:"hotel_total"`
	GrandTotal   float64 `json:"grand_total"`
	PerPerson    float64 `json:"per_person"`
	PerDay       float64 `json:"per_day"`
	Currency     string  `json:"currency"`
}

// PlanResult is the full trip plan response.
type PlanResult struct {
	Success         bool         `json:"success"`
	Origin          string       `json:"origin"`
	Destination     string       `json:"destination"`
	DepartDate      string       `json:"depart_date"`
	ReturnDate      string       `json:"return_date"`
	Nights          int          `json:"nights"`
	Guests          int          `json:"guests"`
	OutboundFlights []PlanFlight `json:"outbound_flights"`
	ReturnFlights   []PlanFlight `json:"return_flights"`
	// RoundTripFares carries native single-ticket round-trip fares — one bookable
	// ticket per entry covering both directions, whose Price is the full round-trip
	// total per person (often discounted below the sum of two separate one-ways).
	// Purely additive: the split OutboundFlights/ReturnFlights view stays populated
	// exactly as before, so this never weakens the existing two-one-way display. A
	// native fare is preferred in the cost summary only when it is genuinely cheaper.
	RoundTripFares []PlanFlight            `json:"round_trip_fares,omitempty"`
	Hotels         []PlanHotel             `json:"hotels"`
	Breakfast      []PlanBreakfast         `json:"breakfast,omitempty"`
	ReviewSnippets []PlanReviewSnippet     `json:"review_snippets,omitempty"`
	Context        *PlanDestinationContext `json:"context,omitempty"`
	Summary        PlanSummary             `json:"summary"`
	Error          string                  `json:"error,omitempty"`

	// HotelCoverage / HotelProviders carry the per-provider evidence so the
	// renderer can be honest about partial accommodation coverage instead of
	// silently presenting a thinned list as if it were exhaustive. A provider
	// that was rate-limited (retryable) is distinct from one that genuinely had
	// no availability — the user needs that distinction to decide whether to
	// retry or trust the prices shown.
	HotelCoverage  models.Completeness     `json:"hotel_coverage,omitempty"`
	HotelProviders []models.ProviderStatus `json:"hotel_providers,omitempty"`

	// FlightCoverage / FlightProviders mirror the hotel fields for air travel.
	// Both legs query the same upstream providers, so per-leg statuses are merged
	// into one union (worst status per provider wins): a provider rate-limited or
	// failed on either leg means the displayed flight prices are not exhaustive.
	FlightCoverage  models.Completeness     `json:"flight_coverage,omitempty"`
	FlightProviders []models.ProviderStatus `json:"flight_providers,omitempty"`
}

// PlanTrip searches flights and hotels in parallel and returns the top options
// along with a cheapest-combination summary.
//
// An empty input.ReturnDate plans a one-way trip: only the outbound flight leg
// is searched (no return leg, no native single-ticket round-trip fare), Nights
// is 0, and the cheapest-combination summary covers the outbound flight plus a
// single-night hotel lead-in. Supplying a ReturnDate keeps the round-trip
// behaviour byte-identical. This mirrors the CLI one-way path, which passes an
// empty return date straight through to the same one-way flight search — no
// return is ever fabricated.
func PlanTrip(ctx context.Context, input PlanInput) (*PlanResult, error) {
	if input.Origin == "" || input.Destination == "" {
		return nil, fmt.Errorf("origin and destination are required")
	}
	if input.DepartDate == "" {
		return nil, fmt.Errorf("depart date is required")
	}
	if input.Guests <= 0 {
		return nil, fmt.Errorf("guests must be at least 1")
	}

	departDate, err := models.ParseDate(input.DepartDate)
	if err != nil {
		return nil, fmt.Errorf("invalid depart date %q: %w", input.DepartDate, err)
	}

	// A round trip is requested only when a return date is supplied. One-way
	// trips skip return-date parsing entirely — there is no date to validate and
	// no nights to compute.
	roundTrip := input.ReturnDate != ""
	nights := 0
	if roundTrip {
		returnDate, rerr := models.ParseDate(input.ReturnDate)
		if rerr != nil {
			return nil, fmt.Errorf("invalid return date %q: %w", input.ReturnDate, rerr)
		}
		if !returnDate.After(departDate) {
			return nil, fmt.Errorf("return date must be after depart date")
		}
		nights = int(math.Round(returnDate.Sub(departDate).Hours() / 24))
	}

	result := &PlanResult{
		Origin:      input.Origin,
		Destination: input.Destination,
		DepartDate:  input.DepartDate,
		ReturnDate:  input.ReturnDate,
		Nights:      nights,
		Guests:      input.Guests,
	}

	// Load user preferences for hotel filtering.
	prefs, _ := preferences.Load()

	// Build hotel search options with preference-based filters.
	// MaxPages=1: compound commands only need the cheapest hotel, not 75 results.
	hotelOpts := hotels.HotelSearchOptions{
		CheckIn:  input.DepartDate,
		CheckOut: input.ReturnDate,
		Guests:   input.Guests,
		Sort:     "cheapest",
		Currency: input.Currency,
		MaxPages: 1,
	}
	if prefs != nil {
		if prefs.MinHotelStars > 0 {
			hotelOpts.Stars = prefs.MinHotelStars
		}
		if prefs.MinHotelRating > 0 {
			hotelOpts.MinRating = prefs.MinHotelRating
		}
	}

	// Search all three in parallel, sharing one HTTP client for connection
	// reuse and shared rate limiting across the 3 parallel goroutines.
	client := newCompoundSearchClient()

	var (
		outResult   *models.FlightSearchResult
		retResult   *models.FlightSearchResult
		rtResult    *models.FlightSearchResult
		hotelResult *models.HotelSearchResult
		outErr      error
		retErr      error
		rtErr       error
		hotelErr    error
		wg          sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		outResult, outErr = flights.SearchFlightsWithClient(ctx, client, input.Origin, input.Destination, input.DepartDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
			Adults: input.Guests,
		})
	}()
	// Return leg and native single-ticket round-trip fares are only searched for
	// a round trip. A one-way plan skips both — no return is fabricated.
	if roundTrip {
		wg.Add(2)
		go func() {
			defer wg.Done()
			retResult, retErr = flights.SearchFlightsWithClient(ctx, client, input.Destination, input.Origin, input.ReturnDate, flights.SearchOptions{
				SortBy: models.SortCheapest,
				Adults: input.Guests,
			})
		}()
		go func() {
			defer wg.Done()
			// Native single-ticket round-trip fares. Passing ReturnDate routes this
			// through searchRoundTripComposed, which queries providers' genuine
			// round-trip fares (FareRoundTrip) alongside composed one-way pairs. The
			// leg sub-searches share the same singleflight cache keys as the outbound
			// and return goroutines above, so only the native-RT passes are net-new
			// network — no extra fan-out beyond this single call.
			rtResult, rtErr = flights.SearchFlightsWithClient(ctx, client, input.Origin, input.Destination, input.DepartDate, flights.SearchOptions{
				SortBy:     models.SortCheapest,
				Adults:     input.Guests,
				ReturnDate: input.ReturnDate,
			})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		hotelLocation := models.ResolveHotelCity(input.Destination)
		hotelResult, hotelErr = hotels.SearchHotelsWithClient(ctx, client, hotelLocation, hotelOpts)
	}()
	wg.Wait()

	// Apply preference-based post-filtering (dormitories, ensuite, districts).
	if hotelErr == nil && hotelResult != nil && hotelResult.Success && prefs != nil {
		city := models.ResolveLocationName(input.Destination)
		hotelResult.Hotels = preferences.FilterHotels(hotelResult.Hotels, city, prefs)
	}

	// Extract top outbound flights (up to 5).
	if outErr == nil && outResult != nil && outResult.Success {
		result.OutboundFlights = extractTopFlights(outResult.Flights, 5)
	}

	// Extract top return flights (up to 5).
	if retErr == nil && retResult != nil && retResult.Success {
		result.ReturnFlights = extractTopFlights(retResult.Flights, 5)
	}

	// Extract native single-ticket round-trip fares (up to 5). Keep ONLY
	// FareRoundTrip results — the composed one-way pairs that searchRoundTripComposed
	// also returns are already represented by the split outbound/return view above,
	// so including them here would double-count. Each kept fare is one bookable
	// ticket whose Price is the full round-trip total per person.
	if rtErr == nil && rtResult != nil && rtResult.Success {
		result.RoundTripFares = extractTopRoundTripFares(rtResult.Flights, 5)
	}

	// Extract top hotels (up to 5).
	if hotelErr == nil && hotelResult != nil && hotelResult.Success {
		result.Hotels = extractTopHotels(hotelResult.Hotels, nights, 5)
	}

	// Surface accommodation coverage so the renderer can be honest about a
	// thinned provider set (rate-limited != no availability). Provider statuses
	// are populated even on a non-Success search, so read them unconditionally.
	if hotelResult != nil {
		result.HotelProviders = hotelResult.ProviderStatuses
		result.HotelCoverage = hotelResult.Completeness
	}

	// Surface flight coverage as the union of both legs (worst status per
	// provider wins), so a provider degraded on either the outbound or return
	// search flags the flight prices as potentially incomplete.
	result.FlightProviders = mergeFlightProviders(outResult, retResult)
	result.FlightCoverage = models.ComputeCompleteness(result.FlightProviders)

	// Find breakfast spots within walking distance.
	// Searches top hotels in order — the first one with at least 3 spots
	// within 500m is picked. This biases toward hotels in lively areas
	// rather than the absolute cheapest (which may be in a food desert).
	var chosenHotel *PlanHotel
	for i := range result.Hotels {
		h := &result.Hotels[i]
		if h.Lat == 0 && h.Lon == 0 {
			continue
		}
		spots := findBreakfastNearHotel(ctx, h.Lat, h.Lon)
		if len(spots) >= 3 {
			for j := range spots {
				spots[j].HotelName = h.Name
			}
			result.Breakfast = spots
			chosenHotel = h
			break
		}
	}
	if chosenHotel == nil && len(result.Hotels) > 0 {
		chosenHotel = &result.Hotels[0]
	}

	// Fetch reviews for the chosen hotel + destination context from Wikivoyage
	// in parallel. These are "nice to have" enrichments — failures are silent.
	var enrichWg sync.WaitGroup
	var enrichMu sync.Mutex
	enrichCtx, cancelEnrich := context.WithTimeout(ctx, 20*time.Second)
	defer cancelEnrich()

	if chosenHotel != nil && chosenHotel.HotelID != "" {
		enrichWg.Add(1)
		go func(hotelID, hotelName string) {
			defer enrichWg.Done()
			reviews, err := hotels.GetHotelReviews(enrichCtx, hotelID, hotels.ReviewOptions{Limit: 3, Sort: "highest"})
			if err != nil || reviews == nil || len(reviews.Reviews) == 0 {
				return
			}
			snippets := buildReviewSnippets(reviews.Reviews, hotelName)
			enrichMu.Lock()
			result.ReviewSnippets = snippets
			enrichMu.Unlock()
		}(chosenHotel.HotelID, chosenHotel.Name)
	}

	// Wikivoyage destination context.
	enrichWg.Add(1)
	go func() {
		defer enrichWg.Done()
		location := models.ResolveLocationName(input.Destination)
		guide, err := destinations.GetWikivoyageGuide(enrichCtx, location)
		if err != nil || guide == nil {
			return
		}
		planCtx := buildDestinationContext(guide)
		if planCtx != nil {
			enrichMu.Lock()
			result.Context = planCtx
			enrichMu.Unlock()
		}
	}()

	// Enrich the chosen hotel with OSM tags (stars, website, wheelchair,
	// operator) by matching nearby tourism=hotel POIs by name.
	if chosenHotel != nil && chosenHotel.Lat != 0 && chosenHotel.Lon != 0 {
		enrichWg.Add(1)
		go func(hotelName string, lat, lon float64) {
			defer enrichWg.Done()
			extra := destinations.EnrichHotelFromOSM(enrichCtx, hotelName, lat, lon)
			if extra == nil {
				return
			}
			applyOSMEnrichment(chosenHotel, extra)
		}(chosenHotel.Name, chosenHotel.Lat, chosenHotel.Lon)
	}

	enrichWg.Wait()

	if input.Currency != "" {
		convertPlanFlights(ctx, result.OutboundFlights, input.Currency)
		convertPlanFlights(ctx, result.ReturnFlights, input.Currency)
		convertPlanFlights(ctx, result.RoundTripFares, input.Currency)
		convertPlanHotels(ctx, result.Hotels, input.Currency)
	}

	// Build summary from cheapest options.
	var cheapOut, cheapRet float64
	var cheapHotel float64
	cur := choosePlanSummaryCurrency(input.Currency, result)

	if len(result.OutboundFlights) > 0 {
		cheapOut = convertedPlanAmount(ctx, result.OutboundFlights[0].Price, result.OutboundFlights[0].Currency, cur)
	}
	if len(result.ReturnFlights) > 0 {
		cheapRet = convertedPlanAmount(ctx, result.ReturnFlights[0].Price, result.ReturnFlights[0].Currency, cur)
	}
	if len(result.Hotels) > 0 {
		cheapHotel = convertedPlanAmount(ctx, result.Hotels[0].Total, result.Hotels[0].Currency, cur)
	}

	// Prefer the cheaper of {two one-ways, native single-ticket round-trip}. The
	// native RT price is ALREADY a full round-trip per person, so it competes
	// directly against cheapOut+cheapRet — no doubling. When no native fare was
	// found (or it is pricier), this is byte-identical to the two-one-way total.
	var cheapRT float64
	if len(result.RoundTripFares) > 0 {
		cheapRT = convertedPlanAmount(ctx, result.RoundTripFares[0].Price, result.RoundTripFares[0].Currency, cur)
	}
	perPersonFlights := cheaperPerPersonFlights(cheapOut+cheapRet, cheapRT)

	flightsTotal := perPersonFlights * float64(input.Guests)
	grandTotal := flightsTotal + cheapHotel

	result.Summary = PlanSummary{
		FlightsTotal: flightsTotal,
		HotelTotal:   cheapHotel,
		GrandTotal:   grandTotal,
		Currency:     cur,
	}
	if input.Guests > 0 {
		result.Summary.PerPerson = grandTotal / float64(input.Guests)
	}
	if nights > 0 {
		result.Summary.PerDay = grandTotal / float64(nights)
	}

	// A one-way plan is complete with outbound flights + hotels; a round trip
	// additionally needs the return leg. The return-flights view stays empty for
	// a one-way trip, so it is neither required for success nor reported missing.
	result.Success = len(result.OutboundFlights) > 0 && len(result.Hotels) > 0
	if roundTrip {
		result.Success = result.Success && len(result.ReturnFlights) > 0
	}

	// Collect errors.
	var errs []string
	var missing []string
	if len(result.OutboundFlights) == 0 {
		missing = append(missing, "outbound flights")
	}
	if roundTrip && len(result.ReturnFlights) == 0 {
		missing = append(missing, "return flights")
	}
	if len(result.Hotels) == 0 {
		missing = append(missing, "hotels")
	}
	if len(missing) > 0 {
		errs = append(errs, "missing "+strings.Join(missing, ", "))
	}
	if outErr != nil {
		errs = append(errs, fmt.Sprintf("outbound: %v", outErr))
	}
	if retErr != nil {
		errs = append(errs, fmt.Sprintf("return: %v", retErr))
	}
	if hotelErr != nil {
		errs = append(errs, fmt.Sprintf("hotels: %v", hotelErr))
	}
	if !result.Success && len(errs) > 0 {
		result.Error = strings.Join(errs, "; ")
	}

	return result, nil
}

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
			Price:    f.Price,
			Currency: f.Currency,
			Stops:    f.Stops,
			Duration: f.Duration,
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
			Price:    f.Price,
			Currency: f.Currency,
			Stops:    f.Stops,
			Duration: f.Duration,
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
		flights[i].Price = convertedPlanAmount(ctx, flights[i].Price, flights[i].Currency, currency)
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
