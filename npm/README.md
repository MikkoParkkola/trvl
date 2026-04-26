# trvl-mcp

AI travel agent MCP server. 60 tools for flights, hotels, ground transport, price alerts. No API keys required.

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

- Flight search (Google Flights, Kiwi)
- Hotel search (Google Hotels, Booking.com, Airbnb, Hostelworld, Trivago)
- Ground transport (buses, trains, ferries — 20 providers)
- Destination intelligence (weather, safety, holidays, events)
- Trip planning and price alerts
- Travel hacks detection (37 detectors)

## License

PolyForm Noncommercial 1.0.0
