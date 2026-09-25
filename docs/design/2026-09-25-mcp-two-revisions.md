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

trvl's MCP server tells clients it can speak three published revisions,
including 2025-03-26. The current published set has moved on. A client written
to the specification's compatibility rule — stay within the revisions the
server actually claims — cannot learn a two-revision window from trvl, because
trvl still claims the older one.

Whose problem: a person connecting an MCP client to `trvl mcp`. They hit it
on first connect, whenever their client picks a protocol revision.

Why now: v1.22.0, tagged 2026-09-25, advertises three revisions. The request
after that tag was that compatibility be the two latest published revisions.
Leaving the third in the advertised set keeps a window the requester has
already closed.

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
   `2025-11-25`, latest first. `server/discover` reports that list.
2. A `2026-07-28` request is served as that revision.
3. An `initialize` of `2025-11-25` is served as `2025-11-25`, including `ping`
   and `resources/subscribe`.
4. A request that names any other revision is not served as that revision.
   The client is told the two revisions above.
5. An `initialize` that names an older legacy revision receives a legacy
   revision the server supports (`2025-11-25`), which is the negotiation rule
   of the legacy revision being kept. It does not add that older revision to
   the supported set.

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
