package models

import (
	"testing"
	"time"
)

func TestClassifyFreshness(t *testing.T) {
	now := time.Now()
	if got := ClassifyFreshness("ryanair", now.Add(-10*time.Minute), now); got != FreshnessLive {
		t.Errorf("10m old ryanair = %q, want live", got)
	}
	if got := ClassifyFreshness("ryanair", now.Add(-120*time.Minute), now); got != FreshnessRecent {
		t.Errorf("2h old ryanair = %q, want recent", got)
	}
	if got := ClassifyFreshness("ryanair", now.Add(-400*time.Minute), now); got != FreshnessStale {
		t.Errorf("400m old ryanair = %q, want stale", got)
	}
	if got := ClassifyFreshness("ryanair", time.Time{}, now); got != FreshnessRecent {
		t.Errorf("zero retrievedAt = %q, want recent", got)
	}
	if got := ClassifyFreshness("mystery_provider", now.Add(-5*time.Minute), now); got != FreshnessLive {
		t.Errorf("unregistered provider 5m = %q, want live", got)
	}
}

func TestMayClaimFreshSuperlative(t *testing.T) {
	if MayClaimFreshSuperlative(FreshnessStale) {
		t.Error("stale price must not allow superlatives")
	}
	if !MayClaimFreshSuperlative(FreshnessLive) || !MayClaimFreshSuperlative(FreshnessRecent) {
		t.Error("live/recent should allow superlatives")
	}
}

func TestSourceProfileFor(t *testing.T) {
	if !SourceProfileFor("RYANAIR").API {
		t.Error("ryanair should be API (case-insensitive)")
	}
	if SourceProfileFor("google_flights").API {
		t.Error("google_flights is a scrape, API should be false")
	}
}
