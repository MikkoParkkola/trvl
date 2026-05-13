package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPromptsList(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "prompts/list", 1, nil)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsListResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Prompts) != 7 {
		t.Fatalf("expected 7 prompts, got %d", len(result.Prompts))
	}

	expected := map[string]bool{
		"plan-trip":           false,
		"where-should-i-go":   false,
		"find-cheapest-dates": false,
		"compare-hotels":      false,
		"packing-list":        false,
		"setup_profile":       false,
		"setup_providers":     false,
	}
	for _, p := range result.Prompts {
		if _, ok := expected[p.Name]; !ok {
			t.Errorf("unexpected prompt: %s", p.Name)
		}
		expected[p.Name] = true
		if p.Description == "" {
			t.Errorf("prompt %s has empty description", p.Name)
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing prompt: %s", name)
		}
	}
}

func TestPromptsGet_PlanTrip(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "plan-trip",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-06-15",
			"return_date":    "2026-06-22",
			"budget":         "3000",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user", result.Messages[0].Role)
	}
	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "HEL") || !strings.Contains(text, "NRT") {
		t.Error("prompt should contain origin and destination")
	}
	if !strings.Contains(text, "3000") {
		t.Error("prompt should contain budget")
	}
}

func TestPromptsGet_PlanTrip_MissingArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name:      "plan-trip",
		Arguments: map[string]any{"origin": "HEL"},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing args")
	}
}

func TestPromptsGet_FindCheapestDates(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "find-cheapest-dates",
		Arguments: map[string]any{
			"origin":      "HEL",
			"destination": "NRT",
			"month":       "june-2026",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if !strings.Contains(result.Messages[0].Content.Text, "june-2026") {
		t.Error("prompt should contain month")
	}
}

func TestPromptsGet_CompareHotels(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "compare-hotels",
		Arguments: map[string]any{
			"location":   "Tokyo",
			"check_in":   "2026-06-15",
			"check_out":  "2026-06-22",
			"priorities": "price,rating",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if !strings.Contains(result.Messages[0].Content.Text, "price,rating") {
		t.Error("prompt should contain priorities")
	}
}

func TestPromptsGet_UnknownPrompt(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{Name: "nonexistent"}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown prompt")
	}
}

// --- promptWhereShouldIGo ---

func TestPromptsGet_WhereShouldIGo_Basic(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "where-should-i-go",
		Arguments: map[string]any{
			"origin": "HEL",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user", result.Messages[0].Role)
	}
	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "HEL") {
		t.Error("prompt should contain origin HEL")
	}
}

func TestPromptsGet_WhereShouldIGo_WithMonth(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "where-should-i-go",
		Arguments: map[string]any{
			"origin": "JFK",
			"month":  "july-2026",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "july-2026") {
		t.Error("prompt should contain month")
	}
	if !strings.Contains(text, "JFK") {
		t.Error("prompt should contain origin")
	}
}

func TestPromptsGet_WhereShouldIGo_WithBudget(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "where-should-i-go",
		Arguments: map[string]any{
			"origin": "CDG",
			"budget": "500",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "500") {
		t.Error("prompt should contain budget")
	}
}

func TestPromptsGet_WhereShouldIGo_MissingOrigin(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name:      "where-should-i-go",
		Arguments: map[string]any{},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing origin")
	}
}

func TestPromptsGet_WhereShouldIGo_AllArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "where-should-i-go",
		Arguments: map[string]any{
			"origin": "SIN",
			"month":  "december-2026",
			"budget": "1000",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	if result.Description == "" {
		t.Error("description should not be empty")
	}
	if !strings.Contains(result.Description, "SIN") {
		t.Error("description should contain origin")
	}
	if !strings.Contains(result.Description, "december-2026") {
		t.Error("description should contain month")
	}
}

// --- promptPlanTrip without budget ---

func TestPromptsGet_PlanTrip_NoBudget(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "plan-trip",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-06-15",
			"return_date":    "2026-06-22",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	text := result.Messages[0].Content.Text
	// Without budget, the budget line should not appear.
	if strings.Contains(text, "total budget") {
		t.Error("prompt should not contain budget line when no budget provided")
	}
}

// --- promptCompareHotels missing args ---

func TestPromptsGet_CompareHotels_MissingArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name:      "compare-hotels",
		Arguments: map[string]any{"location": "Tokyo"},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing check_in/check_out")
	}
}

// --- promptCompareHotels default priorities ---

func TestPromptsGet_CompareHotels_DefaultPriorities(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "compare-hotels",
		Arguments: map[string]any{
			"location":  "Paris",
			"check_in":  "2026-09-01",
			"check_out": "2026-09-05",
			// No priorities -- should default to "price,rating"
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "price,rating") {
		t.Error("prompt should use default priorities price,rating")
	}
}

// --- getPrompt unknown prompt ---

func TestGetPrompt_Unknown(t *testing.T) {
	t.Parallel()
	_, err := getPrompt("nonexistent", nil)
	if err == nil {
		t.Error("expected error for unknown prompt")
	}
}

// --- argOr ---

// --- promptPackingList ---

func TestPromptsGet_PackingList_Basic(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "packing-list",
		Arguments: map[string]any{
			"destination": "Tokyo",
			"dates":       "2026-06-15 to 2026-06-22",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Messages) == 0 {
		t.Fatal("expected messages")
	}
	if result.Messages[0].Role != "user" {
		t.Errorf("role = %q, want user", result.Messages[0].Role)
	}
	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "Tokyo") {
		t.Error("prompt should contain destination")
	}
	if !strings.Contains(text, "2026-06-15 to 2026-06-22") {
		t.Error("prompt should contain dates")
	}
	if !strings.Contains(text, "leisure") {
		t.Error("prompt should default to leisure trip type")
	}
}

func TestPromptsGet_PackingList_AllArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name: "packing-list",
		Arguments: map[string]any{
			"destination": "Iceland",
			"dates":       "2026-12-20 to 2026-12-27",
			"trip_type":   "adventure",
			"activities":  "hiking, hot springs, northern lights",
		},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result PromptsGetResult
	_ = json.Unmarshal(resultJSON, &result)

	text := result.Messages[0].Content.Text
	if !strings.Contains(text, "adventure") {
		t.Error("prompt should contain trip type")
	}
	if !strings.Contains(text, "hiking, hot springs, northern lights") {
		t.Error("prompt should contain activities")
	}
	if !strings.Contains(text, "Iceland") {
		t.Error("prompt should contain destination")
	}
	if result.Description == "" {
		t.Error("description should not be empty")
	}
	if !strings.Contains(result.Description, "adventure") {
		t.Error("description should contain trip type")
	}
}

func TestPromptsGet_PackingList_MissingArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name:      "packing-list",
		Arguments: map[string]any{"destination": "Tokyo"},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing dates")
	}
}

func TestArgOr_NilArgs(t *testing.T) {
	t.Parallel()
	got := argOr(nil, "key", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

func TestArgOr_MissingKey(t *testing.T) {
	t.Parallel()
	got := argOr(map[string]any{"other": "val"}, "key", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

func TestArgOr_EmptyStringValue(t *testing.T) {
	t.Parallel()
	got := argOr(map[string]any{"key": ""}, "key", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want fallback for empty string", got)
	}
}

func TestArgOr_NonStringValue(t *testing.T) {
	t.Parallel()
	got := argOr(map[string]any{"key": 42}, "key", "fallback")
	if got != "fallback" {
		t.Errorf("got %q, want fallback for non-string value", got)
	}
}

func TestArgOr_ValidValue(t *testing.T) {
	t.Parallel()
	got := argOr(map[string]any{"key": "value"}, "key", "fallback")
	if got != "value" {
		t.Errorf("got %q, want value", got)
	}
}

// --- promptFindCheapestDates missing args ---

func TestPromptsGet_FindCheapestDates_MissingArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := PromptsGetParams{
		Name:      "find-cheapest-dates",
		Arguments: map[string]any{"origin": "HEL"},
	}
	resp := sendRequest(t, s, "prompts/get", 1, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing destination and month")
	}
}
