package flights

import (
	"context"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// eligibility_boost_test.go adds coverage for the low/zero *SearchEligible and *EligibleOptions
// family and related skip paths. Table driven. Reuses batchexec test client pattern and run*Provider
// skip semantics from providers_concurrent.go so no network is required for the !eligible branches.

func TestEligibleOptions_Skiplagged_Table(t *testing.T) {
	cases := []struct {
		name string
		opts SearchOptions
		want bool
	}{
		{"default oneway", SearchOptions{}, true},
		{"rt supported", SearchOptions{ReturnDate: "2026-07-20"}, true},
		{"airlines block", SearchOptions{Airlines: []string{"BA"}}, false},
		{"alliances block", SearchOptions{Alliances: []string{"ONEWORLD"}}, false},
		{"bags block", SearchOptions{CarryOnBags: 1}, false},
		{"checked block", SearchOptions{CheckedBags: 2}, false},
		{"require checked block", SearchOptions{RequireCheckedBag: true}, false},
		{"exclude basic block", SearchOptions{ExcludeBasic: true}, false},
		{"emissions block", SearchOptions{LessEmissions: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := skiplaggedEligibleOptions(tc.opts); got != tc.want {
				t.Errorf("skiplaggedEligibleOptions = %v want %v", got, tc.want)
			}
		})
	}
}

func TestSearchEligible_ClientGuard_Table(t *testing.T) {
	shared := batchexec.SharedClient()
	inj := batchexec.NewTestClient("http://127.0.0.1:0")

	type entry struct {
		name   string
		el     func(*batchexec.Client, SearchOptions) bool
		opts   SearchOptions
		client *batchexec.Client
		want   bool
	}
	entries := []entry{
		{"skiplagged shared good", skiplaggedSearchEligible, SearchOptions{}, shared, true},
		{"skiplagged injected", skiplaggedSearchEligible, SearchOptions{}, inj, false},
		{"skiplagged bad opts", skiplaggedSearchEligible, SearchOptions{Airlines: []string{"FR"}}, shared, false},
		{"kiwi shared good", kiwiSearchEligible, SearchOptions{}, shared, true},
		{"kiwi rt bad", kiwiSearchEligible, SearchOptions{ReturnDate: "2026-08-01"}, shared, false},
		{"ryanair shared good (oneway econ)", ryanairSearchEligible, SearchOptions{}, shared, true},
		{"wizzair shared good", wizzairSearchEligible, SearchOptions{}, shared, true},
	}
	for _, e := range entries {
		t.Run(e.name, func(t *testing.T) {
			if got := e.el(e.client, e.opts); got != e.want {
				t.Errorf("%s = %v want %v", e.name, got, e.want)
			}
		})
	}
}

func TestTransaviaSearchEligible_ConfigAndClient(t *testing.T) {
	shared := batchexec.SharedClient()
	inj := batchexec.NewTestClient("http://127.0.0.1:0")
	t.Setenv("TRANSAVIA_API_KEY", "")
	if transaviaSearchEligible(shared, SearchOptions{}) {
		t.Error("no key => ineligible")
	}
	t.Setenv("TRANSAVIA_API_KEY", "dummykey")
	if transaviaSearchEligible(inj, SearchOptions{}) {
		t.Error("injected => ineligible")
	}
	if transaviaSearchEligible(shared, SearchOptions{ReturnDate: "2026-07-01"}) {
		t.Error("rt => ineligible for transavia")
	}
	if !transaviaSearchEligible(shared, SearchOptions{Airlines: []string{"TO"}}) {
		t.Error("TO filter ok")
	}
}

func TestRunProviderEligibilitySkips(t *testing.T) {
	shared := batchexec.SharedClient()
	// These hit the eligibility early return inside runXXX without calling the actual provider search.
	out := runSkiplaggedProvider(context.Background(), shared, "HEL", "LHR", "2026-07-01", SearchOptions{CarryOnBags: 1})
	if out.status.Status != "skipped" || out.status.ID != "skiplagged" {
		t.Errorf("skiplagged skip: %+v", out.status)
	}

	t.Setenv("TRANSAVIA_API_KEY", "k")
	out = runTransaviaProvider(context.Background(), shared, "HEL", "LHR", "2026-07-01", "EUR", SearchOptions{})
	// even with key, if we wanted to test bad opts for transavia eligible
	// (transavia allows no rt but allows no bags req)
	// use airline filter wrong
	out = runTransaviaProvider(context.Background(), shared, "HEL", "LHR", "2026-07-01", "EUR", SearchOptions{Airlines: []string{"BA"}})
	if out.status.Status != "skipped" {
		t.Errorf("transavia wrong airline skip: got %s", out.status.Status)
	}
}
