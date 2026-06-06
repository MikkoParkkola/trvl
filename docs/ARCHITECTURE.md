# Architecture

## Package dependency diagram

```
cmd/trvl                          CLI entry point (cobra)
  |
  +-- internal/flights            Google Flights search, dates, calendar, grid
  |     +-- internal/batchexec    Google batchexecute protocol (TLS, encoding, retry, cache)
  |     |     +-- internal/cache  In-memory response cache
  |     +-- internal/jsonutil     Safe JSON array traversal
  |     +-- internal/models       Shared data types + output formatting
  |
  +-- internal/hotels             Google Hotels search, prices, reviews, detail
  |     +-- internal/batchexec
  |     +-- internal/jsonutil
  |     +-- internal/models
  |
  +-- internal/ground             Bus + train + ferry search (20 providers in parallel)
  |     +-- flixbus.go            FlixBus REST API (global.api.flixbus.com)
  |     +-- regiojet.go           RegioJet REST API (brn-ybus-pubapi.sa.cz)
  |     +-- eurostar.go           Eurostar GraphQL (site-api.eurostar.com)
  |     +-- deutschebahn.go       DB Vendo API (int.bahn.de/web/api)
  |     +-- oebb.go               ÖBB Austrian Railways (Playwright browser scraper)
  |     +-- ns.go                 NS Dutch Railways (public API, embedded key)
  |     +-- digitransit.go        VR Finnish Railways via Digitransit GraphQL
  |     +-- sncf.go               SNCF Connect API (curl BFF fallback + Playwright)
  |     +-- trainline.go          Trainline aggregated rail API (browser cookie auth)
  |     +-- renfe.go              Renfe Spanish Railways (Playwright browser scraper)
  |     +-- transitous.go         Transitous/MOTIS2 (routing.spicebus.org)
  |     +-- tallink.go            Tallink/Silja Line REST API (book.tallink.com) — live prices
  |     +-- vikingline.go         Viking Line reference schedule — Distribusion API pending
  |     +-- eckeroline.go         Eckerö Line Magento AJAX API (getdepartures) — live prices
  |     +-- stenaline.go          Stena Line reference schedule — Distribusion API pending
  |     +-- dfds.go               DFDS availability API (travel-search-prod.dfds-pax-web.com)
  |     +-- taxi.go               Taxi fare estimates for airport transfers
  |     +-- browser_scraper.go    Shared Playwright browser automation
  |     +-- search.go             Parallel dispatch + result merging
  |     +-- internal/models
  |
  +-- internal/route              Multi-modal routing engine
  |     +-- router.go             Pareto-optimal itinerary search across all providers
  |     +-- hubs.go               26 European hub cities for route optimization
  |     +-- internal/ground
  |     +-- internal/flights
  |     +-- internal/models
  |
  +-- internal/explore            Destination discovery (GetExploreDestinations)
  |     +-- internal/batchexec
  |     +-- internal/models
  |
  +-- internal/destinations       Travel intelligence (weather, safety, POIs, guides, events)
  |     +-- internal/batchexec    (for Google Maps nearby/restaurants)
  |     +-- internal/jsonutil
  |     +-- internal/models
  |
  +-- internal/trip               Trip planning (cost, multi-city, weekend, smart dates, plan)
  |     +-- plan.go               Parallel flights+hotel search with cost summary (trvl trip)
  |     +-- internal/flights
  |     +-- internal/hotels
  |     +-- internal/explore
  |     +-- internal/batchexec
  |     +-- internal/models
  |
  +-- internal/deals              RSS feed aggregation (Secret Flying, Fly4Free, etc.)
  |     +-- internal/models
  |
  +-- internal/watch              Price tracking + alerts
  |     +-- internal/models
  |
  +-- internal/models             Shared types: Flight, Hotel, GroundRoute, Airport, formatting
  +-- internal/cache              TTL cache (5m flights, 10m hotels, 1h destinations)
  +-- internal/cookies            Browser cookie loader for CAPTCHA-protected providers (Trainline, Eurostar, SNCF)
  +-- internal/jsonutil           Safe nested JSON array access

mcp/                              MCP server (1 advertised smart tool + 64 aliases, stdio + HTTP)
  +-- internal/flights
  +-- internal/hotels
  +-- internal/ground
  +-- internal/destinations
  +-- internal/trip
  +-- internal/deals
  +-- internal/watch
  +-- internal/models
```

### Dependency rules

1. `internal/models` has zero internal dependencies -- it is the leaf package
2. `internal/batchexec` depends only on `internal/cache` -- it is the HTTP layer
3. Domain packages (`flights`, `hotels`, `ground`, `explore`, `destinations`) depend on `batchexec` and/or `models` but never on each other (except `trip`, which composes `flights`, `hotels`, and `explore`)
4. `cmd/trvl` and `mcp/` are the two top-level entry points; they depend on domain packages but domain packages never depend on them
5. No circular dependencies exist

## Data flow

### Flight search (example)

```
User: "flights HEL NRT 2026-06-15"
          |
          v
    cmd/trvl/flights.go          Parse CLI flags, validate IATA codes
          |
          v
    flights.Search()             Build batchexecute payload (filter arrays)
          |
          v
    batchexec.Client.Do()        Chrome TLS handshake (utls) -> POST to Google
          |                      Rate limit (10 req/s) -> retry on 429/5xx
          |                      Cache check (5min TTL)
          v
    flights.Parse()              Decode anti-XSSI prefix, extract nested JSON arrays
          |
          v
    []models.Flight              Structured results with prices, airlines, routes
          |
          v
    models.FormatFlights()       Pretty table (default) or JSON output
```

### MCP tool call (example)

```
AI assistant                     Sends JSON-RPC tool call via stdin
          |
          v
    mcp.Server.handleCall()      Route to tool handler by name
          |
          v
    mcp/tools_flights.go         Validate params, call flights.Search()
          |
          v
    (same flow as CLI)           batchexec -> parse -> models
          |
          v
    mcp response                 structuredContent (JSON for AI) +
                                 human-readable summary (audience: user) +
                                 suggestions for follow-up searches
```

### Ground transport search

```
User: "ground Prague Vienna 2026-07-01"
          |
          v
    ground.SearchByName()        Resolve city names for each provider
          |
          +---> flixbus.go       City autocomplete -> search (10 req/s limit)
          +---> regiojet.go      Location resolve -> route search (10 req/s limit)
          +---> eurostar.go      Station lookup -> GraphQL query (1 req/20s limit)
          +---> deutschebahn.go  Location search -> journey query (1 req/2s limit)
          +---> oebb.go          Browser session -> Railjet journey (Playwright)
          +---> ns.go            Station lookup -> journey query (embedded key)
          +---> digitransit.go   GraphQL query -> VR fare lookup (public key)
          +---> sncf.go          curl BFF -> offer query (1 req/6s limit)
          +---> trainline.go     Station search -> journey query (browser cookie auth)
          +---> renfe.go         Browser session -> AVE journey (Playwright)
          +---> transitous.go    Geocode -> MOTIS2 routing (1 req/6s limit)
          +---> tallink.go       voyage-avails API (1 req/12s limit)
          +---> vikingline.go    Reference schedule lookup (no network)
          +---> eckeroline.go    Magento AJAX API (form_key + getdepartures)
          +---> stenaline.go     Reference schedule lookup (no network)
          +---> dfds.go          Availability API (1 req/12s limit)
          |     (all 16 run in parallel via goroutines)
          v
    merge + sort + filter        Combine results, apply --max-price / --type filters
          |
          v
    []models.GroundRoute         Unified type across all providers
```

## Adding a new provider

Example: adding Amtrak (US rail) to the ground transport package.

### 1. Create the provider file

Create `internal/ground/amtrak.go`:

```go
package ground

import (
    "context"
    "github.com/MikkoParkkola/trvl/internal/models"
    "golang.org/x/time/rate"
)

var amtrakLimiter = rate.NewLimiter(rate.Every(2*time.Second), 1)

// searchAmtrak searches Amtrak for routes between two stations.
func searchAmtrak(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
    // 1. Resolve city names to Amtrak station codes
    // 2. Query Amtrak's API (rate-limited)
    // 3. Parse response into []models.GroundRoute
    // 4. Return results
}
```

Every provider function must:
- Accept `ctx context.Context` for cancellation
- Use a package-level `rate.Limiter`
- Return `[]models.GroundRoute` (the shared model)
- Handle errors gracefully (return empty slice + log, not crash)

### 2. Wire into the parallel search

In `internal/ground/search.go`, add the new provider to `SearchByName()`:

```go
// Inside SearchByName(), alongside the existing provider goroutines:
if useProvider("amtrak") {
    wg.Add(1)
    go func() {
        defer wg.Done()
        routes, err := searchAmtrak(ctx, from, to, date, opts.Currency)
        results <- providerResult{routes: routes, err: err, name: "amtrak"}
    }()
}
```

Also update the results channel buffer in `search.go` if needed (currently `make(chan providerResult, 10)`).

### 3. Add tests

Create `internal/ground/amtrak_test.go` with:
- Unit tests for response parsing (use recorded JSON fixtures)
- A test for the city name resolver
- Integration with the `ground_test.go` provider filtering tests

### 4. Update documentation

- Add the provider to `README.md` (the "Buses & Trains" section and comparison table)
- Add the provider to the MCP tool description in `mcp/tools_ground.go`
- Update `CONTRIBUTING.md` if the provider uses a new pattern

### 5. What NOT to do

- Do not add a new package for each provider -- all ground providers live in `internal/ground/`
- Do not add new dependencies unless absolutely necessary -- prefer stdlib `net/http` + `encoding/json`
- Do not skip the rate limiter -- every HTTP call must go through a `rate.Limiter`
- Do not add provider-specific models -- use `models.GroundRoute` for all providers

## Key design decisions

### Why Go?

- **Single binary**: `trvl` compiles to a ~15MB static binary. Users download it and it works. No Python environment, no Node.js, no Docker. `curl | tar | run`.
- **No runtime dependencies**: No pip install, no npm install, no virtualenv. The binary is the whole application.
- **Fast compilation**: The full test suite (980+ tests) runs in seconds. CI builds complete in under a minute.
- **Concurrency**: Goroutines make parallel provider search natural. Searching 6 ground transport providers in parallel is a `sync.WaitGroup` and 6 goroutines.
- **MCP fit**: MCP servers are long-running stdio processes. Go's low memory footprint and fast startup make it ideal for a tool that launches per-conversation.

### Why reverse-engineer vs official APIs?

- **Free**: Google has no public Flights/Hotels API. Skyscanner's affiliate API requires business approval. Booking.com's API requires a partner agreement. trvl works out of the box with zero signup.
- **No API keys**: Nothing to manage, rotate, or pay for. No `.env` files, no secrets in CI.
- **No rate limits imposed by the provider**: Official APIs typically limit you to N requests per day. trvl's self-imposed limits are conservative but not artificially low.
- **Same data**: The batchexecute protocol returns the exact same data that google.com/travel shows. No "lite" tier, no missing fields.
- **Precedent**: [fli](https://github.com/punitarani/fli) has done this for Google Flights since 2023 with no legal issues.

The tradeoff is maintenance: when Google changes their protocol (rare but possible), trvl needs updating. This is a conscious choice -- free and keyless access is worth occasional breakage.

### Why parallel provider search?

When you search "Prague to Vienna", trvl queries all relevant ground providers simultaneously:

```
Sequential: FlixBus(2s) + RegioJet(1s) + DB(3s) + ÖBB(4s) + NS(1s) + VR(1s) + SNCF(2s) + Trainline(2s) + Renfe(4s) + Eurostar(1s) + Transitous(1s) + ferries(1s) = 23s
Parallel:   max(all 20 providers)                                                                                                                                   = 4s
```

Parallel search gives you the best price across all providers in the time it takes to query the slowest one. The implementation is straightforward Go concurrency: one goroutine per provider, results collected via a channel, merged and sorted after all complete.

### Why MCP?

MCP (Model Context Protocol) is how AI assistants call external tools. trvl as an MCP server means:

- **AI-native**: Claude, Cursor, Windsurf, and any MCP client can search flights, hotels, and trains natively. The AI decides when to search, what parameters to use, and how to present results.
- **Structured content**: MCP's `structuredContent` returns typed JSON alongside human-readable summaries. The AI gets machine-parseable data; the user gets formatted text. Both from one call.
- **Progressive disclosure**: Every response includes suggestions for follow-up searches ("Try nearby airports", "Check flexible dates"). The AI can chain these automatically.
- **No integration work for local mode**: Adding trvl to any MCP client is one config line. Local stdio needs no REST API, webhook, or OAuth setup. Remote HTTP mode is explicit and can use scoped bearer tokens or OAuth 2.1 introspection when a gateway/provider handles Authorization Code + PKCE.

trvl also works as a standalone CLI (22 commands) for users who prefer the terminal or want to script searches.

### Why a monorepo with internal packages?

Go's `internal/` convention enforces that packages under `internal/` cannot be imported by external code. This means:

- **API stability**: Only `cmd/trvl` and `mcp/` are public entry points. Internal packages can change freely without breaking external users.
- **Shared types**: `internal/models` defines `Flight`, `Hotel`, `GroundRoute`, etc. Both CLI and MCP use the same types, ensuring consistency.
- **Shared HTTP layer**: `internal/batchexec` handles TLS fingerprinting, rate limiting, caching, and retry. All Google-facing packages share this single client.
- **No circular dependencies**: The dependency graph is a clean DAG from entry points down to `models` at the leaf.

## External dependencies

trvl has 5 direct dependencies (and 5 transitive):

| Dependency | Purpose |
|-----------|---------|
| `github.com/refraction-networking/utls` | Chrome TLS fingerprint impersonation |
| `github.com/spf13/cobra` | CLI command framework |
| `golang.org/x/time` | Token-bucket rate limiter (`rate.Limiter`) |
| `golang.org/x/net` | HTTP/2 and proxy support |
| `golang.org/x/term` | Terminal width detection for table formatting |

Everything else is Go stdlib: `net/http`, `encoding/json`, `sync`, `context`, `time`, `sort`, `strings`, `fmt`.
