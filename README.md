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

**Ask your AI assistant to plan a real trip, and it actually can.** trvl gives Claude, Cursor, Windsurf, Codex, or any MCP-compatible client one smart tool — the `travel` router — with live access to flights, hotels, rental cars, trains, buses, ferries, price alerts, award sweet spots, weather, and destination intel. Free for noncommercial use, no personal API keys for default search, no signup. One binary.

> **You:** I have €300 and a free weekend. Surprise me.
>
> **Illustrative response shape (prices are examples):** **Dubrovnik, Croatia** 🇭🇷 — ✈️ Ryanair HEL→DBV €167 RT · 🏨 Old Town Studios 4.6★ €84 · 🌡️ 26°C, sunny.
> 📊 Naive €350 → optimized €251 → **saved €99 (28%)** by flying Friday and splitting airlines.

**▶ Try it live, no install:** [socialistic.ai/trvl-travel-mcp](https://socialistic.ai/en/skill/trvl-travel-mcp-4f7aa7) (community-hosted).

---

## How it works, in one paragraph

trvl is a single binary that runs on your machine. Your AI client talks to it over MCP; it queries flight, hotel, transport, weather, and places sources, then merges, de-duplicates and optimizes the results before handing back one answer. Most sources are free public endpoints, so there is nothing to sign up for. The ones behind bot protection may reuse the browser session you already have, which is the one thing worth reading about before you install: see [What trvl reads, and what it keeps](#what-trvl-reads-and-what-it-keeps).

## Why trvl, not the alternatives

- **Whole journey, door to door.** It plans the entire trip across modes — home to airport, flight, arrival transfer, hotel, onward train — and prices each leg in its real mode. Most tools stop at one flight, one hotel.
- **No personal API key for default search.** There is no Amadeus key to apply for, subscription, or per-call bill on the default paths. Optional providers switch on when you supply their credentials; none is required for the default flight, hotel, or ground searches.
- **Your assistant, your machine.** One local binary, any MCP client, not locked to a vendor. Searching sends the query to the providers being searched, the same as any travel site would: route, dates and traveller count go to Google, Kiwi, Booking and the rest. What trvl keeps for itself stays on your machine, apart from a daily anonymous heartbeat you can switch off and any webhook you configure yourself — both spelled out below.
- **It optimizes, not just lists.** Shift-day pricing, split-airline routing, hidden-city checks, award sweet spots, round-trip fares. It hands back the cheaper option and shows what it saved.
- **It is honest when a source fails.** Typed statuses and labelled estimates, never an empty result dressed up as "nothing found."

Full head-to-head against Google Flights, KAYAK, Skyscanner, Kiwi, and other travel MCPs: [docs/COMPARISON.md](docs/COMPARISON.md).

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
TRVL_VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/MikkoParkkola/trvl/releases/latest | sed 's#.*/v##')"
curl -fsSL "https://github.com/MikkoParkkola/trvl/releases/download/v${TRVL_VERSION}/trvl_${TRVL_VERSION}_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" | tar xz -C /usr/local/bin trvl

# Go
go install github.com/MikkoParkkola/trvl/cmd/trvl@latest

# Docker
docker run --rm ghcr.io/mikkoparkkola/trvl flights HEL NRT 2027-06-15

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

## Why trust it

An AI agent acts on trvl's output without a human checking every result, so the bar is correctness, not just coverage.

- **It tells you when it can't.** A blocked or rate-limited provider returns a typed status (`AKAMAI_BLOCK`, `RATE_LIMITED`, `BOOKING_COOKIES_MISSING`) with a fix hint, instead of a fake "nothing found." Estimated values are labelled; currency-mismatched totals are skipped, not faked.
- **Tested more than it is written.** More test code than source, race-checked on macOS, Linux, and Windows. A smoke gate runs the packaged binary before any release — a build that doesn't run can't ship.
- **It degrades gracefully.** Providers run concurrently with per-provider timeouts; one source failing returns partial results instead of aborting the search.
- **It's observable.** `trvl status` (or the local `/dashboard` in HTTP mode) shows per-provider success rate, latency, freshness, and circuit-breaker state.

**On hotel prices specifically.** Hotel metasearch exposes list-level rates first; some are real, some only firm up after the property detail page reveals the room/tax/cancellation matrix. So trvl separates **discovery** (`search_hotels` — fast, lead-in prices) from **decisions** (`search_accommodations` — verifies room-level offers before ranking) and **drill-down** (`search_hotels_with_details`, `hotel_rooms`). It provides booking links for manual handoff but never books, holds, or guarantees a rate. Detail: [docs/PROVIDERS.md](docs/PROVIDERS.md).

## What trvl reads, and what it keeps

Two things happen without you asking for them. Neither is obvious, so both are stated here rather than left to a linked page.

**It reads your browser's cookies, automatically.** Hotel and rail sites put bot protection in front of their search APIs, and trvl gets past it by reusing the browser session you already have — that is why searches work with no API key. The reads start when trvl launches, before any search. No flag turns them on. On macOS, browser cookie stores are encrypted, so reading them means Keychain access and you should expect a Keychain prompt. What is read is your own session cookies for the site being searched, and they go into the request to that same site. Every provider definition capable of receiving cookies is reviewed in source and embedded in the binary; runtime provider JSON is not executable. The room lookup's caller-supplied URL is checked against Booking.com before cookies are attached, and a test fails if that stops being true. If a site redirects trvl to a different host, the cookies do not follow: Go's HTTP client refuses to carry them across a change of host. That check compares hosts and not schemes, so a site redirecting its own `https://` address to plain `http://` would keep them — a site downgrading its own traffic to cleartext is the one case that would put a session on the wire unencrypted. trvl reports your cookies to no endpoint of its own.

**Optional provider definitions are source-only.** `trvl providers enable <id>` can enable only a reviewed definition shipped in the current binary. The runtime state file records consent, enabled state, and health; it cannot replace endpoints, headers, authentication, request templates, or response mappings. Older files under `~/.trvl/providers` are left in place for rollback or manual migration, but trvl does not load them. New definitions must arrive through a reviewed source change (or a user-maintained fork).

**It keeps working state under `~/.trvl`:** saved trips, preferences and traveller profile, price watches, search history, cached cookies and provider tokens, a provider health log, upgrade and provider self-heal bookkeeping, and a random install id. That state is local, and trvl uploads none of it — with two exceptions it would be dishonest to bury. The install id is the one field the telemetry heartbeat sends, described below, and `TRVL_NO_TELEMETRY=1` stops it. A price watch you give a webhook URL to POSTs that watch's route and price data to the address you supplied, which is the point of a webhook.

JSON state files are written to a temp file and then renamed over the target, so a crash cannot leave a half-written JSON document behind. Price watches and their history use transactional `watch.db` storage; the first migration backs up the legacy JSON before committing the database. Temp-file replacement can still leave **orphaned temp files from interrupted writes**, each a full copy of the JSON file it was about to replace. trvl does not delete them on its own, because the orphan is occasionally the only surviving copy of the target. `trvl tempfiles` reports what is there with sizes and ages; `trvl tempfiles --delete` removes only the ones whose writing process is provably gone.

You can decline either behaviour:

```bash
export TRVL_NO_BROWSER_COOKIES=1   # never read your browsers or the sessions in them, and never open a window in your real browser
export TRVL_NO_TIER2_CDP=1         # never start a headless browser of its own
```

Both controls trade coverage for isolation. Cookie access is enabled by default because protected hotel and rail sources rely on existing sessions; turning it off can materially reduce results. The headless fallback starts an installed Chrome, Brave, or Edge with an empty profile only when a challenged source needs fresh cookies. It does not read your existing browser sessions, show a window, or take focus. Turning it off can leave a challenged source empty. A visible-browser recovery path exists for a small number of providers, but it is separate, provider-specific, and asks before opening; disabling the headless fallback does not enable it automatically.

### If you are behind a proxy

trvl honours `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`. It checks and pins both hops — the proxy and the destination — so a redirect or a changed DNS answer cannot move the connection somewhere the URL never named.

By default trvl refuses to connect to private, loopback and link-local addresses, which is what keeps a hostile redirect away from your internal network and from cloud metadata endpoints. A corporate proxy is almost always on a private address, so reaching one needs an explicit opt-in:

```bash
export TRVL_ALLOW_PRIVATE_PROXY=1   # the PROXY may be on a private address
```

That relaxes the proxy hop only. Destinations are still refused on private addresses. The broader `TRVL_ALLOW_LOCAL_PROVIDERS=1` allows both, and exists for pointing trvl at a mock provider on your own machine — do not reach for it just to use a proxy, because it also switches off the destination guard.

Authenticated proxies (`http://user:pass@host`) are not supported.

### How much price history trvl keeps

**Price watching is experimental.** It works, and it keeps data — your watches and their whole price history live under `~/.trvl`. Treat that history as something you could lose. The store is backed up before any migration and the legacy files are kept afterwards, so a bad outcome is recoverable; but the feature is younger than the rest of trvl and is being changed more often.

**Upgrading to 1.21.0 can delete price history.** The first run converts the store to a transactional database, and if you have lowered `TRVL_WATCH_MAX_POINTS_PER_WATCH` it now honours that limit during the conversion — earlier versions ignored it there while applying it everywhere else, so a lowered limit was quietly not in force. Points above your limit are removed. Run `trvl watch migrate --dry-run` first to see the real number: the preview used to under-report it, which is the reason this warning exists.

Watch price history is capped, or it grows without bound: one real store reached 320,028 points in 41MB, which cost every running trvl process about 686MB of memory. Three limits bound it, and all three can be changed.

```bash
export TRVL_WATCH_MAX_POINTS_PER_WATCH=1000   # points kept per watch
export TRVL_WATCH_MAX_POINTS_TOTAL=50000      # points kept across all watches
export TRVL_WATCH_ROUTE_TTL_DAYS=90           # how long a dateless route watch keeps being checked
```

Those are the defaults, and the cost of moving each one:

| setting | raising it costs | lowering it costs |
|---|---|---|
| points per watch | memory and disk, multiplied by how many watches you have | resolution in long-range trend views. Nothing needs the raw tail — the sparkline reads 10–20 points, and a watch's all-time low is stored on the watch itself, so it survives eviction |
| points in total | this is the number that actually bounds the file. The per-watch cap cannot, because many watches multiply it | your watches compete for one budget, so a large collection loses history on all of them rather than on the busiest |
| route watch TTL | routes with no travel date are re-checked against live providers every 30 minutes for as long as this allows. One real store carried 468 permanently-active route watches | a seasonal route expires between uses. Re-watching renews it, but an annual trip expires every year |

An unusable value is **refused, not adjusted**: `trvl` will not start against `TRVL_WATCH_MAX_POINTS_TOTAL=0`, rather than quietly using the default and leaving you to believe your setting took effect.

To see whether any limit is actually binding on your store, run `trvl watch migrate --dry-run`. It reports the size on disk, the spread of points per watch, and how close you are to each cap, without changing anything.

Full mechanism — what is read at which point, the exact difference between those two switches, the headless-browser fallback, and the AF-KLM credential rules: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#browser-sessions-credentials-and-local-state).

One command publishes deliberately, because that is what you ran it for: the calendar helper writes an event to your Google calendar. `trvl share` does not — it prints the trip card, or copies it to your clipboard, and you decide who receives it. It used to upload the card as a public GitHub gist; that was removed in [#527](https://github.com/MikkoParkkola/trvl/issues/527), because a card carries destinations and dates, and those together say when your home is empty.

## Optional credentialed providers

Every source trvl uses by default is free and needs no account. These extras switch on only if you supply a key of your own, and stay silent otherwise:

| Variable | Enables |
| --- | --- |
| `AFKLM_KEY` | AF-KLM native round-trip and rail+fly fares |
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

Every one of these reads its key from the environment. AF-KLM is the single exception, and only when you ask for it: under an explicit `--provider afklm` it may also read the macOS Keychain or 1Password. Nothing in this table reaches a credential manager during an ordinary search — the reasoning is in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#af-klm-the-one-credential-that-may-come-from-a-credential-manager).

## Privacy & telemetry

trvl sends one anonymous heartbeat per install per day so the project knows roughly how many people use it and on which platforms. That is the entire purpose. The heartbeat carries only:

- a fixed project tag (`trvl`) and event name (`heartbeat`)
- the trvl version
- the Go runtime string (OS, architecture, Go version, e.g. `darwin/arm64/go1.26.5`)
- a random install id generated locally on first run (stored in `~/.trvl/install-id`)

No hostname, no username, no search queries, no travel data. Two things that list does not make obvious, stated rather than left to inference. Your IP is not in the payload, but the request reveals it as any HTTP request does, and the collector uses it server-side to derive coarse geography, reported only in aggregate with a minimum group size of 5. And the install id is stable, so repeated heartbeats from one machine are linkable to each other over time; it is random and contains nothing about you, but it is not a fresh value each time. The request has a 3-second timeout and fails silently — if the collector is down, trvl behaves exactly as if telemetry were off.

To turn it off, set any one of these before running trvl:

```bash
export TRVL_NO_TELEMETRY=1   # trvl-specific switch
export NO_TELEMETRY=1        # common convention
export DO_NOT_TRACK=1        # cross-tool Do-Not-Track signal
```

It is also skipped automatically in CI and for development builds. Override the endpoint with `TRVL_TELEMETRY_ENDPOINT` if you run your own collector.

## Run it as an HTTP / remote server

Local stdio is the default and safest transport. `trvl mcp --http` binds to `127.0.0.1`, requires a bearer token, and generates one at startup if unset. Remote exposure, scoped read/write tokens, and OAuth 2.1 introspection: [docs/REMOTE-MCP-OAUTH.md](docs/REMOTE-MCP-OAUTH.md).

## Troubleshooting

- **No tools showing?** Restart your AI client after `trvl mcp install`; confirm `which trvl` is on `$PATH`.
- **Empty flight results?** Some routes have no Google Flights data — try a major pair like `trvl flights HEL LHR 2027-07-01`.
- **Ground transport times out?** Rail/ferry providers throttle; retry after 30s or pass `--timeout 3m`.

Full troubleshooting: [docs/CLI.md](docs/CLI.md).

## Available on

[Glama](https://glama.ai/mcp/servers/MikkoParkkola/trvl) · [LobeHub](https://lobehub.com/mcp/mikkoparkkola-trvl) · [Smithery](https://smithery.ai/server/@MikkoParkkola/trvl) · [MCPHub](https://www.mcphub.com/mcp-servers/MikkoParkkola/trvl) · [Cursor Directory](https://cursor.directory/mcp/trvl) · [PulseMCP](https://www.pulsemcp.com/servers/mikkoparkkola-trvl) · [MCP Market](https://mcpmarket.com/server/trvl) · [pkg.go.dev](https://pkg.go.dev/github.com/MikkoParkkola/trvl)

Third-party directories cache their own descriptions and may show older tool or command counts. Use this README, the [changelog](CHANGELOG.md), and the [official MCP Registry](https://registry.modelcontextprotocol.io/?q=io.github.MikkoParkkola%2Ftrvl) for the current surface and release version.

**Independent coverage:** Roberto Reale's [Budget Travel Pipeline](https://blog-roberto-reale.vercel.app) — an independent build-and-test that surfaced real fixes and shaped the v1.10 trust roadmap.

## Ecosystem

Part of a suite of MCP tools: [mcp-gateway](https://github.com/MikkoParkkola/mcp-gateway) (universal gateway) · [nab](https://github.com/MikkoParkkola/nab) (web extraction with anti-bot) · [axterminator](https://github.com/MikkoParkkola/axterminator) (macOS GUI automation).

## Legal & license

trvl is a **personal-use tool** that reads public-facing web APIs (Google Flights, Google Hotels, and others). It does not bypass authentication or circumvent rate limits; request patterns are throttled to look like manual browsing. Automated access may violate some providers' Terms of Service — you are responsible for compliance in your jurisdiction.

`trvl flights` and `trvl hotels` accept an optional `--stealth` flag that routes the fetch through trvl's Chrome HTTP/2 fingerprint transport. It is off unless you pass it, activates only for hosts you list in `TRVL_STEALTH_ALLOWLIST`, and does nothing but log one line for any host not on that list — an empty allowlist means it never activates. Only flight and hotel search honour it. Using stealth against sites whose terms prohibit automated access is your responsibility.

```bash
export TRVL_STEALTH_ALLOWLIST=".google.com"
trvl flights HEL NRT 2027-09-01 --stealth
```

Licensed under [PolyForm Noncommercial 1.0](LICENSE) — free for personal and noncommercial use. Commercial use (company-internal, hosted service, embedding in paid platforms) requires a separate license: EUR 500/month per named project via [GitHub Sponsors](https://github.com/sponsors/MikkoParkkola), see [COMMERCIAL.md](COMMERCIAL.md).

Built on [fli](https://github.com/punitarani/fli), [utls](https://github.com/refraction-networking/utls), and [SerpAPI](https://serpapi.com/google-hotels-api)'s parameter reference.

## Star it

If trvl saved you a browser tab or an API subscription, a star helps other travellers (and their assistants) find it. That's the whole ask.
