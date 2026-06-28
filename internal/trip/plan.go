// Package trip implements trip planning by combining flight and hotel searches.
package trip

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dailyspend"
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
	// Budget is the all-in ceiling for the whole trip in Currency. Zero means
	// "no budget set": the planner ranks normally and never reports over-budget.
	Budget float64
}

// PlanFlight is a flight option in the trip plan.
type PlanFlight struct {
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	// ComparablePrice is the all-in fare including unavoidable baggage fees
	// (mirrors models.FlightResult.ComparablePrice). Equals Price when no bag
	// surcharge applies. The plan's GrandTotal is summed from this, not the
	// headline Price, so the quoted total is what a traveller actually pays.
	ComparablePrice float64 `json:"comparable_price,omitempty"`
	Airline         string  `json:"airline"`
	Flight          string  `json:"flight_number"`
	Stops           int     `json:"stops"`
	Duration        int     `json:"duration_min"`
	Departure       string  `json:"departure"`
	Arrival         string  `json:"arrival"`
	Route           string  `json:"route"`
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
	// BaggageTotal is the unavoidable baggage surcharge folded into the flight
	// quote (all-in flight cost minus headline fares). Itemised so the user can
	// see what bags add. GrandTotal == FlightsTotal + BaggageTotal + HotelTotal.
	BaggageTotal float64 `json:"baggage_total"`
	HotelTotal   float64 `json:"hotel_total"`
	// MealsTotal is the estimated on-the-ground spend (meals, local transport,
	// incidentals) for the whole party over the stay, from the bundled offline
	// daily-spend index. It is an ESTIMATE, never a live quote — MealsEstimated
	// is set whenever it is included so callers can label it. Folding it in is
	// what makes GrandTotal a no-surprise landed cost, not just flights + hotel.
	// GrandTotal == FlightsTotal + BaggageTotal + HotelTotal + MealsTotal +
	// TransfersTotal + TaxesTotal.
	MealsTotal     float64 `json:"meals_total"`
	MealsEstimated bool    `json:"meals_estimated"`
	// TransfersTotal is the airport<->city round-trip cost for the whole party,
	// from the bundled offline transfer table. TransfersEstimated is true when a
	// curated figure was found; when false the amount is zero (a typed not-found
	// status, never a fabricated number) per the MIK-6530 fail-fast contract.
	TransfersTotal     float64 `json:"transfers_total"`
	TransfersEstimated bool    `json:"transfers_estimated"`
	// TaxesTotal is the tourist/city tax for the whole party over the stay, from
	// the bundled offline city-tax table. Same typed-not-found semantics as
	// transfers via TaxesEstimated.
	TaxesTotal     float64 `json:"taxes_total"`
	TaxesEstimated bool    `json:"taxes_estimated"`
	GrandTotal     float64 `json:"grand_total"`
	PerPerson      float64 `json:"per_person"`
	PerDay         float64 `json:"per_day"`
	Currency       string  `json:"currency"`
	// Budget echoes the requested ceiling (PlanInput.Budget). OverBudget is true
	// when no package fits under it; Overage is how much the cheapest total
	// exceeds it, and BudgetMessage carries the explicit "no package fits"
	// sentence. All zero/false when no budget was set.
	Budget        float64 `json:"budget,omitempty"`
	OverBudget    bool    `json:"over_budget,omitempty"`
	Overage       float64 `json:"overage,omitempty"`
	BudgetMessage string  `json:"budget_message,omitempty"`
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
	// Enrichment carries the typed, never-failing per-package weather/holiday/
	// event status (MIK-6530 PLANCOMP.3). Each source reports ok / none /
	// unavailable / not_configured so the renderer is honest about what was and
	// wasn't reachable rather than silently omitting a missing feed.
	Enrichment PackageEnrichment `json:"enrichment"`
	Summary    PlanSummary       `json:"summary"`
	Error      string            `json:"error,omitempty"`

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

	// PLANCOMP.3: per-package weather + public holidays for the exact date
	// window, plus events when the opt-in key is set. Best-effort and typed —
	// each source degrades to an honest status, never a hard failure, so a quiet
	// feed leaves a label rather than a blank.
	enrichWg.Add(1)
	go func() {
		defer enrichWg.Done()
		location := models.ResolveLocationName(input.Destination)
		dates := models.DateRange{CheckIn: input.DepartDate, CheckOut: input.ReturnDate}
		info := destinations.EnrichBestEffort(enrichCtx, location, dates)
		eventsKeyOn := os.Getenv("TICKETMASTER_API_KEY") != ""
		var events []models.Event
		if eventsKeyOn {
			events, _ = destinations.GetEvents(enrichCtx, location, input.DepartDate, input.ReturnDate)
		}
		enrichment := classifyEnrichment(info, events, eventsKeyOn)
		enrichMu.Lock()
		result.Enrichment = enrichment
		enrichMu.Unlock()
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

	// Build summary from cheapest options. Two parallel figures per leg: the
	// headline fare (Price) and the all-in fare (ComparablePrice, incl. bags).
	var cheapOut, cheapRet float64     // all-in (baggage-inclusive)
	var cheapOutHl, cheapRetHl float64 // headline fares
	var cheapHotel float64
	cur := choosePlanSummaryCurrency(input.Currency, result)

	if len(result.OutboundFlights) > 0 {
		cheapOut = convertedPlanAmount(ctx, comparableOrPrice(result.OutboundFlights[0]), result.OutboundFlights[0].Currency, cur)
		cheapOutHl = convertedPlanAmount(ctx, result.OutboundFlights[0].Price, result.OutboundFlights[0].Currency, cur)
	}
	if len(result.ReturnFlights) > 0 {
		cheapRet = convertedPlanAmount(ctx, comparableOrPrice(result.ReturnFlights[0]), result.ReturnFlights[0].Currency, cur)
		cheapRetHl = convertedPlanAmount(ctx, result.ReturnFlights[0].Price, result.ReturnFlights[0].Currency, cur)
	}
	if len(result.Hotels) > 0 {
		cheapHotel = convertedPlanAmount(ctx, result.Hotels[0].Total, result.Hotels[0].Currency, cur)
	}

	// Prefer the cheaper of {two one-ways, native single-ticket round-trip}. The
	// native RT price is ALREADY a full round-trip per person, so it competes
	// directly against cheapOut+cheapRet -- no doubling. When no native fare was
	// found (or it is pricier), this is byte-identical to the two-one-way total.
	// The cheaper-of decision is made on the all-in cost (what a traveller really
	// pays), and the matching headline figure is tracked so the baggage delta is
	// consistent with the branch we actually chose.
	var cheapRT, cheapRTHl float64
	if len(result.RoundTripFares) > 0 {
		cheapRT = convertedPlanAmount(ctx, comparableOrPrice(result.RoundTripFares[0]), result.RoundTripFares[0].Currency, cur)
		cheapRTHl = convertedPlanAmount(ctx, result.RoundTripFares[0].Price, result.RoundTripFares[0].Currency, cur)
	}

	oneWayAllIn := cheapOut + cheapRet
	perPersonAllIn := oneWayAllIn
	perPersonHeadline := cheapOutHl + cheapRetHl
	if cheapRT > 0 && cheapRT < oneWayAllIn {
		perPersonAllIn = cheapRT
		perPersonHeadline = cheapRTHl
	}

	flightsHeadline := perPersonHeadline * float64(input.Guests)
	flightsAllIn := perPersonAllIn * float64(input.Guests)
	baggageTotal := flightsAllIn - flightsHeadline
	if baggageTotal < 0 {
		baggageTotal = 0 // never let a stale comparable under-report fares
	}
	// On-the-ground daily spend (meals, local transport, incidentals). This is a
	// coarse offline estimate, never a live quote, so it is always tagged via
	// MealsEstimated. Folding it in makes GrandTotal a no-surprise landed cost
	// rather than just flights + hotel.
	meals := dailyspend.Lookup(models.ResolveLocationName(input.Destination))
	mealsTotal := convertedPlanAmount(ctx, meals.Total(input.Guests, nights), meals.Currency, cur)
	// Airport<->city transfer and tourist/city tax: real money a flight+hotel
	// quote hides. Both from bundled offline tables; a city not in the table
	// degrades to a typed not-found status (zero + Estimated=false) rather than a
	// fabricated figure (MIK-6530 PLANCOMP.1).
	transfersTotal, transfersKnown := transferCostConverted(ctx, input.Destination, input.Guests, cur)
	taxesTotal, taxesKnown := cityTaxConverted(ctx, input.Destination, input.Guests, nights, cur)
	grandTotal := flightsAllIn + cheapHotel + mealsTotal + transfersTotal + taxesTotal

	result.Summary = PlanSummary{
		FlightsTotal:       flightsHeadline,
		BaggageTotal:       baggageTotal,
		HotelTotal:         cheapHotel,
		MealsTotal:         mealsTotal,
		MealsEstimated:     mealsTotal > 0,
		TransfersTotal:     transfersTotal,
		TransfersEstimated: transfersKnown,
		TaxesTotal:         taxesTotal,
		TaxesEstimated:     taxesKnown,
		GrandTotal:         grandTotal,
		Currency:           cur,
	}
	// PLANCOMP.2: if a budget was set and nothing fits under it, say so plainly,
	// carrying the cheapest total and the overage.
	result.Summary.Budget = input.Budget
	result.Summary.OverBudget, result.Summary.Overage, result.Summary.BudgetMessage = budgetVerdict(grandTotal, input.Budget, cur)
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
