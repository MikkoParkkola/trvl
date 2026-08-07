package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"
)

const (
	scopeRead  = "trvl:read"
	scopeWrite = "trvl:write"
)

// RequestAccess is the authenticated scope set for one HTTP MCP request.
type RequestAccess struct {
	Subject string
	Source  string
	Scopes  map[string]bool
}

// HTTPAuth validates local bearer tokens or OAuth access tokens.
type HTTPAuth struct {
	token                 string
	readToken             string
	writeToken            string
	oauthIntrospectionURL string
	oauthClientID         string
	oauthClientSecret     string
	oauthAudience         string
	client                *http.Client
}

// NewHTTPAuth builds an authenticator from HTTP server options.
func NewHTTPAuth(opts HTTPServerOptions) *HTTPAuth {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	a := &HTTPAuth{
		token:                 strings.TrimSpace(opts.Token),
		readToken:             strings.TrimSpace(opts.ReadToken),
		writeToken:            strings.TrimSpace(opts.WriteToken),
		oauthIntrospectionURL: strings.TrimSpace(opts.OAuthIntrospectionURL),
		oauthClientID:         strings.TrimSpace(opts.OAuthClientID),
		oauthClientSecret:     strings.TrimSpace(opts.OAuthClientSecret),
		oauthAudience:         strings.TrimSpace(opts.OAuthAudience),
		client:                client,
	}
	if a.oauthIntrospectionURL != "" && a.oauthAudience == "" {
		// Confused-deputy guard (RFC 7662 §2.2): without a pinned audience, any
		// valid token at this IdP — including tokens minted for other services —
		// is accepted. Warn loudly; do not hard-fail (single-tenant dev setups).
		slog.Warn("mcp oauth: no --oauth-audience configured; tokens issued for any client at this introspection endpoint will be accepted (set --oauth-audience for production)")
	}
	return a
}

// Configured reports whether HTTP auth should be enforced.
func (a *HTTPAuth) Configured() bool {
	if a == nil {
		return false
	}
	return a.token != "" || a.readToken != "" || a.writeToken != "" || a.oauthIntrospectionURL != ""
}

// Authenticate validates a bearer token and returns its MCP scopes.
func (a *HTTPAuth) Authenticate(ctx context.Context, token string) (RequestAccess, bool) {
	if a == nil || !a.Configured() {
		return FullAccess("anonymous", "disabled"), true
	}
	if token == "" {
		return RequestAccess{}, false
	}
	if a.token != "" && safeTokenCompare(token, a.token) {
		return FullAccess("local-token", "static"), true
	}
	if a.writeToken != "" && safeTokenCompare(token, a.writeToken) {
		return FullAccess("write-token", "static"), true
	}
	if a.readToken != "" && safeTokenCompare(token, a.readToken) {
		return ReadAccess("read-token", "static"), true
	}
	if a.oauthIntrospectionURL != "" {
		return a.authenticateOAuth(ctx, token)
	}
	return RequestAccess{}, false
}

// safeTokenCompare reports whether two tokens are equal in constant time,
// preventing a byte-by-byte timing oracle on static bearer tokens.
func safeTokenCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// FullAccess returns an access context with read and write scopes.
func FullAccess(subject, source string) RequestAccess {
	return RequestAccess{
		Subject: subject,
		Source:  source,
		Scopes:  map[string]bool{scopeRead: true, scopeWrite: true},
	}
}

// ReadAccess returns an access context with read-only scope.
func ReadAccess(subject, source string) RequestAccess {
	return RequestAccess{
		Subject: subject,
		Source:  source,
		Scopes:  map[string]bool{scopeRead: true},
	}
}

// CanRead reports whether the request can read MCP state or call read-only tools.
func (a RequestAccess) CanRead() bool {
	return a.Scopes[scopeRead] || a.CanWrite()
}

// CanWrite reports whether the request can mutate local/user state.
func (a RequestAccess) CanWrite() bool {
	return a.Scopes[scopeWrite]
}

func (a *HTTPAuth) authenticateOAuth(ctx context.Context, token string) (RequestAccess, bool) {
	form := url.Values{}
	form.Set("token", token)
	form.Set("token_type_hint", "access_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.oauthIntrospectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		slog.Warn("mcp oauth introspection request build failed", "error", logredact.Err(err))
		return RequestAccess{}, false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if a.oauthClientID != "" || a.oauthClientSecret != "" {
		req.SetBasicAuth(a.oauthClientID, a.oauthClientSecret)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		slog.Warn("mcp oauth introspection failed", "error", logredact.Err(err))
		return RequestAccess{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("mcp oauth introspection rejected", "status", resp.StatusCode)
		return RequestAccess{}, false
	}

	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&claims); err != nil {
		slog.Warn("mcp oauth introspection decode failed", "error", logredact.Err(err))
		return RequestAccess{}, false
	}
	if active, _ := claims["active"].(bool); !active {
		return RequestAccess{}, false
	}
	if exp, ok := numericClaim(claims["exp"]); ok && exp > 0 && time.Now().Unix() >= int64(exp) {
		return RequestAccess{}, false
	}
	if a.oauthAudience != "" && !audienceMatches(claims["aud"], a.oauthAudience) {
		slog.Warn("mcp oauth token audience mismatch")
		return RequestAccess{}, false
	}

	scopes := normalizedScopes(claims["scope"], claims["scp"])
	if !scopes[scopeRead] && !scopes[scopeWrite] {
		return RequestAccess{}, false
	}
	subject := claimString(claims, "sub")
	if subject == "" {
		subject = claimString(claims, "username")
	}
	if subject == "" {
		subject = claimString(claims, "client_id")
	}
	return RequestAccess{Subject: subject, Source: "oauth", Scopes: scopes}, true
}

func normalizedScopes(values ...any) map[string]bool {
	scopes := map[string]bool{}
	for _, raw := range values {
		for _, s := range scopeStrings(raw) {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "read", "mcp:read", "travel:read", scopeRead:
				scopes[scopeRead] = true
			case "write", "mcp:write", "travel:write", scopeWrite:
				scopes[scopeWrite] = true
			}
		}
	}
	if scopes[scopeWrite] {
		scopes[scopeRead] = true
	}
	return scopes
}

func scopeStrings(raw any) []string {
	switch v := raw.(type) {
	case string:
		return strings.Fields(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

func audienceMatches(raw any, want string) bool {
	switch aud := raw.(type) {
	case string:
		return aud == want
	case []any:
		for _, item := range aud {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range aud {
			if s == want {
				return true
			}
		}
	}
	return false
}

func numericClaim(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func claimString(claims map[string]any, key string) string {
	if s, ok := claims[key].(string); ok {
		return s
	}
	return ""
}

// slogHTTPAuthDecision emits a structured audit record of an auth decision.
// detail carries the denial reason for "deny" or the subject for "allow"; it
// is logged server-side only and never surfaced on the unauthenticated
// /health endpoint. Denials log at Warn (security-relevant), allows at Info.
func slogHTTPAuthDecision(decision, method, scope, detail string) {
	attrs := []any{
		"decision", decision,
		"tool", method,
		"scope", scope,
		"detail", detail,
	}
	if decision == "deny" {
		slog.Warn("mcp http auth decision", attrs...)
		return
	}
	slog.Info("mcp http auth decision", attrs...)
}

func (s *Server) toolWriteRequirement(req *Request) (string, bool, bool) {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return "", false, false
	}
	target, requiresWrite := s.toolRequiresWrite(params.Name, params.Arguments)
	return target, requiresWrite, true
}

func (s *Server) toolRequiresWrite(name string, args map[string]any) (string, bool) {
	if name == "travel" {
		query := strings.TrimSpace(argString(args, "query"))
		intent := strings.TrimSpace(argString(args, "intent"))
		params := smartToolParams(args)
		action := strings.TrimSpace(argString(args, "action"))
		if action == "" {
			action = strings.TrimSpace(argString(params, "action"))
		}
		if target, _ := s.resolveTravelTarget(intent, action, query); target != "" && target != "travel" {
			return s.toolRequiresWrite(target, params)
		}
	}

	if name == "trip_workspace" && !tripWorkspaceActionRequiresWrite(args) {
		return name, false
	}

	tool, ok := s.toolDefs[name]
	if !ok {
		return name, false
	}
	if tool.Annotations == nil {
		return name, true
	}
	return name, !tool.Annotations.ReadOnlyHint
}

func tripWorkspaceActionRequiresWrite(args map[string]any) bool {
	action := normalizeSmartToken(argString(args, "action"))
	switch action {
	case "get", "show", "export", "export_json", "json_export", "export_markdown", "markdown_export", "md", "fare_intelligence", "fare", "buy_wait", "booking_ready", "readiness":
		return false
	default:
		return true
	}
}
