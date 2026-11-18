package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
)

// TestGroundCmd_ProviderHelpListsMarketedProviders pins D4: the --provider
// help is built from the provider catalog, not a hand-typed subset.
func TestGroundCmd_ProviderHelpListsMarketedProviders(t *testing.T) {
	f := groundCmd().Flags().Lookup("provider")
	if f == nil {
		t.Fatal("ground has no --provider flag")
	}
	for _, name := range ground.MarketedProviderNames() {
		if !strings.Contains(f.Usage, name) {
			t.Errorf("--provider help does not mention %q: %q", name, f.Usage)
		}
	}
}

// TestGroundCmd_UnknownProviderIsAnError pins D4: an unknown provider name is
// an error that lists the valid names, so the command exits non-zero instead
// of silently searching nothing. The context is already cancelled so that,
// before the fix, the command stops at the search without dialling out.
func TestGroundCmd_UnknownProviderIsAnError(t *testing.T) {
	cmd := groundCmd()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	err := executeCommandExpectError(t, cmd, "Prague", "Vienna", "2026-07-01", "--provider", "flixbuss")
	if err == nil {
		t.Fatal("unknown provider accepted")
	}
	for _, s := range []string{"flixbuss", "flixbus", "regiojet"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not mention %q", err, s)
		}
	}
}
