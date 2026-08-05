package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// TestHandleCheckWatches_LiveProbe re-prices a real watch against live providers
// end-to-end through the MCP handler. This is the regression guard that would
// have caught the original bug (check_watches always returned current_price 0):
// a stubbed checker fails this. Probe-gated; run nightly in CI
// (.github/workflows/live-probes.yml).
func TestHandleCheckWatches_LiveProbe(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("hits live flight APIs; set TRVL_TEST_LIVE_PROBES=1 to run")
	}
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	store, err := watch.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore: %v", err)
	}
	if _, _, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-09-15",
		Currency:    "EUR",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, structured, err := handleCheckWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("handleCheckWatches: %v", err)
	}
	raw, _ := json.Marshal(structured)
	// A throttled night (Google 429 from the CI datacenter IP) re-prices to 0
	// the same way a stubbed checker would, but for a transient reason. If the
	// structured output carries a rate-limit/transient marker, treat the 0 as
	// noise and skip rather than red the nightly.
	if testutil.IsTransientMsg(string(raw)) {
		t.Skipf("skipping: transient provider noise (not a regression): %s", string(raw))
	}
	if strings.Contains(string(raw), `"current_price":0`) {
		t.Errorf("live probe returned current_price 0 — re-check did not produce a real price: %s", string(raw))
	}
}
