package main

import (
	"github.com/MikkoParkkola/trvl/mcp"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	var (
		httpMode              bool
		host                  string
		port                  int
		token                 string
		readToken             string
		writeToken            string
		oauthIntrospectionURL string
		oauthClientID         string
		oauthClientSecret     string
		oauthAudience         string
	)

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP (Model Context Protocol) server",
		Long: `Start the trvl MCP server for AI agent integration.

By default, runs in stdio mode (JSON-RPC over stdin/stdout) for use with
Claude Code and other MCP-compatible clients.

Use --http to start an HTTP server instead. It listens on 127.0.0.1 by
default; pass --host explicitly for gateway or remote access. HTTP mode
requires bearer-token authentication, using --token, TRVL_MCP_TOKEN, or a
random token generated at startup. Remote deployments can use scoped
read/write bearer tokens or OAuth 2.1 access-token introspection; trvl acts as
the MCP resource server and expects the OAuth provider or gateway to handle
Authorization Code + PKCE.

For safety, binding to a non-loopback host (e.g. --host 0.0.0.0) without any
token or OAuth introspection configured is refused: remote exposure requires
explicit authentication. See docs/REMOTE-MCP-OAUTH.md.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if httpMode {
				return mcp.RunHTTPWithOptions(mcp.HTTPServerOptions{
					Host:                  host,
					Port:                  port,
					Token:                 token,
					ReadToken:             readToken,
					WriteToken:            writeToken,
					OAuthIntrospectionURL: oauthIntrospectionURL,
					OAuthClientID:         oauthClientID,
					OAuthClientSecret:     oauthClientSecret,
					OAuthAudience:         oauthAudience,
				})
			}
			return mcp.Run()
		},
	}

	cmd.Flags().BoolVar(&httpMode, "http", false, "Run as HTTP server instead of stdio")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "HTTP server host (only used with --http)")
	cmd.Flags().IntVar(&port, "port", 8080, "HTTP server port (only used with --http)")
	cmd.Flags().StringVar(&token, "token", "", "Bearer token for HTTP mode (default: TRVL_MCP_TOKEN or generated)")
	cmd.Flags().StringVar(&readToken, "read-token", "", "Read-only bearer token for HTTP mode (default: TRVL_MCP_READ_TOKEN)")
	cmd.Flags().StringVar(&writeToken, "write-token", "", "Read/write bearer token for HTTP mode (default: TRVL_MCP_WRITE_TOKEN)")
	cmd.Flags().StringVar(&oauthIntrospectionURL, "oauth-introspection-url", "", "OAuth 2.1 token introspection URL for HTTP mode (default: TRVL_MCP_OAUTH_INTROSPECTION_URL)")
	cmd.Flags().StringVar(&oauthClientID, "oauth-client-id", "", "OAuth introspection client ID (default: TRVL_MCP_OAUTH_CLIENT_ID)")
	cmd.Flags().StringVar(&oauthClientSecret, "oauth-client-secret", "", "OAuth introspection client secret (default: TRVL_MCP_OAUTH_CLIENT_SECRET)")
	cmd.Flags().StringVar(&oauthAudience, "oauth-audience", "", "Required OAuth audience claim for HTTP mode (default: TRVL_MCP_OAUTH_AUDIENCE)")

	cmd.AddCommand(mcpInstallCmd())

	return cmd
}
