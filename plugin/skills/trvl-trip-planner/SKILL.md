---
name: trvl-trip-planner
description: Plan a trip with trvl using traveller preferences, real-time flights, hotels, and viability assessment.
triggers:
  - trip plan
  - plan trip
  - vacation
  - holiday
  - weekend getaway
allowed-tools:
  - mcp__trvl__travel
  - mcp__trvl__get_preferences
  - mcp__trvl__interview_trip
  - mcp__trvl__plan_trip
  - mcp__trvl__search_flights
  - mcp__trvl__search_hotels
  - mcp__trvl__search_hotels_with_details
  - mcp__trvl__assess_trip
  - mcp__trvl__detect_travel_hacks
  - mcp__gateway__gateway_invoke
---

# trvl Trip Planner

Use this skill when the user wants a vacation, holiday, weekend getaway, or
general trip plan.

## Inputs

Collect only missing facts:

- Origin or home airport.
- Destination city or airport.
- Departure and return dates, or a flexible window.
- Traveller count, budget, cabin, bags, and hotel constraints when relevant.

Always load preferences first through `travel` with `intent="get_preferences"`
or by calling the compatibility alias directly. Do not re-ask values already
present in the traveller profile. If the request is underspecified, ask at most
three questions or call `interview_trip` when available.

## Workflow

1. Normalize airports, dates, guests, currency, and bag posture from the user
   request plus profile.
2. Call `travel` with `intent="plan_trip"` and `params` containing `origin`,
   `destination`, `depart_date`, `return_date`, `guests`, and `currency`.
3. Use `travel` with `intent="search_flights"` when the user needs more flight options, a specific
   airline/alliance/cabin, direct-only filtering, baggage-aware pricing, or a
   cheaper fallback than `plan_trip` returned.
4. Use `travel` with `intent="search_hotels_with_details"` when comparing a
   short hotel list by rooms, rates, and amenities; otherwise use
   `intent="search_hotels"` for broad hotel alternatives, district filtering,
   stars/rating constraints, amenities, or max-price filtering.
5. Use `travel` with `intent="assess_trip"` and dates/passport when nationality
   is known or visa/weather viability matters.
6. Use `travel` with `intent="detect_travel_hacks"` after flight search unless
   the user explicitly says not to optimize.

## Output

Return an assessed itinerary, not raw tool dumps:

- Recommended booking path with total estimated cost and currency.
- Flight shortlist with all-in baggage/status notes when available.
- Hotel shortlist with neighborhood, rating, cancellation, and price notes.
- Viability result from `assess_trip`: GO, WAIT, or NO_GO.
- "Naive -> Optimized -> Saved" comparison when optimization or hacks changed
  the recommendation.
- Follow-up searches only when they would materially improve the plan.

If native `mcp__trvl__travel` is unavailable, use
`mcp__gateway__gateway_invoke` with `server="trvl"` and `tool="travel"`.
Exact legacy tool names remain callable as compatibility aliases.
