// Package mcp implements an MCP (Model Context Protocol) server for trvl.
//
// It supports two transports:
//   - stdio: JSON-RPC messages over stdin/stdout (one JSON object per line)
//   - HTTP:  JSON-RPC messages via POST /mcp
//
// The server exposes the current trvl travel tools for flights, hotels, ground
// transport, trip state, weather, baggage, and travel hacks. It also provides
// prompts and resources.
//
// Protocol version: 2025-11-25
// Key features: structured output, content annotations, progress notifications,
// and logging.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/telemetry"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

const (
	serverName      = "trvl"
	protocolVersion = "2025-11-25"

	// toolTimeout is the maximum wall-clock time a single tool call may run.
	// Prevents hung queries from blocking the MCP server indefinitely.
	// Flights: 2-5s typical, 30s worst case (Google + Kiwi sequential).
	// Hotels: 5-20s typical, 60s worst case (Google 9 pages + providers).
	// Ground: 10-30s typical (20 parallel providers).
	// 60s is the hard cap — kills multi-page searches and preflight cascades
	// before they accumulate to minutes.
	toolTimeout = 60 * time.Second

	// maxConcurrentTools limits how many tool calls execute in parallel.
	// AI agents may spawn 8+ simultaneous searches, overwhelming upstream
	// rate limits. 4 concurrent searches is a good balance between
	// throughput and not tripping provider bot detection.
	maxConcurrentTools = 4
)

// serverVersion is set at link time for release builds.
var serverVersion = "dev"

// --- Server ---

// Server handles MCP JSON-RPC requests.
type Server struct {
	tools              []ToolDef
	handlers           map[string]ToolHandler
	prompts            []PromptDef
	resources          []ResourceDef
	clientCapabilities ClientCapabilities

	// Notification writer, set during ServeStdio for server-to-client messages.
	notifyWriter io.Writer
	notifyMu     sync.Mutex

	// For elicitation: reader set during ServeStdio.
	elicitReader *bufio.Scanner

	// Session state for trip planning.
	tripState  TripState
	priceCache *priceCache

	// Watch store for price tracking resources.
	watchStore *watch.Store

	// Background price check scheduler.
	scheduler *watch.Scheduler

	// Resource subscriptions: map from URI to true.
	subsMu sync.Mutex
	subs   map[string]bool

	// Concurrency semaphore: limits parallel tool executions.
	toolSem chan struct{}

	// External provider support.
	providerRegistry *providers.Registry
	providerRuntime  *providers.Runtime

	// OTel shutdown function; non-nil when tracing is active.
	otelShutdown func(context.Context) error
}

// ToolHandler processes a tool call and returns content blocks, optional
// structured content, and an error.
// The ctx parameter carries the request deadline and cancellation signal.
// The elicit parameter may be nil if the client does not support elicitation.
// The sampling parameter may be nil if the client does not support sampling.
// The progress parameter may be nil if notifications are not available (HTTP).
type ToolHandler func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error)

// NewServer creates a new MCP server with the standard trvl tools registered.
func NewServer() *Server {
	s := &Server{
		handlers:   make(map[string]ToolHandler),
		priceCache: newPriceCache(),
		subs:       make(map[string]bool),
		toolSem:    make(chan struct{}, maxConcurrentTools),
	}

	// Initialize watch store (best-effort; nil store is handled gracefully).
	if ws, err := watch.DefaultStore(); err == nil {
		_ = ws.Load()
		s.watchStore = ws
	}

	// Prepare the background price-check scheduler (started in ServeStdio/RunHTTP,
	// not here, so that tests calling NewServer() directly don't spawn goroutines).
	if home, err := os.UserHomeDir(); err == nil {
		watchDir := filepath.Join(home, ".trvl")
		s.scheduler = watch.NewScheduler(watchDir, 30*time.Minute, watch.NoopChecker{})
	}

	// Initialize provider registry (best-effort; nil registry is handled gracefully).
	if reg, err := providers.NewRegistry(); err == nil {
		s.providerRegistry = reg
		s.providerRuntime = providers.NewRuntime(reg)
		// Wire external providers into the hotel search pipeline.
		hotels.SetExternalProviderRuntime(s.providerRuntime)
	} else {
		log.Printf("warning: provider registry: %v", err)
	}

	// If TRVL_OTEL_ENDPOINT is set, initialise OTel tracing.
	if endpoint := os.Getenv("TRVL_OTEL_ENDPOINT"); endpoint != "" {
		shutdown, err := telemetry.Init(context.Background(), endpoint)
		if err != nil {
			slog.Warn("OTel init failed, tracing disabled", "endpoint", endpoint, "err", err)
		} else {
			s.otelShutdown = shutdown
			slog.Info("OTel tracing enabled", "endpoint", endpoint)
		}
	}

	registerTools(s)
	registerPrompts(s)
	registerResources(s)
	return s
}

// maxTripStateSearches caps tripState.Searches to bound memory growth on long
// sessions. Older entries are dropped FIFO when the cap is hit. Per audit
// MIK-3073: append-only without bound is a slow leak.
const maxTripStateSearches = 1000

// recordSearch adds a search record to the session trip state.
//
// The Searches slice is capped at maxTripStateSearches entries; the oldest
// records are evicted FIFO once the cap is reached. The eviction reuses the
// underlying array storage to avoid repeated allocations.
func (s *Server) recordSearch(typ, query string, bestPrice float64, currency string) {
	s.tripState.mu.Lock()
	defer s.tripState.mu.Unlock()
	rec := SearchRecord{
		Type:      typ,
		Query:     query,
		BestPrice: bestPrice,
		Currency:  currency,
		Time:      time.Now(),
	}
	if len(s.tripState.Searches) >= maxTripStateSearches {
		// Drop the oldest entry by shifting in place. copy is faster than
		// re-slicing + reallocating for the steady-state cap behaviour.
		copy(s.tripState.Searches, s.tripState.Searches[1:])
		s.tripState.Searches[len(s.tripState.Searches)-1] = rec
		return
	}
	s.tripState.Searches = append(s.tripState.Searches, rec)
}

// SendNotification writes a JSON-RPC notification to the client (server->client).
func (s *Server) SendNotification(method string, params interface{}) error {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	if s.notifyWriter == nil {
		return nil // No writer available (HTTP mode or not started).
	}

	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return writeJSON(s.notifyWriter, notif)
}

// SendProgress sends a progress notification to the client.
func (s *Server) SendProgress(token string, progress, total float64, message string) {
	_ = s.SendNotification("notifications/progress", ProgressParams{
		ProgressToken: token,
		Progress:      progress,
		Total:         total,
		Message:       message,
	})
}

// SendLog sends a log notification to the client.
func (s *Server) SendLog(level, message string) {
	_ = s.SendNotification("notifications/message", LogParams{
		Level:  level,
		Logger: "trvl",
		Data:   message,
	})
}

// makeElicitFunc returns the transport-level elicitation hook.
//
// Returns a real ElicitFunc when the client declares the elicitation capability
// and the server has both a notification writer (for sending requests) and an
// elicit reader (for receiving responses). Returns nil otherwise, which means
// tool handlers must fall back to CLI instructions.
func (s *Server) makeElicitFunc() ElicitFunc {
	if s.clientCapabilities.Elicitation == nil {
		return nil
	}
	s.notifyMu.Lock()
	hasWriter := s.notifyWriter != nil
	s.notifyMu.Unlock()
	if !hasWriter {
		return nil
	}
	if s.elicitReader == nil {
		return nil
	}

	return func(message string, schema map[string]interface{}) (map[string]interface{}, error) {
		id := fmt.Sprintf("elicit-%d", time.Now().UnixNano())

		req := ElicitationRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "elicitation/create",
			Params: ElicitationReqParams{
				Message:         message,
				RequestedSchema: schema,
			},
		}

		s.notifyMu.Lock()
		data, err := json.Marshal(req)
		if err != nil {
			s.notifyMu.Unlock()
			return nil, fmt.Errorf("elicitation: marshal request: %w", err)
		}
		_, err = fmt.Fprintf(s.notifyWriter, "%s\n", data)
		s.notifyMu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("elicitation: send request: %w", err)
		}

		if !s.elicitReader.Scan() {
			return nil, fmt.Errorf("elicitation: no response from client")
		}

		var resp ElicitationResponse
		if err := json.Unmarshal(s.elicitReader.Bytes(), &resp); err != nil {
			return nil, fmt.Errorf("elicitation: parse response: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("elicitation: client error: %s", resp.Error.Message)
		}
		if resp.Result.Action != "accept" {
			return nil, nil
		}
		return resp.Result.Content, nil
	}
}

// makeSamplingFunc returns the transport-level sampling hook.
//
// Returns nil. Sampling not wired at transport level. Claude Code does not
// implement sampling client-side. Revisit when a target client supports it
// OR when a tool genuinely needs to delegate LLM reasoning server-side.
func (s *Server) makeSamplingFunc() SamplingFunc {
	return nil
}

// makeProgressFunc returns a ProgressFunc that sends notifications/progress
// for the given token. The function is fire-and-forget; errors are discarded.
// Returns nil if there is no notification writer (HTTP transport or not started).
func (s *Server) makeProgressFunc(token string) ProgressFunc {
	s.notifyMu.Lock()
	hasWriter := s.notifyWriter != nil
	s.notifyMu.Unlock()
	if !hasWriter {
		return nil
	}
	return func(progress, total float64, message string) {
		s.SendProgress(token, progress, total, message)
	}
}

// HandleRequest processes a single JSON-RPC request and returns the response.
func (s *Server) HandleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil
	case "notifications/cancelled":
		return nil // Client cancelled a request; acknowledged.
	case "ping":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "logging/setLevel":
		return s.handleLoggingSetLevel(req)
	case "completion/complete":
		return s.handleCompletionComplete(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(req)
	case "resources/subscribe":
		return s.handleResourcesSubscribe(req)
	case "resources/unsubscribe":
		return s.handleResourcesUnsubscribe(req)
	default:
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	// Parse client capabilities from the initialize request.
	if req.Params != nil {
		var params InitializeParams
		if err := json.Unmarshal(req.Params, &params); err == nil {
			s.clientCapabilities = params.Capabilities
			if params.Capabilities.Sampling != nil {
				s.SendLog("info", "Client supports sampling/createMessage; trvl currently keeps transport-level sampling disabled")
			}
			if params.Capabilities.Elicitation != nil {
				s.SendLog("info", "Client supports elicitation/create; trvl currently uses follow-up suggestions instead")
			}
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: InitializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities: Capabilities{
				Tools:     &ToolsCapability{ListChanged: false},
				Prompts:   &PromptsCapability{ListChanged: false},
				Resources: &ResourcesCapability{Subscribe: true, ListChanged: true},
				Logging:   &LoggingCapability{},
			},
			ServerInfo: ServerInfo{
				Name:    serverName,
				Version: serverVersion,
			},
		},
	}
}

func (s *Server) handleToolsList(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsListResult{Tools: s.tools},
	}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)},
		}
	}

	handler, ok := s.handlers[params.Name]
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)},
		}
	}

	// Log the tool call.
	s.SendLog("info", fmt.Sprintf("Calling tool: %s", params.Name))

	// Build elicit, sampling, and progress functions based on client capabilities.
	elicit := s.makeElicitFunc()
	sampling := s.makeSamplingFunc()
	progressToken := fmt.Sprintf("%s-%v", params.Name, req.ID)
	progress := s.makeProgressFunc(progressToken)

	// Per-tool timeout. This is the hard ceiling for any single tool call.
	// The wrapHandler wrapper also enforces toolTimeout (60s) as a fallback,
	// but this server-level timeout is the canonical one. Previously 120s,
	// which was too generous — agents spawning 8 parallel searches would all
	// hang for 2 minutes before timing out.
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	ctx = providers.WithInteractive(ctx)

	// Thread elicitation into the provider runtime context so Tier 4 WAF
	// recovery can prompt the user instead of silently timing out.
	if elicit != nil {
		ctx = providers.WithElicit(ctx, func(message string) (bool, error) {
			resp, err := elicit(message, map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"done": map[string]interface{}{
						"type":        "boolean",
						"description": "Confirm when you have completed the browser challenge",
						"default":     true,
					},
				},
			})
			if err != nil {
				return false, err
			}
			if done, ok := resp["done"].(bool); ok {
				return done, nil
			}
			// If the user responded at all, treat it as confirmation.
			return resp != nil, nil
		})
	}

	content, structured, err := handler(ctx, params.Arguments, elicit, sampling, progress)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentBlock{{Type: "text", Text: err.Error()}},
				IsError: true,
			},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolCallResult{
			Content:           content,
			StructuredContent: structured,
		},
	}
}

func (s *Server) handlePromptsList(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  PromptsListResult{Prompts: s.prompts},
	}
}

func (s *Server) handlePromptsGet(req *Request) *Response {
	var params PromptsGetParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)},
		}
	}

	result, err := getPrompt(params.Name, params.Arguments)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32602, Message: err.Error()},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

func (s *Server) handleResourcesList(req *Request) *Response {
	// Combine static resources with dynamic ones from trip state.
	resources := s.listResources()
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ResourcesListResult{Resources: resources},
	}
}

func (s *Server) handleResourcesRead(req *Request) *Response {
	var params ResourcesReadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)},
		}
	}

	result, err := s.readResource(params.URI)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32002, Message: err.Error()},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

// --- Resource subscription handlers ---

func (s *Server) handleResourcesSubscribe(req *Request) *Response {
	var params ResourcesReadParams
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &params)
	}
	if params.URI != "" {
		s.subsMu.Lock()
		s.subs[params.URI] = true
		s.subsMu.Unlock()
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
}

func (s *Server) handleResourcesUnsubscribe(req *Request) *Response {
	var params ResourcesReadParams
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &params)
	}
	if params.URI != "" {
		s.subsMu.Lock()
		delete(s.subs, params.URI)
		s.subsMu.Unlock()
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
}

// SendResourceUpdated sends a notifications/resources/updated notification to
// all clients that have subscribed to the given URI. Fire-and-forget.
func (s *Server) SendResourceUpdated(uri string) {
	s.subsMu.Lock()
	subscribed := s.subs[uri]
	s.subsMu.Unlock()
	if !subscribed {
		return
	}
	_ = s.SendNotification("notifications/resources/updated", map[string]string{"uri": uri})
}

// --- logging/setLevel handler ---

// logLevelMu protects logLevel from concurrent read/write.
var logLevelMu sync.Mutex

// logLevel stores the current minimum log level. Access via getLogLevel/setLogLevel.
var logLevel = "info"

func getLogLevel() string {
	logLevelMu.Lock()
	defer logLevelMu.Unlock()
	return logLevel
}

func setLogLevel(level string) {
	logLevelMu.Lock()
	logLevel = level
	logLevelMu.Unlock()
}

func (s *Server) handleLoggingSetLevel(req *Request) *Response {
	var params struct {
		Level string `json:"level"`
	}
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &params)
	}
	if params.Level != "" {
		setLogLevel(params.Level)
		s.SendLog("info", fmt.Sprintf("Log level set to %s", params.Level))
	}
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
}

// --- completion/complete handler ---

// handleCompletionComplete provides argument auto-completion for tools and prompts.
func (s *Server) handleCompletionComplete(req *Request) *Response {
	var params struct {
		Ref struct {
			Type string `json:"type"` // "ref/prompt" or "ref/resource"
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"ref"`
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
	}
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &params)
	}

	var values []string

	// Provide completions for known argument patterns.
	switch params.Argument.Name {
	case "origin", "destination", "from", "to":
		// Return matching IATA airport codes.
		values = completeAirport(params.Argument.Value)
	case "cabin_class":
		values = []string{"economy", "premium_economy", "business", "first"}
	case "sort":
		values = []string{"cheapest", "rating", "distance", "stars"}
	case "type":
		values = []string{"bus", "train"}
	case "provider":
		values = []string{"flixbus", "regiojet"}
	case "currency":
		values = []string{"EUR", "USD", "GBP", "CZK", "PLN", "SEK", "NOK", "DKK", "CHF", "JPY"}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"completion": map[string]any{
				"values":  values,
				"hasMore": false,
				"total":   len(values),
			},
		},
	}
}

// completeAirport returns IATA codes matching the given prefix.
func completeAirport(prefix string) []string {
	if prefix == "" {
		return nil
	}
	prefix = toUpper(prefix)
	var matches []string
	for code := range airportCompletionMap {
		if len(matches) >= 20 {
			break
		}
		if len(code) >= len(prefix) && code[:len(prefix)] == prefix {
			matches = append(matches, code)
		}
	}
	return matches
}

func toUpper(s string) string {
	b := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		b[i] = c
	}
	return string(b)
}

// airportCompletionMap is populated from the models package at init time.
var airportCompletionMap map[string]string

func init() {
	// Build airport completion map lazily on first access.
	airportCompletionMap = make(map[string]string, 250)
	// Common airports — populated from models.AirportNames if available,
	// otherwise a static subset for completion.
	commonAirports := map[string]string{
		"HEL": "Helsinki", "AMS": "Amsterdam", "PRG": "Prague", "KRK": "Krakow",
		"CDG": "Paris CDG", "ORY": "Paris Orly", "LHR": "London Heathrow",
		"LGW": "London Gatwick", "STN": "London Stansted", "FCO": "Rome",
		"BCN": "Barcelona", "MAD": "Madrid", "VIE": "Vienna", "BUD": "Budapest",
		"WAW": "Warsaw", "BER": "Berlin", "MUC": "Munich", "FRA": "Frankfurt",
		"ZRH": "Zurich", "CPH": "Copenhagen", "OSL": "Oslo", "ARN": "Stockholm",
		"DUB": "Dublin", "BRU": "Brussels", "LIS": "Lisbon", "ATH": "Athens",
		"IST": "Istanbul", "JFK": "New York JFK", "EWR": "Newark", "LAX": "Los Angeles",
		"SFO": "San Francisco", "ORD": "Chicago", "NRT": "Tokyo Narita",
		"HND": "Tokyo Haneda", "ICN": "Seoul", "SIN": "Singapore", "BKK": "Bangkok",
		"HKG": "Hong Kong", "SYD": "Sydney", "DXB": "Dubai", "DOH": "Doha",
	}
	for code, name := range commonAirports {
		airportCompletionMap[code] = name
	}
}

// Shutdown stops background services (e.g. the price-check scheduler).
// It is called automatically by ServeStdio when the stdin stream ends.
func (s *Server) Shutdown() {
	if s.scheduler != nil {
		s.scheduler.Stop()
	}
	if s.otelShutdown != nil {
		_ = s.otelShutdown(context.Background())
	}
}

// ServeStdio runs the MCP server over stdin/stdout.
// Each line of input is a JSON-RPC request; each response is written as a single JSON line.
func (s *Server) ServeStdio(in io.Reader, out io.Writer) error {
	// Start the background scheduler now that we are in a real server session.
	if s.scheduler != nil {
		s.scheduler.Start()
	}
	defer s.Shutdown()

	scanner := bufio.NewScanner(in)
	// Allow up to 1MB per line for large tool call results.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Set up notification writer and elicitation reader for server->client.
	s.notifyWriter = out
	s.elicitReader = scanner

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			resp := Response{
				JSONRPC: "2.0",
				Error:   &Error{Code: -32700, Message: fmt.Sprintf("parse error: %v", err)},
			}
			if writeErr := writeJSON(out, resp); writeErr != nil {
				return writeErr
			}
			continue
		}

		resp := s.HandleRequest(&req)
		if resp == nil {
			// Notification -- no response.
			continue
		}
		if err := writeJSON(out, resp); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

// writeJSON marshals v as a single JSON line to w.
func writeJSON(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	if _, err := fmt.Fprintf(w, "%s\n", data); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

// Run starts the MCP server on stdin/stdout. This is the main entry point
// for the stdio transport.
//
// Coverage exclusion: blocking stdio entry point.
// ServeStdio (which Run calls) is tested via buffer I/O in server_test.go.
func Run() error {
	s := NewServer()
	log.SetOutput(io.Discard) // Suppress log output on stdio transport.
	return s.ServeStdio(os.Stdin, os.Stdout)
}
