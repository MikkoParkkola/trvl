package main

// MIK-3080: trvl multidest — multi-destination trip bundle ranker.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/multidest"
	"github.com/spf13/cobra"
)

func multidestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "multidest",
		Short: "Rank multi-destination city orderings by total trip cost",
		Long: `multidest reads candidate city orderings from stdin or --file (JSON) and
outputs the top-K bundles ranked by grand total (flights + hotels).

Input JSON schema (array of Ordering objects):
  [{"cities":["AMS","ROM"],"legs":[{"origin":"AMS","destination":"ROM","date":"2026-06-01","price":120}],"hotels":[{"city":"ROM","check_in":"2026-06-01","check_out":"2026-06-05","total_price":400}]}]

Examples:
  cat orderings.json | trvl multidest
  trvl multidest --file orderings.json --top-k 5 --format json`,
		Args: cobra.NoArgs,
		RunE: runMultidest,
	}
	cmd.Flags().String("file", "", "Path to JSON file with orderings (default: read stdin)")
	cmd.Flags().Int("top-k", 3, "Number of top bundles to show after drill-down")
	cmd.Flags().Int("screen-k", 3, "Number of orderings to keep after flight-only screen")
	return cmd
}

func runMultidest(cmd *cobra.Command, args []string) error {
	filePath, _ := cmd.Flags().GetString("file")
	topK, _ := cmd.Flags().GetInt("top-k")
	screenK, _ := cmd.Flags().GetInt("screen-k")
	format, _ := cmd.Flags().GetString("format")
	var r *os.File
	if filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("multidest: cannot open file: %w", err)
		}
		defer f.Close()
		r = f
	} else {
		r = os.Stdin
	}
	var orderings []multidest.Ordering
	if err := json.NewDecoder(r).Decode(&orderings); err != nil {
		return fmt.Errorf("multidest: invalid JSON input: %w", err)
	}
	opt := multidest.ScreenOptions{TopKAfterScreen: screenK, TopKFinal: topK}
	bundles := multidest.ScreenAndDrillDown(orderings, opt)
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(bundles)
	}
	renderMultidestTable(os.Stdout, bundles)
	return nil
}

func renderMultidestTable(out *os.File, bundles []multidest.Bundle) {
	if len(bundles) == 0 {
		fmt.Fprintln(out, "No bundles to rank — check input orderings.")
		return
	}
	fmt.Fprintf(out, "Top %d multi-destination bundles:\n", len(bundles))
	fmt.Fprintf(out, "  %-4s  %-28s %10s %10s %10s  %s\n", "Rank", "Cities", "Flights", "Hotels", "Total", "Notes")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 78))
	for _, b := range bundles {
		cities := strings.Join(b.Cities, " -> ")
		if len(cities) > 28 {
			cities = cities[:25] + "..."
		}
		fmt.Fprintf(out, "  #%-3d  %-28s %10.2f %10.2f %10.2f  %s\n", b.Rank, cities, b.FlightTotal, b.HotelTotal, b.GrandTotal, b.Reason)
	}
}
