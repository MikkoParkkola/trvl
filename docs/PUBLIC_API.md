# Public API

trvl's public surface is the CLI and the MCP server in one binary. Packages under `internal/` are not a stable import API.

## MCP

One smart tool, `travel`, routes to the legacy tools. The compatibility window is MCP `2026-07-28` and `2025-11-25`.

Ground search (`search_ground`) returns each route with mode (`type`), price, `price_provenance` (`live`, `estimated` or `unavailable`), duration, transfers, and legs when they add stops the route does not show. It returns at most 20 routes per direction, best-ranked first, and fewer when the result would pass 50 KB; there is no limit argument. `count` is the number returned and `total_count` the number found, and the text summary says how many more exist and that `type`, `provider` or `max_price` narrow them. The summary marks estimates as `~€22 (est.)` and routes without a fare as `price unavailable`; live fares are unmarked. Each route leaves out what repeats it: a one-entry `sources` array, a lone leg identical to the route, leg cities already named, and the confidence `rated` and `freshness` fields. A bucket list of Iceland or the Balkans raises those cities in discover. A ground search is one origin and one destination, so it does not take a bucket list.

Flight search (`search_flights`) accepts `seat_preference` of `window` or `aisle` and writes it onto Kiwi and KLM booking URLs.

`trvl rail-pass` compares a rail pass with point-to-point fares. `trvl open-jaw` prices one flight into a city plus one ground leg out of that city. `trvl cabin-arb` flags a cabin upgrade within 15% of the cheaper fare.

## CLI

`trvl` is the command. `trvl mcp` serves MCP on stdin. `trvl version` prints the build.
