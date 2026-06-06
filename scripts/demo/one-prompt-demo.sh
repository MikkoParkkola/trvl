#!/usr/bin/env bash
set -euo pipefail

cat <<'DEMO'
# one prompt into the travel MCP router
> Plan a realistic long weekend from HEL to London in July. Compare flights,
> hotel details, ground transfer, hacks, and create a watch if the best fare
> is above EUR 220.

travel(intent=plan_trip, origin=HEL, destination=LON, depart=2026-07-01, return=2026-07-05)

1. flights: HEL -> LHR, Finnair nonstop, EUR 219, booking_url present
2. hotel detail: 4-star central London, free cancellation, rooms + amenities checked
3. ground: Heathrow Express vs Elizabeth line surfaced with time/cost tradeoff
4. hacks: date-flex and rail/ferry alternatives checked; hidden-city rejected for checked-bag risk
5. watch: optional watch_price below EUR 200; user confirmation required before saving

Naive -> Optimized -> Saved
EUR 684 -> EUR 611 -> EUR 73

1 smart MCP tool + 63 compatibility aliases + 21 providers
Manual booking only: trvl returns provider URLs and readiness checks, not automatic purchase.
DEMO
