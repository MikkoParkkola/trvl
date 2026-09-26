# MCP compatibility window: problem

Status: **problem ratified, solution unratified**. No implementation is
authorized until the solution below is reviewed on its own.

Date: 2026-09-25

## Quarantine

Pull request 672 (`e4619ea0`, branch `fix/mcp-two-protocol-versions`) already
narrows the advertised set and was opened before this problem was written.
That patch is a pre-existing proposal. It is set aside here. It is not
evidence that this problem is solved, and it is not the design.

## Problem

On the tagged release v1.22.0 (`d9c84ca3`), `supportedProtocolVersions` in
`mcp/protocol_2026.go` is `2026-07-28`, `2025-11-25`, and `2025-03-26`.
`server/discover` reports that list. Two behaviours do not honour it.

A handshake that names `2025-03-26` is answered `2025-11-25`.
`handleInitialize` in `mcp/server.go` advertises the constant `2025-11-25`
unless the handshake names exactly `2026-07-28`. The legacy rule below says a
server that supports the requested revision must answer with that same
revision. v1.22.0 lists `2025-03-26` and answers with a different one.

A handshake that names `2026-07-28` is answered `2026-07-28`, with resource
subscription turned off. The 2026-07-28 versioning page says an `initialize`
request selects legacy semantics. v1.22.0 treats that handshake as the modern
revision.

Whose problem: a client that trusts the list `server/discover` returns. It
can select `2025-03-26` and not be served that revision. The same client,
sending `2026-07-28` on the handshake, gets a modern session from a request
the specification keeps on the legacy side.

Why now: both mismatches are on the release tagged 2026-09-25. The requester
asked that day for the two latest published revisions. `2025-03-26` is outside
that pair, and it is the revision the handshake lists and does not answer.
The handshake that names `2026-07-28` is the second mismatch: the specification
keeps `initialize` on the legacy side.

## Measured constraints

These are facts checked on 2026-09-25, not choices.

Published revisions, from `https://modelcontextprotocol.io/llms.txt` (nab,
HTTP 200). Newest first: `2026-07-28`, `2025-11-25`, `2025-06-18`,
`2025-03-26`, `2024-11-05`. Nothing newer than `2026-07-28` is listed.

`2026-07-28` is the modern era: every request carries
`_meta["io.modelcontextprotocol/protocolVersion"]`. A version the server does
not implement **must** be rejected with JSON-RPC `-32022`
(`UnsupportedProtocolVersion`) whose `data` lists `supported` and `requested`.
The specification's own example `supported` array is `["2026-07-28",
"2025-11-25"]`. The client is expected to retry from that list.
Source: `https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning`.

`2025-11-25` and earlier are the legacy era. An `initialize` handshake agrees
the revision. If the server supports the requested revision, it **must**
answer with that same revision. Otherwise it **must** answer with another
revision it supports. This should be the latest revision it supports for that
handshake. A legacy `initialize` selects legacy semantics, so the revision in
that response is a legacy revision, not `2026-07-28`.
Sources: `https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle`
(version negotiation) and the 2026-07-28 versioning page (legacy / dual-era).

trvl's JSON-RPC `Error` value is `Code` and `Message` only
(`mcp/protocol.go`). The open proposal returns `-32022` with a message and
no `data`. That fact is recorded so the solution gate cannot ignore it. This
document does not choose a repair.

## Acceptance signal

Solved means all of the following are observable against a server built from
the revision under review:

1. The revisions a client can select are exactly `2026-07-28` and
   `2025-11-25`, latest first. `server/discover` reports that list. The
   2026-07-28 versioning page requires every server to implement
   `server/discover` so a client can read the supported revisions before any
   other call. trvl already handles that method (`mcp/protocol_2026.go`).
2. A request that names `2026-07-28` in `_meta` is served as that revision.
   Its result carries `resultType`. It does not need a prior `initialize`.
3. An `initialize` whose handshake names `2025-11-25`, and which does not
   also name a revision in `_meta`, is served as `2025-11-25`. That session
   still accepts `ping` and `resources/subscribe`, and its results do not
   carry `resultType` or cache hints. A request that names `2025-11-25` in
   `_meta` is the same revision on that request alone, with that same legacy
   shape, and it does not need a prior `initialize`. It is not rejected.
   The specification's retry path sends a version from `data.supported`
   back on the request, and both revisions are in that list.
4. A request that names a revision other than `2026-07-28` and `2025-11-25`
   in `_meta`, including an `initialize` that does so, is not served as that
   revision. The `-32022` error's `data.supported` is the two revisions
   above, and `data.requested` is the revision the client named.
5. An `initialize` that names its revision only in the handshake parameters,
   and names anything other than `2025-11-25`, is not signal 4. It receives
   `2025-11-25`. That includes a handshake that names `2026-07-28` or an
   older legacy revision. The named revision is not added to the supported
   set. The initialize result carries that one negotiated revision.

## Out

- A release tag, or a bump of `server.json` / `npm/package.json`.
- The Trivago outbound client. trvl is a client of that server; its
  `2025-03-26` constant is not this server's compatibility window.
- Rewriting the historical `## [1.22.0]` changelog or the ROADMAP sentence
  that records what that tag shipped.
- Replacing the hand-rolled server with the official Go SDK.

## Unknowns

Resolved: which two revisions are latest — `llms.txt` fetched 2026-09-25 —
`2026-07-28` then `2025-11-25` — the pair in the acceptance signal is those
two.

Resolved: what a modern server returns for an unsupported version — 2026-07-28
versioning page — `-32022` with `data.supported` and `data.requested` — the
acceptance signal requires the client to be told the two revisions. The
mechanism is not chosen here.

Resolved: what `initialize` does with a revision outside the pair — 2025-11-25
lifecycle, "MUST respond with another protocol version it supports" — signal 5.
Not load-bearing on which Go type carries the modern error `data`.

Re-check, not an open unknown: before a solution review, fetch
`https://modelcontextprotocol.io/llms.txt` again. If the two newest published
revisions are no longer `2026-07-28` and `2025-11-25`, this problem reopens.
Owner of that fetch: whoever opens the solution gate. If the fetch fails, the
solution gate stays closed.

No deferred unknowns.

## Asked and answered

- 2026-09-25, requester: support the two latest MCP revisions for
  compatibility. That sentence is the problem. The pair is fixed by the
  specification index, not by a further guess.
- 2026-09-25, requester: follow the development process for design and
  implementation, and the Definition of Done for completion checks. That
  binds the gate order. It does not choose a protocol mechanism.

## Solution

The problem was ratified on 2026-09-26. Claude Opus 5 returned SHIP, exit 0,
on commit `50b4f795`, and closed the three findings that were open at that
gate. Copilot, model `gpt-6-astra`, returned SHIP, exit 0, on the same text.
The requester named Copilot on that model as the seat in place of `gpt-review`,
which was still out of quota. GLM had already returned SHIP on an earlier
revision of the problem; that row is not this ratification.

Re-check before this section, 2026-09-26: `https://modelcontextprotocol.io/llms.txt`
still lists the dated revisions `2026-07-28`, `2025-11-25`, `2025-06-18`,
`2025-03-26`, `2024-11-05`. A `draft` entry is present and is not a published
revision. The pair is unchanged, so the problem stays closed.

One residual from the problem review is taken up here rather than by reopening
the problem. Claude rated it BEFORE-DEPLOY: on HTTP the same per-request
revision is also carried in the `MCP-Protocol-Version` header (2026-07-28
versioning page). A header outside the pair has to get the same `-32022`
answer as `_meta` outside the pair, including `data.supported` and
`data.requested`. A header and a body that name two different revisions from
the pair stay a header mismatch (`-32020`), which is disagreement, not an
unknown revision.

### Rejected

The open patch `e4619ea0` only removes `2025-03-26` from
`supportedProtocolVersions`. It leaves the handshake that names `2026-07-28`
answered as `2026-07-28`, and it returns `-32022` with no `data`. That fails
signals 4 and 5 and the error-shape constraint. It is not the solution.

The official Go SDK is out of the problem. It would own the HTTP server.

Rejecting every handshake that is not exactly `2025-11-25` with `-32022`
breaks the legacy rule the problem quotes: a server answers a legacy
handshake with a revision it supports.

Answering a handshake of `2026-07-28` with `2026-07-28`, because that revision
is supported, treats `initialize` as the modern selector. The versioning page
says `initialize` selects legacy semantics. The modern revision is selected
by `_meta`, and on HTTP also by the protocol header.

### Chosen

The hand-rolled server stays. `supportedProtocolVersions` is `2026-07-28`
then `2025-11-25`. `server/discover` reports that slice.

`mcp.Error` gains an optional `data` value, omitted on every error that does
not set it. The unsupported-version error sets `supported` to the two
revisions, latest first, and `requested` to the revision the client named.
A second error type was rejected because this server has one JSON-RPC error
struct.

`_meta` and the HTTP `MCP-Protocol-Version` header are the two declaration
channels. The handshake string is a declaration only when both of those are
absent. The first matching rule wins:

1. If `_meta` or the header names a revision outside the pair, the response
   is `-32022` with the `data` above. `data.requested` is the `_meta` value
   when that value is outside the pair, and otherwise the header value.
   This is checked before a mismatch and before serving. A header of
   `2026-07-28` together with `_meta` of `2025-03-26` is this rule, not a
   successful 2026 request.
2. If `_meta` and the header each name one of the two supported revisions
   and the names differ, the response is `-32020`. The handshake string is
   not one of those two names.
3. If `_meta` or the header names `2026-07-28`, and the other channel is
   absent or names the same revision, the request is served as `2026-07-28`.
   The result carries `resultType` and no cache hints. No prior `initialize`
   is required. `ping` and `resources/subscribe` stay absent, as they are
   today. An `initialize` whose header or `_meta` names `2026-07-28`, and
   whose handshake says something else, is this rule.
4. If `_meta` or the header names `2025-11-25`, and the other channel is
   absent or names the same revision, the request is served as `2025-11-25`
   on that request alone. `ping` and `resources/subscribe` work. The result
   has no `resultType` and no cache hints.
5. If neither channel names a revision, an `initialize` is answered
   `2025-11-25`, whether the handshake names `2025-11-25`, `2026-07-28`,
   `2025-03-26`, or anything else. That answer does not add the handshake
   revision to the supported set. Resource subscription stays on. A request
   that is not `initialize` and declares no revision keeps that same legacy
   shape, which is what v1.22.0 does.

The docs and changelog lines that still say the server advertises three
revisions are updated to the two. The historical `## [1.22.0]` section and
the ROADMAP sentence for that tag stay as the record of the tag. No version
bump and no tag.

### How the signals are met

1. The slice and `server/discover` are the two revisions, latest first.
2. Rule 3 serves `_meta` or the header naming `2026-07-28` as that revision,
   with `resultType`, and without a prior `initialize`.
3. Rule 4 serves a request that names `2025-11-25` in `_meta` or the header
   as that revision, including `ping` and `resources/subscribe`, without
   `resultType` or cache hints. Rule 5 does the same for a handshake of
   `2025-11-25` when neither channel names a revision.
4. Rule 1 rejects any other revision in `_meta` or the header with `-32022`
   and the `data` fields. A handshake string alone is rule 5, not this one.
5. Rule 5 answers a handshake-only `initialize` of anything other than
   `2025-11-25` with `2025-11-25`.

## Test plan

Status: **unratified**. No new test code until this table is reviewed.
Level is unit unless the row says HTTP. Type is the assertion's job.
Expected values are literals. A case that reads `supportedProtocolVersions`
and expects that same variable is not a case: `TestProtocol2026Discover`
does that today and will be replaced for the list assertion.

Stdio `HandleRequest` cannot see an HTTP header. Header rows go through
`HTTPServer.handleMCP`. One defect per row.

| id | proves | input | expect | fails on this branch before the solution |
|---|---|---|---|---|
| S1 | signal 1, rules of the slice | `server/discover` with `_meta` `2026-07-28` | `supportedVersions` is exactly `["2026-07-28","2025-11-25"]` in that order. `2025-03-26` is absent. `resultType` is `complete` | no. The list is already those two. The new assertion is the literals, so adding `2025-03-26` back to the slice fails it. The current discover test would stay green |
| S2 | signal 2, rule 3 | `tools/list` with `_meta` `2026-07-28` and no prior `initialize` | no error. `resultType` is `complete`. `cacheScope` is `public` | no. `TestProtocol2026Discover` and `TestProtocol2026ListEndpointsCarryCacheHints` already require this. They stay |
| S2b | signal 2, ping absent | `resources/subscribe` and `ping` with `_meta` `2026-07-28` | both return code `-32601` | no. `TestProtocol2026ListenReplacesSubscribeAndHonoursOptIn` already requires the subscribe rejection. Add `ping` to that same server, same `_meta`, so a 2026 session that grew `ping` back fails |
| S2c | rule 3, header alone | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2026-07-28`, no `_meta`, `Mcp-Method: tools/list` | HTTP 200. Body has `resultType` `complete` and no error | yes. Today the header is compared with the legacy constant and the response is `-32020` |
| S3 | signal 3, rule 5 | `initialize` handshake `2025-11-25`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. No `resultType`, no `ttlMs`. `resources.subscribe` is true. Then `ping` and `resources/subscribe` on that server succeed | no. `TestProtocol2026LegacySessionsUnchanged` already requires this for `2025-11-25`. It stays. The expected revision in the new rows is the literal `2025-11-25`, not the Go constant |
| S3b | signal 3, rule 4 | `tools/list` with `_meta` `2025-11-25` and no prior `initialize` | no error. Result JSON has neither `resultType` nor `ttlMs`. A following `ping` on that same request shape succeeds | no. This is the regression guard for serving the legacy revision through `_meta`. Deleting the legacy branch makes `ping` return `-32601` |
| S3c | rule 4, header alone | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-11-25`, no `_meta`, no `Mcp-Method` | HTTP 200. Body has no `resultType` and no `ttlMs` | no. A legacy header that matches the default revision is already accepted. The row stops a later change from requiring 2026 headers on that header |
| S4 | signal 4, rule 1 | `tools/list` with `_meta` `2025-03-26` | error code `-32022`. `data.supported` is exactly `["2026-07-28","2025-11-25"]`. `data.requested` is `2025-03-26`. No `result` | yes. Today the code is `-32022` and `data` is absent. `TestProtocolSupportIsTheTwoLatestRevisions` checks the code only and will grow this `data` assertion |
| S4b | rule 1 before rule 3 | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2026-07-28`, `_meta` `2025-03-26`, `Mcp-Method: tools/list` | `-32022`. `data.requested` is `2025-03-26`. Body has no `resultType` | yes. The code is already `-32022` because `_meta` is rejected, and `data` is absent. If rule 1 is deleted and the header is served, the body has `resultType` and this row fails |
| S4c | rule 1, header alone | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-03-26`, no `_meta` | `-32022`. `data.requested` is `2025-03-26`. `data.supported` is the same two literals. Not `-32020` | yes. Today this is a header mismatch, `-32020` |
| S4d | rule 1 tie-break | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-06-18`, `_meta` `2025-03-26` | `-32022`. `data.requested` is `2025-03-26`, not `2025-06-18` | yes. `data` is absent today. The two bad names are the whole point: one field, `_meta` wins |
| S5 | signal 5, rule 5 | `initialize` handshake `2026-07-28`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. `resources.subscribe` is true. No `resultType`. No error | yes. Today the handshake is answered `2026-07-28` with subscription off |
| S5b | signal 5 | `initialize` handshake `2025-03-26`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. No `resultType`. `supportedVersions` from a later discover that names `2026-07-28` in `_meta` still does not contain `2025-03-26` | the initialize half is already green (`TestProtocol2026LegacySessionsUnchanged`). The discover half is S1. This row keeps the handshake from being added to the advertised set |
| R2 | rule 2 | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2026-07-28`, `_meta` `2025-11-25`, `Mcp-Method: tools/list` | code `-32020`. `data` is absent. Body has no `resultType` | no, if the current mismatch check compares header to `_meta`. The row fails if that pair is served, or if it is rejected as `-32022` |
| R5 | rule 5 default | `tools/list` with no `_meta` and no header, no prior `initialize` | no error. No `resultType`. No `ttlMs`. `ping` on the same shape succeeds | no. This is the current default. It fails if an undeclared request becomes 2026 |
| D1 | solution docs sentence | `docs/CLI.md` and the Unreleased changelog | the CLI row contains the literal `2026-07-28 and 2025-11-25`. `## [1.22.0]` remains. ROADMAP still contains `v1.22.0** (2026-09-25): current release line` | no. `TestPublicDocsAdvertiseCurrentCounts` and `TestReleaseFacingDocsStayAligned` already require those literals. They stay. No new docs test |

Sweep before review:

- A1, A4. S1 states both revisions and the order, not a length. S4 states both the refused code and the permitted list inside `data.supported`.
- A2. `data` on `-32022` is the key set `supported` and `requested`. R2 says `data` is absent, so an error that carries the list does not pass the mismatch row.
- A3. S1 names what arrived. Absence of `2025-03-26` is additional, not the only assertion.
- A5. S4b is the row that dies if an outside-the-pair `_meta` is ignored whenever the header is `2026-07-28`. S4c dies if a header outside the pair is only a mismatch. S2c dies if a header of `2026-07-28` with no body revision is treated as the legacy default.
- A8. New assertions use the literals `2026-07-28`, `2025-11-25`, `2025-03-26`, `-32022`, and `-32020`. They do not expect `supportedProtocolVersions` or `protocolVersion`.
- A9. S4d is only the tie-break. S4b is only precedence against a successful 2026 response. They are not the same input.
- A6, A7. No time and no named-constant identity. Not applicable.

`TestProtocol2026LegacySessionsUnchanged` does not send a `2026-07-28` handshake. S5 is that handshake, expecting `2025-11-25`. The existing loop must not gain a `2026-07-28` handshake that expects the modern answer.
