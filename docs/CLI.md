# trvl — CLI Reference

> Moved out of the README. The standalone CLI (56 commands), output formats, booking-link shape, and the full capability table.

## CLI Usage

trvl also works as a standalone CLI tool with 56 commands:

All search commands accept `--currency <CODE>` (e.g. `--currency EUR`) to convert displayed prices. trvl detects the actual API currency and converts at the display layer — no hardcoded currencies.

### Operational status

See which data providers are healthy, degraded, rate-limited, or circuit-broken at a glance. `trvl status` aggregates the local health log (`~/.trvl/health.jsonl`) into per-provider success rate, latency, and freshness, then overlays live circuit-breaker state. It reads only local files — no network calls, no credentials touched.

```bash
trvl status                 # table: every provider that has been called
trvl status --format json   # machine-readable, same data
```

When running as an HTTP server (`trvl mcp --http`), the same view is served as a read-only, auto-refreshing HTML dashboard at `/dashboard`. On a local (loopback) bind it needs no token — just open <http://127.0.0.1:8080/dashboard> in a browser. A non-loopback bind (`--host 0.0.0.0`) requires the server's bearer token, like every other remote route. AI assistants get the identical data via the `provider_health` MCP tool.

### Flights

```bash
$ trvl flights HEL NRT 2026-06-15

Found 86 flights (one_way)

| Price    | Duration | Stops   | Route                    | Airline               | Departs          |
+----------+----------+---------+--------------------------+-----------------------+------------------+
| EUR 603  | 24h 20m  | 2 stops | HEL -> CPH -> AUH -> NRT | Scandinavian Airlines | 2026-06-15T06:10 |
| EUR 656  | 24h 10m  | 2 stops | HEL -> CPH -> AUH -> NRT | Finnair               | 2026-06-15T06:20 |
| EUR 875  | 31h 20m  | 1 stop  | HEL -> IST -> NRT        | Turkish Airlines      | 2026-06-15T19:35 |
```

```bash
trvl flights JFK LHR 2026-07-01 --cabin business --stops nonstop
trvl flights AMS,EIN,ANR HEL,TKU,TLL 2026-06-15     # Multi-airport search
trvl flights HEL BCN 2026-07-01 --return 2026-07-08
trvl flights HEL NRT 2026-06-15 --format json       # JSON output
trvl flights HEL BCN 2026-07-01 --deep              # Budget-gated counterfactual fan-out
```

**`--deep` flag:** runs a budget-gated counterfactual fan-out after the primary search. It probes nearby departure airports, split-ticket options, and hidden-city routes via extra provider calls. The budget is best-effort and caps the total call count; if the budget runs out before all probes complete, the primary result is returned with a note. The flag never delays the primary search output.

**Price position:** when a route has enough price history, each result includes where the current fare sits in that route's observed range (low / typical / high) and a buy-or-wait verdict. Below the data floor the output says so and makes no trend claim. Available in `--format json` as `price_position` and in the MCP `search_flights` response.

**Call-free savings:** results include a "Savings you could capture" panel showing same-day cheaper fares, a vs-history comparison, and a shift-day option (departing a nearby date). These read from the persisted price calendar — no extra provider calls. Each carries an age label when the data is not live.

### Cheapest Dates

```bash
trvl dates HEL NRT --from 2026-06-01 --to 2026-06-30
trvl dates HEL BCN --from 2026-07-01 --to 2026-08-31 --duration 7 --round-trip
```

### Hotels

```bash
$ trvl hotels "Tokyo" --checkin 2026-06-15 --checkout 2026-06-18

Found 20 hotels:

| Name                              | Stars | Rating | Reviews | Price   |
+-----------------------------------+-------+--------+---------+---------+
| HOTEL MYSTAYS PREMIER Omori       | 4     | 4.1    | 2059    | 150 EUR |
| Hotel JAL City Tokyo Toyosu       | 4     | 4.2    | 1080    | 89 EUR  |
```

```bash
trvl hotels "Paris" --checkin 2026-07-01 --checkout 2026-07-05 --stars 4 --sort rating
trvl prices "<hotel_id>" --checkin 2026-06-15 --checkout 2026-06-18
trvl prices hold "<hotel_id>" --name "Hotel Lutetia Paris" --checkin 2026-06-15 --checkout 2026-06-18 --price 420 --currency EUR --refundable
trvl prices rebook <hold_id> --min-savings 25
trvl rooms "Hotel Lutetia Paris" --checkin 2026-06-15 --checkout 2026-06-18
```

**Price position:** `trvl prices` shows where the current rate sits in that property's observed price history (low / typical / high). Available in `--format json` as `price_position`.

**Booking readiness:** `trvl prices` and `trvl rooms` show a readiness verdict (ready / caution / unverified) composed from verified price, stable link, confirmed property identity, and known refundability. Any unknown signal downgrades the verdict conservatively. In `--format json` the fields are `booking_readiness` and `booking_readiness_reasons`.

### Islands and unindexed destinations

Small islands and out-of-the-way towns often have properties that Google Hotels never assigns a stable hotel ID. The usual `trvl rooms <name>` path needs that ID to resolve room-level data, so it can come up empty for exactly the places where a verified price matters most. Roberto Reale tested this on Ischia and reported the gap; the sequence below is what works there.

1. Discover what exists in the area:

   ```bash
   trvl hotels "Ischia, Italy" --checkin 2026-07-30 --checkout 2026-08-04 --format json
   ```

   This lists properties and, where Google exposes one, a place ID you can feed to `trvl prices`.

2. Try room-level data for a named property:

   ```bash
   trvl rooms "Hotel Continental Mare" --checkin 2026-07-30 --checkout 2026-08-04
   ```

   If the property has no Google Hotel ID, this no longer fails silently. It tells you so and points you at the paths that do resolve island stays, including the `trvl serpapi` command below.

3. Use `trvl serpapi` as the first tool for islands, not a last resort:

   ```bash
   trvl serpapi "Ischia, Italy" --checkin 2026-07-30 --checkout 2026-08-04 --currency EUR --adults 2
   ```

   For unindexed areas this is the most reliable verified-price path. It searches Google Hotels through SerpAPI, then fetches each top property's detail endpoint by `property_token` so the total is a per-provider quote rather than the list-level lead-in price. Each property carries a `price_verification` status, so verified and unverified results stay distinguishable. It needs a free `SERPAPI_KEY`; without one, trvl behaves exactly as before and this path stays off.

   Detail lookups cost one API call per property, so trvl bounds them with `--max-details` (default 8). Properties past that limit, and properties whose `property_token` returns no provider rows, are labelled unverified instead of being shown as a verified total.

4. Compare providers for a property that does have a Google place ID:

   ```bash
   trvl prices "PLACE_ID" --checkin 2026-07-30 --checkout 2026-08-04 --currency EUR
   ```

   When `SERPAPI_KEY` is set, this fetches the selected property's detail matrix and returns each OTA/provider row with a durable link, so the comparison survives the teaser price expiring at checkout.

Sequence tested and contributed by Roberto Reale. See `docs/PUBLIC_ARTICLE_FEEDBACK.md` for the underlying feedback and retest history.

### Explore Destinations

```bash
trvl explore HEL                                        # Cheapest destinations from Helsinki
trvl explore JFK --from 2026-07-01 --to 2026-07-14      # With dates
trvl explore AMS --currency EUR                         # Display prices in EUR
```

### Price Grid

```bash
trvl grid HEL NRT --depart-from 2026-07-01 --depart-to 2026-07-07 \
                   --return-from 2026-07-08 --return-to 2026-07-14
```

### Destination Info

```bash
trvl destination "Tokyo"                           # Weather, safety, holidays, currency
trvl destination "Barcelona" --dates 2026-07-01,2026-07-08
```

### Plan a Trip

Search flights and hotels in one command. Runs both searches in parallel and shows a cost summary.

```bash
trvl trip AMS PRG --depart 2026-06-15 --return 2026-06-18 --currency EUR
trvl trip JFK LHR --depart 2026-08-01 --return 2026-08-10 --guests 2
```

### Trip Cost

```bash
trvl trip-cost HEL BCN --depart 2026-07-01 --return 2026-07-08 --guests 2
```

### Weekend Getaway

```bash
trvl weekend HEL --month july-2026                 # Top 10 cheapest weekends
trvl weekend HEL --month july-2026 --budget 500    # Under EUR 500 total
```

### Smart Date Suggestions

```bash
trvl suggest HEL BCN --around 2026-07-15 --flex 7  # Best dates +/- 7 days
```

### Multi-City Optimizer

```bash
trvl multi-city HEL --visit BCN,ROM,PAR --dates 2026-07-01,2026-07-21
```

### Buses, Trains & Ferries

Searches 22 providers in parallel: FlixBus (buses, pan-European), RegioJet (buses+trains, CZ/SK/AT/HU/DE/PL), Eurostar/Snap (trains, London↔Paris/Brussels/Amsterdam/Cologne), Deutsche Bahn (trains, all European rail), ÖBB (shop/HAFAS API), NS (Dutch railways), VR (Finnish railways, via Digitransit API), SNCF (trains, French TGV/TER), Trainline (aggregated rail across major European operators), Transitous.org (transit routing, pan-European), Renfe (Spanish AVE high-speed API), Trenitalia (Italian high-speed + regional rail, lefrecce.it BFF), Italo (Italian high-speed rail, NTV — AGV/EVO), European Sleeper (night trains Brussels↔Berlin↔Prague + Amsterdam/Rotterdam/Antwerp/Dresden), Snälltåget (Swedish night trains Stockholm↔Malmö/Åre/Berlin, Sqills S3 API), Tallink (Baltic Sea ferries, live API), Viking Line (Baltic Sea ferries), Eckerö Line (Helsinki↔Tallinn, live Magento API), Finnlines (Helsinki↔Travemünde, Naantali↔Kapellskär, GraphQL API), Stena Line (North Sea + Baltic ferries), DFDS (North Sea + Baltic ferries, live availability API), and Ferryhopper (33 countries, 190+ ferry operators, via MCP). Airport transfers also add taxi fare estimates for door-to-door comparisons.

API routes are the default path. If you want trvl to try browser/curl/cookie-assisted fallbacks for protected providers such as SNCF or Trainline, pass `--allow-browser-fallbacks` (or set `TRVL_ALLOW_BROWSER_FALLBACKS=true`).

```bash
trvl ground Prague Vienna 2026-07-01                  # All 22 providers
trvl ground London Paris 2026-07-01                   # Eurostar + FlixBus + DB
trvl bus Prague Krakow 2026-07-01                     # Same command, bus alias
trvl train Prague Vienna 2026-07-01 --type train      # Trains only
trvl ground Prague Vienna 2026-07-01 --provider regiojet  # RegioJet only
trvl ground Vienna Salzburg 2026-07-01 --provider oebb    # ÖBB Railjet (EUR 38+)
trvl ground Helsinki Tampere 2026-07-01 --provider vr     # VR Finnish Railways (EUR 14+)
trvl ground Amsterdam Utrecht 2026-07-01 --provider ns    # NS Dutch Railways (EUR 5+)
trvl ground Paris Lyon 2026-07-01 --provider sncf         # SNCF TGV only
trvl ground Berlin Munich 2026-07-01 --provider db        # DB ICE (e.g. EUR 47.99)
trvl ground London Paris 2026-07-01 --provider trainline --allow-browser-fallbacks  # Trainline aggregated rail + optional protected fallback
trvl ground Madrid Barcelona 2026-07-01 --provider renfe   # Renfe AVE high-speed (EUR 36+)
trvl ground Prague Vienna 2026-07-01 --max-price 20       # Under EUR 20
trvl airport-transfer CDG "Hotel Lutetia Paris" 2026-07-01
trvl airport-transfer LHR "Paddington Station" 2026-07-01 --arrival-after 14:30
trvl airport-transfer CDG "Hotel Lutetia Paris" 2026-07-01 --provider taxi
```

### Multi-Modal Routing

Combines flights, trains, buses and ferries into optimal itineraries across all 22 providers. `--avoid` filters only the avoided mode, and `--depart-after` / `--arrive-by` are applied against the assembled itinerary times.

```bash
trvl route Helsinki Dubrovnik --arrive-by 2026-04-10     # Pareto-optimal itineraries
trvl route HEL TLL --arrive-by 2026-04-06               # Ferry + bus options
trvl route London Barcelona --arrive-by 2026-07-15       # Eurostar + TGV vs flight
```

### Price Watch

Track flight and hotel prices over time. Get alerts when prices drop below a threshold.

```bash
trvl watch add HEL BCN --depart 2026-07-01 --return 2026-07-08 --below 200
trvl watch add Prague --type hotel --depart 2026-07-01 --return 2026-07-02 --last-minute
trvl watch list                                       # Show all active watches
trvl watch check                                      # Check current prices
trvl watch daemon --every 6h                          # Keep checking on a schedule
trvl watch history <id>                               # Price history for a watch
trvl watch remove <id>                                # Remove a watch
```

### Travel Deals

Aggregates error fares, flash sales, and deals from 4 RSS feeds (Secret Flying, Fly4Free, Holiday Pirates, The Points Guy). Deals also appear automatically in flight search results when a matching deal is found.

```bash
trvl deals                                            # All recent deals
trvl deals --from HEL,AMS --max-price 400             # From my airports, under €400
trvl deals --from Helsinki,Amsterdam,Prague            # City names also accepted
trvl deals --type error_fare                           # Error fares only
```

### Proactive Nudges

`trvl nudges` reads your watches, price history, preferences, and trips and surfaces a list of grounded nudges. A nudge fires only when a real trigger exists: a price watch crossing its target, or a route sitting at a confident historic low. When nothing has triggered, the command prints nothing. Every nudge cites the record it came from. The command reads only `~/.trvl` and makes no network calls.

```bash
trvl nudges                   # Show grounded nudges (quiet when nothing triggered)
trvl nudges --format json     # Machine-readable output
```

### Travel Hacks

Runs 36 detectors in parallel and ranks savings opportunities. Pass `--return` for round-trip hacks. Add `--carry-on` to restrict hidden-city results to carry-on only.

```bash
trvl hacks HEL AMS 2026-04-13                         # One-way hacks
trvl hacks HEL AMS 2026-04-13 --return 2026-04-15 --carry-on  # Round-trip, carry-on
trvl hacks-accom Prague --checkin 2026-06-15 --checkout 2026-06-22  # Hotel split hacks
```

### Trip Persistence

Save and manage trips across sessions. Trips are stored in `~/.trvl/trips.json`.

```bash
trvl trips list                                       # List all saved trips
trvl trips show <id>                                  # Show trip details
trvl trips create "Helsinki → Prague → Amsterdam"     # Create a new trip
trvl trips add-leg <id> flight --from HEL --to PRG --date 2026-06-15
trvl trips book <id>                                  # Mark trip as booked
trvl trips delete <id>                                # Remove a trip
```

### User Preferences

Personal travel profile stored in `~/.trvl/preferences.json`. Drives real-time filtering: hotel results filtered by stars, rating, and neighborhood; hostels and airport hotels excluded; flight results filtered by budget and departure time window. Your AI assistant builds this profile automatically on first use (via `get_preferences` + `update_preferences` MCP tools).

```bash
trvl prefs                                            # Show current preferences
trvl prefs init                                       # Interactive setup wizard
trvl prefs edit                                       # Open in $EDITOR
```

Home airport and currency are auto-detected from your IP on first search. The AI assistant interviews you for the rest on first use. Examples of what the profile controls:

| Preference | What it does |
|---|---|
| `home_airports: ["HEL", "AMS"]` | Default origin for every search |
| `no_dormitories: true` | Drops hostels, capsule hotels, guesthouse rooms |
| `min_hotel_stars: 4` | Only 4-star+ hotels in results |
| `min_hotel_rating: 4.0` | Only well-reviewed properties (20+ reviews required) |
| `preferred_districts: {"Prague": ["Prague 1"]}` | Hotels in your favorite neighborhoods first |
| `carry_on_only: true` | Unlocks hidden-city and throwaway-ticket hacks |
| `prefer_direct: true` | Nonstop flights only |
| `budget_per_night_max: 150` | Hotel price cap passed to Google Hotels API |
| `budget_flight_max: 300` | Flights over budget dropped from results |
| `flight_time_earliest: "07:00"` | No 5am departures |
| `default_companions: 1` | Hotel searches default to 2 guests (you + companion) |
| `notes: "boutique hotels, no chains"` | Free-text — the AI applies these as soft filters |

Full profile reference: [AGENTS.md](AGENTS.md#step-6-build-the-traveller-profile)

### Points & Miles

Compare a redemption against paying cash, or list supported loyalty programs:

```bash
trvl points-value --cash 450 --points 20000 --program finnair-plus
trvl points-value --cash 300 --offer world-of-hyatt:12000 --offer hilton-honors:80000
trvl points-value --list
```

### Traveller Profile

Build a personal travel profile from your booking history. The profile learns your patterns — preferred airlines, favourite destinations, accommodation preferences, travel hacks — and uses them to skip redundant interview questions and personalize searches.

```bash
trvl profile                                          # Show your travel profile
trvl profile summary                                  # Quick stats
trvl profile add --type flight --provider KLM --ref ZHS9BM  # Add a booking manually
trvl profile import-email                             # Build profile from email history (LLM-assisted)
```

The profile stores:
- **Frequent flyer status**: Alliance tiers, programme memberships
- **Booking history**: Flights, hotels, Airbnb, ground transport, rides
- **Preferences**: Preferred airlines, apartment vs hotel, favourite neighbourhoods
- **Travel hacks used**: Fare tricks you've applied before
- **Family composition**: Travelling companions, unaccompanied minor experience
- **Seasonal patterns**: When you typically travel


## Booking links and verified hotel prices

Flight results and most hotel results include a `booking_url` — a direct link to Google Flights, Google Hotels, Booking.com, or another provider where you can continue manual booking:

```json
{
  "price": 113,
  "currency": "EUR",
  "airline": "Norwegian",
  "flight_number": "D8 2900",
  "booking_url": "https://www.google.com/travel/flights?q=Flights+to+BCN+from+HEL+on+2026-07-01"
}
```

The AI uses these to give you actionable handoff links. For accommodation decisions, use `search_accommodations` first. A hotel link alone is not a rate guarantee: verify shortlisted properties with `search_hotels_with_details`, `hotel_rooms`, or `trvl serpapi`, then rank on room-level totals or tax-inclusive provider totals when available. trvl never books, cancels, holds, or guarantees a rate automatically.

## At a Glance

| | |
|---|---|
| **Binary** | Single static ~15MB for API-first flows. Optional protected-provider fallbacks may use local browser/python tooling. |
| **Data** | Real-time from Google Flights (+ Kiwi, Ryanair, Wizz Air, easyJet, Vueling, Norwegian; Transavia/Skiplagged/AFKLM/Travelpayouts opt-in) + 6 hotel sources (Google Hotels, Trivago, Airbnb, Booking.com, Hostelworld, HomeToGo) + 22 ground providers (FlixBus, RegioJet, Eurostar, DB, ÖBB, NS, VR, SNCF, Trainline, Transitous, Renfe, Trenitalia, Italo, European Sleeper, Snälltåget, Tallink, Viking Line, Eckerö Line, Finnlines, Stena Line, DFDS, Ferryhopper) + free destination/enrichment APIs (weather, air quality, sun times, bike-share, holidays, currency) |
| **Auth** | No personal API keys required. Two providers (NS, Digitransit/VR) use public keys embedded in the binary. Optional browser/cookie fallbacks are available for protected providers when explicitly enabled. |
| **MCP** | Full v2025-11-25 — 1 smart MCP tool, 66 legacy-compatible capabilities (incl. 4 profile, 3 price-watch, provider-health, award sweet-spot capabilities), 7 prompts, resources, structured content, progress notifications, resource subscriptions, tool description orchestration |
| **CLI** | 56 commands (+ 7 watch subcommands) with table/JSON output, color, shell completion |
| **Booking links** | Flight and hotel results include manual handoff links; hotel search prices must be verified before treating them as final |
| **Travel hacks** | 36 detectors (throwaway, hidden-city, positioning, ferry, multi-modal, stopover, date-flex, error fare, back-to-back, rail competition, and more) |
| **Personal profile** | Learns from your booking history (email parsing + LLM). Remembers FF status, luggage needs, favourite properties, departure preferences, travel hacks used, accommodation preferences, family composition. Pre-search interviews skip questions the profile already answers. |
| **Output** | Pretty tables with color (default) or JSON (`--format json`) |
| **Platforms** | Linux, macOS (amd64, arm64). Windows CI in progress. |
| **Code** | Go codebase with CI race/coverage gates and a documented local test matrix in [docs/TESTING.md](docs/TESTING.md) |
| **License** | PolyForm Noncommercial 1.0 |

