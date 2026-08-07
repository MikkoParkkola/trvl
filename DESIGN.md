# trvl — System Design

**Audience:** senior engineers evaluating the architecture.
**Scope:** provider runtime, anti-detection strategy, data flow, merge logic, flight encoding.

---

## 1. The Problem

Every major travel aggregator (Kayak, Google Flights, Booking.com) relies on negotiated API
agreements, rate cards, and affiliate relationships that are unavailable to a personal tool.
The alternative — scraping — is hard for reasons that go beyond the obvious HTTP layer:

- **TLS fingerprinting.** Akamai, Cloudflare, and PerimeterX inspect the TLS ClientHello
  (cipher suite order, extension list, key-share groups). Go's `crypto/tls` produces a
  fingerprint that no browser has ever sent, so it fails JA3/JA4 checks instantly.

- **HTTP/2 framing.** Even with a correct TLS fingerprint, Go's `x/net/http2` sends
  SETTINGS and WINDOW_UPDATE frames that differ from Chrome's values. Akamai's b_bot
  classifier operates at this layer independently of TLS.

- **Header ordering.** Go maps iterate in random order; WAF systems fingerprint the
  sequence of HTTP headers. A request where `Accept-Encoding` precedes `User-Agent` one
  moment and follows it the next is a bot fingerprint.

- **SSR cache structures.** Booking.com embeds its API response inside an Apollo
  normalized cache blob in the HTML. Airbnb uses a Niobe SSR cache with a completely
  different shape. Neither is a straightforward JSON API.

- **City identity.** "Prague" returns zero results from Hostelworld, whose API expects
  city ID "19". Booking expects a `dest_id` obtained from a separate autocomplete call
  with a city-specific WAF cookie tied to that destination.

- **Bot challenge cascades.** A single HTTP 202 from Akamai isn't a server error; it's
  an HTML JS-challenge page in a 2xx wrapper.

trvl handles all of this without API keys.

---

## 2. Architecture Overview

```
CLI / MCP tool call
        |
        v
  hotels.SearchHotels()            flights.SearchFlights()
        |                                   |
   ┌────┴────────────────────────┐    ┌─────┴──────────────────────────┐
   │  Google Hotels (scrape)      │    │  Google Flights (batchexecute) │
   │  Trivago (streamable HTTP)   │    │  Kiwi (REST API)               │
   │  providers.Runtime           │    └─────────────────────────────────┘
   │    ├── Booking.com           │
   │    ├── Airbnb                │
   │    ├── Hostelworld           │
   │    └── <user configs>        │
   └──────────────────────────────┘
        |
   models.MergeHotelResults()
        |
   filterHotels() → sortHotels()
        |
        v
   []HotelResult (unified)
```

The provider runtime (`internal/providers/`) is the generic execution engine for
anything requiring auth, custom headers, SSR unwrapping, or city ID resolution. Google
Hotels and Trivago bypass the runtime; Booking, Airbnb, Hostelworld, and user-added
providers go through it.

---

## 3. Provider Runtime Architecture

### 3.1 ProviderConfig — the data model

A provider is fully described by a `ProviderConfig`. Since 1.21.0 these are **reviewed
source files embedded in the binary** (`internal/providers/definitions`), not files on
disk: the config is the schema contract between the definition and the runtime that
executes it. Runtime state under `~/.trvl` records consent, enabled state and health, and
cannot replace endpoints, headers, authentication, request templates or response
mappings. Key fields:

```
ProviderConfig {
  id, name, category          — identity
  endpoint                    — URL with ${var} placeholders
  method                      — GET | POST
  headers                     — key/value pairs; env vars via ${env.VAR_NAME}
  header_order                — deterministic header sequence for WAF bypass
  query_params                — key/value with ${var} substitution
  body_template               — POST body with ${var} substitution
  auth                        — preflight config (see §3.3)
  cookies                     — "none" | "preflight" | "browser"
  tls.fingerprint             — "standard" | "chrome"
  rate_limit                  — requests_per_second, burst
  response_mapping            — how to parse the response (see §3.4)
  city_lookup                 — static map: city name → provider ID
  city_resolver               — dynamic autocomplete API for cache misses
  property_type_lookup        — canonical type → provider code
  amenity_lookup              — canonical amenity → provider ID
  sort_lookup                 — canonical sort → provider param
  filter_composite            — compound filter param builder (Booking nflt)
}
```

Every `${var}` is substituted before the request fires. Standard search vars:
`${location}`, `${checkin}`, `${checkout}`, `${currency}`, `${guests}`, `${lat}`,
`${lon}`, `${ne_lat}/${ne_lon}/${sw_lat}/${sw_lon}` (bounding box), `${num_nights}`,
`${city_id}`. Filter vars are set conditionally (absent when no filter is active) and
any `${...}` that remains unresolved after substitution is stripped from the URL along
with its `&key=` prefix — so optional params disappear cleanly rather than sending
literal placeholder strings.

### 3.2 Execution pipeline (Runtime.searchProvider)

```
searchProvider(ctx, cfg, location, lat, lon, checkin, checkout, ...)
    │
    ├─ ReloadIfChanged()          config hot-reload: re-reads JSON if mtime advanced
    │                             preserves cookie jar across reloads
    │
    ├─ rate limiter Wait()        token bucket per provider, default 0.5 req/s
    │
    ├─ Build vars map             ${checkin}, ${city_id}, bounding box, filters, ...
    │
    ├─ resolveCityID()            static lookup → dynamic resolver → cache result
    │
    ├─ applyBrowserCookies()      kooky reads real browser cookie store (if configured)
    │
    ├─ runPreflight()             optional: GET/POST for CSRF token, session cookies
    │   │                         skipped when browser cookies cover auth need
    │   └─ Extraction[]           regex-based extraction → vars for next request
    │
    ├─ Build composite filters    FilterComposite joins active filters into ${nflt}
    │
    ├─ Substitute + strip         endpoint URL finalized, orphan ${...} removed
    │
    ├─ Build request              ordered headers, env var substitution, POST body
    │
    ├─ Do()                       HTTP request via providerClient
    │
    ├─ isAkamaiChallenge()?       Tier 3a: browser cookies
    │                             Tier 3b: WAF JS solver (internal/waf)
    │                             Tier 4:  browser escape hatch (interactive only)
    │
    ├─ BodyExtractPattern         regex extracts JSON from HTML (Booking Apollo blob)
    │
    ├─ unwrapNiobe()              Airbnb SSR cache → inner data object
    │
    ├─ denormalizeApollo()        resolve __ref pointers in Apollo normalized cache
    │
    ├─ jsonPath(results_path)     dot-notation + prefix-wildcard + array traversal
    │
    └─ mapHotelResult() × N       field map → HotelResult, with Booking-specific
                                  extractors for room types, images, neighborhood
```

Per-provider state (`providerClient`) lives in a `sync.RWMutex`-guarded map. Each
client has its own cookie jar, rate limiter, and auth cache (10-minute TTL). Auth cache
is keyed on the resolved preflight URL — when `${city_id}` changes between searches,
the URL changes and the cache is invalidated. This matters for Booking.com, whose WAF
cookies are destination-specific.

### 3.3 Authentication tiers

```
Tier 1: header-based API key (${env.API_KEY})
Tier 2: preflight request
           GET/POST to auth URL → extract CSRF token, session ID via regex
           Two-stage: first URL fetches HTML, second fetches JS bundle
           referenced from HTML (persisted-query sha256 for GraphQL)
Tier 3a: browser cookie injection (kooky)
           Reads cookies from Brave/Chrome/Firefox on disk
           Skips preflight if extractions are empty: avoids overwriting
           real browser session with bot-classified preflight cookies
Tier 3b: WAF JS solver (internal/waf)
           Executes the Akamai JS challenge in a minimal event loop
           Produces bm_sz / _abck cookies without launching a browser
Tier 4: browser escape hatch (interactive + opt-in only)
           Opens the provider URL in the user's browser
           User clears the challenge manually
           kooky re-reads the updated cookie store
```

`BrowserEscapeHatch` in `AuthConfig` must be explicitly set to `true` AND the calling
context must carry `WithInteractive`. Background MCP calls never reach Tier 4.

### 3.4 Response mapping

Two challenges are solved at this layer beyond basic JSON extraction:

**Apollo normalized cache (Booking.com):** Booking's SSR page embeds a blob like:

```json
{
  "ROOT_QUERY": {
    "searchQueries": { "search({...})": { "__ref": "SearchQuery:..." } }
  },
  "SearchQuery:abc": { "results": [{"__ref": "BasicPropertyData:123"}] },
  "BasicPropertyData:123": { "displayName": {...}, "reviewScore": {...} }
}
```

`denormalizeApollo()` resolves `__ref` pointers recursively with a cycle guard, turning
the normalized graph into a plain tree that `jsonPath` can traverse normally.

**Airbnb Niobe cache:** Airbnb's SSR hydrates as:

```json
{"niobeClientData": [["CacheKey:...", {"data": {...}, "variables": {...}}]]}
```

`unwrapNiobe()` extracts the first `data` payload with non-empty content, then
`jsonPath` walks it with the standard `results_path`.

**Path wildcards:** Apollo's search key is
`search({"input":{...giant-params...}})` — it varies per request. The `results_path`
uses a `prefix*` wildcard segment: `searchQueries.search*.results` matches any key
beginning with `search`.

**Array traversal:** When an intermediate path segment hits an array (Airbnb's
`sections` is an array of heterogeneous objects), `jsonPath` iterates and returns the
first element that has a non-empty value for the next segment. This skips ad/metadata
sections that have `listings: []` to find the real results section.

**Rating normalization:** `response_mapping.rating_scale` is a multiplier applied
post-extraction. Booking returns 0–5 (`rating_scale: 2.0`), Hostelworld 0–100
(`rating_scale: 0.1`). All ratings land on the 0–10 scale.

### 3.5 City resolution

```
resolveCityID(lookup, location)    ← static table: O(1) or partial-match scan
        |
        └─ miss → resolveCityIDDynamic()
                    └─ GET city_resolver.url (${location} encoded)
                    └─ jsonPath(result_path) → id_field, extra_fields
                    └─ cache result into cfg.CityLookup
                    └─ registry.Save() → persisted to disk
```

Subsequent searches for the same city skip the network call. Extra fields (e.g.
Booking's `dest_type` alongside `dest_id`) are captured and injected as additional
`${vars}` for the main request URL.

### 3.6 Filter composite

Booking.com encodes multiple filters into a single `nflt` query parameter:

```
nflt=ht_id%3D204%3Bfc%3D1%3Breview_score%3D80
```

`FilterComposite` describes how to build this:

```json
{
  "target_var": "nflt",
  "separator": "%3B",
  "parts": {
    "property_type": "ht_id%3D",
    "free_cancellation": "fc%3D",
    "min_rating": "review_score%3D"
  },
  "scales": { "min_rating": 10.0 }
}
```

The runtime iterates active (non-empty) vars, applies scales (`min_rating: 8.0 → 80`),
handles multi-value expansion (amenity IDs joined as separate prefix+id parts), and joins
with the separator. The result lands in `${nflt}` for URL substitution.

---

## 4. Anti-Detection Strategy

The stack operates at four layers simultaneously:

### 4.1 TLS fingerprint (JA3/JA4)

`batchexec.Chrome146Spec()` returns a `utls.ClientHelloSpec` that matches Chrome 146's
exact ClientHello: cipher suite order, compression methods, extension list, supported
groups (`X25519MLKEM768` for post-quantum hybrid KEM), and the `ShuffleChromeTLSExtensions`
randomization that Chrome itself applies to avoid static fingerprinting. Providers
configured with `tls.fingerprint: "chrome"` use this spec via `refraction-networking/utls`.

### 4.2 HTTP/2 framing

Go's `x/net/http2` sends SETTINGS frames that Akamai's b_bot classifier recognizes.
`internal/providers/fhttp_transport.go` uses `bogdanfinn/fhttp`, a fork of Go's HTTP
stack patched to send Chrome's exact SETTINGS values and WINDOW_UPDATE:

```
HEADER_TABLE_SIZE:      65536
ENABLE_PUSH:            0
MAX_CONCURRENT_STREAMS: 1000
INITIAL_WINDOW_SIZE:    6291456
MAX_FRAME_SIZE:         16384
MAX_HEADER_LIST_SIZE:   262144
WINDOW_UPDATE:          15663105   (chrome connection flow)
```

Pseudo-header order (`:method :authority :scheme :path`) and PRIORITY frames match
Chrome's framing exactly. This is combined with the utls dialer via a bridge
`RoundTripper` that converts between `fhttp.Request` and `net/http.Request` types.

When `cookies.source: "browser"` is set, the standard Go TLS transport is used instead.
Observation: Booking.com's WAF produces fewer SSR results through the fhttp/utls pipeline
when real browser session cookies are present — the HTTP/2 framing difference triggers a
different server-side rendering path. The two strategies are therefore mutually exclusive.

### 4.3 Header ordering

Go's `map[string]string` iterates in random order. Without `header_order`, each request
has a different header sequence — a trivially detectable bot signal. When `header_order`
is set in the config, headers are written to the request in that exact sequence using
`req.Header.Set` in order (not relying on Go's internal map). Headers not listed in
`header_order` are appended after the ordered set.

### 4.4 Browser cookie injection

`applyBrowserCookies()` uses `browserutils/kooky` to read the cookie database of the
user's installed browsers (Brave, Chrome, Firefox, Safari) from disk, without requiring
any browser to be running. Cookies for the provider's domain are extracted and seeded
into the request's cookie jar before the search fires.

This carries Akamai's `bm_sz` and `_abck` sensor cookies written by the browser's JS
execution — cookies that server-side bot detection validates. No JS execution is needed
on trvl's side because the browser already produced them.

### 4.5 Google consent bypass

Google's EU consent gate returns a page with `consent.google.com` markers instead of
hotel data. `isGoogleConsentPage()` detects this and retries with pre-seeded consent
cookies:

```
SOCS=CAESNQgD...   (accept-all, no personalisation, no ad-tracking consent)
CONSENT=YES+srp.gws-20230810...
```

These are not session secrets; they are the same values Google generates for any
"accept" click and contain no PII.

### 4.6 WAF challenge handling

When Akamai returns HTTP 202 with an HTML JS challenge, trvl attempts:

1. Re-read browser cookies (Tier 3a) — covers the common case where the challenge
   was already cleared by a browser session
2. `waf.SolveAWSWAF()` (Tier 3b) — executes the Akamai JS in `internal/waf/eventloop.go`,
   a minimal DOM event loop that computes the challenge response without a full browser
3. Browser escape hatch (Tier 4) — opens the URL in the user's browser for manual
   clearance, only when `BrowserEscapeHatch: true` and context is interactive

---

## 5. Data Flow — Hotel Search

```
SearchHotels(ctx, "Amsterdam", opts{checkin, checkout, guests, currency})
    │
    ├─ normalizeHotelCity()           "amsterdam" → "Amsterdam" (passthrough here)
    │
    ├─ goroutine: Google Hotels       3 sort orders × 3 pages = up to 9 requests
    │   fetchHotelPageFull()          500ms cooldown between sort orders
    │   parseHotelsFromPageFull()     parse AF_initDataCallback JSON from HTML
    │   tagHotelSource("google_hotels")
    │
    ├─ goroutine: Trivago             StreamableHTTP MCP call (separate client)
    │   SearchTrivago()
    │
    └─ goroutine: providers.Runtime   parallel across all hotel-category providers
        ResolveLocation()             geocode city → lat/lon for bounding box
        SearchHotels()                fans out to each provider goroutine
            searchProvider() × N      rate-limited, auth, substitution, parse
                                      errors are non-fatal, logged + returned as
                                      ProviderStatus for LLM-readable diagnostics

    auxWg.Wait()

    MergeHotelResults(google_batches..., trivago, external...)
        │   name normalization + geo-proximity dedup (see §7)
        │
    filterHotels(opts)                price, rating, distance, amenities, brand
    sortHotels(opts.Sort)             price | rating | stars | distance
    enrichHotelAmenities(limit=5)     optional: detail page fetch for top results
    computeDistanceKm()               haversine from city center for every hotel
        │
        v
    HotelSearchResult{Hotels, Count, TotalAvailable, ProviderStatuses}
```

The Google Hotels scrape fetches up to 9 pages across three sort orders (relevance,
highest-rated, cheapest) to maximize unique coverage before external providers are merged.
Trivago and external providers run concurrently with Google's later pagination pages.

---

## 6. Google Flights — Protobuf-Style Encoding

Google Flights uses an internal RPC protocol called `batchexecute`. The request body is:

```
f.req=url_encode( json([ [[ rpcid, json(filters), null, "generic" ]] ]) )
```

The `filters` argument is a deeply nested JSON array with positional semantics — not
named keys. This is protobuf-style encoding in JSON: field identity is position, not name.
There are no `.proto` files available; the structure was reverse-engineered from browser
traffic.

Key positions (simplified):

```
filters[0]          — flights array (empty for one-way)
filters[1]          — settings
filters[1][2]       — trip type: 1=round-trip, 2=one-way
filters[1][5]       — seat class: 1=economy, 2=premium-economy, 3=business, 4=first
filters[1][6]       — passengers: [adults, children, infants_lap, infants_seat]
filters[1][7]       — max price
filters[1][10]      — bags: [carry_on_flag, checked_flag]
filters[1][13]      — segments: [{departure, arrival, stops, airlines, date, ...}]
filters[2]          — sort_by: 1=best, 2=price, 3=duration
```

Each segment is itself a 15-element positional array. Airline filters at segment[4]
require a list of IATA codes; alliance filters encode to string literals
(`"STAR_ALLIANCE"`, `"ONEWORLD"`, `"SKYTEAM"`) at a different position in the outer
array. Bag filters require `[]any{carryOn, checked}` — sending a scalar returns HTTP 400.

The response is a multi-part body prefixed with line lengths and the RPC ID:

```
)]}'
<length>
[["wrb.fr","rpcid",null,null,null,[...results...], "generic"],...]
```

`batchexec.ExtractFlightData()` parses this wrapping to reach the flight entries, then
`parseFlights()` walks the positional arrays to produce `[]FlightResult`.

This encoding is stable at the positional level but individual slot semantics have
shifted across Google UI updates. The bags filter position (moved from `outer[1][10]` to
the segment level) was discovered via live probe tests that hit the real endpoint and
verified against actual browser traffic.

---

## 7. Merge Strategy

`models.MergeHotelResults()` takes multiple `[]HotelResult` batches and produces a
single deduplicated list. The challenge: providers use different names for the same
physical hotel, different rating scales, and different price formats. A naive union
produces 4x duplicates.

### 7.1 Name normalization

```go
normalizeName(name string) string:
  1. lowercase + trim
  2. strip brand suffixes:
       " by ihg", " by marriott", " by hilton", " autograph collection", etc.
  3. remove punctuation: , . - ' " ( ) → replace & with "and"
  4. collapse multiple spaces
```

"Holiday Inn Express Amsterdam Arena Towers by IHG" →
"holiday inn express amsterdam arena towers"

This produces the primary dedup key. When two entries share the same normalized name but
are actually different properties (same brand, different city address), a disambiguation
key is built: `normalizedName|normalizedAddress` or `normalizedName|lat,lon`.

### 7.2 Geo-proximity dedup

Even after name normalization, cross-provider name variants slip through:

```
"Holiday Inn Express Amsterdam - Arena Towers"   (Google)
"Holiday Inn Express Amsterdam Arena Towers"     (Booking)
```

The normalized forms differ by punctuation around the dash. A geo-index tracks each
merged entry's coordinates. When a new entry's normalized name doesn't match any
existing key AND the entry has coordinates, the geo-index is scanned for any existing
entry within 150m. If found, the new entry is merged into that existing entry instead of
creating a new one.

### 7.3 Source aggregation

When two entries are confirmed as the same hotel:

- `Sources []PriceSource` accumulates all provider prices with deduplication on
  `(provider, price, currency)` tuples
- Primary price is updated to the lowest non-zero price
- Missing fields are filled in from the secondary: rating, review_count, stars,
  address, coordinates, description, image_url, neighborhood, room_types

The result is a single `HotelResult` with prices from all providers that have it,
letting downstream presentation show "from €89 (Google) / €94 (Airbnb) / €91 (Booking)".

### 7.4 Rating scale

External providers return ratings on incompatible scales. `rating_scale` in each
provider config converts to 0–10 before results enter the merge:

```
Google Hotels:    0–10 (passthrough)
Booking.com:      0–5  → ×2.0
Hostelworld:      0–10 (passthrough)
Airbnb:           0–5  → ×2.0
```

After normalization, rating comparisons and `MinRating` filters operate on a single scale.

---

## 8. Provider Lifecycle

**Adding a provider:** a reviewed definition is added to
`internal/providers/definitions` by source change — a pull request, or a
user-maintained fork — and a user turns it on with `trvl providers enable <id>`.

Before 1.21.0 the `configure_provider` MCP tool wrote LLM-generated JSON to
`~/.trvl/providers/<id>.json` and the runtime loaded it. That made a file on disk into
executable request-building instructions, so anything able to write that directory could
direct trvl's HTTP client (#538). The tool now refuses with an error explaining the
source-only path; legacy files are retained for rollback and never loaded, with a
one-time warning when they are found.

**Hot reload:** no longer applies to definitions, which are fixed at build time. Runtime
STATE (enabled, consent, health) is re-read from disk so a change takes effect without
restarting the MCP server. WAF tokens and session cookies live in the client and survive
state changes.

**Testing:** The `test_provider` MCP tool calls `searchProvider` directly and returns
the raw response body snippet, resolved URL, and any extraction values alongside the
results. This surfaces the root cause of failures without requiring manual HTTP debugging.

**Error classification:** Provider errors are classified into actionable hints returned
in `ProviderStatus.FixHint`:

```
preflight error    → WAF/auth needs refresh, call test_provider
results_path miss  → API response changed, update results_path via configure_provider
http 403 / 202     → WAF block, may need browser cookie refresh
rate limit         → back off, reduce requests_per_second in config
```

These hints flow back to the calling LLM for autonomous diagnosis and remediation.

**Stale detection:** A provider is stale if `error_count > 0` and no successful request
in the last 24 hours. Stale providers are listed in `list_providers` output with status
"error" so they can be reconfigured or removed.

---

## 9. Key Invariants

- All prices entering `MergeHotelResults` must have a `Sources` entry with `Provider`
  set, or the merge cannot distinguish "Google €120" from "Booking €120".

- Rate limits are enforced per provider, not globally. Two providers can request
  concurrently. Each is bounded by its own token bucket.

- Auth cache is invalidated when the preflight URL changes between calls (city switch).
  WAF cookies are destination-specific; reusing Paris cookies for Amsterdam produces
  silently degraded results, not an error.

- `${env.VAR_NAME}` substitution happens at request build time, not at config load
  time. This allows API keys to be rotated without reloading the config.

- `BodyExtractPattern`, `unwrapNiobe`, and `denormalizeApollo` are applied in that
  order. The first transforms the raw HTTP body before JSON parsing; the latter two
  operate on the parsed JSON tree. They are independent and composable.

- Hotels without coordinates skip geo-proximity dedup and fall back to name-key-only
  matching. The 150m geo-merge radius is tight enough to avoid false-positives between
  adjacent but distinct properties in dense city centers.
