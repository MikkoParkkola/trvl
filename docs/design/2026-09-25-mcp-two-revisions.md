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

A request that names `2026-07-28` in `_meta`, or on HTTP in
`MCP-Protocol-Version` when the body does not name the other supported
revision, is served as `2026-07-28`. The result carries `resultType`. No
prior `initialize` is required. `ping` and `resources/subscribe` stay absent
on that revision, as they are today.

A request that names `2025-11-25` in `_meta`, or in that header on the same
terms, is served as `2025-11-25` on that request alone. `ping` and
`resources/subscribe` work. The result has no `resultType`.

A request that names any other revision in `_meta` or in that header,
including an `initialize`, is not served. The response is `-32022` with the
`data` above.

When the header and the body each name one of the two supported revisions and
the two names differ, the response stays `-32020`. The client is not given a
supported-list retry for a pair it already knows.

An `initialize` that names its revision only in the handshake, with no `_meta`
revision and no protocol header, is answered `2025-11-25` when the handshake
names `2025-11-25`, and also answered `2025-11-25` when it names anything
else, including `2026-07-28` and `2025-03-26`. That answer does not add the
named revision to the supported set. Resource subscription stays on, because
the session is the legacy revision.

A request that declares no revision and is not `initialize` keeps the legacy
shape. That is what v1.22.0 does, and the signals do not ask for a different
default.

The docs and changelog lines that still say the server advertises three
revisions are updated to the two. The historical `## [1.22.0]` section and
the ROADMAP sentence for that tag stay as the record of the tag. No version
bump and no tag.

### How the signals are met

1. The slice and `server/discover` are the two revisions, latest first.
2. `_meta` or the HTTP header naming `2026-07-28` is served as that revision,
   with `resultType`, and without a prior `initialize`.
3. A `2025-11-25` handshake with no other declaration, and a request that
   names `2025-11-25` in `_meta` or the header, are served as that revision,
   including `ping` and `resources/subscribe`, without `resultType`.
4. Any other revision declared in `_meta` or in `MCP-Protocol-Version` is
   `-32022` with `data.supported` and `data.requested`. A handshake string
   alone is signal 5, not this one.
5. A handshake-only `initialize` of anything other than `2025-11-25` is
   answered `2025-11-25`.
