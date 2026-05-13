package flights

// Skiplagged flight provider.
//
// Uses the Skiplagged MCP server hosted at the public host
// `mcp.skiplagged.com` (path `/mcp`) — a Streamable HTTP MCP endpoint
// (protocol version 2025-06-18) that requires no API key. Each search
// session begins with an `initialize` handshake that returns an
// `Mcp-Session-Id` header, then a single `tools/call` to
// `sk_flights_search` returns the flight list.
//
// Skiplagged is the genre-defining brand for hidden-city ticketing.
// Their MCP server's `sk_flights_search` tool defaults to
// `includeHiddenCity: true` and `includeVirtualInterlining: true`,
// so the provider surfaces hidden-city candidates by default. Each
// returned FlightResult is tagged with Provider="skiplagged" and any
// hidden-city / virtual-interlining flag is propagated via Warnings.
//
// Tracking issue: trvl#62.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// skiplaggedMCPEndpointDefault is the public Skiplagged MCP host. The
// path is appended at request time; tests override the full URL via
// skiplaggedSetEndpointForTest.
const skiplaggedMCPEndpointDefault = "https" + "://" + "mcp.skiplagged.com" + "/mcp"

// skiplaggedMCPProtocolVersion is the MCP protocol version Skiplagged's
// Streamable HTTP endpoint reports during initialize. Verified live on
// 2026-04-27 against `@skiplagged/mcp v0.0.4`.
const skiplaggedMCPProtocolVersion = "2025-06-18"

// skiplaggedEnabled controls whether SearchSkiplagged makes live HTTP
// requests. Set to false in tests that mock the transport to prevent
// unintended real-network calls.
var skiplaggedEnabled = true

// skiplaggedEndpoint is the resolved endpoint URL. Tests can override
// it via skiplaggedSetEndpointForTest to point at a httptest server.
var skiplaggedEndpoint = skiplaggedMCPEndpointDefault

// skiplaggedLimiter enforces a 2 req/s rate limit — conservative to
// avoid overloading the public Skiplagged origin (which we observed
// returning HTTP 502 from Cloudflare under load on 2026-04-27).
var skiplaggedLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)

// skiplaggedHTTPClient is a dedicated HTTP client for Skiplagged MCP
// calls, separate from the package's flight-search clients so the
// timeout can be tuned independently.
var skiplaggedHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// skiplaggedSetEndpointForTest swaps the endpoint URL. Returns a
// restore function that re-points to the previous URL. Test-only.
func skiplaggedSetEndpointForTest(url string) func() {
	prev := skiplaggedEndpoint
	skiplaggedEndpoint = url
	return func() { skiplaggedEndpoint = prev }
}

// ---- JSON-RPC types ----

type skiplaggedRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type skiplaggedToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type skiplaggedInitParams struct {
	ProtocolVersion string               `json:"protocolVersion"`
	Capabilities    map[string]any       `json:"capabilities"`
	ClientInfo      skiplaggedClientInfo `json:"clientInfo"`
}

type skiplaggedClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type skiplaggedRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type skiplaggedToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	// IsError is the MCP-spec tool-error indicator. When true, the
	// content[0].text contains a human-readable error message
	// (e.g. "Failed to fetch: Invalid range for depart. Must be no
	// greater than 2027-03-22") rather than a structured payload.
	// We surface it as a Go error rather than letting downstream
	// JSON parsing fail with a cryptic "invalid character" message.
	IsError bool `json:"isError,omitempty"`
}

// ---- Skiplagged sk_flights_search response shape (verified against
// the tool's outputSchema returned by tools/list on 2026-04-27).

type skiplaggedFlightsResult struct {
	SearchURL string             `json:"searchUrl"`
	Flights   []skiplaggedFlight `json:"flights"`
}

type skiplaggedFlight struct {
	Type       string                  `json:"type"` // "FlightCard"
	ID         string                  `json:"id"`
	Airlines   string                  `json:"airlines"`
	Departure  skiplaggedFlightTerm    `json:"departure"`
	Arrival    skiplaggedFlightTerm    `json:"arrival"`
	Duration   string                  `json:"duration"`
	Layovers   float64                 `json:"layovers"`
	Price      skiplaggedFlightPrice   `json:"price"`
	DeepLink   string                  `json:"deepLink"`
	Attributes []string                `json:"attributes"` // "hidden_city", "virtual_interlining", etc.
	Return     *skiplaggedFlightReturn `json:"returnFlight,omitempty"`
}

type skiplaggedFlightTerm struct {
	Airport  string `json:"airport"`
	DateTime string `json:"dateTime"`
}

type skiplaggedFlightPrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// skiplaggedFlightReturn is intentionally minimal — the round-trip
// return-leg shape is tracked but not yet promoted into FlightResult
// because trvl's existing Google Flights and AFKLM providers handle
// returns through duplicated FlightResult entries with TripType set
// at the top-level response. We surface only what we can express in
// FlightResult; richer return modeling lands in a follow-up.
type skiplaggedFlightReturn struct {
	Departure skiplaggedFlightTerm `json:"departure"`
	Arrival   skiplaggedFlightTerm `json:"arrival"`
	Duration  string               `json:"duration"`
	Layovers  float64              `json:"layovers"`
}

// ---- MCP session management ----

// skiplaggedInitSession performs the MCP initialize handshake and
// returns the session ID from the Mcp-Session-Id response header.
// The session ID must be included in subsequent tool-call requests
// via the same header on the response.
func skiplaggedInitSession(ctx context.Context) (string, error) {
	if err := skiplaggedLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("skiplagged: rate limiter: %w", err)
	}

	reqBody := skiplaggedRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: skiplaggedInitParams{
			ProtocolVersion: skiplaggedMCPProtocolVersion,
			Capabilities:    map[string]any{},
			ClientInfo: skiplaggedClientInfo{
				Name:    "trvl",
				Version: "1.0",
			},
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("skiplagged: marshal init: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, skiplaggedEndpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return "", fmt.Errorf("skiplagged: build init: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := skiplaggedHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("skiplagged: init HTTP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("skiplagged: init HTTP %d", resp.StatusCode)
	}

	sessionID := resp.Header.Get("Mcp-Session-Id")
	// Some MCP servers omit the header for stateless session models;
	// in that case we proceed without one and let the tool call
	// surface any rejection. Skiplagged returns the header in
	// practice, but we don't hard-require it.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return sessionID, nil
}

// ---- MCP caller ----

// skiplaggedMCPCall sends a single tools/call JSON-RPC request to the
// Skiplagged MCP endpoint using the Streamable HTTP transport.
// Returns the structuredContent payload (preferred) or content[0].text
// fallback as raw JSON.
//
// Retries: a single retry with 1s backoff on HTTP 502 (Cloudflare
// origin overload). Empirically observed during validation as
// recurring rather than one-off, so a transparent single retry
// improves caller success rate without disguising persistent
// outages — the second 502 surfaces as an error.
func skiplaggedMCPCall(ctx context.Context, sessionID, toolName string, args map[string]any) (json.RawMessage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := skiplaggedMCPCallOnce(ctx, sessionID, toolName, args)
		if err == nil {
			return raw, nil
		}
		// Only retry the documented transient envelope; everything
		// else (4xx, timeout, parse errors) bubbles immediately.
		if attempt == 0 && strings.Contains(err.Error(), "bad gateway") {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1 * time.Second):
			}
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("skiplagged: exhausted retries")
}

// skiplaggedMCPCallOnce performs a single tools/call without retry.
func skiplaggedMCPCallOnce(ctx context.Context, sessionID, toolName string, args map[string]any) (json.RawMessage, error) {
	if err := skiplaggedLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("skiplagged: rate limiter: %w", err)
	}

	reqBody := skiplaggedRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: skiplaggedToolCallParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("skiplagged: marshal call: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, skiplaggedEndpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("skiplagged: build call: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := skiplaggedHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skiplagged: HTTP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("skiplagged: rate limited (HTTP 429)")
	}
	if resp.StatusCode == http.StatusBadGateway {
		// Cloudflare 502 from origin overload. Surface as transient
		// so downstream callers can retry rather than treat as
		// permanent provider failure.
		return nil, fmt.Errorf("skiplagged: origin bad gateway (HTTP 502, transient)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skiplagged: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB cap
	if err != nil {
		return nil, fmt.Errorf("skiplagged: read body: %w", err)
	}

	return parseSkiplaggedResponse(body)
}

// parseSkiplaggedResponse extracts the JSON-RPC result from a
// Streamable HTTP response. Handles both bare JSON-RPC envelopes
// and Server-Sent Event framing (`event: message\ndata: { ... }`)
// because Skiplagged's endpoint switches between the two depending
// on negotiated transport.
func parseSkiplaggedResponse(body []byte) (json.RawMessage, error) {
	jsonBytes := stripSSEFraming(body)

	var rpcResp skiplaggedRPCResponse
	if err := json.Unmarshal(jsonBytes, &rpcResp); err != nil {
		return nil, fmt.Errorf("skiplagged: unmarshal: %w", err)
	}
	return extractSkiplaggedContent(rpcResp)
}

// stripSSEFraming converts a `data: { ... }` SSE event into the
// embedded JSON, returning the body unchanged if it is not framed.
func stripSSEFraming(body []byte) []byte {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return body
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if strings.HasPrefix(payload, "{") || strings.HasPrefix(payload, "[") {
				return []byte(payload)
			}
		}
	}
	return body
}

// extractSkiplaggedContent unwraps the result content from a
// JSON-RPC response. Prefers structuredContent (typed JSON
// matching the tool's outputSchema) and falls back to
// content[0].text for text-only response shapes.
func extractSkiplaggedContent(rpc skiplaggedRPCResponse) (json.RawMessage, error) {
	if rpc.Error != nil {
		return nil, fmt.Errorf("skiplagged: RPC error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil {
		return nil, fmt.Errorf("skiplagged: empty result")
	}

	// 1 MB cap on individual content payloads — enough for ~100 flights
	// at the schema's documented field count, but bounded to guard
	// against a misbehaving origin sending a multi-MB blob.
	const maxContentText = 1 << 20

	var toolResult skiplaggedToolResult
	if err := json.Unmarshal(rpc.Result, &toolResult); err == nil {
		// Tool-level error envelope: surface the human-readable
		// message rather than letting downstream JSON parsing fail
		// with a cryptic "invalid character" wrap.
		if toolResult.IsError {
			for _, c := range toolResult.Content {
				if c.Type == "text" && c.Text != "" {
					return nil, fmt.Errorf("skiplagged: tool error: %s", c.Text)
				}
			}
			return nil, fmt.Errorf("skiplagged: tool error (no message)")
		}
		if len(toolResult.StructuredContent) > 0 {
			if len(toolResult.StructuredContent) > maxContentText {
				return nil, fmt.Errorf("skiplagged: structuredContent too large (%d bytes)", len(toolResult.StructuredContent))
			}
			return toolResult.StructuredContent, nil
		}
		for _, c := range toolResult.Content {
			if c.Type == "text" && c.Text != "" {
				if len(c.Text) > maxContentText {
					return nil, fmt.Errorf("skiplagged: content text too large (%d bytes)", len(c.Text))
				}
				return json.RawMessage(c.Text), nil
			}
		}
	}

	return rpc.Result, nil
}

// ---- Public API ----

// SearchSkiplagged searches for flights via the Skiplagged MCP API.
//
// Sequence:
//  1. initialize handshake to obtain a session ID (Mcp-Session-Id header).
//  2. tools/call sk_flights_search with origin, destination, dates, and
//     pax/cabin filters from SearchOptions.
//
// The returned FlightResult slice has Provider="skiplagged" set on
// each entry. Hidden-city flights have "hidden_city" in Warnings;
// virtual-interlining flights have "virtual_interlining" in Warnings.
// Booking URL is set to Skiplagged's deepLink for the candidate.
//
// When skiplaggedEnabled is false (test mode), returns an empty
// result rather than nil so callers can rely on a non-nil pointer.
func SearchSkiplagged(ctx context.Context, origin, destination, departureDate string, opts SearchOptions) (*models.FlightSearchResult, error) {
	if !skiplaggedEnabled {
		return &models.FlightSearchResult{Success: true, TripType: "one-way", Flights: nil}, nil
	}
	if origin == "" || destination == "" {
		return nil, fmt.Errorf("skiplagged: origin and destination required")
	}
	if departureDate == "" {
		return nil, fmt.Errorf("skiplagged: departure date required")
	}

	tripType := "one-way"
	if opts.ReturnDate != "" {
		tripType = "round-trip"
	}

	slog.Debug("skiplagged session init")
	sessionID, err := skiplaggedInitSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("skiplagged init: %w", err)
	}

	args := buildSkiplaggedFlightSearchArgs(origin, destination, departureDate, opts)
	slog.Debug("skiplagged flights search", "origin", origin, "destination", destination,
		"departureDate", departureDate, "returnDate", opts.ReturnDate)

	raw, err := skiplaggedMCPCall(ctx, sessionID, "sk_flights_search", args)
	if err != nil {
		return nil, fmt.Errorf("skiplagged flights: %w", err)
	}

	flights, err := parseSkiplaggedFlights(raw)
	if err != nil {
		return nil, fmt.Errorf("skiplagged parse: %w", err)
	}

	slog.Debug("skiplagged results", "count", len(flights))
	return &models.FlightSearchResult{
		Success:  true,
		Count:    len(flights),
		TripType: tripType,
		Flights:  flights,
	}, nil
}

// buildSkiplaggedFlightSearchArgs maps trvl's SearchOptions into the
// argument shape expected by sk_flights_search. Only fields that
// trvl currently exposes through SearchOptions are passed; the
// caller relies on Skiplagged's documented defaults
// (limit=12, sort=value, fareClass=economy, includeHiddenCity=true,
// includeVirtualInterlining=true, adults=1) for anything we don't
// override.
func buildSkiplaggedFlightSearchArgs(origin, destination, departureDate string, opts SearchOptions) map[string]any {
	args := map[string]any{
		"origin":        origin,
		"destination":   destination,
		"departureDate": departureDate,
	}
	if opts.ReturnDate != "" {
		args["returnDate"] = opts.ReturnDate
	}
	if opts.Adults > 0 {
		args["adults"] = opts.Adults
	}
	switch opts.MaxStops {
	case models.NonStop:
		args["maxStops"] = "none"
	case models.OneStop:
		args["maxStops"] = "one"
	case models.TwoPlusStops:
		args["maxStops"] = "many"
	}
	switch opts.CabinClass {
	case models.PremiumEconomy:
		args["fareClass"] = "premium"
	case models.Business:
		args["fareClass"] = "business"
	case models.First:
		args["fareClass"] = "first"
	}
	switch opts.SortBy {
	case models.SortCheapest:
		args["sort"] = "price"
	case models.SortDuration:
		args["sort"] = "duration"
	}
	if len(opts.Airlines) > 0 {
		args["preferredAirlines"] = opts.Airlines
	}
	return args
}

// parseSkiplaggedFlights converts a sk_flights_search structuredContent
// payload into trvl's FlightResult slice. Hidden-city and virtual-
// interlining flags from the `attributes` array are mapped into
// per-flight Warnings so downstream consumers can filter on them.
func parseSkiplaggedFlights(raw json.RawMessage) ([]models.FlightResult, error) {
	var result skiplaggedFlightsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal flights: %w", err)
	}

	flights := make([]models.FlightResult, 0, len(result.Flights))
	for _, f := range result.Flights {
		fr := models.FlightResult{
			Provider:   "skiplagged",
			Price:      f.Price.Amount,
			Currency:   f.Price.Currency,
			Stops:      int(f.Layovers),
			Duration:   parseSkiplaggedDuration(f.Duration),
			BookingURL: f.DeepLink,
			Legs: []models.FlightLeg{{
				DepartureAirport: models.AirportInfo{Code: f.Departure.Airport},
				ArrivalAirport:   models.AirportInfo{Code: f.Arrival.Airport},
				DepartureTime:    f.Departure.DateTime,
				ArrivalTime:      f.Arrival.DateTime,
				Airline:          f.Airlines,
			}},
		}
		// Map Skiplagged's attribute flags into Warnings so the
		// caller can detect "this is a hidden-city ticket; the
		// passenger must skip the final leg" without re-scraping
		// the deepLink.
		for _, attr := range f.Attributes {
			switch strings.ToLower(attr) {
			case "hidden_city", "hidden-city", "hiddencity":
				fr.SelfConnect = true
				fr.Warnings = append(fr.Warnings, "hidden_city")
			case "virtual_interlining", "virtual-interlining", "virtualinterlining":
				fr.SelfConnect = true
				fr.Warnings = append(fr.Warnings, "virtual_interlining")
			default:
				if attr != "" {
					fr.Warnings = append(fr.Warnings, attr)
				}
			}
		}
		flights = append(flights, fr)
	}
	return flights, nil
}

// parseSkiplaggedDuration parses Skiplagged's free-form duration
// string ("5h 30m", "PT5H30M", "12h", "45m") into total minutes.
// Returns 0 when the format is unrecognised — the field is best-
// effort and downstream code already handles 0-duration flights.
func parseSkiplaggedDuration(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	s = strings.TrimPrefix(s, "pt")

	var total, num int
	for _, ch := range s {
		switch {
		case ch >= '0' && ch <= '9':
			num = num*10 + int(ch-'0')
		case ch == 'h' || ch == 'H':
			total += num * 60
			num = 0
		case ch == 'm' || ch == 'M':
			total += num
			num = 0
		case ch == ' ' || ch == '\t':
			// skip
		default:
			return total
		}
	}
	return total
}
