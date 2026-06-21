package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/travelgraph"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/watch"
	"github.com/spf13/cobra"
)

// nudgesCmd surfaces grounded proactive nudges from the personal travel graph
// (MIK-6233). It reads only local state (~/.trvl) and emits nudges ONLY when a
// real trigger fires (a watch crossing its target, or a confident historic
// low). It never speculates.
func nudgesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nudges",
		Short: "Show grounded proactive travel nudges from your local history",
		Long: `Join your watches, price history, preferences, and trips into a personal
travel graph and surface proactive nudges. Every nudge is grounded in a real
record (a watch that crossed your target price, or a route at a confident
historic low) and cites its source. No speculation: when nothing has triggered,
nothing is shown.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := watch.DefaultStore()
			if err != nil {
				return fmt.Errorf("open watch store: %w", err)
			}
			if err := store.Load(); err != nil {
				return fmt.Errorf("load watches: %w", err)
			}
			ws := store.List()

			// AllHistory returns every price point — both watch-keyed (from the
			// watch scheduler) and route-keyed (from ad-hoc searches, MIK-6229).
			// Build feeds both into the graph so historicLowNudge evaluates the
			// full corpus, not just the watch-scoped history.
			history := store.AllHistory()

			prefs, err := preferences.Load()
			if err != nil {
				prefs = preferences.Default()
			}

			var ts []trips.Trip
			if tstore, terr := trips.DefaultStore(); terr == nil && tstore.Load() == nil {
				ts = tstore.List()
			}

			g := travelgraph.Build(ws, history, prefs, ts)
			nudges := travelgraph.Nudges(g)

			if format == "json" {
				return models.FormatJSON(os.Stdout, nudges)
			}
			if len(nudges) == 0 {
				fmt.Println("No nudges — watches quiet and no historic lows detected.")
				return nil
			}
			for _, n := range nudges {
				fmt.Printf("[%s] %s\n  sources: %s\n", n.Kind, n.Message, strings.Join(n.Sources, ", "))
			}
			return nil
		},
	}
}
