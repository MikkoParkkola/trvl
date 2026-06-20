[![Go Report Card](https://goreportcard.com/badge/github.com/MikkoParkkola/trvl)](https://goreportcard.com/report/github.com/MikkoParkkola/trvl)
[![CI](https://github.com/MikkoParkkola/trvl/actions/workflows/ci.yaml/badge.svg)](https://github.com/MikkoParkkola/trvl/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/MikkoParkkola/trvl)](https://github.com/MikkoParkkola/trvl/releases)
[![Downloads](https://img.shields.io/github/downloads/MikkoParkkola/trvl/total)](https://github.com/MikkoParkkola/trvl/releases)
[![License](https://img.shields.io/badge/license-PolyForm%20NC%201.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/MikkoParkkola/trvl.svg)](https://pkg.go.dev/github.com/MikkoParkkola/trvl)
[![MCP](https://img.shields.io/badge/MCP-2025--11--25-blue)](https://modelcontextprotocol.io)
[![Providers](https://img.shields.io/badge/providers-22-brightgreen)](https://github.com/MikkoParkkola/trvl#providers)
[![Go Version](https://img.shields.io/github/go-mod/go-version/MikkoParkkola/trvl)](https://go.dev/)
[![Install in VS Code](https://img.shields.io/badge/VS_Code-Install_MCP-0078d4?logo=visualstudiocode)](https://insiders.vscode.dev/redirect/mcp/install?name=trvl&config=%7B%22command%22%3A%22trvl%22%2C%22args%22%3A%5B%22mcp%22%5D%7D)
[![Install in Cursor](https://img.shields.io/badge/Cursor-Install_MCP-black?logo=cursor)](cursor://anysphere.cursor-deeplink/mcp/install?name=trvl&config=%7B%22command%22%3A%22trvl%22%2C%22args%22%3A%5B%22mcp%22%5D%7D)
[![Live Demo](https://img.shields.io/badge/Live_Demo-Try_in_browser-14b8a6?logo=googlechrome&logoColor=white)](https://socialistic.ai/en/skill/trvl-travel-mcp-4f7aa7)

# trvl — The Travel MCP Server

![trvl demo](https://raw.githubusercontent.com/MikkoParkkola/trvl/main/demo.gif?v=1.10.0)

Asciinema source: [`demo.cast`](demo.cast) · Demo script and claims: [`docs/DEMO.md`](docs/DEMO.md)

**▶ Try it live in your browser** — run trvl as a hosted MCP demo, no install (sign in to run): **[socialistic.ai/trvl-travel-mcp](https://socialistic.ai/en/skill/trvl-travel-mcp-4f7aa7)** (community-hosted by [@shesl-tinkerland](https://github.com/shesl-tinkerland)).

> **The canonical travel MCP for AI assistants. 1 smart tool. 66 compatibility aliases. 22 providers. Zero API keys. One binary.**
>
> Gives Claude, Cursor, Windsurf, Codex, and any MCP-compatible AI 1 smart MCP tool for your AI assistant — the `travel` router — with 66 compatibility aliases for flights, hotels, rental cars, trains, buses, ferries, price alerts, award sweet spots, weather, baggage, lounges, and destination intel. Free. API-first. Also works as a standalone CLI with 56 commands.
>
> **Token-efficient by design:** advertising 1 smart tool instead of 66 keeps the `tools/list` payload at ~378 tokens instead of ~33,500 — a **98.9% smaller context footprint**, ~33K tokens handed back to your assistant. The 66 aliases stay callable via the `intent` field (set `TRVL_MCP_TOOL_MODE=legacy` to advertise all 66).

**For**: AI-assistant users who book ≥4 trips/yr · AI-app builders integrating travel intent · devs shopping MCP registries.
**Not for**: humans booking via a website (use Google Flights) · travel-agency SaaS shoppers (we are not a hosted product).

→ Full positioning: [`docs/POSITIONING.md`](docs/POSITIONING.md) · Comparison: [`docs/COMPARISON.md`](docs/COMPARISON.md) · Architecture: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) · Traveller Workspace: [`docs/traveller-workspace.md`](docs/traveller-workspace.md) · Tools: [`AGENTS.md`](AGENTS.md)

## Available on

Listed across the MCP ecosystem: [Glama](https://glama.ai/mcp/servers/MikkoParkkola/trvl) · [LobeHub](https://lobehub.com/mcp/mikkoparkkola-trvl) · [Smithery](https://smithery.ai/server/@MikkoParkkola/trvl) · [MCPHub](https://www.mcphub.com/mcp-servers/MikkoParkkola/trvl) · [Cursor Directory](https://cursor.directory/mcp/trvl) · [PulseMCP](https://www.pulsemcp.com/servers/mikkoparkkola-trvl) · [Agent Index](https://agentindex.app/en/tool/mikkoparkkola-trvl) · [MCP Market](https://mcpmarket.com/server/trvl) · [Fleur](https://fleurmcp.com/mcp/trvl) · [MagicSlides](https://www.magicslides.app/mcps/mikkoparkkola-trvl) · [pkg.go.dev](https://pkg.go.dev/github.com/MikkoParkkola/trvl)

**Independent coverage:** Roberto Reale, [Budget Travel Pipeline](https://blog-roberto-reale.vercel.app) (3-part series) — an independent build-and-test of a flights + accommodation agent pipeline on trvl, with a hotel-price verification deep-dive. His testing surfaced real fixes (v1.9.1–v1.9.2) and shaped the v1.10 trust roadmap.

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

## Why trust it

An AI agent acts on trvl's output without a human checking every result, so the bar is correctness, not just coverage. trvl is built for that:

- **It tells you when it can't.** A blocked or rate-limited provider returns a typed status (`AKAMAI_BLOCK`, `RATE_LIMITED`, `BOOKING_COOKIES_MISSING`) with a fix hint, instead of an empty result that looks like "nothing found." Estimated or composed values are labelled; currency-mismatched totals are skipped rather than faked.
- **Tested more than it is written.** There is more test code than source, thousands of tests, race-checked on macOS, Linux, and Windows. A smoke gate runs the packaged binary before any release publishes, so a build that doesn't run can't ship.
- **It degrades gracefully.** Providers run concurrently with per-provider timeouts. One source failing returns partial results instead of aborting the whole search.
- **It's observable.** Run `trvl status` (or open the local `/dashboard` when running `trvl mcp --http`) to see per-provider success rate, latency, freshness, and circuit-breaker state.

## First 5 prompts to try

Copy these into Claude, Cursor, Windsurf, Codex, or any MCP client after `trvl mcp install`:

```text
I have a free long weekend from July 1-5, 2026. Find the cheapest realistic trip from Helsinki with nonstop flights and hotels near the center.
```

```text
I have 80,000 Amex Membership Rewards points and 20,000 Virgin points. Compare award sweet spots from HEL to LHR in business class on August 15, 2026.
```

```text
Check whether AMS -> HEL -> RIX on July 1, 2026 is a sensible hidden-city option if I only have carry-on luggage, and compare it against a EUR 150 direct baseline.
```

```text
Find 3+ star hotels within 5 km of ExCeL London for July 1-4, 2026 for two guests, sorted by cheapest first.
```

```text
Create a mistake-fare watch for HEL -> BCN, July 1-8, 2026, and alert me if it drops below EUR 90.
```

trvl is an [MCP server](https://modelcontextprotocol.io/) + CLI that gives Claude, Cursor, Windsurf, and any MCP-compatible AI assistant direct access to Google Flights, Google Hotels, Trivago, Airbnb, Booking.com, Hostelworld, Ferryhopper, and European ground transport data. It searches, optimizes, and applies travel hacks automatically — no personal API keys required, no monthly fees, API-first by default, with optional browser-assisted fallbacks only for a few protected providers.

## Hotel price trust model

Hotel search has three trust levels:

1. **Accommodation decisions:** use `search_accommodations` when a traveller asks where to stay. It starts from the requested room/apartment need, verifies room-level offers for shortlisted candidates, and returns only criteria-matched offers in the ranked `offers` list.
2. **Discovery:** `search_hotels` and `trvl hotels` find candidate properties quickly across Google Hotels, Trivago, Airbnb, Booking.com, Hostelworld, HomeToGo, and configured external providers. Treat these prices as lead-in search prices, not checkout-final quotes.
3. **Verification drill-down:** use `search_hotels_with_details`, `hotel_rooms`, or `trvl serpapi` (SerpAPI key optional) for explicit room/provider detail checks. Rank final trip costs on room-level `total_price` or tax-inclusive provider totals when present, and surface cancellation, board, and taxes/fees separately.

This matters because public hotel metasearch exposes list-level rates first. Some are real selected-provider prices; others only become trustworthy after the property detail page exposes the OTA/room matrix, taxes/fees, and cancellation terms. trvl provides booking/provider links for manual handoff, but it does not book, hold, lock, or guarantee a hotel rate. Public article feedback and caveats are tracked in [`docs/PUBLIC_ARTICLE_FEEDBACK.md`](docs/PUBLIC_ARTICLE_FEEDBACK.md).

## Setup

**One command — your AI does everything:**

> Read https://raw.githubusercontent.com/MikkoParkkola/trvl/main/AGENTS.md and set up trvl

That's it. Your AI will install the binary, wire the MCP server, install the skill, build your travel profile, and verify everything works. Takes under a minute. Works in Claude Code, Cursor, Windsurf, Codex, and any AI with terminal access.

**Or tell your AI assistant:**

> Read https://github.com/MikkoParkkola/trvl and install trvl as my travel MCP server

**Or install manually:**

```bash
brew install MikkoParkkola/tap/trvl   # install
trvl mcp install                       # wire to Claude Desktop (default)
```

`trvl mcp install` auto-detects your AI client. Specify one with `--client`:

```bash
trvl mcp install --client claude-code  # Claude Code
trvl mcp install --client cursor       # Cursor
trvl mcp install --client windsurf     # Windsurf
trvl mcp install --client codex        # OpenAI Codex CLI
trvl mcp install --client vscode       # VS Code Copilot
trvl mcp install --client zed          # Zed
trvl mcp install --client --list       # show all 10 supported clients
```

Also supported: `gemini`, `amazon-q`, `lm-studio`. Restart your client after install.

<details>
<summary>More install options</summary>

```bash
# Direct binary (no Homebrew)
curl -fsSL https://github.com/MikkoParkkola/trvl/releases/latest/download/trvl_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C /usr/local/bin trvl

# Go
go install github.com/MikkoParkkola/trvl/cmd/trvl@latest

# Docker
docker run --rm ghcr.io/mikkoparkkola/trvl flights HEL NRT 2026-06-15

# Build from source
git clone https://github.com/MikkoParkkola/trvl.git && cd trvl && make build

# Claude Code CLI
claude mcp add trvl --transport stdio -- trvl mcp

# Manual JSON (Claude Desktop, Cursor, Windsurf, etc.)
# Add to your MCP client config:
# { "mcpServers": { "trvl": { "command": "trvl", "args": ["mcp"] } } }
```

</details>

### 3. (Optional) Teach your AI about trvl

If you used the one-command setup above, the skill is already installed. Otherwise, install the bundled skill that teaches Claude how to use trvl optimally, plus the provider-helper skill for safe optional provider setup:

```bash
# Install the bundled skills (Claude Code)
mkdir -p ~/.claude/skills
for s in trvl providers; do
  curl -fsSL "https://raw.githubusercontent.com/MikkoParkkola/trvl/main/.claude/skills/$s.md" -o "$HOME/.claude/skills/$s.md"
done
```

Reference docs for other clients: [AGENTS.md](https://raw.githubusercontent.com/MikkoParkkola/trvl/main/AGENTS.md) (full) · [llms.txt](https://raw.githubusercontent.com/MikkoParkkola/trvl/main/llms.txt) (compact)

### 4. Build your travel profile

trvl learns how you travel. On first use, the AI runs a short interview (4 phases, ~2 minutes) to understand your home airports, travel style, favourite cities, and booking strategies. It can also scan your email for past bookings.

```
Tell trvl to build my travel profile
```

The profile makes every subsequent search smarter — it skips questions it already knows the answer to, applies your preferences automatically, and suggests destinations based on your patterns.

### 5. Ask your AI to search

That's it. Your AI assistant now has 1 smart travel tool available. The old 66 tool names remain callable as compatibility aliases, and `TRVL_MCP_TOOL_MODE=legacy` restores the old advertised list for clients that require it. Just ask naturally:

- *"Search flights from JFK to Tokyo on July 1st, business class"*
- *"Find hotels in Paris for July 1-5, at least 4 stars"*
- *"What's the cheapest day to fly Helsinki to Barcelona in August?"*
- *"Where can I fly cheaply from Helsinki this weekend?"*
- *"How much would a week in Barcelona cost — flights and hotel?"*
- *"When should I fly to London? Check dates around July 15th"*
- *"Plan a trip: Helsinki -> Barcelona -> Rome -> Paris, cheapest routing"*
- *"Search buses from Prague to Krakow on May 3rd"*
- *"Compare train and bus prices Prague to Vienna"*
- *"Search rental cars at HEL from July 1-4 for three travellers"*
- *"Search flights from Amsterdam, Eindhoven, or Antwerp to Helsinki or Tallinn"*
- *"Show me travel deals from Helsinki under €400"*
- *"Alert me when flights to Tokyo drop below €500"*
- *"Import this booking confirmation into my Japan trip, then check whether the hotel candidate is still booking-ready"*

## MCP Tool + Compatibility Aliases

| Tool | What it does | Example |
|------|-------------|---------|
| **travel** | Smart MCP router for natural or structured requests; forwards to the right compatibility alias | "find hotels in Tokyo" / `intent=search_flights` |
| **search_flights** | Search flights on a specific date | HEL -> NRT, 2026-06-15, business class, nonstop |
| **search_dates** | Find cheapest day to fly across a date range | HEL -> BCN, June-August 2026 |
| **search_accommodations** | Traveller-first room/apartment search; verifies matched offers before ranking | Paris, Jul 1-5, apartment with kitchen, refundable |
| **search_hotels** | Discover hotel candidates in any city; prices are lead-in until verified | Tokyo, June 15-18, 4+ stars |
| **search_hotels_with_details** | Search hotels and verify top picks with rooms, rates, cancellation, board, fees, and amenities | Paris, Jul 1-5, top 3 detailed |
| **hotel_prices** | Compare exposed Google booking-provider prices for a hotel; use room/detail totals for final booking decisions |
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
| **search_ground** | Search buses, trains and ferries (20 providers, API-first with optional browser fallbacks) | Prague -> Vienna, May 3rd, trains only |
| **search_airport_transfers** | Door-to-door comparison card: every mode (transit, airport express, taxi, ride-hail) with time, price, pros/cons, grounded steps, and cheapest/fastest/best-value labels | CDG -> Hotel Lutetia Paris, after 14:30 |
| **plan_journey** | Leave-By Scheduler: when to leave home to reach the gate comfortably (check-in + security buffer, conservative, never optimistic). Pass `origin` to auto-stitch the home→airport leg; `as_ics` returns a calendar event with a leave-home alarm | "when do I leave for my 09:40 HEL flight?" |
| **search_cars** | Search rental cars with provider statuses when optional credentials are missing | HEL, Jul 1-4, compact |
| **search_restaurants** | Find restaurants near a location (Google Maps) | Barcelona, italian cuisine |
| **search_deals** | Travel deals from 4 RSS feeds (error fares, flash sales) | Deals from HEL under EUR 400 |
| **plan_trip** | Plan a complete trip — flights + hotel in one parallel search | AMS→PRG, Jun 15–18, EUR |
| **search_route** | Multi-modal routing combining flights, trains, buses and ferries | Helsinki → Dubrovnik, arrive by 2026-04-10 |
| **get_weather** | Get a weather forecast for any city (Open-Meteo, up to 14 days) | Prague, weekend forecast |
| **get_preferences** | Read user travel preferences (FF status, bag rules, seat preferences) | — |
| **provider_health** | Diagnose configured providers with success rate, freshness, result counts, error class, circuit state, next retry, and fix hints | "why is Booking.com failing?" |
| **detect_travel_hacks** | Run 18 parallel detectors for flight and ground savings opportunities | HEL → AMS, Apr 13, carry-on only |
| **detect_accommodation_hacks** | Find hotel split savings (e.g. 2-city stay cheaper than 1 hotel) | Prague, Jun 15-22 |
| **search_natural** | Natural language search using keyword heuristics — dispatches to the right tool automatically | "cheapest weekend in July from Helsinki" |
| **find_trip_window** | Find optimal travel windows by intersecting price calendars with your busy intervals (pass from your calendar tool) | "best week for Prague, May-Aug" |
| **search_lounges** | Find airport lounges, access rules, and card/status eligibility | HEL lounges with Priority Pass or Oneworld status |
| **check_visa** | Check visa and entry requirements for a passport→destination country pair | FI passport → TH |
| **calculate_points_value** | Compare cash price vs points required for a redemption | EUR 450 vs 20k Finnair Plus points |
| **search_awards** | Rank award-seat sweet spots across points balances and transfer partners | VS seat + MR/UR/Bilt balances |
| **list_trips** | List saved trips from ~/.trvl/trips.json | — |
| **get_trip** | Get details of a saved trip | Trip ID |
| **create_trip** | Create a new trip record | "Helsinki court + Prague + Amsterdam" |
| **add_trip_leg** | Add a flight, hotel, or ground leg to a saved trip | Trip ID, type, details |
| **mark_trip_booked** | Mark a trip leg as booked | Trip ID, leg index |
| **trip_workspace** | Import confirmations, export workspaces, save booking candidates, run itinerary checks, and get fare intelligence | `action=import_reservation` / `action=booking_ready` |
| **get_baggage_rules** | Look up carry-on and checked baggage allowances for airlines | AY carry-on + checked bag rules |
| **build_profile** | Build a traveller profile from booking history (email parsing + LLM) | Scans emails for past bookings |
| **add_booking** | Add a booking to the traveller profile | Flight, hotel, ground, or ride |
| **interview_trip** | Generate pre-search interview questions based on profile knowledge | Skip questions the profile already answers |

## Search Filters

### Flight Filters (`search_flights`)

| Filter | Parameter | Notes |
|--------|-----------|-------|
| Cabin class | `cabin_class` | `economy`, `premium_economy`, `business`, `first` |
| Max stops | `max_stops` | `nonstop`, `one_stop`, `two_plus`, or `any` |
| Alliance | `alliances` | `STAR_ALLIANCE`, `ONEWORLD`, `SKYTEAM` — server-side |
| Departure time window | `depart_after` / `depart_before` | `HH:MM` format — server-side |
| Lower emissions | `less_emissions` | Only flights with below-average CO2 — server-side |
| Carry-on bags | `carry_on_bags` | Require N carry-on bags included — server-side price recalculation |
| Checked bags | `checked_bags` | **Hidden Google feature** — require N checked bags included, server-side. Google's own UI only exposes carry-on; trvl also wires the checked-bag slot in the same filter array. |
| Require checked bag | `require_checked_bag` | Client-side post-filter: drops any flight without ≥1 free checked bag in the parsed response |
| Max price | `max_price` | Integer, whole currency units — server-side |
| Max duration | `max_duration` | Minutes — server-side |
| Exclude basic economy | `exclude_basic` | Drops BE fares — server-side |
| Sort | `sort_by` | `cheapest`, `duration`, `departure`, `arrival` |
| Airlines | `airlines` | Comma-separated IATA codes (e.g. `AY,LH`) |

### Accommodation Filters (`search_accommodations`, `search_hotels`)

| Filter | Parameter | Notes |
|--------|-----------|-------|
| Free cancellation | `free_cancellation` | `?fc=1` server-side Google Hotels param |
| Property type | `property_type` | `hotel`, `apartment`, `hostel`, `resort`, `bnb`, `villa` — server-side `?ptype=N` |
| Brand / chain | `brand` | Case-insensitive substring match (e.g. `hilton`, `marriott`) — client-side |
| Star rating | `stars` | Minimum 1-5 — server-side `?class=N` |
| Guest rating | `min_rating` | e.g. `4.0` — server-side `?rating=N` and client-side guard |
| Distance from center | `max_distance` | Kilometres — server-side `?lrad=N` (metres) |
| Amenities | `amenities` | Comma-separated required amenities — client-side |
| Price range | `min_price` / `max_price` | Per night — server-side `?min_price` / `?max_price` and client-side guard |
| Sort | `sort` | `price`, `rating`, `distance`, `stars` |

### Ground Transport Filters (`search_ground`)

| Filter | Parameter | Notes |
|--------|-----------|-------|
| Mode | `type` | `bus`, `train`, `ferry` — client-side |
| Max price | `max_price` | Currency units — client-side |
| Provider | `provider` | Restrict to one provider (e.g. `flixbus`, `db`, `regiojet`) |

### Rental Car Filters (`search_cars`)

| Filter | Parameter | Notes |
|--------|-----------|-------|
| Dropoff location | `dropoff_location` | Defaults to `pickup_location` |
| Pickup/dropoff time | `pickup_time` / `dropoff_time` | Local `HH:MM`, default `10:00` |
| Traveller count | `passengers` | Defaults from profile companions + user where available |
| Driver age | `driver_age` | Default 30 |
| Vehicle class | `vehicle_class` | `economy`, `compact`, `SUV`, `van`, etc. |
| Max price | `max_price` | Total rental price filter |
| Provider | `provider` | `skyscanner` when `SKYSCANNER_API_KEY` is configured; otherwise a typed setup status is returned |

> **Unique feature:** The `checked_bags` filter on `search_flights` directly sets the checked-bags slot in Google's internal `batchexecute` filter array — the same wire position as carry-on bags. Google's own Flights UI only exposes the carry-on filter; the checked-bag slot works server-side but is undocumented and not surfaced in the UI. trvl is the only client that exposes it.

### MCP Protocol Features (v2025-11-25)

| Feature | Details |
|---------|---------|
| **Structured content** | Typed JSON (`structuredContent`) alongside human-readable summaries |
| **Content annotations** | `audience: ["user"]` for summaries, `audience: ["assistant"]` for data |
| **Output schemas** | Full JSON Schema validation for the `travel` smart router and all 66 compatibility tool responses |
| **Prompts** | `plan-trip`, `find-cheapest-dates`, `compare-hotels`, `where-should-i-go` |
| **Resources** | Airport codes (50 major hubs), flight/hotel usage guides, price-watch subscriptions |
| **Tool description orchestration** | `find_trip_window` instructs the LLM to fetch calendar data first, then pass busy intervals in — works on every MCP client. See [docs/MCP-ORCHESTRATION.md](docs/MCP-ORCHESTRATION.md) |
| **Progress notifications** | Long-running searches stream progress tokens to the client |
| **Resource subscriptions** | Price-watch resources notify subscribers on price changes |
| **Progressive disclosure** | Suggestions for follow-up searches in every response |
| **Booking links** | Direct Google Flights/Hotels links in results |

## Travel Hack Detectors

`detect_travel_hacks` and `trvl hacks` run 37 detectors in parallel. Each one is independent and has a 20-second timeout:

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
| **advance_purchase** | Identify routes where booking 14-21+ days ahead drops the fare significantly |
| **group_split** | Split a group across separate bookings for lower per-person fares |
| **rail_fly_arbitrage** | Train to a hub + flight cheaper than direct flight |
| **fare_breakpoint** | Move date by 1-2 days to cross a fare-class boundary |
| **destination_airport** | Fly to a secondary airport in the same city |
| **throwaway_ground** | Book a throwaway ground leg to unlock a cheaper bundle |
| **eurostar_return** | Eurostar return tickets cheaper than two one-ways |
| **cross_border_rail** | Cross-border rail cheaper than domestic + domestic |
| **ferry_cabin** | Overnight ferry with cabin saves a hotel night |
| **eu261** | Long connection triggers EU261 compensation rights |
| **self_transfer** | Build your own connection via two separate tickets |
| **regional_pass** | Day/week rail pass cheaper than point-to-point tickets |
| **departure_tax** | Avoid high departure taxes by originating from a different country |
| **rail_competition** | Competitive rail route cheaper than flying |
| **back_to_back** | Two overlapping round-trips cheaper than flexible one-ways |
| **mileage_run** | Cheap fare earns disproportionate frequent-flyer miles |
| **day_use** | Day-use hotel for long layovers instead of a full night |
| **error_fare** | Flag abnormally cheap fares (potential error fares or flash sales) |

## Flight Providers

trvl ships with **Google Flights** (hand-rolled protobuf) on the default code path, augmented by **Kiwi** (virtual-interlining merge) for compatible one-way searches and **AFKLM Flying Blue** (Offers API v3, opt-in) for award-availability scans. Two additional providers are available as opt-ins for specific use cases:

| Provider | Protocol | Strength | Activation | Auth |
|----------|----------|----------|------------|------|
| **Google Flights** | hand-rolled protobuf | Broadest coverage, server-side filters | default | None |
| **Kiwi** | REST | Virtual-interlining + self-connect candidates | default (one-way merge) | None |
| **AFKLM Flying Blue** | Offers API v3 + Award API | Cash + miles cabin search on KL/AF metal | `--award` (CLI) / built-in opt-in | KLM/Flying Blue session cookie |
| **Skiplagged** | Streamable HTTP MCP (`@skiplagged/mcp` v0.0.4, protocol 2025-06-18) | Genre-defining hidden-city + virtual-interlining defaults | default (one-way merge) / `--provider skiplagged` for solo | None |

The default flight search merges results from Google Flights, Kiwi, and Skiplagged into a single sorted list, so plain `trvl flights HEL BCN 2026-07-01` already includes hidden-city / virtual-interlining options. Use `--provider skiplagged` to query Skiplagged on its own when you want to cross-validate or see only the hidden-city candidates.

```bash
# Default (Google Flights + Kiwi + Skiplagged merge):
trvl flights HEL BCN 2026-07-01

# Skiplagged hidden-city / virtual-interlining only:
trvl flights HEL BCN 2026-07-01 --provider skiplagged

# AFKLM Flying Blue award availability:
trvl flights AMS NRT 2026-09-15 --award
```

## Ground Transport Providers

trvl searches 20 ground transport providers in parallel, covering most of Europe. Airport transfers add taxi estimates and rental cars add optional Skyscanner Car Hire, so trvl exposes 22 transport providers overall:

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
| **Tallink** | Booking SPA API | Baltic Sea ferries (Helsinki, Tallinn, Stockholm, Riga) — future dates | EUR 16+ | Session cookie (auto) |
| **Viking Line** | Reference schedule | Baltic Sea ferries (Helsinki, Tallinn, Stockholm, Turku) | EUR 22+ | None |
| **Eckerö Line** | Magento AJAX API | Helsinki ↔ Tallinn (M/S Finlandia) | EUR 19+ | None |
| **Stena Line** | Reference schedule | North Sea + Baltic (Gothenburg, Kiel, Karlskrona, Gdynia, …) | EUR 25+ | None |
| **Finnlines** | GraphQL (AppSync) | Helsinki ↔ Travemünde, Naantali ↔ Kapellskär, Malmö ↔ Świnoujście | EUR 27+ | Public API key (embedded) |
| **DFDS** | REST API | North Sea + Baltic (Kiel, Amsterdam, Newcastle, Copenhagen, …) | EUR 49+ | None |

Two providers (NS, Digitransit/VR) use public API keys that are embedded in the binary — no signup or personal key is required from the user.

## How trvl Compares

**Why trvl:** Most travel MCP servers wrap a single provider and require you to bring your own paid API key. trvl is different on three axes that matter for AI assistants:

- **No API keys required** — Google Flights and Google Hotels are read directly, so it works the moment it's installed. No signup, no billing, no key rotation.
- **Multi-modal in one place** — flights, hotels, trains, buses, ferries, and rental cars behind one `travel` tool, not five separate integrations.
- **Single Go binary, any MCP client** — one static binary runs as a stdio MCP server for Claude, Cursor, Windsurf, Codex, or any MCP-compatible assistant, and doubles as a standalone CLI. No runtime, no container required.
- **One-command setup** — `trvl mcp install` auto-configures 10 MCP clients (Claude Desktop, Cursor, Claude Code, Windsurf, Codex, VS Code Copilot, Gemini CLI, Amazon Q, Zed, LM Studio) with no JSON hand-editing; add `--dry-run` to preview the change first.

For the agent-focused comparison against Google Flights, KAYAK, ChatGPT search, and other travel MCPs, see [`docs/COMPARISON.md`](docs/COMPARISON.md).

| Feature | trvl | fli | Google Flights | Skyscanner | Kiwi |
|---------|------|-----|---------------|------------|------|
| Flight search | ✅ | ✅ | ✅ | ✅ | ✅ |
| Bus/train/ferry search | ✅ (20 providers: FlixBus, RegioJet, Eurostar, DB, ÖBB, NS, VR, SNCF, Trainline, Transitous, Renfe, European Sleeper, Snälltåget, Tallink, Viking Line, Eckerö Line, Finnlines, Stena Line, DFDS, Ferryhopper) | ❌ | ❌ | ❌ | ❌ |
| Price tracking | ✅ (watches with alerts) | ❌ | ❌ | ❌ | ❌ |
| Hotel search | ✅ (6 providers: Google Hotels, Trivago, Airbnb, Booking.com, Hostelworld, HomeToGo) | ❌ | ❌ | ❌ | ❌ |
| Hotel search + top-N room/amenity detail | ✅ (`search_hotels_with_details`, including best-effort cancellation, board, and fee metadata) | ❌ | ✅ (web UI) | ❌ | ❌ |
| Flight providers | ✅ (Google Flights default + Kiwi virtual-interlining merge; direct LCC fares: Ryanair + Wizz Air; AFKLM Flying Blue award scanner; Transavia opt-in; Skiplagged hidden-city via opt-in `--provider skiplagged`; Travelpayouts price signals via opt-in `pricetrends`) | ✅ | ✅ | ✅ | ✅ |
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
trvl prices hold "<hotel_id>" --name "Hotel Lutetia Paris" --checkin 2026-06-15 --checkout 2026-06-18 --price 420 --currency EUR --refundable
trvl prices rebook <hold_id> --min-savings 25
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

### Buses, Trains & Ferries

Searches 20 providers in parallel: FlixBus (buses, pan-European), RegioJet (buses+trains, CZ/SK/AT/HU/DE/PL), Eurostar/Snap (trains, London↔Paris/Brussels/Amsterdam/Cologne), Deutsche Bahn (trains, all European rail), ÖBB (shop/HAFAS API), NS (Dutch railways), VR (Finnish railways, via Digitransit API), SNCF (trains, French TGV/TER), Trainline (aggregated rail across major European operators), Transitous.org (transit routing, pan-European), Renfe (Spanish AVE high-speed API), European Sleeper (night trains Brussels↔Berlin↔Prague + Amsterdam/Rotterdam/Antwerp/Dresden), Snälltåget (Swedish night trains Stockholm↔Malmö/Åre/Berlin, Sqills S3 API), Tallink (Baltic Sea ferries, live API), Viking Line (Baltic Sea ferries), Eckerö Line (Helsinki↔Tallinn, live Magento API), Finnlines (Helsinki↔Travemünde, Naantali↔Kapellskär, GraphQL API), Stena Line (North Sea + Baltic ferries), DFDS (North Sea + Baltic ferries, live availability API), and Ferryhopper (33 countries, 190+ ferry operators, via MCP). Airport transfers also add taxi fare estimates for door-to-door comparisons.

API routes are the default path. If you want trvl to try browser/curl/cookie-assisted fallbacks for protected providers such as SNCF or Trainline, pass `--allow-browser-fallbacks` (or set `TRVL_ALLOW_BROWSER_FALLBACKS=true`).

```bash
trvl ground Prague Vienna 2026-07-01                  # All 20 providers
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

Combines flights, trains, buses and ferries into optimal itineraries across all 20 providers. `--avoid` filters only the avoided mode, and `--depart-after` / `--arrive-by` are applied against the assembled itinerary times.

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

### Travel Hacks

Runs 37 detectors in parallel and ranks savings opportunities. Pass `--return` for round-trip hacks. Add `--carry-on` to restrict hidden-city results to carry-on only.

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
| **Data** | Real-time from Google Flights (+ Kiwi, Ryanair, Wizz Air; Transavia/Skiplagged/Travelpayouts opt-in) + 6 hotel sources (Google Hotels, Trivago, Airbnb, Booking.com, Hostelworld, HomeToGo) + 20 ground providers (FlixBus, RegioJet, Eurostar, DB, ÖBB, NS, VR, SNCF, Trainline, Transitous, Renfe, European Sleeper, Snälltåget, Tallink, Viking Line, Eckerö Line, Finnlines, Stena Line, DFDS, Ferryhopper) + free destination/enrichment APIs (weather, air quality, sun times, bike-share, holidays, currency) |
| **Auth** | No personal API keys required. Two providers (NS, Digitransit/VR) use public keys embedded in the binary. Optional browser/cookie fallbacks are available for protected providers when explicitly enabled. |
| **MCP** | Full v2025-11-25 — 1 smart MCP tool, 66 compatibility aliases (incl. 4 profile aliases, 3 price watch aliases, provider health, award sweet-spot scanning), 7 prompts, resources, structured content, progress notifications, resource subscriptions, tool description orchestration |
| **CLI** | 56 commands (+ 7 watch subcommands) with table/JSON output, color, shell completion |
| **Booking links** | Flight and hotel results include manual handoff links; hotel search prices must be verified before treating them as final |
| **Travel hacks** | 37 detectors (throwaway, hidden-city, positioning, ferry, multi-modal, stopover, date-flex, error fare, back-to-back, rail competition, and more) |
| **Personal profile** | Learns from your booking history (email parsing + LLM). Remembers FF status, luggage needs, favourite properties, departure preferences, travel hacks used, accommodation preferences, family composition. Pre-search interviews skip questions the profile already answers. |
| **Output** | Pretty tables with color (default) or JSON (`--format json`) |
| **Platforms** | Linux, macOS (amd64, arm64). Windows CI in progress. |
| **Code** | Go codebase with CI race/coverage gates and a documented local test matrix in [docs/TESTING.md](docs/TESTING.md) |
| **License** | PolyForm Noncommercial 1.0 |

## Attribution

Built on the shoulders of:

- **[fli](https://github.com/punitarani/fli)** by Punit Arani — the original Google Flights reverse-engineering library
- **[utls](https://github.com/refraction-networking/utls)** — Chrome TLS fingerprint impersonation
- **[icecreamsoft](https://icecreamsoft.hashnode.dev/building-a-web-app-for-travel-search)** — Google Hotels batchexecute documentation
- **[SerpAPI](https://serpapi.com/google-hotels-api)** — Hotel parameter reference

## Legal

`trvl` is a **personal-use tool** that accesses public-facing web APIs from Google Flights, Google Hotels, and other travel providers. It does not bypass authentication, access protected content, or circumvent rate limits. Request patterns are rate-limited to be indistinguishable from manual browsing (0.5-2 req/s per provider).

**Important:** Automated access may violate some providers' Terms of Service. This tool is licensed under PolyForm Noncommercial 1.0 — commercial use, operation as a service, or redistribution of scraped data is prohibited. You are responsible for ensuring your use complies with applicable terms and laws in your jurisdiction. Same approach as [fli](https://github.com/punitarani/fli) (1K+ stars, MIT licensed).

## Ecosystem

trvl is part of a suite of MCP tools:

| Tool | Description |
|------|-------------|
| [mcp-gateway](https://github.com/MikkoParkkola/mcp-gateway) | Universal MCP gateway — compact 12-15 tool surface replaces 100+ registrations |
| **[trvl](https://github.com/MikkoParkkola/trvl)** | **AI travel agent — 1 smart MCP tool plus 66 compatibility aliases for flights, hotels, rental cars, ground transport** |
| [nab](https://github.com/MikkoParkkola/nab) | Web content extraction — fetch any URL with cookies + anti-bot bypass |
| [axterminator](https://github.com/MikkoParkkola/axterminator) | macOS GUI automation — 35 MCP tools via Accessibility API |

## HTTP / Remote MCP Mode

Local stdio remains the default and safest transport. `trvl mcp --http` binds
to `127.0.0.1` by default, requires `Authorization: Bearer <token>`, and
generates a random local token at startup when no token is configured.

Remote exposure requires an explicit host and auth configuration:

```bash
# Local gateway, read/write static token
TRVL_MCP_TOKEN="$(openssl rand -base64 32)" trvl mcp --http --host 127.0.0.1 --port 8000

# Remote gateway, separate read-only and read/write tokens
TRVL_MCP_READ_TOKEN="$(openssl rand -base64 32)" \
TRVL_MCP_WRITE_TOKEN="$(openssl rand -base64 32)" \
trvl mcp --http --host 0.0.0.0 --port 8000
```

OAuth 2.1 deployments should put an OAuth provider or gateway in front of
trvl. The provider handles Authorization Code + PKCE; trvl validates the
resulting access token through introspection and enforces scopes:

```bash
trvl mcp --http --host 127.0.0.1 --port 8000 \
  --oauth-introspection-url https://auth.example.com/oauth2/introspect \
  --oauth-client-id trvl-resource-server \
  --oauth-client-secret "$TRVL_OAUTH_INTROSPECTION_SECRET" \
  --oauth-audience trvl-mcp
```

Use `trvl:read` for read-only tools and `trvl:write` for tools that mutate
local trips, preferences, watches, providers, or traveller workspace data.
Write scope implies read scope.

## Star it

If trvl saved you a browser tab or an API subscription, a star helps other travellers (and their AI assistants) find it. That's the whole ask.

## Troubleshooting

**MCP server not showing tools?** Restart your AI client after running `trvl mcp install`. Verify the binary is on your `$PATH` with `which trvl`.

**Flight search returns empty results?** Some routes have no data in Google Flights. Try a major airport pair (e.g. `trvl flights HEL LHR 2026-07-01`) to confirm it works. Use `--format json` to see the raw response.

**"context deadline exceeded" on ground transport?** Ferry and rail providers sometimes throttle. Retry after 30 seconds, or use `--timeout 3m` for slow providers.

**Hotel search shows 0 results?** Check your dates — Google Hotels requires check-in in the future and at least 1 night. Try: `trvl hotels "London" --checkin 2026-07-01 --checkout 2026-07-03`.

**MCP server crashes on startup?** Run `trvl mcp` directly in your terminal to see the error. Common cause: port conflict when using `--http`. Try a different port with `--port 8081`. HTTP mode listens on `127.0.0.1` by default and requires `Authorization: Bearer <token>`; set `TRVL_MCP_TOKEN`, scoped `TRVL_MCP_READ_TOKEN` / `TRVL_MCP_WRITE_TOKEN`, OAuth introspection env vars, or use the generated startup token.

## License

[PolyForm Noncommercial 1.0.0](LICENSE) — free for personal and noncommercial use.

Commercial use requires a separate license. That includes company-internal use, forks used for business workflows, hosted/shared-service deployments, embedding in commercial agent platforms, or using `trvl` to power paid travel, consulting, automation, or data products.

Companies can buy a standard commercial-use license via [GitHub Sponsors](https://github.com/sponsors/MikkoParkkola) at EUR 500/month per named project. See [COMMERCIAL.md](COMMERCIAL.md) for scope, exclusions, and custom terms.
