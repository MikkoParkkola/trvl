package watch

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Retention limits, and the environment variables that override them
// (trvl#514).
//
// These three numbers shipped in #508 with no usage data behind any of them.
// They are defensible -- 1000 mirrors the existing per-route cap, Sparkline
// reads 10-20 points, and a watch's all-time low lives on the Watch record so it
// survives eviction -- but nothing measured them, and until now an operator
// could neither see them nor change them without rebuilding.
//
// This makes them visible and adjustable. It deliberately does NOT change any
// default: "there is no evidence for better numbers, so changing them again
// would be numerology."
const (
	// EnvMaxPointsPerWatch overrides how many price points each watch retains.
	//
	// Cost of raising it: memory and store size, multiplied by the number of
	// watches. One real store reached 320,028 points in 41MB, of which 319,966
	// were watch-keyed, and loading that made each `trvl mcp` process cost about
	// 686MB. At the 30-minute scheduler cadence the default retains roughly
	// three weeks of full-resolution history per watch.
	//
	// Cost of lowering it: older points are evicted sooner. Nothing that reads
	// history needs the raw tail -- Sparkline asks for 10-20 points, and the
	// all-time low is on the Watch record -- so the practical loss is resolution
	// in long-range trend views.
	EnvMaxPointsPerWatch = "TRVL_WATCH_MAX_POINTS_PER_WATCH"

	// EnvMaxPointsTotal overrides the global backstop across all watches,
	// evicting oldest first.
	//
	// Cost of raising it: this is the number that actually bounds the file. The
	// per-watch cap alone cannot, because many watches multiply it. Raising this
	// is what turns a bounded store into an unbounded one.
	//
	// Cost of lowering it: watches compete for one budget, so a user with many
	// watches loses history on all of them rather than on the busiest.
	EnvMaxPointsTotal = "TRVL_WATCH_MAX_POINTS_TOTAL"

	// EnvRouteTTLDays overrides how long a dateless route watch keeps being
	// checked without the user re-expressing interest, in whole days.
	//
	// Cost of raising it: route watches have no travel date to expire against,
	// so a long TTL means live provider calls every 30 minutes for routes nobody
	// is watching any more. One real store carried 468 permanently-active route
	// watches before this existed.
	//
	// Cost of lowering it: a seasonal watcher's route expires between uses.
	// RenewedAt only advances on user intent, so an annual traveller's watch
	// expires every year -- arguably correct, arguably surprising. This is the
	// value most likely to be wrong, and the reason it is now adjustable.
	EnvRouteTTLDays = "TRVL_WATCH_ROUTE_TTL_DAYS"
)

// Ceilings exist so a typo cannot be accepted as a policy. They are not
// judgements about the right value -- they bound the absurd, an order of
// magnitude beyond any plausible setting, so that a stray zero is refused
// rather than silently turning the store unbounded.
const (
	maxAllowedPointsPerWatch = 1_000_000
	maxAllowedPointsTotal    = 10_000_000
	maxAllowedRouteTTLDays   = 3650 // ten years
)

// retentionConfig carries the three limits for one store.
type retentionConfig struct {
	MaxPointsPerWatch int
	MaxPointsTotal    int
	RouteTTL          time.Duration
}

func defaultRetention() retentionConfig {
	return retentionConfig{
		MaxPointsPerWatch: maxObservationsPerWatch,
		MaxPointsTotal:    maxWatchObservations,
		RouteTTL:          routeWatchTTL,
	}
}

// loadRetention reads the overrides, or returns an error naming the variable
// and what it would accept.
//
// TRVL.RETENTION.4: an out-of-range or unparseable value is REFUSED, never
// clamped. Clamping would mean an operator who set 0 to disable a cap gets 1000
// instead and is never told -- the store would then behave one way while its
// configuration said another, which is worse than refusing to start. An empty
// or unset variable is not an error; it means "use the default".
//
// getenv is a parameter so this is testable without touching process
// environment, which matters because the tests must be able to run in parallel.
func loadRetention(getenv func(string) string) (retentionConfig, error) {
	cfg := defaultRetention()

	if v, err := positiveInt(getenv, EnvMaxPointsPerWatch, maxAllowedPointsPerWatch); err != nil {
		return retentionConfig{}, err
	} else if v > 0 {
		cfg.MaxPointsPerWatch = v
	}

	if v, err := positiveInt(getenv, EnvMaxPointsTotal, maxAllowedPointsTotal); err != nil {
		return retentionConfig{}, err
	} else if v > 0 {
		cfg.MaxPointsTotal = v
	}

	if v, err := positiveInt(getenv, EnvRouteTTLDays, maxAllowedRouteTTLDays); err != nil {
		return retentionConfig{}, err
	} else if v > 0 {
		cfg.RouteTTL = time.Duration(v) * 24 * time.Hour
	}

	// A per-watch cap above the global backstop is not wrong so much as
	// meaningless: the backstop evicts first, so the larger number never takes
	// effect. Saying so beats letting an operator believe a setting is active.
	if cfg.MaxPointsPerWatch > cfg.MaxPointsTotal {
		return retentionConfig{}, fmt.Errorf(
			"%s (%d) exceeds %s (%d): the global backstop evicts first, so the per-watch cap would never take effect",
			EnvMaxPointsPerWatch, cfg.MaxPointsPerWatch, EnvMaxPointsTotal, cfg.MaxPointsTotal)
	}

	return cfg, nil
}

// positiveInt returns 0 when the variable is unset or empty, meaning "default".
func positiveInt(getenv func(string) string, name string, max int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a whole number (accepted: 1..%d)", name, raw, max)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s=%d must be greater than zero; there is no way to disable a retention cap, because an unbounded store is the defect these caps exist to prevent", name, n)
	}
	if n > max {
		return 0, fmt.Errorf("%s=%d exceeds the accepted maximum of %d", name, n, max)
	}
	return n, nil
}

// retentionFromEnv is the process-wide accessor used by the store.
func retentionFromEnv() (retentionConfig, error) {
	return loadRetention(os.Getenv)
}

// retentionOrDefault returns this store's limits, falling back to the compiled
// defaults when Load has not run.
//
// The fallback is not a way to skip validation. Every path that mutates the
// store goes through withTxn, which reloads committed state and therefore
// validates; and every consumer calls Load before use. This exists so a Store
// constructed directly in a test still evicts rather than retaining without
// limit, which is the behaviour those tests were written against.
func (s *Store) retentionOrDefault() retentionConfig {
	if s.retention.MaxPointsPerWatch == 0 || s.retention.MaxPointsTotal == 0 {
		return defaultRetention()
	}
	return s.retention
}

// effectiveRetention is the accessor for code with no *Store to hand -- the
// activity check runs as a free function on a Watch value.
//
// It falls back to defaults when the environment is invalid rather than
// returning an error, because these call sites have no way to report one. That
// is not a hole: Store.Load validates the same variables and REFUSES to load on
// a bad value, so a process that reaches these functions with an invalid
// override is one that already failed to start. The fallback keeps a
// misconfigured process from behaving unboundedly in the window before it
// exits, rather than being the place the value is decided.
func effectiveRetention() retentionConfig {
	cfg, err := retentionFromEnv()
	if err != nil {
		return defaultRetention()
	}
	return cfg
}
