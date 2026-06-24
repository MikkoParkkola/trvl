# TheFork API Map — Key-Free Stealth-Tier Discovery (MIK-2949)

**Status: BLOCKED by DataDome behavioural CAPTCHA. No connector shipped. Docs-only,
fail-fast outcome — plus one honest detection fix (DataDome is now recognized as a
challenge page).**

This document records what was **actually observed** when driving trvl's own stealth
browser machinery (`internal/providers/tier2_chromedp.go` + `challenge.go`, and the
Chrome-TLS-impersonating `tier1_client.go`) against TheFork on **2026-06-24**, to try
to reach the JSON API behind the restaurant-search SPA **without any API keys**.

It supersedes the prior discovery pass (PR #314, headed `agent-browser`), which was
walled at the root document. This pass used the repo's real stealth tier with an
extended behavioural warm-up. The wall held. Every endpoint shape below is from
captured traffic; nothing is guessed or fabricated. The prior pass's HAR evidence
remains at `docs/providers/thefork-capture-2026-06-24.evidence.json`.

## What was attempted (this pass — the key-free route)

Three independent vectors against `https://www.thefork.com/` and the Amsterdam search
route `https://www.thefork.com/search?cityId=415144`:

| # | Vector | Machinery | Warm-up |
|---|--------|-----------|---------|
| 1 | **Headless Chrome via CDP** | same as `runCDPChallenge` (`chromedp`, user's installed Chrome, `chromedp.Headless`) | 12 s device-check window + 6 synthetic mouse-move events + scroll-down/scroll-up + 36 s total dwell |
| 2 | **Headed Chrome via CDP** | identical, `Headless` removed (real visible window) | identical warm-up |
| 3 | **Tier-1 Chrome-TLS client** | `providers.NewTier1ClientForURL` (JA3/JA4 + HTTP/2 Chrome fingerprint via `bogdanfinn/tls-client`), seeded with 6 real browser cookies | n/a (single round-trip) |

A fourth vector — driving the user's **real Chrome profile** (`--user-data-dir`) to
carry any human-warmed `datadome` cookie — could not run: the user's Chrome was already
holding the profile's `SingletonLock`, and forcing it risks profile corruption. It is
noted under "What a key-free pass would require".

## What was observed

The TheFork SPA **never loaded** on any vector. All three were walled by DataDome.

### Vector 1 & 2 (CDP, headless and headed) — identical outcome

- Document navigation returned **HTTP 403**.
- Rendered page was the **DataDome interstitial**, not the TheFork app: page title
  `"thefork.com"`, body is the DataDome bootstrap
  `var dd={'rt':'c', … ,'t':'fe','host':'geo.captcha-delivery.com', …}` followed by a
  `<iframe … title="DataDome CAPTCHA" …>` pointing at
  `geo.captcha-delivery.com/captcha/?…&t=fe&…`.
- `'t':'fe'` = DataDome **full device-check** that escalates to the visual/behavioural
  CAPTCHA; the iframe is the CAPTCHA itself.
- A `datadome` cookie (128 chars, domain `.thefork.com`) **was** issued — but it is the
  *unsolved challenge* cookie, not a cleared-session cookie. It does not unlock content.
- Captured network on each run (4 requests):

  | Status | Type | URL |
  |--------|------|-----|
  | 403 | Document | `https://www.thefork.com/search?cityId=415144` |
  | 200 | Script | `https://ct.captcha-delivery.com/c.js` |
  | 200 | Document | `https://geo.captcha-delivery.com/captcha/?…&t=fe&…` |
  | 200 | Other | `https://www.thefork.com/favicon.ico` |

- **Zero** TheFork application/API requests (no GraphQL, no `/api/`, no `_next/data`,
  no XHR/fetch to any `thefork.com` JSON endpoint). The SPA JS never executed past the
  interstitial. There is nothing real to document for search / detail / reviews.

### Vector 3 (Tier-1 Chrome-TLS impersonation) — also 403

Confirms the block is **not merely a headless tell** — it sits at the edge before any
fingerprint nuance matters:

| URL | Status | DataDome in body | `IsChallengePage` (before fix) |
|-----|--------|------------------|--------------------------------|
| `https://www.thefork.com/` | **403** | yes | false ← detection gap |
| `https://www.thefork.com/search?cityId=415144` | **403** | yes | false ← detection gap |

The 403 body is the DataDome interstitial with `'t':'bv'` (behavioural-verification
CAPTCHA). Frozen, PII-free, at
[`internal/providers/testdata/thefork_datadome_interstitial.html`](../../internal/providers/testdata/thefork_datadome_interstitial.html).
This is **real captured traffic** (the block page), not a fabricated API response.

## The wall, precisely

- Vendor: **DataDome** (`*.captcha-delivery.com`).
- Placement: on the **root/protected document itself** (403 body = interstitial),
  at the **edge/CDN layer**, *before* any TheFork application or auth layer.
- Mechanism: JS/blob device-attestation that issues a signed `datadome` session cookie
  only after passing browser-environment checks, escalating to a visual/behavioural
  CAPTCHA (`t=fe` device-check, `t=bv` behavioural verification).
- Why CDP-driven Chrome fails (headless *and* headed): a `chromedp`-driven browser
  carries the CDP automation signal (`navigator.webdriver`, DevTools `Runtime` hooks)
  that DataDome detects regardless of window visibility. This is exactly why the repo's
  `challenge.go` already classifies DataDome as **`ChallengeNeedsHuman`** — a headless
  browser cannot clear it without a human gesture.
- Why Tier-1 (TLS impersonation) fails: a perfect TLS/HTTP2 Chrome fingerprint is
  necessary but not sufficient — DataDome requires the *client-side JS attestation*, not
  just a browser-shaped handshake, so a single HTTP round-trip cannot replay it.

## Auth posture for discovery endpoints

**Undetermined — and unreachable.** Search / detail / reviews could not be observed
because the SPA never loaded. The gate is at the device-attestation layer *before* any
application/auth layer. Whether the underlying discovery API is anonymous-session or
token-gated is moot until DataDome is solved, which no key-free path here achieves.

## Verdict (fail-fast per MIK-2949 ABSOLUTE RULES)

> "if the stealth tier CANNOT pass DataDome's behavioural CAPTCHA (`t=bv`) key-free …
> STOP. … That is an honest SUCCESS, not a failure."

This condition is met across all three runnable vectors. **No connector was built.** No
discovery fixtures were fabricated (there is no real captured discovery response to
freeze — only the block page, which *is* frozen as evidence).

## Shipped (honest, tested, non-fabricated)

1. **DataDome challenge detection** — `IsChallengePage` (`internal/providers/tier1_client.go`)
   now recognizes the DataDome 403 interstitial (`captcha-delivery.com`, `var dd={`
   body markers). Before this pass it returned `false` for DataDome 403s, so the
   Tier-1 → Tier-2 escalation signal never fired for DataDome-protected hosts; the 403
   was silently treated as a hard failure. Now the caller escalates (and `challenge.go`'s
   existing `DetectInteractiveCaptcha` correctly tags it `NEEDS_HUMAN`).
2. **Frozen real fixture** — `internal/providers/testdata/thefork_datadome_interstitial.html`
   (verbatim 403 body, per-session tokens redacted), backing
   `TestIsChallengePage_TheForkDataDomeFixture`.

## Replay probe (plain HTTP client, no browser) — from the prior pass

To confirm the wall is not merely browser-fingerprint specific, candidate hosts were
probed directly. **These URLs are hand-guessed candidates, NOT observed traffic** — all
returned 403 (or were unresolvable), so none are confirmed endpoints:

| Probe URL (guessed) | Result |
|---------------------|--------|
| `https://www.thefork.com/` | 403 |
| `https://www.thefork.com/api/graphql` | 403 |
| `https://api.thefork.com/graphql` | 403 |
| `https://graphql.thefork.com/` | unresolved (000) |
| `https://www.thefork.com/_next/data` | 403 |

## Parked (out of scope / still blocked)

- TheFork discovery connector — **blocked**: no observable discovery endpoints behind
  DataDome.
- Reservations / any write path — parked per ticket scope.

## What a key-free pass would require

None of these are in-repo today; all are materially larger than a JSON connector and
some conflict with the no-keys / no-paid-service product principle:

1. **A real, human-warmed browser profile** carrying an already-solved `datadome` session
   cookie, driven *without* the CDP automation tell (e.g. a non-CDP attach to a normally
   launched Chrome, or `--user-data-dir` on a profile not currently locked by the user's
   live Chrome). This pass could not test it (profile lock); it is the most promising
   key-free lever but is fragile and per-machine.
2. **A CDP-evasion layer** the repo does not have — patching `navigator.webdriver`, the
   `Runtime.enable` leak, and CDP timing signals (the "undetected-chromedriver" class of
   evasions). Even then, DataDome's `t=bv` behavioural CAPTCHA may still demand a human.
3. **A residential/mobile-IP proxy** plus a behavioural warm-up long enough to satisfy
   DataDome's risk score — infrastructure, not a static binary, and not key-free in
   spirit.
4. **Solving the `t=bv` CAPTCHA**, which requires either a human or a paid CAPTCHA-solving
   service. The latter is explicitly **forbidden** by MIK-2949 (violates key-free).

Until one of (1)–(3) is built and *verified* to yield a cleared session that lets the SPA
load, there is no honest key-free TheFork connector to ship.
