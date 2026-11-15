package ground

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
)

func statusByID(statuses []models.ProviderStatus) map[string]models.ProviderStatus {
	m := make(map[string]models.ProviderStatus, len(statuses))
	for _, s := range statuses {
		m[s.ID] = s
	}
	return m
}

func oneRoute(provider string) []models.GroundRoute {
	return []models.GroundRoute{{Provider: provider, Type: "bus", Price: 20, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-15T08:00:00"}}}
}

// TestCollectGroundResults_StatusMapping pins D3's aggregator mapping: a
// deadline or a limiter wait past it is a timeout; a valid empty answer is
// checked_no_hit; not-served is skipped; a breaker skip is circuit_broken and
// not a failure; a ProviderNotice, even wrapped, keeps routes with a fix hint,
// and without routes it is a failure carrying its Reason.
func TestCollectGroundResults_StatusMapping(t *testing.T) {
	results := make(chan providerResult, 9)
	results <- providerResult{name: "ferryhopper", err: fmt.Errorf("ferryhopper: request: %w", context.DeadlineExceeded)}
	results <- providerResult{name: "dfds", err: errors.New("dfds: rate limiter: rate: Wait(n=1) would exceed context deadline")}
	results <- providerResult{name: "vikingline"}
	results <- providerResult{name: "flixbus", routes: oneRoute("flixbus")}
	results <- providerResult{name: "eckeroline", routes: oneRoute("eckeroline"),
		err: fmt.Errorf("eckeroline: %w", &models.ProviderNotice{Reason: models.ReasonRateLimited, FixHint: "fares are from the timetable; retry later", Err: models.ErrRateLimited})}
	results <- providerResult{name: "stenaline",
		err: fmt.Errorf("stenaline: %w", &models.ProviderNotice{Reason: models.ReasonBlocked, FixHint: "try again later", Err: errors.New("blocked")})}
	results <- providerResult{name: "regiojet", err: fmt.Errorf("regiojet: %w", models.ErrNotApplicable)}
	results <- providerResult{name: "tallink", err: errCircuitBroken}
	close(results)

	routes, errMsgs, statuses := collectGroundResults(results)
	got := statusByID(statuses)
	if len(got) != 8 {
		t.Fatalf("want 8 status rows, got %d: %+v", len(got), statuses)
	}
	for id, want := range map[string]string{
		"ferryhopper": models.StatusTimeout,
		"dfds":        models.StatusTimeout,
		"vikingline":  models.StatusCheckedNoHit,
		"flixbus":     models.StatusOK,
		"eckeroline":  models.StatusOK,
		"regiojet":    models.StatusSkipped,
		"tallink":     models.StatusCircuitBroken,
	} {
		if got[id].Status != want {
			t.Errorf("%s status = %q, want %q", id, got[id].Status, want)
		}
	}
	if n := got["eckeroline"]; n.FixHint == "" || n.Results != 1 || n.Error != "" {
		t.Errorf("eckeroline notice row = %+v, want results 1, the fix hint and no error (an ok row is not a failure)", n)
	}
	if s := got["stenaline"]; s.Status == models.StatusOK || s.Status == models.StatusSkipped || s.Reason != models.ReasonBlocked {
		t.Errorf("stenaline notice-without-routes row = %+v, want a failure with reason blocked", s)
	}
	if r := got["regiojet"]; r.Reason != models.ReasonRouteNotServed {
		t.Errorf("regiojet not-served row reason = %q, want %q", r.Reason, models.ReasonRouteNotServed)
	}
	if len(routes) != 2 {
		t.Errorf("want the flixbus and eckeroline routes kept (2), got %d", len(routes))
	}
	if len(errMsgs) != 3 {
		t.Errorf("want 3 failure messages (2 timeouts, 1 notice without routes), got %d: %v", len(errMsgs), errMsgs)
	}
	c := models.ComputeCompleteness(statuses)
	missing := map[string]bool{}
	for _, id := range c.Missing {
		missing[id] = true
	}
	for _, id := range []string{"ferryhopper", "dfds", "stenaline"} {
		if !missing[id] {
			t.Errorf("completeness missing = %v, want it to include %s", c.Missing, id)
		}
	}
}

// swapGroundBreaker installs a breaker that trips on the first failure.
func swapGroundBreaker(t *testing.T) {
	t.Helper()
	orig := groundBreaker
	groundBreaker = breaker.NewWithConfig(1, time.Hour)
	t.Cleanup(func() { groundBreaker = orig })
}

func runLaunch(name string, fn func() ([]models.GroundRoute, error)) {
	results := make(chan providerResult, 1)
	var wg sync.WaitGroup
	launchProvider(&wg, results, name, fn)
	wg.Wait()
}

// TestLaunchProvider_BreakerOutcomes pins D3's breaker feed: a timeout and a
// notice without routes count as failures; a notice with routes is a success.
func TestLaunchProvider_BreakerOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		fn          func() ([]models.GroundRoute, error)
		wantTripped bool
	}{
		{"deadline", func() ([]models.GroundRoute, error) { return nil, fmt.Errorf("x: %w", context.DeadlineExceeded) }, true},
		{"limiter past deadline", func() ([]models.GroundRoute, error) {
			return nil, errors.New("x: rate limiter: rate: Wait(n=1) would exceed context deadline")
		}, true},
		{"notice with routes", func() ([]models.GroundRoute, error) {
			return oneRoute("p"), fmt.Errorf("p: %w", &models.ProviderNotice{Reason: models.ReasonRateLimited, FixHint: "h", Err: models.ErrRateLimited})
		}, false},
		{"notice without routes", func() ([]models.GroundRoute, error) {
			return nil, fmt.Errorf("p: %w", &models.ProviderNotice{Reason: models.ReasonBlocked, FixHint: "h", Err: errors.New("blocked")})
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			swapGroundBreaker(t)
			runLaunch("p", c.fn)
			if got := groundBreaker.Tripped("p"); got != c.wantTripped {
				t.Errorf("breaker tripped = %v, want %v", got, c.wantTripped)
			}
		})
	}
}
