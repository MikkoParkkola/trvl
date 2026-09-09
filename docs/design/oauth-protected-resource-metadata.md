# OAuth Protected Resource Metadata (RFC 9728) for HTTP MCP mode

Status: **implemented**
Date: 2026-09-09

## Why

MCP spec rev 2025-11-25 requires an HTTP MCP server to implement OAuth 2.0
Protected Resource Metadata (RFC 9728) so clients can discover which
authorization server(s) protect it. trvl's HTTP transport (`mcp/http.go`)
serves only `/mcp`, `/health`, `/dashboard` today and has no RFC 9728 support
at all. This doc settles the open questions before writing code.

## (a) Where does `authorization_servers` come from?

RFC 9728 requires the PRM document to name at least one authorization-server
issuer URL. trvl's existing `--oauth-introspection-url` is a token
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
closed at startup (`requireHTTPAuth`); a missing *recommended* field
(`--oauth-audience`) only warns, because that one is a defense-in-depth
extra, not something the spec's response format requires you to emit.

## (b) Where does `resource` come from?

RFC 9728 §2 requires `resource`: the canonical URI clients use to reach this
MCP server. trvl binds `--host`/`--port`, but that's the bind address, not
necessarily the address a client sees (reverse proxy, container port
mapping, etc).

**Decision: add `--public-url` / `TRVL_MCP_PUBLIC_URL`, defaulting to
`http://<host>:<port>`.** The default is honest for the common local/direct
case; anyone fronting trvl with a proxy sets it explicitly, same posture as
every other "trvl behind a proxy" concern in `docs/REMOTE-MCP-OAUTH.md`.

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

## (d) Which routes, and the 401 header

The spec requires implementing *one* of: the `WWW-Authenticate` header
mechanism, or a well-known URI (either path-suffixed or root form).

**Decision: do all three; it's one extra route and costs nothing.**

- `GET /.well-known/oauth-protected-resource/mcp` (path-suffixed to the MCP
  endpoint — the form MCP clients try first for a path-mounted server)
- `GET /.well-known/oauth-protected-resource` (root form, for clients that
  don't know to try the path-suffixed form first)
- Every `401` from `POST /mcp` gets
  `WWW-Authenticate: Bearer resource_metadata="<public-url>/.well-known/oauth-protected-resource/mcp", scope="trvl:read trvl:write"`
  when OAuth is configured (omitted in static-token-only mode, per (c)).

Both well-known routes serve the identical document.

## (e) Discovery endpoints are unauthenticated

A discovery document behind auth can't be discovered — a client with no
token yet is precisely who needs to fetch it to learn where to get one. Both
well-known routes, like `/health`, run before/outside `h.authorize()`.

## What's out of scope

- No dynamic client registration (RFC 7591) — not requested, no client here needs it.
- No `bearer_methods_supported`/`resource_signing_alg_values_supported` etc. —
  RFC 9728 optional fields trvl doesn't have an opinion on; omitted rather
  than guessed.
