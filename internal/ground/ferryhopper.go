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

// ferryhopperMCPURL is the Ferryhopper MCP endpoint. It is a var (not a const)
// so tests can point the full SearchFerryhopper path at a local httptest server.
var ferryhopperMCPURL = "https://mcp.ferryhopper.com/mcp"

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
		// StructuredContent is the current Ferryhopper MCP response shape: the
		// trip data lives here (not embedded as a JSON string in content[0].text,
		// which is now only a human-readable summary like "Found 8 trips ...").
		StructuredContent *ferryhopperStructuredContent `json:"structuredContent"`
		IsError           bool                          `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ferryhopperStructuredContent is the typed trip payload in result.structuredContent.
// The current API key is foundDirectItinerariesForTrip; Itineraries is kept as a
// defensive fallback for the older/legacy shape.
type ferryhopperStructuredContent struct {
	FoundDirectItinerariesForTrip []ferryhopperItinerary `json:"foundDirectItinerariesForTrip"`
	Itineraries                   []ferryhopperItinerary `json:"itineraries"`
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

// ferryhopperSegment is a single ferry leg within an itinerary. It carries both
// the legacy and current field names so a fixture/response in either shape parses:
//   - operator (legacy)  / ownerCompany.name (current)
//   - vesselName (legacy) / vessel.name (current)
type ferryhopperSegment struct {
	DeparturePort     ferryhopperPort            `json:"departurePort"`
	ArrivalPort       ferryhopperPort            `json:"arrivalPort"`
	DepartureDateTime string                     `json:"departureDateTime"` // ISO 8601
	ArrivalDateTime   string                     `json:"arrivalDateTime"`   // ISO 8601
	Operator          string                     `json:"operator"`          // legacy
	OwnerCompany      ferryhopperCompany         `json:"ownerCompany"`      // current
	VesselName        string                     `json:"vesselName"`        // legacy
	Vessel            ferryhopperVessel          `json:"vessel"`            // current
	Accommodations    []ferryhopperAccommodation `json:"accommodations"`
}

// operatorName returns the carrier name, preferring the legacy operator field and
// falling back to the current ownerCompany.name.
func (s ferryhopperSegment) operatorName() string {
	if strings.TrimSpace(s.Operator) != "" {
		return s.Operator
	}
	return s.OwnerCompany.Name
}

// ferryhopperCompany is the current carrier object (result...ownerCompany).
type ferryhopperCompany struct {
	Name string `json:"name"`
}

// ferryhopperVessel is the current vessel object (result...vessel).
type ferryhopperVessel struct {
	Name string `json:"name"`
}

// ferryhopperPort holds port name details.
type ferryhopperPort struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ferryhopperAccommodation holds a fare class with a price in cents. It supports
// both the legacy price field and the current expectedPrice.totalPriceInCents.
type ferryhopperAccommodation struct {
	Name          string `json:"name"`
	PriceCents    int    `json:"price"` // legacy: EUR cents
	ExpectedPrice struct {
		TotalPriceInCents int `json:"totalPriceInCents"` // current: EUR cents
	} `json:"expectedPrice"`
}

// cents returns the fare in EUR cents, preferring the legacy price field and
// falling back to the current expectedPrice.totalPriceInCents.
func (a ferryhopperAccommodation) cents() int {
	if a.PriceCents > 0 {
		return a.PriceCents
	}
	return a.ExpectedPrice.TotalPriceInCents
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

		// Accept frames that carry a result (content or structuredContent) or error.
		if rpcResult.Result.Content != nil || rpcResult.Result.StructuredContent != nil || rpcResult.Error != nil {
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
		c := a.cents()
		if c <= 0 {
			continue
		}
		price := float64(c) / 100.0
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
		// MCP-spec tool error: content[0].text carries the human-readable
		// reason. Echo it (bounded) so the caller sees what Ferryhopper
		// actually said instead of an opaque "tool returned error".
		if c := rpcResult.Result.Content; len(c) > 0 {
			if msg := strings.TrimSpace(c[0].Text); msg != "" {
				if len(msg) > 256 {
					msg = msg[:256]
				}
				return nil, fmt.Errorf("ferryhopper: tool error: %s", msg)
			}
		}
		return nil, fmt.Errorf("ferryhopper: tool returned error")
	}

	// Resolve the itinerary list. The current API returns trips in
	// result.structuredContent.foundDirectItinerariesForTrip; content[0].text is
	// now only a human-readable summary. Fall back to the legacy shape (trip JSON
	// embedded as a string in content[0].text -> {"itineraries":[...]}).
	itineraries, err := ferryhopperResolveItineraries(rpcResult)
	if err != nil {
		return nil, err
	}
	if len(itineraries) == 0 {
		slog.Debug("ferryhopper: no itineraries")
		return nil, nil
	}

	routes := ferryhopperBuildRoutes(itineraries)
	slog.Debug("ferryhopper results", "routes", len(routes))
	return routes, nil
}

// ferryhopperResolveItineraries extracts the itinerary list from a JSON-RPC
// result, preferring the current structuredContent shape and falling back to the
// legacy content[0].text JSON string. A plain-text content (e.g. "Found no
// itineraries") resolves to an empty list with no error.
func ferryhopperResolveItineraries(rpcResult *ferryhopperRPCResult) ([]ferryhopperItinerary, error) {
	if sc := rpcResult.Result.StructuredContent; sc != nil {
		if len(sc.FoundDirectItinerariesForTrip) > 0 {
			return sc.FoundDirectItinerariesForTrip, nil
		}
		if len(sc.Itineraries) > 0 {
			return sc.Itineraries, nil
		}
		// structuredContent present but empty -> a valid "no results".
		return nil, nil
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

	// content[0].text is only JSON in the legacy shape. The current API puts a
	// plain summary here ("Found 8 trips ...") with the data in structuredContent,
	// so a non-JSON text with no structuredContent is a no-results signal.
	if len(contentText) == 0 || (contentText[0] != '{' && contentText[0] != '[') {
		slog.Debug("ferryhopper: content is not JSON and no structuredContent, treating as no results",
			"preview", contentText[:min(len(contentText), 80)])
		return nil, nil
	}

	var tripResult ferryhopperTripResult
	if err := json.Unmarshal([]byte(contentText), &tripResult); err != nil {
		return nil, fmt.Errorf("ferryhopper: decode trip result: %w", err)
	}
	return tripResult.Itineraries, nil
}

// ferryhopperBuildRoutes maps Ferryhopper itineraries to GroundRoutes. It is pure
// (no network) so it is exercised by offline fixture tests.
func ferryhopperBuildRoutes(itineraries []ferryhopperItinerary) []models.GroundRoute {
	routes := make([]models.GroundRoute, 0, len(itineraries))
	for _, itin := range itineraries {
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

		// Determine the provider name from the first segment's carrier.
		provider := "ferryhopper"
		if op := first.operatorName(); op != "" {
			provider = strings.ToLower(op)
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
	return routes
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
