# Distribution Status

Release channels and third-party directory listings. Last verified 2026-08-09.
The original registry-submission work was tracked in closed issue [#19](https://github.com/MikkoParkkola/trvl/issues/19).

## Release Channels

These channels carry v1.21.0 and are part of the release workflow:

| Channel | Current artifact |
| --- | --- |
| [GitHub Releases](https://github.com/MikkoParkkola/trvl/releases/tag/v1.21.0) | Signed archives and checksums for macOS, Linux, and Windows |
| [Homebrew](https://github.com/MikkoParkkola/homebrew-tap/blob/main/Formula/trvl.rb) | `brew install MikkoParkkola/tap/trvl` |
| [npm](https://www.npmjs.com/package/trvl-mcp) | `trvl-mcp@1.21.0` wrapper |
| [GHCR](https://github.com/MikkoParkkola/trvl/pkgs/container/trvl) | `ghcr.io/mikkoparkkola/trvl:1.21.0` and `:latest`, multi-architecture |
| [Official MCP Registry](https://registry.modelcontextprotocol.io/?q=io.github.MikkoParkkola%2Ftrvl) | `io.github.MikkoParkkola/trvl@1.21.0` |
| Go module proxy | `github.com/MikkoParkkola/trvl@v1.21.0` |

## GitHub Referrer Baseline

Captured 2026-05-12 before this distribution pass:

| Referrer | Views | Unique visitors |
| --- | ---: | ---: |
| github.com | 54 | 24 |
| Google | 31 | 19 |
| chatgpt.com | 13 | 4 |
| bcnrsnl3m9wk.feishu.cn | 3 | 1 |
| t.co | 3 | 1 |
| moodle.chu.edu.tw | 1 | 1 |
| perplexity.ai | 1 | 1 |
| statics.teams.cdn.office.net | 1 | 1 |

Re-check with:

```bash
gh api repos/MikkoParkkola/trvl/traffic/popular/referrers
```

## Automated Distribution Metrics

Weekly aggregate metrics collection was added:

```bash
make distribution-metrics
```

The generated dashboard is tracked at [docs/internal/distribution-metrics.md](internal/distribution-metrics.md). Weekly JSON snapshots are written under ignored `.internal/metrics/` files:

- `.internal/metrics/downloads-$YYYYWW.json` for GitHub release asset downloads by version and asset
- `.internal/metrics/npm-$YYYYWW.json` for npm download counts

The 2026-05-12 baseline captured 337 GitHub release asset downloads and 0 npm `trvl` downloads because the npm downloads API returned `npm package or range not found`.

## Third-party directories

Directory pages are discovery mirrors, not release channels. Their descriptions and tool counts may lag the repository even when the listing itself is live. The README, changelog, and official MCP Registry are authoritative.

- [Glama](https://glama.ai/mcp/servers/MikkoParkkola/trvl)
- [LobeHub](https://lobehub.com/mcp/mikkoparkkola-trvl)
- [Smithery](https://smithery.ai/server/@MikkoParkkola/trvl)
- [MCPHub](https://www.mcphub.com/mcp-servers/MikkoParkkola/trvl)
- [Cursor Directory](https://cursor.directory/mcp/trvl)
- [PulseMCP](https://www.pulsemcp.com/servers/mikkoparkkola-trvl)
- [MCP Market](https://mcpmarket.com/server/trvl)

For example, MCP Market was reachable during the 2026-08-09 audit but still showed an old 9-tool/14-command snapshot. That is directory-cache drift, not the current trvl surface.

## Homebrew macOS Policy

Decision: Formula-only until Developer ID notarization is proven in release CI.

trvl intentionally publishes a Homebrew Formula, not a Cask. The release
workflow updates `MikkoParkkola/homebrew-tap` directly from the checksums emitted
by GoReleaser because GoReleaser's legacy Formula publisher is deprecated and
its supported replacement publishes Casks. The Formula install path relocates
the CLI binary in a way that avoids the `com.apple.quarantine` launch failure
that affected the prior Cask-style distribution attempt. A Cask must not be
introduced unless the release workflow first signs and notarizes Darwin
artifacts with a Developer ID certificate, staples the notarization ticket when
applicable, and verifies a quarantined install can run `trvl version` on macOS
before publishing.

The existing Formula path stays the supported Homebrew channel until that full
notarized-Cask path is implemented and proven. `scripts/ci/check-workflow-hygiene.sh`
blocks accidental `homebrew_casks` reintroduction while this policy is active.

## Listing Copy

Short description:

> AI travel agent with 1 smart MCP tool plus 66 legacy-compatible capabilities for flights, hotels, rental cars, trains, buses, ferries, price alerts, hidden-city search, and award redemptions. Free core providers, no personal API keys, one Go binary.

Install snippet:

```bash
brew install MikkoParkkola/tap/trvl
trvl mcp install
```

MCP config:

```json
{
  "mcpServers": {
    "trvl": {
      "command": "trvl",
      "args": ["mcp"]
    }
  }
}
```

Canonical links:

- Repository: https://github.com/MikkoParkkola/trvl
- Positioning: https://github.com/MikkoParkkola/trvl/blob/main/docs/POSITIONING.md
- Comparison matrix: https://github.com/MikkoParkkola/trvl/blob/main/docs/COMPARISON.md
