package mcp

import "testing"

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
