# Accommodation search reliability plan

> **Status: SHIPPED.** This plan has been implemented — `search_accommodations` (criteria-first ranking + evidence ledger) is live (`mcp/tools.go`, `mcp/tools_accommodations*.go`), with `AccommodationNeed`/`AccommodationOffer` models in `internal/models/accommodation.go`. Retained as design rationale; the "gaps" and pending-work sections below describe the original starting point, not current behaviour.

This plan turns the public article caveat into a product rule: trvl must search
for the accommodation the user asked for, not merely for properties with low
lead-in prices.

## Problem

The current broad hotel search can find useful candidates, but a property-level
lead-in price is not enough for a trustworthy recommendation. A user asks for an
accommodation need: dates, party composition, location, accommodation type, room
privacy, bedrooms, bathrooms, beds, amenities, cancellation, board, budget, and
currency. A displayed option is only useful if the price belongs to an available
room or apartment that satisfies those criteria.

The Roberto Reale article exposed the failure mode clearly: a low Google Hotels
lead-in price looked useful during discovery, but the checkout path showed a
much higher price. That is a trust issue, not just a wording issue.

Once this flow is implemented and validated, follow up with the article author
with a concrete changelog: trvl now treats accommodation search as a
criteria-first room/apartment workflow, rejects lead-in prices for final ranking,
and exposes refundability/cancellation data as booking-order inputs. The message
should point to verified behavior and tests, not just say that the caveat was
"fixed".

## Current branch implementation

- Added `search_accommodations` as the primary traveller-facing accommodation
  decision tool.
- It normalizes the user's need, uses hotel search for candidate recall, checks
  room-level availability for the shortlist, and ranks only
  `criteria_matched=true` room/apartment offers in `offers`.
- Discovery lead-in prices remain visible under `candidates` with the
  `lead_in_prices_are_candidates_only` warning.
- Refundability and free-cancellation criteria feed the match result and
  `booking_order_hint`, so the planner can decide whether flights can be booked
  before accommodation.

## Reliability principle

Search must be criteria-first:

1. Capture the required accommodation need before searching.
2. Generate property candidates only as a recall step.
3. Fetch room/apartment offers for shortlisted candidates.
4. Match each offer against the requested criteria.
5. Rank only matched, priced offers.
6. Show unmatched lead-in prices only as unverified candidates with explicit
   machine-readable caveats.

No final recommendation, trip-cost ranking, or "best deal" claim should use a
raw property-level price unless it has been verified against the requested
room/apartment criteria.

## Evidence from current sources

- Google Hotel Center's price accuracy policy expects landing and booking pages
  to match the selected occupancy and dates, and expects mandatory taxes and
  fees to be represented in the total price.
- Google's taxes and fees policy says prices that omit mandatory taxes and fees
  are inaccurate.
- The FTC final unfair/deceptive fees rule for short-term lodging requires
  advertised prices to clearly and prominently disclose total price inclusive of
  most mandatory charges.
- SerpAPI's Google Hotels surface supports filters that map directly to
  criteria-first accommodation search, including property types, amenities,
  vacation rentals, bedrooms, bathrooms, and total rates.
- Expedia Rapid's Lodging API is a stronger production verification path
  because its shopping API returns rates and availability across room types with
  refundable/cancellation data and a price breakdown.

Sources:

- https://support.google.com/hotelprices/answer/6064419
- https://support.google.com/hotelprices/answer/6064432
- https://www.federalregister.gov/documents/2025/01/10/2024-30293/trade-regulation-rule-on-unfair-or-deceptive-fees
- https://serpapi.com/google-hotels-api
- https://developers.expediagroup.com/rapid/sdk/java/usage-examples

## Current trvl gaps

- `search_hotels` has broad filters, but it is still property-first: it returns
  hotel candidates and then applies post-filters. Room/apartment matching is not
  the primary result type.
- The SerpAPI client hardcodes `adults=2`, so it cannot yet be trusted for party
  sizes other than two adults.
- `hotel_rooms` has room-level fields, but `RoomSearchOptions` does not yet carry
  guests, children, rooms, bedrooms, bathrooms, required amenities, board,
  cancellation, or room privacy criteria.
- The search fallback in `hotel_rooms` currently uses `Guests: 2`, which can
  misalign room prices with the original query.
- Amenity filtering is exact set matching after provider parsing; it does not
  distinguish required, preferred, unknown, and provider-unverified amenities.
- Final ranking code can still sort on numeric price without a hard check that
  the price belongs to a matched offer with sufficient confidence.
- There is no persisted quote/evidence ledger with `checked_at`, TTL,
  source-status reason codes, criteria match, and price delta from lead-in.

## Proposed model

Add a first-class accommodation need:

```go
type AccommodationNeed struct {
    Location          string
    CheckIn           string
    CheckOut          string
    Adults            int
    ChildrenAges      []int
    Rooms             int
    AccommodationType string // hotel_room, entire_apartment, private_room, shared_room, hostel_bed, villa
    MinBedrooms       int
    MinBathrooms      int
    MinBeds           int
    RequiredAmenities []string
    PreferredAmenities []string
    MaxDistanceKm     float64
    Neighborhoods     []string
    MustHaveKitchen   bool
    MustHaveWifi      bool
    MustHaveWorkspace bool
    BreakfastRequired bool
    RefundableRequired bool
    FreeCancellationRequired bool
    Currency          string
    MaxTotalPrice     float64
}
```

Add a first-class matched offer:

```go
type AccommodationOffer struct {
    PropertyName      string
    PropertyID        string
    OfferID           string
    AccommodationType string
    RoomName          string
    Provider          string
    ProviderURL       string
    OccupancyMatched  bool
    CriteriaMatched   bool
    MissingCriteria   []string
    UnknownCriteria   []string
    NightlyPrice      float64
    TotalPrice        float64
    TaxesAndFees      float64
    TaxesFeesIncluded *bool
    Currency          string
    PriceBasis        string // lead_in, room_nightly, room_total, tax_inclusive_total
    PriceConfidence   string // unverified, room_level, verified
    CheckedAt         time.Time
    ExpiresAt         time.Time
    Freshness         string
    CancellationPolicy string
    Refundable        *bool
    FreeCancellation  *bool
    BookingOrderHint  string // accommodation_first, flights_first_ok, needs_refundability_check
    Board             string
    Warnings          []string
}
```

## Search flow

1. `parse_accommodation_need`
   Normalize the user's request into explicit required and preferred criteria.
   If the user says "apartment with kitchen near X", the primary target becomes
   an apartment/unit offer, not a hotel property.

2. `search_accommodation_candidates`
   Use Google Hotels, HomeToGo, Airbnb, Booking, Hostelworld, and configured
   providers for recall. Return candidates with provider health and lead-in
   price caveats. Do not rank final recommendations here.

3. `verify_accommodation_offers`
   For top candidates, fetch room/apartment offers. Pass the original criteria
   through every provider path: adults, children ages, rooms, bedrooms,
   bathrooms, beds, room privacy, cancellation/refundability, board, amenities,
   and currency.

4. `match_accommodation_offers`
   Produce `criteria_matched`, `missing_criteria`, and `unknown_criteria`.
   A price cannot be considered aligned unless `criteria_matched=true` and
   `occupancy_matched=true`.

5. `rank_accommodation_offers`
   Sort by verified total first, then room-level total, then lead-in candidates
   only if the user explicitly asks to see unverified possibilities. Never mix
   hostel beds, private rooms, and whole apartments in one ranked price list
   unless the user opted into that comparison.

## Trust gates

- `booking_ready`: requires `criteria_matched=true`, `occupancy_matched=true`,
  `total_price>0`, known currency, non-stale `checked_at`, and
  `price_confidence` of `room_level` or `verified`.
- `flights_first_ok`: requires `booking_ready` plus refundability/cancellation
  evidence that satisfies the user's risk tolerance. If refundability is unknown
  and flight dates are not locked, the planner should prefer
  `accommodation_first` or `needs_refundability_check`.
- `final_trip_cost_ready`: requires `booking_ready` and a known tax/fee status.
  If taxes are excluded or unknown, the optimizer must return
  `needs_price_verification`, not a final ranked cost.
- `lead_in_only`: allowed for discovery and maps, not for final ranking.
- `teaser_suspect`: set when verified total materially exceeds lead-in price,
  for example by more than 30 percent or more than the local configured absolute
  threshold.

## Provider improvements

- Extend `internal/serpapi.SearchHotels` to accept full accommodation criteria
  instead of hardcoding two adults. Pass through adults, children, vacation
  rental mode, bedrooms, bathrooms, property type, amenities, min/max price, and
  free cancellation where supported.
- Add a production verification provider path, preferably Expedia Rapid or
  another official lodging API, for room-rate availability and price breakdown.
  Keep scraping as fallback/recall, not as the highest-trust source.
- Treat Booking/Google direct scraping as opportunistic. Add explicit provider
  preflight statuses for cookie wall, WAF block, timeout, no availability, and
  parser drift.
- Normalize vacation rentals and rooms separately. A whole apartment, hotel
  double room, private room, hostel bed, and shared room need different schemas
  and cannot be compared by raw nightly price without user consent.

## User-facing caveats

Do not use a generic disclaimer as a substitute for reliable data. Surface the
specific status:

- `verified_total`: "Verified total from provider at checked_at; price may still
  change before booking."
- `room_level`: "Room-level price for the requested criteria; tax/fee status:
  included, excluded, or unknown."
- `lead_in_only`: "Lead-in search price, not a matched room/apartment quote."
- `criteria_unknown`: "Provider did not expose enough data to confirm required
  criteria."
- `criteria_mismatch`: "Shown only as an alternative because it does not satisfy:
  missing_criteria."

## Implementation priorities

P0:

- Add `AccommodationNeed` and `AccommodationOffer` models.
- Thread full criteria through `search_hotels`, `hotel_rooms`,
  `search_hotels_with_details`, and SerpAPI.
- Replace the `Guests: 2` fallback in room search with the original occupancy.
- Add a final-ranking guard: no final trip-cost ranking from `lead_in_only` or
  criteria-mismatched offers.
- Add tests that fail if a final recommendation uses a price whose
  accommodation type, occupancy, amenities, refundability requirement, or
  total-price status is unverified.

P1:

- Add evidence ledger rows for every provider response: provider, status,
  checked_at, ttl, criteria echo, raw lead-in price, verified total, warnings,
  and parser version.
- Add canary live probes for fixed city/date/party/accommodation-type cases.
- Add price-shock telemetry comparing lead-in price to verified total by
  provider and destination.

P2:

- Integrate an official room-rate provider for production verification.
- Build a room-equivalence graph to identify when two providers are selling the
  same actual room/rate plan.
- Add saved candidate re-verification before the user acts on a booking link.

## Acceptance criteria

- A user can ask for "entire apartment, two bedrooms, kitchen, washing machine,
  near the old town, two adults and one child" and trvl returns only matched
  accommodation offers in the ranked list.
- If only property-level prices are available, trvl returns candidates but marks
  every price `lead_in_only` and does not use them in final trip totals.
- If a provider cannot confirm a required amenity or room type, that uncertainty
  is machine-readable and visible in the response.
- If a verified total differs materially from the lead-in price, trvl records a
  `teaser_suspect` warning and downranks or excludes that source from final
  recommendations.
- All final-ranked accommodation prices include the original criteria echo,
  checked timestamp, source, refundability/cancellation status, tax/fee status,
  and confidence.
- When the user's trip sequence depends on booking risk, trvl emits a
  booking-order hint based on refundability: `flights_first_ok`,
  `accommodation_first`, or `needs_refundability_check`.
