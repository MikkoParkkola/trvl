# MCP compatibility window: problem

Status: **unratified problem**. The solution section is closed until this problem
is reviewed and approved on its own. No implementation is authorized by this
document.

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

Closed. Not written. Opens only after this problem is ratified by the two
review vendors required for this author.
