package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/calendar"
	"github.com/MikkoParkkola/trvl/internal/trips"
)

// tripOutputSchema returns a JSON Schema for a trip object.
func tripOutputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id":             schemaString(),
			"schema_version": schemaInt(),
			"name":           schemaString(),
			"status":         schemaString(),
			"created_at":     schemaString(),
			"updated_at":     schemaString(),
			"legs": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":       schemaString(),
					"from":       schemaString(),
					"to":         schemaString(),
					"provider":   schemaString(),
					"start_time": schemaString(),
					"end_time":   schemaString(),
					"price":      schemaNum(),
					"currency":   schemaString(),
					"confirmed":  schemaBool(),
					"reference":  schemaString(),
				},
			}),
			"bookings": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":         schemaString(),
					"provider":     schemaString(),
					"reference":    schemaString(),
					"url":          schemaString(),
					"candidate_id": schemaString(),
					"price":        schemaNum(),
					"currency":     schemaString(),
				},
			}),
			"workspace": schemaObject(),
			"tags":      schemaStringArray(),
			"notes":     schemaString(),
		},
	}
}

// --- Trip tool definitions ---

func listTripsTool() ToolDef {
	return ToolDef{
		Name:        "list_trips",
		Title:       "List Trips",
		Description: "Returns all active trips (status: planning, booked, in_progress). Use get_trip for full leg detail on a specific trip.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"trips": schemaArray(tripOutputSchema()),
				"count": schemaInt(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "List Trips",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

func getTripTool() ToolDef {
	return ToolDef{
		Name:        "get_trip",
		Title:       "Get Trip",
		Description: "Returns full details for a single trip including all legs and bookings.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"id": {Type: "string", Description: "Trip ID (e.g. trip_abc123)"},
			},
			Required: []string{"id"},
		},
		OutputSchema: tripOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Get Trip",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

func createTripTool() ToolDef {
	return ToolDef{
		Name:        "create_trip",
		Title:       "Create Trip",
		Description: "Creates a new trip and returns its ID. Use add_trip_leg to add travel segments.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"name":   {Type: "string", Description: "Human-friendly trip name, e.g. 'Helsinki court + Prague + Amsterdam'"},
				"tags":   {Type: "string", Description: "Comma-separated tags, e.g. 'work,court'"},
				"notes":  {Type: "string", Description: "Free-form notes"},
				"status": {Type: "string", Description: "Initial status: planning (default) or booked"},
			},
			Required: []string{"name"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":   schemaString(),
				"name": schemaString(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Create Trip",
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}
}

func addTripLegTool() ToolDef {
	return ToolDef{
		Name:        "add_trip_leg",
		Title:       "Add Trip Leg",
		Description: "Adds a travel segment (flight, train, hotel, etc.) to an existing trip.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"trip_id":    {Type: "string", Description: "Trip ID"},
				"type":       {Type: "string", Description: "Segment type: flight, train, bus, ferry, hotel, activity"},
				"from":       {Type: "string", Description: "Origin city or location"},
				"to":         {Type: "string", Description: "Destination city or location"},
				"provider":   {Type: "string", Description: "Carrier or hotel name, e.g. KLM"},
				"start_time": {Type: "string", Description: "Departure/check-in ISO datetime, e.g. 2026-04-11T18:25"},
				"end_time":   {Type: "string", Description: "Arrival/check-out ISO datetime"},
				"price":      {Type: "number", Description: "Price amount"},
				"currency":   {Type: "string", Description: "Currency code, e.g. EUR"},
				"confirmed":  {Type: "boolean", Description: "Whether this leg is booked/confirmed"},
				"reference":  {Type: "string", Description: "Booking reference or PNR"},
			},
			Required: []string{"trip_id", "type", "from", "to"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"trip_id": schemaString(),
				"leg":     tripOutputSchema(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Add Trip Leg",
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}
}

func markTripBookedTool() ToolDef {
	return ToolDef{
		Name:        "mark_trip_booked",
		Title:       "Mark Trip Booked",
		Description: "Adds a booking reference to a trip and advances its status to 'booked'.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"trip_id":      {Type: "string", Description: "Trip ID"},
				"provider":     {Type: "string", Description: "Carrier or hotel name, e.g. KLM"},
				"reference":    {Type: "string", Description: "Booking reference / PNR, e.g. ABC123"},
				"type":         {Type: "string", Description: "Booking type: flight, hotel, other"},
				"url":          {Type: "string", Description: "Confirmation URL"},
				"candidate_id": {Type: "string", Description: "Optional saved trip_workspace booking candidate ID"},
				"price":        {Type: "number", Description: "Confirmed booking price"},
				"currency":     {Type: "string", Description: "Confirmed booking currency"},
				"notes":        {Type: "string", Description: "Notes"},
			},
			Required: []string{"trip_id", "provider", "reference"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"trip_id":   schemaString(),
				"reference": schemaString(),
				"status":    schemaString(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Mark Trip Booked",
			ReadOnlyHint:   false,
			IdempotentHint: false,
		},
	}
}

// --- Handlers ---

func handleListTrips(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}

	list := store.Active()
	var summary string
	if len(list) == 0 {
		summary = "No active trips. Use create_trip to start planning."
	} else {
		var names []string
		for _, t := range list {
			names = append(names, fmt.Sprintf("%s (%s, %d legs)", t.Name, t.Status, len(t.Legs)))
		}
		summary = fmt.Sprintf("%d active trip(s): %s", len(list), strings.Join(names, "; "))
	}

	// Wrap the result in the schema-declared shape ({trips:[], count:N}) so MCP
	// clients that validate structuredContent against the OutputSchema do not
	// reject it as "expected record, received array". This mirrors the pattern
	// used by every other list-style tool in this package.
	result := map[string]any{
		"trips": list,
		"count": len(list),
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

func handleGetTrip(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	id := argString(args, "id")
	if id == "" {
		return nil, nil, fmt.Errorf("id is required")
	}

	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}

	trip, err := store.Get(id)
	if err != nil {
		return nil, nil, err
	}

	first := trips.FirstLegStart(*trip)
	var countdown string
	if !first.IsZero() {
		d := time.Until(first)
		if d > 0 {
			days := int(d.Hours()) / 24
			countdown = fmt.Sprintf(", departs in %d days", days)
		}
	}
	summary := fmt.Sprintf("Trip: %s (status: %s, %d legs%s)", trip.Name, trip.Status, len(trip.Legs), countdown)

	content, err := buildAnnotatedContentBlocks(summary, trip)
	if err != nil {
		return nil, nil, err
	}
	return content, trip, nil
}

func handleCreateTrip(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	name := argString(args, "name")
	if name == "" {
		return nil, nil, fmt.Errorf("name is required")
	}

	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}

	var tags []string
	if tagsStr := argString(args, "tags"); tagsStr != "" {
		for _, tag := range strings.Split(tagsStr, ",") {
			if t := strings.TrimSpace(tag); t != "" {
				tags = append(tags, t)
			}
		}
	}

	t := trips.Trip{
		Name:   name,
		Status: argString(args, "status"),
		Notes:  argString(args, "notes"),
		Tags:   tags,
	}

	id, err := store.Add(t)
	if err != nil {
		return nil, nil, err
	}

	result := map[string]string{"id": id, "name": name}
	content, err := buildAnnotatedContentBlocks(fmt.Sprintf("Trip created: %s (ID: %s)", name, id), result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

func handleAddTripLeg(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	tripID := argString(args, "trip_id")
	if tripID == "" {
		return nil, nil, fmt.Errorf("trip_id is required")
	}

	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}

	leg := trips.TripLeg{
		Type:      argString(args, "type"),
		From:      argString(args, "from"),
		To:        argString(args, "to"),
		Provider:  argString(args, "provider"),
		StartTime: argString(args, "start_time"),
		EndTime:   argString(args, "end_time"),
		Price:     argFloat(args, "price", 0),
		Currency:  argString(args, "currency"),
		Confirmed: argBool(args, "confirmed", false),
		Reference: argString(args, "reference"),
	}

	if err := store.Update(tripID, func(t *trips.Trip) error {
		t.Legs = append(t.Legs, leg)
		return nil
	}); err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf("Leg added: %s %s->%s to trip %s", leg.Type, leg.From, leg.To, tripID)
	result := map[string]interface{}{"trip_id": tripID, "leg": leg}
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

func handleMarkTripBooked(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	tripID := argString(args, "trip_id")
	provider := argString(args, "provider")
	reference := argString(args, "reference")

	if tripID == "" || provider == "" || reference == "" {
		return nil, nil, fmt.Errorf("trip_id, provider, and reference are required")
	}

	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}

	booking := trips.Booking{
		Type:        argString(args, "type"),
		Provider:    provider,
		Reference:   reference,
		URL:         argString(args, "url"),
		CandidateID: argString(args, "candidate_id"),
		Price:       argFloat(args, "price", 0),
		Currency:    strings.ToUpper(argString(args, "currency")),
		ConfirmedAt: time.Now(),
		Notes:       argString(args, "notes"),
	}
	if booking.Type == "" {
		booking.Type = "flight"
	}

	var finalStatus string
	var staleWarning string
	if err := store.Update(tripID, func(t *trips.Trip) error {
		if booking.CandidateID != "" {
			for i := range t.Workspace.Candidates {
				cand := &t.Workspace.Candidates[i]
				if cand.ID != booking.CandidateID {
					continue
				}
				if booking.URL == "" {
					booking.URL = cand.URL
				}
				if booking.Price == 0 {
					booking.Price = cand.Price
				}
				if booking.Currency == "" {
					booking.Currency = cand.Currency
				}
				if cand.IsStale(time.Now(), 24*time.Hour) {
					staleWarning = "candidate price or availability was stale when marked booked; keep the manual confirmation as the source of truth"
				}
				cand.Status = "booked"
				break
			}
		}
		t.Bookings = append(t.Bookings, booking)
		if t.Status == "planning" {
			t.Status = "booked"
		}
		finalStatus = t.Status
		return nil
	}); err != nil {
		return nil, nil, err
	}

	result := map[string]string{"trip_id": tripID, "reference": reference, "status": finalStatus, "stale_warning": staleWarning}
	summary := fmt.Sprintf("Booking %s/%s added to trip %s (status: %s)", provider, reference, tripID, finalStatus)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

func exportICSTool() ToolDef {
	return ToolDef{
		Name:        "export_ics",
		Title:       "Export Trip as ICS",
		Description: "Exports a saved trip as an iCalendar (.ics) file. Each leg becomes a VEVENT with start/end times, summary, location, and description. The output is RFC 5545 compliant and can be imported into Apple Calendar, Google Calendar, Outlook, etc.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"trip_id": {Type: "string", Description: "Trip ID (e.g. trip_abc123)"},
			},
			Required: []string{"trip_id"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"ics":         schemaString(),
				"trip_name":   schemaString(),
				"event_count": schemaInt(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Export Trip as ICS",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

func handleExportICS(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	id := argString(args, "trip_id")
	if id == "" {
		return nil, nil, fmt.Errorf("trip_id is required")
	}

	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}

	trip, err := store.Get(id)
	if err != nil {
		return nil, nil, err
	}

	ics, err := calendar.ExportICS(*trip)
	if err != nil {
		return nil, nil, fmt.Errorf("export ICS: %w", err)
	}

	result := map[string]interface{}{
		"ics":         ics,
		"trip_name":   trip.Name,
		"event_count": len(trip.Legs),
	}
	summary := fmt.Sprintf("Exported %d events for trip %q as ICS", len(trip.Legs), trip.Name)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

// defaultTripStore opens and loads the default trip store.
func defaultTripStore() (*trips.Store, error) {
	store, err := trips.DefaultStore()
	if err != nil {
		return nil, fmt.Errorf("open trip store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("load trips: %w", err)
	}
	return store, nil
}
