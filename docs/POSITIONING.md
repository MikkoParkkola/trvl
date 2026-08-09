# trvl — Positioning

> **A local-first travel MCP server. 1 smart tool, 66 legacy-compatible capabilities, 24 transport providers, no personal API keys for default search, one binary.**

Last updated: 2026-08-09

---

## 1. Tagline

**trvl makes your AI assistant a competent travel agent.**

trvl gives an agent structured access to 24 transport providers, 10 flight integrations, 6 hotel sources, and destination-enrichment services through one compact `travel` MCP tool plus 66 legacy-compatible capabilities. Current MCP clients such as Claude, Cursor, Windsurf, and Codex can call it directly.

## 2. The problem we solve

General-purpose AI assistants often fail at travel in the same three ways:

1. **Stale knowledge.** Models trained months ago don't know today's prices, schedules, or award charts.
2. **Unstructured web results.** Browsing can find useful pages, but it does not provide a stable schema for prices, provider failures, room evidence, or booking handoff.
3. **No repeatable multi-provider workflow.** A general web-search agent has no stable schema for comparing provider results, saved preferences, watches, and evidence across calls.

trvl addresses those problems with provider-backed searches, typed partial-failure states, local traveller data, and explicit booking-readiness evidence.

## 3. Who this is for (ICP)

| Tier | Profile | What they get |
|---|---|---|
| **Primary** | AI-assistant power users (Claude / Cursor / Windsurf) who plan several trips per year | A repeatable trip-planning workflow with provider-backed data and explicit evidence |
| **Secondary** | AI-app builders integrating travel intent (booking concierges, expense automation, corporate travel agents) | A single-binary backend whose default search does not require personal provider keys |
| **Tertiary** | Developers comparing travel MCP servers in public registries | A local, single-binary option with broad transport coverage |

## 4. Who this is NOT for (anti-positioning)

- **Humans who book through a website.** Use Google Flights. trvl serves *agents*, not direct human UIs.
- **Travel agencies wanting managed white-label SaaS.** trvl has no hosted account, billing, or support operation.
- **Single-flight one-shot lookups.** Kayak is fine for that. trvl earns its keep when an agent runs 10+ tool-calls per query.

## 5. Value triangle

```
                 24 transport providers
                       /\
                      /  \
                     /    \
                    /      \
                   /        \
 No personal keys-/----------\-- Agent-native
  for defaults    \          /   (1 smart tool,
    free tier,     \        /    structured I/O,
    one binary)     \      /     not screen scrape)
                     \    /
                      \  /
                       \/
                  Browser fallback
              (Booking.com, AFKLM, ...)
```

| Pillar | Why it matters | Evidence |
|---|---|---|
| 24 transport providers | Broad ground, ferry, transfer, and rental-car coverage gives the agent alternatives across modes. | [Provider reference](PROVIDERS.md) |
| No personal keys for default search | The default flight, hotel, and ground paths work without asking the user to create an API account. Optional integrations document their own requirements. | [Provider reference](PROVIDERS.md) |
| Agent-native | Structured tool I/O beats HTML scraping for agent reliability. | [AGENTS.md](../AGENTS.md) — 1 smart tool, 66 legacy-compatible capabilities, typed schemas |
| Browser fallback | Some protected providers use browser cookies or a headless browser. Both behaviours and their opt-outs are documented. | [Privacy and local-state documentation](../README.md#what-trvl-reads-and-what-it-keeps) |
| One binary | `brew install`, done. No Docker, no Python venv, no Node toolchain. | `goreleaser` artifacts, all platforms |

## 6. Versus the real alternatives

The maintained head-to-head matrix lives in [COMPARISON.md](COMPARISON.md). It compares trvl against Google Flights, KAYAK, ChatGPT-with-search, and other travel MCPs, with every support/unsupported cell linked to source evidence.

| Alternative | What it is | Where trvl wins |
|---|---|---|
| **fli** | Python library and CLI for Google Flights data | trvl keeps the Google Flights-style workflow but adds local MCP install, hotels, ground, watches, awards, hacks, and assistant skills |
| **Skiplagged MCP** | Official remote MCP for Skiplagged flights, hotels, and rental cars | trvl is local-first and broader across Google Flights, Kiwi, hotels, ground, awards, profile, and watch workflows; rental cars now ship via `trvl cars` and the `search_cars` tool (optional Skyscanner Car Hire) |
| **1Stay/stays** | Transaction-complete hotel booking MCP | trvl is broader and safer for local assistants; it deliberately stops at provider URLs and booking-readiness checks rather than taking payment/cancellation liability |
| **Google Flights / KAYAK (web)** | Consumer search UIs | trvl is callable through MCP and adds saved local workflows; the websites retain the stronger visual browsing and payment handoff experience |
| **ChatGPT browse + travel sites** | LLM searches and summarizes web pages | trvl provides a stable travel schema and deterministic watch, award, and provider-comparison workflows |
| **Other travel MCPs** | Many focus on one provider or one travel domain | trvl combines 24 transport providers with separate flight and hotel rosters in one binary |
| **Travel-agent SaaS (Hopper, etc.)** | Paid consumer app | trvl is source-available, local-first, and embeddable rather than a hosted account product |

## 7. Proof points

- 1 smart MCP tool plus 66 legacy-compatible capabilities live on `main` ([tool list](../AGENTS.md))
- Traveller Workspace v2 adds confirmation import, booking-candidate readiness, itinerary route-time warnings, and conservative fare intelligence without automatic purchase claims ([workspace docs](traveller-workspace.md)).
- Hotel detail enrichment surfaces best-effort room cancellation/refundability, board/breakfast, nightly-vs-total pricing, and tax/fee metadata when providers expose it through structured detail pages.
- 24 transport providers wired: 22 ground/ferry providers (`flixbus`, `regiojet`, `eurostar`, `db`, `oebb`, `ns`, `vr`, `sncf`, `trainline`, `transitous`, `renfe`, `trenitalia`, `italo`, `european-sleeper`, `snalltaget`, `tallink`, `viking-line`, `eckero-line`, `finnlines`, `stena-line`, `dfds`, `ferryhopper`) plus taxi estimates and optional Skyscanner Car Hire. Flights and hotels are separate rosters: 10 flight integrations (`google-flights`, `kiwi`, `skiplagged`, `afklm`, `ryanair`, `wizzair`, `transavia`, `easyjet`, `vueling`, `norwegian`) and 6 hotel sources (`google-hotels`, `booking.com`, `airbnb`, `trivago`, `hostelworld`, `hometogo`).
- Real protobuf reverse-engineering for Google Flights (not HTML scrape — see `internal/flights/`)
- Single-binary distribution: macOS / Linux / Windows / Docker
- License: PolyForm NC 1.0 — free for non-commercial agents, paid for commercial integrations (see [LICENSE](../LICENSE))

## 8. Distribution strategy

Maintained distribution surfaces:

- comparison matrix maintenance
- demo and starter-prompt maintenance
- release-channel and third-party directory checks ([status](DISTRIBUTION.md))
- distribution telemetry to measure positioning impact

## 9. Success metrics (90-day)

| Metric | Baseline | 90-day target |
|---|---|---|
| GitHub release downloads (28-day) | Tracked in [distribution metrics](internal/distribution-metrics.md) | +5× |
| npm `trvl` installs (28-day) | Tracked in [distribution metrics](internal/distribution-metrics.md) | +5× |
| Registry listings live | Tracked in [distribution metrics](internal/distribution-metrics.md) | Keep active listings accurate |
| Third-party verification | Track reproducible external reports | Record verified reports and fixes |
| Demo cast viewable | static GIF only | <30s asciinema cast |

## 10. What we are *not* doing yet

- Booking execution (we surface, agents book) — by design, until liability + payment story is solved
- Mobile-app shell — agents already live where users are
- Hosted SaaS — open-source first; hosted comes later if it's the bottleneck

---

**Maintenance**: review quarterly. Update Section 6 when a competing travel MCP changes materially, and refresh Section 9 from the distribution report.
