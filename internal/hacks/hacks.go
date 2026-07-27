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
	"sync/atomic"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
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

	// RailCost, when non-zero, is the explicit cost (in RailCostCurrency) of the
	// ground/rail leg a multimodal hack depends on. For airline-bundled rail
	// (e.g. KLM Air&Rail) this is 0 with RailCostNote explaining it is included.
	RailCost float64 `json:"rail_cost,omitempty"`
	// RailCostCurrency is the currency for RailCost (defaults to EUR).
	RailCostCurrency string `json:"rail_cost_currency,omitempty"`
	// RailProvider names the operator the RailCost was quoted from (e.g.
	// "Eurostar", "Deutsche Bahn") or "" when bundled/estimated.
	RailProvider string `json:"rail_provider,omitempty"`
	// RailCostEstimated is true when RailCost is a conservative internal estimate
	// rather than a live provider quote — surfaced so the saving stays honest.
	RailCostEstimated bool `json:"rail_cost_estimated,omitempty"`
	// RailCostNote is a short human-readable qualifier for the rail cost
	// (e.g. "included in airline ticket", "live quote", "estimate").
	RailCostNote string `json:"rail_cost_note,omitempty"`

	// Bundle, when set, is the composed multi-leg itinerary (rail leg +
	// flight leg + return) priced as a single total. It carries per-leg timing
	// plus the change window and connection-guarantee status so a multimodal
	// hack is auditable end-to-end. See rail_fly_bundle.go.
	Bundle *RailFlyBundle `json:"rail_fly_bundle,omitempty"`

	// ConcreteCandidates holds zero or more real FlightResult values returned
	// by an actual provider search performed inside this detector (e.g. the
	// cheapest itinerary(s) from a rail-station origin for rail+fly). These
	// are safe to inject into a search result's Flights list as bookable
	// ranked candidates. Advisory-only hacks leave this empty. Never invent.
	ConcreteCandidates []models.FlightResult `json:"-"`
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

	// Loyalty carries the traveller's frequent-flyer programmes so that
	// loyalty-aware detectors (mileage run, back-to-back) can prefer or filter
	// to opportunities relevant to the user's actual alliances and status.
	// The zero value preserves pre-loyalty behaviour (no regression): detectors
	// fall back to surfacing every opportunity. See loyalty.go.
	Loyalty LoyaltyProfile

	// SearchOverride, when non-nil, is threaded into every flights.SearchOptions
	// this detector builds so tests can inject synthetic search results. Nil
	// (the default) runs the real flights.SearchFlights. This replaces the old
	// pattern of mutable package-level function variables (positioningSearchFunc,
	// openJawSearchFunc), which raced when parallel detector or test calls read
	// and wrote the same shared global; carrying the override as ordinary
	// per-call data instead removes the shared mutable state entirely. See
	// flights.SearchOptions.SearchOverride.
	SearchOverride flights.SearchFunc

	// GroundSearchOverride, when non-nil, is threaded into every
	// ground.SearchOptions this detector builds so tests can inject synthetic
	// ground-transport (ferry/bus/train) results. Nil (the default) runs the
	// real ground.SearchByName. Mirrors SearchOverride above for the ground
	// package. See ground.SearchOptions.SearchOverride.
	GroundSearchOverride ground.SearchFunc
}

// currency returns the display currency for this search: the explicit request
// currency if set, else the user's saved profile preference
// (preferences.DisplayCurrency), else EUR as a last-resort fallback.
func (in *DetectorInput) currency() string {
	if in.Currency != "" {
		return in.Currency
	}
	if p, err := preferences.Load(); err == nil && p != nil && p.DisplayCurrency != "" {
		return p.DisplayCurrency
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

// allDetectors returns the full registered set of detectors that DetectAll runs
// in parallel. It is the single source of truth for the detector roster:
// RegisteredDetectorCount() reports len(allDetectors()), and the public docs
// detector count is pinned to it by a tripwire test so a claim can never drift
// from the implementation.
// sweepTimeout caps how long DetectAll waits for the whole roster, independently
// of any deadline the caller supplied. Set above the per-detector timeout so a
// cooperative detector still gets its full allowance; the margin exists only to
// stop an uncooperative one holding the response open forever.
//
// Atomic because a test that shrinks it does so while goroutines from an earlier
// abandoned sweep may still be live. The same reasoning as the currency seam: a
// straggler is not under the writing test's control, so sequencing writes is no
// defence. Production never reassigns it.
var sweepTimeoutNanos atomic.Int64

func init() {
	sweepTimeoutNanos.Store(int64(25 * time.Second))
	detectorTimeoutNanos.Store(int64(20 * time.Second))
}

// detectorTimeoutNanos bounds a single detector. Atomic for the same reason as
// the other seams here: a test that shrinks it does so while goroutines from an
// earlier sweep may still be reading.
var detectorTimeoutNanos atomic.Int64

func currentDetectorTimeout() time.Duration {
	return time.Duration(detectorTimeoutNanos.Load())
}

func setDetectorTimeout(d time.Duration) {
	detectorTimeoutNanos.Store(int64(d))
}

func currentSweepTimeout() time.Duration {
	return time.Duration(sweepTimeoutNanos.Load())
}

func setSweepTimeout(d time.Duration) {
	sweepTimeoutNanos.Store(int64(d))
}

// detectorRoster is the seam tests use to supply deterministic detectors.
// Production always uses the real roster; only tests reassign it, so that a test
// of DetectAll's own control flow does not depend on live providers.
//
// Atomic for the same reason as the currency seam and the sweep timeout: a test's
// cleanup restores it while detector goroutines from an abandoned sweep can still
// be reading, and those goroutines are not under the test's control.
type rosterFn func() []detectFn

var detectorRosterSeam atomic.Pointer[rosterFn]

func init() {
	setDetectorRoster(allDetectors)
}

func currentDetectorRoster() rosterFn {
	return *detectorRosterSeam.Load()
}

func setDetectorRoster(fn rosterFn) {
	detectorRosterSeam.Store(&fn)
}

func allDetectors() []detectFn {
	return []detectFn{
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
}

// RegisteredDetectorCount reports how many detectors DetectAll runs. It is the
// authoritative number for public "N detectors" claims; keep marketing docs in
// sync with this value (a tripwire test enforces it).
func RegisteredDetectorCount() int {
	return len(allDetectors())
}

// DetectAll runs every detector in parallel and returns the hacks they found,
// plus whether the sweep actually finished.
//
// The second return value is not decoration. When the sweep ends early
// DetectAll returns what arrived, and a caller that cannot tell that apart from
// a completed sweep will present a truncated list as the whole answer — which is
// precisely the "empty result dressed up as nothing found" this project refuses
// to do elsewhere. Callers must surface false.
//
// A cancelled or already-expired ctx short-circuits before any detector is
// dispatched: several detectors make live provider calls (Google Flights,
// Kiwi, ground transport providers such as rome2rio/ferryhopper/skiplagged),
// and there is no point paying for a network round trip whose result nobody
// can use. This keeps a cancelled MCP request response-fast and keeps the
// default (offline) test suite free of live provider traffic.
//
// A detector that finishes after DetectAll has returned is not reported: its
// result lands in a buffered channel nobody reads. A result already buffered
// when the bail-out drains is reported; one that arrives after the drain has
// looked is not, because the drain takes what is there and does not wait.
// Neither is thereby reclaimed, which is a distinction the note above the sweep
// timer states precisely.
func DetectAll(ctx context.Context, in DetectorInput) (hacks []Hack, complete bool) {
	// Fast path: ctx is already done (cancelled or deadline already passed).
	// Don't launch a single detector goroutine.
	if ctx.Err() != nil {
		return nil, false
	}

	detectors := currentDetectorRoster()()

	// Each detector gets a child context with a per-detector timeout so a
	// slow API call cannot block the entire hacks response. context.WithTimeout
	// inherits ctx's deadline if it is earlier than detectorTimeout, so a
	// tight parent deadline still propagates into every detector's HTTP path.

	ch := make(chan detectorResult, len(detectors))
	var wg sync.WaitGroup
	dispatched := 0

	for _, fn := range detectors {
		// ctx can expire mid fan-out (e.g. a parent deadline of a few ms
		// elapses while we're still iterating the detector roster). Stop
		// dispatching further detectors the moment that happens rather than
		// firing off calls that are already doomed to fail.
		if ctx.Err() != nil {
			break
		}
		dispatched++
		fn := fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Defensive re-check: ctx may expire between the dispatch-loop
			// check above and this goroutine actually getting scheduled.
			if ctx.Err() != nil {
				return
			}
			dCtx, cancel := context.WithTimeout(ctx, currentDetectorTimeout())
			defer cancel()
			h := fn(dCtx, in)
			// cutShort is read after the detector returns, and that reading is
			// deliberately conservative rather than exact. A detector that finished
			// normally a moment before its allowance expired is recorded as cut
			// short, because from out here the two are indistinguishable: there is
			// no instant at which "still running" can be observed atomically with
			// "the context is done".
			//
			// The bias is the safe direction. It can call a finished sweep partial;
			// it can never call a truncated sweep complete. What it does mean is
			// that no caller may be told the sweep ended before its detectors did,
			// because sometimes they had all finished. Both surfaces therefore say
			// only that not every detector was confirmed to finish, which is exactly
			// what this value supports.
			ch <- detectorResult{hacks: h, cutShort: dCtx.Err() != nil}
		}()
	}

	// Close channel once all goroutines complete.
	go func() {
		wg.Wait()
		close(ch)
	}()

	// Collect until the detectors are done OR the caller's deadline passes,
	// whichever comes first.
	//
	// Ranging over the channel alone was not enough. A detector that has already
	// started cannot be interrupted mid-computation by a context — cancellation
	// is cooperative, and a detector doing synchronous work only notices at its
	// next check. Waiting for all of them to notice meant a caller asking for
	// results within 1ms waited a measured 1m0.30s, so DetectAll silently broke
	// the contract its own per-detector timeouts were there to keep.
	//
	// What abandoning them does and does not cost, stated precisely, because the
	// earlier version of this comment claimed more than it could:
	//
	// A detector that FINISHES after the caller has gone gets its send and its
	// goroutine bounded, and that is the whole of the claim. ch is buffered to
	// len(detectors), so the send completes whether or not anyone is reading, and
	// the goroutine exits. What is NOT claimed is that the result goes away: if a
	// sibling detector is still stuck, the goroutine waiting to close ch is stuck
	// with it, and it holds ch alive along with every payload buffered in it. So a
	// late result is unread rather than reclaimed, and one abandoned sweep retains
	// a channel plus its contents for as long as the stuck sibling runs. Bounding
	// the goroutine is not the same as freeing the memory, and an earlier version
	// of this comment claimed both.
	//
	// A detector stuck INSIDE its own function could not be bounded by anything
	// here. Cancellation is cooperative and Go cannot terminate a goroutine, so one
	// that ignored its context would keep running and hold whatever it holds.
	// Capping concurrency would not help: a permanently stuck detector would hold a
	// slot forever and turn slow accumulation into a total stall.
	//
	// That case is nearly closed, and what remains was audited rather than assumed
	// (#520). Of the 36 detectors, 17 are pure computation over already-fetched
	// data, importing nothing that can perform I/O. The other 19 bottom out in an
	// http.Client carrying its own Timeout, which fires whether or not the context
	// is honoured: 32 client literals across the flights, ground, batchexec,
	// destinations, preferences and hacks packages, every one with a Timeout on the
	// client rather than only inside its Transport, checked by walking each
	// literal's own fields so that IdleConnTimeout cannot be mistaken for it.
	//
	// One path escapes that, and it is the one from #507. A flight detector reaching
	// the default round-trip merge constructs the AF-KLM provider, which resolves
	// its credential with context.Background() and shells out to `security` and
	// `op` under no deadline at all; `op` blocks indefinitely when it has a terminal
	// to prompt on. So a detector CAN sit inside its own function forever today, and
	// this bound is what stops it taking DetectAll and the caller down with it.
	// #515 closes the path itself by reading that credential from the environment.
	//
	// Everywhere else the residual is latency, not accumulation. A straggler can
	// outlive this sweep by up to its own client timeout, longest where a 30s ground
	// client sits under a 20s per-detector allowance, and then it exits. Adding a
	// detector whose I/O carries no timeout of its own would widen the exception,
	// which is why the audit is worth rerunning when one is added.
	//
	// Bound the sweep itself, not just each detector.
	//
	// The per-detector timeout only reaches the detector; it does not stop the
	// collector waiting. A caller with no deadline of its own — an MCP request
	// that sets none — plus a single detector that ignores cancellation left
	// DetectAll blocked forever. Cancellation being cooperative means we cannot
	// make that detector stop; it does not mean the response has to wait for it.
	sweep := time.NewTimer(currentSweepTimeout())
	defer sweep.Stop()

	return collectSweep(ch, ctx.Done(), sweep.C, dispatched, len(detectors))
}

// collectSweep gathers detector results until the set is complete, the caller's
// context ends, or the sweep's own bound fires. It is a function rather than an
// inline loop so the two bail-out paths can be driven directly from a test: an
// already-closed done channel makes the bail-out ready at the first select, and
// a pre-filled ch makes the delivery case ready at the same moment, which is the
// interleaving that matters and the one the scheduler will not reproduce on
// request.
//
// dispatched is how many detectors were actually started and total how many were
// registered. Both are needed: a detector dispatched just before cancellation can
// return without sending, so counting deliveries against dispatches alone would
// call a sweep complete when one detector never ran.
func collectSweep(ch <-chan detectorResult, done <-chan struct{}, sweep <-chan time.Time, dispatched, total int) ([]Hack, bool) {
	var all []Hack
	received := 0
	anyCutShort := false

	// finish assembles the return value for a bail-out path. It exists so both
	// paths compute completeness the same way the closed-channel path does, from
	// what actually arrived, instead of assuming the worst because a timer fired.
	finish := func(collected []Hack, drained []Hack) ([]Hack, bool) {
		return dedupHacks(append(collected, drained...)),
			received == dispatched && dispatched == total && !anyCutShort
	}

	for {
		select {
		case r, ok := <-ch:
			if !ok {
				// Every dispatched detector must have delivered, and every
				// detector must have been dispatched. Counting dispatches alone
				// was wrong: a detector dispatched just before cancellation hits
				// its own defensive check, returns without sending, and the
				// channel closes with the sweep looking complete when one
				// detector never ran at all.
				return dedupHacks(all), received == dispatched && dispatched == total && !anyCutShort
			}
			received++
			if r.cutShort {
				anyCutShort = true
			}
			all = append(all, r.hacks...)
		case <-done:
			// Return what arrived rather than nothing: partial results beat an
			// empty answer, and this matches how the rest of trvl degrades when a
			// provider is slow. Draining first is what stops a result that was
			// already delivered from being thrown away when select happens to pick
			// this case over the channel.
			//
			// Completeness is then recomputed rather than assumed false. Reaching
			// this case does not prove anything is missing: select picks at random
			// among ready cases, so cancellation can arrive at the same moment as
			// the last detector's result, and the drain above may have collected
			// every one of them. Hardcoding false there told the caller the sweep
			// ended before its detectors did when they had all finished, and both
			// the CLI and the MCP tool repeat that sentence to a human.
			return finish(all, drainBuffered(ch, &received, &anyCutShort))
		case <-sweep:
			// No caller deadline, and something is not coming back. Same contract,
			// and the same reasoning about recomputing rather than assuming: the
			// timer firing does not prove a result was still outstanding.
			return finish(all, drainBuffered(ch, &received, &anyCutShort))
		}
	}
}

// detectorResult is one detector's contribution to a sweep. It is declared at
// package level rather than inside DetectAll so drainBuffered can be a plain
// function with its own test, instead of a closure reachable only by driving the
// whole sweep and hoping the scheduler cooperates.
type detectorResult struct {
	hacks []Hack
	// cutShort reports that this detector's own deadline fired rather than the
	// detector finishing. Delivery alone was not enough: a timed-out detector
	// still sends whatever it had, so counting deliveries reported the sweep
	// complete while one detector had in fact been cut off.
	cutShort bool
}

// drainBuffered takes everything already sitting in ch and returns without
// waiting for anything more.
//
// Both of DetectAll's bail-out paths need this. A select with more than one
// ready case picks at random, so when the deadline lands at the same moment as a
// detector's send, the timer case can win while finished results are already
// buffered. Returning there would throw away work that was done and delivered,
// which makes the answer wrong rather than merely incomplete, and keeping those
// two apart is the whole purpose of the partial flag. The accounting is passed
// in so a drained result still counts toward received and can still mark the
// sweep cut short.
//
// Anything not yet buffered is what makes the sweep partial, and waiting for it
// is precisely what the caller's deadline forbids, so the default case returns
// immediately.
func drainBuffered(ch <-chan detectorResult, received *int, anyCutShort *bool) []Hack {
	var extra []Hack
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				return extra
			}
			*received++
			if r.cutShort {
				*anyCutShort = true
			}
			extra = append(extra, r.hacks...)
		default:
			return extra
		}
	}
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
