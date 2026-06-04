# Door-to-Door Transfer — Architecture & Implementation Plan

**Date:** 2026-06-04
**Status:** Design doc + build plan (companion to `2026-06-04-door-to-door-transfer-proposal.md`, which is the PRD)
**Scope:** Options C (comparison card), A (surfacing), B (journey + leave-by scheduler), F (calendar handoff), E (providers). Grounded in the real trvl codebase.

---

## 0. Architecture principles (inherited from trvl CLAUDE.md — do not violate)

- No frameworks; stdlib + existing libs only.
- `internal/models` is the shared type package; unidirectional imports (nothing imports back into models from siblings).
- MCP handlers in `mcp/tools*.go` are **thin** — they delegate to `internal/` domain packages. No product logic in handlers.
- New providers mirror the `internal/ground/providers` pattern (HTTP → JSON → `models.GroundRoute`).
- Smart router stays the single advertised tool; new capability = a new **intent**, not a new advertised tool.
- Strict JSON schemas (issue #96 — strict OpenAI hosts reject the whole surface on a bad schema).
- Default test suite stays deterministic + offline (fixtures); live calls behind `TRVL_TEST_LIVE_*`.
- When the tool surface changes, update README/AGENTS/plugin/demo + the doc-claims guard in one PR.

## 1. Package & file map (what gets created / touched)

```
internal/
  transfer/                         NEW — door-to-door domain logic
    option.go        TransferOption type + BuildOptions(routes) enrichment
    compare.go       sort/label helpers (cheapest/fastest/best-value/luggage)
    steps.go         step-by-step assembly from route legs + airport KB
    proscons.go      structured pros/cons from signals (no prose opinion)
    schedule.go      Leave-By Scheduler — backward induction + buffer model
    journey.go       JourneyPlan stitcher (home->airport->flight->airport->dest + return)
    airport_kb/      curated per-airport profiles (top ~50): arrival buffers,
      kb.go            loader + AirportProfile type
      data/*.json      BCN.json, HEL.json, ... (exit/ticket snippets, buffers, last-departure)
  calendar/
    ics.go           EXTEND — emit full JourneyPlan (not just flight); leave-by alert
    gcal.go          NEW (Option F2) — gws calendar insert adapter (opt-in)
  trips/
    workspace.go     EXTEND (Option D, deferred) — store JourneyPlan as ground_legs
  models/
    transfer.go      NEW — shared TransferOption / JourneyPlan / ScheduleTimeline types
mcp/
  tools_smart.go     EXTEND — add `journey` + `compare_transfer` intents to resolveTravelTarget
  tools_ground.go    EXTEND — handleSearchAirportTransfers returns enriched options
  tools_journey.go   NEW — handleJourney (thin; delegates to internal/transfer)
```

Rationale: all real logic lands in `internal/transfer` (new domain package) + shared types in `internal/models`. MCP stays thin. Calendar reuses `internal/calendar`. Nothing in `internal/models` imports siblings.

## 2. Core data structures (`internal/models/transfer.go`)

```go
// TransferOption is one mode for one leg, enriched for the comparison card.
// Derived from one or more models.GroundRoute by internal/transfer.BuildOptions.
type TransferOption struct {
    Mode            string   `json:"mode"`              // "airport_express","metro","taxi","ride_hail","private_transfer","bus","train"
    Label           string   `json:"label"`            // "Aerobus A1"
    TotalPrice      float64  `json:"total_price"`
    PriceIsEstimate bool     `json:"price_is_estimate"`
    Currency        string   `json:"currency"`
    DoorToDoorMin   int      `json:"door_to_door_minutes"`
    Changes         int      `json:"changes"`
    Pros            []string `json:"pros"`
    Cons            []string `json:"cons"`
    Steps           []Step   `json:"steps"`            // grounded; see Step.Grounded
    BookURL         string   `json:"book_url,omitempty"`
    SourceRouteIDs  []string `json:"-"`                // provenance back to GroundRoute
}

type Step struct {
    Order    int    `json:"order"`
    Text     string `json:"text"`
    Grounded bool   `json:"grounded"`   // false => rendered as "(estimated)"
    DurMin   int    `json:"duration_minutes,omitempty"`
}

// TransferComparison is the full card for one leg: every option + sort labels.
type TransferComparison struct {
    From, To   string           `json:"from"`
    DistanceKm float64          `json:"distance_km"`
    Options    []TransferOption `json:"options"`
    Cheapest   string           `json:"cheapest_mode"`
    Fastest    string           `json:"fastest_mode"`
    BestValue  string           `json:"best_value_mode"`
    LuggageBest string          `json:"most_luggage_friendly_mode"`
}

// JourneyPlan is the full door-to-door plan (Option B).
type JourneyPlan struct {
    Outbound []LegPlan       `json:"outbound"`  // home->airport, flight, airport->dest
    Return   []LegPlan       `json:"return,omitempty"`
    Schedule ScheduleTimeline `json:"schedule"`
    Assumptions []string     `json:"assumptions"`
    Confidence string        `json:"confidence"` // high|medium|low
}

type LegPlan struct {
    Kind       string             `json:"kind"`     // "ground","flight"
    From, To   string             `json:"from"`
    Comparison *TransferComparison `json:"comparison,omitempty"` // ground legs
    Flight     *FlightLeg         `json:"flight,omitempty"`
    Selected   string             `json:"selected_mode,omitempty"` // user choice
}

// ScheduleTimeline is the leave-by backward plan.
type ScheduleTimeline struct {
    LeaveHomeBy   string     `json:"leave_home_by"`   // local time
    Steps         []SchedRow `json:"steps"`           // each timed row
    Fallback      string     `json:"fallback,omitempty"`
    BufferMinutes int        `json:"airport_buffer_minutes"`
}
```

`AirportProfile` (in `internal/transfer/airport_kb`):

```go
type AirportProfile struct {
    Code              string            // "BCN"
    IntlBufferMin     int               // 120
    DomesticBufferMin int               // 60
    LastTrainDepart   string            // "00:30" local, "" if unknown
    ExitSnippets      map[string]string // mode -> "exit T1, follow A1 signs"
    TaxiCaveats       string            // "official black-and-yellow rank only; ~EUR 4.50 supplement"
}
```

## 3. Component composition (how options compose)

```
                         travel (smart router intent)
                                  |
       compare_transfer ----------+---------- journey
            |                                    |
  internal/transfer.BuildOptions        internal/transfer.PlanJourney
            |                                    |
   +--------+--------+                  +--------+----------+--------+
   |        |        |                  |        |          |       |
 routes   steps   proscons         search_flights  BuildOptions  Schedule
 (existing  (KB+    (signals)        (existing)     (=compare)   (backward
  search)  transitous)                                            induction)
            |                                                        |
   models.TransferComparison ------> JourneyPlan ------> calendar.EmitICS (Option F)
```

- **Option C** = `compare_transfer` intent → `BuildOptions` → `TransferComparison`.
- **Option A** = tool-description hint on `search_flights`/`search_hotels` results → suggests `compare_transfer`.
- **Option B** = `journey` intent → `PlanJourney` (reuses BuildOptions per ground leg + search_flights + Schedule).
- **Option F** = `calendar.EmitICS(JourneyPlan)` after user selects modes.
- **Option E** = more providers feeding `routes` (transparent to the above).

## 4. The trust-critical grounding architecture (steps.go)

`AssembleSteps(route models.GroundRoute, profile *AirportProfile) []Step`:
1. **Skeleton from route legs** — each `GroundLeg` (line, board stop, alight stop, duration) becomes a grounded `Step{Grounded:true}`.
2. **Endpoints from airport KB** — prepend the curated "exit T1 / buy ticket" snippet (`Grounded:true`) when the airport is in the KB; otherwise a generic `Grounded:false` step rendered "(estimated)".
3. **Never synthesize** a terminal/line/sign absent from (1) or (2). If neither source covers it, omit the specific claim and keep the generic action.
4. Pros/cons (`proscons.go`) derive ONLY from structured signals: price ratio vs cheapest, `Changes`, amenities (luggage/stairs flags), KB last-departure (late-night), `Transfers`. No free-text opinion.

This is the single most important module: it is what keeps trvl on the right side of the roadmap's #1 trust failure ("invented details").

## 5. Leave-By Scheduler (schedule.go) — the buffer model

```
leave_by = flight.departure_local
         - airport_buffer(profile, intl?, time_of_day)   // KB, conservative
         - ground_leg.door_to_door_minutes               // chosen mode
         - transfer_variance(mode)                        // train<bus<road
         - origin_walk_minutes
         - safety_margin                                  // fixed floor, e.g. 10
```
- Buffers SURFACED in `Assumptions`. Confidence from data quality (frequent train + KB hit = high; unknown airport = medium; missing transit data = low).
- `Fallback` = the next-earlier departure that still clears a reduced buffer.
- Return leg: same model anchored on the return flight, origin = accommodation.

## 6. MCP surface changes

- `resolveTravelTarget` (`tools_smart.go`): add intents `compare_transfer` → handler, `journey` → handler. Both accept natural-language + structured `params`.
- New `tools_journey.go`: `handleJourney` (thin) + `handleCompareTransfer` (thin) → delegate to `internal/transfer`.
- `handleSearchAirportTransfers`: wrap existing routes through `BuildOptions` so even the legacy tool returns the enriched card (backward-compatible — extra fields, no removals).
- Strict JSON schemas for new tools (reuse `schema_helpers.go`). Update doc-claims guard counts (new aliases) + README/AGENTS/plugin/demo in the same PR.

## 7. Implementation plan (phased, task-decomposed)

Ordering follows the PRD recommendation: **A (quick) -> C (core) -> B (flagship) -> F (handoff)**, E parallel, D deferred.

### Phase 0 — Foundations (0.5 day)
- T0.1 Add `internal/models/transfer.go` types (above). AC: compiles, JSON round-trips, `go vet` clean.
- T0.2 Scaffold `internal/transfer/` package + `airport_kb` loader with 1 seed profile (BCN). AC: KB loads BCN from JSON fixture; unit test.

### Phase 1 — Option A: surface existing (XS, 1 day)
- T1.1 Tool-description orchestration hint on `search_flights`/`search_hotels` results suggesting `compare_transfer`. AC: a flight search response includes a transfer suggestion line; smart-router routes "how do I get from X" to the transfer path. Test: router resolution test.

### Phase 2 — Option C: comparison card (M, 1-2 weeks)
- T2.1 `BuildOptions(routes []GroundRoute) []TransferOption` — map+merge existing routes to modes. AC: fixtures of GroundRoute → expected options; deterministic test.
- T2.2 `proscons.go` — structured pros/cons from signals. AC: table tests per signal (price ratio, changes, late-night, luggage).
- T2.3 `steps.go` `AssembleSteps` with grounding rules (route legs + KB; `Grounded` flag). AC: grounded steps from transitous fixture; ungrounded labelled; never invents terminal not in fixture/KB.
- T2.4 `compare.go` sort/label (cheapest/fastest/best-value/luggage). AC: label tests.
- T2.5 `compare_transfer` intent + `handleCompareTransfer` + strict schema. AC: MCP call returns `TransferComparison`; schema validates on strict host.
- T2.6 Wrap `handleSearchAirportTransfers` through BuildOptions (backward-compatible). AC: legacy tool still passes existing tests + now returns options.
- T2.7 Seed airport KB to top 10 by traffic. AC: 10 profiles load; KB coverage test.
- T2.8 Surface + doc update (README/AGENTS/plugin/demo + doc-claims guard count). AC: guard test passes.

### Phase 3 — Option B: journey + leave-by scheduler (M-L, 1-2 weeks)
- T3.1 Home-address geocoding via existing `destinations.Geocode`; store/read home in `~/.trvl/preferences.json`. AC: geocode fixture; prefs round-trip; privacy test (no remote persist).
- T3.2 Airport disambiguation (nearest/served airport for a city/flight). AC: HEL for Espoo, BCN for Barcelona; test.
- T3.3 `schedule.go` buffer model + `ScheduleTimeline` + fallback. AC: backward-induction tests with KB buffers; fallback selection test; conservative-by-default assertion.
- T3.4 `journey.go` `PlanJourney` stitcher (outbound + return, per-leg comparison + flight). AC: end-to-end fixture → JourneyPlan; reuses BuildOptions + search_flights.
- T3.5 `journey` intent + `handleJourney` + strict schema + surface/doc update. AC: MCP call returns JourneyPlan; guard passes.

### Phase 4 — Option F: calendar handoff (S-M, 3-5 days)
- T4.1 Extend `internal/calendar/ics.go` to emit a full `JourneyPlan` (leave-by event with alarm, each leg, return). AC: valid VCALENDAR; leave-by VALARM present; imports into Apple/Google in manual check.
- T4.2 `compare_transfer`/`journey` accept a `selected_mode` per leg; ICS reflects selection. AC: selection test.
- T4.3 (opt-in) `gcal.go` via `gws calendar +insert`, write-only to a dedicated "Trips (trvl)" calendar. AC: dry-run insert; never touches existing events; behind explicit user action.

### Parallel — Option E: providers (ongoing)
- T-E.1 Airport-express operators + one transfer aggregator (Welcome Pickups/Mozio) mirroring `providers/` pattern. AC: provider returns GroundRoute; fixture test; ToS note in code.

### Deferred — Option D: workspace integration
- Gated on MIK-3496. Extend `internal/trips/workspace.go` to persist `JourneyPlan` as `ground_legs` with recheck/watch.

## 8. Test strategy

- Deterministic default suite: all logic (BuildOptions, proscons, steps grounding, schedule math, journey stitch) tested with fixtures; no live calls. New `TRVL_TEST_LIVE_PROBES` cases for transitous/geocode behind the flag.
- Grounding guard test: assert `AssembleSteps` NEVER emits a `Grounded:true` step whose terminal/line is absent from the input route/KB (anti-hallucination unit test).
- Schedule safety test: assert `leave_by` is always <= the naive "departure - buffer" (never optimistic).
- Schema strictness test: new tools validate under the strict-array schema rules (issue #96 regression guard).
- Doc-claims guard: extend with the new alias count.

## 9. Rollout / flags

- New intents are additive; default advertised surface stays 1 tool. No breaking change.
- Ship behind natural-language discovery first (A); KB coverage grows incrementally (10 → 50 airports) without code change.
- Each phase independently shippable and demoable.

## 10. Effort summary

| Phase | Option | Effort | Gates |
|---|---|---|---|
| 0 | foundations | 0.5d | — |
| 1 | A surface | 1d | P0 |
| 2 | C card | 1-2w | P0 |
| 3 | B journey+schedule | 1-2w | P2 |
| 4 | F calendar | 3-5d | P3 |
| parallel | E providers | per-provider | P0 |
| deferred | D workspace | — | MIK-3496 |
