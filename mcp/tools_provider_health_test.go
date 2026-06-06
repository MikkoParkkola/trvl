package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

func TestProviderHealthSurfacesFreshnessResultsAndCircuitState(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	reg, err := providers.NewRegistryAt(filepath.Join(tmp, "providers"))
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	now := time.Now().UTC()
	if err := reg.Save(&providers.ProviderConfig{
		ID:          "flaky",
		Name:        "Flaky Provider",
		Category:    "hotel",
		Endpoint:    "https://example.com/search",
		Method:      "GET",
		ErrorCount:  5,
		LastError:   "http 429",
		LastErrorAt: now.Add(-time.Minute),
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: "items",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	healthPath := filepath.Join(tmp, ".trvl", "health.jsonl")
	if err := os.MkdirAll(filepath.Dir(healthPath), 0o700); err != nil {
		t.Fatal(err)
	}
	entries := []providers.HealthEntry{
		{
			Timestamp: now.Add(-2 * time.Minute).Format(time.RFC3339),
			Provider:  "flaky",
			Operation: "search",
			Status:    "ok",
			LatencyMs: 120,
			Results:   6,
		},
		{
			Timestamp:  now.Add(-time.Minute).Format(time.RFC3339),
			Provider:   "flaky",
			Operation:  "search",
			Status:     "error",
			LatencyMs:  250,
			Error:      "http 429 for https://example.com?api_key=secret123",
			ErrorClass: string(providers.FixHintRateLimited),
			HintCode:   string(providers.FixHintRateLimited),
		},
	}
	f, err := os.Create(healthPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		line, _ := json.Marshal(entry)
		_, _ = f.Write(append(line, '\n'))
	}
	_ = f.Close()

	content, structured, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("handleProviderHealth: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected text content")
	}
	text := content[0].Text
	for _, want := range []string{"flaky", "freshness fresh", "results total 6", "circuit open", string(providers.FixHintRateLimited)} {
		if !strings.Contains(text, want) {
			t.Fatalf("provider_health text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "secret123") {
		t.Fatalf("provider_health text leaked secret:\n%s", text)
	}

	var parsed struct {
		Providers []struct {
			Provider           string  `json:"provider"`
			Name               string  `json:"name"`
			TotalResults       int     `json:"total_results"`
			AvgResults         float64 `json:"avg_results"`
			Freshness          string  `json:"freshness"`
			LastErrorClass     string  `json:"last_error_class"`
			CircuitState       string  `json:"circuit_state"`
			CircuitNextRetryAt string  `json:"circuit_next_retry_at"`
			FixHint            string  `json:"fix_hint"`
		} `json:"providers"`
	}
	data, _ := json.Marshal(structured)
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if len(parsed.Providers) != 1 {
		t.Fatalf("providers = %d, want 1: %s", len(parsed.Providers), data)
	}
	row := parsed.Providers[0]
	if row.Provider != "flaky" || row.Name != "Flaky Provider" {
		t.Fatalf("row identity = %#v", row)
	}
	if row.TotalResults != 6 || row.AvgResults != 6 {
		t.Fatalf("result metrics = total %d avg %f, want 6/6", row.TotalResults, row.AvgResults)
	}
	if row.Freshness != "fresh" || row.LastErrorClass != string(providers.FixHintRateLimited) {
		t.Fatalf("freshness/error class = %#v", row)
	}
	if row.CircuitState != "open" || row.CircuitNextRetryAt == "" || row.FixHint == "" {
		t.Fatalf("circuit fields = %#v", row)
	}
}
