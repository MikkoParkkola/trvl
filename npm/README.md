# trvl-mcp

AI travel agent MCP server. **1 smart tool, not 66** — advertises a single `travel` router (~378 tokens of context) instead of 66 separate tools (~33,500 tokens), a **98.9% smaller context footprint**. Covers flights, hotels, ground transport, price alerts, and more, dispatched by natural language. No API keys required for default discovery.

Hotel trust rule: use `search_accommodations` for traveller-facing stay recommendations. Raw `search_hotels` prices are lead-in discovery prices. Before ranking a final trip cost or telling a user what to book, use criteria-matched room/apartment offers from `search_accommodations` or verify shortlisted hotels with room/detail/provider totals (`search_hotels_with_details`, `hotel_rooms`, or `trvl serpapi` when `SERPAPI_KEY` is configured). trvl provides booking handoff links; it does not book, hold, or guarantee hotel rates.

## Usage

```
npx trvl-mcp
```

## Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "trvl": {
      "command": "npx",
      "args": ["trvl-mcp"]
    }
  }
}
```

## What's included

- **1 smart `travel` MCP tool** — natural-language router; advertises one tool (~378 tokens) instead of 66 (~33,500), so your AI's context window stays lean. 66 legacy tool names remain callable as compatibility aliases via the `intent` field (set `TRVL_MCP_TOOL_MODE=legacy` to advertise all 66 for clients that require it).
- Flight search (Google Flights, Kiwi)
- Criteria-first accommodation search plus hotel discovery (Google Hotels, Booking.com, Airbnb, Hostelworld, Trivago) and verified room/detail flows for shortlisted candidates
- Ground transport (buses, trains, ferries — 20 providers)
- Destination intelligence (weather, safety, holidays, events)
- Trip planning and price alerts
- Travel hacks detection (37 detectors)

## License

PolyForm Noncommercial 1.0.0
