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

	"github.com/MikkoParkkola/trvl/internal/logredact"

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

// kiwiSearchResponse is the search-flight tool payload for the
// kiwicom-flight-search server (v1.15.0+): an object wrapping the itinerary
// list, replacing the bare JSON array earlier server versions returned.
type kiwiSearchResponse struct {
	Currency     string          `json:"currency"`
	ResultsCount int             `json:"resultsCount"`
	Itineraries  []kiwiItinerary `json:"itineraries"`
}

// kiwiItinerary is one priced option. Outbound is always present; Inbound is
// non-nil only for a round-trip search.
type kiwiItinerary struct {
	ID                   string   `json:"id"`
	Price                float64  `json:"price"`
	TotalDurationSeconds int      `json:"totalDurationSeconds"`
	BookingURL           string   `json:"bookingUrl"`
	Outbound             *kiwiLeg `json:"outbound"`
	Inbound              *kiwiLeg `json:"inbound"`
}

// kiwiLeg is one direction of travel: a routing summary plus the concrete
// per-flight segments that make it up.
type kiwiLeg struct {
	From            string        `json:"from"`
	To              string        `json:"to"`
	DepartureTime   string        `json:"departureTime"`
	ArrivalTime     string        `json:"arrivalTime"`
	DurationSeconds int           `json:"durationSeconds"`
	Stops           int           `json:"stops"`
	CabinClass      string        `json:"cabinClass"`
	Segments        []kiwiSegment `json:"segments"`
}

// kiwiSegment is a single operated flight within a leg.
type kiwiSegment struct {
	From            string `json:"from"`
	To              string `json:"to"`
	FromCity        string `json:"fromCity"`
	ToCity          string `json:"toCity"`
	DepartureTime   string `json:"departureTime"`
	ArrivalTime     string `json:"arrivalTime"`
	DurationSeconds int    `json:"durationSeconds"`
	Carrier         string `json:"carrier"`
	FlightNumber    string `json:"flightNumber"`
	CabinClass      string `json:"cabinClass"`
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

	var searchResp kiwiSearchResponse
	if err := json.Unmarshal(payload, &searchResp); err != nil {
		return nil, fmt.Errorf("kiwi: decode search results: %w", err)
	}

	respCurrency := firstNonEmpty(searchResp.Currency, currency)
	results := make([]models.FlightResult, 0, len(searchResp.Itineraries))
	for _, itinerary := range searchResp.Itineraries {
		if !kiwiItineraryUsable(itinerary) {
			continue // routeless entry (nil/empty outbound) — never emit a no-leg fare
		}
		results = append(results, mapKiwiItinerary(itinerary, respCurrency))
	}

	raw := len(searchResp.Itineraries)
	// Shape-rotation guard: the server promised results but returned no entries.
	if searchResp.ResultsCount > 0 && raw == 0 {
		return nil, fmt.Errorf("kiwi: %d results promised but none returned: %w", searchResp.ResultsCount, models.ErrParseFailed)
	}
	// Entries arrived but none carried a usable route -> the itinerary shape
	// rotated underneath us (a parse failure), not a genuine empty result.
	if perr := kiwiParseFailureIfAllDropped(len(results), raw); perr != nil {
		return nil, perr
	}
	return results, nil
}

// kiwiLegEndpoints returns a leg's origin and destination, preferring the leg's
// routing summary and falling back to the first/last segment. ok is false when
// neither yields both endpoints — so a leg that keeps concrete segments after a
// summary rename still resolves, and a truly routeless leg is rejected.
func kiwiLegEndpoints(leg *kiwiLeg) (from, to string, ok bool) {
	if leg == nil {
		return "", "", false
	}
	from, to = leg.From, leg.To
	if from == "" && len(leg.Segments) > 0 {
		from = leg.Segments[0].From
	}
	if to == "" && len(leg.Segments) > 0 {
		to = leg.Segments[len(leg.Segments)-1].To
	}
	return from, to, from != "" && to != ""
}

// kiwiItineraryUsable reports whether an itinerary has a resolvable outbound
// route, so routeless entries are skipped instead of emitted as no-leg fares.
func kiwiItineraryUsable(it kiwiItinerary) bool {
	_, _, ok := kiwiLegEndpoints(it.Outbound)
	return ok
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

	// Per the MCP Streamable HTTP spec the mcp-session-id header is OPTIONAL: a
	// stateless server may omit it (Kiwi's kiwicom-flight-search server went
	// stateless in v1.15.0, dropping the header). An empty session id is
	// therefore valid — operate without one; kiwiRPCWithHeaders simply omits the
	// header on subsequent calls, and the server treats each request as
	// independent.
	return strings.TrimSpace(headers.Get("mcp-session-id")), nil
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
	// The max token must exceed extractKiwiContent's 512 KiB content limit plus
	// JSON-RPC envelope overhead, otherwise a large-but-valid SSE frame fails
	// here before extraction ever runs.
	scanner.Buffer(make([]byte, 64*1024), 640*1024)

	var last kiwiRPCResponse
	found := false
	var data []string // accumulated `data:` fields for the current SSE event

	flush := func() {
		if len(data) == 0 {
			return
		}
		// Per the SSE spec, an event's multiple data fields are joined with "\n"
		// before the value is used — a single JSON payload may be split across
		// several data lines.
		joined := strings.Join(data, "\n")
		data = data[:0]
		if joined == "" || joined == "[DONE]" {
			return
		}
		var rpc kiwiRPCResponse
		if err := json.Unmarshal([]byte(joined), &rpc); err != nil {
			slog.Debug("kiwi: skipping unparseable SSE frame", "error", logredact.Err(err))
			return
		}
		if rpc.Result != nil || rpc.Error != nil {
			last = rpc
			found = true
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" { // a blank line terminates the current SSE event
			flush()
			continue
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			// SSE strips exactly one leading space after the colon; internal
			// whitespace is part of the payload and preserved.
			data = append(data, strings.TrimPrefix(after, " "))
		}
	}
	flush() // the final event need not be followed by a blank line
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
	roundTrip := itinerary.Inbound != nil

	var legs []models.FlightLeg
	if itinerary.Outbound != nil {
		out := buildKiwiLegs(*itinerary.Outbound)
		// Tag legs by direction only for a round-trip. A one-way itinerary leaves
		// Direction empty so single-direction results stay byte-unchanged.
		if roundTrip {
			for i := range out {
				out[i].Direction = "outbound"
			}
		}
		legs = append(legs, out...)
	}

	// Outbound stops: prefer the leg's own stops count, else outbound legs minus
	// one. Computed before the inbound legs are appended so the return's airports
	// are never counted as outbound stops.
	stops := max(len(legs)-1, 0)
	if itinerary.Outbound != nil && itinerary.Outbound.Stops > 0 {
		stops = itinerary.Outbound.Stops
	}
	// Self-connect risk: a connection is typically Kiwi virtual interlining
	// (separate tickets, self-transfer bag re-check). Derived from the outbound
	// only (so the return never flags the outbound), from EITHER the stops count
	// or the segment count — a summary-only leg reports stops without segments.
	selfConnect := itinerary.Outbound != nil &&
		(itinerary.Outbound.Stops > 0 || len(itinerary.Outbound.Segments) > 1)

	if roundTrip {
		inbound := buildKiwiLegs(*itinerary.Inbound)
		for i := range inbound {
			inbound[i].Direction = "inbound"
		}
		legs = append(legs, inbound...)
	}

	// Duration: prefer the provider's trip total (covers both halves of a
	// round-trip); then the sum of each leg's own DurationSeconds; then the sum
	// of the mapped per-segment leg durations.
	durationMinutes := itinerary.TotalDurationSeconds / 60
	if durationMinutes <= 0 {
		legSeconds := 0
		if itinerary.Outbound != nil {
			legSeconds += itinerary.Outbound.DurationSeconds
		}
		if itinerary.Inbound != nil {
			legSeconds += itinerary.Inbound.DurationSeconds
		}
		durationMinutes = legSeconds / 60
	}
	if durationMinutes <= 0 {
		durationMinutes = sumLegDurations(legs)
	}

	flight := models.FlightResult{
		Price:       itinerary.Price,
		Currency:    fallbackCurrency,
		Duration:    durationMinutes,
		Stops:       stops,
		Provider:    "kiwi",
		SelfConnect: selfConnect,
		Legs:        legs,
		BookingURL:  itinerary.BookingURL,
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

// sumLegDurations totals each leg's per-segment duration (minutes), used as a
// last-resort fallback when the provider supplies no usable trip total.
func sumLegDurations(legs []models.FlightLeg) int {
	total := 0
	for _, leg := range legs {
		total += leg.Duration
	}
	return total
}

// buildKiwiLegs converts a leg's concrete flight segments into FlightLegs. If a
// leg carries no segments (summary only), it yields a single leg from the leg's
// own from/to/times so the route stays usable.
func buildKiwiLegs(leg kiwiLeg) []models.FlightLeg {
	if len(leg.Segments) == 0 {
		duration := leg.DurationSeconds / 60
		if duration <= 0 {
			duration = kiwiDurationMinutes(leg.DepartureTime, leg.ArrivalTime)
		}
		return []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: leg.From, Name: leg.From},
			ArrivalAirport:   models.AirportInfo{Code: leg.To, Name: leg.To},
			DepartureTime:    kiwiDisplayTime(leg.DepartureTime),
			ArrivalTime:      kiwiDisplayTime(leg.ArrivalTime),
			Duration:         duration,
		}}
	}

	legs := make([]models.FlightLeg, 0, len(leg.Segments))
	for _, s := range leg.Segments {
		duration := s.DurationSeconds / 60
		if duration <= 0 {
			duration = kiwiDurationMinutes(s.DepartureTime, s.ArrivalTime)
		}
		legs = append(legs, models.FlightLeg{
			DepartureAirport: models.AirportInfo{Code: s.From, Name: firstNonEmpty(s.FromCity, s.From)},
			ArrivalAirport:   models.AirportInfo{Code: s.To, Name: firstNonEmpty(s.ToCity, s.To)},
			DepartureTime:    kiwiDisplayTime(s.DepartureTime),
			ArrivalTime:      kiwiDisplayTime(s.ArrivalTime),
			Duration:         duration,
			AirlineCode:      s.Carrier,
			FlightNumber:     s.FlightNumber,
		})
	}
	// Compute per-journey so the inbound's first leg never inherits a spurious
	// layover from the outbound's final arrival.
	computeLayovers(legs)
	return legs
}

// kiwiDisplayTime formats a Kiwi naive-local timestamp for display, returning ""
// when it cannot be parsed.
func kiwiDisplayTime(ts string) string {
	if t, ok := parseKiwiTimestamp(ts); ok {
		return t.Format(flightTimeLayout)
	}
	return ""
}

func kiwiDurationMinutes(start, end string) int {
	s, ok := parseKiwiTimestamp(start)
	if !ok {
		return 0
	}
	e, ok := parseKiwiTimestamp(end)
	if !ok {
		return 0
	}
	if !e.After(s) {
		return 0
	}
	return int(e.Sub(s).Minutes())
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
