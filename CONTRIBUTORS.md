# Contributors

trvl is built with contributions from the community. Thank you to everyone who reports bugs, submits fixes, proposes improvements, or flags missing providers.

Contributions follow the rules in [`CONTRIBUTING.md`](CONTRIBUTING.md) — in particular the **upstream-first rule** for changes that belong in a dependency.

## External Contributors

- **@Alorse** — *Alfredo Ortegón Sepúlveda*
  - #33 — original native batchexec integration for flight search
  - #34 — wired `Currency` + `returnDate` through `FlightSearchOptions` and into MCP booking deep links
  - #43 (landed via #49) — `--first` CLI flag and `first_result` MCP parameter for single-best-priced flight results, enabling low-token price-calendar and quick-estimate workflows

---

Want to help? Good entry points:

- Issues labelled `good first issue` or `help wanted`
- Missing or broken providers — see the provider list in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- CI flakes — look for the `flaky-ci` label so you do not waste effort chasing environmental noise
