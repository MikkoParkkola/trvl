// Package hacks detects travel optimization opportunities alongside normal
// flight and route searches. Each detector is independent and runs in parallel.
//
// # Airline Pricing Fundamentals
//
// Detectors exploit systematic pricing patterns in how airlines construct fares:
//
// Airlines discount: return flights (guarantees hub traffic), connecting flights
// (competing for transfer passengers vs point-to-point LCCs), Saturday night
// stays (separates leisure from business), advance purchase (demand certainty),
// origin market pricing (purchasing power of departure country), and off-peak
// days (Tue/Wed/Sat seat fill).
//
// Airlines charge premium for: one-way (uncertainty premium), direct/nonstop
// (convenience), last-minute (desperation), hub-as-destination (business demand
// = inelastic), peak days (Fri/Sun = business), monopoly routes (no competition).
//
// Fare construction zones (IATA TC1/TC2/TC3), fare basis codes, married segments,
// and routing rules create arbitrage when the fare construction model diverges
// from actual travel patterns. Adding segments can change applicable fare rules.
// Routing via certain hubs triggers different fare zones. Rail integration adds
// additional fare zone flexibility (e.g., Belgian vs Dutch market for KLM).
//
// # Composite Hack Patterns
//
// Maximum savings come from stacking multiple arbitrage vectors:
//   - Rail fare zone + hidden city: book via rail station to hub, exit at hub, skip flight
//   - Origin market + return discount: buy round-trip from cheap origin, use only return
//   - Connecting discount + hidden city: book cheap connection, exit at expensive hub
//   - Throwaway + fare zone: buy longer route in cheaper fare zone, discard excess segments
//
// New detectors should be built by identifying which pricing fundamental they exploit.
//
// # Accommodation Pricing Fundamentals
//
// Hotels, hostels, and short-term rentals have their own systematic pricing
// patterns that create arbitrage opportunities:
//
// Hotels discount for:
//   - Long stays (fewer check-in/check-out operations, guaranteed occupancy)
//   - Weekdays in city hotels (business travel gone, rooms empty Mon-Thu)
//   - Weekends in resort/rural hotels (opposite pattern — empty Fri-Sun)
//   - Off-season (fixed costs regardless of occupancy — staff, mortgage, utilities)
//   - Advance booking (certainty of demand, revenue forecasting)
//   - Last-minute (distressed inventory — rather sell at 50% than leave empty)
//   - Loyalty members (retention + direct booking saves 15-25% OTA commission)
//   - Direct bookings (saves OTA commission — often passed as price match + perks)
//   - Package deals (flight+hotel bundles use contracted/wholesale rates)
//
// Hotels charge premium for:
//   - Short stays in peak periods (high demand, limited supply)
//   - Event dates (conferences, festivals, sports — demand spike)
//   - Refundable/flexible rates (insurance premium built into the rate)
//   - OTA bookings (15-25% commission passed to consumer or absorbed at margin loss)
//   - Single-night stays (high operational cost per check-in/checkout cycle)
//   - Room-only vs package (packages lock revenue across departments: restaurant, spa)
//   - Premium room types when standard is available (upsell margin)
//
// Airbnb / short-term rental pricing:
//   - Monthly discount (28+ nights) — often 20-50% off nightly rate (hosts prefer stability)
//   - Weekly discount (7+ nights) — 10-30% off (reduced turnover cost)
//   - New listing discount — hosts undercharge to build initial review count
//   - Superhosts charge premium — but verified quality and reliability
//   - Off-platform rebooking — returning guests book direct, save 15% Airbnb service fee
//   - Cleaning fee amortisation — fixed fee spreads across more nights on longer stays
//   - Gap-night pricing — hosts discount nights between bookings to avoid empty gaps
//
// # Accommodation Arbitrage Patterns
//
// Exploitable pricing gaps in accommodation:
//   - Accommodation split (implemented: detectAccommodationSplit) — move between
//     cheaper weekday and weekend properties in the same city
//   - Book refundable, rebook cheaper — hotels drop prices as event cancels or
//     demand softens; refundable rate is free optionality
//   - Monthly rate overstay — book 28 nights on Airbnb at monthly discount even
//     for 21-night stay (monthly discount makes it cheaper than 21 × nightly rate)
//   - OTA price match — find hotel on Booking.com, book direct for 5-15% less
//     plus loyalty points; most chains have explicit "best rate guarantee"
//   - Event date avoidance — conference in town = 2-3x hotel prices; shift dates
//     by 1-2 days or stay in adjacent city with train access
//   - Flight+hotel package — opaque/bundled rates use wholesale hotel inventory
//     not available for standalone booking; sometimes cheaper than hotel alone
//   - Status match between chains — free upgrades, breakfast, late checkout across
//     competing loyalty programs (Marriott↔Hilton, IHG↔Hyatt promotions)
//   - Hostel private room vs budget hotel — often same quality at 40-60% less;
//     Hostelworld private rooms are not dormitories
//   - Cleaning fee arbitrage — Airbnb cleaning fees are fixed regardless of stay
//     length; a €80 cleaning fee on a 1-night stay is €80/night overhead, but on
//     a 7-night stay it's €11/night; always compare total cost including fees
//
// # Cross-Domain Composite Patterns
//
// Maximum savings combine flight and accommodation arbitrage:
//   - Ferry cabin as hotel (implemented: detectFerryCabin) — overnight transport
//     replaces a hotel night entirely
//   - Night train/bus as hotel (implemented: detectNightTransport) — same concept
//     for land transport
//   - Positioning flight + cheap accommodation — fly to cheaper origin city,
//     stay one night in budget hotel, fly onward at lower fare; total still saves
//   - Destination airport + suburb hotel — fly into secondary airport (cheaper),
//     stay in suburb near that airport instead of city center
//
// # Ground Transport Pricing Fundamentals
//
// Trains discount for:
//   - Advance purchase (Sparpreis/Super Sparpreis on DB, Prems on SNCF) — 50-70% off flex
//   - Off-peak travel (avoiding morning/evening commuter peaks)
//   - Cross-border booking arbitrage — same train, different price from different
//     national railway (OBB vs DB for Vienna-Munich, CD vs DB for Prague-Berlin)
//   - Flat-rate passes (Deutschlandticket €49/mo, Klimaticket, Swiss Half Fare)
//   - Split ticketing — A→C via B as two tickets cheaper than A→C direct (UK, cross-border)
//   - Longer routes — some operators price longer routes non-linearly (book past
//     destination, exit early — no enforcement on ground transport)
//   - Return tickets — Eurostar return premium often just €5-10 over one-way
//
// Trains charge premium for:
//   - Flexible/refundable fares (2-3x advance purchase)
//   - Peak hours (morning/evening commuter slots)
//   - Mandatory seat reservations (TGV, Eurostar, some Trenitalia — €4-34 on top of ticket)
//   - Last-minute (especially on capacity-controlled high-speed routes)
//   - Single national operator booking (vs shopping across operators)
//
// Buses discount for:
//   - Advance purchase (FlixBus/RegioJet early bird)
//   - Longer routes (non-linear pricing — sometimes longer is cheaper)
//   - Off-peak days (midweek)
//   - New routes (promotional pricing to build demand)
//
// Buses charge premium for:
//   - Peak periods (holiday weekends, Friday evenings)
//   - Seat selection / extra legroom
//   - Last-minute (dynamic pricing)
//
// # Ferry Pricing Fundamentals
//
// Ferries discount for:
//   - Advance booking (cabins especially — sell out in peak season)
//   - Off-season (winter Baltic, shoulder Mediterranean)
//   - Midweek crossings (Mon-Thu cheaper than Fri-Sun)
//   - Foot passengers vs car (car deck space is the constraint)
//   - Return bookings (often barely more than one-way, like Eurostar)
//   - Loyalty programmes (Viking Line Club, Tallink Club — 10-15% off)
//   - Day cruises (same ferry, round-trip same day — tax-free shopping subsidises fare)
//
// Ferries charge premium for:
//   - Peak season (Jul-Aug on all routes, Dec/Easter on family routes)
//   - Friday/Sunday departures (weekend travel pattern)
//   - Car deck space (finite, non-expandable)
//   - Cabin upgrades (sea view, suite — high margin)
//   - Single-night weekend crossings (party/entertainment demand)
//
// Ferry arbitrage:
//   - Cabin replaces hotel night (implemented: detectFerryCabin) — transport + sleep
//   - Day cruise for shopping (Helsinki-Tallinn day return often €10-15 including tax-free)
//   - Return barely more than one-way (like Eurostar — book return even if one-way trip)
//   - Schedule-aware positioning: frequent routes (HEL-TLL every 1-2h) are flexible,
//     infrequent routes (HEL-ARN 1x/day 17:00) require schedule planning — miss it = hotel
//
// # Known Composite Patterns (user-confirmed)
//
// AMS→HEL via hidden city: book AMS→RIX via HEL on Finnair, exit at Helsinki,
// skip the HEL→RIX last leg. Helsinki as Finnair hub makes AMS→RIX cheaper
// than AMS→HEL direct because connecting traffic is discounted.
//
// KLM rail+fly + train skip: book via Antwerp (ZWE) for Belgian fare zone,
// skip train both directions (user-confirmed safe on KLM), fly directly
// from/to Schiphol. Pure fare zone arbitrage without taking any train.
//
// PRG/KRK→AMS via hidden city: book to HEL via AMS, exit at Amsterdam.
// Eastern European origin gives cheaper market pricing, connecting discount
// makes via-AMS routing cheaper than AMS-as-destination.
package hacks

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Hack represents a detected travel optimization opportunity.
type Hack struct {
	Type        string   `json:"type"`                // "throwaway", "hidden_city", "positioning", "split", "night_transport", "stopover", "date_flex", "open_jaw", "ferry_positioning", "multi_stop", "currency_arbitrage", "calendar_conflict", "tuesday_booking", "low_cost_carrier", "multimodal_skip_flight", "multimodal_positioning", "multimodal_open_jaw_ground", "multimodal_return_split", "advance_purchase", "group_split", "fare_breakpoint", "destination_airport", "fuel_surcharge", "self_transfer", "regional_pass", "departure_tax", "rail_competition", "back_to_back", "mileage_run", "day_use_hotel"
	Title       string   `json:"title"`               // human-readable hack name
	Description string   `json:"description"`         // explanation for the traveller
	Savings     float64  `json:"savings"`             // EUR saved vs naive booking
	Currency    string   `json:"currency"`            // currency for Savings
	Risks       []string `json:"risks,omitempty"`     // airline ToS, operational risks
	Steps       []string `json:"steps"`               // how to execute
	Citations   []string `json:"citations,omitempty"` // booking URLs / provider names
}

// DetectorInput carries all parameters shared across detectors.
type DetectorInput struct {
	Origin      string
	Destination string
	Date        string  // outbound YYYY-MM-DD
	ReturnDate  string  // round-trip return YYYY-MM-DD; empty = one-way search
	Currency    string  // defaults to EUR
	CarryOnOnly bool    // relevant for hidden-city (checked bags go to final dest)
	NaivePrice  float64 // baseline price for savings computation
	Passengers  int     // number of passengers (group-split fires at 3+)
}

func (in *DetectorInput) currency() string {
	if in.Currency != "" {
		return in.Currency
	}
	return "EUR"
}

// valid returns true when Origin and Destination are both non-empty.
func (in *DetectorInput) valid() bool {
	return in.Origin != "" && in.Destination != ""
}

// StopoverProgram describes an airline's free stopover offer.
type StopoverProgram struct {
	Airline      string
	Hub          string
	MaxNights    int
	Restrictions string
	URL          string
}

// stopoverPrograms is the static database of airline stopover programs.
var stopoverPrograms = map[string]StopoverProgram{
	"AY": {Airline: "Finnair", Hub: "HEL", MaxNights: 5, Restrictions: "Non-Finnish residents only", URL: "https://www.finnair.com/en/stopover"},
	"FI": {Airline: "Icelandair", Hub: "KEF", MaxNights: 7, Restrictions: "Free for transit passengers", URL: "https://www.icelandair.com/stopover"},
	"TP": {Airline: "TAP Portugal", Hub: "LIS", MaxNights: 10, Restrictions: "Free; book through TAP website", URL: "https://www.flytap.com/en-us/stopover"},
	"TK": {Airline: "Turkish Airlines", Hub: "IST", MaxNights: 2, Restrictions: "Free hotel for long layovers (TourIST program)", URL: "https://www.turkishairlines.com/en-int/any-questions/fly-and-smile/"},
	"QR": {Airline: "Qatar Airways", Hub: "DOH", MaxNights: 4, Restrictions: "Doha Stopover from +1 USD", URL: "https://www.qatarairways.com/en/destinations/qatar/doha-stopover.html"},
	"EK": {Airline: "Emirates", Hub: "DXB", MaxNights: 4, Restrictions: "Dubai Connect program", URL: "https://www.emirates.com/english/destinations/dubai/stopover/"},
	"SQ": {Airline: "Singapore Airlines", Hub: "SIN", MaxNights: 3, Restrictions: "Singapore Stopover Holiday program", URL: "https://www.singaporeair.com/en_UK/us/promotions/stopover-holiday/"},
	"EY": {Airline: "Etihad", Hub: "AUH", MaxNights: 2, Restrictions: "Abu Dhabi Stopover program", URL: "https://www.etihad.com/en-us/destinations/united-arab-emirates/abu-dhabi/stopover"},
}

// detectFn is the signature for individual hack detectors.
type detectFn func(ctx context.Context, in DetectorInput) []Hack

// DetectAll runs all detectors in parallel and returns every hack found.
// It respects ctx cancellation; detectors that finish after cancellation
// are discarded.
func DetectAll(ctx context.Context, in DetectorInput) []Hack {
	detectors := []detectFn{
		detectThrowaway,
		detectHiddenCity,
		detectPositioning,
		detectSplit,
		detectNightTransport,
		detectStopover,
		detectDateFlex,
		detectOpenJaw,
		detectFerryPositioning,
		detectMultiStop,
		detectCurrencyArbitrage,
		detectCalendarConflict,
		detectTuesdayBooking,
		detectLowCostCarrier,
		detectMultiModalSkipFlight,
		detectMultiModalPositioning,
		detectMultiModalOpenJawGround,
		detectMultiModalReturnSplit,
		detectAdvancePurchase,
		detectGroupSplit,
		detectRailFlyArbitrage,
		detectFareBreakpoint,
		detectDestinationAirport,
		detectThrowawayGround,
		detectEurostarReturn,
		detectCrossBorderRail,
		detectFerryCabin,
		detectEU261,
		detectSelfTransfer,
		detectRegionalPass,
		detectDepartureTax,
		detectRailCompetition,
		detectBackToBack,
		detectMileageRun,
		detectDayUse,
		detectErrorFare,
	}

	// Each detector gets a child context with a per-detector timeout so a
	// slow API call cannot block the entire hacks response.
	const detectorTimeout = 20 * time.Second

	type result struct {
		hacks []Hack
	}

	ch := make(chan result, len(detectors))
	var wg sync.WaitGroup

	for _, fn := range detectors {
		fn := fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			dCtx, cancel := context.WithTimeout(ctx, detectorTimeout)
			defer cancel()
			h := fn(dCtx, in)
			ch <- result{hacks: h}
		}()
	}

	// Close channel once all goroutines complete.
	go func() {
		wg.Wait()
		close(ch)
	}()

	var all []Hack
	for r := range ch {
		all = append(all, r.hacks...)
	}
	return dedupHacks(all)
}

// dedupHacks removes hacks that are functionally identical. Two hacks are
// considered duplicates when they share the same Type, From/To airports (derived
// from their Steps), and a savings amount within EUR 5 of each other. When
// duplicates are found the one with more Steps (more detail) is kept.
func dedupHacks(hacks []Hack) []Hack {
	if len(hacks) <= 1 {
		return hacks
	}

	// extractKey returns a normalised signature for a hack. We use Type +
	// savings-bucket (rounded to nearest 5) + final destination airport derived
	// from the last Step that contains an IATA-like token (3 uppercase letters).
	extractKey := func(h Hack) string {
		bucket := math.Round(h.Savings/5) * 5
		// Find the last step that mentions a 3-letter uppercase airport code.
		airport := ""
		for _, s := range h.Steps {
			words := strings.Fields(s)
			for _, w := range words {
				// Strip punctuation
				clean := strings.Trim(w, "()[].,:-→>")
				if len(clean) == 3 && clean == strings.ToUpper(clean) {
					airport = clean
				}
			}
		}
		return fmt.Sprintf("%s|%.0f|%s", h.Type, bucket, airport)
	}

	seen := make(map[string]int) // key → index in result slice
	result := make([]Hack, 0, len(hacks))

	for _, h := range hacks {
		key := extractKey(h)
		if idx, exists := seen[key]; exists {
			// Keep the more detailed hack (more Steps).
			if len(h.Steps) > len(result[idx].Steps) {
				result[idx] = h
			}
		} else {
			seen[key] = len(result)
			result = append(result, h)
		}
	}
	return result
}

// DetectFlightTips runs a curated subset of zero-API-call detectors suitable
// for auto-triggering after a flight search. It returns hacks for:
// advance_purchase, fare_breakpoint, destination_airport, and group_split.
// Fuel surcharge requires airline codes and is handled separately via
// DetectFuelSurcharge.
func DetectFlightTips(ctx context.Context, in DetectorInput) []Hack {
	detectors := []detectFn{
		detectAdvancePurchase,
		detectFareBreakpoint,
		detectDestinationAirport,
		detectGroupSplit,
		detectDepartureTax,
		detectErrorFare,
	}

	var all []Hack
	for _, fn := range detectors {
		if h := fn(ctx, in); len(h) > 0 {
			all = append(all, h...)
		}
	}
	return all
}

// roundSavings rounds to the nearest integer for display.
func roundSavings(v float64) float64 {
	return math.Round(v)
}
