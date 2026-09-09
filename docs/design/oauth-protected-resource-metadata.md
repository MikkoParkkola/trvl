# OAuth Protected Resource Metadata (RFC 9728) for HTTP MCP mode

Status: designed, implementation pending
Date: 2026-09-09

## Why

MCP spec rev 2025-11-25 requires an HTTP MCP server to implement OAuth 2.0
Protected Resource Metadata (RFC 9728) so clients can discover which
authorization server(s) protect it. trvl's HTTP transport (`mcp/http.go`)
serves only `/mcp`, `/health`, `/dashboard` today and has no RFC 9728 support
at all. This doc settles the open questions before writing code.

## (a) Where does `authorization_servers` come from?

The MCP spec (rev 2025-11-25) requires the PRM document to name at least one
authorization-server issuer URL for an OAuth-protected server; RFC 9728 §2
itself lists `authorization_servers` as OPTIONAL, so this requirement comes
from MCP, not the RFC. trvl's existing `--oauth-introspection-url` is a token
*introspection* endpoint, not the issuer identifier — the two are often on
different hosts/paths at real IdPs (e.g. Auth0's introspection endpoint is
`https://tenant.auth0.com/oauth/introspect`, but the issuer clients discover
against is exactly `https://tenant.auth0.com/`). Deriving one from the other
would be a guess trvl has no basis for.

**Decision: add `--oauth-issuer` / `TRVL_MCP_OAUTH_ISSUER`.** It is the
`authorization_servers[0]` value verbatim.

**When OAuth is configured but the issuer is not set: refuse to start.**
Same failure shape as `requireHTTPAuth`/`requireRemoteAuth` (GH-89.AUTH.4) —
trvl already treats "started with an incomplete security config" as fatal,
not a silent degrade. `authorization_servers` is a MUST field per the MCP
spec; serving a PRM document without it (or not serving PRM at all while
claiming OAuth support) is worse than refusing to start, because it looks
compliant to a client's discovery probe while failing to name an issuer. This
mirrors the codebase's existing convention: missing *required* config fails
closed at startup (`requireHTTPAuth`).

**`--oauth-audience` moves into that same required-and-fail-closed bucket.**
MCP 2025-11-25 requires resource servers to bind and validate token
audience: a token issued for another service must not be accepted. Today
(`mcp/http_auth.go:224` `audienceMatches`, called from `authenticateOAuth` at
`:168`) audience checking only runs when `--oauth-audience` is set — with no
audience configured, `authenticateOAuth` accepts any active token from the
introspection endpoint regardless of its `aud` claim. That is fail-*open*,
not fail-closed; `NewHTTPAuth` (`:57-61`) only logs a startup warning for the
gap today, it does not refuse to start. That warn-only posture predates this
spec requirement and no longer satisfies it.

**Decision:** when `--oauth-introspection-url` is configured, require
`--oauth-audience` too, and refuse to start without it — the same
fail-closed treatment `--oauth-issuer` gets above, replacing today's
warning. Audience *matching* itself (`audienceMatches`) is already correctly
enforced whenever an audience value is present; only the "is one present"
gate needs to become mandatory.

## (b) Where does `resource` come from?

RFC 9728 §2 requires `resource`: the canonical URI clients use to reach this
MCP server. trvl binds `--host`/`--port`, but that's the bind address, not
necessarily the address a client sees (reverse proxy, container port
mapping, etc).

**Decision: add `--public-url` / `TRVL_MCP_PUBLIC_URL`.** RFC 9728 §2
requires `resource` to be an https URI (it borrows the well-known-URI
construction and identifier rules of RFC 8414 §3.3, which impose the same
https requirement on issuer/resource identifiers). trvl cannot default to
`http://<host>:<port>` and stay compliant, so:

- When `--oauth-introspection-url` is configured, `--public-url` is
  required and must be an `https://` URL — refuse to start otherwise, same
  fail-closed treatment as `--oauth-issuer` and `--oauth-audience` above.
- **Exception:** `http://localhost` and `http://127.0.0.1` public URLs are
  accepted without erroring, for local development only. This is a known,
  documented non-compliant convenience (an RFC 9728 client is free to reject
  an http resource identifier) — not a general http allowance.

`resource` in the served document is `<public-url>/mcp` — the MCP endpoint
itself, not the bare host — because that's the identifier a client actually
holds a token for.

## (c) Static bearer tokens have no authorization server — PRM is meaningless there

trvl also supports `--token`/`--read-token`/`--write-token` with no OAuth
server involved at all. RFC 9728 exists to point a client at *an
authorization server*; there isn't one to name in static-token mode.

**Decision: serve PRM only when `--oauth-introspection-url` is configured.**
When only static tokens are configured, both well-known routes return `404`
(not registered) exactly as they would on any build that predates this
feature. No PRM document, no `WWW-Authenticate: resource_metadata=...` on
401s either — nothing here claims OAuth support it can't back up.

## (d) Which routes, the well-known URL, and the 401 header

The spec requires implementing *one* of: the `WWW-Authenticate` header
mechanism, or a well-known URI (either path-suffixed or root form).

**Decision: do all three; it's one extra route and costs nothing.**

**Well-known URL construction (RFC 9728 §3).** The well-known path segment
goes immediately after the authority; any path component `--public-url`
carries (e.g. a reverse-proxy mount prefix) comes *after* the well-known
segment, not before it. With `<base>` the path component of `--public-url`
(empty by default):

- `GET <scheme>://<authority>/.well-known/oauth-protected-resource<base>/mcp`
  (path-suffixed to the MCP endpoint — the form MCP clients try first for a
  path-mounted server)
- `GET <scheme>://<authority>/.well-known/oauth-protected-resource` (root
  form, unconditional — no `<base>/mcp` suffix — for clients that don't try
  the path-suffixed form first)

Example: `--public-url=https://host/base` gives the path-suffixed URL
`https://host/.well-known/oauth-protected-resource/base/mcp` — **not**
`https://host/base/.well-known/oauth-protected-resource/mcp`.

Both well-known routes serve the identical document.

**401 challenge scope.** The initial challenge must not push a read-only
client into consenting to write access. `mcp/http_auth.go:281
toolWriteRequirement` / `:290 toolRequiresWrite` already know, given a
parsed request, whether the called tool requires write — reuse them:

- Default challenge scope is `trvl:read`.
- Escalate to `scope="trvl:write"` only when the request is a `tools/call`
  for a tool `toolRequiresWrite` reports as write.

The unauthenticated-request 401 currently fires at the top of `handleMCP`
(`mcp/http.go:108-114`), before the body is read, so the tool being called
isn't known yet at that point. Restructure so the body is read and parsed
before the missing/invalid-token check runs, so it can pick the scope; a
request with no body, an unparseable body, or a call to a read-only tool
falls back to `scope="trvl:read"`.

Full header when OAuth is configured (omitted in static-token-only mode,
per (c)):
`WWW-Authenticate: Bearer resource_metadata="<path-suffixed well-known URL>", scope="<trvl:read|trvl:write>"`

## (e) Discovery endpoints are unauthenticated

A discovery document behind auth can't be discovered — a client with no
token yet is precisely who needs to fetch it to learn where to get one. Both
well-known routes, like `/health`, run before/outside `h.authorize()`.

## What's out of scope

- No dynamic client registration (RFC 7591) — not requested, no client here needs it.
- No `bearer_methods_supported`/`resource_signing_alg_values_supported` etc. —
  RFC 9728 optional fields trvl doesn't have an opinion on; omitted rather
  than guessed.
