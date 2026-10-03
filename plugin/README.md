# trvl Claude Code Plugin

This plugin adds three travel skills, a `/trvl` command, and a
`trip-coordinator` agent. It does not download a program and it does not start
one. The trvl MCP server is the `trvl` program, installed on its own.

## Install

From a local clone of this repository (run from the repo root):

```bash
claude plugin validate ./plugin
claude plugin marketplace add . --scope user
claude plugin install trvl --scope user
```

Install the trvl program separately, then connect it as an MCP server in your
own client configuration:

```bash
brew install MikkoParkkola/tap/trvl
```

The skills call `mcp__trvl__*` tools when that server is already connected.
If it is not, they can call the same tool through an mcp-gateway that already
exposes a server named `trvl`. This plugin starts neither server.

## Components

- Skill: `trvl-trip-planner`
- Skill: `trvl-price-watch`
- Skill: `trvl-destination-research`
- Command: `/trvl`
- Agent: `trip-coordinator`

## Worked Examples

### 1. Plan A Trip

```text
/trvl plan a weekend getaway from HEL to Prague in July for two people under 900 EUR
```

The command routes to `trvl-trip-planner`, loads the traveller profile through
the `travel` smart tool, then dispatches to `plan_trip`, `search_flights`,
`search_accommodations`, and `search_hotels` legacy-compatible capabilities when useful.
It runs `assess_trip` and
reports the itinerary with travel hack savings.

For hotels, use `search_accommodations` for traveller-facing
stay recommendations and treat `search_hotels` prices as discovery lead-ins.
Before a final recommendation, use criteria-matched offers or verify shortlisted
properties with `search_hotels_with_details`, `hotel_rooms`, or `trvl serpapi`
when the user has `SERPAPI_KEY`, then rank on room-level totals or
tax-inclusive provider totals.
Do not present a hotel rate as booked, held, locked, or guaranteed.

### 2. Watch A Flight Or Hotel Deal

```text
/trvl price-watch HEL to NRT for September, alert me under 700 EUR with one checked bag
```

The command routes to `trvl-price-watch`, creates a durable `watch_price`
record, and returns a /loop-compatible `check_watches` cadence. For hotels it
can also use `watch_room_availability`; for rolling inspiration windows it uses
`watch_opportunities`.

### 3. Research A Destination

```text
/trvl destination-research Barcelona for 2027-07-01 to 2027-07-08, food, museums, and local events
```

The command routes to `trvl-destination-research`, composing
`destination_info`, `travel_guide`, `local_events`, `nearby_places`, and
`check_visa` into one research packet.

## Underlying Tool Surface

The trvl program at v1.25.0 advertises 1 smart MCP tool plus 66 legacy-compatible capabilities.
These skills and the `/trvl` command are written for that surface. They run only when that program is connected separately.

Flights:
`search_flights`, `search_dates`, `suggest_dates`, `optimize_trip_dates`,
`find_trip_window`, `plan_flight_bundle`, `find_interactive`,
`search_natural`, `search_hidden_city`, `search_awards`, `plan_trip`,
`optimize_booking`.

Hotels (6 sources, including HomeToGo vacation rentals):
`search_accommodations`, `search_hotels`, `search_hotels_with_details`, `search_hotel_by_name`,
`hotel_prices`, `hotel_reviews`, `hotel_rooms`, `watch_room_availability`,
`detect_accommodation_hacks`.

Ground and multimodal:
`search_ground`, `search_route`, `search_airport_transfers`,
`optimize_multi_city`.

The default flight merge uses Google Flights, Kiwi, and Skiplagged. AFKLM and
the low-cost-carrier integrations are solo or opt-in paths. Their requirements
are in the repository at
[docs/PROVIDERS.md](https://github.com/MikkoParkkola/trvl/blob/main/docs/PROVIDERS.md).

Destination context:
`destination_info`, `get_weather`, `travel_guide`, `local_events`,
`nearby_places`, `search_restaurants`, `search_lounges`, `weekend_getaway`.

Hacks and viability:
`detect_travel_hacks`, `assess_trip`, `search_deals`.

Reference:
`get_baggage_rules`, `check_visa`, `calculate_points_value`.

Profile and preferences:
`get_preferences`, `update_preferences`, `onboard_profile`, `interview_trip`,
`build_profile`, `add_booking`.

Trips and calendar:
`create_trip`, `add_trip_leg`, `mark_trip_booked`, `get_trip`, `list_trips`,
`export_ics`, `trip_workspace`.

Watches and opportunities:
`watch_price`, `list_watches`, `check_watches`, `watch_opportunities`,
`list_opportunity_watches`.

Providers:
`list_providers`, `provider_health`, `suggest_providers`, `test_provider`,
`remove_provider`, plus provider status blocks returned by search tools.

Provider definitions are reviewed source shipped in the binary; `trvl providers
enable <id>` turns one on. `configure_provider` still exists but refuses with an
error — runtime JSON definitions were removed in 1.21.0 (#538), and new
providers arrive by pull request or in a fork.

Awards and points:
`calculate_points_value`, `search_awards`, and miles earning annotations on
flight searches.

## What this plugin runs and where data goes

This plugin runs no program and downloads nothing. It sends no telemetry and
it does not read credentials.

The separate trvl program is what searches, stores state, and sends the daily
heartbeat. A search sends the route, the dates, and the traveller count to the
travel sites that search uses. The default flight merge reaches Google Flights,
Kiwi, and Skiplagged. Optional providers run only when you turn them on.

The program keeps trips, preferences, the traveller profile, price watches,
search history, cached cookies, provider tokens, and a random install id under
`~/.trvl` until you delete those files. It does not send your Claude
conversation to the author.

A released build of that program sends at most one POST per install per day to
`https://telemetry.revaluator.ai/v1/heartbeat`. The JSON has five fields:
`project` (`trvl`), `event` (`heartbeat`), the version, the Go runtime string,
and `install_id`. It has no search, no hostname, and no username. Cloudflare
sees the connection and stores an approximate city and country with the
heartbeat for 90 days. Set `TRVL_NO_TELEMETRY=1`, `NO_TELEMETRY=1`, or
`DO_NOT_TRACK=1` to stop it. The full text is [PRIVACY.md](PRIVACY.md).

If you attach a webhook URL to a price watch, trvl POSTs that watch's route
and price to the address you supplied.

## Notes

- The plugin stays stateless; durable trip and watch state lives in trvl's own
  preference, trip, and watch stores.
- The command and skills never book travel or save preference changes without
  explicit user confirmation.
- Optional provider configuration still requires user consent through trvl.
