# Cross-model review findings (codex/gpt-5.6-sol) — currency honesty sweep

Reviewer: codex `gpt-5.6-sol`. Reviewed: 2026-07-13. Gate: no branch merges to
lever11 until its BLOCKERs+MAJORs are fixed and re-reviewed clean by codex.

## RF — inline rail_fly (commit 2292a54, MINE)
- [BLOCKER] rail_fly.go:181 — inconvertible rail leg keeps foreign currency but
  its cost is subtracted from target-denominated savings + labelled target.
- [BLOCKER] rail_fly_bundle.go:75 & :186 — bundle sums target flight + unconverted
  rail cost, labels mixed total as flight currency.
- [MAJOR] rail_fly_test.go:580 — test checks only labels; never verifies converted
  arithmetic or the failed-conversion drop.
STATUS: fixing inline this session (my own code).

## C — worktree-agent-a72b5bd61b7d22fe9 (5 detectors)
- [BLOCKER] error_fare.go:157 — NaivePrice already in requested currency, converted
  AGAIN from EUR. Wrong for every non-EUR request.
- [BLOCKER] night_transport.go:55 — overwrites r.Currency with target without
  converting/verifying. Reject routes whose returned currency differs.
- [MAJOR] rail_competition.go:121 — NaivePrice double-converted from EUR.
- [MAJOR] accommodation_split.go:194 — minSavingsEUR compared to target-currency
  savings; convert threshold first.
- [MAJOR] accommodation_split.go:318 — hotel Currency never checked vs requested.
- [MAJOR] departure_tax.go:124 — Savings is gross tax but text says "minus
  transport"; must subtract convGround.
- [MINOR] error_fare_test.go:181 — only EUR + unconvertible XXX; no valid non-EUR case.

## B — worktree-agent-a4e242c0a9b4c4733 (back-to-back/positioning/open-jaw/combo)
- [MAJOR] flight_combo.go:121 — cheapestFlightPriceInCurrency picks cheapest raw
  fare BEFORE conversion; mixed-currency can pick wrong flight. Convert then min.
- [MAJOR] open_jaw.go:14 & positioning.go:80 — mutable package-level search hooks
  unsynchronized; parallel test/detector race. (Note: E's DI refactor is the fix pattern.)
- [MAJOR] flight_combo_test.go:104 — no successful cross-currency test verifying
  converted amounts.
- [MINOR] positioning_test.go:109 — "inconvertible ground" test never exercises one.

## E — worktree-agent-a5c9cd49f837d79c7 (time-window/native-RT/DI)
- [MAJOR] roundtrip_native.go:371 — replacing retained result with cheaper later
  native fare breaks documented sorted order. Re-sort or insert in place.
- [MAJOR] wizzair_selfheal.go:161 — per-call host override mutates process-global
  wizzVersion; concurrent different-host searches cross-contaminate. (Agent scoped
  this OUT; codex disagrees — scope healed version by host.)
- [MAJOR] roundtrip_retain_test.go:34 — fixture not actually sorted (290 after 310),
  masking the order bug.
- [MINOR] roundtrip_retain_test.go:16 — all-EUR fixtures; no cross-currency case.

## D — worktree-agent-a282842898a867cef (test-only)
- [MAJOR x2] split_test.go:225 & multimodal_return_split_test.go:432 — assert only
  Hack.Currency, never the rendered numeric price/savings. Non-blocking (D only adds
  coverage) but the added tests should assert amounts too.

## Cross-cutting root cause (biggest potential)
Multiple detectors treat NaivePrice / provider prices as EUR and convert FROM EUR
to target, when the value is ALREADY in the requested currency. Fix once, verify
across ALL detectors with a non-EUR sweep test. VERIFY NaivePrice denomination in
DetectorInput before mass-editing.
