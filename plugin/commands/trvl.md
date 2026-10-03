---
description: Route trvl travel requests to trip planning, price watches, destination research, or advanced MCP tools.
argument-hint: "[plan|price-watch|destination-research|tool] travel request"
---

# /trvl

Route travel requests to the trvl plugin workflow.

## Intent Routing

- Trip planning, vacation, holiday, weekend, itinerary, flights plus hotel:
  use `trvl-trip-planner`.
- Price watch, fare monitor, hotel deal alert, room availability, rolling deal
  scan: use `trvl-price-watch`.
- Destination research, what to do in a city, things to see, events, visa,
  local context: use `trvl-destination-research`.
- Multi-city, multi-leg, open-jaw, rail plus fly, or high-level complex trip:
  delegate to the `trip-coordinator` agent when available.

## Fallback

When the user is advanced, asks for a specific trvl tool, or the intent does
not match the three skills, this command falls back to MCP tool selection.
Prefer native `mcp__trvl__<tool>` calls. If native tools are not loaded, call
`mcp__gateway__gateway_invoke` with `server="trvl"` and the requested tool.
This plugin does not start the trvl server. Those tools exist only when the
separately installed trvl program is already connected, or when a gateway
already exposes it. If neither is available, tell the user to install trvl
and connect it under the name `trvl`:

```bash
brew install MikkoParkkola/tap/trvl
claude mcp add --scope user --transport stdio trvl -- trvl mcp
```

`trvl mcp install` without `--client claude-code` targets Claude Desktop.
Do not offer that default.

## Response Contract

Always load the traveller profile first when a trvl profile tool is available.
Ask at most three targeted questions before searching. Present results as a
decision: best option, cost, tradeoffs, risks, and next action.
