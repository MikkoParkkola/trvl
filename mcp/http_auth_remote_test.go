package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- GH-89.AUTH.4: remote bind requires explicit auth ---

func TestRequireRemoteAuth_GateMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		host           string
		authConfigured bool
		wantErr        bool
	}{
		{"loopback default empty no auth", "", false, false},
		{"loopback ipv4 no auth", "127.0.0.1", false, false},
		{"loopback ipv6 no auth", "::1", false, false},
		{"localhost name no auth", "localhost", false, false},
		{"remote all-interfaces no auth refused", "0.0.0.0", false, true},
		{"remote ipv6 unspecified no auth refused", "::", false, true},
		{"remote public ip no auth refused", "203.0.113.5", false, true},
		{"remote hostname no auth refused", "mcp.example.com", false, true},
		{"remote all-interfaces with auth allowed", "0.0.0.0", true, false},
		{"remote public ip with auth allowed", "203.0.113.5", true, false},
		{"remote hostname with auth allowed", "mcp.example.com", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireRemoteAuth(tt.host, tt.authConfigured)
			if (err != nil) != tt.wantErr {
				t.Fatalf("requireRemoteAuth(%q, %v) err=%v, wantErr=%v", tt.host, tt.authConfigured, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "refusing to start") {
				t.Fatalf("error message = %q, want refusal text", err.Error())
			}
		})
	}
}

func TestIsRemoteBindHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"localhost", false},
		{"LOCALHOST", false},
		{"0.0.0.0", true},
		{"::", true},
		{"203.0.113.5", true},
		{"example.org", true},
	}
	for _, tt := range tests {
		if got := isRemoteBindHost(tt.host); got != tt.want {
			t.Errorf("isRemoteBindHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

// RunHTTPWithOptions must refuse to start when a remote host has no auth.
// This exercises the gate without binding a socket because the gate runs
// before ListenAndServe.
func TestRunHTTPWithOptions_RemoteHostWithoutAuthRefused(t *testing.T) {
	// Ensure env-derived auth does not accidentally configure the server.
	t.Setenv("TRVL_MCP_TOKEN", "")
	t.Setenv("TRVL_MCP_READ_TOKEN", "")
	t.Setenv("TRVL_MCP_WRITE_TOKEN", "")
	t.Setenv("TRVL_MCP_OAUTH_INTROSPECTION_URL", "")

	err := RunHTTPWithOptions(HTTPServerOptions{Host: "0.0.0.0", Port: 0})
	if err == nil {
		t.Fatal("expected refusal error for remote host without auth, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("error = %q, want refusal", err.Error())
	}
}

// --- Audit counters surfaced on /health ---

func TestHealth_AuthAuditCounters(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:       0,
		ReadToken:  "read-token",
		WriteToken: "write-token",
	})

	// Baseline health: enforced, zero decisions.
	health := getHealth(t, hs)
	auth, ok := health["auth"].(map[string]any)
	if !ok {
		t.Fatalf("health missing auth object: %#v", health)
	}
	if auth["enforced"] != true {
		t.Fatalf("auth.enforced = %v, want true", auth["enforced"])
	}
	if auth["decisions_allowed"].(float64) != 0 || auth["decisions_denied"].(float64) != 0 {
		t.Fatalf("baseline counters non-zero: %#v", auth)
	}

	// One allowed call (read token, read-only tool).
	postHTTPToolCall(t, hs, "read-token", "get_preferences", map[string]any{})
	// One denied call (read token, write tool).
	postHTTPToolCall(t, hs, "read-token", "update_preferences", map[string]any{"display_currency": "EUR"})
	// One denied call (no/invalid token → 401).
	{
		body, _ := json.Marshal(Request{JSONRPC: "2.0", ID: float64(9), Method: "initialize"})
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
		hs.handleMCP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("missing-token status = %d, want 401", rr.Code)
		}
	}

	health = getHealth(t, hs)
	auth = health["auth"].(map[string]any)
	if got := auth["decisions_allowed"].(float64); got < 1 {
		t.Fatalf("decisions_allowed = %v, want >=1", got)
	}
	if got := auth["decisions_denied"].(float64); got < 2 {
		t.Fatalf("decisions_denied = %v, want >=2", got)
	}
}

// /health must never leak subjects, tokens, scopes, or denial reasons.
func TestHealth_DoesNotLeakSensitiveAuthDetail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:       0,
		ReadToken:  "super-secret-read-token",
		WriteToken: "super-secret-write-token",
	})
	postHTTPToolCall(t, hs, "super-secret-read-token", "update_preferences", map[string]any{"display_currency": "EUR"})

	rr := httptest.NewRecorder()
	hs.handleHealth(rr, httptest.NewRequest("GET", "/health", nil))
	raw := rr.Body.String()
	for _, leak := range []string{"super-secret-read-token", "super-secret-write-token", "trvl:write", "requires trvl"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("/health leaked sensitive value %q: %s", leak, raw)
		}
	}
}

func getHealth(t *testing.T, hs *HTTPServer) map[string]any {
	t.Helper()
	rr := httptest.NewRecorder()
	hs.handleHealth(rr, httptest.NewRequest("GET", "/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rr.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("health unmarshal: %v", err)
	}
	return out
}

// --- OAuth introspection path (deterministic via httptest fake IdP) ---

func TestOAuthIntrospection_ScopeEnforcement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Fake external IdP introspection endpoint. Token "rw" → read+write,
	// "ro" → read only, anything else → inactive.
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		tok := r.Form.Get("token")
		w.Header().Set("Content-Type", "application/json")
		switch tok {
		case "rw":
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "scope": "trvl:read trvl:write", "sub": "alice", "aud": "trvl-mcp"})
		case "ro":
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "scope": "trvl:read", "sub": "bob", "aud": "trvl-mcp"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
		}
	}))
	defer idp.Close()

	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:                  0,
		OAuthIntrospectionURL: idp.URL,
		OAuthAudience:         "trvl-mcp",
	})

	// Inactive token → 401 before JSON-RPC.
	{
		body, _ := json.Marshal(Request{JSONRPC: "2.0", ID: float64(1), Method: "initialize"})
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer nonsense")
		hs.handleMCP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("inactive token status = %d, want 401", rr.Code)
		}
	}

	// Read-only OAuth token denied on a write tool.
	resp, status := postHTTPToolCall(t, hs, "ro", "update_preferences", map[string]any{"display_currency": "EUR"})
	if status != http.StatusOK {
		t.Fatalf("ro write-tool status = %d, want 200 JSON-RPC error", status)
	}
	if resp.Error == nil || resp.Error.Code != -32001 {
		t.Fatalf("ro write-tool expected -32001 scope error, got %#v", resp.Error)
	}

	// Read/write OAuth token allowed on the same write tool.
	resp, status = postHTTPToolCall(t, hs, "rw", "update_preferences", map[string]any{"display_currency": "EUR"})
	if status != http.StatusOK {
		t.Fatalf("rw write-tool status = %d, want 200", status)
	}
	if resp.Error != nil {
		t.Fatalf("rw write-tool unexpected error: %#v", resp.Error)
	}
}

func TestOAuthIntrospection_AudienceMismatchRejected(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "scope": "trvl:read", "sub": "carol", "aud": "some-other-rs"})
	}))
	defer idp.Close()

	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:                  0,
		OAuthIntrospectionURL: idp.URL,
		OAuthAudience:         "trvl-mcp",
	})

	body, _ := json.Marshal(Request{JSONRPC: "2.0", ID: float64(1), Method: "initialize"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer mismatched")
	hs.handleMCP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("audience mismatch status = %d, want 401", rr.Code)
	}
}

// --- Tool-to-scope matrix (read vs write) ---

// GH-89.AUTH.3: state-mutating tools must require trvl:write; read-only tools
// must not. This table is the authoritative read/write scope matrix and pins
// the annotation-driven classification used by authorizeJSONRPC.
func TestToolScopeMatrix_ReadVsWrite(t *testing.T) {
	t.Setenv(smartToolModeEnv, "legacy")
	s := NewServer()

	tests := []struct {
		tool          string
		args          map[string]any
		requiresWrite bool
	}{
		// Read-only tools must not require write scope.
		{"get_preferences", map[string]any{}, false},
		{"search_flights", map[string]any{"origin": "HEL", "destination": "LHR"}, false},
		{"get_weather", map[string]any{"location": "Paris"}, false},
		{"get_baggage_rules", map[string]any{"airline": "AY"}, false},
		// State-mutating tools (write to ~/.trvl) must require write scope.
		{"update_preferences", map[string]any{"display_currency": "EUR"}, true},
		{"watch_price", map[string]any{"origin": "HEL", "destination": "LHR"}, true},
		{"trip_workspace", map[string]any{"action": "add", "name": "Summer"}, true},
		// trip_workspace read action must not require write scope.
		{"trip_workspace", map[string]any{"action": "get"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.tool+"_"+writeLabel(tt.requiresWrite), func(t *testing.T) {
			if _, ok := s.toolDefs[tt.tool]; !ok {
				t.Skipf("tool %q not registered in this build", tt.tool)
			}
			_, gotWrite := s.toolRequiresWrite(tt.tool, tt.args)
			if gotWrite != tt.requiresWrite {
				t.Fatalf("toolRequiresWrite(%q, %v) = %v, want %v", tt.tool, tt.args, gotWrite, tt.requiresWrite)
			}
		})
	}
}

func writeLabel(w bool) string {
	if w {
		return "write"
	}
	return "read"
}

// TestSafeTokenCompare_ConstantTime verifies the constant-time token comparison
// accepts only exact matches (timing-attack hardening, security review fix 1).
func TestSafeTokenCompare_ConstantTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"secret-token-abc", "secret-token-abc", true},
		{"secret-token-abc", "secret-token-abd", false},
		{"secret-token-abc", "secret-token-ab", false}, // length mismatch
		{"", "", true},
		{"x", "", false},
	}
	for _, c := range cases {
		if got := safeTokenCompare(c.a, c.b); got != c.want {
			t.Errorf("safeTokenCompare(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestNewHTTPAuth_OAuthWithoutAudienceStillConstructs verifies the confused-deputy
// warning path (security review fix 2) does not break construction or auth.
func TestNewHTTPAuth_OAuthWithoutAudienceStillConstructs(t *testing.T) {
	t.Parallel()
	a := NewHTTPAuth(HTTPServerOptions{OAuthIntrospectionURL: "https://idp.example/introspect"})
	if a == nil || !a.Configured() {
		t.Fatal("expected configured auth with OAuth introspection URL")
	}
}
