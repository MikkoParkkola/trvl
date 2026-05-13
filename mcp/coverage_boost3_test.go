package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/lounges"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

// ============================================================
// handleOnboardProfileWithPath — all phase and error paths
// ============================================================

func TestHandleOnboardProfileWithPath_Phase1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	args := map[string]any{"phase": 1}
	content, structured, err := handleOnboardProfileWithPath(args, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected content blocks")
	}
	if structured == nil {
		t.Error("expected structured output")
	}
}

func TestHandleOnboardProfileWithPath_Phase4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	args := map[string]any{"phase": 4}
	content, structured, err := handleOnboardProfileWithPath(args, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected content blocks for phase 4")
	}
	if structured == nil {
		t.Error("expected structured output for phase 4")
	}
	// nextPhase should be clamped to 4.
	res, ok := structured.(map[string]interface{})
	if !ok {
		t.Fatal("structured output is not a map")
	}
	if np, ok := res["next_phase"].(int); ok && np > 4 {
		t.Errorf("next_phase = %d, want <= 4", np)
	}
}

func TestHandleOnboardProfileWithPath_InvalidPhase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	for _, phase := range []int{0, 5, -1, 100} {
		args := map[string]any{"phase": phase}
		_, _, err := handleOnboardProfileWithPath(args, path)
		if err == nil {
			t.Errorf("expected error for phase %d", phase)
		}
	}
}

func TestHandleOnboardProfileWithPath_InvalidAnswersJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	args := map[string]any{
		"phase":   1,
		"answers": "not-valid-json",
	}
	_, _, err := handleOnboardProfileWithPath(args, path)
	if err == nil {
		t.Error("expected error for invalid answers JSON")
	}
}

func TestHandleOnboardProfileWithPath_ValidAnswers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	args := map[string]any{
		"phase":   1,
		"answers": `{"home_airport":"HEL"}`,
	}
	content, structured, err := handleOnboardProfileWithPath(args, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Error("expected content blocks")
	}
	if structured == nil {
		t.Error("expected structured output")
	}
}

// ============================================================
// joinStrs — pure helper (66.7% → 100%)
// ============================================================

func TestJoinStrs_Empty(t *testing.T) {
	if got := joinStrs(nil); got != "" {
		t.Errorf("joinStrs(nil) = %q, want empty", got)
	}
	if got := joinStrs([]string{}); got != "" {
		t.Errorf("joinStrs([]) = %q, want empty", got)
	}
}

func TestJoinStrs_Single(t *testing.T) {
	got := joinStrs([]string{"HEL"})
	if got != "HEL" {
		t.Errorf("joinStrs([HEL]) = %q, want HEL", got)
	}
}

func TestJoinStrs_Multiple(t *testing.T) {
	got := joinStrs([]string{"HEL", "LHR", "JFK"})
	if got != "HEL, LHR, JFK" {
		t.Errorf("joinStrs([HEL LHR JFK]) = %q, want 'HEL, LHR, JFK'", got)
	}
}

// ============================================================
// loungeSummary — all branches
// ============================================================

func TestLoungeSummary_NotSuccessNoError(t *testing.T) {
	result := &lounges.SearchResult{
		Success: false,
		Airport: "HEL",
	}
	got := loungeSummary(result, nil)
	if !strings.Contains(got, "No lounge information available") {
		t.Errorf("loungeSummary(failure, no error) = %q, want 'No lounge information available'", got)
	}
}

func TestLoungeSummary_NotSuccessWithError(t *testing.T) {
	result := &lounges.SearchResult{
		Success: false,
		Airport: "HEL",
		Error:   "scrape failed",
	}
	got := loungeSummary(result, nil)
	if !strings.Contains(got, "failed") || !strings.Contains(got, "scrape failed") {
		t.Errorf("loungeSummary(failure, error) = %q, want to contain error detail", got)
	}
}

func TestLoungeSummary_ZeroCount(t *testing.T) {
	result := &lounges.SearchResult{
		Success: true,
		Airport: "HEL",
		Count:   0,
	}
	got := loungeSummary(result, nil)
	if !strings.Contains(got, "No lounges found") {
		t.Errorf("loungeSummary(zero count) = %q, want 'No lounges found'", got)
	}
}

func TestLoungeSummary_WithLounges_NoPrefs(t *testing.T) {
	result := &lounges.SearchResult{
		Success: true,
		Airport: "HEL",
		Count:   2,
		Lounges: []lounges.Lounge{
			{Name: "Finnair Lounge"},
			{Name: "Priority Pass Lounge"},
		},
	}
	got := loungeSummary(result, nil)
	if !strings.Contains(got, "2 lounge(s)") {
		t.Errorf("loungeSummary(2 lounges, no prefs) = %q", got)
	}
}

func TestLoungeSummary_WithPrefs_AccessibleLounges(t *testing.T) {
	result := &lounges.SearchResult{
		Success: true,
		Airport: "HEL",
		Count:   2,
		Lounges: []lounges.Lounge{
			{Name: "Finnair Lounge", AccessibleWith: []string{"Priority Pass"}},
			{Name: "No Access Lounge"},
		},
	}
	prefs := &preferences.Preferences{
		LoungeCards: []string{"Priority Pass"},
	}
	got := loungeSummary(result, prefs)
	if !strings.Contains(got, "free access") {
		t.Errorf("loungeSummary(accessible lounges) = %q, want 'free access'", got)
	}
}

func TestLoungeSummary_WithPrefs_NoAccessibleLounges(t *testing.T) {
	result := &lounges.SearchResult{
		Success: true,
		Airport: "HEL",
		Count:   1,
		Lounges: []lounges.Lounge{
			{Name: "VIP Lounge", AccessibleWith: []string{}},
		},
	}
	prefs := &preferences.Preferences{
		LoungeCards: []string{"Priority Pass"},
	}
	got := loungeSummary(result, prefs)
	if !strings.Contains(got, "None of these lounges accept") {
		t.Errorf("loungeSummary(no accessible) = %q, want 'None of these lounges accept'", got)
	}
}

// ============================================================
// wrapProviderHandler — nil registry path
// ============================================================

func TestWrapProviderHandler_NilRegistry(t *testing.T) {
	s := NewServer()
	// Explicitly nil out the registry to exercise the nil-check branch.
	s.providerRegistry = nil
	handler := s.wrapProviderHandler(func(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, _ *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
		return nil, nil, nil
	})
	_, _, err := handler(context.Background(), nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error when provider registry is nil")
	}
}

// ============================================================
// makeElicitFunc — nil capabilities path (returns nil)
// ============================================================

func TestMakeElicitFunc_NilElicitationCapability(t *testing.T) {
	s := NewServer()
	// clientCapabilities.Elicitation is nil by default.
	fn := s.makeElicitFunc()
	if fn != nil {
		t.Error("expected nil ElicitFunc when elicitation capability not declared")
	}
}

func TestMakeElicitFunc_WithCapability_NoWriter(t *testing.T) {
	s := NewServer()
	s.clientCapabilities.Elicitation = &ElicitationCapability{}
	// notifyWriter is nil → should return nil.
	fn := s.makeElicitFunc()
	if fn != nil {
		t.Error("expected nil ElicitFunc when no notify writer")
	}
}

// ============================================================
// buildProfileSummary — pure display function for profile summary
// ============================================================

func TestBuildProfileSummary_Basic(t *testing.T) {
	p := &preferences.Preferences{
		HomeAirports:    []string{"HEL"},
		DisplayCurrency: "EUR",
	}
	got := buildProfileSummary(p)
	if !strings.Contains(got, "HEL") {
		t.Errorf("buildProfileSummary should contain home airport, got %q", got)
	}
	if !strings.Contains(got, "EUR") {
		t.Errorf("buildProfileSummary should contain currency, got %q", got)
	}
}

func TestBuildProfileSummary_WithAllFields(t *testing.T) {
	p := &preferences.Preferences{
		HomeAirports:    []string{"HEL", "TMP"},
		HomeCities:      []string{"Helsinki"},
		DisplayCurrency: "EUR",
		Nationality:     "FI",
		FrequentFlyerPrograms: []preferences.FrequentFlyerStatus{
			{Alliance: "oneworld", Tier: "sapphire", AirlineCode: "AY"},
		},
		LoyaltyAirlines:   []string{"AY", "KL"},
		LoungeCards:       []string{"Priority Pass"},
		LoyaltyHotels:     []string{"Marriott Bonvoy"},
		BudgetPerNightMin: 80,
		BudgetPerNightMax: 150,
		BudgetFlightMax:   400,
	}
	got := buildProfileSummary(p)
	if !strings.Contains(got, "HEL") {
		t.Errorf("buildProfileSummary should contain HEL, got %q", got)
	}
	if !strings.Contains(got, "Priority Pass") {
		t.Errorf("buildProfileSummary should contain lounge card, got %q", got)
	}
}
