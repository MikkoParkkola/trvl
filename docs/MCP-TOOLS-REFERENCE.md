# trvl — MCP Tools & Filters Reference

> Moved out of the README to keep it readable. The smart `travel` router and its 66 compatibility aliases, the search-filter matrices, and the MCP protocol feature set.

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
| **detect_travel_hacks** | Run 36 parallel detectors for flight and ground savings opportunities | HEL → AMS, Apr 13, carry-on only |
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
| **Prompts** | `plan-trip`, `find-cheapest-dates`, `compare-hotels`, `where-should-i-go`, `packing-list` |
| **Resources** | Airport codes (50 major hubs), flight/hotel usage guides, price-watch subscriptions |
| **Tool description orchestration** | `find_trip_window` instructs the LLM to fetch calendar data first, then pass busy intervals in — works on every MCP client. See [docs/MCP-ORCHESTRATION.md](docs/MCP-ORCHESTRATION.md) |
| **Progress notifications** | Long-running searches stream progress tokens to the client |
| **Resource subscriptions** | Price-watch resources notify subscribers on price changes |
| **Progressive disclosure** | Suggestions for follow-up searches in every response |
| **Booking links** | Direct Google Flights/Hotels links in results |

