package mcp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const defaultHTTPHost = "127.0.0.1"

// authAudit holds aggregate counters for auth decisions surfaced on /health.
//
// Only aggregate counts are exposed (never subjects, tokens, or denial
// reasons) because /health is unauthenticated. Counters are atomic so the
// shared HTTPServer is safe under concurrent requests.
type authAudit struct {
	allowed atomic.Uint64
	denied  atomic.Uint64
}

// HTTPServer wraps an MCP Server with an HTTP transport.
type HTTPServer struct {
	server *Server
	host   string
	port   int
	auth   *HTTPAuth
	audit  authAudit
}

// HTTPServerOptions configures the HTTP MCP transport.
type HTTPServerOptions struct {
	Host                  string
	Port                  int
	Token                 string
	ReadToken             string
	WriteToken            string
	OAuthIntrospectionURL string
	OAuthClientID         string
	OAuthClientSecret     string
	OAuthAudience         string
	HTTPClient            *http.Client
}

// NewHTTPServer creates an HTTP transport for the MCP server on the given port.
func NewHTTPServer(port int) *HTTPServer {
	return NewHTTPServerWithOptions(HTTPServerOptions{Host: defaultHTTPHost, Port: port})
}

// NewHTTPServerWithOptions creates an HTTP transport for the MCP server.
func NewHTTPServerWithOptions(opts HTTPServerOptions) *HTTPServer {
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		host = defaultHTTPHost
	}
	return &HTTPServer{
		server: NewServer(),
		host:   host,
		port:   opts.Port,
		auth:   NewHTTPAuth(opts),
	}
}

// ListenAndServe starts the HTTP server. It blocks until the server exits.
//
// Coverage exclusion: blocking HTTP server entry point.
// The handler logic (handleMCP, handleHealth) is tested via httptest in server_extra_test.go.
func (h *HTTPServer) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.handleMCP)
	mux.HandleFunc("/health", h.handleHealth)

	addr := net.JoinHostPort(h.host, strconv.Itoa(h.port))
	log.Printf("trvl MCP server listening on http://%s/mcp", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	// CORS headers — restrict to localhost origins only.
	origin := r.Header.Get("Origin")
	if isLocalhostOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	access, ok := h.authorize(r)
	if !ok {
		h.audit.denied.Add(1)
		slogHTTPAuthDecision("deny", "", "", "missing or invalid bearer token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 1MB to prevent abuse.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32700, Message: fmt.Sprintf("parse error: %v", err)},
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if errResp := h.authorizeJSONRPC(&req, access); errResp != nil {
		h.audit.denied.Add(1)
		slogHTTPAuthDecision("deny", req.Method, scopeSummary(access), errResp.Message)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   errResp,
		})
		return
	}

	h.audit.allowed.Add(1)
	slogHTTPAuthDecision("allow", req.Method, scopeSummary(access), access.Subject)

	resp := h.server.HandleRequest(&req)
	if resp == nil {
		// Notification — return 204 No Content.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *HTTPServer) authorize(r *http.Request) (RequestAccess, bool) {
	if h.auth == nil || !h.auth.Configured() {
		return FullAccess("anonymous", "disabled"), true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return RequestAccess{}, false
	}
	return h.auth.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(auth, prefix)))
}

func (h *HTTPServer) authorizeJSONRPC(req *Request, access RequestAccess) *Error {
	if !access.CanRead() {
		return &Error{Code: -32001, Message: "permission denied: token requires trvl:read scope"}
	}
	if req.Method != "tools/call" {
		return nil
	}
	tool, requiresWrite, ok := h.server.toolWriteRequirement(req)
	if !ok {
		return nil
	}
	if requiresWrite && !access.CanWrite() {
		return &Error{Code: -32001, Message: fmt.Sprintf("permission denied: tool %s requires trvl:write scope", tool)}
	}
	return nil
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// /health is unauthenticated: expose only aggregate auth-decision counts,
	// never subjects, tokens, scopes, or denial reasons.
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"server":  serverName,
		"version": serverVersion,
		"tools":   len(h.server.tools),
		"auth": map[string]interface{}{
			"enforced":          h.auth != nil && h.auth.Configured(),
			"decisions_allowed": h.audit.allowed.Load(),
			"decisions_denied":  h.audit.denied.Load(),
		},
	})
}

// isLocalhostOrigin checks if the origin is a localhost URL (any port).
func isLocalhostOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isRemoteBindHost reports whether binding to host exposes the server beyond
// the loopback interface. Empty/loopback hosts are local; "0.0.0.0", "::", any
// public IP, or a non-localhost hostname are treated as remote.
func isRemoteBindHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		// Empty defaults to the loopback host (see NewHTTPServerWithOptions).
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		// Unspecified (0.0.0.0, ::) binds all interfaces — that is remote.
		return !ip.IsLoopback()
	}
	// A non-loopback hostname (e.g. a public DNS name) is remote.
	return true
}

// requireRemoteAuth enforces GH-89.AUTH.4: a remote (non-loopback) bind must
// have authentication explicitly configured. authConfigured reports whether a
// token or OAuth introspection URL was supplied. It returns a clear error when
// a remote bind would otherwise run unauthenticated.
func requireRemoteAuth(host string, authConfigured bool) error {
	if authConfigured || !isRemoteBindHost(host) {
		return nil
	}
	return fmt.Errorf(
		"refusing to start: --host %q exposes the MCP server beyond localhost but no authentication is configured; "+
			"set --token/--read-token/--write-token (or TRVL_MCP_TOKEN), or --oauth-introspection-url, before binding to a non-loopback host",
		strings.TrimSpace(host),
	)
}

// scopeSummary renders an access scope set for audit logging (never the token).
func scopeSummary(access RequestAccess) string {
	switch {
	case access.CanWrite():
		return "read+write"
	case access.CanRead():
		return "read"
	default:
		return "none"
	}
}

// RunHTTP starts the MCP server in HTTP mode on the given host and port.
//
// Coverage exclusion: blocking HTTP server entry point.
// Calls ListenAndServe, whose handler logic is tested via httptest in server_extra_test.go.
func RunHTTP(host string, port int, token string) error {
	return RunHTTPWithOptions(HTTPServerOptions{Host: host, Port: port, Token: token})
}

func RunHTTPWithOptions(opts HTTPServerOptions) error {
	generatedToken := false
	if strings.TrimSpace(opts.Token) == "" {
		opts.Token = strings.TrimSpace(os.Getenv("TRVL_MCP_TOKEN"))
	}
	if strings.TrimSpace(opts.ReadToken) == "" {
		opts.ReadToken = strings.TrimSpace(os.Getenv("TRVL_MCP_READ_TOKEN"))
	}
	if strings.TrimSpace(opts.WriteToken) == "" {
		opts.WriteToken = strings.TrimSpace(os.Getenv("TRVL_MCP_WRITE_TOKEN"))
	}
	if strings.TrimSpace(opts.OAuthIntrospectionURL) == "" {
		opts.OAuthIntrospectionURL = strings.TrimSpace(os.Getenv("TRVL_MCP_OAUTH_INTROSPECTION_URL"))
	}
	if strings.TrimSpace(opts.OAuthClientID) == "" {
		opts.OAuthClientID = strings.TrimSpace(os.Getenv("TRVL_MCP_OAUTH_CLIENT_ID"))
	}
	if strings.TrimSpace(opts.OAuthClientSecret) == "" {
		opts.OAuthClientSecret = strings.TrimSpace(os.Getenv("TRVL_MCP_OAUTH_CLIENT_SECRET"))
	}
	if strings.TrimSpace(opts.OAuthAudience) == "" {
		opts.OAuthAudience = strings.TrimSpace(os.Getenv("TRVL_MCP_OAUTH_AUDIENCE"))
	}
	if !NewHTTPAuth(opts).Configured() {
		// GH-89.AUTH.4: remote exposure requires explicit auth. A non-loopback
		// bind with no configured token/OAuth is refused rather than silently
		// protected by an auto-generated token printed to logs. Localhost keeps
		// the convenient auto-generated token.
		if err := requireRemoteAuth(opts.Host, false); err != nil {
			return err
		}
		generated, err := generateMCPToken()
		if err != nil {
			return fmt.Errorf("generate MCP HTTP token: %w", err)
		}
		opts.Token = generated
		generatedToken = true
	}
	if generatedToken {
		log.Printf("trvl MCP generated HTTP bearer token: %s", opts.Token)
	} else if strings.TrimSpace(opts.OAuthIntrospectionURL) != "" {
		log.Printf("trvl MCP HTTP OAuth introspection auth enabled")
	} else {
		log.Printf("trvl MCP HTTP auth enabled")
	}
	return NewHTTPServerWithOptions(opts).ListenAndServe()
}

func generateMCPToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
