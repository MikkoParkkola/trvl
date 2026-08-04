package mcp

import (
	"context"
	"testing"
)

// Provider tool schemas, config parsing and formatting helpers.
//
// Split out of tools_providers_test.go, which crossed the 800-line hygiene
// ceiling (trvl#560). The seam is subject, not size: everything here asserts a
// declared surface or a pure function, and none of it drives a handler. The
// handler behaviour tests -- elicitation, credential redaction, concurrency --
// stay in tools_providers_test.go.
//
// Moved verbatim.

func TestExtractDomain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		endpoint string
		want     string
	}{
		{"https://api.agoda.com/search", "api.agoda.com"},
		{"https://booking.com/api/v2/search", "booking.com"},
		{"http://localhost:8080/search", "localhost"},
		{"not-a-url", "not-a-url"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			got := extractDomain(tt.endpoint)
			if got != tt.want {
				t.Errorf("extractDomain(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestParseStringMap(t *testing.T) {
	t.Parallel()
	// From map[string]any.
	m := parseStringMap(map[string]any{
		"Accept":       "application/json",
		"Content-Type": "text/plain",
	})
	if m["Accept"] != "application/json" {
		t.Errorf("Accept = %q", m["Accept"])
	}

	// From JSON string.
	m2 := parseStringMap(`{"key":"value"}`)
	if m2["key"] != "value" {
		t.Errorf("key = %q", m2["key"])
	}

	// From empty string.
	m3 := parseStringMap("")
	if m3 != nil {
		t.Errorf("expected nil for empty string, got %v", m3)
	}

	// From nil.
	m4 := parseStringMap(nil)
	if m4 != nil {
		t.Errorf("expected nil for nil, got %v", m4)
	}
}

func TestParseAuthExtractions(t *testing.T) {
	t.Parallel()
	// From map.
	m := parseAuthExtractions(map[string]any{
		"token": map[string]any{
			"pattern":  `"token":"([^"]+)"`,
			"variable": "auth_token",
			"header":   "Authorization",
		},
	})
	if m == nil {
		t.Fatal("expected non-nil result")
	}
	if m["token"].Pattern != `"token":"([^"]+)"` {
		t.Errorf("Pattern = %q", m["token"].Pattern)
	}
	if m["token"].Variable != "auth_token" {
		t.Errorf("Variable = %q", m["token"].Variable)
	}

	// From JSON string.
	m2 := parseAuthExtractions(`{"csrf":{"pattern":"csrf=([a-z0-9]+)","variable":"csrf","header":"X-CSRF"}}`)
	if m2 == nil {
		t.Fatal("expected non-nil result from JSON")
	}
	if m2["csrf"].Header != "X-CSRF" {
		t.Errorf("Header = %q", m2["csrf"].Header)
	}

	// Nil input.
	if parseAuthExtractions(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

func TestTextContent(t *testing.T) {
	t.Parallel()
	blocks := textContent("hello world")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("Type = %q, want text", blocks[0].Type)
	}
	if blocks[0].Text != "hello world" {
		t.Errorf("Text = %q, want hello world", blocks[0].Text)
	}
}

func TestConfigureProviderTool_Definition(t *testing.T) {
	t.Parallel()
	tool := configureProviderTool()
	if tool.Name != "configure_provider" {
		t.Errorf("Name = %q", tool.Name)
	}
	if len(tool.InputSchema.Required) != 6 {
		t.Errorf("Required fields = %d, want 6", len(tool.InputSchema.Required))
	}
	if tool.Annotations == nil {
		t.Fatal("annotations should be set")
	}
	if tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be false for write tool")
	}
}

func TestListProvidersTool_Definition(t *testing.T) {
	t.Parallel()
	tool := listProvidersTool()
	if tool.Name != "list_providers" {
		t.Errorf("Name = %q", tool.Name)
	}
	if tool.Annotations == nil {
		t.Fatal("annotations should be set")
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true for read-only tool")
	}
}

func TestRemoveProviderTool_Definition(t *testing.T) {
	t.Parallel()
	tool := removeProviderTool()
	if tool.Name != "remove_provider" {
		t.Errorf("Name = %q", tool.Name)
	}
	if tool.Annotations == nil {
		t.Fatal("annotations should be set")
	}
	if !tool.Annotations.DestructiveHint {
		t.Error("DestructiveHint should be true for remove tool")
	}
}

func TestTestProviderTool_Definition(t *testing.T) {
	t.Parallel()
	tool := testProviderTool()
	if tool.Name != "test_provider" {
		t.Errorf("Name = %q", tool.Name)
	}
	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "id" {
		t.Errorf("Required = %v, want [id]", tool.InputSchema.Required)
	}
	if tool.Annotations == nil {
		t.Fatal("annotations should be set")
	}
	if tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be false (makes HTTP requests)")
	}
	if !tool.Annotations.IdempotentHint {
		t.Error("IdempotentHint should be true")
	}
	if tool.OutputSchema == nil {
		t.Error("OutputSchema should be set")
	}
}

func TestSuggestProviders_ConfigSkeletons(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	content, structured, err := handleSuggestProviders(context.Background(), map[string]any{}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}

	suggestions, ok := structured.([]providerSuggestion)
	if !ok {
		t.Fatalf("structured type = %T, want []providerSuggestion", structured)
	}

	for _, s := range suggestions {
		if s.ConfigSkeleton == nil {
			t.Errorf("provider %q has nil ConfigSkeleton", s.ID)
		}
	}
}

func TestSuggestProviders_SkeletonHasResponseMapping(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	_, structured, err := handleSuggestProviders(context.Background(), map[string]any{}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	suggestions := structured.([]providerSuggestion)
	for _, s := range suggestions {
		if s.ConfigSkeleton == nil {
			continue
		}
		rm, ok := s.ConfigSkeleton["response_mapping"]
		if !ok {
			t.Errorf("provider %q skeleton missing response_mapping", s.ID)
			continue
		}
		rmMap, ok := rm.(map[string]any)
		if !ok {
			t.Errorf("provider %q response_mapping is not a map", s.ID)
			continue
		}
		if _, ok := rmMap["results_path"]; !ok {
			t.Errorf("provider %q response_mapping missing results_path", s.ID)
		}
		if _, ok := rmMap["fields"]; !ok {
			t.Errorf("provider %q response_mapping missing fields", s.ID)
		}
	}
}

func TestParseProviderConfig_BodyTemplateObjectAutoStringify(t *testing.T) {
	t.Parallel()
	// Simulate the Qwen3.5 failure mode: body_template sent as a JSON object
	// instead of a string. The type guard should auto-stringify it.
	args := map[string]any{
		"id":       "test-body",
		"name":     "Test Body",
		"category": "hotels",
		"endpoint": "https://api.example.com/search",
		"body_template": map[string]any{
			"query":     "search",
			"variables": map[string]any{"checkin": "${checkin}"},
		},
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name": "$.hotel_name",
		},
	}

	config, err := parseProviderConfig(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.BodyTemplate == "" {
		t.Fatal("body_template should be auto-stringified from object, got empty string")
	}
	// Should be valid JSON.
	if config.BodyTemplate[0] != '{' {
		t.Errorf("body_template should start with '{', got %q", config.BodyTemplate[:1])
	}
	// Should contain the expected keys.
	if !containsString(config.BodyTemplate, "query") {
		t.Errorf("body_template should contain 'query', got %s", config.BodyTemplate)
	}
}

func TestParseProviderConfig_BodyTemplateStringPassthrough(t *testing.T) {
	t.Parallel()
	// Normal case: body_template as a string should pass through unchanged.
	args := map[string]any{
		"id":            "test-str",
		"name":          "Test Str",
		"category":      "hotels",
		"endpoint":      "https://api.example.com/search",
		"body_template": `{"query":"search"}`,
		"results_path":  "$.results",
		"field_mapping": map[string]any{"name": "$.n"},
	}

	config, err := parseProviderConfig(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.BodyTemplate != `{"query":"search"}` {
		t.Errorf("body_template = %q, want original string", config.BodyTemplate)
	}
}
