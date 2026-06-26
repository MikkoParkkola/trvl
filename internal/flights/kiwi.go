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
	// Return carries the inbound journey of a native round-trip fare. Kiwi nests
	// it in a top-level "return" object with the same routing shape as the
	// outbound (flyFrom/flyTo, departure/arrival, layovers) but WITHOUT its own
	// price/currency/deepLink — the parent's Price is the full round-trip total.
	// Nil for a one-way search, so one-way results stay byte-unchanged.
	Return *kiwiReturnJourney `json:"return,omitempty"`
}

// kiwiReturnJourney is the inbound half of a Kiwi native round-trip. It mirrors
// the outbound routing fields of kiwiItinerary; it deliberately has no
// price/currency/deepLink because the round-trip total lives on the parent.
type kiwiReturnJourney struct {
	FlyFrom           string        `json:"flyFrom"`
	FlyTo             string        `json:"flyTo"`
	CityFrom          string        `json:"cityFrom"`
	CityTo            string        `json:"cityTo"`
	Departure         kiwiDateTime  `json:"departure"`
	Arrival           kiwiDateTime  `json:"arrival"`
	DurationInSeconds int           `json:"durationInSeconds"`
	Layovers          []kiwiLayover `json:"layovers"`
}

// kiwiJourney is the common routing shape shared by an itinerary's outbound and
// its nested return, letting one leg-builder serve both halves.
type kiwiJourney struct {
	FlyFrom   string
	FlyTo     string
	CityFrom  string
	CityTo    string
	Departure kiwiDateTime
	Arrival   kiwiDateTime
	Layovers  []kiwiLayover
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

	args := buildKiwiSearchArgs(origin, destination, departureDate, currency, opts)

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
	usable := 0
	for _, itinerary := range itineraries {
		if itinerary.FlyFrom != "" && itinerary.FlyTo != "" {
			usable++
		}
		results = append(results, mapKiwiItinerary(itinerary, currency))
	}
	if perr := kiwiParseFailureIfAllDropped(usable, len(itineraries)); perr != nil {
		return nil, perr
	}
	return results, nil
}

// kiwiParseFailureIfAllDropped mirrors parseFailureIfAllDropped for the Kiwi MCP
// path: when the upstream returned itinerary entries but none carried a usable
// route, the response shape rotated underneath us. It wraps models.ErrParseFailed
// so the caller classifies it as StatusFailed and the circuit breaker counts it,
// instead of mistaking a broken decoder for a route with no flights. Returns nil
// for a genuine empty result (no entries) or when at least one itinerary parsed.
func kiwiParseFailureIfAllDropped(usable, rawEntries int) error {
	if rawEntries > 0 && usable == 0 {
		return fmt.Errorf("kiwi: decoded 0 of %d itineraries: %w", rawEntries, models.ErrParseFailed)
	}
	return nil
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
		// Classify rate-limit / block / overload as a typed, retryable error so
		// the merge layer reports an honest `rate_limited` status instead of a
		// generic failure (mirrors googleUpstreamStatusError in search.go).
		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusForbidden, http.StatusServiceUnavailable:
			return resp.Header, kiwiRPCResponse{}, fmt.Errorf("kiwi: HTTP %d: %s: %w", resp.StatusCode, bytes.TrimSpace(body), models.ErrRateLimited)
		}
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
	legs := buildKiwiLegs(outboundJourney(itinerary))
	roundTrip := itinerary.Return != nil

	// Tag legs by direction only for a round-trip. A one-way itinerary leaves
	// Direction empty so single-direction results stay byte-unchanged.
	if roundTrip {
		for i := range legs {
			legs[i].Direction = "outbound"
		}
	}

	// SelfConnect means the OUTBOUND has a layover (self-connect risk on the way
	// there). It is derived from the outbound layovers only; the return's
	// layovers must not by themselves flag the outbound as self-connect.
	selfConnect := len(itinerary.Layovers) > 0
	// Outbound stops = outbound legs minus 1; computed before the inbound legs
	// are appended so the return's airports are never counted as outbound stops.
	stops := max(len(legs)-1, 0)

	if roundTrip {
		inbound := buildKiwiLegs(returnJourney(*itinerary.Return))
		for i := range inbound {
			inbound[i].Direction = "inbound"
		}
		legs = append(legs, inbound...)
	}

	// Duration: prefer the provider's total (covers both halves for a round-trip);
	// fall back to the single-journey duration, then to summing all leg durations.
	durationSeconds := itinerary.TotalDurationInSeconds
	if durationSeconds <= 0 {
		durationSeconds = itinerary.DurationInSeconds
	}
	durationMinutes := durationSeconds / 60
	if durationMinutes <= 0 {
		durationMinutes = sumLegDurations(legs)
	}

	flight := models.FlightResult{
		Price:       itinerary.Price,
		Currency:    firstNonEmpty(itinerary.Currency, fallbackCurrency),
		Duration:    durationMinutes,
		Stops:       stops,
		Provider:    "kiwi",
		SelfConnect: selfConnect,
		Legs:        legs,
		BookingURL:  itinerary.DeepLink,
	}
	if roundTrip {
		// FlightResult carries the per-result round-trip marker via FareType
		// (matching the roundtrip.go composer convention); the "round_trip"
		// TripType string lives on the enclosing FlightSearchResult, not here.
		flight.FareType = models.FareRoundTrip
	}
	if selfConnect {
		flight.Warnings = []string{kiwiSelfConnectWarning}
	}
	return flight
}

// outboundJourney projects the outbound routing of an itinerary into the shared
// kiwiJourney shape.
func outboundJourney(it kiwiItinerary) kiwiJourney {
	return kiwiJourney{
		FlyFrom:   it.FlyFrom,
		FlyTo:     it.FlyTo,
		CityFrom:  it.CityFrom,
		CityTo:    it.CityTo,
		Departure: it.Departure,
		Arrival:   it.Arrival,
		Layovers:  it.Layovers,
	}
}

// returnJourney projects a nested return into the shared kiwiJourney shape.
func returnJourney(r kiwiReturnJourney) kiwiJourney {
	return kiwiJourney{
		FlyFrom:   r.FlyFrom,
		FlyTo:     r.FlyTo,
		CityFrom:  r.CityFrom,
		CityTo:    r.CityTo,
		Departure: r.Departure,
		Arrival:   r.Arrival,
		Layovers:  r.Layovers,
	}
}

// sumLegDurations totals each leg's per-segment duration (minutes), used as a
// last-resort fallback when the provider supplies no usable trip total.
func sumLegDurations(legs []models.FlightLeg) int {
	total := 0
	for _, leg := range legs {
		total += leg.Duration
	}
	return total
}

func buildKiwiLegs(journey kiwiJourney) []models.FlightLeg {
	legs := make([]models.FlightLeg, 0, len(journey.Layovers)+1)
	currentCode := journey.FlyFrom
	currentName := firstNonEmpty(journey.CityFrom, journey.FlyFrom)
	currentDeparture := journey.Departure

	for _, layover := range journey.Layovers {
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
		journey.FlyTo,
		firstNonEmpty(journey.CityTo, journey.FlyTo),
		journey.Arrival,
	))
	// Compute per-journey so the inbound's first leg never inherits a spurious
	// layover from the outbound's final arrival.
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
	// Shared outbound date canonicalizer (MIK-4956 XSHOP.6) instead of a
	// hand-rolled layout. Kiwi wants day/month/year.
	if out, ok := models.FormatProviderDate(date, models.DateLayoutDMY); ok {
		return out, nil
	}
	return "", fmt.Errorf("kiwi: invalid date %q", date)
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

// buildKiwiSearchArgs assembles the Kiwi MCP search-flight arguments, including
// the advanced options Kiwi exposes (round-trip returnDate + flexible date
// ranges). departureDate must already be in Kiwi's day/month/year format.
func buildKiwiSearchArgs(origin, destination, departureDate, currency string, opts SearchOptions) map[string]any {
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
	if opts.ReturnDate != "" {
		if rd, rerr := kiwiDate(opts.ReturnDate); rerr == nil {
			args["returnDate"] = rd
		}
	}
	if d := clampFlexDays(opts.DepartureFlexDays); d > 0 {
		args["departureDateFlexRange"] = d
	}
	if r := clampFlexDays(opts.ReturnFlexDays); r > 0 && opts.ReturnDate != "" {
		args["returnDateFlexRange"] = r
	}
	return args
}

// clampFlexDays bounds a flexible-date range to Kiwi's accepted 0..3.
func clampFlexDays(n int) int {
	if n < 0 {
		return 0
	}
	if n > 3 {
		return 3
	}
	return n
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
