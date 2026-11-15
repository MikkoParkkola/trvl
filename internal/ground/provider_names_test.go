package ground

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// launchNamesInSearch reads the provider names search.go launches, so a new
// launch cannot be added without the filter accepting it.
func launchNamesInSearch(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("search.go")
	if err != nil {
		t.Fatalf("read search.go: %v", err)
	}
	re := regexp.MustCompile(`launchProvider\(&wg, results, "([^"]+)"`)
	var names []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		names = append(names, m[1])
	}
	if len(names) < 20 {
		t.Fatalf("found only %d launchProvider names in search.go; the scan pattern is stale", len(names))
	}
	return names
}

// TestValidProviderNames_CoverLaunchesAndAliases pins D4: every launch name,
// every marketed name and the documented aliases are valid filter names.
func TestValidProviderNames_CoverLaunchesAndAliases(t *testing.T) {
	valid := map[string]bool{}
	for _, n := range ValidProviderNames() {
		valid[n] = true
	}
	want := append(launchNamesInSearch(t), MarketedProviderNames()...)
	want = append(want, "distribusion", "rome2rio", "eurostar snap", "digitransit")
	for _, n := range want {
		if !valid[n] {
			t.Errorf("%q is not a valid provider name", n)
		}
	}
}

// TestValidateProviderNames_TrimsAndAccepts pins that whitespace around names
// is trimmed and known names pass.
func TestValidateProviderNames_TrimsAndAccepts(t *testing.T) {
	got, err := ValidateProviderNames([]string{" flixbus ", "vr", "eurostar snap"})
	if err != nil {
		t.Fatalf("ValidateProviderNames: %v", err)
	}
	want := []string{"flixbus", "vr", "eurostar snap"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("names = %q, want %q", got, want)
	}
}

// TestValidateProviderNames_RejectsUnknown pins that an unknown name is an
// error that names the valid choices.
func TestValidateProviderNames_RejectsUnknown(t *testing.T) {
	_, err := ValidateProviderNames([]string{"flixbus", "flixbuss"})
	if err == nil {
		t.Fatal("unknown provider \"flixbuss\" accepted")
	}
	for _, s := range []string{"flixbuss", "flixbus", "regiojet", "ferryhopper"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not mention %q", err, s)
		}
	}
}

// TestAttachGroundHackSaving_PassesSearchScope pins D4's plumbing: the hacks
// engine receives the caller's provider filter and no-cache flag.
func TestAttachGroundHackSaving_PassesSearchScope(t *testing.T) {
	var got HackComposeRequest
	withStubComposer(t, func(_ context.Context, req HackComposeRequest) *models.HackSaving {
		got = req
		return nil
	})
	opts := SearchOptions{Providers: []string{"flixbus"}, ExcludeProviders: []string{"rome2rio"}, NoCache: true}
	attachGroundHackSaving(context.Background(), okGroundResult(), "Prague", "Vienna", "2026-07-01", opts)
	if strings.Join(got.Providers, ",") != "flixbus" || strings.Join(got.ExcludeProviders, ",") != "rome2rio" || !got.NoCache {
		t.Errorf("compose request scope = providers %v exclude %v nocache %v; want [flixbus] [rome2rio] true", got.Providers, got.ExcludeProviders, got.NoCache)
	}
}
