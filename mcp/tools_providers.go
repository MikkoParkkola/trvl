package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

// textContent returns a single-element text content block slice.
func textContent(s string) []ContentBlock {
	return []ContentBlock{{Type: "text", Text: s}}
}

// providerHandler is a tool handler that also receives the provider registry
// and the provider runtime.
type providerHandler func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc, reg *providers.Registry, rt *providers.Runtime) ([]ContentBlock, interface{}, error)

// wrapProviderHandler adapts a providerHandler into a ToolHandler by injecting
// the server's provider registry and runtime.
func (s *Server) wrapProviderHandler(handler providerHandler) ToolHandler {
	return func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
		if s.providerRegistry == nil {
			return nil, nil, fmt.Errorf("provider registry not available")
		}
		return handler(ctx, args, elicit, sampling, progress, s.providerRegistry, s.providerRuntime)
	}
}

// --- configure_provider ---

// configureProviderTool returns the MCP tool definition for configure_provider.
func configureProviderTool() ToolDef {
	return ToolDef{
		Name:  "configure_provider",
		Title: "Configure External Provider",
		Description: "Configure an external data provider for accommodation, transport, or restaurant search. " +
			"The user will be asked directly to confirm before the provider is enabled.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"id":                 {Type: "string", Description: "Unique identifier for this provider (e.g. \"agoda-hotels\")."},
				"name":               {Type: "string", Description: "Human-readable provider name (e.g. \"Agoda\")."},
				"category":           {Type: "string", Description: "Provider category: hotels, flights, ground, restaurants, or reviews."},
				"endpoint":           {Type: "string", Description: "Full URL of the provider's search endpoint."},
				"method":             {Type: "string", Description: "HTTP method (default: POST)."},
				"headers":            {Type: "object", Description: "Extra HTTP headers as key-value pairs."},
				"query_params":       {Type: "object", Description: "URL query parameters as key-value pairs."},
				"body_template":      {Type: "string", Description: "Request body template with {{placeholder}} variables."},
				"auth_type":          {Type: "string", Description: "Authentication type: none, header, or preflight."},
				"auth_preflight_url": {Type: "string", Description: "URL for preflight auth request (when auth_type=preflight)."},
				"auth_extractions":   {Type: "object", Description: "Map of extraction name to {pattern, variable, header} for preflight auth."},
				"results_path":       {Type: "string", Description: "JSONPath to the results array in the response (e.g. \"$.data.results\")."},
				"field_mapping":      {Type: "object", Description: "Map of trvl field name to JSONPath in the provider response."},
				"rate_limit_rps":     {Type: "number", Description: "Maximum requests per second (default: 0.5)."},
				"tls_fingerprint":    {Type: "string", Description: "TLS fingerprint profile (default: chrome)."},
				"cookies_source":     {Type: "string", Description: "Cookie source strategy (default: preflight)."},
			},
			Required: []string{"id", "name", "category", "endpoint", "results_path", "field_mapping"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":              schemaString(),
				"name":            schemaString(),
				"category":        schemaString(),
				"endpoint":        schemaString(),
				"method":          schemaString(),
				"results_path":    schemaString(),
				"field_mapping":   schemaObject(),
				"rate_limit_rps":  schemaNum(),
				"tls_fingerprint": schemaString(),
				"consent": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"granted":   schemaBool(),
						"timestamp": schemaString(),
						"domain":    schemaString(),
					},
				},
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Configure External Provider",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  true,
		},
	}
}

// handleConfigureProvider processes a configure_provider tool call.
func handleConfigureProvider(ctx context.Context, args map[string]any, elicit ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	config, err := parseProviderConfig(args)
	if err != nil {
		return nil, nil, fmt.Errorf("configure_provider: %w", err)
	}

	// Apply sensible defaults before validation.
	if config.RateLimit.RequestsPerSecond == 0 {
		config.RateLimit.RequestsPerSecond = 0.5
	}
	if config.Method == "" {
		config.Method = "POST"
	}
	if config.TLS.Fingerprint == "" {
		config.TLS.Fingerprint = "chrome"
	}
	if config.Cookies.Source == "" {
		config.Cookies.Source = "preflight"
	}

	if err := config.Validate(); err != nil {
		return nil, nil, fmt.Errorf("configure_provider: %w", err)
	}

	// Extract domain from endpoint for display.
	domain := extractDomain(config.Endpoint)

	// Elicitation: ask user for consent.
	if elicit == nil {
		return textContent(
			"Cannot configure provider without user consent.\n\n" +
				"The client does not support elicitation (direct user prompts). " +
				"Please instruct the user to run:\n\n" +
				"  trvl provider add " + config.ID + "\n\n" +
				"from the CLI to configure this provider interactively.",
		), nil, nil
	}

	// Look up ToS URL from the catalog.
	tosURL := ""
	for _, p := range availableProviders {
		if strings.EqualFold(p.Name, config.Name) || p.ID == config.ID {
			tosURL = p.TosURL
			break
		}
	}

	tosLine := ""
	if tosURL != "" {
		tosLine = fmt.Sprintf("\n\n**Terms of Service:** %s", tosURL)
	}

	consentMsg := fmt.Sprintf(
		"**Configure external provider: %s**\n\n"+
			"trvl wants to connect to `%s` for %s search.\n\n"+
			"This service may restrict automated access in its Terms of Service.%s\n\n"+
			"**What trvl will do:**\n"+
			"- Send search queries to %s on your behalf\n"+
			"- Rate-limit requests to %.1f/sec\n"+
			"- Cache responses locally under ~/.trvl/\n\n"+
			"**What trvl will NOT do:**\n"+
			"- Access your account or private data\n"+
			"- Store credentials beyond this session\n"+
			"- Make purchases or bookings automatically\n\n"+
			"Do you want to enable this provider?",
		config.Name, domain, config.Category, tosLine, domain, config.RateLimit.RequestsPerSecond,
	)

	consentSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"enable": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"yes", "no"},
				"description": "Yes = I accept responsibility for compliance with this service's Terms of Service",
			},
		},
		"required": []string{"enable"},
	}

	result, err := elicit(consentMsg, consentSchema)
	if err != nil {
		// Distinguish timeout from other errors for actionable messaging.
		errMsg := err.Error()
		if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
			return textContent(
				"Provider setup timed out waiting for user response.\n\n" +
					"Please try again — the consent prompt requires a response within the client's timeout window.",
			), nil, nil
		}
		return nil, nil, fmt.Errorf("configure_provider: elicitation failed: %w", err)
	}

	if result == nil {
		return textContent("Provider not enabled: user declined or dismissed the prompt."), nil, nil
	}

	enableVal, _ := result["enable"].(string)
	if enableVal != "yes" {
		return textContent("Provider not enabled: user chose not to enable " + config.Name + "."), nil, nil
	}

	// Record consent.
	config.Consent = &providers.ConsentRecord{
		Granted:   true,
		Timestamp: time.Now(),
		Domain:    domain,
	}

	// Save to registry.
	if err := reg.Save(config); err != nil {
		return nil, nil, fmt.Errorf("configure_provider: save: %w", err)
	}

	// Proactive cookie warming: if the provider uses browser_escape_hatch,
	// open the preflight URL in the user's browser now so that cookies are
	// warm before the first search. This is a one-time setup action.
	warmingNote := ""
	if config.Auth != nil && config.Auth.BrowserEscapeHatch && config.Auth.PreflightURL != "" {
		if err := providers.OpenURLInBrowser(config.Auth.PreflightURL, ""); err != nil {
			log.Printf("cookie warming: failed to open browser for %s: %v", config.Name, err)
		} else {
			warmingNote = fmt.Sprintf("\n\nOpened %s in browser to warm cookies for %s. Future searches will use these cookies automatically.",
				config.Auth.PreflightURL, config.Name)
		}
	}

	summary := fmt.Sprintf("Provider %q enabled for %s search (domain: %s, rate limit: %.1f rps).%s",
		config.Name, config.Category, domain, config.RateLimit.RequestsPerSecond, warmingNote)
	return textContent(summary), config, nil
}

// parseProviderConfig extracts a ProviderConfig from MCP tool arguments.
func parseProviderConfig(args map[string]any) (*providers.ProviderConfig, error) {
	// body_template type guard: some LLMs send a JSON object instead of a
	// JSON string for body_template. Auto-stringify it rather than rejecting.
	bodyTemplate := argString(args, "body_template")
	if bodyTemplate == "" {
		if v, ok := args["body_template"]; ok && v != nil {
			if _, isMap := v.(map[string]any); isMap {
				b, err := json.Marshal(v)
				if err == nil {
					bodyTemplate = string(b)
				}
			}
		}
	}

	config := &providers.ProviderConfig{
		ID:           argString(args, "id"),
		Name:         argString(args, "name"),
		Category:     argString(args, "category"),
		Endpoint:     argString(args, "endpoint"),
		Method:       argString(args, "method"),
		BodyTemplate: bodyTemplate,
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: argString(args, "results_path"),
		},
		RateLimit: providers.RateLimitConfig{
			RequestsPerSecond: argFloat(args, "rate_limit_rps", 0),
		},
		TLS: providers.TLSConfig{
			Fingerprint: argString(args, "tls_fingerprint"),
		},
		Cookies: providers.CookieConfig{
			Source: argString(args, "cookies_source"),
		},
	}

	// Build Auth config if auth_type is provided.
	if authType := argString(args, "auth_type"); authType != "" {
		config.Auth = &providers.AuthConfig{
			Type:         authType,
			PreflightURL: argString(args, "auth_preflight_url"),
		}
		if v, ok := args["auth_extractions"]; ok {
			config.Auth.Extractions = parseAuthExtractions(v)
		}
	}

	// Parse headers (map[string]string).
	if v, ok := args["headers"]; ok {
		config.Headers = parseStringMap(v)
	}

	// Parse query_params (map[string]string).
	if v, ok := args["query_params"]; ok {
		config.QueryParams = parseStringMap(v)
	}

	// Parse field_mapping into ResponseMapping.Fields.
	if v, ok := args["field_mapping"]; ok {
		config.ResponseMapping.Fields = parseStringMap(v)
	}

	return config, nil
}

// parseStringMap converts a map[string]any to map[string]string.
// Also handles a JSON string encoding.
func parseStringMap(v any) map[string]string {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]string, len(val))
		for k, v := range val {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
		return result
	case string:
		if val == "" {
			return nil
		}
		var result map[string]string
		if err := json.Unmarshal([]byte(val), &result); err != nil {
			return nil
		}
		return result
	default:
		return nil
	}
}

// parseAuthExtractions converts a map[string]any to map[string]Extraction.
func parseAuthExtractions(v any) map[string]providers.Extraction {
	m, ok := v.(map[string]any)
	if !ok {
		// Try JSON string.
		if s, ok := v.(string); ok && s != "" {
			var result map[string]providers.Extraction
			if err := json.Unmarshal([]byte(s), &result); err != nil {
				return nil
			}
			return result
		}
		return nil
	}

	result := make(map[string]providers.Extraction, len(m))
	for name, val := range m {
		em, ok := val.(map[string]any)
		if !ok {
			continue
		}
		ext := providers.Extraction{}
		if s, ok := em["pattern"].(string); ok {
			ext.Pattern = s
		}
		if s, ok := em["variable"].(string); ok {
			ext.Variable = s
		}
		if s, ok := em["header"].(string); ok {
			ext.Header = s
		}
		result[name] = ext
	}
	return result
}

// extractDomain returns the hostname from a URL, or the URL itself if parsing fails.
func extractDomain(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		// Fallback: try to extract something useful.
		parts := strings.SplitN(endpoint, "/", 4)
		if len(parts) >= 3 {
			return parts[2]
		}
		return endpoint
	}
	return u.Hostname()
}

// --- list_providers ---

// listProvidersTool returns the MCP tool definition for list_providers.
func listProvidersTool() ToolDef {
	return ToolDef{
		Name:        "list_providers",
		Title:       "List External Providers",
		Description: "List all configured external data providers with their status, consent, and error counts.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"providers": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":           schemaString(),
						"name":         schemaString(),
						"category":     schemaString(),
						"domain":       schemaString(),
						"consent":      schemaBool(),
						"last_success": schemaString(),
						"error_count":  schemaInt(),
					},
				}),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "List External Providers",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleListProviders processes a list_providers tool call.
func handleListProviders(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	configs := reg.List()

	if len(configs) == 0 {
		return textContent("No external providers configured. Use configure_provider to add one."), nil, nil
	}

	type providerSummary struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Domain      string `json:"domain"`
		Consent     bool   `json:"consent"`
		LastSuccess string `json:"last_success,omitempty"`
		ErrorCount  int    `json:"error_count"`
	}

	summaries := make([]providerSummary, 0, len(configs))
	var lines []string

	for _, c := range configs {
		domain := extractDomain(c.Endpoint)
		lastSuccess := ""
		if !c.LastSuccess.IsZero() {
			lastSuccess = c.LastSuccess.Format(time.RFC3339)
		}

		consentGranted := c.Consent != nil && c.Consent.Granted
		summaries = append(summaries, providerSummary{
			ID:          c.ID,
			Name:        c.Name,
			Category:    c.Category,
			Domain:      domain,
			Consent:     consentGranted,
			LastSuccess: lastSuccess,
			ErrorCount:  c.ErrorCount,
		})

		status := "enabled"
		if !consentGranted {
			status = "no consent"
		}
		line := fmt.Sprintf("- %s (%s) [%s] %s", c.Name, c.Category, status, domain)
		if c.ErrorCount > 0 {
			line += fmt.Sprintf(" (%d errors)", c.ErrorCount)
		}
		lines = append(lines, line)
	}

	summary := fmt.Sprintf("%d provider(s) configured:\n%s", len(configs), strings.Join(lines, "\n"))
	structured := map[string]any{"providers": summaries}
	content, err := buildAnnotatedContentBlocks(summary, structured)
	if err != nil {
		return nil, nil, err
	}
	// Return structured data so programmatic clients can parse it.
	// OutputSchema is intentionally omitted on the tool definition so strict
	// MCP clients don't reject the nested-array shape ("expected record,
	// received array" was previously seen against aggressive validators).
	return content, structured, nil
}

// --- remove_provider ---

// removeProviderTool returns the MCP tool definition for remove_provider.
func removeProviderTool() ToolDef {
	return ToolDef{
		Name:        "remove_provider",
		Title:       "Remove External Provider",
		Description: "Remove a configured external data provider by ID. No confirmation needed.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"id": {Type: "string", Description: "ID of the provider to remove."},
			},
			Required: []string{"id"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message": schemaString(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Remove External Provider",
			ReadOnlyHint:    false,
			DestructiveHint: true,
			IdempotentHint:  true,
		},
	}
}

// handleRemoveProvider processes a remove_provider tool call.
func handleRemoveProvider(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	id := argString(args, "id")
	if id == "" {
		return nil, nil, fmt.Errorf("remove_provider: id is required")
	}

	if err := reg.Delete(id); err != nil {
		return nil, nil, fmt.Errorf("remove_provider: %w", err)
	}

	return textContent(fmt.Sprintf("Provider %q removed.", id)), nil, nil
}

// --- test_provider ---

// testProviderTool returns the MCP tool definition for test_provider.
func testProviderTool() ToolDef {
	return ToolDef{
		Name:  "test_provider",
		Title: "Test Provider Configuration",
		Description: "Test a configured provider by making a single search request. " +
			"Returns detailed diagnostics including which step succeeded or failed " +
			"(preflight, auth extraction, search request, response parsing, field mapping). " +
			"Use this after configure_provider to verify the config works, and iterate on " +
			"failures without requiring re-consent.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"id":       {Type: "string", Description: "Provider ID to test."},
				"location": {Type: "string", Description: "Test location (default: Paris)."},
				"checkin":  {Type: "string", Description: "Test check-in date (default: tomorrow, YYYY-MM-DD)."},
				"checkout": {Type: "string", Description: "Test check-out date (default: day after tomorrow, YYYY-MM-DD)."},
			},
			Required: []string{"id"},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"success":            schemaBool(),
				"step":               schemaString(),
				"http_status":        schemaInt(),
				"results_count":      schemaInt(),
				"error":              schemaString(),
				"extraction_results": schemaObject(),
				"body_snippet":       schemaString(),
				"sample_result":      schemaObject(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Test Provider Configuration",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  true,
		},
	}
}

// handleTestProvider processes a test_provider tool call.
func handleTestProvider(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	id := argString(args, "id")
	if id == "" {
		return nil, nil, fmt.Errorf("test_provider: id is required")
	}

	// Reload from disk so manual edits to ~/.trvl/providers/<id>.json are
	// picked up without restarting the MCP server. Registry.Reload falls back
	// to the in-memory copy if the file is missing or malformed.
	cfg, err := reg.Reload(id)
	if err != nil {
		cfg = reg.Get(id)
	}
	if cfg == nil {
		return nil, nil, fmt.Errorf("test_provider: provider %q not found", id)
	}

	// Default test parameters.
	location := argString(args, "location")
	if location == "" {
		location = "Paris"
	}
	checkin := argString(args, "checkin")
	checkout := argString(args, "checkout")
	if checkin == "" {
		checkin = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		checkout = time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	}
	if checkout == "" {
		// checkin was provided but checkout was not.
		ci, err := models.ParseDate(checkin)
		if err == nil {
			checkout = ci.AddDate(0, 0, 1).Format("2006-01-02")
		} else {
			checkout = time.Now().AddDate(0, 0, 2).Format("2006-01-02")
		}
	}

	// Paris coordinates.
	lat, lon := 48.8566, 2.3522

	result := providers.TestProvider(ctx, cfg, location, lat, lon, checkin, checkout, "EUR", 2)

	// Build summary text with actionable diagnostics.
	var summary string
	if result.Success {
		if result.ResultsCount == 0 {
			bodyHint := ""
			if result.BodySnippet != "" {
				snippet := result.BodySnippet
				if len(snippet) > 500 {
					snippet = snippet[:500]
				}
				bodyHint = fmt.Sprintf("\n\nFirst 500 chars of response:\n```\n%s\n```\nInspect the JSON structure and update results_path to the correct dot-notation path.", snippet)
			}
			summary = fmt.Sprintf(
				"Provider %q test completed (HTTP %d) but returned 0 results.\n\n"+
					"**Hint:** HTTP 200 but 0 results. Your results_path %q may be wrong."+
					" Check the actual JSON structure in the response.%s",
				id, result.HTTPStatus, cfg.ResponseMapping.ResultsPath, bodyHint)
		} else {
			summary = fmt.Sprintf("Provider %q test passed: %d results found at step %q.", id, result.ResultsCount, result.Step)
		}
	} else {
		summary = fmt.Sprintf("Provider %q test failed at step %q: %s", id, result.Step, result.Error)

		// Add actionable hints based on failure patterns.
		switch {
		case result.HTTPStatus == 202 || result.HTTPStatus == 403:
			summary += fmt.Sprintf("\n\n**Hint:** Server returned HTTP %d (likely bot detection / WAF challenge). "+
				"Set tls_fingerprint=\"chrome\" and auth.browser_escape_hatch=true in your config. "+
				"If already set, the service may require real browser cookies — try cookies_source=\"browser\".",
				result.HTTPStatus)
		case result.HTTPStatus == 401 || result.HTTPStatus == 407:
			summary += "\n\n**Hint:** Authentication failed (HTTP " + fmt.Sprint(result.HTTPStatus) + "). " +
				"Check your auth.preflight_url and extraction patterns. " +
				"The API key or token regex may not match the current page source. " +
				"Re-read the reference project to verify the auth endpoint and header names."
		case result.HTTPStatus == 429:
			summary += "\n\n**Hint:** Rate limited. Lower `rate_limit_rps` and retry after a few minutes."
		case result.Step == "auth_extraction":
			patternHint := ""
			if cfg.Auth != nil {
				for name, ext := range cfg.Auth.Extractions {
					patternHint += fmt.Sprintf("\n  - Extraction %q: pattern=%q", name, ext.Pattern)
				}
			}
			bodyHint := ""
			if result.BodySnippet != "" {
				snippet := result.BodySnippet
				if len(snippet) > 300 {
					snippet = snippet[:300]
				}
				bodyHint = fmt.Sprintf("\n\nFirst 300 chars of preflight body:\n```\n%s\n```", snippet)
			}
			summary += fmt.Sprintf("\n\n**Hint:** The regex pattern did not match the preflight response body.%s%s\n\n"+
				"Adjust your regex to match the actual content. "+
				"Re-read the reference project source to find the correct extraction pattern.",
				patternHint, bodyHint)
		case result.Step == "response_parse" && strings.Contains(result.Error, "did not resolve to an array"):
			summary += "\n\n**Hint:** The results_path does not point to a JSON array in the response. " +
				"Inspect the body_snippet and try a different dot-notation path (e.g. \"data.results\" or \"searchResults.results\")."
		}
	}

	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

// --- suggest_providers ---
//
// The provider catalog (availableProviders) and the skeleton* config
// builders used by it live in tools_providers_catalog.go for
// file-size hygiene (DoD file ≤800 LOC gate). Types used here:
//   - providerSuggestion  (describes one catalog entry)
//   - availableProviders  (the catalog, driven by suggest_providers)
//   - skeleton* helpers   (ConfigSkeleton builders per auth pattern)

// suggestProvidersTool returns the MCP tool definition for suggest_providers.
func suggestProvidersTool() ToolDef {
	return ToolDef{
		Name:  "suggest_providers",
		Title: "Suggest Available Providers",
		Description: "Returns a catalog of external data providers that the user can enable " +
			"for additional hotel, transport, restaurant, and review sources. " +
			"Call this proactively after hotel searches to suggest additional sources, " +
			"or when the user asks about expanding their search coverage. " +
			"Each provider includes an auth pattern description and a reference to an " +
			"open-source project where the API integration details can be found. " +
			"Use configure_provider to enable a suggested provider (requires user consent).",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"category": {Type: "string", Description: "Filter by category: hotels, ground, restaurants, reviews. Empty returns all."},
			},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"providers": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":              schemaString(),
						"name":            schemaString(),
						"category":        schemaString(),
						"description":     schemaString(),
						"auth_pattern":    schemaString(),
						"auth_hint":       schemaString(),
						"reference":       schemaString(),
						"tls":             schemaString(),
						"rate_limit":      schemaString(),
						"configured":      schemaBool(),
						"config_skeleton": schemaObject(),
					},
				}),
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Suggest Available Providers",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleSuggestProviders processes a suggest_providers tool call.
func handleSuggestProviders(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	category := argString(args, "category")

	// Mark which providers are already configured.
	configured := make(map[string]bool)
	for _, c := range reg.List() {
		configured[c.ID] = true
	}

	suggestions := make([]providerSuggestion, 0, len(availableProviders))
	for _, p := range availableProviders {
		if category != "" && p.Category != category {
			continue
		}
		s := p // copy
		s.Configured = configured[p.ID]
		suggestions = append(suggestions, s)
	}

	if len(suggestions) == 0 {
		return textContent("No providers available for category: " + category), nil, nil
	}

	var lines []string
	for _, s := range suggestions {
		status := "available"
		if s.Configured {
			status = "configured"
		}
		lines = append(lines, fmt.Sprintf("- %s (%s) [%s] — %s", s.Name, s.Category, status, s.Description))
	}

	summary := fmt.Sprintf("%d provider(s) available:\n%s\n\nTo enable a provider: "+
		"(1) read the reference project source listed in auth_hint to find the real endpoint, auth, and response schema, "+
		"(2) generate a config using verified info, "+
		"(3) call configure_provider (requires user consent), "+
		"(4) call test_provider and iterate on failures up to 3 times. "+
		"Do NOT guess endpoints — fetch the reference project first.",
		len(suggestions), strings.Join(lines, "\n"))

	content, err := buildAnnotatedContentBlocks(summary, suggestions)
	if err != nil {
		return nil, nil, err
	}
	return content, suggestions, nil
}

// --- provider_health ---

// providerHealthTool returns the MCP tool definition for provider_health.
func providerHealthTool() ToolDef {
	return ToolDef{
		Name:        "provider_health",
		Title:       "Provider Health Summary",
		Description: "Shows per-provider health statistics aggregated from the local health log (~/.trvl/health.jsonl): total calls, success rate, average latency, and last error. Use this to diagnose which external providers are failing or slow.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"providers": schemaArray(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"provider":       schemaString(),
						"total_calls":    schemaInt(),
						"success_count":  schemaInt(),
						"error_count":    schemaInt(),
						"timeout_count":  schemaInt(),
						"success_rate":   schemaNum(),
						"avg_latency_ms": schemaInt(),
						"last_error":     schemaString(),
					},
				}),
				"trvl_update_available": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"available":       schemaBool(),
						"latest_version":  schemaString(),
						"current_version": schemaString(),
						"release_url":     schemaString(),
						"checked_at":      schemaString(),
						"install_method":  schemaString(),
						"upgrade_hint":    schemaString(),
					},
				},
			},
		},
		Annotations: &ToolAnnotations{
			Title:          "Provider Health Summary",
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
	}
}

// handleProviderHealth processes a provider_health tool call.
func handleProviderHealth(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, _ *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	dir, err := providers.HealthLogDir()
	if err != nil {
		return nil, nil, fmt.Errorf("provider_health: %w", err)
	}

	summary := providers.HealthSummary(dir)
	if len(summary) == 0 {
		return textContent("No health data recorded yet. Health entries are written after provider searches."), nil, nil
	}

	type row struct {
		Provider     string  `json:"provider"`
		TotalCalls   int     `json:"total_calls"`
		SuccessCount int     `json:"success_count"`
		ErrorCount   int     `json:"error_count"`
		TimeoutCount int     `json:"timeout_count"`
		SuccessRate  float64 `json:"success_rate"`
		AvgLatencyMs int64   `json:"avg_latency_ms"`
		LastError    string  `json:"last_error,omitempty"`
	}

	rows := make([]row, 0, len(summary))
	var lines []string
	for _, h := range summary {
		rows = append(rows, row{
			Provider:     h.Provider,
			TotalCalls:   h.TotalCalls,
			SuccessCount: h.SuccessCount,
			ErrorCount:   h.ErrorCount,
			TimeoutCount: h.TimeoutCount,
			SuccessRate:  h.SuccessRate,
			AvgLatencyMs: h.AvgLatencyMs,
			LastError:    h.LastError,
		})
		line := fmt.Sprintf("- %s: %d calls, %.0f%% ok, avg %dms",
			h.Provider, h.TotalCalls, h.SuccessRate*100, h.AvgLatencyMs)
		if h.ErrorCount > 0 || h.TimeoutCount > 0 {
			line += fmt.Sprintf(", %d errors, %d timeouts", h.ErrorCount, h.TimeoutCount)
		}
		if h.LastError != "" {
			line += fmt.Sprintf(", last error: %s", h.LastError)
		}
		lines = append(lines, line)
	}

	text := fmt.Sprintf("Provider health (%d provider(s)):\n%s", len(rows), strings.Join(lines, "\n"))
	structured := map[string]any{"providers": rows}

	// Surface the cached update-check info so AI assistants can mention
	// "trvl v1.1.3 available" alongside provider health without needing
	// to make their own network call. Read-only against the on-disk
	// cache populated by the daily background check; never blocks.
	if info := selfupdate.LoadCachedInfo(); info.LatestVersion != "" {
		// Detect how trvl was installed so the upgrade advice routes to
		// the right channel: brew users get `brew upgrade trvl`, npm
		// users get `npm install -g trvl-mcp@latest`, standalone tarball
		// users get `trvl self-update`. Detection is read-only against
		// the running binary's path — no subprocesses, no network.
		method := selfupdate.DetectInstallMethod(info.CurrentVersion)
		hint := method.UpgradeHint()

		updateField := map[string]any{
			"available":       info.UpdateAvailable,
			"latest_version":  info.LatestVersion,
			"current_version": info.CurrentVersion,
			"release_url":     info.ReleaseURL,
			"checked_at":      info.CheckedAt,
			"install_method":  method.String(),
		}
		if hint != "" {
			updateField["upgrade_hint"] = hint
		}
		structured["trvl_update_available"] = updateField

		if info.UpdateAvailable {
			text += fmt.Sprintf("\n\ntrvl v%s available (you have v%s). Release notes: %s",
				info.LatestVersion, info.CurrentVersion, info.ReleaseURL)
			if hint != "" {
				text += fmt.Sprintf("\nUpgrade: %s", hint)
			}
		}
	}

	content, err := buildAnnotatedContentBlocks(text, structured)
	if err != nil {
		return nil, nil, err
	}
	return content, structured, nil
}
