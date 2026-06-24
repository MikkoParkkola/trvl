# Public article feedback

This file tracks concrete public feedback from articles or directory pages that
mention trvl and turns it into product/docs work.

## Roberto Reale budget travel pipeline articles

Sources:

- `building-a-budget-travel-pipeline.mdx`, published 2026-06-06.
- `budget-travel-pipeline-applied.mdx`, published 2026-06-06.

Public signal:

- The articles present trvl as a useful accommodation MCP companion to `fli`
  for budget travel searches.
- The trust caveat is serious: raw free hotel prices are list-level Google
  Hotels lead-in rates. Some are backed by real providers after selecting the
  property, but they must not be used for final trip ranking until trvl exposes
  the selected-property OTA/room matrix. The worked article case had a EUR
  46/night hotel become EUR 269/night at checkout.
- Part 2 says the working pipeline needs a verified read before ranking hotels:
  exact dates, exact party size, tax-inclusive total where exposed, and a link
  that lands on a durable provider or property page.

Current trvl response:

- `search_accommodations` is now the primary traveller-facing stay search. It
  starts from the requested accommodation need, verifies room-level offers for
  shortlisted candidates, and keeps candidate lead-in prices out of the ranked
  final `offers` list.
- `search_hotels` remains the broad discovery surface across Google Hotels,
  Trivago, Airbnb, Booking.com, Hostelworld, HomeToGo, and configured external
  providers.
- `search_hotels_with_details` and `hotel_rooms` are the booking-readiness
  surfaces. Agents should use them for the top candidates before making a public
  recommendation.
- `trvl serpapi` is the optional verified-price path when the user has a free
  SerpAPI key. It exposes provider prices with per-night and total cost for the
  exact dates.
- `hotel_prices` now mirrors the selected-property Google Hotels step when
  SerpAPI is configured: it finds the exact `property_token`, fetches that
  property's detail matrix, and returns OTA/provider rows instead of treating a
  list-level total as a single generic provider.

Required public-trust rule:

1. Say "lead-in price" or "search price" for raw `search_hotels` prices.
2. Do not rank final trip cost on raw hotel search prices.
3. Prefer criteria-matched `search_accommodations` offers, or verify
   shortlisted hotels with `search_hotels_with_details`, `hotel_rooms`, or
   `trvl serpapi`.
4. Prefer tax-inclusive `total_price` or provider total where exposed.
5. Keep any local tourist tax as a separate user-confirmed cost; do not invent
   one.
6. Do not claim trvl books, holds, locks, or guarantees a hotel rate.

Follow-up product work:

- Keep `price_confidence` / `price_basis` fields on hotel search and
  accommodation offer results so agents can enforce the rule programmatically.
- Preserve `retrieved_at` / `checked_at` and freshness for every hotel price
  source; never leave a zero timestamp on public recommendation paths.
- Keep a durable fallback URL for provider links that may expire.
- Track provider trust tiers separately from price. A lower Google Hotels matrix
  price from a lesser-known OTA can be real, but users may still prefer a
  mainstream OTA, the official hotel site, or a refundable rate.

## Retest log

### Roberto Reale, v1.15.0 — `trvl serpapi` on Ischia (island / unindexed)

Roberto retested `trvl serpapi` against Ischia (2026-07-30 to 2026-08-04, 2
adults, 5 nights, EUR) and reported that the per-OTA `prices` breakdown was
populated for only about half of the properties. For the rest `prices` came
back null, so the total shown was the list-level Google Hotels minimum (the
lead-in teaser) rather than a per-provider verified total. He measured this
understating real cost by up to 24%: Sorriso Thermae showed EUR 779 in trvl
against a EUR 1,024 reference and a live Booking.com rate of EUR 1,102. The
root distinction he identified is list-level minimum versus the per-property
detail-endpoint price reached through `property_token`.

Outcome:

- Detail-verified properties whose provider rows arrived only under
  `featured_prices` were leaving `prices` null even though the total had
  already been promoted to the verified figure. The verified provider
  breakdown is now mirrored into `prices`, so a detail-verified property
  always exposes its per-provider rows.
- Provider-row aggregation deduplicates across `featured_prices` and `prices`,
  so a quote present in both is counted once.
- The honest limits stand: SerpAPI's detail endpoint genuinely returns no
  provider rows for some properties, and detail lookups are bounded by
  `--max-details` (default 8) to respect the quota cost of one call per
  property. Those properties are labelled unverified via `price_verification`
  rather than presented as a verified per-provider total.
- Roberto also contributed an island / unindexed command sequence, now in
  `docs/CLI.md` under "Islands and unindexed destinations".
