package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/livecheck"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// --- watch_price tool ---

func watchPriceTool() ToolDef {
	return ToolDef{
		Name:  "watch_price",
		Title: "Watch Price",
		Description: "Create a price watch for a flight route or hotel stay. " +
			"The watch is stored in ~/.trvl/watches.json and tracks whether the price drops " +
			"below your target. Call check_watches later to re-check all active watches. " +
			"For flights: provide origin, destination, and either a single date or a " +
			"depart_from/depart_to date range to scan. " +
			"For hotels: provide location, check_in, and check_out. " +
			"Set target_price to the maximum price you are willing to pay, or use " +
			"alert_drop/alert_drop_abs for a proactive drop-from-baseline alert with no fixed threshold.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"type":                 {Type: "string", Description: "Watch type: \"flight\" or \"hotel\""},
				"origin":               {Type: "string", Description: "Origin airport IATA code (flights only, e.g. HEL)"},
				"destination":          {Type: "string", Description: "Destination airport IATA code (flights only, e.g. BCN)"},
				"location":             {Type: "string", Description: "City or location name (hotels only, e.g. Barcelona)"},
				"date":                 {Type: "string", Description: "Departure date YYYY-MM-DD (flights) or check-in date (hotels, use check_in instead)"},
				"return_date":          {Type: "string", Description: "Return date YYYY-MM-DD for round-trip flights (optional)"},
				"check_in":             {Type: "string", Description: "Hotel check-in date YYYY-MM-DD (hotels only)"},
				"check_out":            {Type: "string", Description: "Hotel check-out date YYYY-MM-DD (hotels only)"},
				"target_price":         {Type: "number", Description: "Alert threshold: notify when price drops below this amount. Optional when alert_drop or alert_drop_abs is set."},
				"currency":             {Type: "string", Description: "Currency code (e.g. EUR, USD). Default: EUR"},
				"webhook":              {Type: "string", Description: "URL to POST a JSON payload to on price drop, mirroring CLI --webhook"},
				"depart_from":          {Type: "string", Description: "Flight date-range start (scan for the cheapest fare across a window, mirrors CLI --from). Use with depart_to instead of a single date."},
				"depart_to":            {Type: "string", Description: "Flight date-range end (scan for the cheapest fare across a window, mirrors CLI --to). Use with depart_from instead of a single date."},
				"alert_drop":           {Type: "number", Description: "Proactively alert when the fare falls this percent below its baseline (mirrors CLI --alert-drop)"},
				"alert_drop_abs":       {Type: "number", Description: "Proactively alert when the fare falls this many currency units below its baseline (mirrors CLI --alert-drop-abs)"},
				"last_minute":          {Type: "boolean", Description: "Hotel watches only: notify when sub-48h availability is at least 25% below last seen price"},
				"last_minute_drop_pct": {Type: "number", Description: "Hotel last-minute drop threshold percentage. Default: 25"},
			},
			Required: []string{"type"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success":      schemaBool(),
				"watch_id":     schemaString(),
				"type":         schemaString(),
				"origin":       schemaString(),
				"destination":  schemaString(),
				"location":     schemaString(),
				"target_price": schemaNum(),
				"currency":     schemaString(),
				"created_at":   schemaString(),
				"error":        schemaString(),
			},
			"required": []string{"success"},
		},
		Annotations: &ToolAnnotations{
			Title:          "Watch Price",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  false,
		},
	}
}

func handleWatchPrice(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	watchType := argString(args, "type")
	if watchType != "flight" && watchType != "hotel" {
		return nil, nil, fmt.Errorf("type must be \"flight\" or \"hotel\"")
	}

	targetPrice := argFloat(args, "target_price", 0)
	alertDropPct := argFloat(args, "alert_drop", 0)
	alertDropAbs := argFloat(args, "alert_drop_abs", 0)
	// target_price is optional when a proactive drop-from-baseline alert is set,
	// mirroring the CLI which allows --alert-drop / --alert-drop-abs without --below.
	if targetPrice <= 0 && alertDropPct <= 0 && alertDropAbs <= 0 {
		return nil, nil, fmt.Errorf("set target_price, alert_drop, or alert_drop_abs to a positive value")
	}

	currency := argString(args, "currency")
	if currency == "" {
		currency = "EUR"
	}

	w := watch.Watch{
		Type:              watchType,
		BelowPrice:        targetPrice,
		Currency:          currency,
		WebhookURL:        argString(args, "webhook"),
		AlertDropPct:      alertDropPct,
		AlertDropAbs:      alertDropAbs,
		LastMinuteMode:    argBool(args, "last_minute", false),
		LastMinuteDropPct: argFloat(args, "last_minute_drop_pct", 25),
	}

	switch watchType {
	case "flight":
		w.Origin = strings.ToUpper(argString(args, "origin"))
		w.Destination = strings.ToUpper(argString(args, "destination"))
		if w.Origin == "" || w.Destination == "" {
			return nil, nil, fmt.Errorf("flight watches require origin and destination")
		}
		// Two flight date modes, mirroring the CLI:
		//   - single date  ("date"/"depart_date") -> DepartDate
		//   - date range   (depart_from + depart_to) -> scan window
		// The watch validator rejects setting both, so pick exactly one.
		date := argString(args, "date")
		if date == "" {
			date = argString(args, "depart_date")
		}
		departFrom := argString(args, "depart_from")
		departTo := argString(args, "depart_to")
		switch {
		case departFrom != "" || departTo != "":
			if departFrom == "" || departTo == "" {
				return nil, nil, fmt.Errorf("flight date-range watches require both depart_from and depart_to")
			}
			if date != "" {
				return nil, nil, fmt.Errorf("provide either a single date or a depart_from/depart_to range, not both")
			}
			w.DepartFrom = departFrom
			w.DepartTo = departTo
		case date != "":
			w.DepartDate = date
		default:
			return nil, nil, fmt.Errorf("flight watches require a departure date (date) or a depart_from/depart_to range")
		}
		if ret := argString(args, "return_date"); ret != "" {
			w.ReturnDate = ret
		}
	case "hotel":
		w.Destination = argString(args, "location")
		if w.Destination == "" {
			w.Destination = argString(args, "destination")
		}
		if w.Destination == "" {
			return nil, nil, fmt.Errorf("hotel watches require a location")
		}
		checkIn := argString(args, "check_in")
		checkOut := argString(args, "check_out")
		if checkIn == "" {
			checkIn = argString(args, "date")
		}
		if checkIn == "" || checkOut == "" {
			return nil, nil, fmt.Errorf("hotel watches require check_in and check_out dates")
		}
		// Store hotel check-in/check-out using the date-range fields.
		w.DepartFrom = checkIn
		w.DepartTo = checkOut
	}

	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watch store: %w", err)
	}

	id, created, err := store.Add(w)
	if err != nil {
		return nil, nil, fmt.Errorf("add watch: %w", err)
	}

	type watchResponse struct {
		Success     bool    `json:"success"`
		WatchID     string  `json:"watch_id"`
		Type        string  `json:"type"`
		Origin      string  `json:"origin,omitempty"`
		Destination string  `json:"destination,omitempty"`
		Location    string  `json:"location,omitempty"`
		TargetPrice float64 `json:"target_price"`
		Currency    string  `json:"currency"`
		CreatedAt   string  `json:"created_at"`
		// False when an existing watch for the same target was updated instead.
		// Add is idempotent, so re-watching returns the ORIGINAL id; reporting it
		// as newly created would be a falsehood the agent then repeats to the user.
		Created bool `json:"created"`
	}

	resp := watchResponse{
		Success:     true,
		Created:     created,
		WatchID:     id,
		Type:        watchType,
		TargetPrice: targetPrice,
		Currency:    currency,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	var summary string
	alert := watchAlertClause(w)
	switch watchType {
	case "flight":
		resp.Origin = w.Origin
		resp.Destination = w.Destination
		when := "on " + w.DepartDate
		if w.IsDateRange() {
			when = fmt.Sprintf("scanning %s to %s", w.DepartFrom, w.DepartTo)
		}
		summary = fmt.Sprintf("Flight watch %s created: %s→%s %s. %s",
			id, w.Origin, w.Destination, when, alert)
		if w.ReturnDate != "" {
			summary += fmt.Sprintf(" Return: %s.", w.ReturnDate)
		}
	case "hotel":
		resp.Location = w.Destination
		summary = fmt.Sprintf("Hotel watch %s created: %s (%s to %s). %s",
			id, w.Destination, w.DepartFrom, w.DepartTo, alert)
		if w.LastMinuteMode {
			summary += fmt.Sprintf(" Last-minute mode enabled at %.0f%% below last seen price.", w.LastMinuteDropPct)
		}
	}
	summary += " Use check_watches to re-check all active watches."

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// --- list_watches tool ---

func listWatchesTool() ToolDef {
	return ToolDef{
		Name:        "list_watches",
		Title:       "List Price Watches",
		Description: "List all active price watches stored in ~/.trvl/watches.json. Shows each watch with its route, target price, last known price, and price history sparkline.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"watches": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":           schemaString(),
						"type":         schemaString(),
						"route":        schemaString(),
						"target_price": schemaNum(),
						"last_price":   schemaNum(),
						"lowest_price": schemaNum(),
						"currency":     schemaString(),
						"created_at":   schemaString(),
						"last_check":   schemaString(),
						"trend":        schemaString(),
						"sparkline":    schemaString(),
					},
				}),
				"count": schemaInt(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "List Price Watches",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

func handleListWatches(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watch store: %w", err)
	}

	watches := store.List()

	type watchSummary struct {
		ID          string  `json:"id"`
		Type        string  `json:"type"`
		Route       string  `json:"route"`
		TargetPrice float64 `json:"target_price"`
		LastPrice   float64 `json:"last_price,omitempty"`
		LowestPrice float64 `json:"lowest_price,omitempty"`
		Currency    string  `json:"currency"`
		CreatedAt   string  `json:"created_at"`
		LastCheck   string  `json:"last_check,omitempty"`
		Trend       string  `json:"trend,omitempty"`
		Sparkline   string  `json:"sparkline,omitempty"`
	}

	summaries := make([]watchSummary, 0, len(watches))
	for _, w := range watches {
		history := store.History(w.ID)
		ws := watchSummary{
			ID:          w.ID,
			Type:        w.Type,
			TargetPrice: w.BelowPrice,
			LastPrice:   w.LastPrice,
			LowestPrice: w.LowestPrice,
			Currency:    w.Currency,
			CreatedAt:   w.CreatedAt.UTC().Format(time.RFC3339),
			Trend:       watch.TrendArrow(history),
			Sparkline:   watch.Sparkline(history, 20),
		}
		if !w.LastCheck.IsZero() {
			ws.LastCheck = w.LastCheck.UTC().Format(time.RFC3339)
		}
		ws.Route = watchRoute(w)
		summaries = append(summaries, ws)
	}

	type listResponse struct {
		Watches []watchSummary `json:"watches"`
		Count   int            `json:"count"`
	}
	resp := listResponse{Watches: summaries, Count: len(summaries)}

	var summary string
	if len(watches) == 0 {
		summary = "No active price watches. Use watch_price to create one."
	} else {
		summary = fmt.Sprintf("%d active watch(es):\n", len(watches))
		for _, ws := range summaries {
			line := fmt.Sprintf("  [%s] %s — target: %.0f %s", ws.ID, ws.Route, ws.TargetPrice, ws.Currency)
			if ws.LastPrice > 0 {
				line += fmt.Sprintf(", last: %.0f", ws.LastPrice)
				if ws.Trend != "" {
					line += " " + ws.Trend
				}
			}
			if ws.Sparkline != "" {
				line += " " + ws.Sparkline
			}
			summary += line + "\n"
		}
		summary += "Use check_watches to re-check prices now."
	}

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// --- check_watches tool ---

func checkWatchesTool() ToolDef {
	return ToolDef{
		Name:  "check_watches",
		Title: "Check Price Watches",
		Description: "Re-check all active price watches against current live prices. " +
			"For each watch, fetches the current best price and compares it to the target. " +
			"Returns which watches have dropped below their target price. " +
			"Note: this makes live network requests and may take 10-30 seconds for many watches.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"checked": schemaInt(),
				"triggered": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":            schemaString(),
						"route":         schemaString(),
						"target_price":  schemaNum(),
						"current_price": schemaNum(),
						"price_drop":    schemaNum(),
						"currency":      schemaString(),
					},
				}),
				"results": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":            schemaString(),
						"route":         schemaString(),
						"current_price": schemaNum(),
						"prev_price":    schemaNum(),
						"price_drop":    schemaNum(),
						"below_goal":    schemaBool(),
						"currency":      schemaString(),
						"error":         schemaString(),
					},
				}),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Check Price Watches",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  true,
		},
	}
}

func handleCheckWatches(ctx context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watch store: %w", err)
	}

	watches := store.List()
	if len(watches) == 0 {
		type emptyResponse struct {
			Checked   int `json:"checked"`
			Triggered int `json:"triggered_count"`
		}
		resp := emptyResponse{Checked: 0, Triggered: 0}
		content, err := buildAnnotatedContentBlocks("No active watches to check. Use watch_price to create one.", resp)
		if err != nil {
			return nil, nil, err
		}
		return content, resp, nil
	}

	// Re-price every watch against live sources, bounded so a synchronous MCP
	// call cannot block indefinitely. checkWatchesChecker is a package var so
	// tests can inject a deterministic checker. Room watches are reported with an
	// honest "not checked" error until the room checker is wired (no fake price).
	results := watch.CheckAllBounded(ctx, store, checkWatchesChecker, nil, watch.BoundedOptions{})

	type resultItem struct {
		ID           string  `json:"id"`
		Route        string  `json:"route"`
		CurrentPrice float64 `json:"current_price,omitempty"`
		PrevPrice    float64 `json:"prev_price,omitempty"`
		PriceDrop    float64 `json:"price_drop,omitempty"`
		BelowGoal    bool    `json:"below_goal"`
		Currency     string  `json:"currency,omitempty"`
		Error        string  `json:"error,omitempty"`
	}

	type triggeredItem struct {
		ID           string  `json:"id"`
		Route        string  `json:"route"`
		TargetPrice  float64 `json:"target_price"`
		CurrentPrice float64 `json:"current_price"`
		PriceDrop    float64 `json:"price_drop,omitempty"`
		Currency     string  `json:"currency"`
	}

	var triggered []triggeredItem
	items := make([]resultItem, 0, len(results))
	for _, r := range results {
		item := resultItem{
			ID:           r.Watch.ID,
			Route:        watchRoute(r.Watch),
			CurrentPrice: r.NewPrice,
			PrevPrice:    r.PrevPrice,
			PriceDrop:    r.PriceDrop,
			BelowGoal:    r.BelowGoal,
			Currency:     r.Currency,
		}
		if r.Error != nil {
			item.Error = r.Error.Error()
		}
		items = append(items, item)

		if r.BelowGoal {
			triggered = append(triggered, triggeredItem{
				ID:           r.Watch.ID,
				Route:        watchRoute(r.Watch),
				TargetPrice:  r.Watch.BelowPrice,
				CurrentPrice: r.NewPrice,
				PriceDrop:    r.PriceDrop,
				Currency:     r.Currency,
			})
		}
	}

	type checkResponse struct {
		Checked   int             `json:"checked"`
		Triggered []triggeredItem `json:"triggered"`
		Results   []resultItem    `json:"results"`
	}
	resp := checkResponse{
		Checked:   len(results),
		Triggered: triggered,
		Results:   items,
	}
	if resp.Triggered == nil {
		resp.Triggered = []triggeredItem{}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Checked %d watch(es).", len(results)))
	if len(triggered) > 0 {
		lines = append(lines, fmt.Sprintf("%d watch(es) triggered (price below target):", len(triggered)))
		for _, t := range triggered {
			lines = append(lines, fmt.Sprintf("  [%s] %s: %.0f %s (target %.0f, drop %.0f)",
				t.ID, t.Route, t.CurrentPrice, t.Currency, t.TargetPrice, -t.PriceDrop))
		}
	} else {
		lines = append(lines, "No watches triggered yet.")
	}
	for _, item := range items {
		if item.Error != "" {
			lines = append(lines, fmt.Sprintf("  [%s] %s: error — %s", item.ID, item.Route, item.Error))
		}
	}

	content, err := buildAnnotatedContentBlocks(strings.Join(lines, "\n"), resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// checkWatchesChecker is the price checker used by handleCheckWatches. It is a
// package var so tests can inject a deterministic fake; in production it performs
// real live flight/hotel searches via the shared livecheck implementation.
var checkWatchesChecker watch.PriceChecker = &mcpPriceChecker{}

// mcpPriceChecker implements watch.PriceChecker by delegating to the shared
// livecheck implementation that the CLI watch daemon also uses. Keeping a single
// implementation behind both entry points is deliberate: the original bug was a
// live CLI checker paired with a stubbed MCP checker that always returned 0,
// which made check_watches silently report no price movement.
type mcpPriceChecker struct{}

func (m *mcpPriceChecker) CheckPrice(ctx context.Context, w watch.Watch) (float64, string, string, error) {
	return livecheck.Checker{}.CheckPrice(ctx, w)
}

// --- helpers ---

// watchAlertClause renders a human-readable description of when a watch fires,
// covering the fixed below-price threshold and the proactive drop-from-baseline
// alerts (percent and/or absolute), mirroring the CLI's summary output.
func watchAlertClause(w watch.Watch) string {
	var parts []string
	if w.BelowPrice > 0 {
		parts = append(parts, fmt.Sprintf("when below %.0f %s", w.BelowPrice, w.Currency))
	}
	if w.AlertDropPct > 0 {
		parts = append(parts, fmt.Sprintf("on a %.0f%% drop from baseline", w.AlertDropPct))
	}
	if w.AlertDropAbs > 0 {
		parts = append(parts, fmt.Sprintf("on a %.0f %s drop from baseline", w.AlertDropAbs, w.Currency))
	}
	if len(parts) == 0 {
		return "Tracking price history."
	}
	return "Alert " + strings.Join(parts, " or ") + "."
}

// watchRoute returns a human-readable route string for a watch.
func watchRoute(w watch.Watch) string {
	switch w.Type {
	case "flight":
		route := w.Origin + "→" + w.Destination
		if w.DepartDate != "" {
			route += " " + w.DepartDate
		}
		if w.ReturnDate != "" {
			route += "→" + w.ReturnDate
		}
		return route
	case "hotel":
		loc := w.Destination
		if w.HotelName != "" {
			loc = w.HotelName
		}
		if w.DepartFrom != "" {
			return fmt.Sprintf("%s %s to %s", loc, w.DepartFrom, w.DepartTo)
		}
		if w.DepartDate != "" {
			return fmt.Sprintf("%s check-in %s", loc, w.DepartDate)
		}
		return loc
	case "room":
		return fmt.Sprintf("%s [room: %s]", w.HotelName, strings.Join(w.RoomKeywords, ","))
	default:
		return w.Destination
	}
}
