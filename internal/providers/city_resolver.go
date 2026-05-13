package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const cityResolverTimeout = 5 * time.Second

// resolveCityIDDynamic attempts to resolve a city name to a provider-specific
// ID using the provider's city_resolver autocomplete API. On success it returns
// the city ID, any extra fields configured in ExtraFields (e.g. dest_type), and
// caches the ID result in cfg.CityLookup. The optional registry parameter, when
// non-nil, persists the updated config to disk so subsequent searches find the
// city without a network call.
//
// The client parameter should be the provider's own HTTP client (with cookies,
// TLS fingerprint, etc.) so the autocomplete request matches the provider's
// expected traffic profile.
func resolveCityIDDynamic(ctx context.Context, cfg *ProviderConfig, client *http.Client, location string, registry *Registry) (string, error) {
	cr := cfg.CityResolver
	if cr == nil {
		return "", fmt.Errorf("no city_resolver configured for provider %s", cfg.ID)
	}
	if cr.URL == "" {
		return "", fmt.Errorf("city_resolver.url is empty for provider %s", cfg.ID)
	}

	// Build the autocomplete URL. ${location} is the only variable we
	// substitute — it gets URL-encoded for safe embedding in query strings.
	resolvedURL := strings.ReplaceAll(cr.URL, "${location}", url.QueryEscape(location))

	method := cr.Method
	if method == "" {
		method = "GET"
	}

	// Use a tighter timeout than the main search request.
	resolveCtx, cancel := context.WithTimeout(ctx, cityResolverTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(resolveCtx, method, resolvedURL, nil)
	if err != nil {
		return "", fmt.Errorf("city_resolver: create request: %w", err)
	}

	// Apply custom headers (e.g. User-Agent to bypass WAF).
	for k, v := range cr.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("city_resolver: http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("city_resolver: http %d for %q", resp.StatusCode, location)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1 MB cap
	if err != nil {
		return "", fmt.Errorf("city_resolver: read body: %w", err)
	}

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("city_resolver: parse json: %w", err)
	}

	// Walk result_path to locate the first result object.
	result := jsonPath(raw, cr.ResultPath)
	if result == nil {
		return "", fmt.Errorf("city_resolver: result_path %q did not resolve for %q", cr.ResultPath, location)
	}

	// If result_path pointed to an array, take the first element.
	if arr, ok := result.([]any); ok {
		if len(arr) == 0 {
			return "", fmt.Errorf("city_resolver: empty results for %q", location)
		}
		result = arr[0]
	}

	obj, ok := result.(map[string]any)
	if !ok {
		return "", fmt.Errorf("city_resolver: result is %T, expected object for %q", result, location)
	}

	// Extract the city ID.
	idVal, exists := obj[cr.IDField]
	if !exists {
		return "", fmt.Errorf("city_resolver: id_field %q not found in result for %q", cr.IDField, location)
	}

	cityID := anyToString(idVal)
	if cityID == "" {
		return "", fmt.Errorf("city_resolver: empty id_field %q for %q", cr.IDField, location)
	}

	slog.Info("city_resolver: resolved city",
		"provider", cfg.ID,
		"location", location,
		"city_id", cityID)

	// Cache the result in the in-memory CityLookup map.
	normalizedLoc := strings.ToLower(strings.TrimSpace(location))
	if cfg.CityLookup == nil {
		cfg.CityLookup = make(map[string]string)
	}
	cfg.CityLookup[normalizedLoc] = cityID

	// Also cache under the provider's display name if available.
	if cr.NameField != "" {
		if nameVal, ok := obj[cr.NameField]; ok {
			if name := anyToString(nameVal); name != "" {
				normalizedName := strings.ToLower(strings.TrimSpace(name))
				if normalizedName != normalizedLoc {
					cfg.CityLookup[normalizedName] = cityID
				}
			}
		}
	}

	// Persist the updated config to disk so subsequent searches find the
	// city without a network call. Non-fatal if this fails.
	if registry != nil {
		if err := registry.Save(cfg); err != nil {
			slog.Warn("city_resolver: failed to persist cache",
				"provider", cfg.ID, "error", err.Error())
		}
	}

	return cityID, nil
}

// resolveCityExtraFields extracts extra variable values from a city resolver
// result. This is called after resolveCityIDDynamic when the resolver has
// extra_fields configured (e.g. Booking's dest_type).
func resolveCityExtraFields(ctx context.Context, cfg *ProviderConfig, client *http.Client, location string) (map[string]string, error) {
	cr := cfg.CityResolver
	if cr == nil || len(cr.ExtraFields) == 0 {
		return nil, nil
	}

	resolvedURL := strings.ReplaceAll(cr.URL, "${location}", url.QueryEscape(location))
	method := cr.Method
	if method == "" {
		method = "GET"
	}

	resolveCtx, cancel := context.WithTimeout(ctx, cityResolverTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(resolveCtx, method, resolvedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("city_resolver extra: create request: %w", err)
	}
	for k, v := range cr.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("city_resolver extra: http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("city_resolver extra: http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("city_resolver extra: read body: %w", err)
	}

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("city_resolver extra: parse json: %w", err)
	}

	result := jsonPath(raw, cr.ResultPath)
	if result == nil {
		return nil, nil
	}
	if arr, ok := result.([]any); ok {
		if len(arr) == 0 {
			return nil, nil
		}
		result = arr[0]
	}
	obj, ok := result.(map[string]any)
	if !ok {
		return nil, nil
	}

	extras := make(map[string]string, len(cr.ExtraFields))
	for varName, jsonKey := range cr.ExtraFields {
		if val, exists := obj[jsonKey]; exists {
			extras[varName] = anyToString(val)
		}
	}
	return extras, nil
}

// anyToString converts a JSON value (string, float64, json.Number, bool) to
// its string representation. Returns "" for nil or unsupported types.
func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		// Prefer integer representation when the float has no fractional part
		// (JSON numbers like 12345 decode as float64 in Go).
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case json.Number:
		return val.String()
	case bool:
		return strconv.FormatBool(val)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}
