package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func testRegistry(t *testing.T) *providers.Registry {
	t.Helper()
	dir := t.TempDir()
	reg, err := providers.NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	return reg
}

func TestHandleConfigureProvider_NoElicitation(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "test-provider",
		"name":         "Test Provider",
		"category":     "hotels",
		"endpoint":     "https://api.example.com/search",
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name":  "$.hotel_name",
			"price": "$.price.total",
		},
	}

	// With elicit == nil, should return a CLI instruction message.
	content, _, err := handleConfigureProvider(context.Background(), args, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if content[0].Type != "text" {
		t.Errorf("expected text block, got %q", content[0].Type)
	}
	if got := content[0].Text; got == "" {
		t.Error("expected non-empty text")
	}
	// Should mention CLI command.
	if !containsString(content[0].Text, "trvl provider add") {
		t.Errorf("expected CLI instruction in response, got: %s", content[0].Text)
	}

	// Provider should NOT be saved.
	if reg.Get("test-provider") != nil {
		t.Error("provider should not be saved without elicitation")
	}
}

func TestHandleConfigureProvider_ElicitDecline(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "test-decline",
		"name":         "Decline Provider",
		"category":     "flights",
		"endpoint":     "https://api.example.com/flights",
		"results_path": "$.data",
		"field_mapping": map[string]any{
			"name": "$.flight_name",
		},
	}

	// Elicit returns nil (user dismissed).
	elicit := func(message string, schema map[string]interface{}) (map[string]interface{}, error) {
		return nil, nil
	}

	content, _, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "not enabled") {
		t.Errorf("expected decline message, got: %s", content[0].Text)
	}
	if reg.Get("test-decline") != nil {
		t.Error("provider should not be saved after decline")
	}
}

func TestHandleConfigureProvider_ElicitNo(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "test-no",
		"name":         "No Provider",
		"category":     "ground",
		"endpoint":     "https://api.example.com/ground",
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name": "$.route_name",
		},
	}

	elicit := func(message string, schema map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"enable": "no"}, nil
	}

	content, _, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(content[0].Text, "not enabled") {
		t.Errorf("expected decline message, got: %s", content[0].Text)
	}
	if reg.Get("test-no") != nil {
		t.Error("provider should not be saved after 'no'")
	}
}

func TestHandleConfigureProvider_ElicitYes(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "agoda-hotels",
		"name":         "Agoda",
		"category":     "hotels",
		"endpoint":     "https://api.agoda.com/search",
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name":  "$.hotel_name",
			"price": "$.price",
		},
		"rate_limit_rps": 1.0,
	}

	var elicitMessage string
	elicit := func(message string, schema map[string]interface{}) (map[string]interface{}, error) {
		elicitMessage = message
		return map[string]interface{}{"enable": "yes"}, nil
	}

	content, structured, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify elicitation message includes provider name and domain.
	if !containsString(elicitMessage, "Agoda") {
		t.Errorf("elicit message should mention provider name, got: %s", elicitMessage)
	}
	if !containsString(elicitMessage, "api.agoda.com") {
		t.Errorf("elicit message should mention domain, got: %s", elicitMessage)
	}

	// Verify success response.
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "enabled") {
		t.Errorf("expected success message, got: %s", content[0].Text)
	}

	// Verify structured output.
	if structured == nil {
		t.Fatal("expected structured output")
	}
	config, ok := structured.(*providerConfigView)
	if !ok {
		t.Fatalf("structured output type = %T, want *providerConfigView", structured)
	}
	if config.Consent == nil || !config.Consent.Granted {
		t.Error("consent should be granted")
	}
	if config.Consent == nil || config.Consent.Domain != "api.agoda.com" {
		t.Errorf("consent domain = %q, want api.agoda.com", config.Consent.Domain)
	}

	// Verify provider is saved in registry.
	saved := reg.Get("agoda-hotels")
	if saved == nil {
		t.Fatal("provider should be saved in registry")
	}
	if saved.Name != "Agoda" {
		t.Errorf("saved name = %q, want Agoda", saved.Name)
	}
	if saved.RateLimit.RequestsPerSecond != 1.0 {
		t.Errorf("saved rate_limit_rps = %v, want 1.0", saved.RateLimit.RequestsPerSecond)
	}
}

func TestHandleConfigureProvider_ValidationError(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "bad-provider",
		"name":         "Bad",
		"category":     "invalid_category",
		"endpoint":     "https://api.example.com",
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name": "$.name",
		},
	}

	_, _, err := handleConfigureProvider(context.Background(), args, nil, nil, nil, reg, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !containsString(err.Error(), "invalid category") {
		t.Errorf("expected category validation error, got: %v", err)
	}
}

func TestHandleListProviders_Empty(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	content, _, err := handleListProviders(context.Background(), nil, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "No external providers configured") {
		t.Errorf("expected empty message, got: %s", content[0].Text)
	}
}

func TestHandleListProviders_WithProviders(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)

	// Add a provider directly.
	config := &providers.ProviderConfig{
		ID:       "test-list",
		Name:     "Test List Provider",
		Category: "hotels",
		Endpoint: "https://api.example.com/search",
		Method:   "POST",
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: "$.results",
			Fields: map[string]string{
				"name": "$.hotel_name",
			},
		},
		Consent: &providers.ConsentRecord{
			Granted: true,
			Domain:  "api.example.com",
		},
	}
	if err := reg.Save(config); err != nil {
		t.Fatalf("Save: %v", err)
	}

	content, structured, err := handleListProviders(context.Background(), nil, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) < 2 {
		t.Fatal("expected annotated content blocks (summary + JSON)")
	}
	if !containsString(content[0].Text, "1 provider(s) configured") {
		t.Errorf("expected provider count in summary, got: %s", content[0].Text)
	}
	if !containsString(content[0].Text, "Test List Provider") {
		t.Errorf("expected provider name in summary, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
	// Regression guard: MCP OutputSchema for list_providers declares
	// `{type: "object", properties: {providers: {type: "array", ...}}}`.
	// If the handler ever returns the array directly (or nil), strict MCP
	// clients reject it with "expected record, received array". Verify by
	// JSON round-trip — which is exactly what MCP clients do.
	data, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	var parsed struct {
		Providers []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Category string `json:"category"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("structured output must be a JSON object with 'providers' array, got %s: %v", string(data), err)
	}
	if len(parsed.Providers) != 1 {
		t.Fatalf("expected 1 provider in structured output, got %d (payload: %s)", len(parsed.Providers), string(data))
	}
	if parsed.Providers[0].ID != "test-list" {
		t.Errorf("structured.providers[0].id = %q, want %q", parsed.Providers[0].ID, "test-list")
	}
	if parsed.Providers[0].Name != "Test List Provider" {
		t.Errorf("structured.providers[0].name = %q, want %q", parsed.Providers[0].Name, "Test List Provider")
	}
}

func TestHandleRemoveProvider_Success(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)

	// Add a provider.
	config := &providers.ProviderConfig{
		ID:       "to-remove",
		Name:     "Remove Me",
		Category: "flights",
		Endpoint: "https://api.example.com/flights",
		Method:   "POST",
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: "$.data",
			Fields: map[string]string{
				"name": "$.flight_name",
			},
		},
	}
	if err := reg.Save(config); err != nil {
		t.Fatalf("Save: %v", err)
	}

	args := map[string]any{"id": "to-remove"}
	content, _, err := handleRemoveProvider(context.Background(), args, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(content[0].Text, "removed") {
		t.Errorf("expected removal confirmation, got: %s", content[0].Text)
	}
	if reg.Get("to-remove") != nil {
		t.Error("provider should be removed from registry")
	}
}

func TestHandleRemoveProvider_NotFound(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{"id": "non-existent"}
	_, _, err := handleRemoveProvider(context.Background(), args, nil, nil, nil, reg, nil)
	if err == nil {
		t.Fatal("expected error for non-existent provider")
	}
}

func TestHandleRemoveProvider_MissingID(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	_, _, err := handleRemoveProvider(context.Background(), map[string]any{}, nil, nil, nil, reg, nil)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestHandleTestProvider_MissingID(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	_, _, err := handleTestProvider(context.Background(), map[string]any{}, nil, nil, nil, reg, nil)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if !containsString(err.Error(), "id is required") {
		t.Errorf("expected 'id is required' error, got: %v", err)
	}
}

func TestHandleTestProvider_NotFound(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	_, _, err := handleTestProvider(context.Background(), map[string]any{"id": "nonexistent"}, nil, nil, nil, reg, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
	if !containsString(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

// containsString checks if s contains sub.
func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestListProvidersConcurrentWithBreakerMarks is the same-package regression
// guard for the MIK-5858 fix: the provider-listing MCP handlers must read
// breaker state through reg.ListSafe() (RLock value-copy snapshot) so a
// concurrent MarkError/MarkSuccess writer cannot race the reader. Run under
// -race; if the handlers are reverted to the unsynchronized List() path this
// fails with a data race. The internal/providers package carries the
// lower-level race test (TestProviderBreaker_ConcurrentStatusReportAndMark);
// this one exercises the same invariant at the mcp handler boundary that the
// fix actually changed.
func TestListProvidersConcurrentWithBreakerMarks(t *testing.T) {
	reg := testRegistry(t)
	config := &providers.ProviderConfig{
		ID:       "race-list",
		Name:     "Race List Provider",
		Category: "hotels",
		Endpoint: "http://127.0.0.1/search",
		Method:   "POST",
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: "$.results",
			Fields:      map[string]string{"name": "$.hotel_name"},
		},
		Consent: &providers.ConsentRecord{Granted: true, Domain: "127.0.0.1"},
	}
	if err := reg.Save(config); err != nil {
		t.Fatalf("Save: %v", err)
	}

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(3)

	// Writer: mutate the breaker fields concurrently.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				reg.MarkError("race-list", "boom")
			} else {
				reg.MarkSuccess("race-list")
			}
		}
	}()

	// Reader 1: list providers via the handler the fix routes through ListSafe.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if _, _, err := handleListProviders(context.Background(), nil, nil, nil, nil, reg, nil); err != nil {
				t.Errorf("handleListProviders: %v", err)
				return
			}
		}
	}()

	// Reader 2: provider health snapshot, also routed through ListSafe.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if _, _, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, reg, nil); err != nil {
				t.Errorf("handleProviderHealth: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

// TRVL.PROVIDERSECRET.1 - configure_provider must not hand the caller's own
// credentials back in its structured result.
//
// The canaries are the four places a provider config carries one: an
// authorization header, an API key in the query string, a token baked into the
// body template, and the endpoint itself -- which routinely carries a key in
// its query string, in userinfo, or -- Telegram-style -- in the path itself. Returning *providers.ProviderConfig
// serialized all of them, which is what this test exists to keep from coming
// back.
func TestHandleConfigureProvider_DoesNotEchoCredentials(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	const (
		headerSecret   = "CANARY-header-77c1f0"
		querySecret    = "CANARY-query-3ab99e"
		bodySecret     = "CANARY-body-5f20dd"
		endpointQuery  = "CANARY-endpointq-91be4c"
		endpointUserpw = "CANARY-endpointu-0d7a3e"
		endpointPath   = "CANARY-endpointp-4c8e12"
	)
	args := map[string]any{
		"id":           "secretive",
		"name":         "Secretive",
		"category":     "hotels",
		"endpoint":     "https://svc:" + endpointUserpw + "@api.secretive.test/bot" + endpointPath + "/search?apikey=" + endpointQuery,
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name": "$.hotel_name",
		},
		"headers":       map[string]any{"Authorization": headerSecret},
		"query_params":  map[string]any{"api_key": querySecret},
		"body_template": `{"token":"` + bodySecret + `"}`,
	}

	elicit := func(string, map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"enable": "yes"}, nil
	}

	content, structured, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}

	// Marshalled rather than field-inspected: what reaches the caller is the
	// JSON, so a field that leaks through an embedded struct is caught here and
	// would not be caught by naming fields.
	blob, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal structured output: %v", err)
	}
	haystacks := map[string]string{"structured output": string(blob)}
	for i, c := range content {
		haystacks[fmt.Sprintf("content block %d", i)] = c.Text
	}
	for where, hay := range haystacks {
		for name, secret := range map[string]string{
			"authorization header": headerSecret,
			"query parameter":      querySecret,
			"body template token":  bodySecret,
			"endpoint query key":   endpointQuery,
			"endpoint userinfo":    endpointUserpw,
			"endpoint path token":  endpointPath,
		} {
			if strings.Contains(hay, secret) {
				t.Errorf("%s leaked the %s: %s", where, name, hay)
			}
		}
	}
}
