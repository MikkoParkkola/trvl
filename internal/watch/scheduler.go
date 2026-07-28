package watch

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Scheduler runs periodic price checks in the background.
// Call Start to launch the goroutine and Stop for graceful shutdown.
// Start and Stop are each idempotent and safe for concurrent use.
type Scheduler struct {
	dir      string        // root directory for the watch store (e.g. ~/.trvl)
	interval time.Duration // how often to run checks
	checker  PriceChecker  // injected for testability

	// roomChecker handles room-type watches. Nil by default (runOnce then
	// passes nil to checkWatchesWithRoomsAndWebhookContext, same as before
	// this field existed: a room watch reports "room checker not
	// configured" rather than being silently skipped). Settable via
	// SetRoomChecker, the same injection-seam pattern PriceChecker and
	// probeHook already use in this package: callers (cmd/trvl, mcp) each
	// own their real hotels-API-backed implementation, keeping this
	// package itself checker-agnostic.
	roomChecker RoomChecker

	mu        sync.Mutex
	doneOnce  sync.Once
	startOnce sync.Once
	stopOnce  sync.Once
	started   bool
	stopped   bool
	cancel    context.CancelFunc
	done      chan struct{}

	// lock is the cross-process scheduler singleton, held for the scheduler's
	// lifetime. Nil when this process is not the scheduler.
	lock *SchedulerLock

	// probeHook, when set, runs after each check round with the active watches.
	// It is the injection seam for the MIK-6234 Tier-1 scheduler-amortized
	// counterfactual probe: the daemon wires a budget-gated probe here (the
	// watch package never imports the hacks/probe engines, avoiding a cycle).
	// Nil by default, so standard scheduler behaviour is unchanged.
	probeHook func(ctx context.Context, active []Watch)
}

// SetProbeHook installs an optional per-round hook invoked with the active
// watches after each check. Safe to call before Start. Pass nil to disable.
func (s *Scheduler) SetProbeHook(hook func(ctx context.Context, active []Watch)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeHook = hook
}

// SetRoomChecker installs the checker used for room-type watches. Safe to
// call before Start. Pass nil to disable room-watch checking (the
// scheduler's default, unchanged from before this method existed).
//
// Found by adversarial review, 2026-07-28: mcp/server.go constructed its
// embedded scheduler with no room checker at all (this field/method did
// not exist), which was harmless before the cross-process scheduler
// singleton -- the standalone `trvl watch daemon` always had a real one,
// and before the lock both schedulers ran independently, so the daemon's
// covered room watches regardless of what MCP could do. The singleton
// lock means only ONE scheduler runs across the whole store now; in the
// normal MCP-first startup order, MCP wins the lock and the daemon exits
// immediately on its own lock attempt, so without this wiring room
// watches would never be periodically checked or alerted for as long as
// MCP holds the lock.
func (s *Scheduler) SetRoomChecker(checker RoomChecker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roomChecker = checker
}

// NoopChecker is a PriceChecker that always returns zero price.
// Used when no real price source is available.
type NoopChecker struct{}

func (NoopChecker) CheckPrice(_ context.Context, w Watch) (float64, string, string, error) {
	return 0, w.Currency, "", nil
}

// NewScheduler creates a Scheduler. If checker is nil, NoopChecker is used.
// interval defaults to 30 minutes if zero.
func NewScheduler(dir string, interval time.Duration, checker PriceChecker) *Scheduler {
	if checker == nil {
		checker = NoopChecker{}
	}
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &Scheduler{
		dir:      dir,
		interval: interval,
		checker:  checker,
		done:     make(chan struct{}),
	}
}

// Start launches the background goroutine. Idempotent — subsequent calls are no-ops.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stopped {
			return
		}
		// At most one scheduler per ~/.trvl across all processes. MCP clients
		// spawn a server per session and some leak them; without this, every
		// live `trvl mcp` runs a full round against the same watches, multiplying
		// provider load and racing on the same JSON files. A process that loses
		// the race still serves tool calls, it just does not schedule.
		lock, held, err := TryLockScheduler(s.dir)
		if err != nil {
			slog.Warn("scheduler: acquire singleton lock", "err", err)
			s.closeDone()
			return
		}
		if !held {
			slog.Debug("scheduler: another process owns the scheduler; not scheduling here")
			s.closeDone()
			return
		}
		s.lock = lock

		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.started = true
		go s.run(ctx)
	})
}

// Stop signals the background goroutine to exit and waits for it to finish.
// Any in-flight price check is cancelled immediately.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		cancel := s.cancel
		started := s.started
		s.stopped = true
		if !started {
			s.closeDone()
		}
		s.mu.Unlock()

		if cancel != nil {
			cancel()
		}
	})
	<-s.done

	s.mu.Lock()
	lock := s.lock
	s.lock = nil
	s.mu.Unlock()
	lock.Release()
}

// run is the background loop. ctx is cancelled when Stop is called.
func (s *Scheduler) run(ctx context.Context) {
	defer s.closeDone()

	// Run one check immediately on startup, then repeat on interval.
	s.runOnce(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce performs a single round of price checks.
// ctx should be cancelled (via Stop) to abort in-flight checks promptly.
func (s *Scheduler) runOnce(ctx context.Context) {
	store := NewStore(s.dir)
	if err := store.Load(); err != nil {
		slog.Warn("scheduler: load watches", "err", err)
		return
	}

	watches := store.List()
	if len(watches) == 0 {
		return
	}

	// Filter to active watches only: not expired and travel date not passed.
	active := activeWatches(watches)
	if len(active) == 0 {
		return
	}

	// Bound check duration to half the interval but also respect the stop signal.
	checkCtx, cancel := context.WithTimeout(ctx, s.interval/2)
	defer cancel()

	s.mu.Lock()
	roomChecker := s.roomChecker
	s.mu.Unlock()
	results := checkWatchesWithRoomsAndWebhookContext(checkCtx, ctx, store, s.checker, roomChecker, active)

	triggered := 0
	for _, r := range results {
		if r.Error != nil {
			slog.Warn("scheduler: check error",
				"watch_id", r.Watch.ID,
				"route", r.Watch.Origin+"→"+r.Watch.Destination,
				"err", r.Error,
			)
			continue
		}
		if r.NewPrice > 0 {
			slog.Info("scheduler: price checked",
				"watch_id", r.Watch.ID,
				"route", r.Watch.Origin+"→"+r.Watch.Destination,
				"price", r.NewPrice,
				"currency", r.Currency,
				"below_goal", r.BelowGoal,
			)
		}
		if r.PriceDropAlert {
			triggered++
			slog.Info("scheduler: proactive price drop",
				"watch_id", r.Watch.ID,
				"route", r.Watch.Origin+"→"+r.Watch.Destination,
				"price", r.NewPrice,
				"baseline", r.AlertBaseline,
				"drop_percent", r.AlertDropPercent,
				"currency", r.Currency,
			)
		}
		if r.BelowGoal {
			triggered++
			slog.Info("scheduler: price below target",
				"watch_id", r.Watch.ID,
				"route", r.Watch.Origin+"→"+r.Watch.Destination,
				"price", r.NewPrice,
				"target", r.Watch.BelowPrice,
				"currency", r.Currency,
			)
		}
	}

	slog.Info("scheduler: check complete",
		"checked", len(results),
		"triggered", triggered,
	)

	// MIK-6234 Tier-1: run the optional, budget-gated counterfactual probe over
	// the active routes. Best-effort and panic-isolated — it must never affect
	// the check round.
	s.mu.Lock()
	hook := s.probeHook
	s.mu.Unlock()
	if hook != nil {
		func() {
			defer func() { _ = recover() }()
			hook(ctx, active)
		}()
	}
}

func (s *Scheduler) closeDone() {
	s.doneOnce.Do(func() {
		close(s.done)
	})
}

// activeWatches filters watches to those that are still worth checking:
//   - no depart date set (route watch), OR
//   - depart date is today or in the future
func activeWatches(watches []Watch) []Watch {
	now := time.Now()
	today := now.Format("2006-01-02")

	var active []Watch
	for _, w := range watches {
		if isActive(w, today) {
			active = append(active, w)
		}
	}
	return active
}

// isActive returns true if the watch should still be checked.
func isActive(w Watch, today string) bool {
	// Route watches have no travel date to expire against, so they age out on
	// renewal instead: created or re-watched within routeWatchTTL. Without this
	// they were active forever and accumulated indefinitely.
	if w.IsRouteWatch() {
		renewed := w.RenewedAt
		if renewed.IsZero() {
			renewed = w.CreatedAt
		}
		if renewed.IsZero() {
			return true // no timestamps at all: do not silently drop it
		}
		return time.Since(renewed) < routeWatchTTL
	}

	// Date-range watches: active if the range end is today or later.
	if w.IsDateRange() {
		return w.DepartTo >= today
	}

	// Room and specific-date watches: active if depart date is today or later.
	if w.DepartDate != "" {
		return w.DepartDate >= today
	}

	return true
}

// ActiveWatches filters to watches that should still be checked: travel date not
// passed, and dateless route watches renewed within routeWatchTTL.
//
// Exported so the CLI and daemon apply the SAME rule as the in-process
// scheduler. They previously checked every stored watch, so a route watch the
// scheduler had aged out was still being re-priced by the daemon — the expiry
// held in one path and not the others.
func ActiveWatches(watches []Watch) []Watch {
	return activeWatches(watches)
}

// IsActiveNow reports whether a watch is still being checked, as of now.
//
// Exposed so user-facing surfaces can show expiry rather than let it be silent:
// an expired watch otherwise renders identically to a healthy one while no price
// is ever fetched for it again.
func IsActiveNow(w Watch) bool {
	return isActive(w, time.Now().Format("2006-01-02"))
}
