package hotels

// Trivago hotel provider.
//
// Uses the Trivago MCP server at https://mcp.trivago.com/mcp — a public
// Streamable HTTP MCP endpoint (protocol version 2025-03-26) that requires
// no API key. Each search session begins with an "initialize" handshake
// that returns an Mcp-Session-Id header, then tool calls are made with
// that session ID.
//
// Tool sequence:
//  1. trivago-search-suggestions(query) -> location with ns + id
//  2. trivago-accommodation-search(ns, id, arrival, departure, adults) -> hotel list

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

const trivagoMCPEndpoint = "https://mcp.trivago.com/mcp"

// trivagoMCPProtocolVersion is the MCP protocol version supported by
// Trivago's Streamable HTTP endpoint. The server requires an initialize
// handshake with this version before accepting tool calls.
const trivagoMCPProtocolVersion = "2025-03-26"

// trivagoEnabled controls whether SearchTrivago makes live HTTP requests.
// Set to false in tests that mock the Google Hotels transport to avoid
// unintended real-network calls to mcp.trivago.com.
var trivagoEnabled = true

// trivagoLimiter enforces a 2 req/s rate limit — conservative to avoid 429s.
var trivagoLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)

// trivagoHTTPClient is a dedicated HTTP client for Trivago MCP calls.
var trivagoHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// ---- JSON-RPC request/response types ----

type trivagoRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type trivagoToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type trivagoInitParams struct {
	ProtocolVersion string            `json:"protocolVersion"`
	Capabilities    map[string]any    `json:"capabilities"`
	ClientInfo      trivagoClientInfo `json:"clientInfo"`
}

type trivagoClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type trivagoRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// trivagoToolResult is the outer envelope returned in result.
type trivagoToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

// ---- Suggestions response types ----

type trivagoSuggestionsResult struct {
	Suggestions []trivagoSuggestionEntry `json:"suggestions"`
}

type trivagoSuggestionEntry struct {
	SuggestionType string `json:"suggestion_type"`
	NS             int    `json:"ns"`
	ID             int    `json:"id"`
	Location       string `json:"location"`
	LocationLabel  string `json:"location_label"`
	LocationType   string `json:"location_type"`
}

// trivagoLocationRef holds the ns and id extracted from a suggestion,
// used to call trivago-accommodation-search.
type trivagoLocationRef struct {
	NS int `json:"ns"`
	ID int `json:"id"`
}

// ---- Accommodation response types ----

type trivagoAccomResult struct {
	Accommodations []trivagoAccommodation `json:"accommodations"`
}

type trivagoAccommodation struct {
	AccommodationID   string                   `json:"accommodation_id"`
	AccommodationName string                   `json:"accommodation_name"`
	Address           string                   `json:"address"`
	PostalCode        string                   `json:"postal_code"`
	CountryCity       string                   `json:"country_city"`
	HotelRating       int                      `json:"hotel_rating"`
	ReviewRating      string                   `json:"review_rating"`
	ReviewCount       trivagoFlexInt           `json:"review_count"`
	Currency          string                   `json:"currency"`
	PricePerNight     string                   `json:"price_per_night"`
	PricePerStay      string                   `json:"price_per_stay"`
	Advertisers       string                   `json:"advertisers"`
	Lat               float64                  `json:"latitude"`
	Lon               float64                  `json:"longitude"`
	AccommodationURL  string                   `json:"accommodation_url"`
	BookingURL        string                   `json:"booking_url"`
	TopAmenities      string                   `json:"top_amenities"`
	Distance          string                   `json:"distance"`
	DistToCenter      *trivagoDistanceToCenter `json:"distance_to_city_center,omitempty"`

	// Legacy fields — kept for backward compatibility with older API responses
	// and unit tests that use the original format.
	Name         string               `json:"name"`
	Rating       float64              `json:"rating"`
	Stars        int                  `json:"stars"`
	Price        trivagoPrice         `json:"price"`
	BookingLinks []trivagoBookingLink `json:"bookingLinks"`
}

type trivagoDistanceToCenter struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// trivagoFlexInt unmarshals an integer that the Trivago API may encode either
// as a JSON number (legacy) or as a thousands-separated string such as
// "25,711" (current API). A failed parse yields 0 rather than an error, so a
// single malformed count never aborts the whole accommodation list decode.
type trivagoFlexInt int

func (c *trivagoFlexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*c = 0
		return nil
	}
	// Numeric form: 25711
	if b[0] != '"' {
		var n float64
		if err := json.Unmarshal(b, &n); err != nil {
			*c = 0
			return nil
		}
		*c = trivagoFlexInt(int(n))
		return nil
	}
	// String form: "25,711" or "25711".
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		*c = 0
		return nil
	}
	s = strings.NewReplacer(",", "", " ", "", " ", "").Replace(strings.TrimSpace(s))
	if s == "" {
		*c = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		*c = 0
		return nil
	}
	*c = trivagoFlexInt(n)
	return nil
}

type trivagoPrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type trivagoBookingLink struct {
	URL      string  `json:"url"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Provider string  `json:"provider"`
}

// ---- MCP session management ----

// trivagoInitSession performs the MCP initialize handshake and returns the
// session ID from the Mcp-Session-Id response header. The session ID must
// be included in subsequent tool call requests.
func trivagoInitSession(ctx context.Context) (string, error) {
	if err := trivagoLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("trivago: rate limiter: %w", err)
	}

	reqBody := trivagoRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: trivagoInitParams{
			ProtocolVersion: trivagoMCPProtocolVersion,
			Capabilities:    map[string]any{},
			ClientInfo: trivagoClientInfo{
				Name:    "trvl",
				Version: "1.0",
			},
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("trivago: marshal init request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trivagoMCPEndpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return "", fmt.Errorf("trivago: build init request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := trivagoHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("trivago: init HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("trivago: init HTTP %d", resp.StatusCode)
	}

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		return "", fmt.Errorf("trivago: init response missing Mcp-Session-Id header")
	}

	// Drain and discard the body — we only need the session header.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	return sessionID, nil
}

// ---- MCP caller ----

// trivagoMCPCall sends a single tools/call JSON-RPC request to the Trivago
// MCP endpoint using the Streamable HTTP transport. Returns the raw JSON
// from the result's structuredContent (preferred) or content text field.
func trivagoMCPCall(ctx context.Context, sessionID string, toolName string, args map[string]any) (json.RawMessage, error) {
	if err := trivagoLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("trivago: rate limiter: %w", err)
	}

	reqBody := trivagoRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: trivagoToolCallParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("trivago: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trivagoMCPEndpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("trivago: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := trivagoHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trivago: HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("trivago: rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trivago: HTTP %d", resp.StatusCode)
	}

	// Parse response — plain JSON (Streamable HTTP transport).
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, fmt.Errorf("trivago: read body: %w", err)
	}

	return parseTrivagoResponse(body)
}

// parseTrivagoResponse extracts the JSON-RPC result payload from a plain
// JSON response body. Prefers structuredContent over content[0].text.
func parseTrivagoResponse(body []byte) (json.RawMessage, error) {
	var rpcResp trivagoRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("trivago: unmarshal response: %w", err)
	}
	return extractTrivagoContent(rpcResp)
}

// extractTrivagoContent unwraps the result content from a JSON-RPC response.
// The Trivago MCP result has two data paths:
//  1. structuredContent — typed JSON matching the tool's outputSchema (preferred)
//  2. content[0].text — stringified JSON for text-only clients (fallback)
func extractTrivagoContent(rpc trivagoRPCResponse) (json.RawMessage, error) {
	if rpc.Error != nil {
		return nil, fmt.Errorf("trivago: RPC error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil {
		return nil, fmt.Errorf("trivago: empty result")
	}

	// 512 KB is ample for any reasonable hotel list; guards against a
	// malicious/compromised mcp.trivago.com sending a multi-MB text payload.
	const maxContentText = 512 * 1024

	var toolResult trivagoToolResult
	if err := json.Unmarshal(rpc.Result, &toolResult); err == nil {
		// Prefer structuredContent — typed JSON matching the outputSchema.
		if len(toolResult.StructuredContent) > 0 {
			if len(toolResult.StructuredContent) > maxContentText {
				return nil, fmt.Errorf("trivago: structuredContent too large (%d bytes)", len(toolResult.StructuredContent))
			}
			return toolResult.StructuredContent, nil
		}

		// Fall back to content[0].text.
		for _, c := range toolResult.Content {
			if c.Type == "text" && c.Text != "" {
				if len(c.Text) > maxContentText {
					return nil, fmt.Errorf("trivago: content text too large (%d bytes)", len(c.Text))
				}
				return json.RawMessage(c.Text), nil
			}
		}
	}

	// Return raw result as-is.
	return rpc.Result, nil
}

// ---- Public API ----

// SearchTrivago searches for hotels using the Trivago MCP API.
//
// Trivago's MCP server dropped the old two-step (suggestions → ns/id → search)
// flow. The current `trivago-accommodation-search` tool resolves the location
// itself from a free-text `query`, so trvl now performs:
//  1. initialize — handshake to obtain a session ID.
//  2. tools/list — discover the live accommodation-search tool name (resilient
//     to further upstream renames; falls back to the documented default).
//  3. <accommodation-search>(query, arrival, departure, adults, …) — hotel list.
//
// Each returned HotelResult is tagged with a PriceSource for "trivago".
func SearchTrivago(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !trivagoEnabled {
		return nil, nil
	}
	if opts.CheckIn == "" || opts.CheckOut == "" {
		return nil, fmt.Errorf("trivago: check-in and check-out dates are required")
	}
	if opts.Guests <= 0 {
		opts.Guests = 2
	}
	currency := opts.Currency
	if currency == "" {
		currency = "USD"
	}

	// Step 0: Initialize MCP session.
	slog.Debug("trivago session init")
	sessionID, err := trivagoInitSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("trivago init: %w", err)
	}

	// Step 1: Discover the accommodation-search tool from tools/list rather
	// than hard-coding it. If discovery fails (network/parse), fall back to
	// the documented default so a transient list error doesn't take Trivago
	// down. If the server genuinely no longer exposes any query-based search
	// tool, return a clear, actionable error.
	toolName := trivagoDefaultSearchTool
	if names, lerr := trivagoListToolNames(ctx, sessionID); lerr == nil {
		selected, serr := selectTrivagoSearchTool(names)
		if serr != nil {
			return nil, fmt.Errorf("trivago: %w", serr)
		}
		toolName = selected
	} else {
		slog.Debug("trivago tools/list failed, using default tool", "tool", toolName, "error", lerr)
	}

	// Step 2: Search accommodations. The current tool resolves the location
	// from a free-text query, so no separate suggestions lookup is required.
	slog.Debug("trivago accommodation search", "location", location, "tool", toolName,
		"arrival", opts.CheckIn, "departure", opts.CheckOut, "guests", opts.Guests)
	accomArgs := map[string]any{
		"query":     location,
		"arrival":   opts.CheckIn,
		"departure": opts.CheckOut,
		"adults":    opts.Guests,
		"rooms":     1,
		"currency":  strings.ToUpper(currency),
	}
	if len(opts.ChildrenAges) > 0 {
		accomArgs["children"] = len(opts.ChildrenAges)
		ages := make([]string, 0, len(opts.ChildrenAges))
		for _, a := range opts.ChildrenAges {
			ages = append(ages, strconv.Itoa(a))
		}
		accomArgs["children_ages"] = strings.Join(ages, "-")
	}
	accomRaw, err := trivagoMCPCall(ctx, sessionID, toolName, accomArgs)
	if err != nil {
		return nil, fmt.Errorf("trivago accommodation search: %w", err)
	}

	hotels, err := parseTrivagoAccommodations(accomRaw, currency)
	if err != nil {
		return nil, fmt.Errorf("trivago accommodation parse: %w", err)
	}

	slog.Debug("trivago results", "location", location, "count", len(hotels))
	return hotels, nil
}

// trivagoDefaultSearchTool is the documented query-based accommodation search
// tool. Used as a fallback when tools/list discovery is unavailable.
const trivagoDefaultSearchTool = "trivago-accommodation-search"

// trivagoToolsListResult is the minimal shape of a tools/list response.
type trivagoToolsListResult struct {
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
}

// trivagoListToolNames calls tools/list on the Trivago MCP endpoint and returns
// the advertised tool names.
func trivagoListToolNames(ctx context.Context, sessionID string) ([]string, error) {
	if err := trivagoLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("trivago: rate limiter: %w", err)
	}

	reqBody := trivagoRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
		Params:  map[string]any{},
	}
	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("trivago: marshal tools/list: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trivagoMCPEndpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, fmt.Errorf("trivago: build tools/list request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := trivagoHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trivago: tools/list HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trivago: tools/list HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("trivago: read tools/list body: %w", err)
	}
	return parseTrivagoToolNames(body)
}

// parseTrivagoToolNames extracts tool names from a tools/list JSON-RPC body.
func parseTrivagoToolNames(body []byte) ([]string, error) {
	var rpcResp trivagoRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("trivago: unmarshal tools/list: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("trivago: tools/list RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	var result trivagoToolsListResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, fmt.Errorf("trivago: decode tools/list result: %w", err)
	}
	names := make([]string, 0, len(result.Tools))
	for _, t := range result.Tools {
		if t.Name != "" {
			names = append(names, t.Name)
		}
	}
	return names, nil
}

// selectTrivagoSearchTool picks the best query-based accommodation-search tool
// from the advertised tool names. It prefers the exact documented name, then
// any accommodation-search tool that is not the coordinate/radius variant.
// Returns an error if no suitable tool is present so the caller can surface an
// actionable message instead of calling a non-existent tool.
func selectTrivagoSearchTool(names []string) (string, error) {
	for _, n := range names {
		if n == trivagoDefaultSearchTool {
			return n, nil
		}
	}
	for _, n := range names {
		lower := strings.ToLower(n)
		if strings.Contains(lower, "accommodation-search") && !strings.Contains(lower, "radius") {
			return n, nil
		}
	}
	// Fall back to any accommodation search tool (including radius) as a last
	// resort before failing outright.
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), "accommodation") && strings.Contains(strings.ToLower(n), "search") {
			return n, nil
		}
	}
	return "", fmt.Errorf("no accommodation-search tool found in Trivago tools/list (advertised: %s)", strings.Join(names, ", "))
}
