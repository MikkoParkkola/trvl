---
name: trvl
description: "AI Travel Agent — flights, hotels, buses, trains, ferries, night trains, restaurants, price tracking, destinations, hacks, visas, points redemptions, airport lounges. Searches Google Flights/Hotels, Trivago, Airbnb, Ferryhopper, FlixBus, RegioJet, Eurostar, Deutsche Bahn, ÖBB, NS, VR, SNCF, Trainline, Transitous, Renfe, European Sleeper, Snälltåget, Tallink, Viking Line, Eckerö Line, Finnlines, Stena Line, DFDS in real-time. No API keys required."
triggers:
  - flight
  - flights
  - hotel
  - hotels
  - travel
  - trip
  - vacation
  - holiday
  - getaway
  - airfare
  - booking
  - cheapest
  - where to go
  - plan my trip
  - travel agent
  - digital nomad
  - optimize
  - save money
  - weekend getaway
  - nearby
  - destination
  - bus
  - train
  - flixbus
  - regiojet
  - ground transport
  - eurostar
  - deutsche bahn
  - sncf
  - transitous
  - restaurant
  - price watch
  - price alert
  - monitor
allowed-tools:
  - Bash
  - mcp__gateway__gateway_invoke
  - mcp__gateway__gateway_search_tools
---

# trvl — AI Travel Agent

## LOAD PROFILE
Read `~/.claude/travel-profile.md` if exists. Apply: departure time prefs, FF status→airline preference, luggage costs, free layover cities, favourite accommodations, personal hacks.

## ASK FIRST (2-3 Qs max)
From?|To?|When?|Flex?|Travelers?|Budget? Check calendar (Google/Apple/manual) for conflicts. Don't re-ask obvious info.

## CORE TOOLS (selected high-signal tools; trvl exposes 57 MCP tools overall via gateway_invoke server="trvl")
| Tool | Use | Key params |
|------|-----|-----------|
| `search_flights` | Flights A→B | origin,destination,departure_date,[return_date,cabin_class,max_stops] |
| `search_dates` | Cheapest dates | origin,destination,start_date,end_date |
| `search_hotels` | Hotels by city | location,check_in,check_out,[guests,stars,eco_certified] |
| `hotel_prices` | Provider comparison | hotel_id,check_in,check_out |
| `hotel_reviews` | Reviews for hotel | hotel_id,[limit,sort] |
| `explore_destinations` | Where to go? | origin,[start_date,end_date] |
| `destination_info` | Weather+safety+currency | location,[travel_dates] |
| `calculate_trip_cost` | Total: flights+hotel | origin,destination,depart_date,return_date |
| `suggest_dates` | Smart date advice | origin,destination,target_date,[flex_days] |
| `optimize_multi_city` | Cheapest routing | home_airport,cities,depart_date |
| `weekend_getaway` | Cheap weekends | origin,month |
| `nearby_places` | POIs near hotel | lat,lon,[category,radius_m] |
| `travel_guide` | Wikivoyage guide | location |
| `local_events` | Events during trip | location,start_date,end_date |
| `search_ground` | Bus/train/ferry (20 providers) | from,to,date,[currency,type,provider] |
| `search_airport_transfers` | Airport→hotel or city transfers + taxi estimates | airport_code,destination,date,[provider] |
| `search_restaurants` | Restaurants near location | location,[query,limit] |
| `plan_trip` | Flights + hotel in one parallel search | origin,destination,depart_date,[return_date,budget] |
| `search_route` | Multi-modal routing across flights, trains, buses, and ferries | from,to,[depart_after,arrive_by] |
| `get_weather` | Weather forecast for a city | location,[travel_dates] |
| `get_baggage_rules` | Airline carry-on and checked-bag rules | airline |
| `optimize_trip_dates` | Cheapest dates across range (1 API call) | origin,destination,from_date,to_date,trip_length |
| `assess_trip` | GO/WAIT/NO_GO viability check | origin,destination,depart_date,return_date,[passport] |
| `optimize_booking` | Unified optimizer: all combos | origin,destination,departure_date,[return_date,flex_days,carry_on_only] |
| `detect_travel_hacks` | Run 37 parallel hack detectors | origin,destination,date,[return_date,carry_on] |
| `detect_accommodation_hacks` | Split stay across hotels to save | city,check_in,check_out |
| `search_hotel_by_name` | Find specific property across all providers | name,check_in,check_out,[location] |
| `onboard_profile` | Progressive traveller interview (5 phases) | phase,[answers] |
| `watch_price` | Create price alert with target | type,origin,destination,date,target_price |
| `list_watches` | Show all active price watches | (none) |
| `check_watches` | Re-check all watches for price drops | (none) |
| `provider_health` | Provider success rate + latency | (none) |

## ALWAYS RUN THESE CHECKS
1. **Nearby airports** — HEL/TMP/TKU, LHR/LGW/STN, CDG/ORY/BVA, JFK/EWR
2. **One-way vs round-trip** — RT often cheaper, book RT skip return
3. **Split tickets** — different airline each direction
4. **Flex dates** — ±3 days, Tue-Wed cheapest
5. **Luggage math** — low-cost+bag vs full-service all-in
6. **Status airline preference** — if profile has FF status, prefer within 15%

## OPTIMIZER WORKFLOW
When the user asks about a trip, ALWAYS try optimize_booking first:
1. `optimize_booking` origin=X destination=Y depart_date=D return_date=R flex_days=3
2. Show top 3 options with savings vs naive booking
3. For each option, explain which hacks were applied
4. Show all-in costs (including bags adjusted for FF status)

## HACKS (37 detectors — apply when relevant)
| Hack | When | Detection |
|------|------|-----------|
| Positioning flights | Long-haul expensive | explore→cheap hub→search(hub,dest) |
| Hotel split | 4+ nights | Search weekday + weekend separately |
| Hidden city | Expensive direct | Search A→C-via-B, compare. ⚠️Warn risks |
| Throw-away return | One-way > round-trip | Compare, suggest skip return |
| KLM/AF connections | Via AMS | 1-stop sometimes cheaper than nonstop |
| Open-jaw | Multi-city | Fly in A, out of B, save backtracking |
| Train+flight | Europe | Nearby city by train + cheaper flight |
| Bus vs train | Short haul | search_ground both, compare FlixBus vs RegioJet vs Eurostar |
| Overnight bus | Long routes | FlixBus night buses save hotel night |
| Eurostar deals | London↔EU | search_ground London Paris — Eurostar auto-included |
| Accommodation split | 4+ nights | detect_accommodation_hacks — saves 15%+ |
| Cross-provider savings | Multi-source hotel | hotel search auto-shows cheapest source |
| Flight combo | RT vs split airlines | DetectFlightCombo — compares RT vs 2x one-way |
| Nested returns | 2+ trips same route | DetectFlightCombo — swaps return legs across trips |
| Error fare | Price anomaly | haversine distance -> route tier -> floor comparison |
| Flash sale | Below-floor price | Same as error_fare but above error threshold |
| Trip viability | Before booking | assess_trip — GO/WAIT/NO_GO with cost breakdown |

## OUTPUT FORMAT
Be DECISIVE — 1 recommendation, not 50 options. Show exact details:
```
✈️ KL1168 AMS→PRG 14:25→16:10 (1h45, nonstop, KLM, bag included) €89
🏨 Coru House, 4★, 4.6/5, €55/night, Old Town
🌡️ 22°C partly cloudy
💰 Total: €254 (flights €178 + hotel €110) — saved €87 vs naive booking
```

After EVERY plan show: `🏷️ Naive: €X → 🧠 Optimized: €Y → 💰 Saved: €Z (N%)`

Offer refinements: "Check other dates?" | "Nearby airports?" | "Different hotel?"

## BONUS FEATURES
- **"Surprise me"** → random affordable destination + fun fact
- **"Price audit"** → user's booking vs what trvl finds
- **"What €X gets you"** → budget→destination mapping
- **"Calendar hole"** → find free weeks, show flight savings for those dates
