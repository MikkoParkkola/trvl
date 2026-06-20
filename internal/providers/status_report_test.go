package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeHealthLog writes the given entries as JSONL into dir/health.jsonl.
func writeHealthLog(t *testing.T, dir string, entries []HealthEntry) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "health.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, e := range entries {
		line, _ := json.Marshal(e)
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildStatusReport_MergesHealthAndCircuit(t *testing.T) {
	tmp := t.TempDir()
	reg, err := NewRegistryAt(filepath.Join(tmp, "providers"))
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	now := time.Now().UTC()

	// A provider with a tripped breaker (>= threshold consecutive errors).
	if err := reg.Save(&ProviderConfig{
		ID:          "flaky",
		Name:        "Flaky Provider",
		Category:    "hotel",
		Endpoint:    "https://example.com/search",
		Method:      "GET",
		ErrorCount:  5,
		LastError:   "http 429",
		LastErrorAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dir := filepath.Join(tmp, ".trvl")
	writeHealthLog(t, dir, []HealthEntry{
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
			ErrorClass: string(FixHintRateLimited),
		},
		{
			Timestamp: now.Add(-30 * time.Second).Format(time.RFC3339),
			Provider:  "solid",
			Operation: "search",
			Status:    "ok",
			LatencyMs: 80,
			Results:   10,
		},
	})

	rows := BuildStatusReport(reg, dir, now)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (flaky + solid): %+v", len(rows), rows)
	}

	// Sorted by provider: "flaky" before "solid".
	if rows[0].Provider != "flaky" || rows[1].Provider != "solid" {
		t.Fatalf("rows not sorted by provider: %q, %q", rows[0].Provider, rows[1].Provider)
	}

	flaky := rows[0]
	if flaky.Name != "Flaky Provider" {
		t.Errorf("flaky.Name = %q, want %q", flaky.Name, "Flaky Provider")
	}
	if flaky.TotalCalls != 2 || flaky.SuccessCount != 1 || flaky.ErrorCount != 1 {
		t.Errorf("flaky call counts = %+v", flaky)
	}
	if flaky.CircuitState != "open" {
		t.Errorf("flaky.CircuitState = %q, want open", flaky.CircuitState)
	}
	if !flaky.RateLimited() {
		t.Errorf("flaky.RateLimited() = false, want true (last error class %q)", flaky.LastErrorClass)
	}
	if flaky.IsHealthy() {
		t.Errorf("flaky.IsHealthy() = true, want false (circuit open)")
	}
	// Secret redaction is applied by HealthSummary upstream.
	if flaky.LastError == "http 429 for https://example.com?api_key=secret123" {
		t.Errorf("flaky.LastError not redacted: %q", flaky.LastError)
	}

	solid := rows[1]
	if solid.CircuitState != "unknown" {
		// No registry config for "solid" => circuit state unknown.
		t.Errorf("solid.CircuitState = %q, want unknown", solid.CircuitState)
	}
	if !solid.IsHealthy() {
		t.Errorf("solid.IsHealthy() = false, want true (100%% success, no breaker)")
	}
	if solid.RateLimited() {
		t.Errorf("solid.RateLimited() = true, want false")
	}
}

func TestBuildStatusReport_Empty(t *testing.T) {
	tmp := t.TempDir()
	rows := BuildStatusReport(nil, filepath.Join(tmp, ".trvl"), time.Time{})
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 for empty log + nil registry", len(rows))
	}
}

func TestStatusRow_IsHealthy(t *testing.T) {
	cases := []struct {
		name string
		row  StatusRow
		want bool
	}{
		{"no calls is healthy", StatusRow{CircuitState: "closed"}, true},
		{"open circuit unhealthy", StatusRow{CircuitState: "open", TotalCalls: 3, SuccessRate: 1.0}, false},
		{"high success healthy", StatusRow{CircuitState: "closed", TotalCalls: 10, SuccessRate: 0.9}, true},
		{"low success unhealthy", StatusRow{CircuitState: "closed", TotalCalls: 10, SuccessRate: 0.2}, false},
		{"half_open with good rate healthy", StatusRow{CircuitState: "half_open", TotalCalls: 4, SuccessRate: 0.75}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.row.IsHealthy(); got != tc.want {
				t.Errorf("IsHealthy() = %v, want %v", got, tc.want)
			}
		})
	}
}
