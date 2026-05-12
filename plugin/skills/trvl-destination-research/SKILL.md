---
name: trvl-destination-research
description: Research a destination with trvl by composing destination facts, guide content, events, nearby places, and visa requirements.
triggers:
  - destination research
  - what to do in
  - things to see in
allowed-tools:
  - mcp__trvl__travel
  - mcp__trvl__get_preferences
  - mcp__trvl__destination_info
  - mcp__trvl__travel_guide
  - mcp__trvl__local_events
  - mcp__trvl__nearby_places
  - mcp__trvl__check_visa
  - mcp__trvl__search_restaurants
  - mcp__gateway__gateway_invoke
---

# trvl Destination Research

Use this skill when the user asks for destination research, things to see, what
to do in a place, event ideas, neighborhoods, safety, visa, or local context.

## Inputs

Load preferences first through `travel` with `intent="get_preferences"` or the
compatibility alias. Collect:

- Location.
- Travel dates or a broad month/season.
- Nationality/passport if visa checks are relevant and not in profile.
- Interests such as food, museums, nature, events, nightlife, or family travel.
- Hotel/address coordinates only when `nearby_places` should be anchored to a
  specific point.

## Workflow

1. Use `travel` with `intent="destination_info"` for weather, safety, holidays,
   timezone, currency, and country facts.
2. Use `intent="travel_guide"` for Wikivoyage-style orientation and neighborhoods.
3. Use `intent="local_events"` for concerts, sports, festivals, and exhibitions
   during the trip dates.
4. Use `intent="nearby_places"` when the user gives a hotel, coordinates,
   district, or an itinerary anchor.
5. Use `intent="check_visa"` when nationality/passport and destination country
   are known or can be inferred safely.
6. Optionally use `intent="search_restaurants"` for dining requests or dietary
   needs.

## Output

Return a single research packet:

- Practical snapshot: weather, safety, currency, timezone, holidays.
- Visa/entry status with passport and destination country named.
- Neighborhood and getting-around guidance.
- Date-specific events.
- Nearby places grouped by theme or distance when coordinates are available.
- Food or restaurant notes when requested.
- Planning risks and follow-up searches.

Do not overstate live availability; mark event, restaurant, and POI details as
current search results. If native `mcp__trvl__travel` is unavailable, use
`mcp__gateway__gateway_invoke` with `server="trvl"` and `tool="travel"`.
Exact legacy tool names remain callable as compatibility aliases.
