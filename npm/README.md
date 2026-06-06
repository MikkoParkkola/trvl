# trvl-mcp

AI travel agent MCP server. **1 smart tool, not 64** — advertises a single `travel` router (~378 tokens of context) instead of 64 separate tools (~33,500 tokens), a **98.9% smaller context footprint**. Covers flights, hotels, ground transport, price alerts, and more, dispatched by natural language. No API keys required.

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

- **1 smart `travel` MCP tool** — natural-language router; advertises one tool (~378 tokens) instead of 64 (~33,500), so your AI's context window stays lean. 64 legacy tool names remain callable as compatibility aliases via the `intent` field (set `TRVL_MCP_TOOL_MODE=legacy` to advertise all 64 for clients that require it).
- Flight search (Google Flights, Kiwi)
- Hotel search (Google Hotels, Booking.com, Airbnb, Hostelworld, Trivago)
- Ground transport (buses, trains, ferries — 20 providers)
- Destination intelligence (weather, safety, holidays, events)
- Trip planning and price alerts
- Travel hacks detection (37 detectors)

## License

PolyForm Noncommercial 1.0.0
