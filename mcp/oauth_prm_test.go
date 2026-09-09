package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 401 challenge scope (design doc (d)): default trvl:read, escalate to
// trvl:write only for a tools/call naming a write tool; anything else
// (no body, unparseable body, read-only tool) falls back to trvl:read.
func TestChallengeScope_ReadVsWriteViaToolRequiresWrite(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServer(0)

	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"no body", nil, scopeRead},
		{"unparseable body", []byte("not json"), scopeRead},
		{"tools/call read-only tool", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_preferences","arguments":{}}}`), scopeRead},
		{"tools/call write tool", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_preferences","arguments":{"display_currency":"EUR"}}}`), scopeWrite},
		{"non-tools/call method", []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`), scopeRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hs.challengeScope(tt.body); got != tt.want {
				t.Errorf("challengeScope(%s) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// Startup refusals (design doc (a)/(b)): OAuth configured but --oauth-issuer
// or a valid --public-url missing is fatal, same shape as requireHTTPAuth.
// http://localhost and http://127.0.0.1 are an explicit dev-only exception.
func TestRequireOAuthPRMConfig_StartupRefusals(t *testing.T) {
	t.Parallel()
	introspection := "https://idp.example.org/oauth/introspect"
	issuer := "https://tenant.auth0.com/"
	tests := []struct {
		name      string
		issuer    string
		publicURL string
		wantErr   bool
	}{
		{"missing issuer refused", "", "https://travel.example.org", true},
		{"missing public-url refused", issuer, "", true},
		{"http non-loopback public-url refused", issuer, "http://travel.example.org", true},
		{"https public-url accepted", issuer, "https://travel.example.org", false},
		{"http localhost accepted (dev exception)", issuer, "http://localhost", false},
		{"http 127.0.0.1 accepted (dev exception)", issuer, "http://127.0.0.1:8080", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireOAuthPRMConfig(HTTPServerOptions{
				OAuthIntrospectionURL: introspection,
				OAuthIssuer:           tt.issuer,
				PublicURL:             tt.publicURL,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("requireOAuthPRMConfig(issuer=%q, publicURL=%q) err=%v, wantErr=%v", tt.issuer, tt.publicURL, err, tt.wantErr)
			}
		})
	}
}

// No OAuth configured: PRM validation is a no-op regardless of issuer/public-url.
func TestRequireOAuthPRMConfig_NoOAuthConfiguredIsNoop(t *testing.T) {
	t.Parallel()
	if err := requireOAuthPRMConfig(HTTPServerOptions{}); err != nil {
		t.Fatalf("unexpected error with no OAuth configured: %v", err)
	}
}

// Static-token-only mode (design doc (c)): no authorization server to name,
// so both well-known routes stay unregistered (404) and the 401 carries no
// resource_metadata challenge.
func TestStaticTokenOnlyMode_WellKnownRoutesUnregistered(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServerWithOptions(HTTPServerOptions{Port: 0, Token: "secret-token"})
	mux := hs.newMux()

	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 in static-token-only mode", path, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	hs.handleMCP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /mcp = %d, want 401", rr.Code)
	}
	if wa := rr.Header().Get("WWW-Authenticate"); strings.Contains(wa, "resource_metadata") {
		t.Errorf("WWW-Authenticate = %q, want no resource_metadata in static-token-only mode", wa)
	}
}

// RFC 9728 §3: the well-known path segment goes immediately after the
// authority; any path component --public-url carries (a reverse-proxy mount
// prefix) comes after the well-known segment, not before it.
func TestWellKnownPaths_PathMountedPublicURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		publicURL    string
		wantRoot     string
		wantSuffixed string
	}{
		{
			name:         "no path component",
			publicURL:    "https://travel.example.org",
			wantRoot:     "/.well-known/oauth-protected-resource",
			wantSuffixed: "/.well-known/oauth-protected-resource/mcp",
		},
		{
			name:         "path-mounted public url",
			publicURL:    "https://host/base",
			wantRoot:     "/.well-known/oauth-protected-resource",
			wantSuffixed: "/.well-known/oauth-protected-resource/base/mcp",
		},
		{
			name:         "trailing slash stripped before mount",
			publicURL:    "https://host/base/",
			wantRoot:     "/.well-known/oauth-protected-resource",
			wantSuffixed: "/.well-known/oauth-protected-resource/base/mcp",
		},
		{
			name:         "ipv6 literal, no path",
			publicURL:    "https://[::1]:8080",
			wantRoot:     "/.well-known/oauth-protected-resource",
			wantSuffixed: "/.well-known/oauth-protected-resource/mcp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, suffixed, err := wellKnownPaths(tt.publicURL)
			if err != nil {
				t.Fatalf("wellKnownPaths(%q) error = %v", tt.publicURL, err)
			}
			if root != tt.wantRoot {
				t.Errorf("root = %q, want %q", root, tt.wantRoot)
			}
			if suffixed != tt.wantSuffixed {
				t.Errorf("suffixed = %q, want %q", suffixed, tt.wantSuffixed)
			}
		})
	}
}

// Audience derivation (design doc (a)): --oauth-audience defaults to the
// published resource (<public-url>/mcp) when unset; an explicit value that
// agrees is accepted; one that disagrees is refused at startup.
func TestRequireOAuthPRMConfig_AudienceDerivationMatrix(t *testing.T) {
	t.Parallel()
	base := HTTPServerOptions{
		OAuthIntrospectionURL: "https://idp.example.org/oauth/introspect",
		OAuthIssuer:           "https://tenant.auth0.com/",
		PublicURL:             "https://travel.example.org",
	}

	t.Run("audience unset derives from resource, no error", func(t *testing.T) {
		opts := base
		if err := requireOAuthPRMConfig(opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("explicit audience equal to derived resource accepted", func(t *testing.T) {
		opts := base
		opts.OAuthAudience = "https://travel.example.org/mcp"
		if err := requireOAuthPRMConfig(opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("explicit audience differing from derived resource refused", func(t *testing.T) {
		opts := base
		opts.OAuthAudience = "trvl-mcp"
		err := requireOAuthPRMConfig(opts)
		if err == nil {
			t.Fatal("expected refusal for mismatched --oauth-audience")
		}
	})
}
