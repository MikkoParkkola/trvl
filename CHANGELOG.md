# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `search_awards` MCP tool: cross-program award scanner with transfer-partner sweet-spot ranking (MIK-3081)
- `trvl awards` CLI command: find cheapest redemption path across FB/Avios/Aeroplan/Virgin/Asia-Miles (MIK-3081)
- `search_hidden_city` MCP tool: hidden-city matrix search ranks priced Origin×hub-beyond offers, computes layover risk score, and returns pre-filled booking URLs per carrier (MIK-3078)
- `trvl hidden-city` CLI command: evaluate a hidden-city routing with customisable risk threshold and booking URL (MIK-3078)
- **`OpportunityWatch`** — rolling-window watcher with configurable interval and favourite-destinations resolver; `internal/watch` package wires `OpportunityWatch` type with `Start`/`Stop` lifecycle and delivers scored opportunities to a channel (MIK-3065)

### Breaking Changes
- **`ValueScore` removed** — `DiscoverResult.value_score` (float64, 0-1) is replaced by `ProfileMatch` (int, 0-100) and `MatchBreakdown` (map[string]float64). Consumers of the `value_score` JSON field must migrate to `profile_match`. The score is computed on-demand; no data migration is required. To restore the old behaviour, revert the commit introducing this change.

### Added
- **`ProfileMatch` score** — `DiscoverResult.profile_match` (int 0-100) is a weighted sum across 12 factors: budget_fit, loyalty_earn, time_window_fit, directness, district_match, airport_affinity, early_connection_compliance, status_retention, lounge_at_transit, bucket_list_boost, warsaw_filter (hard exclusion), family_mode_compatibility. Factor weights are user-tunable via `match_weights` in `preferences.json`.
- **Per-factor breakdown** — `DiscoverResult.match_breakdown` (map[string]float64) exposes per-factor scores in [0,1] so users can see exactly why a trip scored 73 instead of 91.
- **`--explain` flag** — `trvl discover --explain` prints an ASCII progress bar table of per-factor scores beneath the main result table.
- **`match_weights` in preferences** — user can override default factor weights; missing keys keep the built-in default.
- **`airport_affinity` in preferences** — maps destination IATA codes to affinity scores in [0,1]; used by the airport_affinity factor.
- **`excluded_destinations` in preferences** — hard-excludes cities or airport codes from all results (warsaw_filter returns 0 for these; ProfileMatch returns 0 for the whole result).
- **`FixHintCode` enum** — typed root-cause classifier (`AKAMAI_BLOCK`, `DNS_FAIL`, `TLS_TIMEOUT`, `COOKIE_EXPIRED`, `RATE_LIMITED`, `RESPONSE_SHAPE_CHANGED`, `PREFLIGHT_FAILED`, `UNCLASSIFIED`) surfaced in MCP search responses (`fix_hint_code` field on `provider_statuses`) and in the `provider_health` aggregate (`last_hint_code`); persisted per-entry in `~/.trvl/health.jsonl` (`hint_code` field)

### Changed
- **Hotel singleflight cache keys** — hotel deduplication keys now include the full `HotelSearchOptions` filter set, with order-insensitive amenity matching, so distinct hotel searches no longer share in-flight results accidentally
- **`providerFixHint`** — now delegates to the new `classifyProviderError` classifier; hint text updated to be more actionable and accurate (back-compatible: the `fix_hint` string field is still populated)

### Fixed
- **MCP handler race safety** — singleflight winners for flights, ground, and hotels are now cloned before caller-specific post-filtering mutates counts, slices, or nested pointers
- **Singleflight timeout isolation** — shared flight, ground, and hotel upstream work now outlives the first caller's timeout, so one canceled request no longer aborts identical concurrent searches for other callers
- **Watch scheduler shutdown** — calling `Stop()` before `Start()` no longer deadlocks; lifecycle state is synchronized and remains idempotent
- **Race regression coverage** — new and expanded tests lock in caller-private result cloning and scheduler lifecycle behavior across the touched packages

## [1.0.3] - 2026-04-20

### Added
- **54 MCP tools** — 4 new tools: `watch_price` (price alert with target threshold), `list_watches`, `check_watches` (re-check all watches for drops), `provider_health` (per-provider success rate, latency, errors)
- **Provider health logging** — append-only `~/.trvl/health.jsonl` records every provider API call with timing and status. Auto-rotates at 1MB
- **Singleflight deduplication** — concurrent searches for the same route coalesce into a single API call (flights, hotels, ground)

### Changed
- **Connection pooling** — MaxIdleConns 100, MaxIdleConnsPerHost 10, IdleConnTimeout 90s for better HTTP connection reuse
- **File splits** — `tools_hotels.go` 939→652 LOC, `tools_flights.go` 883→640 LOC
- **Magic number documentation** — all bare numeric constants annotated with "Why N:" reasoning
- **Legal disclaimer** — expanded to cover all providers, states ToS risk explicitly
- **Booking.com cold-start** — cookie timeout 5s→12s, eager pre-warm at NewRuntime init

### Fixed
- **Hotel post-filter** — external provider results (Airbnb, Booking.com) without ratings no longer dropped by MinRating filter
- **Optimizer currency** — pre-priced ground candidates use input currency instead of hardcoded EUR
- **All staticcheck warnings** resolved (7 total)
- **CI coverage threshold** raised from 50% to 75%

## [1.0.0] - 2026-04-20

### Added
- **50 MCP tools** — `search_hotel_by_name` (cross-provider name-based property lookup with fuzzy matching) and `onboard_profile` (5-phase progressive interview for new users)
- **Profile-driven search** — traveller profile (TravelMode, CityIntelligence, BookingStrategy, PreferenceElasticity, DestinationRelationship) now drives search behaviour as soft defaults. Flights use preferred airlines/alliance/cabin from booking history. Hotels use star rating, property type, price ceiling, and city-specific neighbourhood preferences. Ground transport uses preferred mode. Explicit parameters always override
- **LLM-aware onboarding Phase 0** — before asking questions, the LLM states what it already knows/infers about the user and asks to confirm. Confirmed inferences skip redundant questions in later phases
- **Travel personality model** — captures WHY the user makes decisions: travel modes (solo_remote, with_partner, with_kids, weekend_break), city intelligence (per-city knowledge depth, neighbourhoods, restaurants), booking strategies (machine-readable patterns), price elasticity factors, destination relationship graph (why each city matters)
- **Eurostar Snap routing** — 14-day rolling window for Snap fares, 9 validated routes from snap.eurostar.com, Antwerp station support

### Changed
- **Optimizer currency consistency** — pre-priced ground candidates (rail/ferry) now use the input currency instead of hardcoded EUR, enabling correct cross-candidate cost comparison
- **Hotel post-filter** — external provider results (Airbnb, Booking.com, Hostelworld) without Google-scale ratings now pass through the MinRating filter instead of being dropped. Fixes Paris 121→1 survivor regression for multi-provider searches

### Fixed
- **All 7 staticcheck warnings resolved** — nil contexts replaced with context.TODO(), impossible nil checks removed, unused functions deleted
- **Stale branches cleaned** — removed 6 local + 13 remote branches (copilot, dependabot, worktree artifacts). Only main remains

## [0.9.2] - 2026-04-19

### Changed
- **README overhaul** — updated to reflect 36 hack detectors (was 18), 5 hotel providers (was 3), 574 Go files / 74K LOC / 32 packages / 5400+ tests, added Traveller Profile section
- **Coverage push** — hacks 65.6→91.9%, providers 75.5→80.0%, trip 68.6→71.4%, cmd/trvl 63.0→63.7%
- **Traveller Profile** now tracks Eurostar, European Sleeper, FlixBus AMS↔Paris/Prague routes, Club Eurostar and Tallink Club One memberships, Uber+Bolt rides, public holiday tracking for 9 countries

## [0.9.1] - 2026-04-19

### Added
- **Traveller profile system** — learns from booking history via email parsing + LLM sampling. 3 new MCP tools (`build_profile`, `add_booking`, `interview_trip`) and CLI `trvl profile` command. Profile stores FF statuses, booking history (flights/hotels/Airbnb/ground/rides), accommodation preferences, travel hacks used, family composition, seasonal patterns. Pre-search interviews skip questions the profile already answers
- **Optimizer: EUR currency normalization** — adds `Currency` field to SearchOptions, maps to Google Flights `gl=` parameter (30 currency→country mappings). Optimizer forces EUR so flights, rail, and ferry candidates compare in the same currency
- **Back-to-back ticketing: live price comparison** — 4 parallel flight searches compare 2x one-way vs 2x overlapping round-trip. Shows concrete savings with prices and booking URLs. Falls back to advisory on search failure
- **Booking.com cold-start fix** — background cookie warm-up via `WarmBrowserCookies`. Kooky Keychain read runs concurrently with initialization, eliminating 5-10s sequential blocking on first request
- **Hotel name similarity guard** — `nameSimilar()` uses word-level Jaccard similarity (≥0.5 threshold) to prevent geo-proximity merging of unrelated nearby hotels
- Now 48 MCP tools (was 45), 574 Go files, 5400+ tests

### Changed
- **DRY refactoring** — `newProviderLimiter` replaces 18 identical rate limiters in ground/; `launchProvider` replaces 20 identical goroutine blocks; `resolveAndSearch[T]` generic for FlixBus/RegioJet autocomplete; 12 MCP schema builder helpers replace 597 bare map literals; `validateOriginDest`/`validateDate` consolidate repeated validation
- **SharedClient singleton** — `batchexec.SharedClient()` replaces duplicate `sync.Once` in flights/

### Fixed
- **Hotel dedup too aggressive** — `sameHotelCandidate` now requires address match OR proximity (not just either); different addresses → never merge; geo threshold tightened 500m→100m, geo-merge 150m→50m. Paris: 121→1 collapse fixed (now 156→61 post-merge)

## [0.9.0] - 2026-04-19

### Added
- **Optimizer: departure tax avoidance** — when origin is in a high-tax country (NL €26, DE €15, GB €14), automatically expands candidates to nearby zero-tax airports where tax savings exceed ground transport cost
- **Optimizer: rail competition alternatives** — for routes matching competitive rail corridors (MAD→BCN 4 operators from €7, Italy duopoly from €10, PRG→VIE from €9), the optimizer includes pre-priced train options ranked alongside flights
- **Optimizer: ferry cabin as hotel** — overnight ferry routes (HEL→ARN €35 cabin vs €120 hotel) appear as pre-priced candidates that combine transport + accommodation savings
- **Pre-priced candidate pipeline** — ground transport alternatives (rail, ferry) skip flight search and bag fee computation, ranked directly by all-in cost against flight options
- **Error fare detection** — 36th hack detector flags prices below 50% of the route-distance floor as likely error fares (book immediately) and below-floor prices as flash sales; zero API calls, uses haversine distance classification across 5 route tiers
- **New accessor functions**: `DepartureTaxSavings`, `ZeroTaxAlternatives`, `CompetitiveRailRoute`, `OvernightFerryRoute` expose hack data to the optimizer
- Optimizer now has 9 expansion strategies (was 6): baseline, alternative origins, alternative destinations, rail+fly, date flex, hidden city, departure tax, rail competition, ferry cabin
- Now 45 MCP tools, 36 hack detectors

### Fixed
- **Cross-currency savings display** — optimizer no longer shows misleading savings when comparing candidates in different currencies (e.g. EUR ferry vs RUB flight); same-currency candidates sort first, cross-currency options show no savings
- **Hotel cross-currency savings** — ComputeSavings now groups price sources by currency before comparing; prevents nonsensical "Save €17824" when comparing RUB vs EUR sources for the same hotel

## [0.8.1] - 2026-04-19

### Added
- **CLI `trvl optimize`**: unified optimizer command — searches all combinations of origins, destinations, dates, airlines, and transport modes to find the cheapest booking strategy
- **Self-Transfer detector**: 10 LCC hub airports (BGY, STN, BVA, CRL, CIA, BCN, BUD, DUB, LTN, AMS) with minimum connection times
- **Regional Pass Calculator**: 7 European passes (Deutschlandticket, Klimaticket, Swiss Half Fare, OV-chipkaart, ÖBB Vorteilscard, BahnCard 25/50)
- **Optimizer: date flexibility** via CalendarGraph (1 API call for entire ±N day range)
- **Optimizer: hidden city candidates** — searches beyond airline hub destinations for connecting discounts

## [0.8.0] - 2026-04-19

### Added
- **Unified trip optimizer engine** (`optimize_booking`, 45th MCP tool): 4-phase architecture (expand→search→price→rank) that composes all pricing primitives into optimal booking strategies
- **Return rail skip**: KLM train legs safely skippable both directions (user-confirmed)
- **Throwaway ground segment**: book bus/train past destination, exit early (no enforcement)
- **Eurostar return pricing**: return premium often just €5-10 over one-way
- **Cross-border rail arbitrage**: same train cheaper on ÖBB/DB/CD vs SNCF/Trenitalia
- **Ferry cabin as hotel**: overnight ferry cabin replaces hotel night (HEL→ARN €35 vs €120 hotel)
- **EU261 awareness**: €250-600 compensation rights on EU-departing flights
- **Complete pricing fundamentals** documented for airlines, trains, buses, ferries, hotels, Airbnb — the systematic framework for discovering hacks from discount primitives
- **Composite hack patterns** documented (rail+fly + hidden city + return skip stacking)
- Now 45 MCP tools, 43 CLI commands, 34 hack detectors

## [0.7.1] - 2026-04-19

### Added
- **Auto-trigger hacks on flight search**: CLI shows up to 3 hack tips after every `trvl flights` search; MCP `search_flights` includes hacks array in response
- **Miles tracking**: estimates miles earned per flight (Flying Blue revenue-based, Oneworld distance-based), shows balance updates in CLI
- **Miles redemption value**: calculates cents-per-mile, flags good redemption opportunities
- `internal/baggage` test coverage: 0% → 100% (37 tests)

## [0.7.0] - 2026-04-19

### Added
- **Trip viability pre-check**: new `assess_trip` MCP tool — GO/WAIT/NO_GO verdict checking flights, hotels, visa, weather in parallel (44th MCP tool)
- **Flight combination optimizer**: compares round-trip vs split-airline one-ways; nested/overlapping return tickets for multi-trip savings
- **Rail+Fly Fare Zone Arbitrage**: detects when booking via Antwerp (KLM), Cologne (Lufthansa), Brussels (Air France), Basel (Swiss) triggers cheaper fare zones — train included free
- **Fare Breakpoint Routing**: suggests routing via IST/DOH/DXB/MAD/LIS that exploits IATA fare construction zone boundaries
- **Destination Airport Alternatives**: 15 alternative airports across 12 primaries (BGY for Milan, GRO for Barcelona, BVA for Paris, etc.)
- **Fuel Surcharge Avoidance**: flags high-YQ airlines (BA £400+, LH €250+) and suggests zero-YQ alternatives
- **Advance Purchase Window**: classifies routes into 5 types and advises optimal booking timing
- **Group Booking Split**: advises splitting 3+ passenger searches for cheaper fare buckets
- **Alliance baggage system**: full SkyTeam/Oneworld/Star Alliance membership database with per-tier baggage benefit resolution
- **All-in pricing**: CLI "All-in" column and MCP `all_in_cost` field add bag fees and subtract FF benefits for honest LCC vs full-service comparison
- Now 44 MCP tools, 26 hack detectors

## [0.6.11] - 2026-04-19

### Added
- **Cross-provider price savings**: when multiple providers (Google, Booking, Airbnb, etc.) return the same hotel, surfaces the savings opportunity — "Save €55 via Booking" — in CLI table and MCP JSON output
- **Trip date optimizer**: new `optimize_trip_dates` MCP tool finds cheapest departure dates across a date range using a single CalendarGraph API call (43rd MCP tool)

### Changed
- `search_dates` MCP handler switched from legacy per-date search (N API calls) to CalendarGraph (1 call) — ~29x fewer requests for a 30-day range
- Accommodation split hack hotel lookups now use `MaxPages: 1` — ~8x fewer HTTP requests per segment
- `plan_trip` now shares a single HTTP client across its 3 parallel searches for connection reuse

## [0.6.10] - 2026-04-18

### Fixed
- **Root cause of hung queries**: server context was 120s, overriding the 60s tool timeout — every search got 2 minutes before timing out
- Per-provider 30s timeout prevents any single provider from blocking the search
- Hotel pagination properly bails on context cancellation (was silently continuing)
- Browser cookie lookup reduced from 15s to 5s (keychain is <1s when cached)
- Browser escape hatch wait reduced from 15s to 10s
- Panic recovery in MCP tool handlers (converts crash to error)
- Circuit breaker skips providers with 5+ consecutive failures
- Ferryhopper graceful handling of non-JSON MCP responses
- Flight parse failures logged at debug level

## [0.6.9] - 2026-04-18

### Fixed
- Hung query protection: 90-second per-tool timeout prevents indefinitely blocked MCP calls
- Concurrency limiter: max 4 parallel tool executions (prevents rate limit exhaustion when AI agents spawn 8+ simultaneous searches)
- Queued requests timeout gracefully instead of hanging

## [0.6.8] - 2026-04-18

### Changed
- mcp test suite: 175s → 0.7s (250x speedup with `-short`, 2.3x without)
- Ground transport: DI refactor enables httptest coverage for 5 providers
- Test coverage: 59% → 64% (architectural ceiling without full DI rewrite)

### Fixed
- Data race in `SetExternalProviderRuntime` (sync.RWMutex guard)
- TestProvider missing `${num_nights}` computation (Hostelworld probe fix)
- TestProvider missing `${location}` override for URL-based providers (Airbnb)
- staticcheck SA1012 nil context in test

### Added
- 10 live HTTP tests gated behind `testing.Short()` (skip in fast mode)
- `t.Parallel()` on ~423 independent mcp tests
- httptest DI tests for FlixBus, RegioJet, SNCF, Trainline, Eckeroline

## [0.6.7] - 2026-04-18

### Fixed
- TestProvider: compute `${num_nights}` from checkin/checkout (fixes Hostelworld 400 errors via `--probe`)
- TestProvider: apply `${location}` override for URL-based providers (Airbnb slug resolution in probe path)

### Added
- Google Flights live probe test (HEL→BCN, 143 results verified)
- Ground transport live probe test (Helsinki→Tallinn, 54 routes from 5 providers)
- 120+ new test cases: mcp arg parsing, watch notifier, trips monitor, cookies sanitization
- Coverage: 58.7% → 59.1%

## [0.6.6] - 2026-04-18

### Added
- `trvl providers status` command — health classification (healthy/stale/error), relative timestamps, color output
- `trvl providers status --probe` — live test request against each provider
- Airbnb city_lookup with 130 global cities (URL-safe slug resolution)
- Hostelworld global city coverage: 53→103 cities (Asia-Pacific, Americas, Africa, Oceania)
- httptest-based integration tests for providers, ground transport, hack detectors
- Shared httptest helper in `internal/testutil/`

### Changed
- Provider runtime: city_lookup now overrides `${location}` for URL-based providers (Airbnb)
- Provider catalog: updated Hostelworld/Booking/Airbnb auth hints with correct city IDs and rating scales

## [0.6.5] - 2026-04-18

### Fixed
- All hotel ratings normalized to 0-10 scale (Google 0-5 ×2, Hostelworld 0-100 ×0.1, Airbnb 0-5 ×2)
- Booking.com probe: replaced stale CSRF extraction with production browser-cookie config
- Hostelworld probe: corrected Paris city ID (59→14) and field mappings
- Google EU consent page bypass: detect and retry with pre-seeded consent cookies
- Rooms command: search-page fallback now works for raw hotel ID lookups
- macOS Keychain prompt spam during tests: skip kooky lookups in test binaries
- Preferences auto-migration: MinHotelRating ≤5 auto-doubled to 0-10 scale

### Added
- Google Hotels live probe test
- Airbnb description enrichment (PDP fetch from Niobe SSR cache)
- Booking.com global city coverage (130 cities across all continents)
- `rating_scale` in provider catalog skeleton (guides LLM config generation)
- DESIGN.md architecture documentation
- 83 new test files / test functions covering display formatting, provider edge cases

### Changed
- Provider runtime split: runtime.go (993 LOC) + enrichment.go (257) + auth.go (583)
- Provider catalog: updated auth hints for Booking (browser cookies), Airbnb (SSR), Hostelworld (city IDs)
- MCP tool count: 42→43
- Coverage: 50%→58%

## [0.6.1] - 2026-04-16

### Changed
- `trvl upgrade` command for seamless binary updates
- README rewritten with agent-first install as the recommended setup path
- CLI command count corrected from 41 to 39

## [0.3.15] - 2026-04-12

### Added
- `trvl search QUERY` CLI command — natural-language travel search with CLI
  parity for the `search_natural` MCP tool. Parses intent (flight/hotel/route/
  deals), IATA codes, "from X to Y" patterns, ISO dates, and "next weekend"
  relative dates. Dispatches to the appropriate concrete command. Includes
  `--dry-run` and `--json` flags.
- `trvl calendar [trip_id|--last] [--output FILE]` CLI command — exports
  saved trips (or the most recent search) as RFC 5545 iCalendar (.ics) files
  for import into Apple Calendar, Google Calendar, Outlook, etc. Each leg
  becomes a VEVENT; hotels are emitted as multi-day all-day events; confirmed
  legs get STATUS:CONFIRMED.
- `internal/nlsearch` package — shared natural-language query parser used
  by both the CLI `search` command and (in a future cleanup) the MCP
  `search_natural` tool.
- `internal/calendar` package — pure iCalendar writer (no I/O), reusable
  by both the CLI and any future surface that needs .ics export.

### Changed
- Stale CHANGELOG header `0.6.0` → corrected to `0.3.15` (the versioning was
  briefly inconsistent during the 0.5/0.6 sprint; tags have always been the
  source of truth and ship as v0.3.x).
- README, demo.tape, plugin.json, and the subcommand-count test updated for
  the new total of 38 CLI commands (was 36; +2 net after adding search,
  calendar, and removing an undisciplined `currency` command experiment).

### Removed
- An experimental `trvl currency` CLI command that was added earlier the same
  day. Removed before shipping after a CPO/CTO review concluded it had no
  user-job justification, no Kano signal, and demonstrated feature-creep
  drift. The underlying `destinations.ConvertCurrency` and `ConvertToEUR`
  helpers remain — they are used by every other search command for display-
  currency conversion.

## [0.6.0] - 2026-04-05

### Added
- `trvl hacks` command and `detect_travel_hacks` MCP tool: 18-detector parallel engine for flight and ground savings opportunities — throwaway, hidden-city, positioning, split, night-transport, stopover, date-flex, open-jaw, ferry-positioning, multi-stop, currency-arbitrage, calendar-conflict, tuesday-booking, low-cost-carrier, and four multi-modal detectors
- `trvl hacks-accom` command and `detect_accommodation_hacks` MCP tool: hotel split detection across multi-city stays
- `trvl trips` command (7 subcommands) and 5 MCP tools (`list_trips`, `get_trip`, `create_trip`, `add_trip_leg`, `mark_trip_booked`): persistent trip management stored in `~/.trvl/trips.json`
- `trvl prefs` command and `get_preferences` MCP tool: user travel profile (`~/.trvl/preferences.json`) — home airport, seat preference, FF programs, bag rules, family members
- `search_natural` MCP tool: free-text query parsing via keyword heuristic parser; dispatches to `search_flights`, `search_route`, or `search_hotels` based on detected intent
- `hotel_rooms` MCP tool: room-level availability, board type, and cancellation policy
- MCP progress notifications: long-running searches stream `notifications/progress` tokens to the client
- MCP resource subscriptions: price-watch resources send `notifications/resources/updated` on price changes
- Hack deduplication: `DetectAll` removes functionally identical hacks found by multiple detectors (same type + savings ± EUR 5 + destination airport)
- Tallink rate limit increased from 5 req/min to 10 req/min to handle parallel hacks detectors without context-deadline errors

### Fixed
- Stderr noise: "no X station for" and "no X city found for" provider errors demoted from WARN to DEBUG — these are expected when a provider does not serve a route, not operational failures
- Duplicate hacks in output: `multimodal_positioning` and `ferry_positioning` occasionally found the same ground+flight combo independently; deduplication now collapses these

### Changed
- MCP tools expanded from 19 to 29 (added 10 tools across hacks, trips, preferences, natural search, hotel rooms)
- CLI commands expanded from 24 to 29 (added `hacks`, `hacks-accom`, `trips`, `prefs`, plus `rooms`)
- 19/19 packages compile clean; govulncheck clean

## [0.5.0] - 2026-04-05

### Added
- `trvl route` command and `search_route` MCP tool: multi-modal routing engine combining flights, trains, buses and ferries into Pareto-optimal itineraries — 19th MCP tool
- Ferry providers (5 new ground transport providers, total now 16):
  - **Tallink** — live REST API (`book.tallink.com/api/voyage-avails`), real prices from Baltic Sea sailings (Helsinki, Tallinn, Stockholm, Riga, Turku)
  - **Viking Line** — reference schedule (Baltic Sea: Helsinki, Tallinn, Stockholm, Turku, Mariehamn); will be replaced by Distribusion API
  - **Eckerö Line** — live Magento AJAX API (`getdepartures` endpoint), Helsinki ↔ Tallinn (M/S Finlandia)
  - **Stena Line** — reference schedule (North Sea + Baltic: Gothenburg, Kiel, Karlskrona, Gdynia, Travemünde, Liepāja, …); will be replaced by Distribusion API
  - **DFDS** — live date availability API (`travel-search-prod.dfds-pax-web.com`), North Sea + Baltic (Kiel, Amsterdam, Newcastle, Copenhagen, Kapellskär, Paldiski, …)
- Chrome 146 TLS fingerprint (Post-Quantum + ECH) for improved provider compatibility
- 26 European hub cities for route optimization in the routing engine
- Pareto-optimal itinerary filtering (price vs. duration trade-off)

### Changed
- Ground transport expanded from 11 to 16 providers (added 5 ferry providers)
- MCP tools expanded from 18 to 19 (added `search_route`)
- CLI commands expanded from 22 to 24 (added `route`, `ferry`)
- Removed HTML scraping fallbacks from Viking Line and Stena Line (replaced with clean reference schedules pending Distribusion integration)
- Removed HTML scraping fallback from DFDS (availability API + reference schedule sufficient)

## [0.4.0] - 2026-04-04

### Added
- `trvl trip` command and `plan_trip` MCP tool: one-search trip planning (flights + hotels in parallel) — 18th MCP tool
- Renfe Spanish Railways provider (11th ground transport provider): AVE high-speed and regional rail via Playwright browser scraper; fares EUR 36+ (`renfe.go`)
- SNCF curl-based BFF fallback: shells out to macOS `curl` (BoringSSL TLS fingerprint bypasses Datadome) before trying Playwright scraper; tries three known BFF API paths (`sncf.go`)
- VR Finnish Railways provider (10th ground transport provider) via Digitransit GraphQL API; fixed fares EUR 14+ (`digitransit.go`)
- ÖBB Austrian Railways provider via browser automation (Playwright scraper); live Railjet fares EUR 38+ (`oebb.go`, `browser_scraper.go`)
- NS Dutch Railways provider: schedule search via public API with embedded key (`ns.go`)
- Trainline provider: aggregated rail across major European operators (`f92d7bd`)
- Airport transfer search as ground sub-command (`f58bb49`)
- `trvl watch` daemon mode: background polling on a configurable schedule (`7d07e89`)
- `internal/cookies` package: browser cookie auth for CAPTCHA-protected providers (SNCF, Trainline, ÖBB) (`f529104`)
- `ResolveLocationName`: IATA code → human-readable city name in hotels and ground results
- `DetectSourceCurrency`: session-cached currency detection (single API call, reused across renders)
- IATA alias map with 34 airport codes mapped to city names for deal filtering

### Changed
- Ground transport expanded from 7 to 11 providers (added VR Finnish Railways, ÖBB Austrian Railways, NS Dutch Railways, Renfe Spanish Railways)
- MCP tools expanded from 17 to 18 (added `plan_trip`)
- `--currency` flag now available on all 22 CLI commands (dates, explore, grid, ground, deals, weekend, suggest, multi-city — previously flights + hotels only)
- Ground transport deduplication: same provider + time + price collapsed into one row (`7e82ede`)
- Demo GIF rewritten as 4-act narrative: Discover / Plan / Book / Monitor (`85385b7`, `181eab3`)
- `DetectSourceCurrency` result cached per session — eliminates repeated API calls on calendar/grid renders

### Fixed
- Hardcoded EUR removed from entire codebase — API source currency detected and stamped at response layer (`c9b7ab0`, `c40cd02`, `acd3f8a`)
- Grid, explore, and calendar were mislabelling PLN (and other currencies) as EUR (`71c95e2`, `19f9423`, `d875abb`)
- DB trains: endpoint corrected, real prices extracted from `angebote.preise.gesamt.ab` (`b402c4c`)
- Ground date filtering: RegioJet multi-day results now filtered to requested departure date (`38aa83c`)
- Ground train-type recognition: RegioJet vehicleTypes mapping corrected (trains no longer classified as buses)
- Deal city-name filtering: substring + IATA alias match (e.g. "Paris" matches CDG/ORY deals) (`38aa83c`)
- UTF-8 deal title truncation: byte-slice cut replaced with rune-safe truncation

## [0.3.0] - 2026-04-03

### Added
- Ground transport: FlixBus, RegioJet, Eurostar/Snap, Deutsche Bahn, SNCF, Transitous
- Price tracking: `trvl watch` with threshold alerts and history
- Hotel amenity extraction from Google Hotels search data (18 codes + description)
- Hotel detail page amenity enrichment (opt-in, fetches full amenity lists per hotel)
- Hotel amenity filtering (pool, wifi, breakfast, etc.)
- Hotel filters: price range, rating, distance from center, sort by stars/distance
- Restaurant search via Google Maps (MCP tool)
- MCP 2025-11-25 full compliance: ping, completion/complete, logging/setLevel
- Rate limiting on all API clients
- Watch MCP resources: trvl://watches, trvl://watch/{id}
- Travel deals aggregation from 4 RSS feeds (Secret Flying, Fly4Free, Holiday Pirates, The Points Guy)
- Deal alerts shown inline in flight search results
- Multi-airport search: `trvl flights AMS,EIN HEL,TLL` searches all combos in parallel
- Route watches: monitor prices without specific dates (scans next 60 days)
- Smart price advice: error fare detection (30%+ drops), trend warnings
- CLI eye-candy: box-drawing banners, summaries, booking hints
- Display-width-aware table alignment (ANSI colors + emojis)
- CODE_OF_CONDUCT.md (Contributor Covenant 2.1)

### Changed
- Eurostar searches Snap deals first (up to 50% off), falls back to regular fares
- Improved test coverage across all packages (trip 47%→84%, watch 56%→84%, batchexec 66%→74%)
- README restructured: MCP-first, CLI secondary
- 16 MCP tools (was 13), 20 CLI commands (was 14)

### Fixed
- Zero-price routes filtered from ground transport results
- RegioJet currency parameter now passed correctly
- FlixBus city names populated in leg data
- HTTP server timeouts added (DoS prevention)
- Table alignment with ANSI color codes and emoji characters

## [0.2.0] - 2026-04-02

### Added
- **Explore destinations** — discover cheapest flights from any airport (`trvl explore HEL`)
- **CalendarGraph** — visual price grid across departure and return date ranges (`trvl grid`)
- **Destination intelligence** — weather, safety, holidays, currency, and country info from 6 free APIs (`destination_info` tool)
- **Trip cost calculator** — estimate total cost including flights and hotel (`calculate_trip_cost` tool)
- **Multi-city optimizer** — find cheapest routing order for up to 6 cities (`optimize_multi_city` tool)
- **Weekend getaway finder** — cheapest weekend destinations ranked by total cost (`weekend_getaway` tool)
- **Smart date suggestions** — analyze prices around a target date with savings insights (`suggest_dates` tool)
- **Hotel reviews** — guest review summaries and scores (`hotel_reviews` tool)
- **Nearby places** — points of interest from OpenStreetMap (`nearby_places` tool)
- **Travel guide** — local tips and practical info (`travel_guide` tool)
- **Local events** — upcoming events at destination (`local_events` tool)
- MCP structured content with content annotations (`audience`, `priority`)
- MCP elicitation for interactive parameter collection
- MCP output schemas with full JSON Schema validation for all tools
- MCP prompts: `plan-trip`, `find-cheapest-dates`, `compare-hotels`
- MCP resources: airport codes, flight/hotel usage guides, session summary
- Progressive disclosure with follow-up suggestions in every response
- Travel profile support for personalized recommendations
- 4 Claude Code skills: trvl, travel-hacks, travel-agent, travel-agent-compact
- Booking links to Google Flights and Google Hotels in results
- Docker support (`docker run ghcr.io/mikkoparkkola/trvl`)

### Changed
- Expanded from 4 to 13 MCP tools
- Upgraded MCP protocol to v2025-11-25

## [0.1.0] - 2026-03-15

### Added
- **Flight search** — real-time Google Flights data via batchexecute protocol (`search_flights` tool)
- **Date search** — cheapest flight prices across a date range (`search_dates` tool)
- **Hotel search** — Google Hotels with ratings, prices, and amenities (`search_hotels` tool)
- **Hotel prices** — compare prices across booking providers (`hotel_prices` tool)
- Chrome TLS fingerprint via utls for reliable access
- MCP server with stdio transport (4 tools)
- CLI with table and JSON output formats
- Rate limiting with token bucket and exponential backoff
- Single static binary, zero runtime dependencies
- MIT license

[0.5.0]: https://github.com/MikkoParkkola/trvl/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/MikkoParkkola/trvl/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/MikkoParkkola/trvl/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/MikkoParkkola/trvl/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/MikkoParkkola/trvl/releases/tag/v0.1.0
