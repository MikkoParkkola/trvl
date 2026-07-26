[![Go Report Card](https://goreportcard.com/badge/github.com/MikkoParkkola/trvl)](https://goreportcard.com/report/github.com/MikkoParkkola/trvl)
[![CI](https://github.com/MikkoParkkola/trvl/actions/workflows/ci.yaml/badge.svg)](https://github.com/MikkoParkkola/trvl/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/MikkoParkkola/trvl)](https://github.com/MikkoParkkola/trvl/releases)
[![Downloads](https://img.shields.io/github/downloads/MikkoParkkola/trvl/total)](https://github.com/MikkoParkkola/trvl/releases)
[![License](https://img.shields.io/badge/license-PolyForm%20NC%201.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/MikkoParkkola/trvl.svg)](https://pkg.go.dev/github.com/MikkoParkkola/trvl)
[![MCP](https://img.shields.io/badge/MCP-2025--11--25-blue)](https://modelcontextprotocol.io)
[![Providers](https://img.shields.io/badge/providers-24-brightgreen)](https://github.com/MikkoParkkola/trvl#what-it-can-do)
[![Go Version](https://img.shields.io/github/go-mod/go-version/MikkoParkkola/trvl)](https://go.dev/)
[![Install in VS Code](https://img.shields.io/badge/VS_Code-Install_MCP-0078d4?logo=visualstudiocode)](https://insiders.vscode.dev/redirect/mcp/install?name=trvl&config=%7B%22command%22%3A%22trvl%22%2C%22args%22%3A%5B%22mcp%22%5D%7D)
[![Install in Cursor](https://img.shields.io/badge/Cursor-Install_MCP-black?logo=cursor)](cursor://anysphere.cursor-deeplink/mcp/install?name=trvl&config=%7B%22command%22%3A%22trvl%22%2C%22args%22%3A%5B%22mcp%22%5D%7D)
[![Live Demo](https://img.shields.io/badge/Live_Demo-Try_in_browser-14b8a6?logo=googlechrome&logoColor=white)](https://socialistic.ai/en/skill/trvl-travel-mcp-4f7aa7)

# trvl — Real travel search for your AI assistant

![trvl demo](https://raw.githubusercontent.com/MikkoParkkola/trvl/main/demo.gif?v=1.10.0)

**Ask your AI assistant to plan a real trip, and it actually can.** trvl gives Claude, Cursor, Windsurf, Codex, or any MCP-compatible client one smart tool — the `travel` router — with live access to flights, hotels, rental cars, trains, buses, ferries, price alerts, award sweet spots, weather, and destination intel. Free, no API keys, no signup. One binary.

> **You:** I have €300 and a free weekend. Surprise me.
>
> **Claude (with trvl):** **Dubrovnik, Croatia** 🇭🇷 — ✈️ Ryanair HEL→DBV €167 RT · 🏨 Old Town Studios 4.6★ €84 · 🌡️ 26°C, sunny.
> 📊 Naive €350 → optimized €251 → **saved €99 (28%)** by flying Friday and splitting airlines.

**▶ Try it live, no install:** [socialistic.ai/trvl-travel-mcp](https://socialistic.ai/en/skill/trvl-travel-mcp-4f7aa7) (community-hosted).

---

## Install

**Let your AI do it** — paste into Claude Code, Cursor, Windsurf, or Codex:

> Read https://raw.githubusercontent.com/MikkoParkkola/trvl/main/AGENTS.md and set up trvl

It installs the binary, wires the MCP server, installs the skill, and verifies everything. Under a minute.

**Or by hand:**

```bash
brew install MikkoParkkola/tap/trvl   # install
trvl mcp install                       # auto-detects your AI client
```

Restart your client. `trvl mcp install --client <name>` targets a specific one (10 supported: Claude Desktop/Code, Cursor, Windsurf, Codex, VS Code, Zed, Gemini, Amazon Q, LM Studio).

<details>
<summary>More ways to install (Go, Docker, raw binary, manual config)</summary>

```bash
# Direct binary (no Homebrew)
curl -fsSL https://github.com/MikkoParkkola/trvl/releases/latest/download/trvl_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C /usr/local/bin trvl

# Go
go install github.com/MikkoParkkola/trvl/cmd/trvl@latest

# Docker
docker run --rm ghcr.io/mikkoparkkola/trvl flights HEL NRT 2026-06-15

# Build from source
git clone https://github.com/MikkoParkkola/trvl.git && cd trvl && make build

# Claude Code CLI
claude mcp add trvl --transport stdio -- trvl mcp

# Manual JSON (Claude Desktop, Cursor, Windsurf, etc.)
# { "mcpServers": { "trvl": { "command": "trvl", "args": ["mcp"] } } }
```

</details>

## Try it

Paste any of these to your assistant once trvl is wired:

```text
Find the cheapest realistic trip from Helsinki for the long weekend of July 1–5, nonstop, hotel near the center.
Compare award sweet spots HEL→LHR business on Aug 15 with 80k Amex MR + 20k Virgin points.
Create a mistake-fare watch for HEL→BCN, July 1–8, and alert me below €90.
```

More starter prompts and what good answers look like: [docs/DEMO.md](docs/DEMO.md).

## Why trvl, not the alternatives

- **Whole journey, door to door.** It plans the entire trip across modes — home to airport, flight, arrival transfer, hotel, onward train — and prices each leg in its real mode. Most tools stop at one flight, one hotel.
- **No API keys, no signup, no bill.** Every core source works the moment you install it — no Amadeus key to apply for, no subscription, no per-call cost. Some optional providers can be switched on with a key of your own (AF-KLM, SerpAPI, Travelpayouts and others); none of them is required, and a search never goes looking for credentials on your machine that you haven't pointed it at. See [Optional credentialed providers](#optional-credentialed-providers).
- **Your assistant, your machine.** One local binary, any MCP client. Not locked to a vendor; your trips and preferences never leave your computer.
- **It optimizes, not just lists.** Shift-day pricing, split-airline routing, hidden-city checks, award sweet spots, round-trip fares. It hands back the cheaper option and shows what it saved.
- **It is honest when a source fails.** Typed statuses and labelled estimates, never an empty result dressed up as "nothing found."

Full head-to-head against Google Flights, KAYAK, Skyscanner, Kiwi, and other travel MCPs: [docs/COMPARISON.md](docs/COMPARISON.md).

## Why trust it

An AI agent acts on trvl's output without a human checking every result, so the bar is correctness, not just coverage.

- **It tells you when it can't.** A blocked or rate-limited provider returns a typed status (`AKAMAI_BLOCK`, `RATE_LIMITED`, `BOOKING_COOKIES_MISSING`) with a fix hint, instead of a fake "nothing found." Estimated values are labelled; currency-mismatched totals are skipped, not faked.
- **Tested more than it is written.** More test code than source, race-checked on macOS, Linux, and Windows. A smoke gate runs the packaged binary before any release — a build that doesn't run can't ship.
- **It degrades gracefully.** Providers run concurrently with per-provider timeouts; one source failing returns partial results instead of aborting the search.
- **It's observable.** `trvl status` (or the local `/dashboard` in HTTP mode) shows per-provider success rate, latency, freshness, and circuit-breaker state.

### A note on hotel prices

Hotel metasearch exposes list-level rates first; some are real, some only firm up after the property detail page reveals the room/tax/cancellation matrix. So trvl separates **discovery** (`search_hotels` — fast, lead-in prices) from **decisions** (`search_accommodations` — verifies room-level offers before ranking) and **drill-down** (`search_hotels_with_details`, `hotel_rooms`). It provides booking links for manual handoff but never books, holds, or guarantees a rate. Detail: [docs/PROVIDERS.md](docs/PROVIDERS.md).

### Stealth: opt-in, authorized first-party access only

`trvl flights` and `trvl hotels` accept an optional `--stealth` flag that routes the fetch through trvl's existing Chrome HTTP/2 fingerprint transport. It is built as a fail-safe scope fence:

- **Default off.** Stealth is inactive unless you explicitly pass `--stealth`.
- **Authorized hosts only.** Even with `--stealth`, it activates only for hosts on an operator-authorized allowlist read from the `TRVL_STEALTH_ALLOWLIST` environment variable (comma-separated hostnames; case-insensitive; exact match plus leading-dot suffix such as `.google.com`). An empty allowlist means stealth never activates — refuse by default.
- **No silent evasion.** With `--stealth` set for a host that is not on the allowlist, trvl runs the normal fetch path and logs one line (`stealth not authorized for host <host>`); it does not error, it does not abort.
- **Scope-fenced.** Only flight and hotel search honour `--stealth`. Other paths never receive it.

Using stealth against sites whose terms prohibit automated access is the operator's responsibility.

Example:

```bash
export TRVL_STEALTH_ALLOWLIST=".google.com"
trvl flights HEL NRT 2026-09-01 --stealth
trvl hotels "Helsinki" --checkin 2026-09-01 --checkout 2026-09-04 --stealth
```

## What it can do

| Area | Highlights | Reference |
|------|-----------|-----------|
| **MCP tools** | 1 smart `travel` router — a natural-language tool that advertises a single tool (~378 tokens) instead of a full per-domain list (~33,500 tokens): ~98.9% smaller `tools/list` footprint. Older clients that call legacy tool names still work (66 legacy-compatible capabilities). | [MCP-TOOLS-REFERENCE.md](docs/MCP-TOOLS-REFERENCE.md) |
| **Flights** | Google Flights + Kiwi + Skiplagged merged; LCC fares, AFKLM award scan, round-trip (both legs) | [PROVIDERS.md](docs/PROVIDERS.md) |
| **Ground** | 22 train/bus/ferry providers across Europe, API-first | [PROVIDERS.md](docs/PROVIDERS.md) |
| **Hotels** | 6 sources, discovery → verification trust model | [PROVIDERS.md](docs/PROVIDERS.md) |
| **Travel hacks** | 36 parallel detectors (hidden-city, positioning, stopover, multimodal, error-fare…) | [PROVIDERS.md](docs/PROVIDERS.md) |
| **CLI** | Standalone tool, 56 commands, table/JSON output | [CLI.md](docs/CLI.md) |
| **Profile** | Learns home airports, FF status, luggage, preferences from your booking history | [traveller-workspace.md](docs/traveller-workspace.md) |

## Is this for you?

**Yes** if you already plan trips with an AI assistant and want it to search real flights, hotels, trains, buses, ferries, and transfers instead of guessing — or if you're building an app that needs travel intent without a paid travel API.

**Probably not** if you just want to book on a website (use Google Flights), or you want a hosted product with an account and dashboard. trvl is a tool you run, not a service you log into.

Full positioning: [docs/POSITIONING.md](docs/POSITIONING.md).

## Run it as an HTTP / remote server

Local stdio is the default and safest transport. `trvl mcp --http` binds to `127.0.0.1`, requires a bearer token, and generates one at startup if unset. Remote exposure, scoped read/write tokens, and OAuth 2.1 introspection: [docs/REMOTE-MCP-OAUTH.md](docs/REMOTE-MCP-OAUTH.md).

## Available on

[Glama](https://glama.ai/mcp/servers/MikkoParkkola/trvl) · [LobeHub](https://lobehub.com/mcp/mikkoparkkola-trvl) · [Smithery](https://smithery.ai/server/@MikkoParkkola/trvl) · [MCPHub](https://www.mcphub.com/mcp-servers/MikkoParkkola/trvl) · [Cursor Directory](https://cursor.directory/mcp/trvl) · [PulseMCP](https://www.pulsemcp.com/servers/mikkoparkkola-trvl) · [MCP Market](https://mcpmarket.com/server/trvl) · [pkg.go.dev](https://pkg.go.dev/github.com/MikkoParkkola/trvl)

**Independent coverage:** Roberto Reale's [Budget Travel Pipeline](https://blog-roberto-reale.vercel.app) — an independent build-and-test that surfaced real fixes and shaped the v1.10 trust roadmap.

## Troubleshooting

- **No tools showing?** Restart your AI client after `trvl mcp install`; confirm `which trvl` is on `$PATH`.
- **Empty flight results?** Some routes have no Google Flights data — try a major pair like `trvl flights HEL LHR 2026-07-01`.
- **Ground transport times out?** Rail/ferry providers throttle; retry after 30s or pass `--timeout 3m`.

Full troubleshooting: [docs/CLI.md](docs/CLI.md).

## Optional credentialed providers

Every source trvl uses by default is free and needs no account. A number of extras switch on only if you supply a key of your own, and stay silent otherwise:

| Variable | Enables |
| --- | --- |
| `AFKLM_KEY` | AF-KLM native round-trip fares (see below) |
| `AFKL_KLM_COOKIES` | AF-KLM Flying Blue award / miles search |
| `SERPAPI_KEY` | Detail-verified hotel provider prices (`trvl serpapi`) |
| `TRAVELPAYOUTS_TOKEN` | Historical price trends |
| `TRANSAVIA_API_KEY` | Transavia flights |
| `DISTRIBUSION_API_KEY` | Bus and coach ground legs |
| `FOURSQUARE_API_KEY` | Nearby places |
| `GEOAPIFY_API_KEY` | Destination geo data |
| `OPENTRIPMAP_API_KEY` | Attractions |
| `TICKETMASTER_API_KEY` | Events |
| `TRVL_GMAIL_APP_PASSWORD` | Emailing trip digests |

Every one of these reads its key from the environment and nowhere else. trvl does not search your machine for credentials.

AF-KLM is the single exception, and it is worth explaining. Ordinary round-trips are already covered in the default merge: Kiwi returns both legs of a paired itinerary, and Google returns the genuine round-trip fare with the matching return chosen at booking. AF-KLM is there for what neither offers — the rail+fly itineraries it sells, where a train leg from Brussels Midi, Antwerp or Brussels is ticketed as part of the flight instead of being a separate rail booking you have to make and risk yourself. It also returns both legs on KL/AF metal in full detail. It is the only provider that can read from a credential store, under tight rules:

| Variable | Effect |
| --- | --- |
| `AFKLM_KEY` | The API key itself. Once set, AF-KLM native round-trips join your default searches automatically. |
| `AFKLM_OP_REF` | A 1Password secret reference, e.g. `op://Private/AF-KLM/credential`. Read via the `op` CLI **only** when you run `--provider afklm` explicitly. |
| `AFKLM_KEYCHAIN_SERVICE` | Overrides the macOS Keychain service name (default `afklm-api-key`). Read under `--provider afklm` only. |

Set `AFKLM_OP_REF` without `AFKLM_KEY` and a default search will tell you AF-KLM was skipped and why, rather than quietly leaving it out.

The rule, and why it exists: a search you didn't ask for never runs a credential helper. `op` and `security` are third-party programs that can block, can pop an interactive prompt, and can leave stray processes behind — so an opportunistic lookup on the default path is limited to reading an environment variable, which costs nothing and cannot prompt. Only an explicit `--provider afklm` may reach an external store, where a prompt is something you asked for. Reported as [#507](https://github.com/MikkoParkkola/trvl/issues/507).

If you use the macOS Keychain (`security add-generic-password -a "$USER" -s afklm-api-key -w <key>`), it is consulted under `--provider afklm` only, for the same reason.

## Ecosystem

Part of a suite of MCP tools: [mcp-gateway](https://github.com/MikkoParkkola/mcp-gateway) (universal gateway) · [nab](https://github.com/MikkoParkkola/nab) (web extraction with anti-bot) · [axterminator](https://github.com/MikkoParkkola/axterminator) (macOS GUI automation).

## Privacy & telemetry

trvl sends one anonymous heartbeat per install per day so the project knows roughly how many people use it and on which platforms. That is the entire purpose. The heartbeat carries only:

- a fixed project tag (`trvl`) and event name (`heartbeat`)
- the trvl version
- the Go runtime string (OS, architecture, Go version, e.g. `darwin/arm64/go1.26.5`)
- a random install id generated locally on first run (stored in `~/.trvl/install-id`)

It never sends your IP, hostname, username, search queries, or any travel data. The collector derives coarse geography server-side from the request and only reports it in aggregate, with a minimum group size of 5 (k-anonymity), so no single install is identifiable. The request has a 3-second timeout and fails silently. If the collector is down or slow, trvl behaves exactly as if telemetry were off.

To turn it off, set any one of these before running trvl:

```bash
export TRVL_NO_TELEMETRY=1   # trvl-specific switch
export NO_TELEMETRY=1        # common convention
export DO_NOT_TRACK=1        # cross-tool Do-Not-Track signal
```

It is also skipped automatically in CI and for development builds. Override the endpoint with `TRVL_TELEMETRY_ENDPOINT` if you run your own collector.

## Legal & license

trvl is a **personal-use tool** that reads public-facing web APIs (Google Flights, Google Hotels, and others). It does not bypass authentication or circumvent rate limits; request patterns are throttled to look like manual browsing. Automated access may violate some providers' Terms of Service — you are responsible for compliance in your jurisdiction.

Licensed under [PolyForm Noncommercial 1.0](LICENSE) — free for personal and noncommercial use. Commercial use (company-internal, hosted service, embedding in paid platforms) requires a separate license: EUR 500/month per named project via [GitHub Sponsors](https://github.com/sponsors/MikkoParkkola), see [COMMERCIAL.md](COMMERCIAL.md).

Built on [fli](https://github.com/punitarani/fli), [utls](https://github.com/refraction-networking/utls), and [SerpAPI](https://serpapi.com/google-hotels-api)'s parameter reference.

## Star it

If trvl saved you a browser tab or an API subscription, a star helps other travellers (and their assistants) find it. That's the whole ask.
