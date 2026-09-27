# Public API

trvl's public surface is the CLI and the MCP server in one binary. Packages under `internal/` are not a stable import API.

## MCP

One smart tool, `travel`, routes to the legacy tools. The compatibility window is MCP `2026-07-28` and `2025-11-25`.

Ground search (`search_ground`) returns each route with mode (`type`), price, duration, transfers, and legs. A bucket list of Iceland or the Balkans raises those cities in discover. A ground search is one origin and one destination, so it does not take a bucket list.

Flight search (`search_flights`) accepts `seat_preference` of `window` or `aisle` and writes it onto Kiwi and KLM booking URLs.

`trvl rail-pass` compares a rail pass with point-to-point fares. `trvl open-jaw` prices one flight into a city plus one ground leg out of that city. `trvl cabin-arb` flags a cabin upgrade within 15% of the cheaper fare.

## CLI

`trvl` is the command. `trvl mcp` serves MCP on stdin. `trvl version` prints the build.
