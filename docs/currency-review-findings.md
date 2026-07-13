# Cross-model review findings (codex/gpt-5.6-sol) — currency honesty sweep

Reviewer: codex `gpt-5.6-sol`. Reviewed: 2026-07-13. Gate: no branch merges to
lever11 until its BLOCKERs+MAJORs are fixed and re-reviewed clean by codex.

## Release gate — FULL-REPO DoD compliance (BLOCKING, operator directive 2026-07-13)

The codebase is expected to be DoD-compliant AT ALL TIMES, not just at tag time.
Every branch merge into lever11 AND the v1.20.0 tag are gated on the ENTIRE trvl
tree passing DoD, not merely the touched detectors:

- §3 static / 0-bug: `GOTOOLCHAIN=go1.26.5 go vet ./...` + `staticcheck ./...` +
  `govulncheck ./...` clean repo-wide (zero errors = stop-the-line).
- §4/§6 testing+regression: `GOTOOLCHAIN=go1.26.5 go test ./... -race -count=1` green
  on the full tree; NEW tests exist AND pass; coverage >=80% (`make test-coverage`).
- H1-H11 hygiene: no orphans / dead code / duplicate fns; `internal/hacks/currency.go`
  seam consolidated (single declaration, not per-branch dupes); findings doc tracked.
- §5 locks: default suite offline+deterministic (the LOCK codex caught C violating);
  `internal/models` import direction intact; MCP 2025-11-25 unbroken.
- §1/§12 evidence+review: cross-model codex review clean on every branch; AC pass/fail
  + gate verdicts posted to the release issue before close.
- D22-D30: supply-chain audit at merge; no secrets; no default API keys.

A red ANYWHERE in `./...` (including pre-existing violations in untouched packages
surfaced by the full pass) blocks the merge/release and is fixed in THIS release,
never merged over.


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

## G — DetectAll context-honoring + live-call leak (found via self-review, pre-existing)
Discovered 2026-07-13 running `go test ./internal/hacks/ -race` on lever11 base.
- [BLOCKER] internal/hacks/benchmark_test.go:207 TestDetectAll_CancelledContext and
  :233 TestDetectAll_DeadlineExceeded FAIL: DetectAll with an already-cancelled /
  1ms-deadline context takes ~820ms (expected <500ms). DetectAll does not check
  ctx.Err() before dispatching detectors, so live provider calls (skiplagged,
  wizzair, rome2rio, agoda, spotahome, ferryhopper, wunderflats) still fire.
- [LOCK VIOLATION] the default (non-live-env) hacks suite makes real network calls
  via DetectAll → non-deterministic; skiplagged 429s (self-inflicted rate-limit)
  push elapsed over the 500ms bar. LOCK: default suite must be offline/deterministic.
- Root fix: DetectAll must short-circuit on ctx.Err() before/inside the dispatch
  loop so a cancelled/expired context returns promptly and issues no provider calls.
  This also makes the two context tests deterministic + offline.
- Likely CI-green today (CI egress to these hosts fast-fails <500ms); real bug is
  masked by environment. Separate from the currency theme; fixed in its own branch.
STATUS: dispatched dedicated fix agent (own worktree off lever11), codex re-review after.

## Cross-cutting root cause (biggest potential)
Multiple detectors treat NaivePrice / provider prices as EUR and convert FROM EUR
to target, when the value is ALREADY in the requested currency. Fix once, verify
across ALL detectors with a non-EUR sweep test. VERIFY NaivePrice denomination in
DetectorInput before mass-editing.

## Codex re-review verdicts (2026-07-13, round 2)

Reviewer: codex gpt-5.6-sol, one runner subagent per branch (relays codex verdict only).

- **E (5e9ec46/c49fe5c/4f94424)** — VERDICT: CLEAN. All 4 findings RESOLVED
  (re-sort via compareFlightPrices; wizz healed-version scoped per-host under mutex;
  fixtures genuinely ascending + assertPriceSorted; cross-currency incl XXX covered).
  No new issues; codex ran focused -race, passed. MERGE-READY.
- **C (8fa2886)** — VERDICT: CHANGES-REQUIRED. 6/7 findings RESOLVED (error_fare
  double-convert, night_transport currency reject, rail_competition, accommodation_split
  x2, departure_tax net-of-transport all fixed). NOT-RESOLVED: #7 error_fare needs an
  injectable conversion-rate seam for a deterministic non-EUR regression test. NEW MAJOR:
  TestDetectDepartureTax_nonEURTarget is network-dependent (calls prod ConvertCurrency for
  both execution + expectation) — LOCK violation. FIX IN PROGRESS: seam impl agent
  (convertCurrency package var, mirrors railGroundSearcher) + deterministic fake-rate tests
  for error_fare + departure_tax. Re-review after.
  - SEAM DONE @9b33255: convertCurrency package var; all 7 call sites routed; deterministic
    fake-rate tests for error_fare (no-double-conversion) + departure_tax; bonus: fixed a
    genuinely-live suppressed-test that warmed the shared rate cache. Offline green, -race PASS.
  - FOLLOW-UP DONE @6fdbb13: inconvertible-currency tests in night_transport_test.go +
    rail_competition_test.go now inject never-convert fake seam; both 0.00s offline, -race PASS.
  - PENDING: codex final re-review of full C stack (8fa2886->9b33255->6fdbb13) dispatched.
- **DetectAll (2491e91)** — VERDICT: CLEAN. Prompt nil-return on pre-cancelled ctx
  (hacks.go:344); concurrent ctx.Err safe; all 3 callers compile unchanged + handle nil
  slices; no nil-deref. codex ran -race -count=10 on both target tests + mcp/cmd pkgs, PASS.
  MERGE-READY.
- **B (redo)** — 5 commits landed green (positioning/open_jaw/back_to_back/flight_combo
  + cheapestFlightPriceInCurrency-in-target + SearchOverride plumbing replacing racy
  pkg-global search vars). Audit of B surfaced 4 MORE detectors with the same
  currency-mixing defect (ferry_positioning, multimodal_positioning,
  multimodal_open_jaw_ground, multimodal_skip_flight) + the fact that
  internal/ground.SearchOptions lacks a SearchOverride seam. Fix agent dispatched into
  B's worktree (convert-before-arithmetic + drop-on-inconvertible, ground override seam,
  offline tests). codex review of the full B stack pending after that lands.

## Codex re-review verdicts (2026-07-13, round 3)

- **C final (8fa2886->9b33255->6fdbb13)** — VERDICT: CHANGES-REQUIRED. Confirmed
  RESOLVED: GBP seam test proves NaivePrice stays GBP 20 + correct Savings/Currency;
  seam defaults to destinations.ConvertCurrency (behavior-preserving); seam-mutating
  tests restore sequentially, -race clean. STILL OPEN:
  (1) LOCK VIOLATION — accommodation_split inconvertible-currency test still invokes the
  live default converter (~0.57s observed = real network call); must inject the
  never-convert fake seam like night_transport/rail_competition.
  (2) NEW MAJOR — detectErrorFare compares target-currency NaivePrice against unconverted
  EUR thresholds (floorEUR/errorThreshold/flashThreshold), so non-EUR targets (JPY etc.)
  misclassify and mis-discount; must convert thresholds into target before comparison.
  FIX IN PROGRESS: dedicated agent in C's worktree (threshold conversion via seam +
  fake-seam offline accommodation test + non-EUR error_fare regression). Re-review after.
  - FIX LANDED: 9b45942 (detectErrorFare converts floorEUR/typicalEUR into target via
    seam BEFORE comparing target-denominated NaivePrice; discount uses converted
    dispTypical; EUR unchanged via from==to short-circuit) + fc84d96 (accommodation_split
    inconvertible test injects never-convert fake seam, offline). New test
    TestDetectErrorFare_nonEURTarget_JPY_classifiesCorrectly (EUR->JPY@130). Scoped -race
    run: 20 tests PASS @0.00s, no network WARN. NOTE: full-package run still fails
    TestDetectAll_CancelledContext/_DeadlineExceeded — these are the SAME live-timing tests
    the DetectAll branch (2491e91) fixes; C's worktree predates that merge, so they clear
    on lever11 post-merge. Confirmed pre-existing on base 6fdbb13 via git stash. codex
    round-4 review of 9b45942+fc84d96 DISPATCHED.
