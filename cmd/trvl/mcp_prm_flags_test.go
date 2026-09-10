package main

import (
	"strings"
	"testing"
)

// TestMCPCmdWiresOAuthPRMFlags guards the seam between this package and mcp:
// --oauth-issuer and --public-url are declared here but validated in
// requireOAuthPRMConfig, so dropping either assignment into HTTPServerOptions
// still compiles and no mcp-package test can observe it.
//
// Each case discriminates wired-but-rejected from never-wired. An unwired flag
// reaches the validator as the empty string and is reported as "is required";
// a wired one is rejected on its own content. Asserting only "returns an error"
// would pass in both directions and prove nothing.
//
// Both paths refuse in RunHTTPWithOptions before ListenAndServe, so no listener
// binds and the test needs no port.
func TestMCPCmdWiresOAuthPRMFlags(t *testing.T) {
	for _, key := range []string{
		"TRVL_MCP_TOKEN",
		"TRVL_MCP_READ_TOKEN",
		"TRVL_MCP_WRITE_TOKEN",
		"TRVL_MCP_OAUTH_INTROSPECTION_URL",
		"TRVL_MCP_OAUTH_CLIENT_ID",
		"TRVL_MCP_OAUTH_CLIENT_SECRET",
		"TRVL_MCP_OAUTH_AUDIENCE",
		"TRVL_MCP_OAUTH_ISSUER",
		"TRVL_MCP_PUBLIC_URL",
	} {
		t.Setenv(key, "")
	}

	const introspection = "https://idp.example/introspect"

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "issuer value reaches the validator",
			args: []string{
				"--http",
				"--oauth-introspection-url", introspection,
				"--oauth-issuer", "http://idp.example",
				"--public-url", "https://trvl.example/mcp",
			},
			want: "must be an absolute https:// URL",
		},
		{
			name: "public URL value reaches the validator",
			args: []string{
				"--http",
				"--oauth-introspection-url", introspection,
				"--oauth-issuer", "https://idp.example",
				"--public-url", "http://trvl.example/mcp",
			},
			want: "must be an https:// URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := mcpCmd()
			cmd.SetArgs(tc.args)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected the server to refuse to start, got nil")
			}
			if strings.Contains(err.Error(), "is required") {
				t.Fatalf("flag never reached HTTPServerOptions, validator saw it as unset: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
