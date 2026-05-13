# Traveller Workspace

The traveller workspace is the local-first trip planning spine for trvl. It is built for the painpoints that appeared repeatedly in competitor and traveller-forum research: too many tabs, low trust in AI itineraries, stale prices, missing reservation context, and awkward handoff from search to manual booking.

## What It Stores

Trip JSON stays backward-compatible. Existing `legs` and `bookings` remain top-level fields, while `schema_version: 2` adds a `workspace` object:

- `places`: map-ready stops with optional coordinates and evidence IDs.
- `days`: day plans with ordered place IDs, estimated route minutes, and warnings.
- `candidates`: manually bookable flight, hotel, and ground options with price, URL, checked time, expiry, and status.
- `imported_records`: normalized booking confirmations from user-approved text.
- `decisions`: unresolved travel choices such as hotel A vs hotel B.
- `evidence`: provider/source freshness references.
- `unresolved_actions`: re-checks, manual booking tasks, and follow-up reminders.

All trip data remains under `~/.trvl/trips.json`, written through the existing 0700 directory and 0600 file path.

## MCP Usage

Use the compact smart router:

```json
{
  "intent": "trip_workspace",
  "action": "import_reservation",
  "params": {
    "trip_id": "trip_abc123",
    "subject": "Booking confirmation - KLM",
    "body": "Flight HEL -> AMS... Booking reference ABC123",
    "source": "email"
  }
}
```

Direct compatibility alias:

```json
{
  "action": "save_candidate",
  "trip_id": "trip_abc123",
  "type": "hotel",
  "title": "Central stay",
  "provider": "Google Hotels",
  "price": 120,
  "currency": "EUR",
  "url": "https://example.com",
  "checked_at": "2026-05-13T12:00:00Z"
}
```

## Booking Safety

`trip_workspace` is manual-handoff only. It can say whether a candidate has enough current evidence to be booking-ready, but it does not book, cancel, hold inventory, or guarantee availability. Before presenting a "book this" recommendation, call `action=booking_ready`; if it reports stale evidence, missing price, or missing URL, re-run the relevant provider search first.

## Fare Intelligence

`action=fare_intelligence` compares a current price to watch history. It returns conservative `buy`, `watch`, or `wait` guidance based on observed medians and confidence from sample size. It is not a live fare forecast unless the supplied history is current.

## Itinerary Sanity Checks

`action=optimize_itinerary` uses workspace place coordinates to estimate route time and warn when a day is likely overpacked. Missing coordinates are treated as unknown rather than invented; the tool keeps the day plan but skips route-time certainty.

## Import Boundaries

Reservation import accepts user-approved text or profile booking records. It should not scan email or calendars without explicit user consent. Imported confirmations create confirmed legs, imported records, and an open verification action because provider schedules, hotel policies, and prices can change after the confirmation was parsed.
