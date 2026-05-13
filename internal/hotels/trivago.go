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
	"net/url"
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
	ReviewCount       int                      `json:"review_count"`
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
// It performs three sequential HTTP calls:
//  1. initialize — handshake to obtain a session ID.
//  2. trivago-search-suggestions(query) — resolve location to ns + id.
//  3. trivago-accommodation-search(ns, id, arrival, departure, …) — hotel list.
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

	// Step 1: Resolve location to a Trivago ns + id.
	slog.Debug("trivago search suggestions", "location", location)
	suggRaw, err := trivagoMCPCall(ctx, sessionID, "trivago-search-suggestions", map[string]any{
		"query": location,
	})
	if err != nil {
		return nil, fmt.Errorf("trivago suggestions: %w", err)
	}

	locRef, err := parseTrivagoSuggestions(suggRaw)
	if err != nil {
		return nil, fmt.Errorf("trivago suggestions parse: %w", err)
	}

	// Step 2: Search accommodations.
	slog.Debug("trivago accommodation search", "location", location, "ns", locRef.NS, "id", locRef.ID,
		"arrival", opts.CheckIn, "departure", opts.CheckOut, "guests", opts.Guests)
	accomArgs := map[string]any{
		"ns":        locRef.NS,
		"id":        locRef.ID,
		"arrival":   opts.CheckIn,
		"departure": opts.CheckOut,
		"adults":    opts.Guests,
		"rooms":     1,
	}
	accomRaw, err := trivagoMCPCall(ctx, sessionID, "trivago-accommodation-search", accomArgs)
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

// parseTrivagoSuggestions extracts the first location's ns and id from a
// trivago-search-suggestions response.
func parseTrivagoSuggestions(raw json.RawMessage) (trivagoLocationRef, error) {
	// Try structured suggestions wrapper (new API format).
	var result trivagoSuggestionsResult
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Suggestions) > 0 {
		s := result.Suggestions[0]
		if s.NS != 0 || s.ID != 0 {
			return trivagoLocationRef{NS: s.NS, ID: s.ID}, nil
		}
	}

	// Some responses embed suggestions in an outer object with a different key.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return trivagoLocationRef{}, fmt.Errorf("unexpected suggestions format")
	}

	// Walk possible key names.
	for _, key := range []string{"suggestions", "results", "data", "items"} {
		if v, ok := outer[key]; ok {
			var arr []trivagoSuggestionEntry
			if err := json.Unmarshal(v, &arr); err == nil && len(arr) > 0 {
				s := arr[0]
				if s.NS != 0 || s.ID != 0 {
					return trivagoLocationRef{NS: s.NS, ID: s.ID}, nil
				}
			}
			// Try as raw array and look for ns/id keys.
			var rawArr []map[string]json.RawMessage
			if err := json.Unmarshal(v, &rawArr); err == nil && len(rawArr) > 0 {
				ref, err := extractNSIDFromMap(rawArr[0])
				if err == nil {
					return ref, nil
				}
			}
		}
	}

	return trivagoLocationRef{}, fmt.Errorf("no location ns/id found in suggestions")
}

// extractNSIDFromMap attempts to pull ns and id from a generic JSON object.
func extractNSIDFromMap(m map[string]json.RawMessage) (trivagoLocationRef, error) {
	var ref trivagoLocationRef
	var found bool

	for _, nsKey := range []string{"ns", "NS"} {
		if v, ok := m[nsKey]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil {
				ref.NS = n
				found = true
				break
			}
		}
	}
	for _, idKey := range []string{"id", "ID"} {
		if v, ok := m[idKey]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil {
				ref.ID = n
				found = true
				break
			}
		}
	}

	if !found {
		return trivagoLocationRef{}, fmt.Errorf("ns/id not found")
	}
	return ref, nil
}

// parseTrivagoAccommodations maps a trivago-accommodation-search response to
// a slice of HotelResult values, each tagged with a "trivago" PriceSource.
func parseTrivagoAccommodations(raw json.RawMessage, currency string) ([]models.HotelResult, error) {
	// Try the typed struct first.
	var result trivagoAccomResult
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Accommodations) > 0 {
		return mapTrivagoAccommodations(result.Accommodations, currency), nil
	}

	// Accommodations might be at a different key.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, fmt.Errorf("unexpected accommodations format")
	}

	for _, key := range []string{"accommodations", "hotels", "results", "data"} {
		if v, ok := outer[key]; ok {
			var accoms []trivagoAccommodation
			if err := json.Unmarshal(v, &accoms); err == nil {
				return mapTrivagoAccommodations(accoms, currency), nil
			}
		}
	}

	// Return empty list if we genuinely got no hotels (e.g. obscure location).
	return nil, nil
}

// sanitizeBookingURL returns rawURL if it has an http or https scheme, or ""
// if it is empty, has an unexpected scheme (e.g. javascript:, data:), or is
// not a valid URL. This prevents a malicious MCP response from injecting
// non-HTTP URLs into booking links that a client might follow.
func sanitizeBookingURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	return rawURL
}

// trivagoParsePrice extracts a numeric value from a formatted price string
// like "€211" or "$150" or "£89". Delegates to the shared parsePriceString
// in parse.go, discarding the currency return (which is handled separately).
func trivagoParsePrice(s string) float64 {
	amount, _ := parsePriceString(s)
	return amount
}

// parseRatingString converts a rating string like "8.4" to a float on a
// 0-10 scale, then normalizes to 0-5 (dividing by 2) for consistency with
// the HotelResult model.
func parseRatingString(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	// Trivago ratings are on a 0-10 scale; normalize to 0-5.
	if v > 5 {
		v = v / 2
	}
	return v
}

// mapTrivagoAccommodations converts trivago API accommodation records to the
// canonical HotelResult model. Handles both the new API format (accommodation_name,
// price_per_night, review_rating) and the legacy format (name, price.amount,
// rating, bookingLinks).
func mapTrivagoAccommodations(accoms []trivagoAccommodation, defaultCurrency string) []models.HotelResult {
	results := make([]models.HotelResult, 0, len(accoms))

	for _, a := range accoms {
		// Determine hotel name — new API uses accommodation_name, legacy uses name.
		hotelName := a.AccommodationName
		if hotelName == "" {
			hotelName = a.Name
		}
		if hotelName == "" {
			continue
		}

		// Determine price — new API uses formatted price_per_night string,
		// legacy uses price.amount and bookingLinks.
		var price float64
		cur := defaultCurrency

		if a.PricePerNight != "" {
			// New API format: parse formatted string like "€211".
			price = trivagoParsePrice(a.PricePerNight)
			if a.Currency != "" {
				cur = a.Currency
			}
		} else {
			// Legacy format: numeric price object + booking links.
			price = a.Price.Amount
			if a.Price.Currency != "" {
				cur = a.Price.Currency
			}
			for _, link := range a.BookingLinks {
				if link.Price <= 0 {
					continue
				}
				if price == 0 || link.Price < price {
					price = link.Price
					if link.Currency != "" {
						cur = link.Currency
					}
				}
			}
		}

		// Determine rating.
		rating := a.Rating
		if rating == 0 && a.ReviewRating != "" {
			rating = parseRatingString(a.ReviewRating)
		}

		// Determine stars.
		stars := a.Stars
		if stars == 0 {
			stars = a.HotelRating
		}

		// Determine review count.
		reviewCount := a.ReviewCount

		// Determine booking URL.
		bookingURL := sanitizeBookingURL(a.BookingURL)
		if bookingURL == "" {
			bookingURL = sanitizeBookingURL(a.AccommodationURL)
		}
		// Legacy: check bookingLinks for URL.
		if bookingURL == "" {
			for _, link := range a.BookingLinks {
				if u := sanitizeBookingURL(link.URL); u != "" {
					bookingURL = u
					break
				}
			}
		}

		// Determine address.
		address := a.Address
		if address == "" && a.CountryCity != "" {
			address = a.CountryCity
		}

		h := models.HotelResult{
			Name:        hotelName,
			Rating:      rating,
			ReviewCount: reviewCount,
			Stars:       stars,
			Price:       price,
			Currency:    strings.ToUpper(cur),
			Address:     address,
			Lat:         a.Lat,
			Lon:         a.Lon,
			BookingURL:  bookingURL,
			Sources: []models.PriceSource{{
				Provider:   "trivago",
				Price:      price,
				Currency:   strings.ToUpper(cur),
				BookingURL: bookingURL,
			}},
		}

		results = append(results, h)
	}

	return results
}
