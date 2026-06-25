//go:build proof

package flights

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSearchVueling_LiveProbe documents the empirically-verified Akamai Bot
// Manager block on Vueling's public booking/availability engine. It is opt-in
// (TRVL_TEST_LIVE_PROBES=1) and VUELING_API_BASE-gated: it never runs in the
// default offline suite.
//
// Live probe (2026-06-25, AMS→BCN one-way ~30 days out): the booking engine at
// tickets.vueling.com is served by AkamaiGHost and returns the full Akamai Bot
// Manager cookie suite — _abck (with a ~-1~ unsolved-sensor token), bm_ss,
// bm_s, bm_so, bm_sz and akacd_dc_tickets. The public www.vueling.com host
// likewise carries x-akamai-transformed and bm_ss. A plain programmatic client
// cannot obtain a valid Akamai sensor payload (generated only by executing
// Akamai's obfuscated JS in a real browser), so availability requests are
// challenged. There is NO clean public unauthenticated JSON endpoint reachable
// from a static Go binary.
//
// Run it ONLY when an operator has configured a reachable endpoint
// (VUELING_API_BASE) — e.g. an authorised partner endpoint or a self-hosted
// proxy that handles the bot challenge. Against the raw public host the adapter
// surfaces a typed error rather than a fabricated empty result.
func TestSearchVueling_LiveProbe(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probe: set TRVL_TEST_LIVE_PROBES=1 to run")
	}
	if os.Getenv("VUELING_API_BASE") == "" {
		t.Skip("Vueling is opt-in: set VUELING_API_BASE to a reachable availability endpoint")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	out, err := SearchVueling(ctx, "AMS", "BCN", date, "EUR", SearchOptions{Adults: 1})
	if err != nil {
		// A typed error (e.g. Akamai 403 on a still-blocked base) is an
		// acceptable, honest outcome — log it rather than fail the probe.
		t.Logf("Vueling live probe returned a typed error (expected when base is bot-defended): %v", err)
		return
	}
	t.Logf("Vueling live probe returned %d flights", len(out))
	for _, f := range out {
		if len(f.Legs) == 0 || f.Legs[0].AirlineCode != "VY" {
			t.Errorf("unexpected non-VY result from Vueling: %+v", f)
		}
	}
}
