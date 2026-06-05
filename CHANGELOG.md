# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.6.0] - 2026-06-04

### Added
- **Door-to-door transfer planning** — closes the first/last-mile gap (the part travellers Google every trip). New capabilities, all reachable via the `travel` smart router:
  - **Transfer comparison card** — `search_airport_transfers` now returns every mode (public transit, airport express, metro, taxi, private transfer, ride-hail) as a choosable option with time, price, pros/cons, and **grounded step-by-step instructions**, plus cheapest / fastest / best-value / most-luggage-friendly labels. No single "best" is imposed; the traveller decides.
  - **Leave-By Scheduler** (`plan_journey` intent) — answers "when do I leave home to reach the gate comfortably, not last-minute?" by backward induction from the flight: airport check-in/security buffer + ground time + mode variance + walk + safety margin, with surfaced assumptions, a confidence band, and a fallback. Conservative by design — never optimistic.
  - **Home→airport auto-stitch** — pass `origin` to `plan_journey` and it computes the home→airport leg itself (comparison card + schedule) instead of requiring the ground option by hand.
  - **Calendar handoff** — `plan_journey` with `as_ics` returns an iCalendar `.ics` carrying a "Leave home" event with a reminder alarm; drop it straight into Apple Calendar, Google Calendar, or Outlook.
  - **Proactive surfacing** — after a flight search, trvl now offers the airport transfer and the leave-by schedule automatically.
  - **Ride-hail options** — Uber and Bolt as deep-links in the card (no API, no scraping, no key); the app shows the real price before you confirm.
  - **Curated airport knowledge base** — 31 major global hubs with conservative, grounded check-in/security buffers so the scheduler uses airport-specific guidance instead of a generic default.
- **Time + location awareness by default** (CLI + MCP) — flight search resolves a missing origin from your saved home airport, then best-effort from your current location; results carry booking-time context. Includes an update-checker fix.

### Changed
- **Token efficiency front-and-centre** — the smart `travel` router advertises **1 tool (~378 tokens) instead of 64 (~33,500 tokens)** in `tools/list`, a measured **98.9% smaller context footprint**. README/npm now lead with this; corrected tool-count drift across all surfaces and added a registry-derived guard so the counts can't silently drift again.
- **Go toolchain pinned to 1.26.4** (was 1.26.3) — picks up the upstream standard-library fixes for GO-2026-5037 (crypto/x509) and GO-2026-5039 (net/textproto).
- Commercial-license sponsor rail added to the README.

### CI / Release
- npm package now published via **Trusted Publishing (OIDC)**, dropping the stored token; opt-in npm-publish job wired into the release workflow.

## [1.5.0] - 2026-05-26

### Added
- **Wizz Air flight provider** — public unauthenticated `be.wizzair.com` timetable endpoint (no key); recovers another ultra-LCC that Google/GDS omit, mapped into the aggregated flight search.
- **Transavia flight provider (opt-in)** — official public API, env-gated via `TRANSAVIA_API_KEY` (AFKLM-style opt-in); no-op when unset.
- **HomeToGo hotel provider** — public SSR+JSON vacation-rental aggregator (no key/browser/proxy); brings whole-apartment inventory alongside the existing 5 hotel sources.
- **Travelpayouts/Aviasales price signals (opt-in)** — `trvl pricetrends ORIGIN DEST`, env-gated via `TRAVELPAYOUTS_TOKEN`; indicative cached fares, deliberately kept out of the bookable merge. Disabled by default.
- **Air quality** — `trvl air CITY` via Open-Meteo Air Quality (European AQI + pollutants), free, no key.
- **Sun times** — `trvl sun CITY` via sunrise-sunset.org (sunrise/sunset/twilight), free, no key.
- **Bike-share** — `trvl bikes CITY` via CityBikes (nearest network + live station availability), free, no key.
- **Full per-leg flight detail in CLI tables** — aircraft type, layover-annotated route, human dates with overnight (`+1`) markers.
- **Direct booking links** beneath flight and hotel result tables (numbered to a `#` column); hotels also link photos.
- **Adults-only hotel filter** — `--children` auto-excludes adults-only properties for parties with children.

### Changed
- Hotel search surfaces degraded/failed providers in CLI output; Booking.com now fails loudly with an actionable cookie fix-hint instead of silently.

## [1.3.0] - 2026-05-25

### Added
- **Nested/overlapping round-trip optimizer (MIK-3076)** — `optimize_nested_rt` MCP tool and `trvl nested` CLI find whether overlapping round-trips beat two separate returns for two visits between the same cities; both windows priced live in parallel. `optimize_booking` routes here when `multi_visit=true`.
- **Direct Ryanair provider (MIK-4963)** — recovers the ultra-LCC Google/GDS omit, via Fare Finder (no API key); carrier/flight number, exact times+price, bag-aware comparable cost.
- **Comparable all-in flight ranking (MIK-4962)** — rank by base fare + unavoidable bag fees − per-user frequent-flyer benefits, FX-normalized; LCC base fares no longer outrank bag-included fares.
- **Evidence envelope (MIK-4950)** — richer per-provider status + completeness; a provider timeout never renders as "no flights found".
- **Multisource resolver for flights & ground (MIK-4951)** — collapses the same itinerary across providers into one result with `cheapest_source`.
- **Price freshness + source-quality registry (MIK-4952)** — stale prices are never labeled "cheapest".
- **Kiwi advanced search (MIK-4956 Phase A)** — round-trip + flexible-date (±0–3 days) pass-through.
- **Canonicalizers (MIK-4949)** — shared ParseMoney/ParseTemporal/ParseDuration/ParsePlace/FormatProviderDate in internal/models.
- **MIK-3087 hotel arbitrage engine** — `internal/hotelarb` now tracks active hotel holds in `~/.trvl/active_holds.json`, evaluates manual re-book decisions against fresh quotes, detects sub-48h last-minute hotel drops at 25%+ below last seen price, and compares hotel points offers against cash using conservative loyalty-program floor values.
- **Hotel re-book CLI flow** — `trvl prices hold` saves a refundable reservation and `trvl prices rebook` fetches current provider prices (or accepts `--current-price`) to present a hold-current vs. manual re-book recommendation.
- **Last-minute hotel watch mode** — `trvl watch add --type hotel --last-minute` and MCP `watch_price(last_minute=true)` surface sub-48h hotel drops through the existing watch notifier/webhook path.
- **Hotel points arbitrage offers** — `trvl points-value --offer program:points[:cash_fees]` compares multiple hotel redemption options in one command; Wyndham Rewards was added to the hotel loyalty program table.

### Added
- **`trvl flights --rail-fly` (MIK-3079)** — opt-in expansion that surfaces KL/AF Air&Rail bundles from rail-connected origins (e.g. BRU/ANR/ZYR for AMS departures), even outside the default hub list.

### Fixed
- **Rail+Fly arbitrage gate (MIK-3079)** — the flight CLI gated rail+fly on the destination airport instead of the origin hub, so departures from a hub (e.g. AMS) never triggered it. Now keyed on origin; the detector fires for the routes it was built for.

### Changed
- **MCP tool annotations complete (MIK-2984)** — every advertised tool carries a ToolAnnotations block (behavior hints + title); coverage test (mcp/tool_annotations_test.go) enforces it.
- Tool surface 63 → 64 compatibility aliases (added optimize_nested_rt); CLI 50 → 51 commands (added nested).

## [1.2.0] - 2026-05-02

### Added
- **`trvl self-update` CLI command** — user-initiated update of the running trvl binary with full cryptographic verification before any swap. Behavior branches by install method (auto-detected from the binary's path):
  - **Homebrew / `go install` / npm**: print the channel-correct upgrade hint (`brew upgrade trvl` / `go install ...@latest` / `npm install -g trvl-mcp@latest`) and exit. trvl REFUSES to overwrite a binary tracked by an external package manager's manifest.
  - **Standalone tarball**: download the latest GH release archive + checksums.txt + `.mldsa65.sig`, verify SHA-256 against the published checksums, verify ML-DSA-65 (NIST FIPS 204 post-quantum) signature against the trust anchor embedded at compile time (fingerprint `05281eded06cc2ab`), extract the binary, atomically replace the running file. Any verification failure aborts and leaves the on-disk binary untouched.
  - **dev build**: no-op.
  - **unclassified**: refuse unless `--force-standalone` is passed.
  Flags: `--check` (non-destructive lookup), `--version=X` (pin target), `--force-standalone` (override unclassified). The trust-anchor fingerprint is printed before any swap so users can spot-check it matches the published value.
- **Install-method detector** (`internal/selfupdate/install_method.go`) — classifies the running trvl binary as one of `{dev, brew, go, npm, standalone, unclassified}` based on path heuristics (Homebrew Cellar layout, `$GOBIN`/`$GOPATH/bin`, `node_modules/trvl-mcp/bin/`, system temp). Read-only — no subprocesses, no network. `SupportsInPlaceReplace()` returns true only for Standalone; `UpgradeHint()` returns the channel-correct one-liner.
- **`provider_health` MCP tool now reports `install_method` + `upgrade_hint`** — when an update is available, the structured output includes the user's install channel and the exact command they should run to upgrade. AI assistants can now give the *correct* upgrade gesture per user instead of a one-size-fits-all hint.

### Internal
- New verifier helpers: `verifySHA256`, `readExpectedChecksum` (goreleaser + BSD-style `sha256sum` formats), `sha256File`, `verifyMLDSAFile`, `extractBinaryFromTarGz` (rejects `..` traversal + absolute paths + symlinks), `atomicReplace` (Unix `rename(2)` -> Windows `.old` shuffle -> cross-FS copy fallback).
- Subcommand-count gate bumped 56 -> 57.
- Cosign keyless verification is intentionally NOT yet performed in the self-update path — ML-DSA-65 alone provides full cryptographic guarantee against silent tampering. Cosign verification via sigstore-go will land in a follow-up for defense-in-depth.

## [1.1.4] - 2026-05-02

### Fixed
- **Race condition in `selfupdate.CheckInBackground`** — `internal/selfupdate/notify.go` previously spawned a goroutine that read the cache, recomputed `UpdateAvailable`, and called `NotifyAvailable`, but `cmd/trvl/main.go` typically exits within milliseconds of `rootCmd.Execute()` returning. The goroutine was killed before it could write the notice to stderr in the common path, so the v1.1.3 daily-update notifier was effectively dead in the wild. Split the function: the warm-cache read + `NotifyAvailable` now run synchronously (microseconds, completes before main exits), and only the HTTP refresh (which populates the cache for the *next* invocation) runs in a 6s-bounded goroutine. Net effect: notification latency is "next invocation after the first one that hit a cold/stale cache". v1.1.3 cache files already on disk feed straight into v1.1.4's correct sync-read path, so the fix self-heals without user intervention. Added `semverCmp` wrapper around `upgrade.CompareSemver` to keep notify.go's imports tight.

## [1.1.3] - 2026-05-03

### Added
- **Daily update-check notification** — `cmd/trvl/main.go` now spawns a fire-and-forget goroutine on every CLI invocation that hits the GH releases API once per 24h (cached at `~/.trvl/update-check.json`) and prints a single-line stderr notice on the next invocation when a newer version is available: `trvl: v1.1.3 available (you have v1.1.2). Release notes: <url>`. Skipped automatically for dev builds, CI environments (CI / GITHUB_ACTIONS / GITLAB_CI / CircleCI / Buildkite / Drone / Jenkins / etc), and when `TRVL_DISABLE_UPDATE_CHECK` is set. Bounded to 6s so trvl's exit is never noticeably delayed.
- **`provider_health` MCP tool surfaces update availability** — adds `trvl_update_available: {available, latest_version, current_version, release_url, checked_at}` to the structured output and a one-line append to the human-readable text when an update is available. AI assistants can now mention "trvl v1.1.3 available" alongside provider health without making their own network call.

### Changed
- **goreleaser config: migrate `archives.format` → `archives.formats: [tar.gz]`** to silence the v2 deprecation warning. Pre-empts a future hard breakage when goreleaser drops `format` entirely. No user-facing change.

## [1.1.2] - 2026-05-02

### Added
- **Hybrid quantum-safe release signing** — every release archive now ships with TWO independent signatures: a cosign keyless ECDSA-P256 signature (Sigstore + GitHub OIDC, no persisted key, classical adversary defense) and an ML-DSA-65 signature (NIST FIPS 204, post-quantum lattice-based, embedded trust anchor, future-quantum-cryptanalysis defense). NIST SP 800-208 transition pattern: defense in depth so a single-layer compromise doesn't break the chain. New release assets per archive: `<archive>.cosign.bundle` and `<archive>.mldsa65.sig`. The matching verifier ships in `internal/selfupdate/` for use by the upcoming v1.2.0 auto-update client.
- **`cmd/keygen-mldsa`** — one-time-use keypair generator for the ML-DSA-65 release-signing root of trust. Marshal/unmarshal + sign-verify roundtrip canary catches a corrupt pair before the privkey ever reaches durable storage.
- **`cmd/sign-mldsa`** — signs a release artifact's SHA-256 digest with the long-lived ML-DSA-65 release key (privkey from `$TRVL_MLDSA_PRIVKEY` env, never logged, never written to disk). Self-verifies the produced signature before writing it so a corrupt key in the secret aborts the release rather than producing un-verifiable signatures.
- **`internal/selfupdate/verify_mldsa.go`** — verifier with the trust-anchor pubkey embedded via `go:embed` at compile time (fingerprint `05281eded06cc2ab`). Distinguishes `errSignatureMismatch` (forged binary, abort permanently) from I/O errors (transient, retry next start) so the auto-update path can treat them differently.

### Changed
- **`.goreleaser.yaml`** — added `signs:` block with two entries (cosign-keyless + mldsa65). Local snapshot builds (`goreleaser release --snapshot`) skip signs by default; only tag-driven CI releases run them.
- **`.github/workflows/release.yml`** — installs cosign v2.4.1 via `sigstore/cosign-installer@v3`, exposes `TRVL_MLDSA_PRIVKEY_V1` from the GH Secret, requests `id-token: write` so cosign keyless can request its OIDC token.
- **`go.mod`** — `github.com/cloudflare/circl@v1.6.3` promoted from indirect to direct dependency (FIPS 204 ML-DSA-65 implementation).

## [1.1.1] - 2026-05-02

### Fixed
- **Circuit breaker stuck-open regression** — `internal/providers/runtime.go`'s eligibility loop and circuit-breaker state had drifted: a provider that ever crossed the failure threshold could remain skipped indefinitely because cooldown was timed off the last *success* rather than the last *failure*. Added `LastErrorAt time.Time` to `ProviderConfig` (`registry.MarkError` now stamps it on every failure), and the breaker now reopens for a half-open probe once `now − LastErrorAt ≥ circuitBreakerCooldown`. A freshly-tripped breaker (`LastErrorAt` zero or within cooldown) still skips deterministically — locked by `TestSearchProvider_CircuitBreakerSkips` (cooldown still active) plus a new `TestSearchProvider_CircuitBreakerHalfOpenProbe` (probe path after cooldown).
- **`search_flights` alliance auto-injection** — `mcp/tools_flights.go` was hard-coercing `opts.Alliances = []string{hints.PreferredAlliance}` whenever the caller did not pass an explicit alliance, which silently filtered out every non-alliance carrier (e.g. Ryanair, EasyJet, Wizz on intra-EU routes) and produced spurious zero-result responses. The alliance preference is now treated as a soft hint only (the existing `cabin_class` and `max_price` auto-injection paths are kept — they degrade pricing gracefully rather than excluding results).
- **`search_natural` heuristic NLU coverage gap** — `internal/nlsearch/parser.go` only matched explicit IATA-coded "X to Y" phrasing, so plain "Helsinki to Prague May 14" fell through. Added a three-tier extractor (explicit IATA → city-name resolver via `models.AirportNames` + 41 manual aliases → bare-IATA fallback) plus natural-date patterns for "May 14" and "14 May". New tests: `TestHeuristic_FromToCityNames`, `TestHeuristic_NaturalDate`, `TestHeuristic_FullNaturalQuery`.
- **`hotel_prices` graceful no-providers path** — when no booking partners are configured the tool no longer returns an opaque error string; `internal/hotels/prices.go` detects the no-partners case via `isNoProviderPricesError` and surfaces a structured `Notice` field on `HotelPriceResult` instead so the LLM can suggest `configure_provider`.
- **`list_trips` MCP OutputSchema mismatch** — `mcp/tools_trips.go`'s `handleListTrips` was returning the raw trips slice while the declared OutputSchema expected `{trips, count}`; strict MCP clients (Claude 4.7+) would reject the structuredContent. Now returns the documented shape, locked by `TestHandleListTrips_ReturnsSchemaShape`.
- **`TestValidateDate_Valid` time rot** — hard-coded date `2026-05-01` rolled into the past on 2026-05-02 and started failing in `mcp/validation_helpers_test.go`. Switched to `time.Now().AddDate(0, 1, 0)` so the gate stays valid as time progresses.

### Added
- **Per-provider transparency on `search_flights`** — `FlightSearchResult.ProviderStatuses` now reports each upstream's outcome with `Status: "ok"|"error"|"skipped"|"circuit_broken"`, plus an actionable `FixHint` when a provider was skipped (e.g. "options not supported by Kiwi"). Mirrors the per-provider transparency `search_hotels` already shipped in v1.1.0 — callers can now see which flight providers contributed, which were skipped, and which failed without re-running the search at debug verbosity. `provider_statuses` is now declared in both `flightSearchOutputSchema` and `hotelSearchOutputSchema` so MCP clients can introspect the field.
- **Circuit-broken hotel providers visible to caller** — `internal/providers/runtime.go` now appends `Status: "circuit_broken"` entries (with cooldown-recovery time + `trvl provider reset <id>` `FixHint`) to the returned statuses slice instead of silently dropping them. Locked by `TestSearchHotelsCircuitBreaksNeverSuccessfulProvider` (now expects `len(statuses) == 1`).

### Changed
- **Bundled Claude skill — KLM Air&Rail checked-bag-safe throwaway pattern** — added a dedicated section to `.claude/skills/trvl.md` documenting the only throwaway shape that survives with checked luggage: KLM/AF Air&Rail tickets where the AMS↔Belgian/Dutch rail station leg is a Eurostar/NS train. The bag is tagged AMS-only by KLM (rail operators don't accept through-checked baggage), so the passenger MUST collect at AMS Schiphol arrivals before the rail leg — at which point the bag is in hand and the rail leg becomes optional. Section enumerates KLM rail-destination codes (ZAP Antwerp Centraal, QYG Antwerp Berchem, ZYR Brussels-Midi, QYU Brussels Airport rail, ZWS Rotterdam Centraal, QYM The Hague HS), explains why connection pricing to a "beyond-AMS" rail destination often undercuts the direct PRG→AMS hub-spoke price, notes that Skiplagged/Google Flights do NOT index Air&Rail (search must be on klm.com / airfrance.com with the train icon enabled, or via the AFKLM Offers v3 provider when wired locally), and lists Mikko-specific routing pairs (PRG/KRK/WAW/VIE/BUD → ZAP/ZYR/ZWS, plus HEL → ZAP/ZYR/ZWS for AMS-flat positioning when AMS pricing is high). Risk reminders cover bag-tag verification at the kiosk, rail voucher refundability, and cutoff-time discipline if the rail leg is intentionally used.

### Changed
- **Bundled Claude skill — discount strategy library + multi-hub carrier hubs** — added two operator-facing reference sections to `.claude/skills/trvl.md`: (1) a "Hub-carrier round-trip throwaway" section codifying the high-leverage hack pattern (book RT origin↔third-city via the carrier's hub, fly outbound + return-to-hub, skip final hub→origin leg) with a verified worked example (KLM HEL↔PRG via AMS €413 vs two one-ways €493, saves €80); (2) a multi-hub carrier table (KLM/AF: AMS+CDG+ORY · SAS: CPH+ARN+OSL · LH Group: FRA+MUC+VIE+ZRH+BRU+DUS · BA: LHR+LGW+LCY · Iberia: MAD+BCN · LOT: WAW+KRK · Aegean: ATH+SKG · ITA: FCO+MXP · TAP: LIS+OPO · single-hub: HEL/IST/DXB/AUH/DOH) so agents pass *all* hubs of a carrier-group to `layover_at` in one search; (3) a "Discount strategy library — strictly trvl-actionable" section enumerating every booking-shape, date, multi-modal, anomaly, bag-math, loyalty, and hotel strategy with the exact trvl tool/parameter that executes it, plus an explicit "out of trvl's reach" subsection (status matches, bid-for-upgrade, mistake-fare social monitors, gate upgrades, Eurostar Snap login, off-peak award calendars, married-segment probing, VPN POS browsing) so agents stop promising what trvl cannot search/execute. Composition heuristics codify the standard chain: optimize_booking → detect_travel_hacks → hub-throwaway retry → find_trip_window for calendar-aware dates → status-user filter layering → strategy-ledger conclusion.
- **Bundled Claude skill (`.claude/skills/trvl.md`) refreshed for v1.1.0 surface** — the bundled skill drifted: it advertised 61 tools in the headline but its tool table only documented ~25 of them, omitting `find_trip_window`, `search_awards`, `search_hidden_city`, `watch_opportunities`, `list_opportunity_watches`, the full `search_flights` filter surface (alliances, layover_at, lounge_required, no_early_connection, min_layover_minutes, exclude_basic, less_emissions, carry_on_bags, require_checked_bag, currency, max_price, max_duration, depart_after/before, first_result, provider), the v1.1.0 ProfileMatch (0-100) scoring + `match_weights`/`airport_affinity`/`excluded_destinations` preference fields, the FixHintCode taxonomy on provider statuses, and the Skiplagged default-merge wiring. Skill now groups all 61 tools by category (Flights / Hotels / Ground / Destinations / Hacks / Reference / Profile / Trips / Watches / Providers), documents headline params per tool, surfaces the optimizer's 9 expansion strategies, lists the 37 hack detectors, and codifies the risk gates for hidden-city and throwaway routings.

### Fixed
- **Live-HTTP tests properly gated** — `TestFlightsCmd_HomeOriginResolves`, `TestWeekendCmd_ValidIATANoNetworkV21`, and `TestWatchCheckCmd_EmptyStore_V29` were guarded only by `testing.Short()`, so they ran on Ubuntu CI (which does not pass `-short`) and could hang the cmd/trvl package past 120s when fanning out to live providers. Switched all three to `testutil.RequireLiveIntegration(t)` — the project's canonical opt-in via `TRVL_TEST_LIVE_INTEGRATIONS=1` — matching the pattern used in `internal/hacks/coverage_target_test.go`. (#66)
- **`flights` CLI input validation** — `trvl flights` now validates origin and destination IATA codes (3 uppercase letters, comma-separated multi-airport supported) before dispatching to any provider, matching the pattern already used in `when/grid/multicity/discover/weekend/explore/setup`. Eliminates the ubuntu CI flake where `TestFlightsCmd_InvalidOriginIATA` could pass or fail depending on which provider happened to error first; negative-path tests now return deterministic errors with no network round-trip. (#65)

## [1.1.0] - 2026-04-28

> Note on versioning: this release contains a breaking change (`ValueScore` → `ProfileMatch`, see below) but ships as `1.1.0` rather than `2.0.0`. The 1.0 surface stabilised only on 2026-04-20 (eight days before this release), and `value_score` had been a public field for less than a week of real-world use, so we treat 1.0.x as a pre-stabilisation window rather than a strict-semver public API. Subsequent breaking changes will go through proper deprecation cycles or trigger a `2.0.0` bump.

### Added
- **Skiplagged flight provider** — open MCP server at `mcp.skiplagged.com/mcp` (no API key, no cookies) wired as the third default flight provider alongside Google Flights and Kiwi. `trvl flights AMS HEL 2026-04-29` now returns merged results from all three; `--provider skiplagged` selects it as the sole source. Surfaces hidden-city / virtual-interlining options without manual flag flipping. Eligibility helper short-circuits options the upstream MCP cannot honour (airline / alliance filters, baggage requirements, exclude-basic, less-emissions); client-guard pattern keeps test-injected clients from auto-dispatching Skiplagged (mirrors Kiwi guard). (#62, #63, #64)
- **OpenTelemetry tracing** — opt-in MCP tracing via `TRVL_OTEL_ENDPOINT`, with per-tool spans and queue/inflight attributes (MIK-3089)
- `search_awards` MCP tool: cross-program award scanner with transfer-partner sweet-spot ranking (MIK-3081)
- `trvl awards` CLI command: find cheapest redemption path across FB/Avios/Aeroplan/Virgin/Asia-Miles (MIK-3081)
- **Chaos fault injector** — `internal/chaos` package: `Transport` wraps any `http.RoundTripper` and injects synthetic 429 / 503 / timeout faults per host according to a `Plan` map; `NewDeterministic` variant for deterministic unit tests. Canary tests cover all three fault types, pass-through, probability gating, and circuit-breaker threshold simulation (MIK-3089)
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

## [1.0.7] - 2026-04-24

> Documented retroactively on 2026-04-28 — this release shipped before the changelog discipline caught up with the four-day v1.0.4 → v1.0.7 patch sprint.

### Added
- **AFKLM Flying Blue cash provider** — opt-in via `--provider afklm` (CLI) / `provider: "afklm"` (MCP arg). Air France-KLM Offers API v3 for cash fares on KL/AF metal, with rail-leg origin codes corrected (only ZYR resolves to a `RAILWAY_STATION`) and top-level connection mapping that populates `route`, `carrier`, `times`, and `duration`
- **Hunt orchestrator** — shared CLI/MCP orchestrator with two new MCP tools, restoring CLI ↔ MCP feature parity (#48)
- **Search flights by city** — when origin or destination is a city name (rather than an IATA code), the search now expands to every member airport automatically so deals out of EIN/ANR/TKU/TLL are not missed (#42, **@Alorse**)
- **`--first` flag and `first_result` MCP parameter** — return only the first result with a valid price after sorting; cuts payload size for price-calendar and quick-estimate workflows (#43 → #49, **@Alorse**)
- **`Personal` field and `ListPublic()` on the provider registry** — lets the CLI/MCP enumerate only providers safe to expose without personal credentials
- **Tag-triggered release workflow + adhoc codesign identifier** (#50) — first cut of the automated release pipeline that all subsequent versions ride on

### Fixed
- **Windows CI parity** — `-short` is now applied on the Windows job, platform-specific tests are gated, content-block assertions tolerate network variability, and the remaining live-HTTP tests are skipped/guarded so the windows-latest matrix is no longer permanently red (#41, #45, #46)

## [1.0.6] - 2026-04-20

### Added
- **55th MCP tool** — `export_ics` produces an ICS calendar feed for booked trip legs

### Fixed
- **Tool-count consistency** — `plugin.json`, `demo.tape`, and README now all report 55 tools (the count was previously drifting between surfaces)

## [1.0.5] - 2026-04-20

### Added
- **Background price-watch scheduler** — long-lived watcher that re-runs `check_watches` on a configurable cadence and pushes drops over webhooks
- **npm wrapper** — `npm install -g @mikkoparkkola/trvl` distribution path so non-Go users can install without `go install`
- **Webhook price alerts** — watchers can now POST drop events to a configurable URL in addition to writing to `~/.trvl/watches.jsonl`

### Changed
- **Cache safety** — broader response-cache audit; bumped MCP coverage tests to lock in the new behavior

## [1.0.4] - 2026-04-20

### Changed
- **`ParseDate` deduplication** — collapsed 75 ad-hoc date-parsing call sites into a single helper, hoisted regexes to package-level `var`s, and reduced per-call allocations across the flight, hotel, ground, and trip-window paths. No external behavior change; user-visible effect is lower latency and lower allocation pressure on hot paths.

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
