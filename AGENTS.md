# trvl — Complete Setup for AI Assistants

> **Human**: Give this URL to your AI assistant and say "set up trvl":
> `https://raw.githubusercontent.com/MikkoParkkola/trvl/main/AGENTS.md`

## What To Do

You are setting up `trvl` — a travel MCP server that gives you real-time Google Flights and Google Hotels data. No API keys needed. Run these steps in order:

### Step 1: Install

```bash
brew install MikkoParkkola/tap/trvl
```

Pre-built binary, no toolchain needed, 1 second. If Homebrew is not available:

```bash
# Manual: detect OS/arch, download, extract
curl -fsSL https://github.com/MikkoParkkola/trvl/releases/latest/download/trvl_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C /usr/local/bin trvl
```

### Step 2: Register as MCP server

```bash
trvl mcp install                       # Claude Desktop (default)
trvl mcp install --client cursor       # Cursor / Windsurf
trvl mcp install --client claude-code  # Claude Code
```

Then restart your MCP client. No JSON editing needed.

For Claude Code, you can also run:
```bash
claude mcp add trvl --transport stdio -- trvl mcp
```

### Step 3: Install the bundled skill (makes you smarter about travel searches)

```bash
mkdir -p ~/.claude/skills
for s in trvl; do
  curl -fsSL "https://raw.githubusercontent.com/MikkoParkkola/trvl/main/.claude/skills/$s.md" -o "$HOME/.claude/skills/$s.md"
done
```

The skill at `.claude/skills/trvl.md` teaches you how to:
- Ask the right questions (From? To? When? Flex? Budget?)
- Run hack detectors automatically after every search
- Use the unified optimizer (`optimize_booking`) for trip planning
- Show the "Naive -> Optimized -> Saved" comparison after every plan
- Use all-in pricing with FF benefits (bag fees included, status benefits subtracted)
- Apply 37 travel hack detectors to find savings opportunities

### Step 4: Verify

```bash
trvl version
# Expected: trvl 0.3.0 (or later)

trvl flights HEL LHR 2026-07-01 --format json | head -5
# Expected: JSON with flight results
```

Tell the user: "trvl is installed with 57 MCP tools and 2 bundled Claude skills. It includes 37 travel hack detectors (including error fare and flash sale detection) that auto-fire on searches, a unified optimizer (optimize_booking) with 9 expansion strategies (alternative origins/destinations, rail+fly, date flex, hidden city, departure tax avoidance, rail competition alternatives, ferry cabin as hotel) that searches all combinations in parallel, all-in pricing with FF status (bag fees included, FF benefits subtracted), pre-priced candidate pipeline for ground alternatives, miles tracking and earning estimates, and cross-provider hotel price comparison with cross-currency savings display. I can search flights, hotels, destinations, plan trips, find weekend getaways, find optimal travel windows, optimize multi-city routes, find nearby restaurants, check local events, search ground transport (buses, trains, ferries, night trains), detect travel hacks, check weather forecasts, look up airline baggage rules, find airport lounges, check visa requirements, calculate points-vs-cash redemptions, and configure additional data providers (Airbnb, Booking.com, Hostelworld). Just ask me anything about travel."

### Step 5: Build travel profile (recommended)

Run the onboarding interview to learn how the user travels:

1. Call `onboard_profile` with `phase: 1` — ask the basics (home, frequency, companions)
2. After user answers, call `onboard_profile` with `phase: 2` — travel style (accommodation, budget, transport)
3. Continue through phases 3 (favourite cities, properties), 4 (companion, wishlist), and 5 (reasoning and strategies)
4. Each phase skips questions the profile already answers
5. Save answers to `~/.trvl/profile.json` via `add_booking` or `update_preferences`

If the user has email access (Gmail), also offer to scan booking history:
```
Call build_profile with source: "email" to scan Gmail for past bookings
```

The profile powers personalized search — preferred neighbourhoods, price elasticity, booking strategies, and destination recommendations.

### Step 6: (Optional) Set up free API keys for enhanced data

trvl works out of the box with Wikivoyage + OpenStreetMap (no keys needed). For richer data (events, restaurant ratings, attractions), the user can get free API keys:

| Service | What it adds | Signup |
|---------|-------------|--------|
| Ticketmaster | Events (concerts, sports, festivals) | https://developer.ticketmaster.com/ |
| Foursquare | Restaurant ratings, tips, price levels | https://developer.foursquare.com/ |
| Geoapify | Walking-distance POI search | https://myprojects.geoapify.com/ |
| OpenTripMap | Tourist attractions + Wikipedia | https://opentripmap.io/product |

All free, no credit card, 2 min signup each. Walk the user through each signup:
1. Open the URL for them
2. Tell them what to click (Sign up → Create project → Copy key)
3. Have them paste the key
4. Set it: `echo 'export TICKETMASTER_API_KEY="their-key"' >> ~/.zshrc && source ~/.zshrc`
5. Verify: `trvl events "Barcelona" --from 2026-07-01 --to 2026-07-08`

Use `/setup-api-keys` command for the guided wizard.

### Step 7: Build the traveller profile (advanced — email scanning)

The profile lives at `~/.trvl/preferences.json`. The best profile comes
from real booking history, not from asking questions. Try these approaches
in order — use the first one the user agrees to.

**Tier 1 (best): Scan their email and calendar**

Ask: "I can build your travel profile automatically by scanning your
booking confirmation emails. I'll look for flight bookings, hotel
reservations, loyalty programmes, and ground transport to understand
your patterns. Want me to do that?"

If yes, search Gmail for:
- `from:(booking.com OR airbnb OR finnair OR klm OR ryanair OR norwegian
  OR sas OR easyjet OR wizzair OR flixbus OR regiojet OR eurostar)
  subject:(confirmed OR confirmation OR booking OR ticket OR itinerary)`
- `from:(flyingblue OR finnairplus) subject:(status OR tier OR gold OR silver)`
- `from:(marriott OR hilton OR accor) subject:(member OR status OR points)`

From the results, extract:
- **Airlines used** and frequency → `loyalty_airlines`, carrier preferences
- **Routes flown** → `home_airports` (most frequent origin)
- **Hotels booked** → `min_hotel_stars` (infer from actual bookings),
  `preferred_districts` (from hotel addresses), accommodation style
- **Loyalty status emails** → exact programme and tier
- **Ground transport** → FlixBus/RegioJet/train patterns
- **Booking patterns** → books multiple and cancels? Airbnb vs hotels?
  Extended stays vs short trips?

Also check calendar for upcoming travel events.

Then run a quick trvl search to detect geoip currency → infer location.

Show the user what you found as a draft profile. Ask:
"Here's what I see from your bookings. Anything wrong or missing?"

Fix what they correct. Save with `update_preferences`.

**Tier 2 (good): Ask about a real trip**

If they decline email access, ask about concrete experiences instead
of abstract preferences:

> "Tell me about your last trip — where did you go, how did you get
> there, and where did you stay?"

One real trip reveals airline choice, hotel quality, neighborhood,
booking platform, trip length, and travel companions.

> "What would you change about it?"

Reveals pain points: bad wifi → `fast_wifi_needed`, too far from center
→ neighborhood matters, 5am flight → `flight_time_earliest`.

> "What's a trip that went perfectly?"

Reveals the gold standard: if they describe a 4-star boutique in
Prague 1 with great coffee, you know more than 10 questions would give.

Then fill gaps with 2-3 targeted questions based on what you still
don't know. Save with `update_preferences`.

**Tier 3 (fallback): Structured questions**

If neither of the above works, ask these 4 questions:

> Q1: Confirm home airport (infer from geoip first)
> Q2: Hotel dealbreakers (hostels? bathroom? stars? rating?)
> Q3: Carry-on only or checked bags?
> Q4: Direct flights or connections fine?
> Q5: Anything else about how you travel?

Save with `update_preferences`.

**What each field actually does in the code:**

| Field | Behavior |
|-------|----------|
| `home_airports` | Default origin for flight/trip/weekend/discover searches |
| `display_currency` | Price display across all 57 tools |
| `no_dormitories` | `FilterHotels()` drops hostels, capsules, guesthouse rooms by chain name + regex |
| `ensuite_only` | `FilterHotels()` drops shared-bathroom properties |
| `min_hotel_stars` | Passed to Google Hotels API as search filter |
| `min_hotel_rating` | Passed to search + activates 20-review minimum gate |
| `preferred_districts` | `FilterHotels()` strict-filters or prioritizes by neighborhood |
| `carry_on_only` | Travel hack detectors: hidden-city and throwaway require carry-on |
| `prefer_direct` | Flight search: nonstop filter |
| `default_companions` | 0=solo, 1=couple, 2+=family/group — personalizes search defaults |
| `trip_types` | e.g. `["city_break","beach","adventure"]` — destination suggestions |
| `seat_preference` | "window", "aisle", "no_preference" |
| `budget_per_night_min` | Filters too-cheap-to-trust hotels |
| `budget_per_night_max` | Max hotel price per night |
| `budget_flight_max` | Max one-way flight price |
| `deal_tolerance` | "price" (6am flight? yes), "comfort" (pay more), "balanced" |
| `flight_time_earliest` | e.g. "06:00" — no flights before this |
| `flight_time_latest` | e.g. "23:00" — no flights after this |
| `red_eye_ok` | Overnight flights acceptable? |
| `nationality` | ISO 3166-1 alpha-2 (e.g. "FI") — visa warnings |
| `languages` | Spoken languages, e.g. `["en","fi","sv"]` |
| `previous_trips` | Cities/countries visited — avoids repeat suggestions |
| `bucket_list` | Dream destinations — prioritized in suggestions |
| `activity_preferences` | e.g. `["museums","food","nature"]` — destination matching |
| `dietary_needs` | e.g. `["vegetarian","halal"]` — restaurant filtering |
| `notes` | Free-text for anything else |

**Frequent flyer and miles configuration:**

Users with frequent flyer status get all-in pricing that accounts for free checked bags, lounge access, and miles earning. Here's an example FF configuration:

```json
{
  "frequent_flyer_programs": [
    {"alliance": "skyteam", "tier": "gold", "airline_code": "KL", "program_name": "Flying Blue", "miles_balance": 45000},
    {"alliance": "oneworld", "tier": "sapphire", "airline_code": "RJ", "program_name": "Royal Plus"}
  ],
  "carry_on_only": false,
  "nationality": "FI",
  "home_airports": ["HEL"]
}
```

When FF status is set:
- Flight results include all-in pricing (bag fees included, FF benefits like free checked bags subtracted)
- Miles earning estimates appear on each flight
- Airline preference within 15% price delta of the cheapest option for the FF's alliance
- Lounge access is annotated on `search_lounges` results
- `calculate_points_value` uses the correct floor/ceiling valuations for the specific program

**Continuous learning — the profile is never "done":**

Track what the user searches for and how they respond to results.
Every interaction is a signal. Update the profile when you see a
pattern — always confirm before saving.

Within a conversation — watch reactions to results:
- Says "too expensive" to a €180 hotel → note their real budget ceiling
- Says "too far from center" → location matters, ask which neighborhood
- Ignores hostels in results → `no_dormitories` candidate
- Picks the 4-star over the 3-star every time → `min_hotel_stars: 4`
- Asks for "something with character" → add to `notes`
- Rejects a 6am flight → `flight_time_earliest: "07:00"`
- Clicks the Booking.com link → note platform preference

Across conversations — detect evolving patterns:
- Searches from a new airport 3+ times → add to `home_airports`
- Visits a city repeatedly → add to `previous_trips`, learn neighborhoods
- Mentions a dream destination → add to `bucket_list`
- Booking patterns change → ask if preferences should update
- Mentions new loyalty status → update immediately

The key: **don't ask abstract questions when you can observe behavior.**
"What's your budget?" is a bad question — watching them reject €180
hotels and pick €120 ones tells you more.

Always confirm before calling `update_preferences`. Show what changed.
CLI alternative: `trvl prefs init`

---

## How To Use (after setup)

You now have 57 MCP tools available. Use them when the user asks about travel:

### search_flights — Find flights between airports
```json
{"origin": "HEL", "destination": "NRT", "departure_date": "2026-06-15"}
```
Optional parameters:
- `return_date`: "2026-06-22" (makes it round-trip)
- `cabin_class`: "economy" | "premium_economy" | "business" | "first"
- `max_stops`: "any" | "nonstop" | "one_stop" | "two_plus"
- `sort_by`: "cheapest" | "duration" | "departure" | "arrival"
- `alliances`: comma-separated alliance names: "STAR_ALLIANCE", "ONEWORLD", "SKYTEAM" — server-side filter
- `depart_after`: earliest departure time "HH:MM" (e.g. "06:00") — server-side
- `depart_before`: latest departure time "HH:MM" (e.g. "22:00") — server-side
- `less_emissions`: true/false — only show flights with below-average CO2 — server-side
- `carry_on_bags`: integer — require N carry-on bags included in price — server-side price recalculation
- `checked_bags`: integer — **hidden Google feature** — require N checked bags in price. Google's UI only exposes carry-on; trvl also wires the checked-bag slot in the same filter array — server-side
- `require_checked_bag`: true/false — drop any flight without ≥1 free checked bag — client-side post-filter
- `max_price`: integer — maximum price in whole currency units — server-side
- `max_duration`: integer — maximum total duration in minutes — server-side
- `exclude_basic`: true/false — exclude basic economy fares — server-side
- `airlines`: comma-separated IATA codes to restrict results (e.g. "AY,LH")

### search_dates — Find the cheapest day to fly
```json
{"origin": "HEL", "destination": "NRT", "start_date": "2026-06-01", "end_date": "2026-06-30"}
```
Optional: `trip_duration` (days), `is_round_trip` (true/false)

### search_hotels — Find hotels in any city (Google Hotels + Trivago + Airbnb)
```json
{"location": "Tokyo", "check_in": "2026-06-15", "check_out": "2026-06-18"}
```
Optional:
- `guests`: number of guests (default: 2)
- `stars`: minimum star rating 1-5 — server-side `?class=N`
- `sort`: "price" | "rating" | "distance" | "stars"
- `currency`: "EUR" | "USD" etc.
- `free_cancellation`: true/false — only hotels with free cancellation — server-side `?fc=1`
- `property_type`: "hotel" | "apartment" | "hostel" | "resort" | "bnb" | "villa" — server-side `?ptype=N`
- `brand`: hotel chain name substring (e.g. "hilton", "marriott", "ibis") — client-side
- `min_rating`: minimum guest rating e.g. 4.0 — server-side `?rating=N` + client-side guard
- `max_distance`: maximum km from city center — server-side `?lrad=N`
- `amenities`: comma-separated required amenities (e.g. "pool,wifi") — client-side
- `min_price` / `max_price`: price range per night — server-side `?min_price` / `?max_price` + client-side guard
- `enrich_amenities`: true/false — fetch detail pages for top results (slower)
- `eco_certified`: true/false (default false) — only show eco-certified hotels with sustainability certifications — server-side `?ecof=1`

### hotel_prices — Compare prices across booking sites
```json
{"hotel_id": "<from search_hotels>", "check_in": "2026-06-15", "check_out": "2026-06-18"}
```

### destination_info — Travel intelligence for any city
```json
{"location": "Tokyo"}
```
Optional: `travel_dates` ("2026-06-15,2026-06-18" — comma-separated check-in,check-out)

Returns: weather forecast, country info (capital, languages, currencies), public holidays during travel dates, safety advisory (1-5 scale), currency exchange rates vs EUR, timezone.

### calculate_trip_cost — Estimate total trip cost
```json
{"origin": "HEL", "destination": "BCN", "depart_date": "2026-07-01", "return_date": "2026-07-08"}
```
Optional: `guests` (number, default 1), `currency` ("EUR" | "USD" etc.)

Returns: cheapest outbound flight + return flight + cheapest hotel per night, total cost, per-person cost, per-day cost.

### weekend_getaway — Find cheap weekend destinations
```json
{"origin": "HEL", "month": "july-2026"}
```
Optional: `max_budget` (number in EUR, 0 = no limit), `nights` (default: 2)

Returns: top 10 cheapest weekend destinations ranked by total estimated cost (round-trip flight + estimated hotel).

### suggest_dates — Smart date suggestions around a target date
```json
{"origin": "HEL", "destination": "BCN", "target_date": "2026-07-15"}
```
Optional: `flex_days` (default: 7), `round_trip` (boolean), `duration` (days for round-trip, default: 7)

Returns: 3 cheapest dates, weekday vs weekend analysis, savings insights, average price comparison.

### optimize_multi_city — Find cheapest routing for multi-city trips
```json
{"home_airport": "HEL", "cities": "BCN,ROM,PAR", "depart_date": "2026-07-01"}
```
Optional: `return_date` ("2026-07-21")

Returns: optimal visit order, per-segment prices, total cost, savings vs worst order. Tries all permutations (up to 6 cities).

### update_preferences — Update user travel preferences
```json
{"min_hotel_stars": 4, "no_dormitories": true}
```
Merges individual fields into `~/.trvl/preferences.json`. Only send the
fields you want to change — other fields are preserved. Always confirm
with the user before calling this tool.

### search_lounges — Find airport lounges at a given airport
```json
{"airport": "HEL"}
```
Returns: lounge name, terminal, type, accepted access cards (Priority Pass, Diners Club, LoungeKey, etc.), amenities, opening hours. Type indicates access network: "card" (Priority Pass/LoungeKey), "airline" (frequent flyer status), "bank" (credit card programme), "amex" (Centurion). If the user has `lounge_cards` or frequent flyer status set in preferences, results are annotated with `accessible_with` — the subset of their own cards or status that grant free entry to each lounge.

### check_visa — Check visa requirements for a passport→destination pair
```json
{"passport": "FI", "destination": "TH"}
```
Returns: visa status (`visa-free`, `visa-required`, `visa-on-arrival`, `e-visa`, `freedom-of-movement`), max stay duration, and notes. Uses ISO 3166-1 alpha-2 country codes.

### calculate_points_value — Compare points vs cash for a redemption
```json
{"cash_price": 450, "points_required": 20000, "program": "finnair-plus"}
```
Returns: effective cents-per-point, floor/ceiling valuation for the program, verdict (`use points`, `pay cash`, or `borderline`), and explanation.

### optimize_booking — Unified trip optimizer
```json
{"origin": "HEL", "destination": "BCN", "departure_date": "2026-07-01", "return_date": "2026-07-08"}
```
Optional:
- `flex_days`: date flexibility +/-N days (default 3)
- `guests`: number of passengers (default 1)
- `currency`: display currency (default EUR)
- `max_results`: top N results to return (default 5)
- `max_api_calls`: API call budget (default 15)
- `need_checked_bag`: whether a checked bag is needed
- `carry_on_only`: carry-on only trip

Returns: ranked booking strategies with all-in cost (baggage + FF status), savings vs naive direct booking. Searches alternative origins, destinations, rail+fly stations, hidden-city candidates, and date flexibility in a single call.

### optimize_trip_dates — Find cheapest dates across a range
```json
{"origin": "HEL", "destination": "BCN", "from_date": "2026-07-01", "to_date": "2026-07-31", "trip_length": 7}
```

Returns: cheapest departure dates across the entire range using a single CalendarGraph API call. Much faster than searching day by day.

### assess_trip — Trip viability pre-check
```json
{"origin": "HEL", "destination": "BKK", "depart_date": "2026-07-01", "return_date": "2026-07-14"}
```
Optional: `passport` (ISO country code for visa check)

Returns: GO / WAIT / NO_GO verdict with parallel checks for flights, hotels, visa requirements, and weather. Use before detailed planning to quickly validate a trip idea.

### search_hotel_by_name — Find a specific property by name
```json
{"name": "CORU House", "location": "Prague", "check_in": "2026-07-01", "check_out": "2026-07-05"}
```
Searches all providers (Google Hotels, Trivago, Airbnb, Booking.com, Hostelworld) using the property name as the search query, then fuzzy-matches results. Use when the user knows the exact hotel name.

### search_ground — Buses, trains, ferries between cities
```json
{"from": "Amsterdam", "to": "Paris", "date": "2026-07-01"}
```
Optional: `type` ("bus"|"train"|"ferry"), `currency`, `max_price`, `provider`
Searches 20+ providers in parallel including FlixBus, RegioJet, Eurostar, DB, NS, SNCF, Trainline, ferries. Eurostar Snap fares auto-searched for valid routes.

### onboard_profile — Progressive user interview
```json
{"phase": 0}
```
5-phase interview (0=LLM context confirmation, 1=basics, 2=style, 3=deep, 4=specifics, 5=reasoning). Builds a traveller personality model that drives search defaults.

### watch_price — Create a price alert
```json
{"type": "flight", "origin": "HEL", "destination": "BCN", "date": "2026-07-01", "target_price": 89, "currency": "EUR"}
```
Stores watch in `~/.trvl/watches.json`. Use `check_watches` to re-check prices, `list_watches` to see all active watches.

### provider_health — Provider status dashboard
```json
{}
```
Shows per-provider success rate, average latency, and last error from `~/.trvl/health.jsonl`. Use to diagnose why a provider isn't returning results.

### detect_travel_hacks — Find savings opportunities
```json
{"origin": "HEL", "destination": "BCN", "date": "2026-07-01"}
```
Runs 37 detectors in parallel: hidden-city, throwaway, positioning, back-to-back, rail competition, ferry cabin, error fare, date flex, and more. Optional: `return_date`, `carry_on_only`.

### MCP Prompts (for complex workflows)
- `plan-trip` — Full trip planning: flights + hotels + budget analysis
- `find-cheapest-dates` — Month-wide price calendar for a route
- `compare-hotels` — Side-by-side hotel comparison by user priorities

## Response Tips

- Results include `booking_url` — share these with the user for direct Google links
- Results include `suggestions` — use these to offer follow-up searches
- Prices reflect the user's IP geolocation currency
- For trip planning: search flights first, then hotels at the destination
- For budget trips: use `weekend_getaway` or `suggest_dates` to find the cheapest options
- For multi-city: use `optimize_multi_city` to find the cheapest routing order
- For full cost estimates: use `calculate_trip_cost` for flights + hotel totals
- For destination research: use `destination_info` for weather, safety, holidays
- Common IATA codes: HEL (Helsinki), JFK (New York), LHR (London), NRT (Tokyo), CDG (Paris), BCN (Barcelona), BKK (Bangkok), SIN (Singapore), DXB (Dubai), LAX (Los Angeles), FRA (Frankfurt), AMS (Amsterdam), ICN (Seoul)

## Troubleshooting

- **"command not found"**: `which trvl` — if empty, the binary isn't in PATH. Re-run Step 1.
- **No results**: Google may rate-limit. Wait 60 seconds and retry.
- **Wrong currency**: Normal — currency follows IP geolocation.
- **MCP tools not showing**: Restart Claude Code / Claude Desktop after Step 2.

## Source

- GitHub: https://github.com/MikkoParkkola/trvl
- License: PolyForm Noncommercial 1.0.0
- Inspired by [fli](https://github.com/punitarani/fli) by Punit Arani
