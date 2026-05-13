package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/fareintel"
	reservationimport "github.com/MikkoParkkola/trvl/internal/imports"
	"github.com/MikkoParkkola/trvl/internal/itinerary"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

func tripWorkspaceTool() ToolDef {
	return ToolDef{
		Name:        "trip_workspace",
		Title:       "Trip Workspace",
		Description: "Manage the local-first traveller workspace: import confirmations, export trips, save booking candidates, run map-aware itinerary checks, and get conservative fare intelligence. No automatic purchases or cancellations.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"action":            {Type: "string", Description: "get, export_json, export_markdown, import_json, import_reservation, optimize_itinerary, fare_intelligence, save_candidate, booking_ready"},
				"trip_id":           {Type: "string", Description: "Trip ID for actions that read or mutate a saved trip"},
				"name":              {Type: "string", Description: "Trip name when importing a new workspace without a trip_id"},
				"json":              {Type: "string", Description: "Trip Workspace JSON for import_json"},
				"subject":           {Type: "string", Description: "Reservation email/calendar subject for import_reservation"},
				"body":              {Type: "string", Description: "Reservation email/calendar body for import_reservation"},
				"source":            {Type: "string", Description: "Reservation source label, e.g. email, calendar, manual"},
				"type":              {Type: "string", Description: "Candidate type, e.g. flight, hotel, ground"},
				"provider":          {Type: "string", Description: "Candidate provider"},
				"title":             {Type: "string", Description: "Candidate title"},
				"price":             {Type: "number", Description: "Candidate or current fare price"},
				"currency":          {Type: "string", Description: "ISO currency code"},
				"url":               {Type: "string", Description: "Manual booking/provider URL"},
				"checked_at":        {Type: "string", Description: "RFC3339 time when candidate price/evidence was checked"},
				"expires_at":        {Type: "string", Description: "RFC3339 time after which the candidate must be rechecked"},
				"candidate_id":      {Type: "string", Description: "Saved booking candidate ID"},
				"max_route_minutes": {Type: "integer", Description: "Overpacked-day threshold for optimize_itinerary"},
				"history":           {Type: "array", Items: &Property{Type: "object"}, Description: "Fare history points for fare_intelligence; each item has price, currency, timestamp"},
			},
			Required: []string{"action"},
		},
		OutputSchema: schemaObject(),
		Annotations: &ToolAnnotations{
			Title:           "Trip Workspace",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  false,
			OpenWorldHint:   false,
		},
	}
}

func handleTripWorkspace(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	action := normalizeSmartToken(argString(args, "action"))
	switch action {
	case "get", "show":
		return workspaceGet(args)
	case "export", "export_json", "json_export":
		return workspaceExport(args, "json")
	case "export_markdown", "markdown_export", "md":
		return workspaceExport(args, "markdown")
	case "import", "import_json", "json_import":
		return workspaceImportJSON(args)
	case "import_reservation", "reservation_import", "parse_reservation":
		return workspaceImportReservation(args)
	case "optimize_itinerary", "itinerary", "map_check":
		return workspaceOptimizeItinerary(args)
	case "fare_intelligence", "fare", "buy_wait":
		return workspaceFareIntelligence(args)
	case "save_candidate", "candidate":
		return workspaceSaveCandidate(args)
	case "booking_ready", "readiness":
		return workspaceBookingReadiness(args)
	default:
		return nil, nil, fmt.Errorf("unknown trip_workspace action %q", action)
	}
}

func workspaceGet(args map[string]any) ([]ContentBlock, interface{}, error) {
	trip, err := loadWorkspaceTrip(args)
	if err != nil {
		return nil, nil, err
	}
	summary := fmt.Sprintf("Trip workspace: %s (%d legs, %d candidates, %d imported records)", trip.Name, len(trip.Legs), len(trip.Workspace.Candidates), len(trip.Workspace.ImportedRecords))
	return annotated(summary, trip)
}

func workspaceExport(args map[string]any, format string) ([]ContentBlock, interface{}, error) {
	trip, err := loadWorkspaceTrip(args)
	if err != nil {
		return nil, nil, err
	}
	if format == "markdown" {
		md := trips.ExportMarkdown(*trip)
		result := map[string]any{"format": "markdown", "trip_id": trip.ID, "content": md}
		return annotated(fmt.Sprintf("Exported trip %s as Markdown", trip.ID), result)
	}
	data, err := trips.ExportJSON(*trip)
	if err != nil {
		return nil, nil, fmt.Errorf("export workspace JSON: %w", err)
	}
	result := map[string]any{"format": "json", "trip_id": trip.ID, "content": string(data)}
	return annotated(fmt.Sprintf("Exported trip %s as Trip Workspace JSON", trip.ID), result)
}

func workspaceImportJSON(args map[string]any) ([]ContentBlock, interface{}, error) {
	raw := argString(args, "json")
	if raw == "" {
		return nil, nil, fmt.Errorf("json is required")
	}
	incoming, err := trips.ImportJSON([]byte(raw))
	if err != nil {
		return nil, nil, err
	}
	if name := argString(args, "name"); name != "" {
		incoming.Name = name
	}
	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}
	tripID := argString(args, "trip_id")
	if tripID == "" {
		id, err := store.Add(incoming)
		if err != nil {
			return nil, nil, err
		}
		trip, err := store.Get(id)
		if err != nil {
			return nil, nil, err
		}
		result := map[string]any{"trip_id": id, "trip": trip, "summary": trips.MergeSummary{}}
		return annotated(fmt.Sprintf("Imported workspace as new trip %s", id), result)
	}

	var merged trips.Trip
	var summary trips.MergeSummary
	if err := store.Update(tripID, func(t *trips.Trip) error {
		var s trips.MergeSummary
		*t, s = trips.MergeTripWorkspace(*t, incoming)
		merged = *t
		summary = s
		return nil
	}); err != nil {
		return nil, nil, err
	}
	result := map[string]any{"trip_id": tripID, "trip": merged, "summary": summary}
	return annotated(fmt.Sprintf("Merged workspace into trip %s (%d legs, %d records)", tripID, summary.LegsAdded, summary.ImportedRecordsAdded), result)
}

func workspaceImportReservation(args map[string]any) ([]ContentBlock, interface{}, error) {
	tripID := argString(args, "trip_id")
	if tripID == "" {
		return nil, nil, fmt.Errorf("trip_id is required")
	}
	artifacts, err := reservationimport.ParseReservationText(argString(args, "subject"), argString(args, "body"), argString(args, "source"))
	if err != nil {
		return nil, nil, err
	}
	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}
	var merged trips.Trip
	var summary trips.MergeSummary
	if err := store.Update(tripID, func(t *trips.Trip) error {
		var s trips.MergeSummary
		*t, s = trips.MergeReservationArtifacts(*t, artifacts.Records, artifacts.Legs, artifacts.Actions)
		merged = *t
		summary = s
		return nil
	}); err != nil {
		return nil, nil, err
	}
	result := map[string]any{"trip_id": tripID, "trip": merged, "summary": summary, "records": artifacts.Records}
	return annotated(fmt.Sprintf("Imported %d reservation record(s) into trip %s", summary.ImportedRecordsAdded, tripID), result)
}

func workspaceOptimizeItinerary(args map[string]any) ([]ContentBlock, interface{}, error) {
	tripID := argString(args, "trip_id")
	if tripID == "" {
		return nil, nil, fmt.Errorf("trip_id is required")
	}
	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}
	maxMinutes := argInt(args, "max_route_minutes", 180)
	var result itinerary.Result
	if err := store.Update(tripID, func(t *trips.Trip) error {
		result = itinerary.Optimize(*t, itinerary.Options{MaxRouteMinutesPerDay: maxMinutes})
		t.Workspace.Days = result.Days
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return annotated(fmt.Sprintf("Optimized itinerary for trip %s (%d day plan(s), %d warning(s))", tripID, len(result.Days), len(result.Warnings)), result)
}

func workspaceFareIntelligence(args map[string]any) ([]ContentBlock, interface{}, error) {
	history, err := parsePriceHistory(args["history"])
	if err != nil {
		return nil, nil, err
	}
	result := fareintel.Analyze(argFloat(args, "price", 0), argString(args, "currency"), history)
	return annotated(fmt.Sprintf("Fare verdict: %s (%s confidence)", result.Verdict, result.Confidence), result)
}

func workspaceSaveCandidate(args map[string]any) ([]ContentBlock, interface{}, error) {
	tripID := argString(args, "trip_id")
	if tripID == "" {
		return nil, nil, fmt.Errorf("trip_id is required")
	}
	cand := candidateFromArgs(args)
	if cand.Title == "" || cand.Type == "" {
		return nil, nil, fmt.Errorf("type and title are required")
	}
	store, err := defaultTripStore()
	if err != nil {
		return nil, nil, err
	}
	var saved trips.BookingCandidate
	if err := store.Update(tripID, func(t *trips.Trip) error {
		t.Workspace.Candidates = trips.MergeCandidates(t.Workspace.Candidates, []trips.BookingCandidate{cand})
		for _, c := range t.Workspace.Candidates {
			if c.ID == cand.ID {
				saved = c
				break
			}
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	result := map[string]any{"trip_id": tripID, "candidate": saved, "stale": saved.IsStale(time.Now(), 24*time.Hour)}
	return annotated(fmt.Sprintf("Saved %s candidate %s for trip %s", saved.Type, saved.ID, tripID), result)
}

func workspaceBookingReadiness(args map[string]any) ([]ContentBlock, interface{}, error) {
	trip, err := loadWorkspaceTrip(args)
	if err != nil {
		return nil, nil, err
	}
	cand, err := selectCandidate(*trip, argString(args, "candidate_id"))
	if err != nil {
		return nil, nil, err
	}
	stale := cand.IsStale(time.Now(), 24*time.Hour)
	var blockers []string
	if stale {
		blockers = append(blockers, "candidate price or availability must be rechecked")
	}
	if cand.URL == "" {
		blockers = append(blockers, "manual booking URL is missing")
	}
	if cand.Price <= 0 {
		blockers = append(blockers, "candidate price is missing")
	}
	result := map[string]any{
		"trip_id":     trip.ID,
		"candidate":   cand,
		"ready":       len(blockers) == 0,
		"blockers":    blockers,
		"stale":       stale,
		"manual_only": true,
	}
	return annotated(fmt.Sprintf("Booking readiness for %s: %t", cand.ID, len(blockers) == 0), result)
}

func loadWorkspaceTrip(args map[string]any) (*trips.Trip, error) {
	tripID := argString(args, "trip_id")
	if tripID == "" {
		return nil, fmt.Errorf("trip_id is required")
	}
	store, err := defaultTripStore()
	if err != nil {
		return nil, err
	}
	return store.Get(tripID)
}

func candidateFromArgs(args map[string]any) trips.BookingCandidate {
	now := time.Now()
	cand := trips.BookingCandidate{
		ID:       argString(args, "candidate_id"),
		Type:     strings.ToLower(strings.TrimSpace(argString(args, "type"))),
		Provider: argString(args, "provider"),
		Title:    argString(args, "title"),
		Price:    argFloat(args, "price", 0),
		Currency: strings.ToUpper(argString(args, "currency")),
		URL:      argString(args, "url"),
		Status:   "candidate",
	}
	if checked := parseOptionalTime(argString(args, "checked_at")); !checked.IsZero() {
		cand.CheckedAt = checked
	} else {
		cand.CheckedAt = now
	}
	cand.ExpiresAt = parseOptionalTime(argString(args, "expires_at"))
	if cand.ID == "" {
		cand.ID = trips.CandidateID(cand)
	}
	return cand
}

func selectCandidate(trip trips.Trip, candidateID string) (trips.BookingCandidate, error) {
	if len(trip.Workspace.Candidates) == 0 {
		return trips.BookingCandidate{}, fmt.Errorf("trip has no booking candidates")
	}
	if candidateID == "" {
		return trip.Workspace.Candidates[len(trip.Workspace.Candidates)-1], nil
	}
	for _, cand := range trip.Workspace.Candidates {
		if cand.ID == candidateID {
			return cand, nil
		}
	}
	return trips.BookingCandidate{}, fmt.Errorf("candidate %q not found", candidateID)
}

func parsePriceHistory(raw any) ([]watch.PricePoint, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode price history: %w", err)
	}
	var history []watch.PricePoint
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("decode price history: %w", err)
	}
	return history, nil
}

func parseOptionalTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func annotated(summary string, result interface{}) ([]ContentBlock, interface{}, error) {
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}
