# trvl Roadmap

Living roadmap. Sequenced by ROI and dependency, not by wishlist size. Each item links a tracking issue.

## Shipped

- **v1.18.0** (2026-06-28): current release line. Booking-readiness verdict from composed trust signals, per-package weather/holidays/events enrichment with typed status, native Google round-trip flight queries, privacy-preserving daily active-user heartbeat. See [CHANGELOG.md](CHANGELOG.md).
- **v1.10 — Trust & Discoverability** (shipped, v1.10.0 2026-06-14): the batch surfaced by @RobertoReale's "Budget Travel Pipeline" blog. All five items landed and their issues are closed:
  - #167 (P1) — docs surfacing `find_trip_window`, multi-pax, verified accommodation.
  - #168 (P1) — link-durability triage: dead `aclk`/`travel/clk` redirects dropped, durable Booking.com fallback always emitted.
  - #169 (P2) — tourist/city tax as a separate cash note (never estimated, never ranked).
  - #170 (P2) — `--min-duration`/`--max-duration` exposed on `trvl dates`.
  - #171 (P3) — tax-added-at-checkout flagged when shown total == pre-tax.
- **v1.11 — Reach** (shipped): #19 directory submissions (mcp.so, Glama, Smithery, PulseMCP) closed; trvl is now registry-listed. Stealth-mode opt-in for nab + trvl landed.
- **v1.9.2** (2026-06-14): wrong-hotel SerpAPI guard. A name-based lookup with no Google place ID no longer returns a different property's prices labelled `verified` (reported by @RobertoReale). LCC integration (Ryanair/Wizz/easyJet) confirmed shipped.

## vNext — Platform

| Issue | What |
|---|---|
|  | trvl-plugin epic — price-watch automation. Monitors + CronCreate + PushNotification = "tell me when this trip drops below €X". The leap from search tool to standing agent. Needs its own design spike before it is DoR-ready. |

## Meta

- **Release process**: v1.9.1 was tagged off a feature branch and never merged back to `main`, orphaning `main` 5 commits behind the released code. The tag-triggered flow should merge back (or release only from `main`) to avoid repeating it.
