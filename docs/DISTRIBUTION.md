# Distribution Status

Tracking issue: [MIK-3441](https://linear.app/parm/issue/MIK-3441/p1pos-trvl-directory-submissions-smitheryai-awesome-mcp-mcp-registry)
Related GitHub issue: [#19](https://github.com/MikkoParkkola/trvl/issues/19)

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

## Registry Matrix

| Channel | Status | Evidence / Next Action |
| --- | --- | --- |
| Smithery | Not live | `https://smithery.ai/servers/@MikkoParkkola/trvl` returned 404 on 2026-05-12. `smithery mcp publish . -n @MikkoParkkola/trvl` failed because the current CLI attempted to bundle the repo path as an shttp server. Smithery now expects a public Streamable HTTP endpoint or an MCPB bundle. |
| awesome-mcp-servers | PR open, blocked | [punkpeye/awesome-mcp-servers#5137](https://github.com/punkpeye/awesome-mcp-servers/pull/5137) is open and clean, but maintainer automation requires a live Glama listing and score badge. |
| Official MCP Registry | Release path prepared; not live | `server.json`, the OCI image label, and the release workflow publish step are present. `https://registry.modelcontextprotocol.io/v0/servers?search=trvl` returned an empty list and the direct `io.github.MikkoParkkola/trvl` lookup returned 404 on 2026-05-12. The v1.2.1 publish job authenticated successfully, then failed with a namespace case mismatch (`io.github.MikkoParkkola/*` allowed, lowercase owner attempted); `server.json` and the OCI label now use the authorized GitHub owner casing for the next tagged publish. |
| PulseMCP | Not verified live | Simple unauthenticated curl to PulseMCP returned 403 on 2026-05-12. Re-check after the official MCP Registry publication, since PulseMCP participates in the registry ecosystem. |
| mcp.so | Submitted, not live | [chatmcp/mcpso#2288](https://github.com/chatmcp/mcpso/issues/2288) tracks the submission. `https://mcp.so/server/trvl` returned "Project not found" on 2026-05-12. |
| Glama | Not live; repo metadata fixed | `https://glama.ai/api/mcp/v1/servers/MikkoParkkola/trvl` returned 404 on 2026-05-12. `glama.json` is now tracked so the repo exposes the maintainer manifest. Manual "Add Server" flow may still be required. |

## Listing Copy

Short description:

> AI travel agent with 61 MCP tools for flights, hotels, trains, buses, ferries, price alerts, hidden-city search, and award redemptions. Free core providers, no personal API keys, one Go binary.

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
