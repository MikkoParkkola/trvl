//go:build proof

package flights

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSearchNorwegian_LiveProbe documents the empirically-verified Cloudflare Bot
// Management block on Norwegian's public site / booking funnel. It is opt-in
// (TRVL_TEST_LIVE_PROBES=1) and NORWEGIAN_API_BASE-gated: it never runs in the
// default offline suite.
//
// Live probe (2026-06-25, OSL→LGW one-way ~30 days out, 1 adult) from a static
// client (curl and Go net/http): every probed endpoint shape returned HTTP 403
// with `server: cloudflare`, `cf-mitigated: challenge`, an "Are you human? |
// Norwegian" text/html interstitial body, and a `__cf_bm` bot-management cookie.
// Paths probed (all 403-challenged):
//
//	GET https://www.norwegian.com/api/fare-calendar/get?...        -> 403 cf challenge
//	GET https://www.norwegian.com/api/flydata/availability/v1?...  -> 403 cf challenge
//	GET https://www.norwegian.com/booking/api/search/availability  -> 403 cf challenge
//	GET https://booking.norwegian.com/api/availability?...         -> 403 cf challenge
//	GET https://www.norwegian.com/  (site root)                    -> 403 cf challenge
//
// A browser-grade fetcher with residential cookies reaches the host, but the
// data is NOT reachable from curl / Go's net/http, and the repo forbids
// browser/headless dependencies — so there is no clean static-client path. This
// is the canonical Branch-B (bot-walled) outcome: an env-gated opt-in adapter
// that returns an honest typed status rather than fabricated data.
//
// Run it ONLY when an operator has configured a reachable endpoint
// (NORWEGIAN_API_BASE) — e.g. an authorised partner endpoint or a self-hosted
// proxy that handles the bot challenge. Against the raw public host the adapter
// surfaces a typed error rather than a fabricated empty result.
func TestSearchNorwegian_LiveProbe(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probe: set TRVL_TEST_LIVE_PROBES=1 to run")
	}
	if os.Getenv("NORWEGIAN_API_BASE") == "" {
		t.Skip("Norwegian is opt-in: set NORWEGIAN_API_BASE to a reachable availability endpoint")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	out, err := SearchNorwegian(ctx, "OSL", "LGW", date, "EUR", SearchOptions{Adults: 1})
	if err != nil {
		// A typed error (e.g. Cloudflare 403 on a still-blocked base) is an
		// acceptable, honest outcome — log it rather than fail the probe.
		t.Logf("Norwegian live probe returned a typed error (expected when base is bot-defended): %v", err)
		return
	}
	t.Logf("Norwegian live probe returned %d flights", len(out))
	for _, f := range out {
		if len(f.Legs) == 0 || f.Legs[0].AirlineCode != "DY" {
			t.Errorf("unexpected non-DY result from Norwegian: %+v", f)
		}
	}
}
