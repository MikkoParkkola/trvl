package ground

import (
	"context"
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// TestBrowserScraperRefusesAnExplicitDecline covers the bypass an adversarial
// review found in the second cut of #521.
//
// The decline gate had been placed on both browser-spawning functions in
// internal/providers -- and that was the whole repo, as far as the change went
// looking. It was not: internal/ground has a THIRD chromedp.NewExecAllocator,
// reached by BrowserScrapeRoutes, which trainline.go and sncf.go both call
// unconditionally after the challenge path fails. So a user who declined got
// ErrTier2Disabled from the challenge, fell straight through to the scraper,
// and had Chrome launched anyway.
//
// The tests could not have caught it, because they all lived in
// internal/providers and tested the two allocators that were already gated.
// This one lives where the third allocator is.
func TestBrowserScraperRefusesAnExplicitDecline(t *testing.T) {
	t.Setenv("TRVL_NO_TIER2_CDP", "1")

	// The allocator itself, not just the entrypoint above it: a caller reaching
	// past BrowserScrapeRoutes must not be able to start a browser either.
	if _, _, err := newBrowserScraperContext(context.Background()); !errors.Is(err, providers.ErrTier2Disabled) {
		t.Fatalf("newBrowserScraperContext: err = %v, want ErrTier2Disabled", err)
	}

	for _, provider := range []string{"trainline", "sncf"} {
		if _, err := BrowserScrapeRoutes(context.Background(), provider, "Paris", "Lyon", "2026-09-01", "EUR"); !errors.Is(err, providers.ErrTier2Disabled) {
			t.Fatalf("BrowserScrapeRoutes(%s): err = %v, want ErrTier2Disabled", provider, err)
		}
	}
}

// TestBrowserScraperAllocatorOpensWithoutADecline is the other half: the gate
// must refuse a decline and only a decline. Building the allocator does not
// start a browser -- chromedp launches on the first Run -- so this asserts the
// gate lets the path through without spawning anything.
func TestBrowserScraperAllocatorOpensWithoutADecline(t *testing.T) {
	t.Setenv("TRVL_NO_TIER2_CDP", "")
	t.Setenv("TRVL_TIER2_CDP", "")

	taskCtx, cancel, err := newBrowserScraperContext(context.Background())
	if err != nil {
		t.Fatalf("newBrowserScraperContext without a decline: err = %v, want nil", err)
	}
	if taskCtx == nil || cancel == nil {
		t.Fatal("newBrowserScraperContext returned a nil context or cancel func")
	}
	cancel()
}
