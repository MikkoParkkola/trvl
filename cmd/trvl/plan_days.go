package main

import (
	"fmt"
	"io"

	"github.com/MikkoParkkola/trvl/internal/daygraph"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/spf13/cobra"
)

// planDaysResult is the structured outcome of a plan-days run, returned by the
// pure composeDays helper so tests can assert without an on-disk trip store.
type planDaysResult struct {
	TripID string          `json:"trip_id"`
	Days   []trips.DayPlan `json:"days"`
}

// planDaysCmd implements `trvl plan-days` — it composes a per-day itinerary for
// a saved trip from its workspace places and the trip's date span.
//
// It loads the trip by ID, calls internal/daygraph.Compose on the trip's
// places, and prints one day per trip day with the assigned places and a
// deterministic route-time estimate.
//
// Examples:
//
//	trvl plan-days trip_abc123
//	trvl plan-days trip_abc123 --format json
func planDaysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan-days TRIP_ID",
		Short: "Compose a per-day itinerary for a saved trip",
		Long: `Build a per-day plan for a saved trip from its workspace places and date span.

Places are distributed across the trip's days and each day gets a deterministic
walking/transit route-time estimate. Places without coordinates are still
assigned to a day but flagged in that day's warnings.

The trip must have a leg with a parseable date (so the day span is known).
Populate places via the trip workspace before composing.

Examples:
  trvl plan-days trip_abc123
  trvl plan-days trip_abc123 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := loadTripStore()
			if err != nil {
				return err
			}
			trip, err := store.Get(args[0])
			if err != nil {
				return err
			}

			result, err := composeDays(*trip)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(cmd.OutOrStdout(), result)
			}
			printPlanDays(cmd.OutOrStdout(), result)
			return nil
		},
	}
	return cmd
}

// composeDays is the pure core of plan-days: it runs daygraph.Compose over the
// trip's workspace places and returns the day plans. It is independent of the
// on-disk store so tests can drive it directly.
func composeDays(t trips.Trip) (planDaysResult, error) {
	var places []trips.Place
	if t.Workspace != nil {
		places = t.Workspace.Places
	}
	days, err := daygraph.Compose(t, places)
	if err != nil {
		return planDaysResult{}, err
	}
	return planDaysResult{TripID: t.ID, Days: days}, nil
}

// printPlanDays renders a human-readable day-by-day itinerary.
func printPlanDays(w io.Writer, res planDaysResult) {
	if len(res.Days) == 0 {
		_, _ = fmt.Fprintln(w, "No days to plan (trip has no date span).")
		return
	}
	_, _ = fmt.Fprintf(w, "Itinerary — %d day(s):\n", len(res.Days))
	for _, d := range res.Days {
		_, _ = fmt.Fprintf(w, "  %s — %d place(s), ~%d min route\n",
			d.Date, len(d.PlaceIDs), d.EstimatedRouteMinutes)
		if d.Title != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", d.Title)
		}
		for _, warn := range d.Warnings {
			_, _ = fmt.Fprintf(w, "    ! %s\n", warn)
		}
	}
}
