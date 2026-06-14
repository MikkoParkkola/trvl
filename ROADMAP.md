# trvl Roadmap

Living roadmap. Sequenced by ROI and dependency, not by wishlist size. Each item links a tracking issue.

## Shipped

- **v1.9.2** (2026-06-14): wrong-hotel SerpAPI guard. A name-based lookup with no Google place ID no longer returns a different property's prices labelled `verified` (reported by @RobertoReale). main healed to the released line; LCC integration (Ryanair/Wizz/easyJet) confirmed shipped.

## v1.10 — Trust & Discoverability

The batch surfaced by @RobertoReale's "Budget Travel Pipeline" blog (parts 1 & 2). Most of what the blog asked for already exists in trvl (`find_trip_window`, multi-passenger `adults`, `search_accommodations` verification, `price_basis` tax basis, anomaly `missing_criteria`), so the work is exposing and hardening it, not building net-new subsystems. Four of five touch the accommodation trust surface, so they ship as one release.

| Issue | Pri | What | Why |
|---|---|---|---|
| #167 | P1 | Docs: surface `find_trip_window`, multi-pax, verified accommodation | A power user forked `fli` and built `hotel-rates-mcp` to get what trvl already does, because the docs hid it. Highest leverage, docs-only, zero code risk. **Do first.** |
| #168 | P1 | Link-durability triage: drop dead `aclk`/`travel/clk` redirects, always emit a durable Booking.com fallback | Fulfils the "link that works" promise; an expired link sends the user to a 404. |
| #169 | P2 | Tourist/city tax as a separate cash note (never estimated, never ranked) | Completes the honesty story; one field. |
| #171 | P3 | Flag tax-added-at-checkout when shown total == pre-tax | Sharpens the existing `price_basis` signal. |
| #170 | P2 | Expose `--min-duration`/`--max-duration` on `trvl dates` CLI (engine already supports it) | Closes the exact CLI gap behind the `fli` fork. Flight-side, parallelizable. |

**Exit:** cut v1.10, then reply to @RobertoReale: the gaps he worked around are now native.

## v1.11 — Reach

| Issue | Pri | What |
|---|---|---|
| #19 / MIK-6076 | P3 | Directory submissions (mcp.so, Glama, Smithery, PulseMCP). trvl is discoverable via GitHub/pkg.go.dev but not yet broadly registry-listed; submission has real value. |
| MIK-3288 | P3 | Stealth-mode opt-in for nab + trvl: hardens the scrapers the no-key promise depends on. |

## vNext — Platform

| Issue | What |
|---|---|
| MIK-4633 | trvl-plugin epic — price-watch automation. Monitors + CronCreate + PushNotification = "tell me when this trip drops below €X". The leap from search tool to standing agent. Needs its own design spike before it is DoR-ready. |

## Meta

- **Release process**: v1.9.1 was tagged off a feature branch and never merged back to `main`, orphaning `main` 5 commits behind the released code. Fix the tag-triggered flow to merge back (or release only from `main`) before the next release repeats it.
