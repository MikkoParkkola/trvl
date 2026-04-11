[![Go Report Card](https://goreportcard.com/badge/github.com/MikkoParkkola/trvl)](https://goreportcard.com/report/github.com/MikkoParkkola/trvl)
[![CI](https://github.com/MikkoParkkola/trvl/actions/workflows/ci.yaml/badge.svg)](https://github.com/MikkoParkkola/trvl/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/MikkoParkkola/trvl)](https://github.com/MikkoParkkola/trvl/releases)
[![Downloads](https://img.shields.io/github/downloads/MikkoParkkola/trvl/total)](https://github.com/MikkoParkkola/trvl/releases)
[![License](https://img.shields.io/badge/license-PolyForm%20NC%201.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/MikkoParkkola/trvl.svg)](https://pkg.go.dev/github.com/MikkoParkkola/trvl)
[![MCP](https://img.shields.io/badge/MCP-2025--11--25-blue)](https://modelcontextprotocol.io)
[![Providers](https://img.shields.io/badge/providers-17-brightgreen)](https://github.com/MikkoParkkola/trvl#providers)
[![Go Version](https://img.shields.io/github/go-mod/go-version/MikkoParkkola/trvl)](https://go.dev/)
[![SafeSkill 60/100](https://img.shields.io/badge/SafeSkill-60%2F100_Use%20with%20Caution-orange)](https://safeskill.dev/scan/mikkoparkkola-trvl)

# trvl — The Travel MCP Server

![trvl demo](https://raw.githubusercontent.com/MikkoParkkola/trvl/main/demo.gif?v=0.3.1)

> **33 travel tools for your AI assistant — flights, hotels, trains, buses, ferries, price alerts, travel hacks, weather forecasts, baggage rules, destination intel. Free. API-first.**
>
> Also works as a standalone CLI with 33 commands.

### What it looks like

> **You:** I have €300 and a free weekend. Surprise me.
>
> **Claude (with trvl):** 🎲 Checking cheapest destinations from Helsinki...
>
> **Dubrovnik, Croatia** 🇭🇷
> ```
> ✈️ Ryanair HEL→DBV Fri 14:25→17:10 (nonstop) — €167 RT
> 🏨 Old Town Studios, 4.6★ — €42/night × 2 = €84
> 🌡️ 26°C, sunny, Adriatic swimming
> 💰 Total: €251 (€49 under budget!)
>
> Optimizations applied:
>   Fly Friday not Saturday: -€48
>   Split airlines (Ryanair out, easyJet back): -€31
>
> 📊 Naive booking: €350 → Optimized: €251 → Saved: €99 (28%)
> ```
> Want me to check nearby restaurants or events that weekend?

trvl is an [MCP server](https://modelcontextprotocol.io/) + CLI that gives Claude, Cursor, Windsurf, and any MCP-compatible AI assistant direct access to Google Flights, Google Hotels, and European ground transport data. It searches, optimizes, and applies travel hacks automatically — no personal API keys required, no monthly fees, API-first by default, with optional browser-assisted fallbacks only for a few protected providers.

## Quick Setup (30 seconds)

### 1. Install

```bash
# Homebrew (macOS / Linux)
brew install MikkoParkkola/tap/trvl

# Or without Homebrew — download the binary directly
curl -fsSL https://github.com/MikkoParkkola/trvl/releases/latest/download/trvl_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C /usr/local/bin trvl
```

<details>
<summary>More install options</summary>

```bash
# Go
go install github.com/MikkoParkkola/trvl/cmd/trvl@latest

# Docker
docker run --rm ghcr.io/mikkoparkkola/trvl flights HEL NRT 2026-06-15

# Build from source
git clone https://github.com/MikkoParkkola/trvl.git && cd trvl && make build
```

</details>

### 2. Connect to your AI assistant

**One command, no JSON editing** — trvl installs itself into your MCP client's config:

```bash
trvl mcp install                       # Claude Desktop (default)
trvl mcp install --client cursor       # Cursor / Windsurf
trvl mcp install --client claude-code  # Claude Code
trvl mcp install --dry-run             # Preview first
```

Then restart your MCP client. That's it.

<details>
<summary>Or add it manually if you prefer</summary>

**Claude Code:**
```bash
claude mcp add trvl --transport stdio -- trvl mcp
```

**Claude Desktop** — add to `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "trvl": {
      "command": "trvl",
      "args": ["mcp"]
    }
  }
}
```

Config file locations:
- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Linux: `~/.config/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`

**Cursor / Windsurf / Other MCP clients** — add to your MCP config:
```json
{
  "mcpServers": {
    "trvl": {
      "command": "trvl",
      "args": ["mcp"]
    }
  }
}
```

</details>

### 3. (Optional) Teach your AI about trvl

Point your AI assistant to the reference docs so it knows all 32 tools:

```
https://raw.githubusercontent.com/MikkoParkkola/trvl/main/AGENTS.md
```

Or the compact version for context-limited models:

```
https://raw.githubusercontent.com/MikkoParkkola/trvl/main/llms.txt
```

For Claude Code, you can also install the bundled skill that teaches Claude how to use trvl optimally:

```bash
mkdir -p ~/.claude/skills
curl -fsSL "https://raw.githubusercontent.com/MikkoParkkola/trvl/main/.claude/skills/trvl.md" -o "$HOME/.claude/skills/trvl.md"
```

### 4. Ask your AI to search

That's it. Your AI assistant now has 33 travel tools available. Just ask naturally:

- *"Search flights from JFK to Tokyo on July 1st, business class"*
- *"Find hotels in Paris for July 1-5, at least 4 stars"*
- *"What's the cheapest day to fly Helsinki to Barcelona in August?"*
- *"Where can I fly cheaply from Helsinki this weekend?"*
- *"How much would a week in Barcelona cost — flights and hotel?"*
- *"When should I fly to London? Check dates around July 15th"*
- *"Plan a trip: Helsinki -> Barcelona -> Rome -> Paris, cheapest routing"*
- *"Search buses from Prague to Krakow on May 3rd"*
- *"Compare train and bus prices Prague to Vienna"*
- *"Search flights from Amsterdam, Eindhoven, or Antwerp to Helsinki or Tallinn"*
- *"Show me travel deals from Helsinki under €400"*
- *"Alert me when flights to Tokyo drop below €500"*

## MCP Tools

| Tool | What it does | Example |
|------|-------------|---------|
| **search_flights** | Search flights on a specific date | HEL -> NRT, 2026-06-15, business class, nonstop |
| **search_dates** | Find cheapest day to fly across a date range | HEL -> BCN, June-August 2026 |
| **search_hotels** | Search hotels in any city | Tokyo, June 15-18, 4+ stars |
| **hotel_prices** | Compare hotel prices from Google (aggregated from multiple providers) |
| **hotel_reviews** | Get reviews for a specific hotel | Top reviews, sorted by rating or recency |
| **hotel_rooms** | Fetch room-level availability, board, and cancellation details | Hotel place ID, Jul 1-5 |
| **destination_info** | Travel intelligence for any city | Tokyo: weather, safety, holidays, currency |
| **calculate_trip_cost** | Estimate total trip cost (flights + hotel) | HEL -> BCN, Jul 1-8, 2 guests |
| **weekend_getaway** | Find cheap weekend destinations | From HEL in July, budget EUR 500 |
| **suggest_dates** | Smart date suggestions around a target date | HEL -> BCN around Jul 15, +/- 7 days |
| **optimize_multi_city** | Find cheapest routing for multi-city trips | HEL -> BCN, ROM, PAR -> HEL |
| **nearby_places** | Find points of interest near a location | Restaurants, attractions near hotel |
| **travel_guide** | Wikivoyage travel guide for a city | Neighbourhoods, getting around, safety |
| **local_events** | Find events during your trip dates | Concerts, festivals, exhibitions |
| **search_ground** | Search buses, trains and ferries (16 providers, API-first with optional browser fallbacks) | Prague -> Vienna, May 3rd, trains only |
| **search_airport_transfers** | Search airport-to-hotel or airport-to-city ground transport, plus taxi estimates | CDG -> Hotel Lutetia Paris, after 14:30 |
| **search_restaurants** | Find restaurants near a location (Google Maps) | Barcelona, italian cuisine |
| **search_deals** | Travel deals from 4 RSS feeds (error fares, flash sales) | Deals from HEL under EUR 400 |
| **plan_trip** | Plan a complete trip — flights + hotel in one parallel search | AMS→PRG, Jun 15–18, EUR |
| **search_route** | Multi-modal routing combining flights, trains, buses and ferries | Helsinki → Dubrovnik, arrive by 2026-04-10 |
| **get_weather** | Get a weather forecast for any city (Open-Meteo, up to 14 days) | Prague, weekend forecast |
| **get_preferences** | Read user travel preferences (FF status, bag rules, seat preferences) | — |
| **detect_travel_hacks** | Run 18 parallel detectors for flight and ground savings opportunities | HEL → AMS, Apr 13, carry-on only |
| **detect_accommodation_hacks** | Find hotel split savings (e.g. 2-city stay cheaper than 1 hotel) | Prague, Jun 15-22 |
| **search_natural** | Natural language search using keyword heuristics — dispatches to the right tool automatically | "cheapest weekend in July from Helsinki" |
| **find_trip_window** | Find optimal travel windows by intersecting price calendars with your busy intervals (pass from your calendar tool) | "best week for Prague, May-Aug" |
| **list_trips** | List saved trips from ~/.trvl/trips.json | — |
| **get_trip** | Get details of a saved trip | Trip ID |
| **create_trip** | Create a new trip record | "Helsinki court + Prague + Amsterdam" |
| **add_trip_leg** | Add a flight, hotel, or ground leg to a saved trip | Trip ID, type, details |
| **mark_trip_booked** | Mark a trip leg as booked | Trip ID, leg index |
| **get_baggage_rules** | Look up carry-on and checked baggage allowances for airlines | AY carry-on + checked bag rules |

### MCP Protocol Features (v2025-11-25)

| Feature | Details |
|---------|---------|
| **Structured content** | Typed JSON (`structuredContent`) alongside human-readable summaries |
| **Content annotations** | `audience: ["user"]` for summaries, `audience: ["assistant"]` for data |
| **Output schemas** | Full JSON Schema validation for all 33 tool responses |
| **Prompts** | `plan-trip`, `find-cheapest-dates`, `compare-hotels`, `where-should-i-go` |
| **Resources** | Airport codes (50 major hubs), flight/hotel usage guides, price-watch subscriptions |
| **Tool description orchestration** | `find_trip_window` instructs the LLM to fetch calendar data first, then pass busy intervals in — works on every MCP client. See [docs/MCP-ORCHESTRATION.md](docs/MCP-ORCHESTRATION.md) |
| **Progress notifications** | Long-running searches stream progress tokens to the client |
| **Resource subscriptions** | Price-watch resources notify subscribers on price changes |
| **Progressive disclosure** | Suggestions for follow-up searches in every response |
| **Booking links** | Direct Google Flights/Hotels links in results |

## Travel Hack Detectors

`detect_travel_hacks` and `trvl hacks` run 18 detectors in parallel. Each one is independent and has a 20-second timeout:

| Detector | What it finds |
|----------|--------------|
| **throwaway** | Book a longer itinerary and skip the final leg (when the add-on is free) |
| **hidden_city** | Fly to a hub when a connecting flight through your real destination is cheaper |
| **positioning** | Fly from a nearby airport to unlock lower fares |
| **split** | Split one ticket into two one-ways across different airlines |
| **night_transport** | Take an overnight train/ferry to save a hotel night |
| **stopover** | Add a free multi-day stopover (Finnair/Icelandair/TAP/Turkish/Qatar/Emirates/Singapore/Etihad) |
| **date_flex** | Fly a day earlier or later for significant savings |
| **open_jaw** | Fly into one city and out of another |
| **ferry_positioning** | Take a ferry to a hub with cheaper flights (e.g. HEL→TLL ferry + TLL flight) |
| **multi_stop** | Break the journey into two cheaper segments |
| **currency_arbitrage** | Book in the destination currency to avoid dynamic pricing |
| **calendar_conflict** | Flag public holidays or peak seasons on your travel dates |
| **tuesday_booking** | Identify cheaper booking windows (off-peak weekdays) |
| **low_cost_carrier** | Find low-cost carrier alternatives not shown in aggregators |
| **multimodal_skip_flight** | Replace a short flight with a train or bus leg |
| **multimodal_positioning** | Ground transport to a hub + cheaper flight (train/ferry/bus) |
| **multimodal_open_jaw_ground** | Mix ground and air for open-jaw itineraries |
| **multimodal_return_split** | Different modes for outbound vs. return leg |

## Ground Transport Providers

trvl searches 16 ground transport providers in parallel, covering most of Europe. Airport transfers add taxi estimates on top of that, so trvl exposes 17 transport providers overall:

| Provider | Protocol | Coverage | Starting price | Auth |
|----------|----------|----------|----------------|------|
| **Eurostar** | GraphQL | London ↔ Paris/Brussels/Amsterdam/Cologne | GBP 39+ | Browser cookies (Datadome) |
| **Deutsche Bahn** | REST (Vendo) | All European rail connections | EUR 37+ | None |
| **ÖBB** | REST (shop + HAFAS) | Austrian Railjet + cross-border (AT/DE/HU/IT) | EUR 38+ | None |
| **VR (via Digitransit)** | GraphQL | Finnish rail network | EUR 14+ | Public API key (embedded) |
| **NS** | REST | Dutch rail network | EUR 5+ | Public subscription key (embedded) |
| **Renfe** | REST | Spanish AVE high-speed + regional | EUR 36+ | None |
| **SNCF** | REST | French TGV, TER, Intercity | Varies | None by default; optional browser/curl fallback |
| **Trainline** | REST | Aggregated: SNCF, DB, Eurostar, Trenitalia, … | Varies | None by default; optional browser/curl fallback |
| **FlixBus** | REST | Pan-European buses (40+ countries) | EUR 5+ | None |
| **RegioJet** | REST | CZ/SK/AT/HU/DE/PL buses + trains | EUR 5+ | None |
| **Transitous** | MOTIS2 REST | Pan-European transit (schedule-based fallback) | — | None |
| **Tallink** | REST API | Baltic Sea ferries (Helsinki, Tallinn, Stockholm, Riga) | EUR 16+ | None |
| **Viking Line** | Reference schedule | Baltic Sea ferries (Helsinki, Tallinn, Stockholm, Turku) | EUR 22+ | None |
| **Eckerö Line** | Magento AJAX API | Helsinki ↔ Tallinn (M/S Finlandia) | EUR 19+ | None |
| **Stena Line** | Reference schedule | North Sea + Baltic (Gothenburg, Kiel, Karlskrona, Gdynia, …) | EUR 25+ | None |
| **DFDS** | REST API | North Sea + Baltic (Kiel, Amsterdam, Newcastle, Copenhagen, …) | EUR 49+ | None |

Two providers (NS, Digitransit/VR) use public API keys that are embedded in the binary — no signup or personal key is required from the user.

## How trvl Compares

| Feature | trvl | fli | Google Flights | Skyscanner | Kiwi |
|---------|------|-----|---------------|------------|------|
| Flight search | ✅ | ✅ | ✅ | ✅ | ✅ |
| Bus/train/ferry search | ✅ (16 providers: FlixBus, RegioJet, Eurostar, DB, ÖBB, NS, VR, SNCF, Trainline, Transitous, Renfe, Tallink, Viking Line, Eckerö Line, Stena Line, DFDS) | ❌ | ❌ | ❌ | ❌ |
| Price tracking | ✅ (watches with alerts) | ❌ | ❌ | ❌ | ❌ |
| Hotel search | ✅ | ❌ | ❌ | ❌ | ❌ |
| Hotel reviews | ✅ | ❌ | ❌ | ❌ | ❌ |
| Trip cost calculator | ✅ | ❌ | ❌ | ❌ | ❌ |
| Explore destinations | ✅ | ❌ | ✅ (web only) | ✅ (web) | ✅ |
| Multi-city optimizer | ✅ | ❌ | ❌ | ❌ | ⚠️ (1.7★) |
| Destination intelligence | ✅ | ❌ | ❌ | ❌ | ❌ |
| Travel hacks (auto-applied) | ✅ | ❌ | ❌ | ❌ | ❌ |
| MCP server | ✅ | ✅ | ❌ | ❌ | ❌ |
| Personal profile | ✅ | ❌ | ❌ | ❌ | ❌ |
| CLI | ✅ | ✅ | ❌ | ❌ | ❌ |
| No API key needed | ✅ | ✅ | N/A | N/A | N/A |
| Single binary | ✅ (Go) | ❌ (Python) | N/A | N/A | N/A |

### mcp-gateway Integration

```yaml
backends:
  trvl:
    transport: stdio
    command: trvl mcp
```

## For AI Assistants

See [Quick Setup step 3](#3-optional-teach-your-ai-about-trvl) above for AGENTS.md and llms.txt links.

---

## CLI Usage

trvl also works as a standalone CLI tool with 33 commands:

All search commands accept `--currency <CODE>` (e.g. `--currency EUR`) to convert displayed prices. trvl detects the actual API currency and converts at the display layer — no hardcoded currencies.

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
```

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
trvl rooms "Hotel Lutetia Paris" --checkin 2026-06-15 --checkout 2026-06-18
```

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

### Buses & Trains

Searches 16 providers in parallel: FlixBus (buses, pan-European), RegioJet (buses+trains, CZ/SK/AT/HU/DE/PL), Eurostar/Snap (trains, London↔Paris/Brussels/Amsterdam/Cologne), Deutsche Bahn (trains, all European rail), ÖBB (shop/HAFAS API), NS (Dutch railways), VR (Finnish railways, via Digitransit API), SNCF (trains, French TGV/TER), Trainline (aggregated rail across major European operators), Transitous.org (transit routing, pan-European), Renfe (Spanish AVE high-speed API), Tallink (Baltic Sea ferries, live API), Viking Line (Baltic Sea ferries), Eckerö Line (Helsinki↔Tallinn, live Magento API), Stena Line (North Sea + Baltic ferries), and DFDS (North Sea + Baltic ferries, live availability API). Airport transfers also add taxi fare estimates for door-to-door comparisons.

API routes are the default path. If you want trvl to try browser/curl/cookie-assisted fallbacks for protected providers such as SNCF or Trainline, pass `--allow-browser-fallbacks` (or set `TRVL_ALLOW_BROWSER_FALLBACKS=true`).

```bash
trvl ground Prague Vienna 2026-07-01                  # All 16 providers
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

Combines flights, trains, buses and ferries into optimal itineraries across all 16 providers. `--avoid` filters only the avoided mode, and `--depart-after` / `--arrive-by` are applied against the assembled itinerary times.

```bash
trvl route Helsinki Dubrovnik --arrive-by 2026-04-10     # Pareto-optimal itineraries
trvl route HEL TLL --arrive-by 2026-04-06               # Ferry + bus options
trvl route London Barcelona --arrive-by 2026-07-15       # Eurostar + TGV vs flight
```

### Price Watch

Track flight and hotel prices over time. Get alerts when prices drop below a threshold.

```bash
trvl watch add HEL BCN --depart 2026-07-01 --return 2026-07-08 --below 200
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

### Travel Hacks

Runs 18 detectors in parallel and ranks savings opportunities. Pass `--return` for round-trip hacks. Add `--carry-on` to restrict hidden-city results to carry-on only.

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

## How It Works

Google's travel frontend uses an internal gRPC-over-HTTP protocol called **batchexecute**. `trvl` speaks this protocol natively:

1. **Chrome TLS fingerprint** — [utls](https://github.com/refraction-networking/utls) impersonates Chrome's exact TLS ClientHello
2. **Flights** — `FlightsFrontendService/GetShoppingResults` with encoded filter arrays
3. **Hotels** — `TravelFrontendUi` embedded JSON parsing from `AF_initDataCallback` blocks
4. **Hotel prices** — `TravelFrontendUi/data/batchexecute` with rpcid `yY52ce`
5. **Explore** — `GetExploreDestinations` for destination discovery
6. **Destination info** — Parallel aggregation of 5 free APIs (Open-Meteo, REST Countries, Nager.Date, travel-advisory.info, ExchangeRate-API)
7. **Buses** — FlixBus public API (`global.api.flixbus.com`) with city autocomplete + search
8. **Trains (RegioJet)** — RegioJet public API (`brn-ybus-pubapi.sa.cz`) with route search + pricing
9. **Trains (Eurostar)** — `site-api.eurostar.com/gateway` GraphQL for London↔Paris/Brussels/Amsterdam/Cologne
10. **Trains (Deutsche Bahn)** — DB Vendo API (`int.bahn.de/web/api`) for all European rail connections
11. **Trains (ÖBB)** — Austrian Federal Railways via shop/HAFAS APIs; live Railjet fares (EUR 38+)
12. **Trains (NS)** — NS Dutch Railways public API (`gateway.apiportal.ns.nl`) with embedded subscription key
13. **Trains (VR)** — Finnish Railways via Digitransit GraphQL API (`api.digitransit.fi`); fixed fares from Matka.fi
14. **Trains (SNCF)** — SNCF Connect API for French TGV, TER, and Intercity routes; optional browser/curl fallback for protected sessions
15. **Trains (Trainline)** — Trainline aggregated rail API across major European operators; optional browser/curl fallback for protected sessions
16. **Transit (Transitous)** — `routing.spicebus.org` MOTIS2 API for pan-European transit routing
17. **Trains (Renfe)** — Spanish AVE high-speed and regional rail via REST API; fares EUR 36+
18. **Rate limiting** — per-provider token buckets (10 req/s FlixBus/RegioJet; 1 req/2s DB; 1 req/6s SNCF/Transitous; 1 req/20s Eurostar) with exponential backoff on 429/5xx

Most providers use pure HTTP/JSON APIs. Optional browser/curl-assisted fallbacks exist only for protected providers that sometimes require live cookies or verification (currently SNCF and Trainline); the default path stays API-first.

## Every Result is Bookable

Every flight and hotel result includes a `booking_url` — a direct link to Google Flights or Google Hotels where you can complete the booking:

```json
{
  "price": 113,
  "currency": "EUR",
  "airline": "Norwegian",
  "flight_number": "D8 2900",
  "booking_url": "https://www.google.com/travel/flights?q=Flights+to+BCN+from+HEL+on+2026-07-01"
}
```

The AI uses these to give you actionable recommendations: "Book here: [link]". No copying flight numbers into another search engine.

## At a Glance

| | |
|---|---|
| **Binary** | Single static ~15MB for API-first flows. Optional protected-provider fallbacks may use local browser/python tooling. |
| **Data** | Real-time from Google Flights/Hotels/Explore/Maps + 16 ground providers (FlixBus, RegioJet, Eurostar, DB, ÖBB, NS, VR, SNCF, Trainline, Transitous, Renfe, Tallink, Viking Line, Eckerö Line, Stena Line, DFDS) + 5 free destination APIs |
| **Auth** | No personal API keys required. Two providers (NS, Digitransit/VR) use public keys embedded in the binary. Optional browser/cookie fallbacks are available for protected providers when explicitly enabled. |
| **MCP** | Full v2025-11-25 — 33 tools, 4 prompts, resources, structured content, progress notifications, resource subscriptions, tool description orchestration |
| **CLI** | 33 commands (+ 6 watch subcommands) with table/JSON output, color, shell completion |
| **Booking links** | Every flight and hotel result includes a direct Google booking link |
| **Travel hacks** | 18 detectors (throwaway, hidden-city, positioning, ferry, multi-modal, stopover, date-flex, and more) |
| **Personal profile** | Remembers your FF status, luggage needs, favourite hotels, departure preferences |
| **Output** | Pretty tables with color (default) or JSON (`--format json`) |
| **Platforms** | Linux, macOS (amd64, arm64). Windows CI in progress. |
| **Code** | 145 Go files, ~44K LOC, 16 packages, 1100+ tests |
| **License** | PolyForm Noncommercial 1.0 |

## Attribution

Built on the shoulders of:

- **[fli](https://github.com/punitarani/fli)** by Punit Arani — the original Google Flights reverse-engineering library
- **[utls](https://github.com/refraction-networking/utls)** — Chrome TLS fingerprint impersonation
- **[icecreamsoft](https://icecreamsoft.hashnode.dev/building-a-web-app-for-travel-search)** — Google Hotels batchexecute documentation
- **[SerpAPI](https://serpapi.com/google-hotels-api)** — Hotel parameter reference

## Legal

`trvl` accesses Google's public-facing internal APIs. It does not bypass authentication, access protected content, or circumvent rate limits. Same approach as [fli](https://github.com/punitarani/fli) (1K+ stars, MIT licensed).

## License

[PolyForm Noncommercial 1.0.0](LICENSE) — free for personal and noncommercial use. Commercial use requires a separate license.
