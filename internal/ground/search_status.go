package ground

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Provider fan-out outcomes: launching a provider behind its circuit breaker,
// and turning each result into routes, failure messages and one status row.

// providerResult holds the outcome of a single provider search goroutine.
type providerResult struct {
	routes []models.GroundRoute
	err    error
	name   string
}

// groundBreaker protects the provider fan-out: a provider that fails (network
// error, timeout, rate-limit) DefaultThreshold times in a row is skipped for
// the cooldown instead of being retried on every search, then probed again once
// the cooldown elapses. It is keyed by provider name and shared across searches
// by design — a provider's recent health carries between requests. A
// "not applicable for this route" answer is healthy, not a failure, so it never
// trips the breaker.
var groundBreaker = breaker.New()

// errCircuitBroken marks a provider call skipped because its breaker is open.
// It is a deliberate skip, not a fresh failure, so the result aggregator
// reports it as a circuit_broken status without counting it as an error.
var errCircuitBroken = errors.New("skipped: recent failures tripped the circuit breaker")

// launchProvider starts a provider search in a new goroutine, sending the
// result to the results channel when done. The shared groundBreaker gates the
// call: a tripped provider is skipped without touching the network, and each
// real outcome feeds the breaker (a genuine failure advances it toward
// tripping; a healthy or not-applicable response closes it).
func launchProvider(wg *sync.WaitGroup, results chan<- providerResult, name string, fn func() ([]models.GroundRoute, error)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		if groundBreaker.Tripped(name) {
			results <- providerResult{name: name, err: errCircuitBroken}
			return
		}
		routes, err := fn()
		// A not-applicable answer is a healthy "nothing to sell", and a notice
		// that still carries routes is an answer; neither pushes the breaker
		// toward tripping. Timeouts and every other error do.
		var notice *models.ProviderNotice
		healthy := err == nil || isProviderNotApplicable(err) ||
			(errors.As(err, &notice) && len(routes) > 0)
		if healthy {
			groundBreaker.RecordSuccess(name)
		} else {
			groundBreaker.RecordFailure(name)
		}
		results <- providerResult{routes: routes, err: err, name: name}
	}()
}

// collectGroundResults drains the provider fan-out into the merged routes, the
// error messages of providers that failed, and one status row per provider.
func collectGroundResults(results <-chan providerResult) ([]models.GroundRoute, []string, []models.ProviderStatus) {
	var routes []models.GroundRoute
	var failures []string
	var statuses []models.ProviderStatus
	fail := func(r providerResult, st models.ProviderStatus) {
		slog.Warn("ground provider error", "provider", r.name, "error", logredact.Err(r.err))
		failures = append(failures, fmt.Sprintf("%s: %v", r.name, r.err))
		statuses = append(statuses, st)
	}
	for r := range results {
		row := models.ProviderStatus{ID: r.name, Name: r.name}
		var notice *models.ProviderNotice
		switch {
		case r.err == nil && len(r.routes) == 0:
			// A valid answer with nothing on this date: checked, no hit.
			row.Status = models.StatusCheckedNoHit
			statuses = append(statuses, row)
		case r.err == nil:
			row.Status, row.Results = models.StatusOK, len(r.routes)
			routes = append(routes, r.routes...)
			statuses = append(statuses, row)
		case errors.Is(r.err, errCircuitBroken):
			// Deliberately skipped while the breaker is open: reported, but not
			// a fresh failure.
			row.Status = models.StatusCircuitBroken
			row.Error = errCircuitBroken.Error()
			row.FixHint = "wait for the cooldown to elapse, then it retries automatically"
			statuses = append(statuses, row)
		case errors.As(r.err, &notice) && len(r.routes) > 0:
			// Routes with a caveat: an answer, with the reason and the fix.
			row.Status, row.Results = models.StatusOK, len(r.routes)
			row.Reason, row.FixHint, row.Error = notice.Reason, notice.FixHint, r.err.Error()
			routes = append(routes, r.routes...)
			statuses = append(statuses, row)
		case errors.As(r.err, &notice):
			row.Status = models.ClassifyProviderError(r.err)
			row.Reason, row.FixHint, row.Error = notice.Reason, notice.FixHint, r.err.Error()
			fail(r, row)
		case isProviderNotApplicable(r.err):
			slog.Debug("ground provider not applicable", "provider", r.name, "reason", logredact.Err(r.err))
			row.Status, row.Reason = models.StatusSkipped, models.ReasonRouteNotServed
			statuses = append(statuses, row)
		case isProviderTimeout(r.err):
			row.Status, row.Reason, row.Error = models.StatusTimeout, models.ReasonTimeout, r.err.Error()
			fail(r, row)
		default:
			row.Status, row.Error = models.ClassifyProviderError(r.err), r.err.Error()
			fail(r, row)
		}
	}
	// Deterministic order: the channel drains concurrently.
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	return routes, failures, statuses
}

// allNotServed reports whether every status row says the provider does not
// serve the pair.
func allNotServed(statuses []models.ProviderStatus) bool {
	for _, st := range statuses {
		if st.Status != models.StatusSkipped || st.Reason != models.ReasonRouteNotServed {
			return false
		}
	}
	return true
}
