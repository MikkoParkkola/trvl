package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAwardsPrefs writes a preferences.json into a temp HOME and points
// the process at it. Not parallel-safe (mutates HOME); callers must not
// run in parallel.
func writeAwardsPrefs(t *testing.T, prefsJSON string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	dir := filepath.Join(home, ".trvl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "preferences.json"), []byte(prefsJSON), 0o644); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
}

func runAwards(t *testing.T, args ...string) string {
	t.Helper()
	cmd := awardsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("awards execute: %v", err)
	}
	return buf.String()
}

// TestAwardsCmd_SeedsBalancesFromProfile proves that with no --balance
// flag, the program set is seeded from the saved loyalty profile: a
// 60k VS balance makes the 50k VS seat affordable.
func TestAwardsCmd_SeedsBalancesFromProfile(t *testing.T) {
	writeAwardsPrefs(t, `{"frequent_flyer_programs":[{"airline_code":"VS","miles_balance":60000}]}`)

	out := runAwards(t, "AMS", "JFK",
		"--seat", "VS:50000:55.00:600.00:2026-08-01:economy")

	// The VS-native row must appear and be affordable ("yes").
	if !strings.Contains(out, "VS") {
		t.Fatalf("expected a VS row seeded from profile, got:\n%s", out)
	}
	if !strings.Contains(out, "yes") {
		t.Fatalf("expected an affordable (yes) row from the 60k VS profile balance, got:\n%s", out)
	}
}

// TestAwardsCmd_ExplicitBalanceOverridesProfile proves an explicit
// --balance always wins: the profile would seed VS:60000 (affordable),
// but an explicit VS:10000 leaves the 50k seat unaffordable.
func TestAwardsCmd_ExplicitBalanceOverridesProfile(t *testing.T) {
	writeAwardsPrefs(t, `{"frequent_flyer_programs":[{"airline_code":"VS","miles_balance":60000}]}`)

	out := runAwards(t, "AMS", "JFK",
		"--seat", "VS:50000:55.00:600.00:2026-08-01:economy",
		"--balance", "VS:10000",
		"--min-cpp", "0", // keep the short row visible
	)

	// With only 10k VS explicitly, the VS native row must be short ("no");
	// the profile's 60k must not leak in.
	if strings.Contains(out, "yes") {
		t.Fatalf("explicit VS:10000 should leave the 50k seat unaffordable, but found an affordable row:\n%s", out)
	}
	if !strings.Contains(out, "no") {
		t.Fatalf("expected an unaffordable (no) row under the explicit 10k balance, got:\n%s", out)
	}
}

// TestAwardsCmd_EmptyProfileNoRegression proves that with no profile and
// no --balance flag, behaviour is unchanged: every seat still surfaces a
// short row for guidance (no balances => nothing affordable).
func TestAwardsCmd_EmptyProfileNoRegression(t *testing.T) {
	writeAwardsPrefs(t, `{}`)

	out := runAwards(t, "AMS", "JFK",
		"--seat", "VS:50000:55.00:600.00:2026-08-01:economy",
		"--min-cpp", "0",
	)

	if strings.Contains(out, "yes") {
		t.Fatalf("empty profile + no balances should yield no affordable rows, got:\n%s", out)
	}
	// A guidance row should still render (FindSweetSpots keeps short rows).
	if !strings.Contains(out, "VS") {
		t.Fatalf("expected a VS guidance row even with no balances, got:\n%s", out)
	}
}
