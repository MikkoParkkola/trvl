# Public API

trvl's public surface is the CLI and the MCP server in one binary. Packages under `internal/` are not a stable import API.

## MCP

One smart tool, `travel`, routes to the legacy tools. The compatibility window is MCP `2026-07-28` and `2025-11-25`.

Ground search (`search_ground`) returns each route with mode (`type`), price, duration, transfers, and legs. A `bucket_list` argument ranks Iceland and Balkan arrivals first.

Flight search (`search_flights`) accepts `seat_preference` of `window` or `aisle` and writes it onto Kiwi and KLM booking URLs.

`trvl railpass` compares a rail pass with point-to-point fares. An open-jaw price is `internal/openjaw.Compose`: one flight into a city plus one ground leg out of that city.

## CLI

`trvl` is the command. `trvl mcp` serves MCP on stdin. `trvl version` prints the build.
