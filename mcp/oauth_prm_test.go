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

// OAuth configured (design doc (d)): both well-known routes serve the same
// two-field document, GET only, and the 401 challenge carries the
// path-suffixed well-known URL plus the picked scope.
func TestOAuthConfigured_PRMDocumentAndChallenge(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:                  0,
		OAuthIntrospectionURL: "https://idp.example.org/oauth/introspect",
		OAuthIssuer:           "https://tenant.auth0.com/",
		PublicURL:             "https://travel.example.org",
	})
	mux := hs.newMux()

	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		body := rr.Body.String()
		if !strings.Contains(body, `"resource":"https://travel.example.org/mcp"`) {
			t.Errorf("body = %s, want resource field", body)
		}
		if !strings.Contains(body, `"authorization_servers":["https://tenant.auth0.com/"]`) {
			t.Errorf("body = %s, want authorization_servers array", body)
		}

		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 405", path, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	hs.handleMCP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST /mcp = %d, want 401", rr.Code)
	}
	want := `Bearer resource_metadata="https://travel.example.org/.well-known/oauth-protected-resource/mcp", scope="trvl:read"`
	if got := rr.Header().Get("WWW-Authenticate"); got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
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

// Insufficient scope on an authenticated request (design doc (d)): a
// trvl:read-only token calling a write tool gets 403, not 200, with a
// WWW-Authenticate insufficient_scope challenge (RFC 6750 §3.1).
func TestHandleMCP_InsufficientScopeReturns403(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServerWithOptions(HTTPServerOptions{Port: 0, ReadToken: "read-only-token"})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_preferences","arguments":{"display_currency":"EUR"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer read-only-token")
	rr := httptest.NewRecorder()
	hs.handleMCP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("insufficient-scope tools/call = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	want := `Bearer error="insufficient_scope", scope="trvl:write"`
	if got := rr.Header().Get("WWW-Authenticate"); got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
	if !strings.Contains(rr.Body.String(), "requires trvl:write scope") {
		t.Errorf("body = %s, want JSON-RPC error detail", rr.Body.String())
	}
}

// Same insufficient-scope case, but with OAuth/PRM configured: the challenge
// carries resource_metadata too, matching the 401 challenge shape.
func TestHandleMCP_InsufficientScopeChallengeCarriesResourceMetadataWhenPRMConfigured(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:                  0,
		OAuthIntrospectionURL: "https://idp.example.org/oauth/introspect",
		OAuthIssuer:           "https://tenant.auth0.com/",
		PublicURL:             "https://travel.example.org",
		ReadToken:             "read-only-token",
	})

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_preferences","arguments":{"display_currency":"EUR"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer read-only-token")
	rr := httptest.NewRecorder()
	hs.handleMCP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("insufficient-scope tools/call = %d, want 403", rr.Code)
	}
	want := `Bearer resource_metadata="https://travel.example.org/.well-known/oauth-protected-resource/mcp", error="insufficient_scope", scope="trvl:write"`
	if got := rr.Header().Get("WWW-Authenticate"); got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
}

// GET/POST etc. on the well-known routes: 405 must carry an Allow header
// (RFC 9110 §15.5.6).
func TestHandleProtectedResourceMetadata_405HasAllowHeader(t *testing.T) {
	t.Parallel()
	hs := NewHTTPServerWithOptions(HTTPServerOptions{
		Port:                  0,
		OAuthIntrospectionURL: "https://idp.example.org/oauth/introspect",
		OAuthIssuer:           "https://tenant.auth0.com/",
		PublicURL:             "https://travel.example.org",
	})
	mux := hs.newMux()

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/.well-known/oauth-protected-resource", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST well-known = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want %q", got, http.MethodGet)
	}
}

// RFC 9728 §1.2: a --public-url with a query or fragment, or a `{`/`}` in
// its path (Go 1.22+ http.ServeMux wildcard syntax — a brace segment in the
// decoded path becomes a live wildcard route in the well-known pattern, not
// a literal path match), is refused rather than accepted. A bare `?` with no
// key=value (net/url's ForceQuery) is refused the same way a real query is:
// RawQuery is empty but u.String() still re-emits the `?`.
func TestNormalizePublicURL_RejectsQueryFragmentAndBraces(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"https://host/base?x=1",
		"https://host/base#frag",
		"https://host/base?",
		"https://host/{id}",
		"https://host/}",
		"https://host/%7Bid%7D", // decodes to /{id} — still a wildcard, still rejected
		"https://:443/x",        // port-only authority, empty hostname
		"https://host/base#",    // bare fragment marker, Fragment=="" but still a fragment
	} {
		if _, err := normalizePublicURL(raw); err == nil {
			t.Errorf("normalizePublicURL(%q) = nil error, want rejection", raw)
		}
	}
}

// The same rejections normalizePublicURL enforces on --public-url must also
// stop buildProtectedResourceMetadata from publishing a resource identifier
// or challenge URL built from one: a real query, a real fragment, and a
// bare "?" (ForceQuery) all take the same path through normalizePublicURL,
// so all three must make buildProtectedResourceMetadata return nil.
func TestBuildProtectedResourceMetadata_RejectsForceQueryPublicURL(t *testing.T) {
	t.Parallel()
	for _, publicURL := range []string{
		"https://host/base?",
		"https://host/base?x=1",
		"https://host/base#frag",
	} {
		prm := buildProtectedResourceMetadata(HTTPServerOptions{
			OAuthIntrospectionURL: "https://idp.example.org/oauth/introspect",
			OAuthIssuer:           "https://tenant.auth0.com/",
			PublicURL:             publicURL,
		})
		if prm != nil {
			t.Errorf("buildProtectedResourceMetadata(publicURL=%q) = %+v, want nil", publicURL, prm)
		}
	}
}

// RFC 9728 §1.2 / the http.ServeMux route these paths build (see design doc
// (b)): a --public-url path that is well-formed URL-wise but produces a
// pattern http.ServeMux itself refuses to register (whitespace) must be
// caught here, not left to panic the server at route-registration time. A
// bare brace segment (no whitespace) is caught by the wildcard check above,
// not this one — it registers fine, it just means something other than a
// literal path.
func TestNormalizePublicURL_RejectsUnregistrablePatterns(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"https://host/a b",
		"https://host/a\tb",
	} {
		if _, err := normalizePublicURL(raw); err == nil {
			t.Errorf("normalizePublicURL(%q) = nil error, want rejection (unregistrable http.ServeMux pattern)", raw)
		}
	}
}

// A --public-url path segment whose percent-encoding changes segment
// structure (e.g. %2F for a literal "/") makes RawPath diverge from Path,
// so the well-known route registered from the decoded Path and the
// resource identifier published from the encoded String() disagree — a
// client following the published URL 404s. The RawPath!="" check is a
// deliberately conservative proxy for "encoding changes segment
// structure", not an exact test of it — it also rejects benign
// non-default encodings that don't change structure (%41 for "A"), which
// is an accepted false positive, not a claim that every such encoding is
// unsupported. Encodings that don't set RawPath at all (café, "a+b")
// leave it empty and must still work.
func TestNormalizePublicURL_RejectsPathEncodingThatChangesSegments(t *testing.T) {
	t.Parallel()
	if _, err := normalizePublicURL("https://h/a%2Fb"); err == nil {
		t.Error(`normalizePublicURL("https://h/a%2Fb") = nil error, want rejection (RawPath diverges from Path)`)
	}
	for _, raw := range []string{
		"https://h/caf%C3%A9",
		"https://h/a+b",
	} {
		if _, err := normalizePublicURL(raw); err != nil {
			t.Errorf("normalizePublicURL(%q) = %v, want acceptance (no segment-structure change)", raw, err)
		}
	}
}

// RFC 8414 §2: --oauth-issuer must be an absolute https:// URL, not any
// nonempty string.
func TestRequireOAuthPRMConfig_OAuthIssuerMustBeAbsoluteHTTPSURL(t *testing.T) {
	t.Parallel()
	base := HTTPServerOptions{
		OAuthIntrospectionURL: "https://idp.example.org/oauth/introspect",
		PublicURL:             "https://travel.example.org",
	}
	for _, tt := range []struct {
		name    string
		issuer  string
		wantErr bool
	}{
		{"bare word rejected", "auth0", true},
		{"http scheme rejected", "http://tenant.auth0.com/", true},
		{"absolute https accepted", "https://tenant.auth0.com/", false},
		{"query rejected", "https://tenant.auth0.com/?x=1", true},
		{"fragment rejected", "https://tenant.auth0.com/#f", true},
		{"bare question mark rejected", "https://tenant.auth0.com/?", true},
		{"bare hash rejected", "https://host/#", true},
		{"port-only host with empty hostname rejected", "https://:443", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := base
			opts.OAuthIssuer = tt.issuer
			err := requireOAuthPRMConfig(opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("requireOAuthPRMConfig(issuer=%q) err=%v, wantErr=%v", tt.issuer, err, tt.wantErr)
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
