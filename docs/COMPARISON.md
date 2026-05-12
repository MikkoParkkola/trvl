# trvl Comparison Matrix

Last updated: 2026-05-12 · Owner: [MIK-3439](https://linear.app/parm/issue/MIK-3439)

This page compares trvl with the alternatives an AI-power-user is likely to consider: Google Flights, KAYAK, ChatGPT with web search, and other travel MCP servers.

## When not to use trvl

Use a normal travel website when you are a human doing one manual lookup and want a polished visual UI. Use Google Flights or KAYAK when you want to click around maps, filters, and fare calendars yourself. Use provider-direct booking, an OTA, or a travel agency when you need payment handling, refunds, customer support, or a managed corporate policy workflow. Use ChatGPT search when you need a prose travel brief rather than repeatable structured provider calls.

trvl is for agent workflows: repeated tool calls, structured inputs and outputs, multi-provider comparison, saved profile/preferences, watches, and automation.

## Evidence legend

- ✅ Verified support with linked evidence.
- ❌ No public evidence of that capability in the linked product surface, or the linked source describes a different human-facing surface.
- 🟡 Conservative inference from current public docs or directory listings; not a benchmark.

## Matrix

| Capability | trvl | Google Flights / Travel | KAYAK | ChatGPT with web search | Other travel MCPs |
|---|---|---|---|---|---|
| Primary user surface | ✅ [Agent + CLI: 61 MCP tools and 50 CLI commands](../README.md#mcp-tools) | ✅ [Human Google Travel help topics](https://support.google.com/travel/?hl=en) | ✅ [Human metasearch UI](https://www.kayak.com/c/help/about/) | ✅ [ChatGPT search conversation UI](https://help.openai.com/en/articles/9237897-chatgpt-) | ✅ [MCP server directory entries](https://glama.ai/mcp/servers?query=travel) |
| MCP-native structured tool calls | ✅ [MCP v2025-11-25, structured content, output schemas](../README.md#mcp-protocol-features-v2025-11-25) | ❌ [Consumer Travel Help, not an MCP server](https://support.google.com/travel/?hl=en) | ❌ [Metasearch website, not an MCP server](https://www.kayak.com/c/help/about/) | ❌ [Web search answers with sources, not a travel MCP schema](https://help.openai.com/en/articles/9237897-chatgpt-) | ✅ [MCP servers expose tools with schemas](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) |
| Install friction for AI assistants | ✅ [One binary plus `trvl mcp install`](../README.md#setup) | ❌ [No installable agent server in Google Travel help](https://support.google.com/travel/?hl=en) | ❌ [KAYAK is a site/app flow](https://www.kayak.com/c/help/about/) | ✅ [Built into ChatGPT search for eligible users](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Varies by server listing and transport](https://glama.ai/mcp/servers?query=travel) |
| Flight and hotel search | ✅ [Flights, hotels, rooms, reviews, hotel prices](../README.md#mcp-tools) | ✅ [Flights and Hotels help categories](https://support.google.com/travel/?hl=en) | ✅ [Flights and hotels across travel sites](https://www.kayak.com/c/help/about/) | 🟡 [Can search the web, but travel availability depends on sources](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Many listed travel MCPs expose travel search subsets](https://glama.ai/mcp/servers?query=travel) |
| Ground transport search | ✅ [20 bus, train, and ferry providers](../README.md#ground-transport-providers) | ✅ [Train and bus queries on Google Search](https://support.google.com/travel/?hl=en) | 🟡 [Vacation/travel metasearch, not documented here as structured ground API](https://www.kayak.com/c/help/about/) | 🟡 [Web search can find pages; no structured transport schema](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Some travel MCP listings include transport or location tools](https://glama.ai/mcp/servers?query=travel) |
| Provider breadth in one agent surface | ✅ [21 providers and public data-source summary](../README.md#at-a-glance) | 🟡 [Google Travel covers flights, hotels, transportation help surfaces](https://support.google.com/travel/?hl=en) | ✅ [Searches hundreds of travel sites](https://www.kayak.com/c/help/about/) | ❌ [Searches web pages, not a provider bundle exposed as tools](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Directory shows travel MCPs with varied tool counts](https://glama.ai/mcp/servers?query=travel) |
| Live/current data path | ✅ [Provider-backed searches and watches](../README.md#mcp-tools) | ✅ [Track flights and prices help topic](https://support.google.com/travel/?hl=en) | ✅ [Scans travel sites simultaneously](https://www.kayak.com/c/help/about/) | ✅ [Automatically searches the web when useful](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Depends on each server/provider](https://glama.ai/mcp/servers?query=travel) |
| Price alerts / buy-vs-wait support | ✅ [Price watches](../README.md#price-watch) and [`trvl forecast`](../cmd/trvl/forecast.go) | ✅ [Track flights and prices](https://support.google.com/travel/?hl=en) | ✅ [Price Alerts and Price Forecast](https://www.kayak.com/c/help/about/) | ❌ [Usage-limited web search, no saved travel watch documented](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Varies by server; not universal in directory listings](https://glama.ai/mcp/servers?query=travel) |
| Travel arbitrage / hack detection | ✅ [37 detectors including hidden-city, error fare, positioning, ferry, rail competition](../README.md#travel-hacks) | ❌ [No comparable agent-call detector documented in Travel Help](https://support.google.com/travel/?hl=en) | 🟡 [Price tools documented; hidden-city/award arbitrage not documented here](https://www.kayak.com/c/help/about/) | ❌ [Search can cite pages, but no deterministic arbitrage tool surface](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Some servers expose flight/hotel tools; capability varies](https://glama.ai/mcp/servers?query=travel) |
| Award sweet-spot workflows | ✅ [Cross-program award scanner](../README.md#mcp-tools) | ❌ [No award-seat scanner in Google Travel Help](https://support.google.com/travel/?hl=en) | ❌ [No award-seat scanner in KAYAK help page](https://www.kayak.com/c/help/about/) | ❌ [Web search only; no award-seat fixture/ranking schema](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Not visible as common travel-MCP baseline](https://glama.ai/mcp/servers?query=travel) |
| Booking execution | ❌ [Surfaces booking URLs; does not book automatically](../README.md#every-result-is-bookable) | ✅ [Users complete booking through listed options](https://support.google.com/travel/?hl=en) | ✅ [Click through to partner or book on KAYAK in some cases](https://www.kayak.com/c/help/about/) | 🟡 [Restaurant Reserve can open third-party reservation flows; details may not carry over](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Some listings mention booking/payment; not universal](https://glama.ai/mcp/servers?query=travel) |
| No personal API keys for default use | ✅ [Zero API keys; embedded public keys for two ground providers](../README.md#ground-transport-providers) | ✅ [Consumer website access](https://support.google.com/travel/?hl=en) | ✅ [Free consumer metasearch](https://www.kayak.com/c/help/about/) | 🟡 [Subject to ChatGPT plan usage limits](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Varies by MCP server and upstream provider](https://glama.ai/mcp/servers?query=travel) |
| Local-first privacy posture | ✅ [Personal profile and watches live under `~/.trvl`](../README.md#user-preferences) | 🟡 [Google account/location settings are product-managed](https://support.google.com/travel/?hl=en) | 🟡 [KAYAK product/account model, not local-first](https://www.kayak.com/c/help/about/) | 🟡 [Location and search behavior governed by ChatGPT settings/plan](https://help.openai.com/en/articles/9237897-chatgpt-) | 🟡 [Depends on server transport, hosting, and auth model](https://glama.ai/mcp/servers?query=travel) |

## Bottom line

trvl should not claim to be a better human travel website than Google Flights or KAYAK. It should claim the narrower, defensible position: **the best default travel MCP for agents that need structured, repeatable, multi-provider travel work without per-user API setup**.

That positioning is strongest when the workflow includes at least one of:

- multiple tool calls in one plan;
- flights plus hotels plus ground transport;
- personal preferences or saved watches;
- hidden-city, error-fare, award, or rail/ferry arbitrage;
- a need to run inside Claude, Cursor, Windsurf, Codex, VS Code, or another MCP-compatible client.
