package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/selfupdate"
)

func testProviderTool() ToolDef {
	return ToolDef{
		Name:  "test_provider",
		Title: "Test Provider Configuration",
		Description: "Test an enabled reviewed provider by making a single search request. " +
			"Returns detailed diagnostics including which step succeeded or failed " +
			"(preflight, auth extraction, search request, response parsing, field mapping). " +
			"Use this after `trvl providers enable <id>` to verify the embedded definition.",
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
		Description: "Returns reviewed external data providers shipped with this binary that the user can enable " +
			"for additional hotel, transport, restaurant, and review sources. " +
			"Call this proactively after hotel searches to suggest additional sources, " +
			"or when the user asks about expanding their search coverage. " +
			"Definitions are source-controlled and immutable at runtime. Use the CLI to enable one; contribute new definitions by pull request or use a fork.",
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
	shipped := make(map[string]bool)
	if reg.IsSourceOnly() {
		for _, c := range reg.ListShipped() {
			shipped[c.ID] = true
		}
	}

	suggestions := make([]providerSuggestion, 0, len(availableProviders))
	for _, p := range availableProviders {
		if reg.IsSourceOnly() && !shipped[p.ID] {
			continue
		}
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

	summary := fmt.Sprintf("%d reviewed provider(s) shipped with this binary:\n%s\n\nTo enable one, run `trvl providers enable <id>`. New definitions must be verified and contributed under internal/providers/definitions, or used from a fork.",
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
func handleProviderHealth(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc, reg *providers.Registry, _ *providers.Runtime) ([]ContentBlock, interface{}, error) {
	dir, err := providers.HealthLogDir()
	if err != nil {
		return nil, nil, fmt.Errorf("provider_health: %w", err)
	}

	summary := providers.HealthSummary(dir)
	configs := map[string]*providers.ProviderConfig{}
	if reg != nil {
		// ListSafe snapshots each config under RLock so the breaker reads in
		// CircuitBreakerHealth below never race concurrent MarkSuccess/MarkError
		// from an in-flight search (#144 class; MIK-5858).
		for _, cfg := range reg.ListSafe() {
			configs[cfg.ID] = cfg
		}
	}
	if len(summary) == 0 && len(configs) == 0 {
		return textContent("No health data recorded yet. Health entries are written after provider searches."), nil, nil
	}

	type row struct {
		Provider           string  `json:"provider"`
		Name               string  `json:"name,omitempty"`
		TotalCalls         int     `json:"total_calls"`
		SuccessCount       int     `json:"success_count"`
		ErrorCount         int     `json:"error_count"`
		TimeoutCount       int     `json:"timeout_count"`
		SuccessRate        float64 `json:"success_rate"`
		AvgLatencyMs       int64   `json:"avg_latency_ms"`
		TotalResults       int     `json:"total_results"`
		AvgResults         float64 `json:"avg_results"`
		LastResults        int     `json:"last_results"`
		LastSeen           string  `json:"last_seen,omitempty"`
		LastSuccess        string  `json:"last_success,omitempty"`
		LastFailure        string  `json:"last_failure,omitempty"`
		Freshness          string  `json:"freshness"`
		LastError          string  `json:"last_error,omitempty"`
		LastErrorClass     string  `json:"last_error_class,omitempty"`
		LastHintCode       string  `json:"last_hint_code,omitempty"`
		CircuitState       string  `json:"circuit_state"`
		CircuitErrorCount  int     `json:"circuit_error_count,omitempty"`
		CircuitReason      string  `json:"circuit_reason,omitempty"`
		CircuitNextRetryAt string  `json:"circuit_next_retry_at,omitempty"`
		FixHint            string  `json:"fix_hint,omitempty"`
	}

	providerIDs := make(map[string]bool, len(summary)+len(configs))
	for id := range summary {
		providerIDs[id] = true
	}
	for id := range configs {
		providerIDs[id] = true
	}

	rows := make([]row, 0, len(providerIDs))
	now := time.Now()
	for id := range providerIDs {
		h := summary[id]
		if h.Provider == "" {
			h.Provider = id
			h.Freshness = "unknown"
		}
		cfg := configs[id]
		circuit := providers.CircuitBreakerHealth(cfg, now)
		name := ""
		if cfg != nil {
			name = cfg.Name
		}
		rows = append(rows, row{
			Provider:           h.Provider,
			Name:               name,
			TotalCalls:         h.TotalCalls,
			SuccessCount:       h.SuccessCount,
			ErrorCount:         h.ErrorCount,
			TimeoutCount:       h.TimeoutCount,
			SuccessRate:        h.SuccessRate,
			AvgLatencyMs:       h.AvgLatencyMs,
			TotalResults:       h.TotalResults,
			AvgResults:         h.AvgResults,
			LastResults:        h.LastResults,
			LastSeen:           h.LastSeen,
			LastSuccess:        h.LastSuccess,
			LastFailure:        h.LastFailure,
			Freshness:          h.Freshness,
			LastError:          h.LastError,
			LastErrorClass:     h.LastErrorClass,
			LastHintCode:       h.LastHintCode,
			CircuitState:       circuit.State,
			CircuitErrorCount:  circuit.ErrorCount,
			CircuitReason:      circuit.Reason,
			CircuitNextRetryAt: circuit.NextRetryAt,
			FixHint:            circuit.FixHint,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })

	var lines []string
	for _, r := range rows {
		label := r.Provider
		if r.Name != "" && r.Name != r.Provider {
			label = fmt.Sprintf("%s (%s)", r.Provider, r.Name)
		}
		line := fmt.Sprintf("- %s: %d calls, %.0f%% ok, avg %dms, freshness %s, results total %d avg %.1f, circuit %s",
			label, r.TotalCalls, r.SuccessRate*100, r.AvgLatencyMs, r.Freshness, r.TotalResults, r.AvgResults, r.CircuitState)
		if r.ErrorCount > 0 || r.TimeoutCount > 0 {
			line += fmt.Sprintf(", %d errors, %d timeouts", r.ErrorCount, r.TimeoutCount)
		}
		if r.LastErrorClass != "" {
			line += fmt.Sprintf(", class %s", r.LastErrorClass)
		}
		if r.LastError != "" {
			line += fmt.Sprintf(", last error: %s", r.LastError)
		}
		if r.CircuitState == "open" || r.CircuitState == "half_open" {
			if r.CircuitReason != "" {
				line += fmt.Sprintf(", reason: %s", r.CircuitReason)
			}
			if r.CircuitNextRetryAt != "" {
				line += fmt.Sprintf(", next retry: %s", r.CircuitNextRetryAt)
			}
			if r.FixHint != "" {
				line += fmt.Sprintf(", fix: %s", r.FixHint)
			}
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
