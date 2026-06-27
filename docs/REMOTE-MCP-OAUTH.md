# Remote MCP mode with OAuth 2.1 (resource-server)

trvl ships as a local stdio MCP server by default, which keeps your travel
profile, price watches, and trip state on your own machine. This document
explains the optional **remote HTTP mode** and how to put it behind an OAuth
2.1 gateway so a hosted client can reach it safely.

trvl is an OAuth **resource server** only. It validates access tokens issued by
an external identity provider (IdP)/gateway. trvl does not run a login page, a
consent screen, user management, nor a token endpoint. Pair it with an IdP you
already trust (Auth0, Okta, Keycloak, Google, an API gateway, a reverse proxy
that injects tokens).

## Localhost-safe defaults

Running `trvl mcp --http` binds to `127.0.0.1:8080` and requires a bearer
token. If you do not supply one, trvl generates a random token at startup and
redacts it from the startup log. Use `--token` or `TRVL_MCP_TOKEN` when a
client needs a reusable token. Nothing is reachable from another machine
because the listener is loopback-only.

```bash
trvl mcp --http                   # 127.0.0.1:8080, random token generated and redacted
trvl mcp --http --token hunter2   # 127.0.0.1:8080, fixed token
```

## Remote exposure requires explicit auth (GH-89.AUTH.4)

Binding to a non-loopback host (for example `--host 0.0.0.0`, a public IP, a
DNS name) **without** any token/OAuth introspection configured is refused. trvl
will not start:

```
$ trvl mcp --http --host 0.0.0.0
refusing to start: --host "0.0.0.0" exposes the MCP server beyond localhost but
no authentication is configured; set --token/--read-token/--write-token (or
TRVL_MCP_TOKEN), or --oauth-introspection-url, before binding to a non-loopback host
```

This is deliberate. A remote listener with an implicit generated token is weak,
so trvl forces you to make the auth decision explicitly before it will accept
connections from the network.

## Scope model: read vs write

trvl enforces two scopes:

| Scope        | Grants                                                              |
|--------------|--------------------------------------------------------------------|
| `trvl:read`  | Read-only tools: search flights/hotels/ground, weather, baggage, lounges, view preferences and trips. |
| `trvl:write` | Everything `trvl:read` grants, plus state-mutating tools that write `~/.trvl`: `update_preferences`, `watch_price`, trip-workspace mutations. |

Every MCP tool is annotated read-only/read-write. A request that carries only
`trvl:read` and calls a write tool is denied with a JSON-RPC error (`-32001`,
"permission denied: tool X requires trvl:write scope") before the tool runs. A
request with no valid token is rejected with HTTP `401` before any JSON-RPC
handling.

When trvl introspects an OAuth token, it accepts these scope spellings and maps
them onto its two scopes: `trvl:read`/`trvl:write`, `read`/`write`,
`mcp:read`/`mcp:write`, `travel:read`/`travel:write`.

## Option A: static scoped bearer tokens

The simplest pluggable verifier. Issue two long random secrets and hand the
read one to read-only clients, the write one to trusted clients.

```bash
trvl mcp --http \
  --host 0.0.0.0 --port 8080 \
  --read-token  "$(openssl rand -base64 32)" \
  --write-token "$(openssl rand -base64 32)"
```

Equivalent environment variables: `TRVL_MCP_READ_TOKEN`, `TRVL_MCP_WRITE_TOKEN`,
`TRVL_MCP_TOKEN` (full access). Use this for a small deployment, also while
wiring up a gateway.

## Option B: OAuth 2.1 token introspection

Point trvl at your IdP's RFC 7662 introspection endpoint. trvl validates each
incoming access token against it, checks the `active`, `exp`, plus (optionally)
`aud` claims, and reads scopes from the `scope`/`scp` claim.

```bash
trvl mcp --http \
  --host 0.0.0.0 --port 8080 \
  --oauth-introspection-url "https://idp.example.org/oauth/introspect" \
  --oauth-client-id "trvl-mcp" \
  --oauth-client-secret "$INTROSPECTION_SECRET" \
  --oauth-audience "trvl-mcp"
```

Environment variables: `TRVL_MCP_OAUTH_INTROSPECTION_URL`,
`TRVL_MCP_OAUTH_CLIENT_ID`, `TRVL_MCP_OAUTH_CLIENT_SECRET`,
`TRVL_MCP_OAUTH_AUDIENCE`.

**Required for production.** Set `--oauth-audience` to the resource identifier
you registered for trvl so a token minted for a different service at the same
IdP is rejected (confused-deputy protection, RFC 7662 §2.2). Without it, trvl
accepts any active token from the introspection endpoint and logs a startup
warning.

### Client-side flow: Authorization Code + PKCE

trvl never sees the user's credentials. The client and IdP run the standard
OAuth 2.1 Authorization-Code flow with PKCE, then the client presents the
resulting access token to trvl:

1. The MCP client generates a PKCE `code_verifier` plus its `code_challenge`
   (`S256`).
2. The client opens the IdP authorize URL with `response_type=code`,
   `code_challenge`, `code_challenge_method=S256`, the redirect URI, the scopes
   it needs (`trvl:read`, `trvl:write`).
3. The user authenticates and consents at the IdP. The IdP redirects back with
   an authorization `code`.
4. The client exchanges the `code` plus the `code_verifier` at the IdP token
   endpoint for an access token (optionally a refresh token).
5. The client calls trvl: `POST /mcp` with `Authorization: Bearer <access_token>`.
6. trvl introspects the token, enforces scope, then serves the MCP request.

trvl owns only step 6. Steps 1 through 5 belong to the IdP plus the client.

## Reverse-proxy / gateway pattern

If you front trvl with an API gateway that already authenticates users, let the
gateway terminate OAuth and forward an `Authorization: Bearer` header that trvl
can introspect (Option B). Alternatively, have the gateway inject one of the
static scoped tokens (Option A) on the internal hop. Keep trvl bound to loopback
or a private interface that only the gateway can reach.

## Auditing auth decisions

trvl logs every auth decision (allow/deny, tool, scope, plus a server-side-only
detail) as a structured log line, and exposes aggregate counts on the
unauthenticated `/health` endpoint:

```json
{
  "status": "ok",
  "server": "trvl",
  "version": "...",
  "tools": 66,
  "auth": {
    "enforced": true,
    "decisions_allowed": 128,
    "decisions_denied": 3
  }
}
```

`/health` exposes counts only. It never returns subjects, tokens, scopes, nor
denial reasons, because it is unauthenticated. Use the structured server logs
for per-decision detail.

## Security checklist

- Keep the default loopback bind unless you genuinely need remote access.
- Never expose a remote host without auth; trvl refuses this by design.
- Use OAuth introspection with a pinned `--oauth-audience` (required for production) over static tokens
  for multi-user deployments.
- Hand out `trvl:read` by default; grant `trvl:write` only to clients that must
  mutate `~/.trvl` state.
- Terminate TLS at a reverse proxy/load balancer in front of trvl.
