package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

// --- watch_opportunities tool ---

func watchOpportunitiesTool() ToolDef {
	return ToolDef{
		Name:  "watch_opportunities",
		Title: "Watch Opportunities",
		Description: "Create an opportunity watch that monitors a rolling time window " +
			"for deals to your favourite destinations. " +
			"Favourites default to your profile BucketList × AirportAffinity if not specified. " +
			"Use list_opportunity_watches to see all active opportunity watches.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"favourites":  {Type: "array", Items: &Property{Type: "string"}, Description: "List of destination IATA codes (optional; defaults to profile)"},
				"window_from": {Type: "string", Description: "Window start: YYYY-MM-DD or next_Nd (e.g. \"next_30d\"). Default: next_30d"},
				"window_to":   {Type: "string", Description: "Window end: YYYY-MM-DD or next_Nd (e.g. \"next_90d\"). Default: next_90d"},
				"min_score":   {Type: "integer", Description: "Minimum composite score to alert (0-100). Default: 85"},
				"min_nights":  {Type: "integer", Description: "Minimum trip length in nights. Default: 3"},
				"max_nights":  {Type: "integer", Description: "Maximum trip length in nights. Default: 14"},
			},
			Required: []string{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success":    schemaBool(),
				"watch_id":   schemaString(),
				"created_at": schemaString(),
				"error":      schemaString(),
			},
			"required": []string{"success"},
		},
		Annotations: &ToolAnnotations{
			Title:          "Watch Opportunities",
			ReadOnlyHint:   false,
			IdempotentHint: false,
			OpenWorldHint:  false,
		},
	}
}

func handleWatchOpportunities(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	var favourites []string
	for _, v := range argStringSlice(args, "favourites") {
		code := strings.ToUpper(strings.TrimSpace(v))
		if code != "" {
			favourites = append(favourites, code)
		}
	}

	windowFrom := argString(args, "window_from")
	if windowFrom == "" {
		windowFrom = "next_30d"
	}
	windowTo := argString(args, "window_to")
	if windowTo == "" {
		windowTo = "next_90d"
	}

	minScore := argInt(args, "min_score", 85)
	minNights := argInt(args, "min_nights", 3)
	maxNights := argInt(args, "max_nights", 14)

	w := watch.Watch{
		Type:       "opportunity",
		Favourites: favourites,
		WindowFrom: windowFrom,
		WindowTo:   windowTo,
		MinScore:   minScore,
		MinNights:  minNights,
		MaxNights:  maxNights,
	}

	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watch store: %w", err)
	}

	id, err := store.Add(w)
	if err != nil {
		return nil, nil, fmt.Errorf("add opportunity watch: %w", err)
	}

	type oppResponse struct {
		Success    bool     `json:"success"`
		WatchID    string   `json:"watch_id"`
		CreatedAt  string   `json:"created_at"`
		Favourites []string `json:"favourites,omitempty"`
		WindowFrom string   `json:"window_from"`
		WindowTo   string   `json:"window_to"`
		MinScore   int      `json:"min_score"`
		MinNights  int      `json:"min_nights"`
		MaxNights  int      `json:"max_nights"`
	}

	resp := oppResponse{
		Success:    true,
		WatchID:    id,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Favourites: favourites,
		WindowFrom: windowFrom,
		WindowTo:   windowTo,
		MinScore:   minScore,
		MinNights:  minNights,
		MaxNights:  maxNights,
	}

	var favStr string
	if len(favourites) > 0 {
		favStr = strings.Join(favourites, ", ")
	} else {
		favStr = "(from profile)"
	}
	summary := fmt.Sprintf(
		"Opportunity watch %s created: destinations=%s, window=%s to %s, min_score=%d, nights=%d-%d.",
		id, favStr, windowFrom, windowTo, minScore, minNights, maxNights,
	)

	content, err := buildAnnotatedContentBlocks(summary, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}

// --- list_opportunity_watches tool ---

func listOpportunityWatchesTool() ToolDef {
	return ToolDef{
		Name:        "list_opportunity_watches",
		Title:       "List Opportunity Watches",
		Description: "List all opportunity watches stored in ~/.trvl/watches.json.",
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
						"id":          schemaString(),
						"favourites":  schemaArray(map[string]interface{}{"type": "string"}),
						"window_from": schemaString(),
						"window_to":   schemaString(),
						"min_score":   schemaInt(),
						"min_nights":  schemaInt(),
						"max_nights":  schemaInt(),
						"created_at":  schemaString(),
					},
				}),
				"count": schemaInt(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "List Opportunity Watches",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

func handleListOpportunityWatches(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
	store, err := watch.DefaultStore()
	if err != nil {
		return nil, nil, fmt.Errorf("open watch store: %w", err)
	}
	if err := store.Load(); err != nil {
		return nil, nil, fmt.Errorf("load watch store: %w", err)
	}

	type oppWatchSummary struct {
		ID         string   `json:"id"`
		Favourites []string `json:"favourites,omitempty"`
		WindowFrom string   `json:"window_from"`
		WindowTo   string   `json:"window_to"`
		MinScore   int      `json:"min_score"`
		MinNights  int      `json:"min_nights"`
		MaxNights  int      `json:"max_nights"`
		CreatedAt  string   `json:"created_at"`
	}

	var summaries []oppWatchSummary
	for _, w := range store.List() {
		if !w.IsOpportunityWatch() {
			continue
		}
		summaries = append(summaries, oppWatchSummary{
			ID:         w.ID,
			Favourites: w.Favourites,
			WindowFrom: w.WindowFrom,
			WindowTo:   w.WindowTo,
			MinScore:   w.MinScore,
			MinNights:  w.MinNights,
			MaxNights:  w.MaxNights,
			CreatedAt:  w.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	type listResponse struct {
		Watches []oppWatchSummary `json:"watches"`
		Count   int               `json:"count"`
	}

	if summaries == nil {
		summaries = []oppWatchSummary{}
	}
	resp := listResponse{Watches: summaries, Count: len(summaries)}

	var text string
	if len(summaries) == 0 {
		text = "No opportunity watches found. Use watch_opportunities to create one."
	} else {
		text = fmt.Sprintf("%d opportunity watch(es):\n", len(summaries))
		for _, ws := range summaries {
			favStr := strings.Join(ws.Favourites, ", ")
			if favStr == "" {
				favStr = "(from profile)"
			}
			text += fmt.Sprintf("  [%s] %s | %s→%s | min_score=%d nights=%d-%d\n",
				ws.ID, favStr, ws.WindowFrom, ws.WindowTo, ws.MinScore, ws.MinNights, ws.MaxNights)
		}
	}

	content, err := buildAnnotatedContentBlocks(text, resp)
	if err != nil {
		return nil, nil, err
	}
	return content, resp, nil
}
