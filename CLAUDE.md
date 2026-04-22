# trvl

Travel MCP server + CLI. 57 MCP tools, 42 CLI commands. Go 1.26, no frameworks.

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
