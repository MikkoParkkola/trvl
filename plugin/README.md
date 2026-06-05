# trvl Claude Code Plugin

This plugin bundles trvl as a Claude Code travel companion: MCP registration,
three travel skills, a `/trvl` command router, and a `trip-coordinator` agent
for complex itineraries.

## Install From Local Path

From this repository:

```bash
claude plugin validate /Users/mikko/github/trvl/plugin
claude plugin marketplace add /Users/mikko/github/trvl/plugin --scope user
claude plugin install trvl --scope user
```

The plugin MCP config launches `trvl mcp`, so install the `trvl` binary first:

```bash
brew install MikkoParkkola/tap/trvl
```

## Components

- Skill: `trvl-trip-planner`
- Skill: `trvl-price-watch`
- Skill: `trvl-destination-research`
- Command: `/trvl`
- Agent: `trip-coordinator`
- MCP server: `trvl`

## Worked Examples

### 1. Plan A Trip

```text
/trvl plan a weekend getaway from HEL to Prague in July for two people under 900 EUR
```

The command routes to `trvl-trip-planner`, loads the traveller profile through
the `travel` smart tool, then dispatches to `plan_trip`, `search_flights`, and
`search_hotels` compatibility aliases when useful. It runs `assess_trip` and
reports the itinerary with travel hack savings.

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
/trvl destination-research Barcelona for 2026-07-01 to 2026-07-08, food, museums, and local events
```

The command routes to `trvl-destination-research`, composing
`destination_info`, `travel_guide`, `local_events`, `nearby_places`, and
`check_visa` into one research packet.

## Underlying Tool Surface

The original MIK-3400 acceptance text refers to the 43 underlying tools
available when the plugin was proposed. The current trvl MCP server advertises
1 smart MCP tool plus 64 compatibility aliases, and this plugin is wired for
the full current surface.

Flights:
`search_flights`, `search_dates`, `suggest_dates`, `optimize_trip_dates`,
`find_trip_window`, `plan_flight_bundle`, `find_interactive`,
`search_natural`, `search_hidden_city`, `search_awards`, `plan_trip`,
`optimize_booking`.

Hotels:
`search_hotels`, `search_hotels_with_details`, `search_hotel_by_name`,
`hotel_prices`, `hotel_reviews`, `hotel_rooms`, `watch_room_availability`,
`detect_accommodation_hacks`.

Ground and multimodal:
`search_ground`, `search_route`, `search_airport_transfers`,
`optimize_multi_city`.

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
`list_providers`, `provider_health`, `suggest_providers`,
`configure_provider`, `test_provider`, `remove_provider`, plus provider status
blocks returned by search tools.

Awards and points:
`calculate_points_value`, `search_awards`, and miles earning annotations on
flight searches.

## Notes

- The plugin stays stateless; durable trip and watch state lives in trvl's own
  preference, trip, and watch stores.
- The command and skills never book travel or save preference changes without
  explicit user confirmation.
- Optional provider configuration still requires user consent through trvl.
