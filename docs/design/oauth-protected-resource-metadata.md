# OAuth Protected Resource Metadata (RFC 9728) for HTTP MCP mode

Status: implemented, with one known residual (see (d) 403 note) — an
authenticated token holding only unrecognized scopes returns 401 instead of
403; tracked as a follow-up, not fixed here.
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
`authorization_servers[0]` value verbatim, validated as an absolute
`https://` URL (RFC 8414 §2 — the issuer identifier's format requirement)
before the server starts.

**When OAuth is configured but the issuer is not set: refuse to start.**
Same failure shape as `requireHTTPAuth`/`requireRemoteAuth` (GH-89.AUTH.4) —
trvl already treats "started with an incomplete security config" as fatal,
not a silent degrade. `authorization_servers` is a MUST field per the MCP
spec; serving a PRM document without it (or not serving PRM at all while
claiming OAuth support) is worse than refusing to start, because it looks
compliant to a client's discovery probe while failing to name an issuer. This
mirrors the codebase's existing convention: missing *required* config fails
closed at startup (`requireHTTPAuth`).

**`--oauth-audience` needs to stop being silently optional.**
MCP 2025-11-25 requires resource servers to bind and validate token
audience: a token issued for another service must not be accepted. Today
(`mcp/http_auth.go:224` `audienceMatches`, called from `authenticateOAuth` at
`:168`) audience checking only runs when `--oauth-audience` is set — with no
audience configured, `authenticateOAuth` accepts any active token from the
introspection endpoint regardless of its `aud` claim. That is fail-*open*,
not fail-closed; `NewHTTPAuth` (`:57-61`) only logs a startup warning for the
gap today, it does not refuse to start. That warn-only posture predates this
spec requirement and no longer satisfies it.

**Decision: derive `--oauth-audience` from the PRM `resource` when unset, and
refuse to start when both are set and differ.** Requiring the flag separately
would leave a correct config and a broken one looking identical. A client that
follows RFC 8707 does exactly what the PRM document tells it to: it sends
`resource=<public-url>/mcp` to the authorization server and receives a token
whose `aud` is that URI. Today `docs/REMOTE-MCP-OAUTH.md:87-92` documents the
flag as `--oauth-audience "trvl-mcp"`, an opaque string, and that is the value
`audienceMatches` compares against — so the token is rejected and the user
sees a 401 immediately after a successful consent flow, which is the worst
place to put a failure. Defaulting the audience to the published `resource`
collapses the two values into one; an explicit `--oauth-audience` that
disagrees with `resource` is a misconfiguration, not a preference, so it fails
at startup rather than at the first request.

This replaces today's warn-only posture. `NewHTTPAuth` (`:57-61`) currently
logs a startup warning when OAuth is configured with no audience and does not
refuse to start; with the audience always derived, that gap closes and the
warning goes away. Audience *matching* itself (`audienceMatches`) is already
correctly enforced whenever a value is present.

`docs/REMOTE-MCP-OAUTH.md` must be updated in the same change: its worked
example currently teaches the value that breaks.

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
  fail-closed treatment as `--oauth-issuer` above (`--oauth-audience`
  itself now defaults rather than requiring an explicit value, per (a)).
- **Exception:** `http://localhost` and `http://127.0.0.1` public URLs are
  accepted without erroring, for local development only. This is a known,
  documented non-compliant convenience (an RFC 9728 client is free to reject
  an http resource identifier) — not a general http allowance.

`resource` in the served document is `<public-url>/mcp` — the MCP endpoint
itself, not the bare host — because that's the identifier a client actually
holds a token for.

**Normalization.** `resource` must equal the URL clients actually call,
byte for byte, because it is compared as a string by both the client's
resource indicator and this server's audience check. So: strip any trailing
slash from `--public-url` before appending `/mcp`; preserve IPv6 literal
brackets (`https://[::1]:8080`); and do not treat `localhost` and `127.0.0.1`
as interchangeable — RFC 9728 §3.3 does not, and a token minted for one will
not match the other. A `--public-url` with an empty hostname (e.g. a
port-only authority like `https://:443/x`) or carrying a query or fragment
is refused at startup (an empty hostname isn't a valid identifier at all;
RFC 9728 §1.2 prohibits both a query and a fragment in a resource
identifier); so is one whose path contains `{` or `}` — those are reserved
wildcard syntax in the `http.ServeMux` route patterns built from it, and an
unvalidated one would register as a live wildcard route instead of a
literal path match, silently serving the PRM document under
attacker-controlled path segments rather than failing closed. A path whose
percent-encoding is non-default (`u.RawPath != ""`) is also refused — this
is a deliberately conservative proxy for encodings that would change segment
structure (`%2F` for a literal `/`) and make the well-known route registered
from the decoded path disagree with the resource identifier published from
the encoded string; it also rejects some benign encodings that don't change
segment structure (e.g. `%41` for `A`), an accepted false positive.

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

**The document.** `GET` only — any other method gets `405`. Served as
`Content-Type: application/json`. Exactly two fields, both mandatory here per
(a) and RFC 9728 §2; `authorization_servers` is a JSON array of strings, not a
bare string:

```json
{
  "resource": "https://travel.example.org/mcp",
  "authorization_servers": ["https://tenant.auth0.com/"]
}
```

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

**Insufficient scope on an authenticated request.** A request that
authenticates but whose token lacks the scope a `tools/call` needs (e.g. a
`trvl:read`-only token calling a write tool) is a distinct case from the
missing/invalid-token 401 above: RFC 6750 §3.1 requires this to be `403`, not
`200` with a JSON-RPC error body — a `200` tells an HTTP-layer client the call
succeeded. The response is `403` with a JSON-RPC error body (so an MCP client
still gets the same machine-readable detail) and:
`WWW-Authenticate: Bearer resource_metadata="<path-suffixed well-known URL>", error="insufficient_scope", scope="<trvl:read|trvl:write>"`
(the `resource_metadata` parameter is omitted in static-token-only mode, same
as the 401 case — there is no authorization server to point a client at).

**Known residual.** A token that is active and audience-valid but carries
only unrecognized scopes (neither `trvl:read` nor `trvl:write`) still falls
into the pre-existing `mcp/http_auth.go` scope check and returns `401`, not
the `403` above — that check predates this change and is out of scope for
it. Tracked as a follow-up.

## (e) Discovery endpoints are unauthenticated

A discovery document behind auth can't be discovered — a client with no
token yet is precisely who needs to fetch it to learn where to get one. Both
well-known routes, like `/health`, run before/outside `h.authorize()`.

## Migration: this is a breaking startup change

Anyone already running with `--oauth-introspection-url` will fail to start
after this lands until they also pass `--oauth-issuer` and `--public-url`.
That is deliberate — both are required to serve a compliant PRM document, and
starting without them means advertising OAuth support the server cannot back
up. Call it out in the release notes and in `docs/REMOTE-MCP-OAUTH.md`.

## What's out of scope

- No dynamic client registration (RFC 7591) — not requested, no client here needs it.
- No `bearer_methods_supported`/`resource_signing_alg_values_supported` etc. —
  RFC 9728 optional fields trvl doesn't have an opinion on; omitted rather
  than guessed.
