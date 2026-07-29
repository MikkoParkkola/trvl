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
  +-- internal/ground             Bus + train + ferry search (22 providers in parallel)
  |     +-- flixbus.go            FlixBus REST API (global.api.flixbus.com)
  |     +-- regiojet.go           RegioJet REST API (brn-ybus-pubapi.sa.cz)
  |     +-- eurostar.go           Eurostar GraphQL (site-api.eurostar.com)
  |     +-- deutschebahn.go       DB Vendo API (int.bahn.de/web/api)
  |     +-- oebb.go               ÖBB Austrian Railways (shop/HAFAS APIs)
  |     +-- ns.go                 NS Dutch Railways (public API, embedded key)
  |     +-- digitransit.go        VR Finnish Railways via Digitransit GraphQL
  |     +-- sncf.go               SNCF Connect API (curl BFF fallback + Go CDP)
  |     +-- trainline.go          Trainline aggregated rail API (browser cookie auth)
  |     +-- renfe.go              Renfe Spanish Railways (REST price calendar)
  |     +-- trenitalia.go         Trenitalia Italian Railways (lefrecce.it BFF JSON)
  |     +-- transitous.go         Transitous/MOTIS2 (routing.spicebus.org)
  |     +-- europeansleeper.go    European Sleeper night trains
  |     +-- snalltaget.go         Snälltåget Swedish night trains
  |     +-- tallink.go            Tallink/Silja Line REST API (book.tallink.com) — live prices
  |     +-- vikingline.go         Viking Line reference schedule — Distribusion API pending
  |     +-- eckeroline.go         Eckerö Line Magento AJAX API (getdepartures) — live prices
  |     +-- finnlines.go          Finnlines ferry schedule
  |     +-- stenaline.go          Stena Line reference schedule — Distribusion API pending
  |     +-- dfds.go               DFDS availability API (travel-search-prod.dfds-pax-web.com)
  |     +-- ferryhopper.go        Ferryhopper Mediterranean ferry routes
  |     +-- taxi.go               Taxi fare estimates for airport transfers
  |     +-- browser_scraper.go    Shared Go CDP browser fallback
  |     +-- search.go             Parallel dispatch + result merging
  |
  +-- internal/cars               Rental car search surface and setup-aware provider statuses
  |     +-- provider_catalog.go   User-facing rental-car provider catalog
  |     +-- search.go             Optional Skyscanner Car Hire integration
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

mcp/                              MCP server (1 advertised smart tool + 66 legacy-compatible capabilities, stdio + HTTP)
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
          +---> oebb.go          Shop/HAFAS API -> Railjet journey
          +---> ns.go            Station lookup -> journey query (embedded key)
          +---> digitransit.go   GraphQL query -> VR fare lookup (public key)
          +---> sncf.go          curl BFF -> offer query (1 req/6s limit)
          +---> trainline.go     Station search -> journey query (browser cookie auth)
          +---> renfe.go         REST price calendar -> AVE journey
          +---> transitous.go    Geocode -> MOTIS2 routing (1 req/6s limit)
          +---> tallink.go       voyage-avails API (1 req/12s limit)
          +---> vikingline.go    Reference schedule lookup (no network)
          +---> eckeroline.go    Magento AJAX API (form_key + getdepartures)
          +---> stenaline.go     Reference schedule lookup (no network)
          +---> dfds.go          Availability API (1 req/12s limit)
          |     (all 20 run in parallel via goroutines)
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
- **Concurrency**: Goroutines make parallel provider search natural. Searching 22 ground transport providers in parallel is a `sync.WaitGroup` and 22 goroutines.
- **MCP fit**: MCP servers are long-running stdio processes. Go's low memory footprint and fast startup make it ideal for a tool that launches per-conversation.

### Why reverse-engineer vs official APIs?

- **Free**: Google has no public Flights/Hotels API. Skyscanner's affiliate API requires business approval. Booking.com's API requires a partner agreement. trvl works out of the box with zero signup.
- **No API keys**: for the core sources, nothing to manage, rotate, or pay for. No `.env` files, no secrets in CI. Optional providers can be switched on with a key of your own, listed in the README; none is required.
- **No rate limits imposed by the provider**: Official APIs typically limit you to N requests per day. trvl's self-imposed limits are conservative but not artificially low.
- **Same data**: The batchexecute protocol returns the exact same data that google.com/travel shows. No "lite" tier, no missing fields.
- **Precedent**: [fli](https://github.com/punitarani/fli) has done this for Google Flights since 2023 with no legal issues.

The tradeoff is maintenance: when Google changes their protocol (rare but possible), trvl needs updating. This is a conscious choice -- free and keyless access is worth occasional breakage.

### Why parallel provider search?

When you search "Prague to Vienna", trvl queries all relevant ground providers simultaneously:

```
Sequential: FlixBus(2s) + RegioJet(1s) + DB(3s) + ÖBB(4s) + NS(1s) + VR(1s) + SNCF(2s) + Trainline(2s) + Renfe(4s) + Eurostar(1s) + Transitous(1s) + ferries(1s) = 23s
Parallel:   max(all 22 providers)                                                                                                                                   = 4s
```

Parallel search gives you the best price across all providers in the time it takes to query the slowest one. The implementation is straightforward Go concurrency: one goroutine per provider, results collected via a channel, merged and sorted after all complete.

### Why MCP?

MCP (Model Context Protocol) is how AI assistants call external tools. trvl as an MCP server means:

- **AI-native**: Claude, Cursor, Windsurf, and any MCP client can search flights, hotels, and trains natively. The AI decides when to search, what parameters to use, and how to present results.
- **Structured content**: MCP's `structuredContent` returns typed JSON alongside human-readable summaries. The AI gets machine-parseable data; the user gets formatted text. Both from one call.
- **Progressive disclosure**: Every response includes suggestions for follow-up searches ("Try nearby airports", "Check flexible dates"). The AI can chain these automatically.
- **No integration work for local mode**: Adding trvl to any MCP client is one config line. Local stdio needs no REST API, webhook, or OAuth setup. Remote HTTP mode is explicit and can use scoped bearer tokens or OAuth 2.1 introspection when a gateway/provider handles Authorization Code + PKCE.

trvl also works as a standalone CLI (56 commands) for users who prefer the terminal or want to script searches.

### Why a monorepo with internal packages?

Go's `internal/` convention enforces that packages under `internal/` cannot be imported by external code. This means:

- **API stability**: Only `cmd/trvl` and `mcp/` are public entry points. Internal packages can change freely without breaking external users.
- **Shared types**: `internal/models` defines `Flight`, `Hotel`, `GroundRoute`, etc. Both CLI and MCP use the same types, ensuring consistency.
- **Shared post-search policy layer**: `internal/flights/policy.go` (`ApplySharedFlightPolicy`) and `internal/hotels/policy.go` (`ApplySharedHotelPolicy`) are the single source of truth so CLI and MCP cannot drift on budget caps, time windows, FF bag adjustments, and adults-only exclusion (when party includes children). Both surfaces call the shared functions after search; genuine `Budget*Max` prefs apply on both; profile-derived *hints* (e.g. HotelHints.MaxPrice, GroundHints.Type) are deliberately not used for hard filtering.
- **Shared HTTP layer**: `internal/batchexec` handles TLS fingerprinting, rate limiting, caching, and retry. All Google-facing packages share this single client.
- **No circular dependencies**: The dependency graph is a clean DAG from entry points down to `models` at the leaf.

## External dependencies

trvl has 21 direct dependencies. The CLI/runtime core is small; most of the list is the browser-emulation and telemetry machinery that lets trvl reach bot-defended providers without user API keys.

| Dependency | Purpose |
|-----------|---------|
| `github.com/spf13/cobra` | CLI command framework |
| `github.com/refraction-networking/utls`, `github.com/bogdanfinn/{utls,fhttp,tls-client}`, `github.com/cloudflare/circl` | Chrome TLS/HTTP fingerprint impersonation |
| `github.com/chromedp/chromedp`, `github.com/chromedp/cdproto` | Headless-Chrome (CDP) browser fallback for JS-gated providers |
| `github.com/browserutils/kooky` | Reads browser cookie databases off disk (e.g. Booking.com auth) |
| `github.com/grafana/sobek` | Embedded JS runtime for provider script evaluation |
| `github.com/andybalholm/brotli`, `github.com/klauspost/compress` | Response decompression |
| `go.opentelemetry.io/otel{,/sdk,/trace,/exporters/...}` | Optional OpenTelemetry tracing |
| `golang.org/x/{net,time,term,sync,text}` | HTTP/2 + proxy, rate limiting, terminal width, errgroup, text transforms |

Everything else is Go stdlib: `net/http`, `encoding/json`, `sync`, `context`, `time`, `sort`, `strings`, `fmt`.

## Browser sessions, credentials, and local state

Hotel and rail sites put bot protection in front of their search APIs. trvl gets past it
by reusing the browser session you already have, which is why searches work at all
without an API key. That mechanism reads local state, so this section states exactly
what, when, and where it goes. The README carries a summary; this is the full account.

### What is read, and when

**At startup, before any search.** Creating the provider runtime kicks off background
reads of the browser cookie stores for every provider configured to use them. On macOS
the first such read goes through the Keychain and takes six to ten seconds cold, which
is precisely why it is started early rather than on demand.

**On a hotel search.** Booking.com needs an `aws-waf-token`. trvl looks for one in this
order: the cache under `~/.trvl`, then the installed browser's cookies via
[kooky](https://github.com/browserutils/kooky), then a headless harvest driven through
the installed Chrome. A token obtained that way is written back to that cache and reused
for days.

**On a rail search.** When Trainline, Eurostar or SNCF answers with a 403 challenge,
trvl runs [nab](https://github.com/MikkoParkkola/nab) to read cookies for that operator
and retries with them.

Three things follow:

- **It is automatic.** No flag turns it on. Running trvl at all starts the startup reads.
- **It reads credential storage.** Browser cookie databases are encrypted, so getting at
  them means Keychain access on macOS. What is read is the user's own session cookies for
  the site being searched.
- **Some of it persists and some of it launches a browser.** The Booking.com token is
  written under `~/.trvl`. The headless harvest starts Chrome.

Where those cookies go: into the request trvl makes to the site they were read from, so
that operator receives its own cookies back, which is the point of reading them. trvl
sends them nowhere else and reports them to no endpoint of its own.

That is enforced, not merely intended. On the one path where the request URL comes from
outside — the Booking.com room lookup takes a URL from an MCP argument or from a link
carried on a search result — the cookie header is withheld unless the request is HTTPS and
its host is `booking.com` or a subdomain. The check runs on the parsed hostname at the last
line before transmission (`cookies.HeaderIfPermittedForURL`), so neither a lookalike domain
nor a `https://www.booking.com@elsewhere/` userinfo trick collects the session. Sending the
cookies to a host they were not read for is a test failure, in `internal/hotels`
(`TestFetchBookingPage_WithholdsCookiesFromForeignHost`) and in `internal/cookies`.

A URL that is not Booking.com is now refused before the first connection is made, so it
gets no request at all rather than a request without credentials. The check requires
HTTPS, requires the host to be `booking.com` or a subdomain, and requires the host to be
an ordinary DNS name — the last clause exists because Go's URL parser keeps an IPv6 zone
identifier in the hostname, which let `https://[::1%25.booking.com]/` suffix-match
`.booking.com` while the dialer connected to loopback.

What remains is redirects. Only the initial URL is checked, and the shared HTTP client
sets no redirect policy, so a Booking.com address that redirects can still produce a
request to another host. Session cookies do not follow it — Go's client refuses to carry
an explicitly-set `Cookie` header across a change of host
(`net/http/client.go`, `shouldCopyHeaderOnRedirect`) — so the exposure is the response
body, not the credential. One caveat on that stdlib guarantee: it compares hosts via
`isDomainOrSubdomain` and never inspects the scheme, so a same-host `https://` → `http://`
redirect keeps the header and puts the session on the wire in cleartext. Closing both
means giving a client shared by every provider its own redirect policy. Tracked in
[#537](https://github.com/MikkoParkkola/trvl/issues/537).

The host check above lives on the room-lookup path because that is the only path taking a
web address from the caller. The rail providers (Trainline, Eurostar, SNCF) attach browser
cookies through `cookies.HeaderIfPermitted`, which enforces the user's consent but not a
destination — they do not need one, because their request URLs are constants in this
repository rather than caller input. That is safety by construction, not by check: if any
of those providers ever starts building a URL from caller data, it must move to
`HeaderIfPermittedForURL` first.

### Two settings, two different questions

They are easy to confuse, so state them plainly:

| Setting | The question it answers | What it covers |
| --- | --- | --- |
| `TRVL_NO_BROWSER_COOKIES=1` | May trvl touch **my** browsers and the sessions I am logged into? | Every read of a browser cookie store (including via nab), the cookie cache under `~/.trvl`, and every window trvl opens in your real browser — the escape hatch and the Trainline/SNCF human-verification fallbacks |
| `TRVL_NO_TIER2_CDP=1` | May trvl **run a browser process** at all? | Every headless browser trvl starts itself — all three places in the code that can start one |

Setting the first does **not** stop the headless browser, and that is deliberate: the
headless browser starts from an empty profile, so it never touches your sessions. Setting
the second does **not** stop trvl reading cookies you already have. Set both to refuse
everything browser-related.

`TRVL_NO_BROWSER_COOKIES=1` covers nab as well as the reader inside trvl: the helper is
refused at the point it would be started, so no cookie store is read from any process.
Cookie reads stay on by default because they are what makes hotel and rail search work
against sites that block non-browser traffic, and switching them off does not make those
searches fall back to something else — an operator that answers with a bot challenge
simply returns no results, which looks like trvl finding no trains rather than like a
setting you chose. That is the trade, stated so it can be made deliberately. Decided in
[#521](https://github.com/MikkoParkkola/trvl/issues/521).

### The headless browser

When a site answers with a bot challenge, trvl can drive a copy of Chrome, Brave or Edge
already installed, let the challenge resolve itself, and keep the resulting cookies. It
runs headless: no window opens, focus is never taken, nothing appears on screen. It
bundles no browser of its own.

It starts from an empty profile, so it does not read cookies you already have — that is
the separate switch above, and they are separate because they are separate things: one
reads the session you are already logged into, this one starts a new anonymous session
and keeps what that session is given. Because it reads nothing of yours,
`TRVL_NO_BROWSER_COOKIES=1` leaves it running: if it did not, declining access to your own
browser would also take away the one path that still works without it, and hotel search
would return nothing for no gain in privacy.

One exception, and it is a real one: on the sites trvl signs into on your behalf, a cookie
decline does switch this recovery path off. Those sites hand the recovered cookies to the
same store that can also hold cookies copied out of a real browser, and that store keeps
no note of which is which, so a cookie decline refuses all of it rather than guess.
Separating the two is tracked as its own change. Hotel and rail search keep this recovery
browser either way.

This also runs by default, for the same reason — with it off, a challenged search returns
nothing and looks like an empty result rather than a switched-off feature. Set
`TRVL_NO_TIER2_CDP=1` to decline. It costs a browser process for a few seconds per
challenged search, which is the reason someone might want it off. The check sits on each
of the three places in trvl that can start a browser, rather than on the entry points
above them, so a provider reaching past the usual route still cannot spawn one. It governs
the *headless* browser only — the separate visible-window escape hatch asks before it opens
anything and requires its own per-provider opt-in.

### Dated measurement: what actually got past the rail bot walls

A one-off measurement on 2026-07-27 — a snapshot of how three sites behaved that day, not
a promise about how they behave now — found that reading cookies off disk returned nothing
at all for Trainline, SNCF Connect and Rome2Rio, because the tokens those sites check are
issued to a live browsing session and are not sitting in the cookie store. A headless visit
cleared the wall for two of them and returned usable cookies (Trainline's `datadome`,
Rome2Rio's Cloudflare `__cf_bm`), so declining the headless browser is likely to leave
those two empty. SNCF Connect was the exception: the headless visit reached a challenge
that needs a human, and trvl only reuses cookies from a challenge that actually cleared, so
the headless path was not what fixed SNCF either way.

Bot walls change without notice and nothing in CI re-checks this, so treat the specifics as
dated. To re-measure, run the probe test with `TRVL_COOKIE_PROBE=1`.

### AF-KLM: the one credential that may come from a credential manager

Ordinary round-trips are already covered in the default merge: Kiwi returns both legs of a
paired itinerary, and Google returns the genuine round-trip fare with the matching return
chosen at booking. AF-KLM is there for what neither offers — the rail+fly itineraries it
sells, where a train leg from Brussels Midi, Antwerp or Brussels is ticketed as part of the
flight instead of being a separate rail booking you have to make and risk yourself. It also
returns both legs on KL/AF metal in full detail.

It is the only provider whose **API key** can come from a credential manager, under tight
rules. Browser cookie access is a separate mechanism, covered above; several providers do
that, and it also touches the Keychain.

| Variable | Effect |
| --- | --- |
| `AFKLM_KEY` | The API key itself. Once set, AF-KLM native round-trips join default searches automatically. |
| `AFKLM_OP_REF` | A 1Password secret reference, e.g. `op://Private/AF-KLM/credential`. Read via the `op` CLI **only** under an explicit `--provider afklm`. |
| `AFKLM_KEYCHAIN_SERVICE` | Overrides the macOS Keychain service name (default `afklm-api-key`). Read under `--provider afklm` only. |

Set `AFKLM_OP_REF` without `AFKLM_KEY` and a default search reports that AF-KLM was skipped
and why, rather than quietly leaving it out.

The rule, and why it exists: a search you didn't ask for never runs an AF-KLM credential
helper. `op` and `security` are third-party programs that can block, can pop an interactive
prompt, and can leave stray processes behind — so an opportunistic lookup on the default
path is limited to reading an environment variable, which costs nothing and cannot prompt.
Only an explicit `--provider afklm` may reach an external store, where a prompt is
something the user asked for. Reported as
[#507](https://github.com/MikkoParkkola/trvl/issues/507).

The Keychain entry is created with
`security add-generic-password -a "$USER" -s afklm-api-key -w <key>` and is consulted under
`--provider afklm` only, for the same reason.

### One limitation on Windows

Every helper trvl runs is bounded by a deadline on every platform, so none of them can hang
a search. Cleaning up what a helper leaves behind is weaker on Windows than elsewhere.

On Unix a helper is signalled as a process group, so anything it started dies with it. On
Windows it goes into a job object, and a job can only be assigned to a process that already
exists, so there is a window of microseconds after the helper starts during which a child it
creates is not yet a member. A helper that forks something in its first instants can
therefore leave that child running after the helper itself has been killed.

None of the programs trvl actually invokes behaves that way, so in practice stray processes
should not appear. It is a real gap rather than a theoretical one, though, and closing it
needs a suspended start whose own failure mode is worse than the gap, so it is documented
instead of half-fixed. The reasoning is in
[#526](https://github.com/MikkoParkkola/trvl/issues/526).
