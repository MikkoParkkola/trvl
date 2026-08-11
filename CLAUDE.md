# trvl

Travel MCP server + CLI. 1 smart MCP tool plus 66 legacy-compatible capabilities, 56 CLI commands. Go 1.26.5, no frameworks.

## Product Vision

trvl is a travel MCP server + CLI that gives any AI assistant (Claude, Cursor, Windsurf, Codex, …) direct access to flights, hotels, trains, buses, ferries, price alerts, travel hacks, weather, baggage rules, airport lounges, and destination intelligence — **without requiring personal API keys**. Single Go binary, MCP 2025-11-25 compliant, 1 smart MCP tool plus 66 legacy-compatible capabilities, 56 CLI commands, API-first with optional browser-assisted fallbacks for a handful of protected providers. The smart `travel` router advertises a single tool (~378 tokens of `tools/list` context) instead of all 66 (~33,500 tokens) — a ~98.9% context reduction; the 66 legacy-compatible capabilities stay callable via the `intent` field, and `TRVL_MCP_TOOL_MODE=legacy` advertises the full surface for clients that need it.

## Current Status

- Go 1.26.5 · MCP 2025-11-25 · single binary · 24 transport providers (+ flight & hotel sources below)
- Hotel providers working: Google Hotels, Booking.com (browser cookies), Airbnb (SSR/Niobe), Hostelworld (autocomplete), Trivago (Streamable HTTP MCP), HomeToGo (public SSR+JSON, vacation rentals)
- Flight providers: the default path merges Google Flights, Kiwi, and Skiplagged. Solo or opt-in integrations cover Ryanair, Wizz Air, Air France–KLM, Transavia, easyJet, Vueling, and Norwegian; protected or credentialed paths return typed setup/block statuses when unavailable. Travelpayouts/Aviasales price signals are opt-in through `trvl pricetrends` and are not part of the bookable merge.
- Enrichment (free, unauthenticated): weather (Open-Meteo), air quality (`trvl air`), sun times (`trvl sun`, sunrise-sunset.org), bike-share (`trvl bikes`, CityBikes)
- CI: build, vet, and race tests on Ubuntu and Windows; staticcheck, golangci-lint, govulncheck, and the >=80% coverage gate run on Ubuntu
- Current release: v1.21.3, published 2026-08-11 from `main` to GitHub Releases, Homebrew, npm, GHCR, the Go module proxy, and the official MCP Registry.
- v1.21.3 carries rental-provider destination-integrity guards, fail-closed Flatio fallback results, and noninteractive background Nab cookie access.

## Plan Forward (near-term, technical)

- No GitHub issues are open as of 2026-08-09. Do not invent release scope from this file; use the live issue tracker.
- Keep provider-version self-healing and typed failure states healthy as upstream endpoints change.
- Keep public counts, release metadata, the README, and the wiki aligned whenever a provider, command, or MCP capability changes.
- New work needs a scoped issue with acceptance criteria before implementation.

## Decisions Locked (do not re-litigate)

| Decision | Rationale | Do not |
|---|---|---|
| **No frameworks** — stdlib + carefully chosen libs only | Predictable behavior, minimal deps, long-lived binary | Add web frameworks, ORMs, DI containers |
| **No API keys required by default** | Zero-friction onboarding; "API-first" phrasing uses *provider* APIs, not user-paid ones | Introduce paid-API requirements on default code paths |
| **`GOTOOLCHAIN=go1.26.5` pinning via Makefile** | CI reproducibility; host `go` on PATH may be older | Run raw `go build/test` without the prefix on older hosts |
| **`internal/models` is the shared type package** | Unidirectional import flow; no cycles | Import from other `internal/` packages into `models/` |
| **Live tests are opt-in** via `TRVL_TEST_LIVE_INTEGRATIONS=1` and `TRVL_TEST_LIVE_PROBES=1` | Default suite must be deterministic and offline | Enable live probes in the default `go test ./...` suite |
| **Protobuf-style encoding for Google Flights is hand-rolled** (no `.proto` files) | The upstream format is undocumented; hand-rolled is auditable | Add `protoc` / `.proto`-generation to the build pipeline |
| **License: PolyForm Noncommercial 1.0.0** | Commercial users contact for license | Relicense without explicit user direction |
| **MCP 2025-11-25 spec target** | Aligned with current Claude/Cursor/Windsurf support | Ship backwards-incompatible MCP changes without version bump |

## Anti-Patterns (things agents get wrong in this repo)

- **Shipping framework dependencies** to "simplify" something — reject on sight
- **Making Google Flights/Hotels default paths depend on user-owned API keys** — breaks the no-key promise
- **Importing `internal/flights` / `internal/hotels` into `internal/models`** — inverts the dependency direction
- **Forgetting `-race` on tests that touch cached/shared state** — MCP handler race conditions have bitten before (#39, #40)
- **Adding Windows-incompatible assertions to the default suite** — use `//go:build !windows` or skip-on-windows pattern (#45, #46)
- **Counting MCP tools in multiple files without updating all of them** — count lives in README, plugin.json, demo.tape (#41 precedent)

## Guidance for Agents

- **Tests must stay deterministic in default suite**: use fixtures for provider responses; put live-API tests behind env-guarded opt-ins
- **Before adding a new provider**: mirror the `providers/` pattern (generic HTTP→JSON→HotelResult/FlightResult/GroundResult); don't hard-code routes in `mcp/` handlers
- **MCP tool handlers delegate** to `internal/` packages; thin handlers, business logic in domain packages
- **When changing tool surface**: update README tool claims, `plugin.json`, `demo.tape`, `AGENTS.md`, bundled skills, and public claim tests in one PR

## Where to Look

| You want to… | Read |
|---|---|
| Onboard a human user | `README.md` |
| Onboard a fresh AI assistant to USE trvl | `AGENTS.md` (intentionally diverged from this file — different audience) |
| Understand hotel provider internals | `internal/hotels/` + `docs/` |
| Understand flight provider internals | `internal/flights/` + protobuf notes in `docs/` |
| Add a new MCP tool | `mcp/tools*.go` + register in `mcp/server.go` |
| Run fastest test loop | `go test -short ./...` |
| Check CI parity | `make lint && make test` (matches GitHub Actions) |

## Hotel Providers (6 working)

- **Google Hotels** — direct scraping, no auth
- **Booking.com** — direct GraphQL (dml/graphql); requires browser cookies (auto-detected from any installed browser via kooky)
- **Airbnb** — SSR via Niobe cache unwrapper + deferred-state-0; dynamic city resolver
- **Hostelworld** — dynamic city resolver via autocomplete API; rich descriptions + district names
- **Trivago** — Streamable HTTP MCP protocol
- **HomeToGo** — public SSR + JSON vacation-rental inventory

## Architecture

```
cmd/trvl/          CLI commands (cobra-style, one file per command)
  main.go          Entrypoint
  mcp.go           MCP stdio server launcher
  flights.go       Flight search command
  hotels.go        Hotel search command
  ...
internal/          Domain packages (one per data source)
  flights/         Google Flights scraping + protobuf encoding
  hotels/          Google Hotels scraping
  ground/          Buses, trains, ferries (22 providers)
  destinations/    City intelligence (weather, safety, holidays)
  deals/           RSS deal feeds
  hacks/           Travel hack detectors (36 parallel)
  lounges/         Airport lounge data
  baggage/         Airline baggage rules
  weather/         Open-Meteo forecasts
  models/          Shared types (FlightResult, HotelResult, etc.)
  preferences/     User prefs (~/.trvl/preferences.json)
  providers/       Reviewed provider runtime and embedded definitions (generic HTTP→JSON→HotelResult)
  cache/           HTTP response caching
  ...
mcp/               MCP server (tools, resources, prompts)
  server.go        Server setup + tool registration
  tools*.go        Tool handlers (one file per domain)
capabilities/      MCP capability YAML definitions
.claude/skills/    Bundled Claude skill
```

## Commands

```bash
make build                          # Build binary to bin/trvl
make test                           # go test ./... (deterministic default suite)
make test-proof                     # go test -v -count=1 -race ./...
make test-coverage                  # go test -race -coverprofile coverage.out ./...
make test-live-integrations         # TRVL_TEST_LIVE_INTEGRATIONS=1 go test ./...
make test-live-probes               # TRVL_TEST_LIVE_PROBES=1 ... -run Probe
make lint                           # go vet + staticcheck
go test -short ./...                # Fastest suite
go test ./internal/flights/...      # Single package
staticcheck ./...                   # Lint (CI runs this)
go vet ./...                        # Vet (CI runs this)
```

## CI

GitHub Actions (`.github/workflows/ci.yaml`): build, vet, staticcheck, govulncheck, test with race detector, coverage threshold (80%). Runs on ubuntu + windows, Go 1.26.5.

Make targets pin `GOTOOLCHAIN=go1.26.5` so local build/test entrypoints match CI even when the host `go` on `PATH` is older. For raw `go ...` commands on such hosts, prefix `GOTOOLCHAIN=go1.26.5`.

## Key Details

- **No personal API keys required** for default flight, hotel, and ground search; optional integrations document their own credentials
- **Optional API keys**: Ticketmaster, Foursquare, Geoapify, OpenTripMap (env vars)
- **User prefs**: `~/.trvl/preferences.json` (home airports, budgets, loyalty status)
- **License**: PolyForm Noncommercial 1.0.0
- **Module**: `github.com/MikkoParkkola/trvl`

## Dev Notes

- Protobuf-style encoding for Google Flights requests (no .proto files, hand-rolled)
- Flight filters use nested protobuf arrays with precise slot indexing
- Live provider/MCP integration tests are opt in via `TRVL_TEST_LIVE_INTEGRATIONS=1`
- Test files ending in `_probe_test.go` hit live Google endpoints (opt in with `TRVL_TEST_LIVE_PROBES=1`; `-short` also skips them)
- `internal/models/` is the shared type package -- all packages import from here
- MCP tool handlers in `mcp/tools*.go` delegate to `internal/` packages

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **trvl** (44749 symbols, 129586 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/trvl/context` | Codebase overview, check index freshness |
| `gitnexus://repo/trvl/clusters` | All functional areas |
| `gitnexus://repo/trvl/processes` | All execution flows |
| `gitnexus://repo/trvl/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
