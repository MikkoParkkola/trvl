# trvl Roadmap

Living roadmap. Sequenced by ROI and dependency, not by wishlist size. Each item links a tracking issue.

## Shipped

- **v1.21.3** (2026-08-11): current release line. Fixed destination-integrity failures across rental providers, made Flatio fallback results fail closed on unrelated inventory, and disabled prompt-capable Keychain access for background Nab fetches. See [CHANGELOG.md](CHANGELOG.md) and the [GitHub release](https://github.com/MikkoParkkola/trvl/releases/tag/v1.21.3).
- **v1.21.0** (2026-08-08): added source-backed cancellation and refundability evidence, an offer-specific booking-readiness ceiling, browser-cookie and headless-browser opt-outs, source-only optional provider definitions, bounded transactional price-watch storage, private-proxy handling, and release/security hardening.
- **v1.18.0** (2026-06-28): booking-readiness verdict from composed trust signals, per-package weather/holidays/events enrichment with typed status, native Google round-trip flight queries, and a privacy-preserving daily active-user heartbeat.
- **v1.10 — Trust & Discoverability** (shipped, v1.10.0 2026-06-14): the batch surfaced by @RobertoReale's "Budget Travel Pipeline" blog. All five items landed and their issues are closed:
  - #167 (P1) — docs surfacing `find_trip_window`, multi-pax, verified accommodation.
  - #168 (P1) — link-durability triage: dead `aclk`/`travel/clk` redirects dropped, durable Booking.com fallback always emitted.
  - #169 (P2) — tourist/city tax as a separate cash note (never estimated, never ranked).
  - #170 (P2) — `--min-duration`/`--max-duration` exposed on `trvl dates`.
  - #171 (P3) — tax-added-at-checkout flagged when shown total == pre-tax.
- **v1.11 — Reach** (shipped): #19 directory submissions (mcp.so, Glama, Smithery, PulseMCP) closed; trvl is now registry-listed. Stealth-mode opt-in for nab + trvl landed.
- **v1.9.2** (2026-06-14): wrong-hotel SerpAPI guard. A name-based lookup with no Google place ID no longer returns a different property's prices labelled `verified` (reported by @RobertoReale). LCC integration (Ryanair/Wizz/easyJet) confirmed shipped.

## Next

There are currently no open GitHub issues committed to the next release. New work starts as a scoped issue with acceptance criteria; the [issue tracker](https://github.com/MikkoParkkola/trvl/issues) is the live source of truth.

## Meta

- **Release process**: releases are cut from `main`. The tag-triggered workflow runs the security gate, builds and signs artifacts, publishes GitHub/npm/Docker/MCP Registry channels, and updates the Homebrew formula. The v1.9.1 feature-branch tagging failure is retained in history as the reason for the `main`-only rule.
