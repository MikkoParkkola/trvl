# Contributors

trvl is built with contributions from the community. Thank you to everyone who reports bugs, submits fixes, proposes improvements, or flags missing providers.

Contributions follow the rules in [`CONTRIBUTING.md`](CONTRIBUTING.md) — in particular the **upstream-first rule** for changes that belong in a dependency.

## External Contributors

- **@Alorse** — *Alfredo Ortegón Sepúlveda*
  - #33 — original native batchexec integration for flight search
  - #34 — wired `Currency` + `returnDate` through `FlightSearchOptions` and into MCP booking deep links
  - #42 — search-by-city flight expansion: when origin or destination is a city name, expands to all member airports automatically so deals on EIN, ANR, TKU, TLL, etc. are not missed
  - #43 (landed via #49) — `--first` CLI flag and `first_result` MCP parameter for single-best-priced flight results, enabling low-token price-calendar and quick-estimate workflows

- **@marcello-russo** — *Marcello Russo*
  - #122 — reported missing data from the `hotel_rooms` / `hotel_prices` MCP tools for Google Hotels results, which drove the hotel-price verification work
  - #142 — Booking.com integration, SerpAPI selected-property price verification, rate limiting, and coordinate/currency fixes
  - #149 — hermetic tests for 10 previously untested MCP tool handlers
  - earlier iterations (#123, #146, #147, #148) reworked into the landed versions above

---

Want to help? Good entry points:

- Issues labelled `good first issue` or `help wanted`
- Missing or broken providers — see the provider list in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- CI flakes — look for the `flaky-ci` label so you do not waste effort chasing environmental noise
