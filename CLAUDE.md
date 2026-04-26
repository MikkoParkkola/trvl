# trvl

Travel MCP server + CLI. 61 MCP tools, 50 CLI commands. Go 1.26, no frameworks.

## Product Vision

trvl is a travel MCP server + CLI that gives any AI assistant (Claude, Cursor, Windsurf, Codex, …) direct access to flights, hotels, trains, buses, ferries, price alerts, travel hacks, weather, baggage rules, airport lounges, and destination intelligence — **without requiring personal API keys**. Single Go binary, MCP 2025-11-25 compliant, 61 MCP tools, 50 CLI commands, API-first with optional browser-assisted fallbacks for a handful of protected providers.

## Current Status

- Go 1.26.2 · MCP 2025-11-25 · single binary · 21 providers
- Hotel providers working: Google Hotels, Booking.com (browser cookies), Airbnb (SSR/Niobe), Hostelworld (autocomplete), Trivago (Streamable HTTP MCP)
- Flight providers: Google Flights (hand-rolled protobuf), Air France–KLM Offers API v3 (opt-in)
- CI: build, vet, staticcheck, govulncheck, race tests, coverage ≥50% on ubuntu + windows
- Latest release train: tag-triggered workflow + adhoc codesign identifier (PR #50)
- npm wrapper, ICS calendar export, 57→61 tool bump landed in last ~90d (MIK-3081/3082/3083/3084 + 6-package wiring batch in PR #57)

## Plan Forward (near-term, technical)

- **Windows CI hardening** — ongoing: `-short` gating, skip/gate platform-specific asserts, content-block assertion resilience under network variability
- **Provider breadth** — AFKLM opt-in done; similar opt-in pattern for other carriers via `--provider` flag
- **Hunt orchestrator** — shared CLI/MCP orchestrator landed (PR #48); continue parity expansion
- **Directory submissions** — open GH issue #19: mcp.so, Glama
- **First-contributor momentum** — external PRs now landing (#43/#49); keep onboarding friction low

## Decisions Locked (do not re-litigate)

| Decision | Rationale | Do not |
|---|---|---|
| **No frameworks** — stdlib + carefully chosen libs only | Predictable behavior, minimal deps, long-lived binary | Add web frameworks, ORMs, DI containers |
| **No API keys required by default** | Zero-friction onboarding; "API-first" phrasing uses *provider* APIs, not user-paid ones | Introduce paid-API requirements on default code paths |
| **`GOTOOLCHAIN=go1.26.2` pinning via Makefile** | CI reproducibility; host `go` on PATH may be older | Run raw `go build/test` without the prefix on older hosts |
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
- **When changing tool surface**: update README tool count, `plugin.json`, `demo.tape`, `AGENTS.md` in one PR

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

## Hotel Providers (5 working)

- **Google Hotels** — direct scraping, no auth
- **Booking.com** — direct GraphQL (dml/graphql); requires browser cookies (auto-detected from any installed browser via kooky)
- **Airbnb** — SSR via Niobe cache unwrapper + deferred-state-0; dynamic city resolver
- **Hostelworld** — dynamic city resolver via autocomplete API; rich descriptions + district names
- **Trivago** — Streamable HTTP MCP protocol

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
  ground/          Buses, trains, ferries (20 providers)
  destinations/    City intelligence (weather, safety, holidays)
  deals/           RSS deal feeds
  hacks/           Travel hack detectors (37 parallel)
  lounges/         Airport lounge data
  baggage/         Airline baggage rules
  weather/         Open-Meteo forecasts
  models/          Shared types (FlightResult, HotelResult, etc.)
  preferences/     User prefs (~/.trvl/preferences.json)
  providers/       External provider runtime (generic HTTP→JSON→HotelResult)
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

GitHub Actions (`.github/workflows/ci.yaml`): build, vet, staticcheck, govulncheck, test with race detector, coverage threshold (50%). Runs on ubuntu + windows, Go 1.26.2.

Make targets pin `GOTOOLCHAIN=go1.26.2` so local build/test entrypoints match CI even when the host `go` on `PATH` is older. For raw `go ...` commands on such hosts, prefix `GOTOOLCHAIN=go1.26.2`.

## Key Details

- **No API keys required** for core functionality (Google Flights/Hotels scraped directly)
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
