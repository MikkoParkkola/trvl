package mcp

import (
	"fmt"
	"net/url"
	"strings"
)

const wellKnownPRMRoot = "/.well-known/oauth-protected-resource"

// normalizePublicURL parses --public-url and strips any trailing slash from
// its path, so the value is stable to append "/mcp" or a well-known segment
// to (RFC 9728 §3.3: resource must equal the URL clients actually call).
func normalizePublicURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("--public-url is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("--public-url %q is not a valid URL: %w", trimmed, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("--public-url %q must be an absolute URL", trimmed)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

// resourceIdentifier is the RFC 9728 `resource` value: the MCP endpoint
// itself, not the bare host.
func resourceIdentifier(u *url.URL) string {
	return u.String() + "/mcp"
}

// isDevLoopbackHTTP reports the documented non-compliant convenience:
// http://localhost and http://127.0.0.1 are accepted unerrored, local
// development only.
func isDevLoopbackHTTP(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1"
}

// wellKnownPaths returns the root and path-suffixed well-known routes for
// publicURL (RFC 9728 §3). The well-known segment goes immediately after the
// authority; any path component publicURL carries comes after it.
func wellKnownPaths(publicURL string) (root, suffixed string, err error) {
	u, err := normalizePublicURL(publicURL)
	if err != nil {
		return "", "", err
	}
	return wellKnownPRMRoot, wellKnownPRMRoot + u.Path + "/mcp", nil
}

// prmDocument is the RFC 9728 Protected Resource Metadata document: exactly
// the two fields the design settled on, nothing optional guessed at.
type prmDocument struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// protectedResourceMetadata holds an HTTPServer's PRM config: the document to
// serve, the routes to serve it on, and the absolute URL used in the
// WWW-Authenticate challenge.
type protectedResourceMetadata struct {
	doc          prmDocument
	rootPath     string
	suffixedPath string
	challengeURL string
}

// buildProtectedResourceMetadata returns nil (PRM disabled) unless OAuth
// introspection, an issuer, and a valid public URL are all configured — the
// same fields requireOAuthPRMConfig enforces at startup. NewHTTPServerWithOptions
// is also called directly by tests that don't go through that gate, so this
// stays lenient rather than panicking on incomplete config.
func buildProtectedResourceMetadata(opts HTTPServerOptions) *protectedResourceMetadata {
	issuer := strings.TrimSpace(opts.OAuthIssuer)
	publicURL := strings.TrimSpace(opts.PublicURL)
	if strings.TrimSpace(opts.OAuthIntrospectionURL) == "" || issuer == "" || publicURL == "" {
		return nil
	}
	u, err := normalizePublicURL(publicURL)
	if err != nil {
		return nil
	}
	root, suffixed, err := wellKnownPaths(publicURL)
	if err != nil {
		return nil
	}
	challenge := *u
	challenge.Path = suffixed
	challenge.RawQuery = ""
	challenge.Fragment = ""
	return &protectedResourceMetadata{
		doc: prmDocument{
			Resource:             resourceIdentifier(u),
			AuthorizationServers: []string{issuer},
		},
		rootPath:     root,
		suffixedPath: suffixed,
		challengeURL: challenge.String(),
	}
}

// requireOAuthPRMConfig enforces the design's fail-closed startup gate: OAuth
// configured without a usable --oauth-issuer/--public-url is fatal, same
// shape as requireHTTPAuth. A no-op when OAuth introspection isn't configured.
func requireOAuthPRMConfig(opts HTTPServerOptions) error {
	if strings.TrimSpace(opts.OAuthIntrospectionURL) == "" {
		return nil
	}
	if strings.TrimSpace(opts.OAuthIssuer) == "" {
		return fmt.Errorf(
			"refusing to start: --oauth-issuer (or TRVL_MCP_OAUTH_ISSUER) is required when " +
				"--oauth-introspection-url is set, to publish RFC 9728 Protected Resource Metadata",
		)
	}
	publicURL := strings.TrimSpace(opts.PublicURL)
	if publicURL == "" {
		return fmt.Errorf(
			"refusing to start: --public-url (or TRVL_MCP_PUBLIC_URL) is required when " +
				"--oauth-introspection-url is set, to publish RFC 9728 Protected Resource Metadata",
		)
	}
	u, err := normalizePublicURL(publicURL)
	if err != nil {
		return fmt.Errorf("refusing to start: %w", err)
	}
	if u.Scheme != "https" && !isDevLoopbackHTTP(u) {
		return fmt.Errorf(
			"refusing to start: --public-url %q must be an https:// URL "+
				"(http://localhost or http://127.0.0.1 is allowed for local development only)",
			publicURL,
		)
	}
	resource := resourceIdentifier(u)
	if audience := strings.TrimSpace(opts.OAuthAudience); audience != "" && audience != resource {
		return fmt.Errorf(
			"refusing to start: --oauth-audience %q does not match the published resource %q "+
				"(leave --oauth-audience unset to derive it from --public-url)",
			audience, resource,
		)
	}
	return nil
}
