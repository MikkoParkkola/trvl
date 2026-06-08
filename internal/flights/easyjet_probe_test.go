package flights

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSearchEasyjet_LiveProbe documents the empirically-verified Akamai block on
// easyJet's public availability endpoint. It is opt-in (TRVL_TEST_LIVE_PROBES=1)
// and EASYJET_API_BASE-gated: it never runs in the default offline suite.
//
// Run it ONLY when an operator has configured a reachable endpoint
// (EASYJET_API_BASE) — e.g. an authorised partner endpoint or a self-hosted
// proxy that handles the bot challenge. Against the raw public host it returns
// HTTP 403 (text/html), which the adapter surfaces as a typed error rather than
// a fabricated empty result.
func TestSearchEasyjet_LiveProbe(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probe: set TRVL_TEST_LIVE_PROBES=1 to run")
	}
	if os.Getenv("EASYJET_API_BASE") == "" {
		t.Skip("easyJet is opt-in: set EASYJET_API_BASE to a reachable availability endpoint")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	out, err := SearchEasyjet(ctx, "AMS", "BCN", date, "EUR", SearchOptions{Adults: 1})
	if err != nil {
		// A typed error (e.g. Akamai 403 on a still-blocked base) is an
		// acceptable, honest outcome — log it rather than fail the probe.
		t.Logf("easyJet live probe returned a typed error (expected when base is bot-defended): %v", err)
		return
	}
	t.Logf("easyJet live probe returned %d flights", len(out))
	for _, f := range out {
		if len(f.Legs) == 0 || f.Legs[0].AirlineCode != "U2" {
			t.Errorf("unexpected non-U2 result from easyJet: %+v", f)
		}
	}
}
