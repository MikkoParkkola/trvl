# MCP compatibility window: problem

Status: **implemented on `d70ce738`**. The problem and the solution were
reviewed before the code. The test plan was reviewed, then the tests were
written. This file is the record of those gates, not a request for a new
implementation.

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
   above, and `data.requested` is the revision the client named. On HTTP,
   when the header names a different revision from `_meta`, the answer is
   `-32020` instead. The streamable HTTP page requires that mismatch when
   the two values differ. Signal 4 is the case where the only named
   revision is outside the pair, or both channels name that same revision.
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
versioning page). The streamable HTTP page then says the header must match
`_meta`, and a difference is HTTP 400 with `-32020`. That includes a header
inside the pair and a `_meta` value outside it. `-32022` with
`data.supported` and `data.requested` is the answer when the only named
revision is outside the pair, or when the header and `_meta` name that same
outside revision.

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

On stdio the only declaration channel is `_meta`. On HTTP the
`MCP-Protocol-Version` header must match that field when the request is
modern. The 2026-07-28 streamable HTTP page says the header value must match
`io.modelcontextprotocol/protocolVersion` in `_meta`, and a mismatch is HTTP
400 with `-32020`. A version the server does not implement is HTTP 400 with
`-32022` and the supported list. A header of `2026-07-28` with no `_meta`
does not select the revision. The handshake string counts only when neither
channel names a revision. The first matching rule wins.

1. HTTP, and the header names a revision, and `_meta` names a different
   revision: HTTP 400 and `-32020`. `data` is absent. This is checked before
   unsupported-version. Header `2026-07-28` with `_meta` `2025-03-26` is this
   rule.
2. `_meta` names a revision outside the pair, and the header is absent or
   names that same revision: `-32022` with the `data` above.
   `data.requested` is that revision. On HTTP the status is 400. The same
   answer is used when the header names a revision outside the pair and
   `_meta` names none.
3. On HTTP, header `2026-07-28` and `_meta` names no revision: HTTP 400 and
   `-32020`. On HTTP, the same answer is used when `_meta` names
   `2026-07-28` and the header is absent. Stdio has no header, so a `_meta`
   of `2026-07-28` is rule 4.
4. `_meta` names `2026-07-28`, and on HTTP the header names it too: the
   request is served as `2026-07-28`. The result carries `resultType`. A
   list result also carries `ttlMs` and `cacheScope`, as it does today. No
   prior `initialize` is required. `ping` and `resources/subscribe` stay
   absent. An `initialize` whose `_meta` names `2026-07-28` while the
   handshake names something else is this rule, provided the HTTP header
   matches when the request is HTTP.
5. `_meta` names `2025-11-25`, or the HTTP header names `2025-11-25` and
   `_meta` names no revision: the request is served as `2025-11-25` on that
   request alone. `ping` and `resources/subscribe` work. The result has no
   `resultType` and no cache hints. On HTTP a matching header is not
   required for this legacy revision.
6. Neither channel names a revision: an `initialize` is answered
   `2025-11-25`, whether the handshake names `2025-11-25`, `2026-07-28`,
   `2025-03-26`, or anything else. That answer does not add the handshake
   revision to the supported set. Resource subscription stays on. Any other
   method with no declaration keeps that legacy shape, which is what
   v1.22.0 does.

The docs and changelog lines that still say the server advertises three
revisions are updated to the two. The historical `## [1.22.0]` section and
the ROADMAP sentence for that tag stay as the record of the tag. No version
bump and no tag.

### How the signals are met

1. The slice and `server/discover` are the two revisions, latest first.
2. Rule 4 serves `_meta` `2026-07-28`, with the HTTP header matching when
   the call is HTTP, as that revision, with `resultType`, and without a
   prior `initialize`.
3. Rule 5 serves `2025-11-25` from `_meta` or from a legacy header. Rule 6
   does the same for a handshake of `2025-11-25` when neither channel names
   a revision.
4. Rule 2 rejects a declared revision outside the pair with `-32022` and
   the `data` fields. Rule 1 is the HTTP mismatch when the two channels
   disagree, and that is not signal 4. A handshake string alone is rule 6.
5. Rule 6 answers a handshake-only `initialize` of anything other than
   `2025-11-25` with `2025-11-25`.

## Test plan

Status: **reviewed, then implemented** in `TestMCPWindow` and the retained
protocol tests. The "fails on this branch before the solution" column is the
record of what was red before `484f876d`. It is not a claim about the
current code.
Level is unit unless the row says HTTP. Type is the assertion's job.
Expected values are literals. A case that reads `supportedProtocolVersions`
and expects that same variable is not a case: `TestProtocol2026Discover`
does that today and will be replaced for the list assertion.

Stdio `HandleRequest` cannot see an HTTP header. Header rows go through
`HTTPServer.handleMCP`. One defect per row.

| id | proves | input | expect | fails on this branch before the solution |
|---|---|---|---|---|
| S1 | signal 1, rules of the slice | `server/discover` with `_meta` `2026-07-28` | `supportedVersions` is exactly `["2026-07-28","2025-11-25"]` in that order. `2025-03-26` is absent. `resultType` is `complete` | no. The list is already those two. The new assertion is the literals, so adding `2025-03-26` back to the slice fails it. The current discover test would stay green |
| S2 | signal 2, rule 4 | `tools/list` with `_meta` `2026-07-28` and no prior `initialize` | no error. `resultType` is `complete`. `cacheScope` is `public` | no. `TestProtocol2026Discover` and `TestProtocol2026ListEndpointsCarryCacheHints` already require this. They stay |
| S2b | signal 2, ping absent | `resources/subscribe` and `ping` with `_meta` `2026-07-28` | both return code `-32601` | no. `TestProtocol2026ListenReplacesSubscribeAndHonoursOptIn` already requires the subscribe rejection. Add `ping` to that same server, same `_meta`, so a 2026 session that grew `ping` back fails |
| S2c | rule 3 | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2026-07-28`, no `_meta`, `Mcp-Method: tools/list` | HTTP 400. Code `-32020`. No `resultType` | no. This is already `-32020`. The row fails if a modern header with no `_meta` is served as 2026 |
| S2d | rule 3, other missing channel | HTTP POST `tools/list`, `_meta` `2026-07-28`, no `MCP-Protocol-Version`, `Mcp-Method: tools/list` | HTTP 400. Code `-32020`. No `resultType` | yes. Today a 2026 body with no protocol header is served |
| S3 | signal 3, rule 6 | `initialize` handshake `2025-11-25`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. No `resultType`, no `ttlMs`. `resources.subscribe` is true. Then `ping` and `resources/subscribe` on that server succeed | no. `TestProtocol2026LegacySessionsUnchanged` already requires this for `2025-11-25`. It stays. The expected revision in the new rows is the literal `2025-11-25`, not the Go constant |
| S3b | signal 3, rule 5 | `tools/list` with `_meta` `2025-11-25` and no prior `initialize`, then `ping` and `resources/subscribe` on new requests with the same `_meta` | no error on any of the three. The list result has no `resultType`, no `ttlMs`, and no `cacheScope`. `ping` and `resources/subscribe` succeed | no. Deleting the legacy branch makes `ping` and `resources/subscribe` return `-32601` |
| S3c | rule 5, header alone | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-11-25`, no `_meta`, no `Mcp-Method`. A second POST `resources/subscribe` with the same header and no prior `initialize` | both HTTP 200. Neither body has an `error`. The list body has a `result` and has no `resultType`, no `ttlMs`, and no `cacheScope`. The subscribe body has no `error` | no. A JSON-RPC error with no `resultType` would have passed the old wording. The subscribe call fails if legacy subscription is only wired after a handshake |
| S3d | rule 5, one request | one server, in order: `tools/list` with `_meta` `2026-07-28`, then `tools/list` with `_meta` `2025-11-25`, then `tools/list` with no `_meta`, then `tools/list` with `_meta` `2026-07-28` again | first and fourth have `resultType` `complete`. Second and third have no `resultType`, no `ttlMs`, and no `cacheScope` | no. A revision remembered from the first request makes the second carry `resultType`, and this row fails |
| S4 | signal 4, rule 2 | `tools/list` with `_meta` `2025-03-26` | error code `-32022`. `data.supported` is exactly `["2026-07-28","2025-11-25"]`. `data.requested` is `2025-03-26`. No `result` | yes. Today the code is `-32022` and `data` is absent. `TestProtocolSupportIsTheTwoLatestRevisions` checks the code only and will grow this `data` assertion |
| S4b | rule 1 | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2026-07-28`, `_meta` `2025-03-26`, `Mcp-Method: tools/list` | HTTP 400. Code `-32020`. `data` is absent. No `resultType` | yes. Today `_meta` is rejected first, so the code is `-32022`. The row fails until a header that disagrees with `_meta` is `-32020` |
| S4c | rule 2, header alone | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-03-26`, no `_meta` | HTTP 400. Code `-32022`. `data.requested` is `2025-03-26`. `data.supported` is `["2026-07-28","2025-11-25"]`. Not `-32020` | yes. Today this is a header mismatch, `-32020` |
| S4d | rule 1, two bad names | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-06-18`, `_meta` `2025-03-26` | HTTP 400. Code `-32020`. `data` is absent | yes. Today the `_meta` rejection returns `-32022`. The names disagree, so the spec answer is a mismatch |
| S4h | rule 2, both channels agree on a bad name | HTTP POST `tools/list`, header and `_meta` both `2025-03-26`, `Mcp-Method: tools/list` | HTTP 400. Code `-32022`. `data.requested` is `2025-03-26`. `data.supported` is `["2026-07-28","2025-11-25"]` | yes. The code is already `-32022` and `data` is absent |
| S4e | signal 4, rule 2, initialize | `initialize` with handshake `2025-11-25` and `_meta` `2025-03-26`, no header | `-32022`. `data.requested` is `2025-03-26`. `data.supported` is `["2026-07-28","2025-11-25"]`. There is no `result` | yes. The code is already `-32022` and `data` is absent. If `initialize` is answered before the `_meta` check, the result is `protocolVersion` `2025-11-25` and this row fails |
| S4f | rule 1, other order | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-03-26`, `_meta` `2026-07-28`, `Mcp-Method: tools/list` | HTTP 400. Code `-32020`. `data` is absent. No `resultType` | no. This mismatch is already `-32020`. The row fails if the bad header is ignored because `_meta` is supported |
| S4g | rule 2, unknown name | `tools/list` with `_meta` `1900-01-01` | `-32022`. `data.requested` is `1900-01-01`. `data.supported` is the two literals | yes. `data` is absent. A denylist of only `2025-03-26` would accept this name |
| S5 | signal 5, rule 6 | `initialize` handshake `2026-07-28`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. `resources.subscribe` is true. No `resultType`. No error | yes. Today the handshake is answered `2026-07-28` with subscription off |
| S5b | signal 5 | `initialize` handshake `2025-03-26`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. No `resultType`. A later `server/discover` with `_meta` `2026-07-28` still has `supportedVersions` exactly `["2026-07-28","2025-11-25"]` | the initialize half is already green (`TestProtocol2026LegacySessionsUnchanged`). The discover half is S1. This row keeps the handshake from being added to the advertised set |
| S5c | rule 4, handshake ignored | `initialize` with handshake `2025-11-25` and `_meta` `2026-07-28`, no header | no error. `protocolVersion` is `2026-07-28`. `resources.subscribe` is false. `resultType` is `complete` | yes. Today `protocolVersion` follows the handshake and is `2025-11-25`, while `resultType` is still added because `_meta` is 2026 |
| S5d | rule 6, unknown handshake | `initialize` handshake `1900-01-01`, no `_meta`, no header | `protocolVersion` is `2025-11-25`. No error. No `resultType` | no. An unknown handshake is already answered `2025-11-25`. The row fails if that handshake is `-32022` or is echoed as `1900-01-01` |
| S5e | rule 5, `_meta` wins over the handshake | `initialize` handshake `2026-07-28` and `_meta` `2025-11-25`, no header | `protocolVersion` is `2025-11-25`. `resources.subscribe` is true. No `resultType` | no. `_meta` already selects the revision. The row fails if the handshake is served as 2026 |
| S5f | rule 4, HTTP initialize | HTTP `initialize`, header and `_meta` both `2026-07-28`, handshake `2025-11-25`, `Mcp-Method: initialize` | HTTP 200. `protocolVersion` is `2026-07-28`. `resources.subscribe` is false. `resultType` is `complete` | no, once rule 4 is in place. The row fails if the handshake wins or the matching header is rejected |
| S5g | rule 3, HTTP initialize | HTTP `initialize`, header `2026-07-28`, handshake `2025-11-25`, no `_meta`, `Mcp-Method: initialize` | HTTP 400. Code `-32020`. No `resultType` | no. A 2026 header with no `_meta` is already `-32020` for `tools/list`. The row fails if `initialize` is exempt |
| R2c | rule 1, handshake ignored | HTTP `initialize`, header `2026-07-28`, `_meta` `2025-11-25`, handshake `2025-11-25`, `Mcp-Method: initialize` | HTTP 400. Code `-32020`. `data` is absent | no. The two channels disagree. The row fails if the handshake is treated as agreement |
| R2 | rule 1 | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2026-07-28`, `_meta` `2025-11-25`, `Mcp-Method: tools/list` | code `-32020`. `data` is absent. Body has no `resultType` | no. The header and `_meta` are both in the pair and differ, and that is already `-32020` |
| R2b | rule 1, other order | HTTP POST `tools/list`, header `MCP-Protocol-Version: 2025-11-25`, `_meta` `2026-07-28`, `Mcp-Method: tools/list` | code `-32020`. `data` is absent. Body has no `resultType` | no. Same mismatch in the other order. It fails if only one order is checked |
| R3 | rules 4 and 5, channels agree | HTTP POST `tools/list` twice. First: header and `_meta` both `2026-07-28`, `Mcp-Method: tools/list`. Second: header and `_meta` both `2025-11-25`, no `Mcp-Method` | first HTTP 200, `resultType` `complete`, no error. Second HTTP 200, no error, no `resultType`, no `ttlMs`, no `cacheScope` | no. Agreement on either revision is already served that way. The row fails if agreement is rejected |
| R5 | rule 6 default | `tools/list` with no `_meta` and no header, no prior `initialize` | no error. No `resultType`. No `ttlMs`. `ping` on the same shape succeeds | no. This is the current default. It fails if an undeclared request becomes 2026 |
| D1 | solution docs sentence | `CHANGELOG.md` Unreleased section, the `## [1.22.0]` section, `docs/CLI.md`, `ROADMAP.md` | Unreleased contains the literals `2026-07-28` and `2025-11-25`. The `## [1.22.0]` section still contains `with **2025-11-25** or **2025-03-26**`. CLI contains `2026-07-28 and 2025-11-25`. ROADMAP still contains `v1.22.0** (2026-09-25): current release line` | no. The changelog and CLI already say this. The row fails if Unreleased drops the pair or the 1.22.0 paragraph is rewritten |

Sweep before review:

- A1, A4. S1 states both revisions and the order, not a length. S4 states both the refused code and the permitted list inside `data.supported`.
- A2. `data` on `-32022` is the key set `supported` and `requested`. R2 says `data` is absent, so an error that carries the list does not pass the mismatch row.
- A3. S1 names what arrived. Absence of `2025-03-26` is additional, not the only assertion.
- A5. S4b is the row that dies if an outside-the-pair `_meta` is ignored whenever the header is `2026-07-28`. S4c dies if a header outside the pair is only a mismatch. S2c dies if a header of `2026-07-28` with no body revision is treated as the legacy default.
- A8. New assertions use the literals `2026-07-28`, `2025-11-25`, `2025-03-26`, `-32022`, and `-32020`. They do not expect `supportedProtocolVersions` or `protocolVersion`.
- A9. S4d is only the tie-break. S4b is only precedence against a successful 2026 response. They are not the same input.
- A6. No time. Not applicable.
- A7. `TestProtocolSupportIsTheTwoLatestRevisions` and `TestProtocol2026LegacySessionsUnchanged` expect the literals `2026-07-28`, `2025-11-25`, and `-32022`, not the Go constants.

`TestProtocol2026LegacySessionsUnchanged` does not send a `2026-07-28` handshake. S5 is that handshake, expecting `2025-11-25`. The existing loop must not gain a `2026-07-28` handshake that expects the modern answer.
