# trvl — Providers & Internals

> Moved out of the README. Travel-hack detectors, the full flight/ground provider rosters, and how trvl talks to each source.

## Travel Hack Detectors

`detect_travel_hacks` and `trvl hacks` run 36 detectors in parallel. Each one is independent and has a 20-second timeout:

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

trvl ships with **Google Flights** (hand-rolled protobuf) on the default code path, augmented by **Kiwi** (virtual-interlining merge) and **Skiplagged** (hidden-city) for compatible one-way searches. Several additional providers are available as opt-ins for specific use cases — **AFKLM Flying Blue** for award scans, plus the low-cost carriers **Ryanair**, **Wizz Air**, **Transavia**, and **easyJet** via `--provider`:

| Provider | Protocol | Strength | Activation | Auth |
|----------|----------|----------|------------|------|
| **Google Flights** | hand-rolled protobuf | Broadest coverage, server-side filters | default | None |
| **Kiwi** | REST | Virtual-interlining + self-connect candidates | default (one-way merge) | None |
| **AFKLM Flying Blue** | Offers API v3 + Award API | Cash + miles cabin search on KL/AF metal; native round-trip fares (both legs, one ticket) | `--provider afklm` (cash) / `--award` (miles), both opt-in | API credential (cash) / Flying Blue session cookie (award) |
| **Skiplagged** | Streamable HTTP MCP (`@skiplagged/mcp` v0.0.4, protocol 2025-06-18) | Genre-defining hidden-city + virtual-interlining defaults | default (one-way merge) / `--provider skiplagged` for solo | None |
| **Ryanair** | Public API | Largest European low-cost network | `--provider ryanair` | None |
| **Wizz Air** | Public API | Central/Eastern European low-cost routes | `--provider wizzair` | None |
| **Transavia** | Official API | KLM-group low-cost (NL/FR bases) | `--provider transavia`, opt-in | `TRANSAVIA_API_KEY` (free developer key) |
| **easyJet** | Availability API | Western European low-cost | `--provider easyjet`, opt-in (`EASYJET_API_BASE`) | None (public path is Akamai bot-defended) |

The default flight search merges results from Google Flights, Kiwi, and Skiplagged into a single sorted list, so plain `trvl flights HEL BCN 2026-07-01` already includes hidden-city / virtual-interlining options. Use `--provider skiplagged` to query Skiplagged on its own when you want to cross-validate or see only the hidden-city candidates.

```bash
# Default (Google Flights + Kiwi + Skiplagged merge):
trvl flights HEL BCN 2026-07-01

# Skiplagged hidden-city / virtual-interlining only:
trvl flights HEL BCN 2026-07-01 --provider skiplagged

# AFKLM Flying Blue award availability:
trvl flights AMS NRT 2026-09-15 --award

# AFKLM cash fares only, with native round-trip tickets (both legs, one fare):
trvl flights AMS BCN 2026-07-01 --return 2026-07-08 --provider afklm

# A single low-cost carrier only (Ryanair, Wizz Air, Transavia, or easyJet).
# Low-cost carriers have no discounted return fare, so a --return request is
# composed honestly as two one-way tickets (both legs, booked separately):
trvl flights STN DUB 2026-07-01 --return 2026-07-08 --provider ryanair
```

## Ground Transport Providers

trvl searches 21 ground transport providers in parallel, covering most of Europe. Airport transfers add taxi estimates and rental cars add optional Skyscanner Car Hire, so trvl exposes 23 transport providers overall:

| Provider | Protocol | Coverage | Starting price | Auth |
|----------|----------|----------|----------------|------|
| **Eurostar** | GraphQL | London ↔ Paris/Brussels/Amsterdam/Cologne | GBP 39+ | Browser cookies (Datadome) |
| **Deutsche Bahn** | REST (Vendo) | All European rail connections | EUR 37+ | None |
| **ÖBB** | REST (shop + HAFAS) | Austrian Railjet + cross-border (AT/DE/HU/IT) | EUR 38+ | None |
| **VR (via Digitransit)** | GraphQL | Finnish rail network | EUR 14+ | Public API key (embedded) |
| **NS** | REST | Dutch rail network | EUR 5+ | Public subscription key (embedded) |
| **Renfe** | REST | Spanish AVE high-speed + regional | EUR 36+ | None |
| **Trenitalia** | REST/BFF JSON | Italian high-speed + regional rail (Frecciarossa, Frecciargento, Intercity) | EUR 19+ | None |
| **SNCF** | REST | French TGV, TER, Intercity | Varies | None by default; optional browser/curl fallback |
| **Trainline** | REST | Aggregated: SNCF, DB, Eurostar, Trenitalia, … | Varies | None by default; optional browser/curl fallback |
| **European Sleeper** | Reference schedule | Night trains (Brussels ↔ Amsterdam ↔ Berlin ↔ Prague) | EUR 49+ | None |
| **Snälltåget** | Reference schedule | Swedish night trains (Stockholm ↔ Malmö ↔ Berlin) | EUR 40+ | None |
| **FlixBus** | REST | Pan-European buses (40+ countries) | EUR 5+ | None |
| **RegioJet** | REST | CZ/SK/AT/HU/DE/PL buses + trains | EUR 5+ | None |
| **Transitous** | MOTIS2 REST | Pan-European transit (schedule-based fallback) | — | None |
| **Tallink** | Booking SPA API | Baltic Sea ferries (Helsinki, Tallinn, Stockholm, Riga) — future dates | EUR 16+ | Session cookie (auto) |
| **Viking Line** | Reference schedule | Baltic Sea ferries (Helsinki, Tallinn, Stockholm, Turku) | EUR 22+ | None |
| **Eckerö Line** | Magento AJAX API | Helsinki ↔ Tallinn (M/S Finlandia) | EUR 19+ | None |
| **Stena Line** | Reference schedule | North Sea + Baltic (Gothenburg, Kiel, Karlskrona, Gdynia, …) | EUR 25+ | None |
| **Finnlines** | GraphQL (AppSync) | Helsinki ↔ Travemünde, Naantali ↔ Kapellskär, Malmö ↔ Świnoujście | EUR 27+ | Public API key (embedded) |
| **DFDS** | REST API | North Sea + Baltic (Kiel, Amsterdam, Newcastle, Copenhagen, …) | EUR 49+ | None |
| **Ferryhopper** | REST API | Mediterranean ferries (Greece, Italy, Spain, Croatia, …) | EUR 10+ | None |

Two providers (NS, Digitransit/VR) use public API keys that are embedded in the binary — no signup or personal key is required from the user.


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

