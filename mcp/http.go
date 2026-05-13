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
	"time"
)

const defaultHTTPHost = "127.0.0.1"

// HTTPServer wraps an MCP Server with an HTTP transport.
type HTTPServer struct {
	server *Server
	host   string
	port   int
	token  string
}

// HTTPServerOptions configures the HTTP MCP transport.
type HTTPServerOptions struct {
	Host  string
	Port  int
	Token string
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
		token:  strings.TrimSpace(opts.Token),
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

	if !h.authorize(r) {
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

	resp := h.server.HandleRequest(&req)
	if resp == nil {
		// Notification — return 204 No Content.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *HTTPServer) authorize(r *http.Request) bool {
	if h.token == "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, prefix)) == h.token
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"server":  serverName,
		"version": serverVersion,
		"tools":   len(h.server.tools),
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

// RunHTTP starts the MCP server in HTTP mode on the given host and port.
//
// Coverage exclusion: blocking HTTP server entry point.
// Calls ListenAndServe, whose handler logic is tested via httptest in server_extra_test.go.
func RunHTTP(host string, port int, token string) error {
	generatedToken := false
	if strings.TrimSpace(token) == "" {
		token = strings.TrimSpace(os.Getenv("TRVL_MCP_TOKEN"))
	}
	if strings.TrimSpace(token) == "" {
		generated, err := generateMCPToken()
		if err != nil {
			return fmt.Errorf("generate MCP HTTP token: %w", err)
		}
		token = generated
		generatedToken = true
	}
	if generatedToken {
		log.Printf("trvl MCP generated HTTP bearer token: %s", token)
	} else {
		log.Printf("trvl MCP HTTP auth enabled")
	}
	return NewHTTPServerWithOptions(HTTPServerOptions{
		Host:  host,
		Port:  port,
		Token: token,
	}).ListenAndServe()
}

func generateMCPToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
