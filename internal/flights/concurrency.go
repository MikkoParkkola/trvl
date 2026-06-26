package flights

import (
	"context"
	"time"

	"github.com/MikkoParkkola/trvl/internal/breaker"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/sync/errgroup"
)

// flightBreaker protects the secondary-provider fan-out: a provider that fails
// (errors, timeouts, rate-limits) DefaultThreshold times in a row is skipped
// for the cooldown instead of being retried on every search, then probed again
// once the cooldown elapses. It is keyed by providerTask.name. Shared across
// searches by design — a provider's recent health carries between requests.
var flightBreaker = breaker.New()

// outcomeFailed reports whether a provider outcome counts as a breaker failure.
// An explicit error (including the harness-recorded timeout) or a failure-class
// status trips it; an empty-but-healthy or not-configured response does not, so
// a provider that simply has no flights is never penalised.
func outcomeFailed(out providerOutcome) bool {
	if out.err != nil {
		return true
	}
	switch out.status.Status {
	case models.StatusFailed, models.StatusError, models.StatusTimeout, models.StatusRateLimited:
		return true
	}
	return false
}

// perProviderTimeout bounds how long any single secondary provider may run
// inside the concurrent fan-out. It is shorter than sharedFlightSearchTimeout
// (the whole-search budget) so one slow provider cannot starve the others; a
// provider that exceeds it is reported as a timeout, not silently dropped.
const perProviderTimeout = 20 * time.Second

// providerConcurrencyLimit caps how many secondary providers run at once. It
// mirrors the bounded-concurrency cap used elsewhere (multi.go) so the search
// never opens an unbounded number of simultaneous upstream connections.
const providerConcurrencyLimit = 6

// providerOutcome is the complete result of querying one provider: its flights,
// the status to surface, whether it produced a definitive success, and any
// error (kept for the aggregate error path). It is self-contained so providers
// can run concurrently without sharing mutable state.
type providerOutcome struct {
	flights   []models.FlightResult
	status    models.ProviderStatus
	succeeded bool
	err       error
}

// providerTask names a unit of provider work. run is invoked with a
// per-provider timeout context and must be self-contained (no shared writes);
// it returns the outcome rather than mutating caller state.
type providerTask struct {
	name string
	run  func(ctx context.Context) providerOutcome
}

// runProviderTasks executes tasks concurrently with bounded parallelism and
// per-task timeout isolation, returning outcomes in the SAME order as the input
// tasks (deterministic), regardless of completion order.
//
// Failure isolation: a task that errors or times out never cancels its peers —
// each task gets its own timeout context derived from ctx, and the errgroup is
// only used for bounded concurrency (the group func always returns nil, so one
// provider's failure does not tear down the others). A task that panics or
// times out yields a typed timeout/failed outcome via its own logic; the harness
// itself records a deadline outcome when the per-task context expires before the
// task returns.
func runProviderTasks(ctx context.Context, tasks []providerTask, limit int, perTaskTimeout time.Duration) []providerOutcome {
	outcomes := make([]providerOutcome, len(tasks))
	if len(tasks) == 0 {
		return outcomes
	}
	if limit <= 0 {
		limit = len(tasks)
	}

	g := new(errgroup.Group)
	g.SetLimit(limit)

	for i, task := range tasks {
		i, task := i, task
		if flightBreaker.Tripped(task.name) {
			// Recent failures tripped the breaker; skip the call and report it
			// honestly rather than hammering a provider that keeps failing.
			outcomes[i] = providerOutcome{
				status: models.ProviderStatus{
					ID:      task.name,
					Name:    task.name,
					Status:  models.StatusCircuitBroken,
					Error:   "skipped: recent failures tripped the circuit breaker",
					FixHint: "wait for the cooldown to elapse, then it retries automatically",
				},
			}
			continue
		}
		g.Go(func() error {
			tctx, cancel := context.WithTimeout(ctx, perTaskTimeout)
			defer cancel()

			done := make(chan providerOutcome, 1)
			go func() { done <- task.run(tctx) }()

			select {
			case out := <-done:
				outcomes[i] = out
			case <-tctx.Done():
				// The provider exceeded its slice of the budget. Record a typed
				// timeout outcome so the result reports "outcome unknown" rather
				// than a fabricated empty. The orphaned goroutine drains into the
				// buffered channel and exits without blocking.
				outcomes[i] = providerOutcome{
					status: models.ProviderStatus{
						ID:     task.name,
						Name:   task.name,
						Status: models.StatusTimeout,
						Error:  tctx.Err().Error(),
					},
					err: tctx.Err(),
				}
			}
			// Feed the outcome back to the breaker: a failure (error, timeout,
			// rate-limit) advances it toward tripping; any healthy response
			// closes it so the provider is retried normally.
			if outcomeFailed(outcomes[i]) {
				flightBreaker.RecordFailure(task.name)
			} else {
				flightBreaker.RecordSuccess(task.name)
			}
			return nil
		})
	}
	_ = g.Wait()
	return outcomes
}
