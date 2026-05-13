package ground

// Ferryhopper ferry provider.
//
// Ferryhopper (ferryhopper.com) is a Greek ferry aggregator covering routes
// primarily in the Aegean, Ionian, and Adriatic seas. It aggregates operators
// such as SEAJETS, Blue Star Ferries, Hellenic Seaways, Minoan Lines,
// Anek Lines, and others.
//
// API: Ferryhopper exposes a public MCP (Model Context Protocol) server at
// https://mcp.ferryhopper.com/mcp using Streamable HTTP transport with
// JSON-RPC 2.0 framing. No API key is required.
//
// The server responds with Server-Sent Events (SSE); each event carries a
// JSON-RPC result envelope. The trip data lives in:
//   result.content[0].text  (a JSON string)
//
// Prices are denominated in EUR cents and must be divided by 100.
//
// Tools used:
//   search_trips(departureLocation, arrivalLocation, date) — search itineraries

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ferryhopperMCPURL is the Ferryhopper MCP endpoint.
const ferryhopperMCPURL = "https://mcp.ferryhopper.com/mcp"

// ferryhopperLimiter: 2 req/s — conservative to avoid overloading the free MCP endpoint.
var ferryhopperLimiter = newProviderLimiter(500 * time.Millisecond)

// ferryhopperClient is a shared HTTP client for Ferryhopper MCP calls.
var ferryhopperClient = &http.Client{
	Timeout: 30 * time.Second,
}

// ferryhopperRPCRequest is a JSON-RPC 2.0 tools/call request body.
type ferryhopperRPCRequest struct {
	JSONRPC string                      `json:"jsonrpc"`
	ID      int                         `json:"id"`
	Method  string                      `json:"method"`
	Params  ferryhopperRPCRequestParams `json:"params"`
}

type ferryhopperRPCRequestParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ferryhopperRPCResult is the JSON-RPC 2.0 result envelope from the SSE stream.
type ferryhopperRPCResult struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ferryhopperTripResult is the top-level structure in result.content[0].text.
type ferryhopperTripResult struct {
	Itineraries []ferryhopperItinerary `json:"itineraries"`
}

// ferryhopperItinerary is one complete trip option (possibly multi-segment).
type ferryhopperItinerary struct {
	Segments []ferryhopperSegment `json:"segments"`
	DeepLink string               `json:"deepLink"`
}

// ferryhopperSegment is a single ferry leg within an itinerary.
type ferryhopperSegment struct {
	DeparturePort     ferryhopperPort            `json:"departurePort"`
	ArrivalPort       ferryhopperPort            `json:"arrivalPort"`
	DepartureDateTime string                     `json:"departureDateTime"` // ISO 8601
	ArrivalDateTime   string                     `json:"arrivalDateTime"`   // ISO 8601
	Operator          string                     `json:"operator"`
	VesselName        string                     `json:"vesselName"`
	Accommodations    []ferryhopperAccommodation `json:"accommodations"`
}

// ferryhopperPort holds port name details.
type ferryhopperPort struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ferryhopperAccommodation holds a fare class with a price in cents.
type ferryhopperAccommodation struct {
	Name       string `json:"name"`
	PriceCents int    `json:"price"` // EUR cents
}

// ferryhopperCallSearchTrips sends a search_trips JSON-RPC call to the
// Ferryhopper MCP endpoint and returns the raw JSON-RPC result envelope.
// The endpoint responds with an SSE stream; this function reads all events
// and returns the last complete JSON-RPC result.
func ferryhopperCallSearchTrips(ctx context.Context, from, to, date string) (*ferryhopperRPCResult, error) {
	reqBody := ferryhopperRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: ferryhopperRPCRequestParams{
			Name: "search_trips",
			Arguments: map[string]interface{}{
				"departureLocation": from,
				"arrivalLocation":   to,
				"date":              date,
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ferryhopper: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ferryhopperMCPURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("ferryhopper: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := ferryhopperClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ferryhopper: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ferryhopper: HTTP %d: %s", resp.StatusCode, body)
	}

	return ferryhopperParseSSE(resp.Body)
}

// ferryhopperParseSSE reads an SSE stream and returns the last JSON-RPC result
// found in a data: line. The Ferryhopper MCP server may emit multiple events;
// the final one containing a result is the authoritative response.
func ferryhopperParseSSE(r io.Reader) (*ferryhopperRPCResult, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, 1024*1024)) // 1 MB limit
	scanner.Buffer(make([]byte, 64*1024), 256*1024)

	var lastResult *ferryhopperRPCResult

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		var rpcResult ferryhopperRPCResult
		if err := json.Unmarshal([]byte(data), &rpcResult); err != nil {
			slog.Debug("ferryhopper: skip unparseable SSE data", "err", err)
			continue
		}

		// Accept frames that carry either result or error.
		if rpcResult.Result.Content != nil || rpcResult.Error != nil {
			lastResult = &rpcResult
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ferryhopper: read SSE: %w", err)
	}

	if lastResult == nil {
		return nil, fmt.Errorf("ferryhopper: no JSON-RPC result in SSE stream")
	}

	return lastResult, nil
}

// ferryhopperCheapestPrice returns the cheapest accommodation price in EUR
// from a segment's accommodation list. Returns 0 if there are no priced fares.
func ferryhopperCheapestPrice(accommodations []ferryhopperAccommodation) float64 {
	var cheapest float64
	for _, a := range accommodations {
		if a.PriceCents <= 0 {
			continue
		}
		price := float64(a.PriceCents) / 100.0
		if cheapest == 0 || price < cheapest {
			cheapest = price
		}
	}
	return cheapest
}

// SearchFerryhopper searches Ferryhopper for ferry connections between two
// locations on a given date. It accepts free-form location names (e.g.
// "Athens", "Santorini", "Piraeus") which are passed directly to the API.
func SearchFerryhopper(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("ferryhopper: invalid date %q: %w", date, err)
	}

	if err := ferryhopperLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("ferryhopper: rate limiter: %w", err)
	}

	slog.Debug("ferryhopper search", "from", from, "to", to, "date", date)

	rpcResult, err := ferryhopperCallSearchTrips(ctx, from, to, date)
	if err != nil {
		return nil, err
	}

	if rpcResult.Error != nil {
		return nil, fmt.Errorf("ferryhopper: RPC error %d: %s", rpcResult.Error.Code, rpcResult.Error.Message)
	}

	if rpcResult.Result.IsError {
		return nil, fmt.Errorf("ferryhopper: tool returned error")
	}

	if len(rpcResult.Result.Content) == 0 {
		slog.Debug("ferryhopper: empty content")
		return nil, nil
	}

	// 512 KB is ample for any realistic ferry schedule; guards against a
	// malicious/compromised mcp.ferryhopper.com inflating the text payload
	// beyond the outer 1 MB HTTP body cap via nested JSON encoding.
	const maxContentText = 512 * 1024
	contentText := rpcResult.Result.Content[0].Text
	if len(contentText) > maxContentText {
		return nil, fmt.Errorf("ferryhopper: content text too large (%d bytes)", len(contentText))
	}

	// The MCP server sometimes returns plain text instead of JSON (e.g.
	// "Ferry routes not available" or "Found no itineraries"). Only attempt
	// JSON parse when the content looks like a JSON object or array.
	if len(contentText) == 0 || (contentText[0] != '{' && contentText[0] != '[') {
		slog.Debug("ferryhopper: content is not JSON, treating as no results",
			"preview", contentText[:min(len(contentText), 80)])
		return nil, nil
	}

	var tripResult ferryhopperTripResult
	if err := json.Unmarshal([]byte(contentText), &tripResult); err != nil {
		return nil, fmt.Errorf("ferryhopper: decode trip result: %w", err)
	}

	routes := make([]models.GroundRoute, 0, len(tripResult.Itineraries))
	for _, itin := range tripResult.Itineraries {
		if len(itin.Segments) == 0 {
			continue
		}

		first := itin.Segments[0]
		last := itin.Segments[len(itin.Segments)-1]

		depTime := first.DepartureDateTime
		arrTime := last.ArrivalDateTime
		duration := computeDurationMinutes(depTime, arrTime)

		// Use the cheapest fare across all segments (worst-case: add segment prices).
		var totalPrice float64
		for _, seg := range itin.Segments {
			totalPrice += ferryhopperCheapestPrice(seg.Accommodations)
		}

		// Determine the provider name from the first segment's operator.
		provider := "ferryhopper"
		if first.Operator != "" {
			provider = strings.ToLower(first.Operator)
		}

		route := models.GroundRoute{
			Provider: provider,
			Type:     "ferry",
			Price:    totalPrice,
			Currency: "EUR", // Ferryhopper always returns EUR
			Duration: duration,
			Departure: models.GroundStop{
				City:    first.DeparturePort.Name,
				Station: first.DeparturePort.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    last.ArrivalPort.Name,
				Station: last.ArrivalPort.Name,
				Time:    arrTime,
			},
			Transfers:  len(itin.Segments) - 1,
			BookingURL: ferryhopperSanitizeURL(itin.DeepLink),
		}

		routes = append(routes, route)
	}

	slog.Debug("ferryhopper results", "routes", len(routes))
	return routes, nil
}

// ferryhopperSanitizeURL returns rawURL if it has an http or https scheme,
// or "" otherwise. Prevents a malicious MCP response from injecting
// javascript:, data:, or other non-HTTP URLs into booking deep links.
func ferryhopperSanitizeURL(rawURL string) string {
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
