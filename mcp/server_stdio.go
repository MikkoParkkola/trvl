// Package mcp: stdio transport, logging, and completion handlers.
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
	"sync"
)

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

	// Set up the notification writer for server->client messages. The read loop
	// below owns the only reader; handlers receive client responses (elicitation)
	// via routed channels rather than reading the transport directly.
	s.notifyMu.Lock()
	s.notifyWriter = out
	s.notifyMu.Unlock()

	// Each request is handled in its own goroutine so a long-running tool call
	// (a 15-30s travel search) never blocks the read loop — control messages like
	// `ping` stay snappy, which keeps health-probing transports (e.g. an MCP
	// gateway) from tearing down a backend that is merely busy. Concurrency is
	// already bounded per-tool by toolSem; stdout writes are serialized by
	// notifyMu so responses never interleave. A WaitGroup ensures every dispatched
	// handler has flushed its response before ServeStdio returns (clean shutdown;
	// deterministic for buffered callers and tests).
	var handlers sync.WaitGroup
	for scanner.Scan() {
		b := scanner.Bytes()
		if len(b) == 0 {
			continue
		}
		// scanner reuses its buffer on the next Scan; copy before handing to a
		// goroutine that may outlive this iteration.
		line := make([]byte, len(b))
		copy(line, b)

		// A response to a server->client request (elicitation) carries no method;
		// route it to the waiting handler instead of treating it as a request.
		if s.routeClientResponse(line) {
			continue
		}

		handlers.Add(1)
		go func(line []byte) {
			defer handlers.Done()
			var req Request
			if err := json.Unmarshal(line, &req); err != nil {
				s.writeMessage(out, Response{
					JSONRPC: "2.0",
					Error:   &Error{Code: -32700, Message: fmt.Sprintf("parse error: %v", err)},
				})
				return
			}

			resp := s.HandleRequest(&req)
			if resp == nil {
				// Notification -- no response.
				return
			}
			s.writeMessage(out, resp)
		}(line)
	}

	handlers.Wait()
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

// writeMessage serializes a JSON-RPC message to the stdio output. notifyMu
// guards the writer so concurrent request handlers (and notifications) never
// interleave lines.
func (s *Server) writeMessage(out io.Writer, v any) {
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if err := writeJSON(out, v); err != nil {
		slog.Warn("mcp_stdio_write_failed", "error", err)
	}
}

// routeClientResponse delivers a client's response to a pending server->client
// request (elicitation) to the waiting handler. It returns true when the line
// was a routed response; false when it is a request/notification the read loop
// should dispatch normally. A JSON-RPC response has an id and no method.
func (s *Server) routeClientResponse(line []byte) bool {
	var probe struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return false
	}
	if probe.Method != "" || len(probe.ID) == 0 {
		return false // has a method => request; no id => notification
	}
	var id string
	if err := json.Unmarshal(probe.ID, &id); err != nil {
		return false // elicitation ids are strings
	}

	s.elicitMu.Lock()
	ch, ok := s.pendingElicit[id]
	if ok {
		delete(s.pendingElicit, id)
	}
	s.elicitMu.Unlock()
	if !ok {
		return false
	}

	msg := make([]byte, len(line))
	copy(msg, line)
	ch <- msg
	return true
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
