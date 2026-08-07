# Legal Analysis

## What trvl does

trvl accesses public-facing internal APIs from Google and European transport providers. It sends HTTP requests to the same endpoints that a web browser uses when you visit google.com/travel, flixbus.com, or similar sites. This is the same approach used by [fli](https://github.com/punitarani/fli) (1,400+ stars, MIT licensed, openly reverse-engineers Google Flights since 2023).

Specifically, trvl:

- Sends HTTP requests to publicly accessible URLs (no VPN tunneling, no network-level tricks)
- Parses the responses (JSON, HTML, GraphQL) into structured data
- Applies a token-bucket rate limiter to every provider
- Does not store or redistribute the raw responses

## Legal precedent

Three cases are directly relevant:

**hiQ Labs v. LinkedIn (2022, 9th Circuit)** -- The court ruled that scraping publicly accessible data does not violate the Computer Fraud and Abuse Act (CFAA). The CFAA's "without authorization" language applies to systems that require authentication, not to public-facing websites. This is the leading US case on scraping public data.

**Meta Platforms v. Bright Data (2024, N.D. Cal.)** -- Meta's claims were dismissed for Bright Data's collection of publicly available data. The court distinguished between accessing public pages (lawful) and circumventing access controls to reach private data (potentially unlawful). trvl only accesses public pages.

**fli library (2023-present)** -- The [fli](https://github.com/punitarani/fli) library has openly reverse-engineered Google Flights' batchexecute protocol for over two years, accumulating 1,400+ stars. It remains available on GitHub and PyPI with no legal challenge from Google. trvl uses the same protocol and approach, with attribution to fli.

## What trvl does NOT do

- **No authentication bypass** -- trvl never logs in, never supplies credentials, never accesses content behind a login wall
- **No CAPTCHA solving** -- if Google or any provider presents a CAPTCHA, trvl returns an error
- **No rate limit circumvention** -- trvl includes its own rate limiters that are more conservative than what the providers would allow; it backs off exponentially on 429/5xx responses
- **No personal data collection** -- trvl searches for flights, hotels, and transport routes; it does not collect, store, or process personal information about individuals
- **No content behind login** -- every endpoint trvl accesses is reachable by an unauthenticated browser visit
- **No Terms of Service circumvention** -- trvl does not agree to any ToS (it never visits a page with a ToS clickwrap), so there is no contractual obligation to breach

## Rate limiting

Every provider has a client-side token-bucket rate limiter implemented via Go's `golang.org/x/time/rate` package. The limits are deliberately conservative:

| Provider | Limit | Implementation |
|----------|-------|----------------|
| Google (flights, hotels, explore) | 10 req/s, burst 1 | `internal/batchexec/client.go` |
| FlixBus | 10 req/s, burst 1 | `internal/ground/search.go` |
| RegioJet | 10 req/s, burst 1 | `internal/ground/search.go` |
| Deutsche Bahn | 1 req/2s (0.5 req/s) | `internal/ground/deutschebahn.go` |
| SNCF | 1 req/6s (~0.17 req/s) | `internal/ground/sncf.go` |
| Eurostar | 1 req/20s (0.05 req/s) | `internal/ground/eurostar.go` |
| Transitous | 1 req/6s (~0.17 req/s) | `internal/ground/transitous.go` |

In addition, all Google requests retry with exponential backoff (base 1s, max 3 retries) when receiving 429 or 5xx responses. The in-memory cache (5-minute TTL for flights, 10-minute for hotels, 1-hour for destinations) further reduces request volume.

## Provider-specific notes

### Google Flights and Hotels

trvl uses Google's `batchexecute` protocol -- the same internal RPC mechanism that google.com/travel uses in the browser. The [fli](https://github.com/punitarani/fli) library documented this protocol in 2023 and it has been used by thousands of developers since. Google has not taken action against fli or any similar project. The TLS fingerprint impersonation (via [utls](https://github.com/refraction-networking/utls)) ensures requests look like normal Chrome browser traffic.

### FlixBus

FlixBus exposes a public REST API at `global.api.flixbus.com` that requires no authentication. This API is used by their own website and mobile app. Multiple open-source projects use it (e.g., [CombiTrip](https://github.com/combitrip), various FlixBus API wrappers on GitHub).

### RegioJet

RegioJet's public API at `brn-ybus-pubapi.sa.cz` is unauthenticated and serves their website and third-party integrations. It returns standard JSON responses with no access restrictions.

### Eurostar

Eurostar's `site-api.eurostar.com/gateway` GraphQL endpoint is the same API their booking website uses. It requires no authentication for schedule and price queries.

### Deutsche Bahn

DB's Vendo API at `int.bahn.de/web/api` powers their international booking site. Deutsche Bahn has a long history of supporting open data initiatives (DB Open Data Portal, HAFAS-based projects). Multiple established open-source projects access DB's APIs, including [direkt.bahn.guru](https://github.com/juliuste/direkt.bahn.guru) and the broader [FPTF ecosystem](https://github.com/public-transport/friendly-public-transport-format).

### SNCF

SNCF Connect's API serves their booking frontend. SNCF actively publishes open data through [data.sncf.com](https://data.sncf.com/) and supports the open transit data ecosystem. trvl's conservative 1-request-per-6-seconds limit generates less traffic than a single user browsing their website.

### Transitous

[Transitous](https://transitous.org) is an explicitly open-source project (routing via `routing.spicebus.org` running MOTIS2). It is designed for programmatic access and welcomes third-party clients.

## Risk assessment

### What could happen

- **API changes** -- Google or any provider could change their API format at any time, breaking trvl's parsing. This is the most likely disruption and is purely a maintenance issue, not a legal one.
- **IP blocking** -- Providers could block requests from known cloud/VPN IPs or implement stricter bot detection. trvl's rate limiting makes this unlikely for individual users, but possible at scale.
- **Cease and desist** -- A provider could send a C&D letter requesting that trvl stop accessing their API. This would be a business decision to comply with, not a legal obligation under current case law (per hiQ v. LinkedIn).

### What is unlikely

- **Lawsuit** -- Given hiQ v. LinkedIn, Meta v. Bright Data, and the continued existence of fli and hundreds of similar projects, a lawsuit over accessing public travel data is extremely unlikely. There is no precedent for it succeeding.
- **Criminal liability** -- The CFAA requires access "without authorization" to a protected computer. Public APIs that require no authentication do not meet this threshold under current 9th Circuit interpretation.
- **GDPR/privacy issues** -- trvl does not collect, store, or process personal data. Search queries (origin, destination, date) are not personal data. Results (prices, schedules) are publicly available commercial information.

## TLS compatibility and anti-bot systems

trvl uses the [utls](https://github.com/refraction-networking/utls) library (BSD-3, developed by the University of Colorado for censorship circumvention research) to maintain TLS compatibility with modern web servers. Many CDNs and web servers validate the TLS ClientHello fingerprint and reject connections that don't match a known browser profile. Without TLS fingerprinting, standard Go HTTP clients receive HTTP 403 responses from approximately 40% of modern web servers.

Whether TLS fingerprinting constitutes circumvention of a Technological Protection Measure (TPM) is unsettled law. DMCA Section 1201 (US) and EU Directive 2001/29/EC Article 6 protect "effective technological measures that control access to a work." The application of these provisions to TLS fingerprint validation by commercial web services has not been definitively adjudicated. Users in jurisdictions with strict TPM laws should consider this when configuring optional providers that require Chrome TLS fingerprinting.

## Privacy and data handling

trvl does not collect, store, or process personal data about individuals. Search queries (origin, destination, date) are not personal data. Results (prices, schedules) are publicly available commercial information.

Provider state stored under `~/.trvl` records which providers are enabled, the consent given (timestamp and domain), and health counters. Endpoint URLs and request templates live in the binary as reviewed source, not on disk. Session cookies obtained during preflight requests are used for the current session only and are not persisted to disk. Consent records contain a timestamp and domain name — users should be aware this constitutes metadata about their provider usage.

Legacy `~/.trvl/providers/*.json` files written by versions before 1.21.0 may still be present. They are no longer loaded or executed; they are retained so a user can roll back or migrate them by hand, and can be deleted at any time.

Diagnostic files under `~/.trvl` — the health journal and the per-provider state — record which provider failed and why. Since 1.21.0 the URLs inside those errors are replaced with a salted fingerprint before being written, so a failure can still be correlated across entries without the file holding the search itself (origin, destination, dates, party size). See issue #531.

## User responsibility

Users of trvl should:

1. **Respect the built-in rate limits** -- Do not modify the rate limiter constants to send faster requests
2. **Do not use for commercial scraping** -- trvl is designed for personal travel search, not bulk data collection
3. **Do not redistribute raw data** -- Displaying search results to yourself or an AI assistant is personal use; bulk redistribution may raise different legal questions
4. **Check local laws** -- While US case law (hiQ v. LinkedIn) is favorable, other jurisdictions may have different rules about automated access to websites
5. **Comply with any C&D requests** -- If a provider specifically asks you to stop, it is wise to comply regardless of legal standing

## Built-in vs. optional providers

trvl includes two tiers of data providers, clearly separated by how they are maintained and where legal responsibility lies.

### Built-in providers (maintained by trvl)

These providers are part of trvl's source code and are active by default. trvl's maintainers are responsible for keeping them working and for any legal implications of their use.

| Provider | Method | Notes |
|----------|--------|-------|
| Google Flights | batchexecute protocol (same approach as [fli](https://github.com/punitarani/fli)) | No API key required |
| Google Hotels | HTML parsing of Google Travel pages | No API key required |
| Kiwi.com | REST API | Fallback for specific flight queries |
| FlixBus | Public REST API at `global.api.flixbus.com` | No API key required |
| RegioJet | Public REST API | No API key required |
| Eurostar | Public GraphQL API | No API key required |
| Deutsche Bahn | Vendo API (widely used by OSS projects) | No API key required |
| SNCF | Public API + SNCF Open Data | No API key required |
| Transitous | Open-source MOTIS 2 transit router | Designed for programmatic access |
| DigiTransit | Finnish Transport Agency open API | CC-BY licensed |
| European Sleeper | Sqills S3 Passenger API (via booking.europeansleeper.eu) | Night trains Brussels↔Berlin↔Prague + Amsterdam/Rotterdam/Antwerp/Dresden |
| Snälltåget | Sqills S3 Passenger API (via bokning.snalltaget.se) | Swedish night trains Stockholm↔Malmö/Åre/Berlin |
| Ferry operators | Various APIs (DFDS, Viking Line, Tallink, Stena, Eckerö, FerryHopper) | No API key required |
| (Booking.com was moved to optional providers in v0.4.0) | — | — |
| Distribusion | Partner API | Requires `DISTRIBUSION_API_KEY` |
| Open-Meteo | Weather API | Free, CC-BY 4.0 |
| Wikivoyage | MediaWiki API | CC-BY-SA 3.0 |
| OpenStreetMap | Overpass API | ODbL licensed |
| Uber / Bolt (ride-hail) | Deep-links only (no API call, no scraping) | trvl constructs a booking deep-link with pickup/dropoff; the user opens the app, where the real price/availability is shown. No data is fetched, no ToS engaged. |
| Ticketmaster, Foursquare, etc. | Official APIs with keys | Optional, free tier available |
| Deal feeds | RSS (Secret Flying, Fly4Free, Holiday Pirates, The Points Guy) | Public RSS syndication |

### Optional providers (user-configured, AI-assisted)

trvl includes a generic provider runtime, and the definitions it can run are **reviewed source files shipped inside the binary**. **They are not active by default.** Each must be explicitly enabled by the user, and each requires individual consent.

**Why this system exists:** Some popular travel services (Airbnb, Booking.com's frontend, Hostelworld, VRBO, etc.) do not offer free public APIs. Their websites use internal APIs that could technically be accessed programmatically, but their Terms of Service may restrict automated access. Rather than including service-specific scraping code in trvl's core (which would make trvl's maintainers responsible for potential ToS violations), trvl keeps these as separate, individually-consented definitions that a user opts into one at a time.

**How it works:**

1. The user enables a shipped definition with `trvl providers enable <id>`
2. trvl asks the user **directly** (bypassing any AI assistant) to confirm they accept responsibility for compliance with the target service's Terms of Service
3. The consent and enabled state are recorded locally under `~/.trvl`
4. Future searches include results from enabled providers

**This changed in 1.21.0.** Previously an AI assistant generated a provider configuration and trvl wrote it to `~/.trvl/providers/<id>.json`, from where it was loaded and executed. That made a JSON file on disk into executable request-building instructions, so anything able to write that directory could direct trvl's HTTP client — see the trust-boundary discussion in issue #538. Provider definitions are now source code: they arrive by reviewed change or in a user-maintained fork, and files under `~/.trvl/providers` are left in place for rollback but are **never loaded**. trvl warns once when it finds them.

**Reliability:** Shipped definitions are reviewed before release, but they depend on services trvl does not control. API endpoints, authentication tokens, and response formats change when those services update their websites, and a definition can stop working between releases.

**What to do if a provider stops working:**

1. Check `trvl providers status` for the recorded failure reason
2. Check the reference open-source project (listed in the provider catalog) for updated API details
3. Use `trvl providers status` to see error details
4. Some services may actively block automated access — in that case, the provider may not be configurable

**What trvl provides for optional providers:**

- A general-purpose HTTP client (like curl or Postman) with configurable JSON field mapping
- Template files targeting `example.com` that document common API patterns
- A provider catalog listing available services and pointers to open-source reference projects
- Rate limiting, session management, and modern TLS compatibility

**What trvl does NOT provide:**

- No service-specific scraping code for any optional provider
- No pre-configured provider settings (all generated by the AI at the user's request)
- No guarantee that any optional provider will work or continue working

### User responsibility for optional providers

When a user configures an optional provider, the user is solely responsible for:

1. **Terms of Service compliance** -- The user should review the target service's ToS before enabling the provider. Some services explicitly restrict automated access.
2. **Consent** -- The user explicitly approves every provider via a direct confirmation prompt (not through the AI).
3. **Rate limiting** -- All configurations include rate limits. Users should not increase them.
4. **Data handling** -- Users are responsible for how they use data from configured providers.
5. **Local laws** -- Users should verify that automated API access is lawful in their jurisdiction.

## Disclaimer

This document is an informational analysis, not legal advice. If you have specific legal concerns about using trvl, consult a qualified attorney in your jurisdiction.
