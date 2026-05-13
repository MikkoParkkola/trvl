package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

func TestHandleProviderHealth_WithData(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Write health log entries directly to temp dir.
	healthPath := filepath.Join(tmp, ".trvl", "health.jsonl")
	if err := os.MkdirAll(filepath.Dir(healthPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entries := []providers.HealthEntry{
		{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Provider:  "test-provider",
			Operation: "search",
			Status:    "ok",
			LatencyMs: 200,
			Results:   5,
		},
		{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Provider:  "test-provider",
			Operation: "search",
			Status:    "error",
			LatencyMs: 500,
			Error:     "connection refused",
		},
		{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Provider:  "other-provider",
			Operation: "search",
			Status:    "timeout",
			LatencyMs: 15000,
			Error:     "deadline exceeded",
		},
	}

	f, err := os.Create(healthPath)
	if err != nil {
		t.Fatalf("create health log: %v", err)
	}
	for _, e := range entries {
		line, _ := json.Marshal(e)
		_, _ = f.Write(append(line, '\n'))
	}
	_ = f.Close()

	content, structured, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "test-provider") {
		t.Errorf("expected provider name in response, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
	// Structured should be a map with "providers" key.
	data, _ := json.Marshal(structured)
	var parsed struct {
		Providers []struct {
			Provider   string `json:"provider"`
			TotalCalls int    `json:"total_calls"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if len(parsed.Providers) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(parsed.Providers))
	}
}

func TestHandleProviderHealth_WithErrorsAndTimeouts(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	healthPath := filepath.Join(tmp, ".trvl", "health.jsonl")
	if err := os.MkdirAll(filepath.Dir(healthPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entry := providers.HealthEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Provider:  "flaky-provider",
		Operation: "search",
		Status:    "error",
		LatencyMs: 100,
		Error:     "timeout",
	}
	line, _ := json.Marshal(entry)
	_ = os.WriteFile(healthPath, append(line, '\n'), 0o600)

	content, _, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(content[0].Text, "flaky-provider") {
		t.Errorf("expected provider name, got: %s", content[0].Text)
	}
	// Error count should show in text.
	if !containsString(content[0].Text, "errors") {
		t.Errorf("expected error count in response, got: %s", content[0].Text)
	}
}

// ============================================================
// readTripsUpcoming — empty and with trip data
// ============================================================

func TestReadTripsUpcoming_Empty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := NewServer()
	result, err := s.readTripsUpcoming()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !containsString(result.Contents[0].Text, "No upcoming trips") {
		t.Errorf("expected empty message, got: %s", result.Contents[0].Text)
	}
}

// ============================================================
// readTripsList / readTripsAlerts — empty home dir
// ============================================================

func TestReadTripsList_Empty(t *testing.T) {
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmp)
	defer func() { _ = os.Setenv("HOME", orig) }()

	s := NewServer()
	result, err := s.readTripsList()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if result.Contents[0].MimeType != "application/json" {
		t.Errorf("expected JSON mime type, got: %s", result.Contents[0].MimeType)
	}
}

func TestReadTripsAlerts_Empty(t *testing.T) {
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmp)
	defer func() { _ = os.Setenv("HOME", orig) }()

	s := NewServer()
	result, err := s.readTripsAlerts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if result.Contents[0].MimeType != "application/json" {
		t.Errorf("expected JSON mime type, got: %s", result.Contents[0].MimeType)
	}
}

// ============================================================
// readWatchResource — ID-based lookup via watch store
// ============================================================

func TestReadWatchResource_InvalidLegacyURI(t *testing.T) {
	t.Parallel()
	s := NewServer()
	s.watchStore = newWatchStore(t)

	// Incomplete legacy format → error.
	_, err := s.readWatchResource("trvl://watch/only-one-part")
	if err == nil {
		t.Fatal("expected error for malformed watch URI")
	}
}

func TestReadWatchResource_IDLookup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := NewServer()
	store := watch.NewStore(tmp)
	if err := store.Load(); err != nil {
		t.Fatalf("store load: %v", err)
	}
	s.watchStore = store

	// Add a watch and get its ID.
	id, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2099-07-01",
		BelowPrice:  300,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("add watch: %v", err)
	}

	// readWatchResource should resolve the ID via the store.
	result, err := s.readWatchResource("trvl://watch/" + id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !containsString(result.Contents[0].Text, "HEL") {
		t.Errorf("expected origin in result, got: %s", result.Contents[0].Text)
	}
}

// ============================================================
// handleToolsCall — extra path coverage (tool not found via RPC)
// ============================================================

func TestHandleToolsCall_MissingNameParam(t *testing.T) {
	t.Parallel()
	s := NewServer()
	// Params with no "name" field → should return an error.
	params := map[string]any{
		"arguments": map[string]any{},
	}
	resp := sendRequest(t, s, "tools/call", 99, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error response for missing tool name")
	}
}

// ============================================================
// handleBuildProfile — path coverage via testable core
// ============================================================

func TestHandleBuildProfileWithPath_NoBookings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	content, structured, err := handleBuildProfileWithPath(map[string]any{}, path, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

func TestHandleBuildProfileWithPath_EmailSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	content, structured, err := handleBuildProfileWithPath(map[string]any{"source": "email"}, path, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "Gmail") {
		t.Errorf("expected Gmail instruction, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

func TestHandleBuildProfileWithPath_InvalidSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	_, _, err := handleBuildProfileWithPath(map[string]any{"source": "bad-source"}, path, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}

// ============================================================
// handleAddBookingWithPath — coverage
// ============================================================

func TestHandleAddBookingWithPath_MissingType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	_, _, err := handleAddBookingWithPath(map[string]any{
		"provider": "Finnair",
	}, path, nil)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestHandleAddBookingWithPath_MissingProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	_, _, err := handleAddBookingWithPath(map[string]any{
		"type": "flight",
	}, path, nil)
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestHandleAddBookingWithPath_FlightSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	content, structured, err := handleAddBookingWithPath(map[string]any{
		"type":     "flight",
		"provider": "Finnair",
		"from":     "HEL",
		"to":       "BCN",
		"price":    350.0,
		"currency": "EUR",
		"date":     "2026-05-01",
	}, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "Finnair") {
		t.Errorf("expected provider in response, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

// ============================================================
// handleInterviewTripWithPath — coverage
// ============================================================

func TestHandleInterviewTripWithPath_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	prefsPath := filepath.Join(dir, "prefs.json")

	content, structured, err := handleInterviewTripWithPath(map[string]any{}, profilePath, prefsPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

// ============================================================
// Tool definition completeness — watch price tools
// ============================================================

func TestWatchPriceTool_Definition(t *testing.T) {
	t.Parallel()
	tool := watchPriceTool()
	if tool.Name != "watch_price" {
		t.Errorf("Name = %q, want watch_price", tool.Name)
	}
	if len(tool.InputSchema.Required) < 2 {
		t.Errorf("Required = %v, want at least 2", tool.InputSchema.Required)
	}
	if tool.Annotations == nil {
		t.Fatal("annotations should be set")
	}
	if tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be false for write tool")
	}
}

func TestListWatchesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := listWatchesTool()
	if tool.Name != "list_watches" {
		t.Errorf("Name = %q, want list_watches", tool.Name)
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true")
	}
}

func TestCheckWatchesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := checkWatchesTool()
	if tool.Name != "check_watches" {
		t.Errorf("Name = %q, want check_watches", tool.Name)
	}
	if !tool.Annotations.OpenWorldHint {
		t.Error("OpenWorldHint should be true (makes live requests)")
	}
}

func TestProviderHealthTool_Definition(t *testing.T) {
	t.Parallel()
	tool := providerHealthTool()
	if tool.Name != "provider_health" {
		t.Errorf("Name = %q, want provider_health", tool.Name)
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true")
	}
}

// ============================================================
// handleConfigureProvider — elicit timeout path
// ============================================================

func TestHandleConfigureProvider_ElicitTimeout(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "timeout-test",
		"name":         "Timeout Provider",
		"category":     "hotels",
		"endpoint":     "https://api.example.com/search",
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name": "$.hotel_name",
		},
	}

	elicit := func(message string, schema map[string]interface{}) (map[string]interface{}, error) {
		return nil, fmt.Errorf("deadline exceeded waiting for user response")
	}

	content, _, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "timed out") {
		t.Errorf("expected timeout message, got: %s", content[0].Text)
	}
}

// ============================================================
// handleSuggestProviders — category filter
// ============================================================

func TestHandleSuggestProviders_CategoryFilter(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	content, structured, err := handleSuggestProviders(context.Background(), map[string]any{
		"category": "hotels",
	}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	// All results should be hotels.
	suggestions, ok := structured.([]providerSuggestion)
	if !ok {
		t.Fatalf("structured type = %T, want []providerSuggestion", structured)
	}
	for _, s := range suggestions {
		if s.Category != "hotels" {
			t.Errorf("provider %q has category %q, want hotels", s.ID, s.Category)
		}
	}
}

func TestHandleSuggestProviders_EmptyCategory(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	_, _, err := handleSuggestProviders(context.Background(), map[string]any{
		"category": "nonexistent_category",
	}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSuggestProviders_ConfiguredMarked(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)

	// Use the first actual catalog provider ID: "booking".
	config := &providers.ProviderConfig{
		ID:       "booking",
		Name:     "Booking.com",
		Category: "hotels",
		Endpoint: "https://www.booking.com/dml/graphql",
		Method:   "POST",
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: "$.data.searchQueries.search.results",
			Fields:      map[string]string{"name": "$.basicPropertyData.name"},
		},
	}
	_ = reg.Save(config)

	_, structured, err := handleSuggestProviders(context.Background(), map[string]any{}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	suggestions, ok := structured.([]providerSuggestion)
	if !ok {
		t.Fatalf("structured type = %T", structured)
	}

	// At least one provider must appear. Verify "booking" is marked configured.
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	found := false
	for _, s := range suggestions {
		if s.ID == "booking" {
			found = true
			if !s.Configured {
				t.Error("booking should be marked as configured")
			}
			break
		}
	}
	if !found {
		t.Errorf("booking not found in %d suggestions", len(suggestions))
	}
}

// ============================================================
// buildAnnotatedContentBlocks — error path (non-marshallable)
// ============================================================

func TestBuildAnnotatedContentBlocks_NonMarshalable(t *testing.T) {
	t.Parallel()
	// channels cannot be marshalled → should return error.
	_, err := buildAnnotatedContentBlocks("summary", make(chan int))
	if err == nil {
		t.Fatal("expected error for non-marshallable structured data")
	}
}

func TestBuildAnnotatedContentBlocks_Nil(t *testing.T) {
	t.Parallel()
	// nil structured → should succeed with just the text block.
	blocks, err := buildAnnotatedContentBlocks("hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one content block")
	}
	if !strings.Contains(blocks[0].Text, "hello") {
		t.Errorf("expected 'hello' in content, got: %s", blocks[0].Text)
	}
}
