package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/spf13/cobra"
)

// seedStatusHealthLog points HOME at a temp dir holding a health.jsonl with one
// successful "kiwi" call, so runStatus has deterministic input.
func seedStatusHealthLog(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	dir := filepath.Join(tmp, ".trvl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "health.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	entry := providers.HealthEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Provider:  "kiwi",
		Operation: "search",
		Status:    "ok",
		LatencyMs: 95,
		Results:   12,
	}
	line, _ := json.Marshal(entry)
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

// captureStdout is defined in display_coverage_test.go and reused here.

func newStatusTestCmd(format string) *cobra.Command {
	cmd := &cobra.Command{Use: "status"}
	cmd.Flags().String("format", format, "output format")
	return cmd
}

func TestRunStatus_Table(t *testing.T) {
	seedStatusHealthLog(t)

	out := captureStdout(t, func() {
		if err := runStatus(newStatusTestCmd("table"), nil); err != nil {
			t.Errorf("runStatus: %v", err)
		}
	})

	for _, want := range []string{"trvl status", "kiwi", "healthy"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestRunStatus_JSON(t *testing.T) {
	seedStatusHealthLog(t)

	out := captureStdout(t, func() {
		if err := runStatus(newStatusTestCmd("json"), nil); err != nil {
			t.Errorf("runStatus: %v", err)
		}
	})

	var parsed struct {
		Providers []providers.StatusRow `json:"providers"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("status --format json is not valid JSON: %v\n%s", err, out)
	}
	if len(parsed.Providers) != 1 || parsed.Providers[0].Provider != "kiwi" {
		t.Fatalf("providers = %+v, want one kiwi row", parsed.Providers)
	}
	if parsed.Providers[0].SuccessCount != 1 {
		t.Errorf("kiwi success count = %d, want 1", parsed.Providers[0].SuccessCount)
	}
}

func TestRunStatus_NoData(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	out := captureStdout(t, func() {
		if err := runStatus(newStatusTestCmd("table"), nil); err != nil {
			t.Errorf("runStatus: %v", err)
		}
	})
	if !strings.Contains(out, "No provider activity recorded yet") {
		t.Errorf("empty-log output missing guidance:\n%s", out)
	}
}
