package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestWrappedSearchFlights_ConcurrentPreferencesFiltering_LiveIntegration(t *testing.T) {
	testutil.RequireLiveIntegration(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := preferences.Save(&preferences.Preferences{
		DisplayCurrency:    "EUR",
		Locale:             "en",
		BudgetFlightMax:    100000,
		FlightTimeEarliest: "00:00",
		FlightTimeLatest:   "23:59",
	}); err != nil {
		t.Fatalf("save preferences: %v", err)
	}

	s := NewServer()
	results := runConcurrentWrappedHandlerCalls(t, s.handlers["search_flights"], map[string]any{
		"origin":         "HEL",
		"destination":    "NRT",
		"departure_date": "2026-05-15",
	}, 10)
	assertWrappedStructuredResultsAreDistinct(t, "search_flights", results)

	for i, result := range results {
		var structured models.FlightSearchResult
		assertWrappedStructuredResult(t, "search_flights", i, result, &structured)
	}
}

func TestWrappedSearchHotels_ConcurrentPreferencesFiltering_LiveIntegration(t *testing.T) {
	testutil.RequireLiveIntegration(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := preferences.Save(&preferences.Preferences{
		DisplayCurrency:    "EUR",
		Locale:             "en",
		BudgetPerNightMin:  1,
		DefaultCompanions:  1,
		PreferredDistricts: map[string][]string{},
	}); err != nil {
		t.Fatalf("save preferences: %v", err)
	}

	s := NewServer()
	results := runConcurrentWrappedHandlerCalls(t, s.handlers["search_hotels"], map[string]any{
		"location":  "Helsinki",
		"check_in":  "2026-05-15",
		"check_out": "2026-05-18",
	}, 10)
	assertWrappedStructuredResultsAreDistinct(t, "search_hotels", results)

	for i, result := range results {
		var structured models.HotelSearchResult
		assertWrappedStructuredResult(t, "search_hotels", i, result, &structured)
	}
}

func TestWrappedSearchGround_ConcurrentLiveIntegration(t *testing.T) {
	testutil.RequireLiveIntegration(t)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	if err := preferences.Save(&preferences.Preferences{
		DisplayCurrency: "EUR",
		Locale:          "en",
	}); err != nil {
		t.Fatalf("save preferences: %v", err)
	}

	s := NewServer()
	results := runConcurrentWrappedHandlerCalls(t, s.handlers["search_ground"], map[string]any{
		"from": "Helsinki",
		"to":   "Espoo",
		"date": "2026-05-15",
	}, 10)
	assertWrappedStructuredResultsAreDistinct(t, "search_ground", results)

	for i, result := range results {
		var structured models.GroundSearchResult
		assertWrappedStructuredResult(t, "search_ground", i, result, &structured)
	}
}

type wrappedHandlerCallResult struct {
	content    []ContentBlock
	structured any
}

func runConcurrentWrappedHandlerCalls(t *testing.T, handler ToolHandler, args map[string]any, callers int) []wrappedHandlerCallResult {
	t.Helper()

	if handler == nil {
		t.Fatal("handler is nil")
	}
	if callers < 2 {
		t.Fatalf("callers = %d, want at least 2", callers)
	}

	results := make([]wrappedHandlerCallResult, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			content, structured, err := handler(context.Background(), cloneArgs(args), nil, nil, nil)
			results[idx] = wrappedHandlerCallResult{
				content:    content,
				structured: structured,
			}
			errs[idx] = err
		}(i)
	}

	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d failed: %v", i, err)
		}
	}

	return results
}

func cloneArgs(args map[string]any) map[string]any {
	cloned := make(map[string]any, len(args))
	for k, v := range args {
		cloned[k] = v
	}
	return cloned
}

func assertWrappedStructuredResult(t *testing.T, tool string, caller int, result wrappedHandlerCallResult, out any) {
	t.Helper()

	if len(result.content) == 0 {
		t.Fatalf("%s caller %d returned no content blocks", tool, caller)
	}
	if result.structured == nil {
		t.Fatalf("%s caller %d returned nil structured content", tool, caller)
	}

	structuredJSON, err := json.Marshal(result.structured)
	if err != nil {
		t.Fatalf("%s caller %d marshal structured content: %v", tool, caller, err)
	}
	if err := json.Unmarshal(structuredJSON, out); err != nil {
		t.Fatalf("%s caller %d unmarshal structured content: %v", tool, caller, err)
	}
}

func assertWrappedStructuredResultsAreDistinct(t *testing.T, tool string, results []wrappedHandlerCallResult) {
	t.Helper()

	if len(results) < 2 || results[0].structured == nil {
		return
	}

	first := reflect.ValueOf(results[0].structured)
	if !pointerLikeKind(first.Kind()) || first.IsNil() {
		return
	}

	firstPtr := first.Pointer()
	for i := 1; i < len(results); i++ {
		current := reflect.ValueOf(results[i].structured)
		if !current.IsValid() || current.Kind() != first.Kind() || !pointerLikeKind(current.Kind()) || current.IsNil() {
			continue
		}
		if current.Pointer() == firstPtr {
			t.Fatalf("%s callers 0 and %d reused the same structured result object", tool, i)
		}
	}
}

func pointerLikeKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return true
	default:
		return false
	}
}
