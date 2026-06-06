# Door-to-Door Transfer Planning — Proposal & Design Options

**Date:** 2026-06-04
**Status:** Proposal (for evaluation — multiple alternatives, no path chosen yet)
**Source signal:** Direct user feedback from a product-manager friend who travels regularly (Beeper DM, 2026-06-01). Personal context omitted; product signal only.

---

## 1. The pain (verbatim from the user)

> "I'm always asking the same questions. How to get from airport to accommodation. Is it best public transport, taxi, special buses/trains?"
>
> "It is complex. Flights, how to get to airport and from. How to get to accommodation and from?"
>
> "Something I go through manually all the time. And unnecessarily."
>
> "I can imagine integrating more ground transport providers."

**Restated:** trvl solves the *flight* and the *hotel*, but the **ground connection between them** — the first mile (home to departure airport) and the last mile (arrival airport to accommodation), in both directions — is still googled manually every single trip. The hard part is not *listing* options; it is **deciding which mode is best** (public transit vs taxi vs airport express bus/train) given cost, time, luggage, and arrival hour.

This is the **first-mile / last-mile "door-to-door" gap.**

## 2. Is this a common pain? (online validation — V)

Verified common and high-frequency across mainstream travel media and user forums (brave search, 2026-06-04):

- **Reddit r/travel** — "Is there an advantage to booking transportation between the airport and [hotel]" — recurring thread; consensus "public transport / a local taxi are usually a lot cheaper than pre-arranged transfers" (the exact mode-choice question).
- **Reddit r/solotravel** — "How does one get from airport to a hotel" — recurring beginner-and-beyond question.
- **The Points Guy** — "Shuttle or chauffeur? 5 ways to get from the airport to your hotel" — a major publication treats mode-choice as a core explainer.
- **Travel Babbo**, **"9 Best Airport Transfers of 2026"**, **"How to Choose the Best Airport Transfer: Costly vs Cheap"** — a whole content + booking industry (Welcome Pickups, Mozio, Kiwitaxi, GetTransfer) exists *solely* to answer this.

Conclusion: the pain is not idiosyncratic. It is one of the most repeated questions in consumer travel, and the decision axis is always the same: **which mode, by cost / time / convenience / luggage.**

## 3. What trvl already has (baseline — do not rebuild)

- `search_airport_transfers` MCP tool + `trvl airport-transfer` CLI (`internal/trip/airport_transfer.go`).
- **One leg only:** arrival airport to destination. Combines transitous (public transit), ground providers (flixbus, regiojet, eurostar, db, sncf, trainline), and a taxi estimate.
- Returns a **list** of routes (exact + city matches), filterable by `max_price`, `type`, `arrival_time`.

**Three things it does NOT do — the actual gap:**

1. **Door-to-door chaining.** Only the arrival-airport leg. No home-to-departure-airport leg, no stitching of the full journey around a flight, no return direction.
2. **Mode recommendation.** Returns a list, not a *verdict*. The user's literal question — "is it *best* public transport, taxi, special buses/trains?" — stays unanswered. No "Recommended: airport express, EUR 11, 32 min, beats taxi (EUR 55) and bus (slower)."
3. **Proactive discoverability.** The user did not know the capability existed. It is never auto-surfaced when a flight / hotel is searched.

---

## 4. Five alternative solutions (to evaluate)

These are deliberately distinct paths, spanning discoverability, orchestration, intelligence, and data breadth. They are not mutually exclusive (a roadmap could sequence them), but each is independently shippable and is presented for head-to-head evaluation.

### Option A — Surface what exists (discoverability + orchestration-lite)

Make the smart `travel` router **proactively offer** the airport transfer whenever a flight / hotel search completes, via tool-description orchestration (the same mechanism `find_trip_window` already uses to instruct the LLM). No new capability — just wiring the existing tool into the natural flow.

```
You: find flights HEL to BCN July 18, and a hotel near Sagrada Familia

Claude (trvl):
  Vueling HEL to BCN 09:40-13:05  EUR 128 RT
  Hotel Eixample, 0.4km from Sagrada Familia  EUR 96/night
  -> Want me to plan the airport transfer? BCN airport to Hotel Eixample
     is ~13km. (ask "how do I get from BCN to the hotel")
```

- **Effort:** XS (1-2 days). Tool-description + smart-router hint only.
- **Solves:** discoverability (#3). Partial orchestration.
- **Does not solve:** door-to-door chaining (#1), mode recommendation (#2).
- **Risk:** low. Worst case it is a no-op suggestion.

### Option B — `plan_journey` + Leave-By Scheduler (door-to-door routing AND timing)

A new smart capability (`intent: journey` on the `travel` router) that takes **origin (home/address), destination (accommodation), and a flight / dates**, and returns BOTH: (1) the **full chained itinerary** (home to departure airport, [flight], arrival airport to accommodation, plus return), each leg reusing existing search and rendered as a Comparison Card (Option C); and (2) the **Leave-By Scheduler** — a backward-planned timeline that answers "when do I leave home?".

The scheduler is backward induction from a fixed anchor (flight/train departure):

```
Your flight: HEL to BCN, departs Fri Jul 18 09:40

  LEAVE HOME BY  06:55   (safety margin included)
  -----------------------------------------------------
  06:55  Leave Espoo home
  07:00  Walk to bus stop (5 min)
  07:08  Bus 550 to Leppavaara (8 min)
  07:25  Train I to Helsinki Airport (22 min)
  07:50  Arrive HEL T2
  07:50  Bag drop + security, allow 75 min (HEL intl, Fri AM peak)
  09:05  Airside, gate / coffee
  09:40  Departure

  Assumptions: 75-min airport buffer · 15-min transfer slack · frequent train
  Confidence:  HIGH, no known disruptions, train every 10 min
  Fallback:    miss the 07:25? the 07:40 still makes it (tighter, 60-min buffer)
  Reverse:     return leg scheduled the same way from your hotel
```

**The buffer model (core IP):**
`leave_by = departure - airport_arrival_buffer(airport, intl/domestic, airline, time_of_day) - transfer_time - transfer_variance - walk_time - safety_margin`
Every term is grounded, conservative by default, and shown to the user.

**Local-recommendation inputs the scheduler must weigh:**
- Airport-specific arrival guidance (curated KB: BCN 2h intl / 1h domestic, HEL 2h intl) — not a generic "2 hours early".
- Time-of-day transit effects (rush hour slows buses; pick the resilient mode, pad variance).
- Last-departure cutoffs (last airport train 00:30; after that taxi only) — critical for late flights.
- Mode reliability (train variance < bus < road traffic; prefer low-variance when margin is tight).
- Day / holiday / strike signals where data exists; honestly flagged when not.

- **Effort:** M-L (1-2 weeks). New stitcher + scheduler in `internal/trip`, geocoding of home address, the buffer model + airport-arrival KB, return-leg logic, smart-router intent + schema. Reuses all existing leg search and the Option C card.
- **Solves:** door-to-door chaining (#1) fully; the "when to leave" anxiety (new, highest-value); orchestration (#3).
- **Risk:** medium. Timing accuracy is critical (see Risk Mitigation section); home-address geocoding + airport disambiguation are the tricky bits; buffers must be conservative + surfaced + offer a fallback.

### Option C — Mode comparison + step-by-step "decision card" (the user chooses)

**Refined per user direction (2026-06-04):** do NOT impose a single "best." Present **every mode as a choosable option** with **time, price, pros, cons, and grounded step-by-step instructions**, so the traveller decides by their own priority — some pay extra for taxi convenience with luggage, some go cheap. Add a sort/compare helper, not a verdict.

For each mode (public transit, airport express bus/train, metro, taxi, ride-hail, private transfer): door-to-door time, total price (incl. airport supplements), 2-3 pros, 2-3 cons, and numbered "exit → buy ticket → board → arrive" steps. Sorted/labelled so cheapest / fastest / best-value / most-luggage-friendly are visible at a glance.

```
Airport to Hotel Eixample (13 km) · 4 ways — you choose:

OPTION 1 · Aerobus express                       EUR 5.90 · ~35 min
  PROS  cheapest fast option, direct, runs to 01:00
  CONS  luggage on board, drops at Pl. Catalunya (not door)
  STEPS 1. T1 exit, follow "Aerobus A1" signs (3 min walk)
        2. Buy ticket at blue machine / onboard, EUR 5.90
        3. Board A1 to Placa Catalunya (every 5 min, ~35 min)
        4. 1 metro stop / 8-min walk to hotel

OPTION 2 · Taxi                                  ~EUR 35 · ~25 min
  PROS  door-to-door, zero luggage hassle, no changes
  CONS  6x the bus, EUR 4.50 airport supplement, peak surge
  STEPS 1. T1, follow "Taxi" to the official rank
        2. Black-and-yellow only, meter starts ~EUR 2.55
        3. Show address to driver  4. Card accepted

OPTION 3 · Metro L9 + L5                          EUR 5.15 · ~48 min
  PROS  cheapest overall, very frequent
  CONS  2 changes, stairs with bags, slowest

OPTION 4 · Private transfer (pre-booked)         ~EUR 55 · ~25 min
  PROS  meet-and-greet, fixed price, best for groups / after 01:00
  CONS  most expensive, must book ahead

Sort: cheapest -> Metro EUR 5.15 · fastest -> Taxi 25min · best value -> Aerobus
      most convenient w/ luggage -> Taxi/Transfer
You pick. Want me to save the chosen leg to your trip?
```

**Data model:** a `TransferOption` per mode — `{mode, total_price, currency, door_to_door_minutes, changes, pros[], cons[], steps[], book_url?}` — and a `compare` summary with the cheapest/fastest/best-value/most-luggage-friendly labels. The smart router returns the full set; the LLM renders the card and lets the user choose. No forced ranking.

**Grounding (the hard, trust-critical part — NOT optional):** step-by-step instructions MUST be grounded, never free-generated. The roadmap's #1 trust failure is "wrong drive times, invented details." Sources, in order:
1. Route data (transitous/MOTIS) gives real stops, lines, transfer points, and times — the skeleton of the steps.
2. A curated per-airport "exit + ticket purchase" snippet for the top ~50 airports (signage, ticket machine location, official taxi-rank caveats, supplements) — small static dataset under `internal/trip/airport_kb/`.
3. Pros/cons from structured signals (price ratio, change count, luggage/stairs flags, late-night availability), not prose opinion.
Anything not grounded is labelled "estimated" or omitted. Never assert a terminal/sign the data does not support.

- **Effort:** M (1-2 weeks — was S-M before the step-by-step requirement). Scoring/labels are cheap; the grounded-instructions dataset + transitous step extraction are the real work.
- **Solves:** mode comparison + the "which suits me" decision (#2), with practical instructions — the exact thing the user asked for.
- **Composes with:** A (render the card on the surfaced suggestion) and B (one card per leg).
- **Risk:** medium. Instruction accuracy is the trust make-or-break; ship grounded-or-labelled, never hallucinated. Curated airport KB needs maintenance (start with top 50 by traffic).

### Option D — First/last-mile in the Trip Workspace (deep integration)

Fold both transfer legs into the existing **Trip Workspace** (`internal/trips`, the verified-workspace roadmap, MIK-3496). Any planned/imported trip **automatically computes and stores** the door-to-door legs as part of the trip artifact, with recheck/watch actions, so the transfer is a first-class part of the itinerary rather than a separate query.

- **Effort:** L (2-3 weeks). Depends on the workspace MVP landing; extends trip schema with `ground_legs` + transfer evidence.
- **Solves:** #1, #3 durably; makes transfers persistent + re-checkable (matches roadmap outcome 2).
- **Does not solve alone:** #2 without Option C.
- **Risk:** medium-high. Couples to the in-flight workspace work; larger surface.

### Option E — Provider breadth (the user's literal ask)

Add more ground/transfer providers — airport-express operators, **Welcome Pickups / Mozio / Kiwitaxi / GetTransfer** aggregators, ride-hail deep-links (Uber/Bolt/FREE NOW), and local transit agencies via transitous/MOTIS. Pure data-quality expansion behind the existing tool surface.

- **Effort:** ongoing / per-provider (S each), mirrors the `providers/` pattern.
- **Solves:** coverage + accuracy; makes A/B/C/D *better* everywhere.
- **Does not solve alone:** the UX gaps (#1, #2, #3). More data, same friction.
- **Risk:** low per provider; ToS/rate-limit care for aggregators.

---

## 4.5 The end-to-end flow: Compare -> Choose -> Schedule -> Calendar

The four options compose into one user journey:

1. **Compare (C)** — see every mode per leg: time, price, pros, cons, step-by-step.
2. **Choose** — the user picks the mode that fits their priority (cheap vs convenient-with-luggage).
3. **Schedule (B)** — the Leave-By Scheduler backward-plans the timeline (when to leave home) with grounded buffers.
4. **Calendar handoff (F)** — once selected, the whole plan is written to the user's calendar as events with reminders.

### Option F — Calendar handoff (set it up once selected)

Once the user selects modes, write the door-to-door plan into their calendar. trvl ALREADY has ICS export (`export_ics` tool, `exportICSTool`) — extend it from flight-only to the full door-to-door plan. Events created:

```
[Calendar after selection]
  Fri Jul 18
   06:55  Leave home for HEL airport   (alert 30 min before: "Leave in 30 min")
   07:25  Train I -> Helsinki Airport
   07:50  HEL: bag drop + security (75 min buffer)
   09:40  Flight HEL -> BCN  (Vueling, conf #ABC)
   13:05  Arrive BCN
   13:20  Aerobus A1 -> Pl. Catalunya
   14:00  Check in: Hotel Eixample
  Tue Jul 22   [return leg, same structure]
```

- **Delivery options (evaluate):**
  - **F1 ICS file** (works everywhere, zero auth, local-first) — extend existing `export_ics`. The user imports / it auto-adds. Privacy-clean, no account access.
  - **F2 Google Calendar via `gws`** (operator already has the `gws calendar +insert` CLI) — direct insert into the user's Google Calendar, structured, with reminders. Requires the user's Google auth.
  - **F3 Apple Calendar** (macOS, via icalbuddy/EventKit) — local insert for Mac/iOS users.
- **Each event carries:** title, start/end, location (with map link), and an alert (the "leave home by" event gets a prominent reminder — that is the anxiety-killer).
- **Effort:** S-M. F1 is small (extend ICS). F2/F3 are per-platform adapters.
- **Risk:** low for F1 (no auth, local). F2/F3 need auth + careful scoping (write-only to a dedicated calendar; never modify existing events).
- **Principle:** the calendar entry is the *commitment* step — only written after explicit user selection, never auto-pushed.

## 4.6 Risk mitigation (cross-cutting — applies to B/C/F)

Meta-rule: **never assert what you cannot ground; never hide an assumption; always err toward "leave earlier / costs more".** Trust comes from transparency, not from a black-box answer.

| Risk | Severity | Mitigation |
|---|---|---|
| Wrong steps (terminal, line, signage) | High | Steps from route data (transitous/MOTIS real stops/lines/transfers) + curated top-50-airport "exit & ticket" KB. Ungrounded -> labelled "estimated" or omitted. Never name a terminal the data does not support. |
| Miss the flight (bad timing) | **Critical** | Conservative buffers by default, SURFACED not hidden: airport-arrival rule (intl/domestic, peak) + transfer time + variance + walk + safety margin. Always offer a fallback option. Confidence band shown. |
| Stale price/time | Medium | Freshness stamps + "estimated" labels + recheck/watch; existing cache TTL. Prices are "approx", never guaranteed. |
| Local blind spots (strike, rush hour, last train, holiday) | Med-High | Time-of-day adjustment + last-departure check + known-disruption lookup where data exists; where it does not, say so: "couldn't verify today's disruptions, add margin." |
| Over-trust / liability | Medium | Framed as a planning assistant, not a guarantee. Every output shows its assumptions + "confirm check-in time with your airline." No "you will make it" promises. |
| Privacy (home address) | Medium | Local-first only. Home in `~/.trvl/preferences.json`, never sent to a hosted service. Geocode against public APIs without persisting the raw address remotely. F1 ICS keeps calendar local; F2/F3 only on explicit user opt-in. |

## 5. Evaluation matrix

| Option | Chaining (#1) | Compare + decide (#2) | Discovery (#3) | Effort | Standalone value |
|---|---|---|---|---|---|
| **A** Surface existing | partial | no | yes | XS | Medium |
| **B** `plan_journey` | yes | renders C per leg | yes | M-L | High |
| **C** Comparison + step-by-step card | n/a | **yes (the core feature)** | partial | M | **Highest — exactly what the user asked for** |
| **D** Workspace integration | yes | stores C output | yes | L | High (durable) |
| **E** Provider breadth | no | better data for C | no | per-provider | Multiplier |

## 6. Recommendation (for discussion, not locked)

**C is the centerpiece. Sequence A (cheap surfacing) → C (the comparison card) → B (door-to-door wrapper), E in parallel, D deferred to the workspace track.**

- **A first** (XS) — surface the existing single-leg transfer in the flight/hotel flow immediately, so discoverability is fixed in days while C is built.
- **C is the product.** The user's refined direction *is* Option C: a choosable comparison of every mode with time, price, pros, cons, and grounded step-by-step instructions — the traveller decides by their own priority (pay for taxi convenience with luggage, or go cheap). It is M effort now (not S-M) because the **grounded instructions are the trust-critical core** — the table is easy, the accurate "exit T1, buy ticket, board A1" steps are the real work. Ship grounded-or-labelled, never hallucinated.
- **B** wraps C per leg into the full home-to-door journey — the flagship demo, built once C's card exists.
- **E** runs forever; start with airport-express operators + one transfer aggregator to deepen C's data.
- **D** waits for the verified-workspace MVP (MIK-3496) so we extend it rather than fork it.

This ships discoverability immediately (A), then the exact comparison-card feature the user described (C), then earns the door-to-door build (B) on validated demand.

## 7. Open questions

1. Home-address handling for the first mile — store in `~/.trvl/preferences.json` (home airport already there), versus ask per trip? Privacy: keep local-first, never send raw home address to a hosted service.
2. Recommendation weights — ship an opinionated default, versus expose a `prioritise: cost|time|comfort` knob on the `travel` intent?
3. Return-leg timing — how aggressive should the "leave for the airport by" buffer be (security + check-in + transit variance)?
4. Aggregator ToS — Welcome Pickups / Mozio terms versus trvl's API-first, no-scraping-of-protected-content posture.
