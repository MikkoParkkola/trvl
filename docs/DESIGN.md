# trvl — Design Document

> **Historical document.** This is the *original* design from 2026-04-02, kept for context. It does not track the current implementation. For the current architecture see [`DESIGN.md`](../DESIGN.md) (System Design) and [`docs/ARCHITECTURE.md`](ARCHITECTURE.md).

**Status**: IMPLEMENTED (initial design — project has evolved significantly since)
**Author**: Mikko Parkkola
**Date**: 2026-04-02
**Version**: 1.0

---

## 0. Attribution

This project is directly inspired by [**fli**](https://github.com/punitarani/fli) by Punit Arani — a Python library that provides programmatic access to Google Flights data through reverse engineering of Google's internal API. The batchexecute protocol understanding, flight search payload structure, and architectural patterns are derived from fli's pioneering work. `trvl` extends this approach to Google Hotels and reimplements both in Go for single-binary distribution.

## 1. Problem Statement

No open-source tool provides free, programmatic access to Google Hotels data via direct API interaction. The `fli` library solved this for Google Flights by reverse-engineering the internal batchexecute protocol, but nothing equivalent exists for hotels. Furthermore, having separate tools for flights and hotels creates fragmentation — the underlying protocol is identical.

**User pain**: Travel planning requires checking both flights and hotels. Current options are either paid APIs (SerpAPI at $50+/mo, Amadeus, Duffel) or fragile Selenium scrapers. Google's internal API is free, fast, and reliable — it just needs a clean programmatic interface.

## 2. Proposed Solution

A single Go binary (`trvl`) that provides:
1. **Flight search** — search flights on specific dates, find cheapest dates across a range
2. **Hotel search** — search hotels by location, check-in/out dates, guests
3. **Hotel price lookup** — get pricing from multiple booking providers for a specific hotel
4. **MCP server** — stdio and HTTP modes for AI agent integration
5. **JSON output** — structured output for pipeline and gateway integration

All operations use Google's `TravelFrontendUi/data/batchexecute` endpoint with Chrome TLS fingerprint impersonation via Go's `utls` library.

## 3. Goals and Non-Goals

### Goals
- G1: Search flights between two airports on a specific date (one-way and round-trip)
- G2: Find cheapest travel dates across a flexible date range
- G3: Search hotels by location with check-in/check-out dates
- G4: Get hotel pricing from multiple providers for a specific property
- G5: CLI with human-readable tables (default) and JSON output (`--format json`)
- G6: MCP server (stdio + HTTP) for Claude Code and AI agent integration
- G7: Zero API keys required — all data from Google's public-facing internal API
- G8: Single statically-linked binary — `go install` or download from releases
- G9: Gateway capability integration (YAML files for mcp-gateway)

### Non-Goals
- NG1: Booking/purchasing — read-only search only
- NG2: Google Flights "Explore" map view
- NG3: Hotel reviews (defer to v2 — rpcid `ocp93e` is known)
- NG4: Multi-city flight search (fragile in Google's API, as fli documents)
- NG5: Price tracking/alerting (future scope)
- NG6: GUI or web interface

## 4. Architecture

### 4.1 High-Level Architecture

```
┌─────────────────────────────────────────────────┐
│                    CLI (cobra)                    │
│  trvl flights | trvl dates | trvl hotels         │
├─────────────┬───────────────┬───────────────────┤
│  flights/   │   hotels/     │   mcp/            │
│  search     │   search      │   stdio + http    │
│  dates      │   prices      │   server          │
├─────────────┴───────────────┴───────────────────┤
│              batchexec/ (shared)                  │
│  Chrome TLS (utls) | Rate limiter | Retry        │
│  Request encoder | Response parser               │
├─────────────────────────────────────────────────┤
│              models/ (shared)                     │
│  Airport | Hotel | Price | DateRange             │
└─────────────────────────────────────────────────┘
```

### 4.2 Package Structure

```
trvl/
├── cmd/trvl/main.go           # Entrypoint
├── internal/
│   ├── batchexec/
│   │   ├── client.go          # HTTP client with utls Chrome impersonation
│   │   ├── encode.go          # f.req payload encoder
│   │   ├── decode.go          # Response parser (strip )]}, parse nested JSON)
│   │   ├── ratelimit.go       # Token bucket rate limiter (10 req/s)
│   │   └── client_test.go
│   ├── flights/
│   │   ├── search.go          # Flight search (rpcid: GetShoppingResults)
│   │   ├── dates.go           # Date search
│   │   ├── encode.go          # Flight-specific payload encoding
│   │   ├── parse.go           # Flight result parsing
│   │   └── search_test.go
│   ├── hotels/
│   │   ├── search.go          # Hotel search (rpcid: AtySUc)
│   │   ├── prices.go          # Hotel price lookup (rpcid: yY52ce)
│   │   ├── encode.go          # Hotel-specific payload encoding
│   │   ├── parse.go           # Hotel result parsing
│   │   └── search_test.go
│   └── models/
│       ├── airport.go         # IATA airport codes + names
│       ├── hotel.go           # Hotel result model
│       ├── flight.go          # Flight result model
│       ├── common.go          # Shared types (Price, DateRange, Location)
│       └── output.go          # JSON/table output formatting
├── mcp/
│   ├── server.go              # MCP server implementation
│   ├── tools.go               # Tool definitions (4 tools)
│   └── server_test.go
├── docs/
│   ├── DESIGN.md              # This document
│   └── PLAN.md                # Work plan
├── go.mod
├── go.sum
├── Makefile
├── LICENSE                    # MIT
└── .goreleaser.yaml           # Cross-platform release builds
```

### 4.3 Shared batchexecute Protocol

Both flights and hotels use the same underlying protocol:

**Request:**
```
POST https://www.google.com/_/TravelFrontendUi/data/batchexecute
Content-Type: application/x-www-form-urlencoded;charset=UTF-8

f.req=<URL-encoded JSON array>
```

The `f.req` payload structure:
```json
[[[
  "<rpcid>",
  "<JSON-stringified arguments>",
  null,
  "generic"
]]]
```

**Response:**
```
)]}'

<length>
[["wrb.fr","<rpcid>","<JSON-stringified result>", ...]]
```

**Parsing steps:**
1. Strip `)]}'` anti-XSSI prefix
2. Split by newlines, find lines starting with `[[`
3. Parse as JSON array
4. Extract index `[0][2]` as the result payload string
5. Parse that string as JSON — deeply nested arrays with positional semantics

### 4.4 TLS Fingerprint Impersonation

Google detects non-browser clients via TLS ClientHello fingerprint (JA3/JA4). We use `utls` to present Chrome's exact TLS fingerprint:

```go
import tls "github.com/refraction-networking/utls"

tlsConfig := &tls.Config{ServerName: "www.google.com"}
conn := tls.UClient(rawConn, tlsConfig, tls.HelloChrome_Auto)
```

`HelloChrome_Auto` tracks the latest stable Chrome release automatically.

### 4.5 Known rpcids

| rpcid | Service | Purpose |
|-------|---------|---------|
| (via FlightsFrontendService) | Flights | Flight search results |
| (via FlightsFrontendService) | Flights | Date price grid |
| `AtySUc` | Hotels | Hotel search results |
| `yY52ce` | Hotels | Hotel price lookup by property ID |
| `ocp93e` | Hotels | Hotel reviews (v2 scope) |

**Note**: Flights uses a named service path (`FlightsFrontendService/GetShoppingResults`) while Hotels uses obfuscated rpcids. Both go through the same `TravelFrontendUi` app.

### 4.6 Rate Limiting

Token bucket: 10 requests/second (matching fli's rate limit). Configurable via `--rate-limit` flag. Automatic retry with exponential backoff (3 attempts, 1s/2s/4s).

### 4.7 MCP Server

Two transport modes:
- **stdio**: `trvl mcp` — for Claude Code integration
- **HTTP**: `trvl mcp --http --port 8000` — loopback by default and refused unless explicit auth is configured; pass `--host` explicitly for gateway or remote use. HTTP supports static read/write bearer tokens or OAuth 2.1 access-token introspection with `trvl:read` / `trvl:write` scopes.

One compact `travel` tool is advertised by default, with the legacy-compatible capabilities still callable directly. `TRVL_MCP_TOOL_MODE=legacy` restores the full advertised list for clients that need explicit tools.

## 5. Tech Selection Rationale

### 5.1 Language: Go

| Criterion | Go | Python | Rust |
|-----------|-----|--------|------|
| TLS impersonation | `utls` — established Chrome-style TLS impersonation library | `curl_cffi` — good | `rquest` — newer, less proven |
| Binary distribution | Single static binary | Needs Python runtime | Single binary |
| Startup time | ~5ms | ~200ms | ~5ms |
| HTTP client stdlib | Strong stdlib coverage | Needs deps | reqwest (good) |
| JSON handling | encoding/json (good enough) | Strong ecosystem support | serde (strong) |
| MCP ecosystem | go-mcp exists | FastMCP (mature) | rmcp (newer) |
| Build time | ~5s | N/A | ~60s+ |
| Cross-compile | `GOOS=linux GOARCH=amd64` | N/A | Cross-compile possible |

**Decision**: Go. The `utls` library supports Chrome-style TLS impersonation for Google-facing traffic. It is used in censorship circumvention tools such as Tor and V2Ray. Single binary, fast builds, and a strong HTTP standard library make the implementation surface smaller.

### 5.2 CLI Framework: Cobra

Widely used for Go CLIs. Used by kubectl, gh, docker. Supports subcommands, flags, shell completion, help generation.

### 5.3 MCP: Custom Implementation

The MCP JSON-RPC protocol is simple enough (initialize, tools/list, tools/call) that a custom implementation is cleaner than pulling in a dependency. ~200 lines for stdio, ~100 more for HTTP.

## 6. Data Models

### 6.1 Flight Search Result

```go
type FlightResult struct {
    Price    float64      `json:"price"`
    Currency string       `json:"currency"`
    Duration int          `json:"duration"`       // minutes
    Stops    int          `json:"stops"`
    Legs     []FlightLeg  `json:"legs"`
    TripType string       `json:"trip_type"`
}

type FlightLeg struct {
    DepartureAirport AirportInfo `json:"departure_airport"`
    ArrivalAirport   AirportInfo `json:"arrival_airport"`
    DepartureTime    string      `json:"departure_time"`  // ISO 8601
    ArrivalTime      string      `json:"arrival_time"`
    Duration         int         `json:"duration"`         // minutes
    Airline          string      `json:"airline"`
    AirlineCode      string      `json:"airline_code"`
    FlightNumber     string      `json:"flight_number"`
}
```

### 6.2 Hotel Search Result

```go
type HotelResult struct {
    Name        string   `json:"name"`
    HotelID     string   `json:"hotel_id"`      // Google's internal ID
    Rating      float64  `json:"rating"`         // 1.0 - 5.0
    ReviewCount int      `json:"review_count"`
    Stars       int      `json:"stars"`          // Hotel class (1-5)
    Price       float64  `json:"price"`          // Per night
    Currency    string   `json:"currency"`
    Address     string   `json:"address"`
    Latitude    float64  `json:"latitude"`
    Longitude   float64  `json:"longitude"`
    Amenities   []string `json:"amenities"`
    Images      []string `json:"images,omitempty"`
}

type HotelPriceResult struct {
    HotelID   string          `json:"hotel_id"`
    Name      string          `json:"name"`
    CheckIn   string          `json:"check_in"`
    CheckOut  string          `json:"check_out"`
    Providers []ProviderPrice `json:"providers"`
}

type ProviderPrice struct {
    Provider string  `json:"provider"`  // "Booking.com", "Hotels.com", etc.
    Price    float64 `json:"price"`
    Currency string  `json:"currency"`
    URL      string  `json:"url,omitempty"`
}
```

### 6.3 Date Search Result

```go
type DateResult struct {
    Date       string  `json:"date"`
    Price      float64 `json:"price"`
    Currency   string  `json:"currency"`
    ReturnDate string  `json:"return_date,omitempty"`
}
```

## 7. CLI Interface

### 7.1 Flight Search

```bash
# Basic one-way search
trvl flights HEL NRT 2026-05-15

# Round-trip
trvl flights HEL NRT 2026-05-15 --return 2026-05-22

# With filters
trvl flights HEL NRT 2026-05-15 \
  --class business \
  --stops nonstop \
  --sort cheapest \
  --format json
```

### 7.2 Date Search

```bash
# Find cheapest dates
trvl dates HEL NRT --from 2026-05-01 --to 2026-06-30

# Round-trip with duration
trvl dates HEL NRT --from 2026-05-01 --to 2026-06-30 --duration 7 --roundtrip
```

### 7.3 Hotel Search

```bash
# Search hotels in a city
trvl hotels "Helsinki" --checkin 2026-05-15 --checkout 2026-05-18

# With filters
trvl hotels "Helsinki" --checkin 2026-05-15 --checkout 2026-05-18 \
  --guests 2 \
  --stars 4 \
  --sort cheapest \
  --format json
```

### 7.4 Hotel Prices

```bash
# Get prices for a specific hotel (ID from search results)
trvl prices <hotel_id> --checkin 2026-05-15 --checkout 2026-05-18
```

### 7.5 MCP Server

```bash
# stdio mode (for Claude Code)
trvl mcp

# HTTP mode (for gateway)
TRVL_MCP_TOKEN="$(openssl rand -base64 32)" trvl mcp --http --host 127.0.0.1 --port 8000

# Scoped HTTP mode
TRVL_MCP_READ_TOKEN="$(openssl rand -base64 32)" \
TRVL_MCP_WRITE_TOKEN="$(openssl rand -base64 32)" \
trvl mcp --http --host 0.0.0.0 --port 8000

# OAuth 2.1 resource-server mode. The OAuth provider/gateway performs
# Authorization Code + PKCE; trvl validates access tokens by introspection.
trvl mcp --http --host 127.0.0.1 --port 8000 \
  --oauth-introspection-url https://auth.example.com/oauth2/introspect \
  --oauth-client-id trvl-resource-server \
  --oauth-client-secret "$TRVL_OAUTH_INTROSPECTION_SECRET" \
  --oauth-audience trvl-mcp
```

## 8. Acceptance Criteria

### AC-1: batchexecute Client
- **AC-1.1**: Client makes POST requests with Chrome TLS fingerprint (verified via JA3 hash comparison)
- **AC-1.2**: Rate limiter enforces 10 req/s with token bucket
- **AC-1.3**: Retry with exponential backoff on 429/5xx (3 attempts)
- **AC-1.4**: Response parser strips `)]}'` prefix and extracts nested JSON correctly
- **AC-1.5**: Graceful error on network failure, timeout, and malformed response

### AC-2: Flight Search
- **AC-2.1**: One-way search returns flight results with price, duration, stops, legs
- **AC-2.2**: Round-trip search returns outbound + return legs with combined pricing
- **AC-2.3**: Cabin class filter works (economy, premium_economy, business, first)
- **AC-2.4**: Stop filter works (any, nonstop, one_stop, two_plus)
- **AC-2.5**: Sort works (cheapest, duration, departure_time, arrival_time)
- **AC-2.6**: Results include airline name, code, flight number, departure/arrival times

### AC-3: Date Search
- **AC-3.1**: Returns date-price pairs for a given route and date range
- **AC-3.2**: Round-trip mode returns paired departure+return dates
- **AC-3.3**: Results sorted by price when `--sort cheapest` is used

### AC-4: Hotel Search
- **AC-4.1**: Search returns hotels with name, rating, stars, price, address, coordinates
- **AC-4.2**: Results include Google hotel_id for subsequent price lookup
- **AC-4.3**: Guest count parameter works
- **AC-4.4**: Star rating filter works
- **AC-4.5**: Sort by price works

### AC-5: Hotel Prices
- **AC-5.1**: Returns prices from multiple booking providers for a specific hotel
- **AC-5.2**: Each provider entry includes provider name, price, currency
- **AC-5.3**: Check-in/check-out dates are correctly encoded

### AC-6: CLI
- **AC-6.1**: Default output is human-readable formatted table
- **AC-6.2**: `--format json` outputs valid JSON to stdout
- **AC-6.3**: Errors go to stderr, data to stdout (pipeable)
- **AC-6.4**: `--help` shows usage for all commands and subcommands
- **AC-6.5**: Exit code 0 on success, 1 on error

### AC-7: MCP Server
- **AC-7.1**: stdio transport handles initialize, tools/list, tools/call
- **AC-7.2**: HTTP transport serves on configurable port
- **AC-7.3**: Four tools registered: search_flights, search_dates, search_hotels, hotel_prices
- **AC-7.4**: Tool input schemas match CLI parameters
- **AC-7.5**: Tool results are valid JSON matching the CLI JSON output format

### AC-8: Quality
- **AC-8.1**: `go vet ./...` passes with zero warnings
- **AC-8.2**: `go test ./...` passes with >= 80% coverage on batchexec/ and models/
- **AC-8.3**: `golangci-lint run` passes (standard config)
- **AC-8.4**: Cross-compiles for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- **AC-8.5**: Binary size < 15MB (static, no CGO)

## 9. Risk Analysis

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| R1: Google changes batchexecute protocol | Low | High | Protocol has been stable 5+ years. Pin Chrome version in utls. |
| R2: Google blocks by TLS fingerprint | Low | High | utls tracks Chrome releases. HelloChrome_Auto auto-updates. |
| R3: Google rate-limits aggressively | Medium | Medium | Conservative 10 req/s limit. Exponential backoff. |
| R4: Hotel rpcid payload format undocumented | High | Medium | Known TypeScript code exists. Network tab RE needed. Fail-fast: if search returns empty in week 1, pivot to alternative approach. |
| R5: Response format changes without notice | Medium | Medium | Parser tests with golden files. CI canary test against live API weekly. |
| R6: Legal risk (ToS violation) | Low | Medium | Read-only, no auth, public endpoint. Same as fli which has 1K+ GitHub stars. MIT license. |

## 10. Fail-Fast Criteria

**Week 1 kill criteria** (if any are true, reassess approach):
- FF-1: utls Chrome impersonation gets blocked by Google (test with simple search)
- FF-2: Hotel search rpcid `AtySUc` returns empty/error for known locations
- FF-3: Response format is completely different from documented examples
- FF-4: Rate limiting kicks in below 1 req/s making the tool impractical

**Validation order**: FF-1 → FF-2 → AC-1 → AC-4.1 → AC-2.1 (validate in risk order)

## 11. Dependencies

### Runtime
- `github.com/refraction-networking/utls` v1.6+ — Chrome TLS impersonation
- `github.com/spf13/cobra` v1.8+ — CLI framework
- No CGO required (pure Go)

### Development
- Go 1.24+
- `golangci-lint` — linting
- `goreleaser` — cross-platform release builds

### External
- Google TravelFrontendUi API (no key, public endpoint)

## 12. Legal Considerations

- **License**: MIT (same as fli)
- **Google ToS**: Same approach as fli (1K+ stars, active development, no legal action). Read-only public endpoint, no authentication bypass, no scraping of protected content.
- **GDPR**: No personal data collected or stored. No cookies. No user accounts.
- **AI Act**: N/A — tool is a search interface, not an AI system.
- **Export**: No cryptographic restrictions beyond standard TLS.

## 13. References & Acknowledgements

- [fli](https://github.com/punitarani/fli) by Punit Arani — Google Flights Python library, primary inspiration
- [icecreamsoft](https://icecreamsoft.hashnode.dev/building-a-web-app-for-travel-search) — Working TypeScript code for Google Hotels batchexecute calls
- [Kovatch](https://kovatch.medium.com/deciphering-google-batchexecute-74991e4e446c) — batchexecute protocol fundamentals
- [Benjamin Altpeter](https://benjamin-altpeter.de/android-top-charts-reverse-engineering/) — batchexecute reverse engineering methodology
- [wong2/batchexecute](https://github.com/wong2/batchexecute) — TypeScript batchexecute helper library
- [SerpAPI Google Hotels API docs](https://serpapi.com/google-hotels-api) — Parameter documentation and response structure reference
- [utls](https://github.com/refraction-networking/utls) — Go TLS fingerprint impersonation library

## 14. Future Scope (v2+)

- Hotel reviews (rpcid `ocp93e`)
- Price tracking with local SQLite storage
- Google Flights "Explore" (cheapest destinations from origin)
- Webhook notifications for price drops
- Browser extension companion
