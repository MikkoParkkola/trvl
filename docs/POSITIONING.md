# trvl — Positioning

> **The canonical travel MCP server. 1 smart tool, 62 compatibility aliases, 21 providers, zero API keys, one binary.**

Last updated: 2026-05-08 · Parent epic: [MIK-3438](https://linear.app/parm/issue/MIK-3438)

---

## 1. Tagline

**trvl makes your AI assistant a competent travel agent.**

Not a chatbot that "thinks" about travel. An agent with structured, live access to 21 real providers — flights, hotels, trains, buses, ferries, weather, awards, alerts — through one compact `travel` MCP tool plus 62 compatibility aliases that any compliant client (Claude, Cursor, Windsurf, Codex, ChatGPT-with-MCP, …) can call directly.

## 2. The problem we solve

Every AI assistant today fails at travel in the same three ways:

1. **Stale knowledge.** Models trained months ago don't know today's prices, schedules, or award charts.
2. **Screen-scraped substitutes.** Even with browse, agents read Kayak's HTML — slow, brittle, captcha-blocked, no structured booking data, no hidden-city, no award sweetspots.
3. **No multi-provider arbitrage.** A consumer-grade agent sees one source at a time; it can't run Ryanair × easyJet × Lufthansa × award-program × hotel-points in parallel and surface the dominated combinations.

trvl is the first MCP server purpose-built to fix all three at once.

## 3. Who this is for (ICP)

| Tier | Profile | What they get |
|---|---|---|
| **Primary** | AI-assistant power users (Claude / Cursor / Windsurf) who book ≥4 trips/yr | A trip-planning copilot that beats Kayak on price *and* effort |
| **Secondary** | AI-app builders integrating travel intent (booking concierges, expense automation, corporate travel agents) | A no-keys, single-binary backend they can ship inside their product |
| **Tertiary** | Devs shopping MCP servers in registries (smithery.ai, awesome-mcp, PulseMCP) | The category-defining travel MCP — install, done |

## 4. Who this is NOT for (anti-positioning)

- **Humans who book through a website.** Use Google Flights. trvl serves *agents*, not direct human UIs.
- **Travel agencies wanting white-label SaaS.** No signup, no servers, no billing — by design.
- **Single-flight one-shot lookups.** Kayak is fine for that. trvl earns its keep when an agent runs 10+ tool-calls per query.

## 5. Value triangle (what makes us category-defining)

```
            21 providers (most in MCP space)
                       /\
                      /  \
                     /    \
                    /      \
                   /        \
  Zero API keys --/----------\-- Agent-native
   (no signup,    \          /   (1 smart tool,
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
| 21 providers | Highest count of any travel MCP. Multi-provider arbitrage is impossible without coverage. | [README provider list](../README.md#providers) |
| Zero API keys | Removes the #1 install-abandonment cause. Free tier works on day zero. | Default config has no key fields |
| Agent-native | Structured tool I/O beats HTML scraping for agent reliability. | [AGENTS.md](../AGENTS.md) — 1 smart tool, 62 compatibility aliases, typed schemas |
| Browser fallback | When a provider has no API (Booking.com, AFKLM), we use a headless browser, not pretend support. | [internal/browser/](../internal/browser/) |
| One binary | `brew install`, done. No Docker, no Python venv, no Node toolchain. | `goreleaser` artifacts, all platforms |

## 6. Versus the real alternatives

The maintained head-to-head matrix lives in [COMPARISON.md](COMPARISON.md). It compares trvl against Google Flights, KAYAK, ChatGPT-with-search, and other travel MCPs, with every support/unsupported cell linked to source evidence.

| Alternative | What it is | Where trvl wins |
|---|---|---|
| **Google Flights / Kayak (web)** | Consumer search UIs | Not callable by agents; no MCP; no award sweetspots; no multi-provider arbitrage in one query |
| **ChatGPT browse + travel sites** | LLM searches and summarizes web pages | No deterministic travel schema; can't run trvl's hidden-city, award, and watch workflows as typed tool calls |
| **Other travel MCPs (one-provider wrappers)** | Usually 1–3 providers, often Google Flights only | trvl has 21 providers in one binary |
| **Travel-agent SaaS (Hopper, etc.)** | Paid consumer app | trvl is free, open-source, embeddable, not a product to log in to |

## 7. Proof points

- 1 smart MCP tool plus 62 compatibility aliases live on `main` ([tool list](../AGENTS.md))
- 21 providers wired (`google-flights`, `airbnb`, `booking.com`, `trivago`, `hostelworld`, `ferryhopper`, `kelkoo`, `kiwi`, `flixbus`, `rome2rio`, `omio`, `trainline`, `afklm`, `lufthansa`, `aircanada`, `delta`, `iata`, `openweathermap`, `noaa`, `weather.gov`, `wikivoyage`)
- Real protobuf reverse-engineering for Google Flights (not HTML scrape — see `internal/providers/googleflights/`)
- Single-binary distribution: macOS / Linux / Windows / Docker
- License: PolyForm NC 1.0 — free for non-commercial agents, paid for commercial integrations (see [LICENSE](../LICENSE))

## 8. Distribution strategy

Tracked as children of [MIK-3438](https://linear.app/parm/issue/MIK-3438):

- **MIK-3439** — vs-comparison matrix (credibility)
- **MIK-3440** — demo refresh: asciinema + GIF + first-5-prompts starter
- **MIK-3441** — registry submissions: smithery.ai, awesome-mcp, MCP-registry, PulseMCP ([status](DISTRIBUTION.md))
- **MIK-3442** — case-study artifact: real booking, real savings, screenshots
- **MIK-3443** — distribution telemetry to measure positioning impact

## 9. Success metrics (90-day)

| Metric | Baseline | 90-day target |
|---|---|---|
| GitHub release downloads (28-day) | Tracked in [distribution metrics](internal/distribution-metrics.md) | +5× |
| npm `trvl` installs (28-day) | Tracked in [distribution metrics](internal/distribution-metrics.md) | +5× |
| Registry listings live | 0 | ≥3 (smithery, awesome-mcp, PulseMCP) |
| Unsolicited third-party mentions | 0 tracked | ≥1 blog / tweet citing trvl as canonical |
| Demo cast viewable | static GIF only | <30s asciinema cast |

## 10. What we are *not* doing yet

- Booking execution (we surface, agents book) — by design, until liability + payment story is solved
- Mobile-app shell — agents already live where users are
- Hosted SaaS — open-source first; hosted comes later if it's the bottleneck

---

**Maintenance**: review quarterly. Update Section 6 alternatives table whenever a competing travel MCP ships. Re-baseline Section 9 metrics post-launch.
