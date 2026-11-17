package hacks

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestDetectorInputFromGround_CarriesSearchScope pins D4's hacks plumbing:
// the ground compose request's provider filter and no-cache flag reach the
// detector input.
func TestDetectorInputFromGround_CarriesSearchScope(t *testing.T) {
	in := detectorInputFromGround(ground.HackComposeRequest{
		Origin: "Prague", Destination: "Vienna", Date: "2026-07-01", Currency: "EUR", NaivePrice: 20,
		Providers: []string{"flixbus"}, ExcludeProviders: []string{"rome2rio"}, NoCache: true,
		Type: "bus", MaxPrice: 40, AllowBrowserFallbacks: true,
	})
	if in.Origin != "Prague" || in.Destination != "Vienna" || in.Date != "2026-07-01" || in.Currency != "EUR" || in.NaivePrice != 20 {
		t.Errorf("route fields not mapped: %+v", in)
	}
	if strings.Join(in.GroundProviders, ",") != "flixbus" || strings.Join(in.GroundExcludeProviders, ",") != "rome2rio" || !in.NoCache {
		t.Errorf("scope = providers %v exclude %v nocache %v; want [flixbus] [rome2rio] true", in.GroundProviders, in.GroundExcludeProviders, in.NoCache)
	}
	if in.GroundType != "bus" || in.GroundMaxPrice != 40 || !in.GroundAllowBrowserFallbacks || in.search != groundSearch {
		t.Errorf("type %q max %v fallbacks %v kind %v; want bus 40 true ground", in.GroundType, in.GroundMaxPrice, in.GroundAllowBrowserFallbacks, in.search)
	}
}

// scopeCapture records the SearchOptions of every ground search a detector
// makes; detectors may search concurrently.
type scopeCapture struct {
	mu   sync.Mutex
	opts []ground.SearchOptions
}

func (c *scopeCapture) search(_ context.Context, _, _, _ string, opts ground.SearchOptions) (*models.GroundSearchResult, error) {
	c.mu.Lock()
	c.opts = append(c.opts, opts)
	c.mu.Unlock()
	return &models.GroundSearchResult{Success: false}, nil
}

func (c *scopeCapture) assertScoped(t *testing.T, minCalls int) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.opts) < minCalls {
		t.Fatalf("%d ground searches captured, want at least %d", len(c.opts), minCalls)
	}
	for i, o := range c.opts {
		if strings.Join(o.Providers, ",") != "flixbus" || strings.Join(o.ExcludeProviders, ",") != "rome2rio" || !o.NoCache {
			t.Errorf("ground search %d scope = providers %v exclude %v nocache %v; want [flixbus] [rome2rio] true", i, o.Providers, o.ExcludeProviders, o.NoCache)
		}
		// Type is not checked: a detector that needs a ferry or a train sets
		// its own, which is the point of its search.
		if o.MaxPrice != 40 || !o.AllowBrowserFallbacks {
			t.Errorf("ground search %d max %v fallbacks %v; want 40 true", i, o.MaxPrice, o.AllowBrowserFallbacks)
		}
	}
}

// scoped adds the caller's ground search scope to a detector input.
func scoped(in DetectorInput) DetectorInput {
	in.GroundProviders = []string{"flixbus"}
	in.GroundExcludeProviders = []string{"rome2rio"}
	in.NoCache = true
	in.GroundType = "bus"
	in.GroundMaxPrice = 40
	in.GroundAllowBrowserFallbacks = true
	return in
}

func sameCurrencyOnly(_ context.Context, amount float64, from, to string) (float64, string) {
	if from == to {
		return amount, to
	}
	return 0, ""
}

func anyFlight400EUR(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
	return chSearchResult(400, "EUR"), nil
}

// TestDetectorGroundSearches_ForwardSearchScope pins D4 at every ground-search
// call site in internal/hacks, through the detector that reaches it: the
// groundLegPriceInTarget helper (positioning, ferry, open-jaw ground), the
// rail quote, night transport, both return-split legs and skip-flight.
func TestDetectorGroundSearches_ForwardSearchScope(t *testing.T) {
	const d, rd = "2026-07-01", "2026-07-08"
	cases := []struct {
		name     string
		minCalls int
		run      func(t *testing.T, c *scopeCapture)
	}{
		{"multimodal positioning", 1, func(t *testing.T, c *scopeCapture) {
			detectMultiModalPositioning(context.Background(), scoped(DetectorInput{Origin: "HEL", Destination: "BCN", Date: d, Currency: "EUR",
				SearchOverride: chMultiModalFlights, GroundSearchOverride: c.search}))
		}},
		{"ferry positioning", 1, func(t *testing.T, c *scopeCapture) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("USERPROFILE", dir)
			detectFerryPositioning(context.Background(), scoped(DetectorInput{Origin: "HEL", Destination: "BCN", Date: d, Currency: "EUR",
				SearchOverride: anyFlight400EUR, GroundSearchOverride: c.search}))
		}},
		{"open-jaw ground", 1, func(t *testing.T, c *scopeCapture) {
			detectMultiModalOpenJawGround(context.Background(), scoped(DetectorInput{Origin: "HEL", Destination: "DBV", Date: d, Currency: "EUR",
				SearchOverride: anyFlight400EUR, GroundSearchOverride: c.search}))
		}},
		{"skip flight", 1, func(t *testing.T, c *scopeCapture) {
			detectMultiModalSkipFlight(context.Background(), scoped(DetectorInput{Origin: "PRG", Destination: "VIE", Date: d, Currency: "EUR",
				SearchOverride: anyFlight400EUR, GroundSearchOverride: c.search}))
		}},
		{"night transport", 1, func(t *testing.T, c *scopeCapture) {
			// Cancelled so that, before the fix, the unscoped real search returns
			// at once instead of dialling out.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			detectNightTransport(ctx, scoped(DetectorInput{Origin: "HEL", Destination: "PRG", Date: d, Currency: "EUR",
				GroundSearchOverride: c.search}))
		}},
		{"return split, both legs", 2, func(t *testing.T, c *scopeCapture) {
			withReturnSplitFlightSearch(t, func(_ context.Context, _, _, _ string, opts flights.SearchOptions) (*models.FlightSearchResult, error) {
				if opts.ReturnDate != "" {
					return returnSplitMakeFlight(300, "EUR"), nil
				}
				return returnSplitMakeFlight(150, "EUR"), nil
			})
			withReturnSplitGroundSearch(t, c.search)
			detectMultiModalReturnSplit(context.Background(), scoped(DetectorInput{Origin: "HEL", Destination: "TLL", Date: d, ReturnDate: rd, Currency: "EUR"}))
		}},
		{"rail quote", 1, func(t *testing.T, c *scopeCapture) {
			withRailFlyFlightSearcher(t, func(_ context.Context, _ *batchexec.Client, origin, _, _ string, _ flights.SearchOptions) (*models.FlightSearchResult, error) {
				if origin == "AMS" {
					return chSearchResult(280, "EUR"), nil
				}
				return chSearchResult(210, "EUR"), nil
			})
			withRailGroundSearcher(t, c.search)
			detectRailFlyArbitrage(context.Background(), scoped(DetectorInput{Origin: "AMS", Destination: "BCN", Date: d, Currency: "EUR"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withConvertCurrencyFn(t, sameCurrencyOnly)
			c := &scopeCapture{}
			tc.run(t, c)
			c.assertScoped(t, tc.minCalls)
		})
	}
}

// TestGroundHackComposer_CarriesSearchScope pins the registered composer
// chain: the request ground search hands the engine reaches the detectors
// with its provider filter and no-cache flag.
func TestGroundHackComposer_CarriesSearchScope(t *testing.T) {
	var got *DetectorInput
	compose := groundHackComposer(func(_ context.Context, in DetectorInput) []Hack {
		got = &in
		return nil
	})
	compose(context.Background(), ground.HackComposeRequest{
		Origin: "Prague", Destination: "Vienna", Date: "2026-07-01", Currency: "EUR", NaivePrice: 20,
		Providers: []string{"flixbus"}, ExcludeProviders: []string{"rome2rio"}, NoCache: true,
		Type: "bus", MaxPrice: 40, AllowBrowserFallbacks: true,
	})
	if got == nil {
		t.Fatal("composer never ran the detectors")
	}
	if strings.Join(got.GroundProviders, ",") != "flixbus" || strings.Join(got.GroundExcludeProviders, ",") != "rome2rio" || !got.NoCache {
		t.Errorf("detector input scope = providers %v exclude %v nocache %v; want [flixbus] [rome2rio] true", got.GroundProviders, got.GroundExcludeProviders, got.NoCache)
	}
	if got.GroundType != "bus" || got.GroundMaxPrice != 40 || !got.GroundAllowBrowserFallbacks {
		t.Errorf("detector input type %q max %v fallbacks %v; want bus 40 true", got.GroundType, got.GroundMaxPrice, got.GroundAllowBrowserFallbacks)
	}
}
