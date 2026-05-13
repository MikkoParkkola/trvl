package flights

import (
	"bufio"
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

const (
	kiwiMCPEndpoint        = "https://mcp.kiwi.com"
	kiwiProtocolVersion    = "2025-06-18"
	kiwiSelfConnectWarning = "Self-connect: separate tickets may require re-checking bags and missed connections are your responsibility."
)

var (
	kiwiLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	kiwiClient  = &http.Client{Timeout: 30 * time.Second}
)

type kiwiRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type kiwiRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type kiwiInitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type kiwiToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type kiwiToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

type kiwiDateTime struct {
	UTC   string `json:"utc"`
	Local string `json:"local"`
}

type kiwiLayover struct {
	At        string       `json:"at"`
	City      string       `json:"city"`
	CityCode  string       `json:"cityCode"`
	Arrival   kiwiDateTime `json:"arrival"`
	Departure kiwiDateTime `json:"departure"`
}

type kiwiItinerary struct {
	FlyFrom                string        `json:"flyFrom"`
	FlyTo                  string        `json:"flyTo"`
	CityFrom               string        `json:"cityFrom"`
	CityTo                 string        `json:"cityTo"`
	Departure              kiwiDateTime  `json:"departure"`
	Arrival                kiwiDateTime  `json:"arrival"`
	DurationInSeconds      int           `json:"durationInSeconds"`
	TotalDurationInSeconds int           `json:"totalDurationInSeconds"`
	Price                  float64       `json:"price"`
	DeepLink               string        `json:"deepLink"`
	Currency               string        `json:"currency"`
	Layovers               []kiwiLayover `json:"layovers"`
}

func SearchKiwiFlights(ctx context.Context, origin, destination, date, currency string, opts SearchOptions) ([]models.FlightResult, error) {
	departureDate, err := kiwiDate(date)
	if err != nil {
		return nil, fmt.Errorf("kiwi: format departure date: %w", err)
	}

	sessionID, err := kiwiInitializeSession(ctx)
	if err != nil {
		return nil, err
	}

	if currency == "" {
		currency = "EUR"
	}

	args := map[string]any{
		"flyFrom":       origin,
		"flyTo":         destination,
		"departureDate": departureDate,
		"passengers": map[string]int{
			"adults": opts.Adults,
		},
		"sort":   kiwiSort(opts.SortBy),
		"curr":   currency,
		"locale": "en",
	}
	if cabin := kiwiCabinClass(opts.CabinClass); cabin != "" {
		args["cabinClass"] = cabin
	}

	rpcResp, err := kiwiRPC(ctx, sessionID, kiwiRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: kiwiToolParams{
			Name:      "search-flight",
			Arguments: args,
		},
	})
	if err != nil {
		return nil, err
	}

	payload, err := extractKiwiContent(rpcResp)
	if err != nil {
		return nil, err
	}

	var itineraries []kiwiItinerary
	if err := json.Unmarshal(payload, &itineraries); err != nil {
		return nil, fmt.Errorf("kiwi: decode search results: %w", err)
	}

	results := make([]models.FlightResult, 0, len(itineraries))
	for _, itinerary := range itineraries {
		results = append(results, mapKiwiItinerary(itinerary, currency))
	}
	return results, nil
}

func kiwiInitializeSession(ctx context.Context) (string, error) {
	params := kiwiInitializeParams{
		ProtocolVersion: kiwiProtocolVersion,
		Capabilities:    map[string]any{},
	}
	params.ClientInfo.Name = "trvl"
	params.ClientInfo.Version = "1.0.0"

	headers, rpcResp, err := kiwiRPCWithHeaders(ctx, "", kiwiRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  params,
	})
	if err != nil {
		return "", err
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("kiwi: initialize RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	sessionID := strings.TrimSpace(headers.Get("mcp-session-id"))
	if sessionID == "" {
		return "", fmt.Errorf("kiwi: initialize response missing mcp-session-id")
	}
	return sessionID, nil
}

func kiwiRPC(ctx context.Context, sessionID string, payload kiwiRPCRequest) (kiwiRPCResponse, error) {
	_, rpcResp, err := kiwiRPCWithHeaders(ctx, sessionID, payload)
	return rpcResp, err
}

func kiwiRPCWithHeaders(ctx context.Context, sessionID string, payload kiwiRPCRequest) (http.Header, kiwiRPCResponse, error) {
	if err := kiwiLimiter.Wait(ctx); err != nil {
		return nil, kiwiRPCResponse{}, fmt.Errorf("kiwi: rate limiter: %w", err)
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, kiwiRPCResponse{}, fmt.Errorf("kiwi: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kiwiMCPEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, kiwiRPCResponse{}, fmt.Errorf("kiwi: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("mcp-session-id", sessionID)
	}

	resp, err := kiwiClient.Do(req)
	if err != nil {
		return nil, kiwiRPCResponse{}, fmt.Errorf("kiwi: HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, kiwiRPCResponse{}, fmt.Errorf("kiwi: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return resp.Header, kiwiRPCResponse{}, fmt.Errorf("kiwi: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	rpcResp, err := parseKiwiRPCResponse(body)
	if err != nil {
		return resp.Header, kiwiRPCResponse{}, err
	}
	if rpcResp.Error != nil {
		return resp.Header, kiwiRPCResponse{}, fmt.Errorf("kiwi: RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return resp.Header, rpcResp, nil
}

func parseKiwiRPCResponse(body []byte) (kiwiRPCResponse, error) {
	var rpcResp kiwiRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err == nil && (rpcResp.Result != nil || rpcResp.Error != nil) {
		return rpcResp, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), 256*1024)

	var last kiwiRPCResponse
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var rpc kiwiRPCResponse
		if err := json.Unmarshal([]byte(data), &rpc); err != nil {
			slog.Debug("kiwi: skipping unparseable SSE frame", "error", err)
			continue
		}
		if rpc.Result != nil || rpc.Error != nil {
			last = rpc
			found = true
		}
	}
	if err := scanner.Err(); err != nil {
		return kiwiRPCResponse{}, fmt.Errorf("kiwi: read SSE: %w", err)
	}
	if !found {
		return kiwiRPCResponse{}, fmt.Errorf("kiwi: no usable JSON-RPC response found")
	}
	return last, nil
}

func extractKiwiContent(rpc kiwiRPCResponse) (json.RawMessage, error) {
	if rpc.Error != nil {
		return nil, fmt.Errorf("kiwi: RPC error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if rpc.Result == nil {
		return nil, fmt.Errorf("kiwi: empty result")
	}

	var toolResult kiwiToolResult
	if err := json.Unmarshal(rpc.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("kiwi: decode tool envelope: %w", err)
	}
	if toolResult.IsError {
		return nil, fmt.Errorf("kiwi: tool returned error")
	}

	const maxContentText = 512 * 1024
	for _, content := range toolResult.Content {
		if content.Type != "text" || content.Text == "" {
			continue
		}
		if len(content.Text) > maxContentText {
			return nil, fmt.Errorf("kiwi: content text too large (%d bytes)", len(content.Text))
		}
		return json.RawMessage(content.Text), nil
	}

	return nil, fmt.Errorf("kiwi: no text content in tool response")
}

func mapKiwiItinerary(itinerary kiwiItinerary, fallbackCurrency string) models.FlightResult {
	legs := buildKiwiLegs(itinerary)
	selfConnect := len(itinerary.Layovers) > 0
	durationSeconds := itinerary.DurationInSeconds
	if durationSeconds <= 0 {
		durationSeconds = itinerary.TotalDurationInSeconds
	}

	flight := models.FlightResult{
		Price:       itinerary.Price,
		Currency:    firstNonEmpty(itinerary.Currency, fallbackCurrency),
		Duration:    durationSeconds / 60,
		Stops:       max(len(legs)-1, 0),
		Provider:    "kiwi",
		SelfConnect: selfConnect,
		Legs:        legs,
		BookingURL:  itinerary.DeepLink,
	}
	if selfConnect {
		flight.Warnings = []string{kiwiSelfConnectWarning}
	}
	return flight
}

func buildKiwiLegs(itinerary kiwiItinerary) []models.FlightLeg {
	legs := make([]models.FlightLeg, 0, len(itinerary.Layovers)+1)
	currentCode := itinerary.FlyFrom
	currentName := firstNonEmpty(itinerary.CityFrom, itinerary.FlyFrom)
	currentDeparture := itinerary.Departure

	for _, layover := range itinerary.Layovers {
		legs = append(legs, buildKiwiLeg(
			currentCode,
			currentName,
			currentDeparture,
			firstNonEmpty(layover.At, layover.CityCode),
			firstNonEmpty(layover.City, layover.At),
			layover.Arrival,
		))
		currentCode = firstNonEmpty(layover.At, layover.CityCode)
		currentName = firstNonEmpty(layover.City, layover.At)
		currentDeparture = layover.Departure
	}

	legs = append(legs, buildKiwiLeg(
		currentCode,
		currentName,
		currentDeparture,
		itinerary.FlyTo,
		firstNonEmpty(itinerary.CityTo, itinerary.FlyTo),
		itinerary.Arrival,
	))
	computeLayovers(legs)
	return legs
}

func buildKiwiLeg(fromCode, fromName string, departure kiwiDateTime, toCode, toName string, arrival kiwiDateTime) models.FlightLeg {
	return models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: fromCode, Name: fromName},
		ArrivalAirport:   models.AirportInfo{Code: toCode, Name: toName},
		DepartureTime:    kiwiDisplayTime(departure),
		ArrivalTime:      kiwiDisplayTime(arrival),
		Duration:         kiwiDurationMinutes(departure.UTC, arrival.UTC),
	}
}

func kiwiDisplayTime(dt kiwiDateTime) string {
	if t, ok := parseKiwiTimestamp(firstNonEmpty(dt.Local, dt.UTC)); ok {
		return t.Format(flightTimeLayout)
	}
	return ""
}

func kiwiDurationMinutes(startUTC, endUTC string) int {
	start, ok := parseKiwiTimestamp(startUTC)
	if !ok {
		return 0
	}
	end, ok := parseKiwiTimestamp(endUTC)
	if !ok {
		return 0
	}
	if !end.After(start) {
		return 0
	}
	return int(end.Sub(start).Minutes())
}

func parseKiwiTimestamp(raw string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func kiwiDate(date string) (string, error) {
	t, err := models.ParseDate(date)
	if err != nil {
		return "", err
	}
	return t.Format("02/01/2006"), nil
}

func kiwiCabinClass(cabin models.CabinClass) string {
	switch cabin {
	case models.PremiumEconomy:
		return "W"
	case models.Business:
		return "C"
	case models.First:
		return "F"
	case models.Economy, 0:
		return "M"
	default:
		return "M"
	}
}

func kiwiSort(sortBy models.SortBy) string {
	switch sortBy {
	case models.SortDuration:
		return "duration"
	default:
		return "price"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
