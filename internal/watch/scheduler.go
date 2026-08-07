package watch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
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

	// lockRetryInterval overrides schedulerLockRetryInterval for this
	// instance. Zero (the default) means "use the package constant" --
	// tests set this via SetLockRetryIntervalForTest to a small value so a
	// lock-failover test doesn't have to wait out the real 30s production
	// interval. Same pattern as SetWebhookHTTPClientForTest (check.go).
	lockRetryInterval time.Duration
}

// SetLockRetryIntervalForTest overrides how often this Scheduler retries
// the singleton lock after losing it. Test-only: production callers should
// never need a value other than schedulerLockRetryInterval. Must be called
// before Start.
func (s *Scheduler) SetLockRetryIntervalForTest(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lockRetryInterval = d
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

// schedulerLockRetryInterval is how often a process that lost the singleton
// lock race re-attempts it. See acquireAndRun's doc comment for why a retry
// loop exists at all. Frequent enough to notice a dead holder promptly (the
// OS releases the lock immediately on process death; nothing here needs to
// wait out any timeout), cheap enough that polling it costs nothing next to
// the 30-minute-default scheduling interval it guards.
const schedulerLockRetryInterval = 30 * time.Second

// Start launches the background goroutine. Idempotent — subsequent calls are no-ops.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.started = true
		s.mu.Unlock()
		go s.reportOrphanedTemps()
		go s.acquireAndRun(ctx)
	})
}

// acquireAndRun retries the cross-process singleton lock until acquired or
// ctx is cancelled (via Stop), then runs scheduling rounds for as long as
// this process holds it.
//
// Found by adversarial review, 2026-07-28: the original one-shot lock
// attempt gave up permanently if another process held the lock at Start()
// time -- there was no retry of any kind. TryLockScheduler's own doc
// comment promises "the OS releases the lock automatically if the holder
// dies, so a crashed or SIGKILLed process cannot wedge scheduling for
// everyone else," but nothing here ever re-attempted the lock, so that
// promise went unfulfilled for every OTHER process that had already lost
// the race: if the winner later died while a loser stayed alive -- the
// exact multi-process-coexistence scenario this whole singleton exists to
// handle; 15 orphaned `trvl mcp` processes were observed alive
// simultaneously in the incident that motivated it -- nothing would ever
// notice and reacquire. All scheduling would silently stop until some
// THIRD, brand-new process happened to start and win the now-free lock.
func (s *Scheduler) acquireAndRun(ctx context.Context) {
	s.mu.Lock()
	retryInterval := s.lockRetryInterval
	s.mu.Unlock()
	if retryInterval <= 0 {
		retryInterval = schedulerLockRetryInterval
	}
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		lock, held, err := TryLockScheduler(s.dir)
		if err != nil {
			slog.Warn("scheduler: acquire singleton lock", "err", logredact.Err(err))
		} else if held {
			s.mu.Lock()
			s.lock = lock
			s.mu.Unlock()
			s.run(ctx) // run() closes s.done itself on return.
			return
		} else {
			slog.Debug("scheduler: another process owns the scheduler; will retry",
				"retry_after", retryInterval)
		}

		select {
		case <-ctx.Done():
			// Exiting without ever having called run(): that is the only
			// other path that closes s.done, so this one must too.
			s.closeDone()
			return
		case <-ticker.C:
		}
	}
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
		slog.Warn("scheduler: load watches", "err", logredact.Err(err))
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
				"err", logredact.Err(r.Error),
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
		// Round 22: the scheduler is the automatic, unattended check path (CLI
		// daemon and the MCP-owned check_watches background loop both run
		// through it) -- unlike an interactive CLI check, there is no
		// Notifier.Notify call here to surface AlertsClearedByCurrencyChange,
		// so a currency-change reset could silently erase a watch's alert
		// threshold with zero durable record. Log it at Warn so it survives
		// in scheduler output/log aggregation even with nobody watching in
		// real time. Found by GPT second-opinion review, 2026-07-30 (round 22).
		if r.AlertsClearedByCurrencyChange {
			slog.Warn("scheduler: alert threshold cleared by currency change",
				"watch_id", r.Watch.ID,
				"route", r.Watch.Origin+"→"+r.Watch.Destination,
				"new_currency", r.Currency,
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
		if isActive(w, today, effectiveRetention().RouteTTL) {
			active = append(active, w)
		}
	}
	return active
}

// isActive returns true if the watch should still be checked.
// routeTTL is passed rather than read from a package constant so an operator
// override reaches this decision too (trvl#514). A configurable retention
// setting that the eviction path honoured and the activity check ignored would
// be a setting in name only.
func isActive(w Watch, today string, routeTTL time.Duration) bool {
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
		return time.Since(renewed) < routeTTL
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
	return isActive(w, time.Now().Format("2006-01-02"), effectiveRetention().RouteTTL)
}

// orphanWarnBytes is the total orphaned-temp size that earns a warning. One
// interrupted write leaves a full copy of the target file, and a real store's
// price history has reached 39MB, so a single orphan can sit just under this.
// The threshold names "this is more than one unlucky kill" rather than "any
// orphan at all", which would warn on the normal case of a single Ctrl-C.
const orphanWarnBytes = 64 << 20

// orphanMinAge keeps a temp belonging to a write that may still be in flight
// out of the report entirely.
const orphanMinAge = 1 * time.Hour

// reportOrphanedTemps warns once at scheduler start when interrupted writes
// have left a meaningful amount of disk behind.
//
// atomicjson writes via temp-file-plus-rename and removes the temp on its error
// paths, but nothing survives a SIGKILL, so a process killed mid-write leaves a
// full copy of the target behind. One machine accumulated 7 files and 149MB in
// ~/.trvl before anyone noticed (trvl#513). `trvl tempfiles --delete` reclaims
// them, but a user who does not know they exist never runs it -- which is the
// actual complaint in that issue, and it is a REPORTING gap, not a cleanup one.
//
// Reports; never deletes. Clean's own doc explains why automatic deletion is
// the wrong call: a PID is only meaningful on the machine and boot that wrote
// it, so a temp written by another host onto a shared store directory, or by a
// process from a previous boot, can read as dead locally. A cleanup that races
// a live writer costs more than the disk it frees. Surfacing the number lets
// the user make that call with the command that already exists.
//
// The scheduler is the right place: it is the long-running process most likely
// to be killed mid-write, so it is the one that creates these files.
func (s *Scheduler) reportOrphanedTemps() {
	orphans, err := atomicjson.FindOrphans(s.dir)
	if err != nil {
		// A store directory that cannot be read is not this goroutine's problem
		// to report -- the next real store operation will fail loudly.
		return
	}

	// Reports on AGE, not on reclaimability.
	//
	// atomicjson.Clean filters by Orphan.Reclaimable, which requires the owning
	// process to be provably gone. On Windows processAlive always returns true
	// -- deliberately, because os.FindProcess there fails for reasons other
	// than "no such process" and an access-denied result on another user's
	// process would read as gone and delete a live writer's file. So nothing is
	// ever reclaimable on Windows, and a report built on Clean was a silent
	// no-op there: the platform still accumulates the files, it just never
	// heard about them. Caught by TRVL.ORPHAN.1 on windows-latest.
	//
	// Reporting and reclaiming are different questions. This answers the first.
	now := time.Now()
	var bytes int64
	var count int
	for _, o := range orphans {
		if o.Age(now) < orphanMinAge {
			continue
		}
		bytes += o.Size
		count++
	}
	if bytes < orphanWarnBytes {
		return
	}
	slog.Warn("watch: interrupted writes have left temp files behind",
		"dir", s.dir,
		"files", count,
		"bytes", bytes,
		"inspect_with", "trvl tempfiles",
		"reclaim_with", "trvl tempfiles --delete")
}
