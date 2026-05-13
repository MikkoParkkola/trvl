package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReadHealthLog_Empty verifies that reading from a non-existent file
// returns nil, nil (no error, no entries).
func TestReadHealthLog_Empty(t *testing.T) {
	dir := t.TempDir()
	entries, err := ReadHealthLog(dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

// TestReadHealthLog_Basic writes JSONL lines manually and checks round-trip.
func TestReadHealthLog_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.jsonl")

	want := []HealthEntry{
		{Timestamp: "2026-01-01T00:00:00Z", Provider: "agoda", Operation: "search", Status: "ok", LatencyMs: 120, Results: 5},
		{Timestamp: "2026-01-01T00:01:00Z", Provider: "agoda", Operation: "search", Status: "error", LatencyMs: 250, Error: "http 403"},
		{Timestamp: "2026-01-01T00:02:00Z", Provider: "booking", Operation: "search", Status: "ok", LatencyMs: 80, Results: 12},
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range want {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	got, err := ReadHealthLog(dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, e := range got {
		if e.Provider != want[i].Provider || e.Status != want[i].Status {
			t.Errorf("entry %d: got %+v, want %+v", i, e, want[i])
		}
	}
}

// TestReadHealthLog_Last verifies the "last N" slicing.
func TestReadHealthLog_Last(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.jsonl")

	f, _ := os.Create(path)
	for i := range 10 {
		e := HealthEntry{Provider: "p", Status: "ok", LatencyMs: int64(i)}
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	got, err := ReadHealthLog(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	// Last 3 entries have LatencyMs 7, 8, 9.
	if got[0].LatencyMs != 7 || got[2].LatencyMs != 9 {
		t.Errorf("wrong tail entries: %+v", got)
	}
}

// TestHealthSummary_Aggregation verifies per-provider aggregation.
func TestHealthSummary_Aggregation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.jsonl")
	now := time.Now().UTC()

	entries := []HealthEntry{
		{Timestamp: now.Add(-3 * time.Hour).Format(time.RFC3339), Provider: "agoda", Operation: "search", Status: "ok", LatencyMs: 100, Results: 4},
		{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339), Provider: "agoda", Operation: "search", Status: "ok", LatencyMs: 200, Results: 8},
		{Timestamp: now.Add(-1 * time.Hour).Format(time.RFC3339), Provider: "agoda", Operation: "search", Status: "error", LatencyMs: 50, Error: "http 403", HintCode: string(FixHintAkamaiBlock)},
		{Timestamp: now.Add(-48 * time.Hour).Format(time.RFC3339), Provider: "booking", Operation: "search", Status: "timeout", LatencyMs: 30000, Error: "deadline exceeded", ErrorClass: string(FixHintTLSTimeout)},
	}

	f, _ := os.Create(path)
	for _, e := range entries {
		b, _ := json.Marshal(e)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	summary := HealthSummary(dir)
	if len(summary) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(summary))
	}

	agoda := summary["agoda"]
	if agoda.TotalCalls != 3 {
		t.Errorf("agoda total_calls: got %d, want 3", agoda.TotalCalls)
	}
	if agoda.SuccessCount != 2 {
		t.Errorf("agoda success_count: got %d, want 2", agoda.SuccessCount)
	}
	if agoda.ErrorCount != 1 {
		t.Errorf("agoda error_count: got %d, want 1", agoda.ErrorCount)
	}
	wantRate := 2.0 / 3.0
	if agoda.SuccessRate < wantRate-0.01 || agoda.SuccessRate > wantRate+0.01 {
		t.Errorf("agoda success_rate: got %f, want ~%f", agoda.SuccessRate, wantRate)
	}
	wantAvg := int64((100 + 200 + 50) / 3)
	if agoda.AvgLatencyMs != wantAvg {
		t.Errorf("agoda avg_latency: got %d, want %d", agoda.AvgLatencyMs, wantAvg)
	}
	if agoda.LastError != "http 403" {
		t.Errorf("agoda last_error: got %q", agoda.LastError)
	}
	if agoda.TotalResults != 12 {
		t.Errorf("agoda total_results: got %d, want 12", agoda.TotalResults)
	}
	if agoda.AvgResults != 6 {
		t.Errorf("agoda avg_results: got %f, want 6", agoda.AvgResults)
	}
	if agoda.LastResults != 8 {
		t.Errorf("agoda last_results: got %d, want 8", agoda.LastResults)
	}
	if agoda.Freshness != "fresh" {
		t.Errorf("agoda freshness: got %q, want fresh", agoda.Freshness)
	}
	if agoda.LastErrorClass != string(FixHintAkamaiBlock) {
		t.Errorf("agoda last_error_class: got %q, want %q", agoda.LastErrorClass, FixHintAkamaiBlock)
	}

	booking := summary["booking"]
	if booking.TimeoutCount != 1 {
		t.Errorf("booking timeout_count: got %d, want 1", booking.TimeoutCount)
	}
	if booking.ErrorCount != 0 {
		t.Errorf("booking error_count: got %d, want 0", booking.ErrorCount)
	}
	if booking.Freshness != "stale" {
		t.Errorf("booking freshness: got %q, want stale", booking.Freshness)
	}
	if booking.LastErrorClass != string(FixHintTLSTimeout) {
		t.Errorf("booking last_error_class: got %q, want %q", booking.LastErrorClass, FixHintTLSTimeout)
	}
}

func TestSanitizeHealthEntryRedactsSecrets(t *testing.T) {
	entry := sanitizeHealthEntry(HealthEntry{
		Provider: "secret-provider",
		Status:   "error",
		Error:    "GET https://api.example/search?api_key=secret123&session_token=tok456 failed Authorization: Bearer abc.def",
		HintCode: string(FixHintRateLimited),
	})
	if strings.Contains(entry.Error, "secret123") || strings.Contains(entry.Error, "tok456") || strings.Contains(entry.Error, "abc.def") {
		t.Fatalf("secret leaked after redaction: %s", entry.Error)
	}
	if entry.ErrorClass != string(FixHintRateLimited) {
		t.Fatalf("ErrorClass = %q, want %q", entry.ErrorClass, FixHintRateLimited)
	}
}

func TestCircuitBreakerHealthStates(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	if got := CircuitBreakerHealth(&ProviderConfig{ErrorCount: 1}, now); got.State != "closed" {
		t.Fatalf("state = %q, want closed", got.State)
	}
	open := CircuitBreakerHealth(&ProviderConfig{
		ErrorCount:  circuitBreakerThreshold,
		LastErrorAt: now.Add(-time.Minute),
	}, now)
	if open.State != "open" || open.NextRetryAt == "" || open.FixHint == "" {
		t.Fatalf("open circuit health = %#v", open)
	}
	half := CircuitBreakerHealth(&ProviderConfig{
		ErrorCount:  circuitBreakerThreshold,
		LastErrorAt: now.Add(-(circuitBreakerCooldown + time.Minute)),
	}, now)
	if half.State != "half_open" {
		t.Fatalf("state = %q, want half_open", half.State)
	}
}

// TestLogHealth_NonBlocking verifies LogHealth doesn't block and the entry
// is eventually written to the log.
func TestLogHealth_NonBlocking(t *testing.T) {
	// LogHealth uses the package-level background goroutine which writes to
	// ~/.trvl/health.jsonl. For isolation we test appendHealthEntry directly.
	dir := t.TempDir()
	path := filepath.Join(dir, "health.jsonl")

	entry := HealthEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Provider:  "test-provider",
		Operation: "search",
		Status:    "ok",
		LatencyMs: 42,
		Results:   7,
	}

	if err := appendHealthEntryTo(path, entry); err != nil {
		t.Fatalf("appendHealthEntry: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test-provider") {
		t.Errorf("entry not found in file: %s", data)
	}
}

// TestHealthLog_Rotate verifies that a file exceeding healthLogMaxBytes gets
// rotated.
func TestHealthLog_Rotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.jsonl")

	// Write a file slightly above the rotate threshold.
	big := make([]byte, healthLogMaxBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatal(err)
	}

	entry := HealthEntry{Provider: "p", Status: "ok", LatencyMs: 1}
	if err := appendHealthEntryTo(path, entry); err != nil {
		t.Fatalf("appendHealthEntry: %v", err)
	}

	// health.jsonl.1 should now exist (the old file renamed).
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("rotated file not found: %v", err)
	}
	// New health.jsonl should be small.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= healthLogMaxBytes {
		t.Errorf("new file too large after rotation: %d", info.Size())
	}
}

// TestHealthSummary_Empty verifies no panic on empty dir.
func TestHealthSummary_Empty(t *testing.T) {
	dir := t.TempDir()
	s := HealthSummary(dir)
	if len(s) != 0 {
		t.Errorf("expected empty summary, got %v", s)
	}
}

// appendHealthEntryTo is a test-only helper that writes to an explicit path
// (bypassing the ~/.trvl default) so tests remain hermetic.
func appendHealthEntryTo(path string, entry HealthEntry) error {
	info, statErr := os.Stat(path)
	if statErr == nil && info.Size() >= healthLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
