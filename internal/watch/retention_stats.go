package watch

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// RetentionStats describes what the store currently holds against the limits
// that bound it (trvl#514, TRVL.RETENTION.3).
//
// This exists because the three retention numbers shipped with no usage data
// behind them, and nothing could report the data that would justify changing
// them. "Revisit only once that data exists" needs somewhere for the data to
// come from.
type RetentionStats struct {
	Watches      int
	TotalPoints  int
	WatchPoints  int
	RoutePoints  int
	StoreBytes   int64
	HistoryBytes int64

	// Per-watch point counts, describing the distribution rather than only its
	// total: a store at 60% of the global cap looks healthy until one watch
	// turns out to hold most of it.
	MinPerWatch    int
	MedianPerWatch int
	P90PerWatch    int
	MaxPerWatch    int

	// AtPerWatchCap is how many watches sit at the per-watch cap, i.e. are
	// actively losing history to eviction. The number that says whether the cap
	// binds in practice or is merely a backstop.
	AtPerWatchCap int

	Limits retentionConfig
}

// RetentionStats reports the store's current occupancy against its limits.
func (s *Store) RetentionStats() RetentionStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refreshHistoryLocked()

	limits := s.retentionOrDefault()
	st := RetentionStats{
		Watches:     len(s.watches),
		TotalPoints: len(s.history),
		Limits:      limits,
	}

	perWatch := make(map[string]int, len(s.watches))
	for _, p := range s.history {
		if p.WatchID != "" {
			st.WatchPoints++
			perWatch[p.WatchID]++
			continue
		}
		st.RoutePoints++
	}

	counts := make([]int, 0, len(perWatch))
	for _, n := range perWatch {
		counts = append(counts, n)
		if n >= limits.MaxPointsPerWatch {
			st.AtPerWatchCap++
		}
	}
	sort.Ints(counts)
	if len(counts) > 0 {
		st.MinPerWatch = counts[0]
		st.MaxPerWatch = counts[len(counts)-1]
		st.MedianPerWatch = counts[len(counts)/2]
		st.P90PerWatch = counts[(len(counts)*9)/10]
		if idx := (len(counts) * 9) / 10; idx >= len(counts) {
			st.P90PerWatch = counts[len(counts)-1]
		}
	}

	if fi, err := os.Stat(s.databasePath()); err == nil {
		st.HistoryBytes = fi.Size()
		st.StoreBytes = fi.Size()
	} else {
		if fi, err := os.Stat(s.watchesPath()); err == nil {
			st.StoreBytes += fi.Size()
		}
		if fi, err := os.Stat(s.historyPath()); err == nil {
			st.HistoryBytes = fi.Size()
			st.StoreBytes += fi.Size()
		}
	}
	return st
}

// Summary renders the stats for a human, alongside the limits and how each was
// set, so an operator can see whether a cap is binding before deciding whether
// it is wrong.
func (st RetentionStats) Summary() string {
	var b strings.Builder
	b.WriteString("Retention\n")
	fmt.Fprintf(&b, "  store on disk        %s (history %s)\n", humanBytes(st.StoreBytes), humanBytes(st.HistoryBytes))
	fmt.Fprintf(&b, "  watches              %d\n", st.Watches)
	fmt.Fprintf(&b, "  price points         %d total (%d watch-keyed, %d route-keyed)\n",
		st.TotalPoints, st.WatchPoints, st.RoutePoints)

	if st.WatchPoints > 0 {
		fmt.Fprintf(&b, "  points per watch     min %d, median %d, p90 %d, max %d\n",
			st.MinPerWatch, st.MedianPerWatch, st.P90PerWatch, st.MaxPerWatch)
	}

	fmt.Fprintf(&b, "  per-watch cap        %d (%s)\n", st.Limits.MaxPointsPerWatch, sourceOf(EnvMaxPointsPerWatch))
	fmt.Fprintf(&b, "  global cap           %d (%s)\n", st.Limits.MaxPointsTotal, sourceOf(EnvMaxPointsTotal))
	fmt.Fprintf(&b, "  route watch TTL      %d days (%s)\n", int(st.Limits.RouteTTL.Hours()/24), sourceOf(EnvRouteTTLDays))

	// The two lines that answer "is a cap actually binding?", which is the
	// question this report exists to make answerable.
	if st.AtPerWatchCap > 0 {
		fmt.Fprintf(&b, "  NOTE %d watch(es) are at the per-watch cap and are losing their oldest points.\n", st.AtPerWatchCap)
	}
	if st.Limits.MaxPointsTotal > 0 {
		pct := (st.WatchPoints * 100) / st.Limits.MaxPointsTotal
		fmt.Fprintf(&b, "  global cap used      %d%%\n", pct)
		if pct >= 100 {
			b.WriteString("  NOTE the global cap is binding: the oldest watch points are being evicted.\n")
		}
	}
	return b.String()
}

func sourceOf(env string) string {
	if os.Getenv(env) != "" {
		return "set via " + env
	}
	return "default; override with " + env
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
