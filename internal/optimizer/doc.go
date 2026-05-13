// Package optimizer implements the unified trip optimization engine.
//
// Instead of detecting hacks AFTER a search, the optimizer uses all pricing
// primitives to GUIDE the search — generating candidate booking strategies
// from composable primitives, executing minimal API calls, and returning
// the optimal combination ranked by all-in cost.
//
// # Architecture
//
// The optimizer works in 4 phases:
//
//  1. EXPAND: Given user intent (origin, destination, dates, constraints),
//     generate all candidate search parameters from applicable primitives:
//     - Alternative origins (positioning, rail+fly stations, ferry connections)
//     - Alternative destinations (nearby airports + ground transport)
//     - Date flexibility (±N days, cheapest within range)
//     - Trip structure (round-trip, split airlines, nested returns)
//     - Hidden city candidates (via airline hubs beyond destination)
//     - Transport mode alternatives (flight, train, bus, ferry, combinations)
//
//  2. SEARCH: Execute candidate searches with an API call budget.
//     Prioritize zero-cost checks first (static hacks), then cheapest API
//     calls (CalendarGraph for date ranges), then targeted searches.
//     Use shared HTTP clients and respect rate limits.
//
//  3. PRICE: Apply all-in cost adjustments to raw search results:
//     - Add LCC baggage fees (carry-on + checked bag)
//     - Subtract FF status benefits (alliance membership)
//     - Add ground transport costs for positioning/destination alternatives
//     - Account for hotel savings from night transport/ferry cabins
//     - Currency conversion to user's display currency
//
//  4. RANK: Sort candidates by total all-in cost (flights + ground + hotels
//     + bags - FF benefits). Present top N options with explanation of which
//     primitives were applied and what the savings are vs naive booking.
//
// # Pricing Primitives (the building blocks)
//
// Each primitive is a function that transforms search parameters:
//
//	Return discount:     search RT instead of 2x one-way
//	Connecting discount: search via hubs (AMS, FRA, IST)
//	Market pricing:      search from cheaper origin country
//	Fare zone arbitrage: search from rail stations (ZWE, QKL)
//	Date flexibility:    search ±N days around target
//	Alternative origin:  search from nearby airports/ports
//	Alternative dest:    search to nearby airports + ground
//	Split airlines:      search one-way per direction independently
//	Hidden city:         search to beyond-destination via hub
//	Transport mode:      search train/bus/ferry as flight replacement
//	Night transport:     search overnight options that save hotel
//	Advance purchase:    advisory on booking timing
//	Group split:         search as 1 pax when group ≥3
//	Throwaway segment:   search longer route if cheaper
//	Nested returns:      for multi-trip, swap return legs
//
// # Constraints
//
//   - API call budget: max N searches per optimization (default 20)
//   - Time budget: max T seconds (default 30)
//   - User preferences: FF status, luggage needs, comfort level
//   - Hard constraints: latest arrival, earliest departure, max stops
//   - Soft constraints: preferred airlines, direct preference, budget
//
// # User-Confirmed Composite Patterns
//
//	KLM Antwerp: book via ZWE for Belgian fare zone, skip train both ways
//	Finnair hidden city: AMS→RIX via HEL, exit at Helsinki (hub discount)
//	PRG/KRK→AMS: book to HEL via AMS, exit at Amsterdam (market pricing + hidden city)
//	Nested returns: 2 trips same route, swap return legs for RT discount
//	Ferry+flight: TLL as HEL alternative (ferry 2h, €30, flexible schedule)
//
// # Implementation Plan
//
//	Phase 1: OptimizeTrip function — expand origins/destinations/dates, search
//	         in parallel, apply all-in pricing, rank results
//	Phase 2: MCP tool optimize_booking — expose to AI agents
//	Phase 3: CLI command trvl optimize — user-facing with interactive output
//	Phase 4: Constraint solver — handle complex multi-city itineraries
package optimizer
