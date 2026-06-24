# TheFork API Map — Discovery Capture (MIK-2949)

**Status: BLOCKED by anti-bot. No connector shipped. Docs-only, fail-fast outcome.**

This document records what was **actually observed** in a live network capture on
**2026-06-24**, when attempting to reverse-engineer the JSON API behind the TheFork
restaurant search SPA (Phase 1 of MIK-2949). It is built only from captured traffic.
No endpoint shapes are guessed or fabricated; where I tried candidate URLs by hand,
that is labelled explicitly and the result was a uniform 403.

## What was attempted

- Tool: `agent-browser` (headed Chrome via CDP), full HAR capture.
- Target: `https://www.thefork.com/` → intent to search restaurants in **Amsterdam**,
  apply a filter, open one restaurant detail page.
- HAR evidence (trimmed, no PII): `docs/providers/thefork-capture-2026-06-24.evidence.json`.

## What was observed

The TheFork SPA **never loaded**. The very first navigation was blocked:

| # | Status | Method | URL (host/path) | Note |
|---|--------|--------|-----------------|------|
| 1 | **403** | GET | `https://www.thefork.com/` | Initial document blocked outright |
| 2 | 200 | GET | `ct.captcha-delivery.com/i.js` | DataDome bot-defense loader |
| 3 | 200 | GET | `geo.captcha-delivery.com/interstitial/?initialCid=…` | DataDome "Device Check" interstitial |
| 4 | 200 | GET | `www.thefork.com/favicon.ico` | — |
| 5 | — | GET | `blob:…geo.captcha-delivery.com/…` | DataDome challenge script (blob) |
| 6 | 200 | POST | `geo.captcha-delivery.com/interstitial/` | DataDome challenge solve attempt |
| 7 | 200 | GET | `geo.captcha-delivery.com/captcha/?…&t=bv&…` | DataDome CAPTCHA escalation |
| 8–11 | 200 | GET | `static.captcha-delivery.com/…` | CAPTCHA template CSS / fonts / logo |

The rendered page is a **DataDome "Device Check" iframe**, not the TheFork app. Page
body content at capture time was the DataDome bootstrap object
(`var dd={'rt':'i','cid':'…','host':'geo.captcha-delivery.com', …}`), confirming the
provider behind the wall is **DataDome** (`*.captcha-delivery.com`).

**Zero** TheFork application/API requests were observed: no GraphQL, no `/api/`,
no `_next/data`, no XHR/fetch to any `thefork.com` JSON endpoint. Because the SPA's
JavaScript never executed past the interstitial, there is nothing real to document
for search / restaurant-detail / reviews endpoints. I will not invent them.

## Replay probe (plain HTTP client, no browser)

To confirm the wall is not merely browser-fingerprint specific, I probed candidate
hosts directly. **These URLs are hand-guessed candidates, NOT observed traffic** —
all returned 403 (or unresolvable), so none are confirmed endpoints:

| Probe URL (guessed) | Result |
|---------------------|--------|
| `https://www.thefork.com/` | 403 |
| `https://www.thefork.com/api/graphql` | 403 |
| `https://api.thefork.com/graphql` | 403 |
| `https://graphql.thefork.com/` | unresolved (000) |
| `https://www.thefork.com/_next/data` | 403 |

## Auth posture for discovery endpoints

**Undetermined — and unreachable.** Discovery (search / detail / reviews) could not be
observed because the SPA never loaded. The gate is at the **edge / CDN layer (DataDome
device attestation)**, *before* any application or auth layer. Whether the underlying
discovery API is anonymous-session or token-gated is moot until the DataDome challenge
is solved, which a plain HTTP client cannot replay.

## Anti-bot / what blocks replay

- **DataDome** device-check + CAPTCHA on the root document itself (`*.captcha-delivery.com`).
- The challenge is a JS/blob-based device-attestation flow that issues a signed
  `datadome` cookie only after passing browser-environment checks; it escalates to a
  visual/behavioural CAPTCHA (`t=bv`, `dc_ir`).
- This cannot be replayed from a plain `net/http` client: there is no static API key
  or replayable token — the gate requires solving a per-session device challenge.

## Verdict (fail-fast per MIK-2949 ABSOLUTE RULES)

> "if unauthenticated discovery calls are blocked by device-attestation / … hard
> bot-defense that cannot be replayed from a plain HTTP client, STOP building the
> connector."

This condition is met. **No connector was built.** No fixtures were frozen (there is
no real captured discovery response to freeze — fabricating one is forbidden).

### Parked (out of scope / blocked)

- Phase 1 connector — **blocked**: no observable discovery endpoints behind DataDome.
- Phase 2 (airlok auth), Phase 4 (reservations), Phase 5 (Botnaut translation) —
  parked per ticket scope.

### If revisited later

Replay would require a path that legitimately solves/holds the DataDome session
(e.g. a real browser-driven session via the repo's existing `tier2_chromedp` /
`internal/stealth` machinery feeding a captured `datadome` cookie + matching TLS/UA
fingerprint into the JSON calls), and re-capturing the SPA's real XHR/fetch traffic
*after* the wall is passed. That is a materially different effort than a plain-client
JSON connector and was explicitly out of scope for this discovery pass.
