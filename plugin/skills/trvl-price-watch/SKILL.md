---
name: trvl-price-watch
description: Create recurring trvl flight, hotel, room-availability, and opportunity watches with /loop-compatible check-in instructions.
triggers:
  - price watch
  - monitor flights
  - hotel deal alert
allowed-tools:
  - mcp__trvl__travel
  - mcp__trvl__get_preferences
  - mcp__trvl__watch_price
  - mcp__trvl__watch_room_availability
  - mcp__trvl__watch_opportunities
  - mcp__trvl__list_watches
  - mcp__trvl__check_watches
  - mcp__trvl__list_opportunity_watches
  - mcp__gateway__gateway_invoke
---

# trvl Price Watch

Use this skill when the user wants to monitor flights, hotel prices, specific
room availability, or rolling trip opportunities.

## Inputs

Load preferences first through `travel` with `intent="get_preferences"` or the
compatibility alias, then collect only the missing watch parameters:

- Watch type: flight, hotel, room availability, or opportunity window.
- Route or property name and location.
- Date or rolling window.
- Target price, currency, minimum score, or alert threshold.
- Passenger/guest count and bag posture when it changes all-in price.

## Workflow

1. For flight or hotel threshold alerts, call `travel` with
   `intent="watches"`, `action="create"`, and the old `watch_price` params.
2. For a specific property and dates, use `intent="watch_room_availability"`.
3. For rolling windows across favourite or bucket-list destinations, use
   `intent="watch_opportunities"`.
4. Read back the active watch with `action="list"` or
   `intent="list_opportunity_watches"` so the user sees the saved threshold.
5. Give a /loop-compatible recurrence instruction:
   "Run `check_watches` daily; if a threshold is hit, summarize the delta and
   show the booking URL."

## Output

Return:

- Watch ID and type.
- Watched route/property/window.
- Target threshold and currency.
- First check cadence and the exact recurring `check_watches` or opportunity
  watch command pattern.
- Any missing data that prevents a durable watch.

Do not create or update saved preferences unless the user explicitly confirms
the profile change. If native `mcp__trvl__travel` is unavailable, invoke
`tool="travel"` through `mcp__gateway__gateway_invoke` with `server="trvl"`.
Exact legacy tool names remain callable as compatibility aliases.
