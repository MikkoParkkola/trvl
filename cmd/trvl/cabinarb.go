package main

// MIK-3090: trvl cabin-arb — cabin-class arbitrage detector.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/cabinarb"
	"github.com/spf13/cobra"
)

func cabinArbCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cabin-arb <cabin:price[:carrier]>...",
		Short: "Detect near-flat cabin upgrades on the same itinerary",
		Long: `cabin-arb inspects the fare ladder for an itinerary and flags every
cabin-class upgrade whose upsell is within the threshold (default 15%).
When premium-economy is within 15% of economy, the upgrade is a near-free
step and the planner surfaces it as a recommendation.

Each positional argument is a colon-separated triple:
  cabin:price[:carrier]

Valid cabin values: economy, premium_economy, business, first.

Examples:
  trvl cabin-arb economy:500 premium_economy:560
  trvl cabin-arb economy:500 premium_economy:570 business:600 first:660
  trvl cabin-arb economy:500:AY economy:480:KL premium_economy:550 --threshold 20
  trvl cabin-arb economy:400 premium_economy:460 --format json`,
		Args: cobra.MinimumNArgs(1),
		RunE: runCabinArb,
	}
	cmd.Flags().Float64("threshold", cabinarb.DefaultUpsellThresholdPct, "Upsell threshold in percent")
	return cmd
}

func runCabinArb(cmd *cobra.Command, args []string) error {
	threshold, _ := cmd.Flags().GetFloat64("threshold")
	format, _ := cmd.Flags().GetString("format")
	fares, err := parseCabinFares(args)
	if err != nil {
		return fmt.Errorf("cabin-arb: %w", err)
	}
	recs := cabinarb.DetectWithThreshold(fares, threshold)
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(recs)
	}
	renderCabinArbTable(os.Stdout, recs, threshold)
	return nil
}

func parseCabinFares(args []string) ([]cabinarb.CabinFare, error) {
	out := make([]cabinarb.CabinFare, 0, len(args))
	for _, arg := range args {
		parts := strings.SplitN(arg, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid fare %q: expected cabin:price[:carrier]", arg)
		}
		cabin := strings.TrimSpace(parts[0])
		if cabin == "" {
			return nil, fmt.Errorf("invalid fare %q: cabin must not be empty", arg)
		}
		price, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid fare %q: price must be a number: %w", arg, err)
		}
		carrier := ""
		if len(parts) == 3 {
			carrier = strings.TrimSpace(parts[2])
		}
		out = append(out, cabinarb.CabinFare{Cabin: cabinarb.Cabin(cabin), Price: price, Carrier: carrier})
	}
	return out, nil
}

func renderCabinArbTable(out *os.File, recs []cabinarb.UpgradeRecommendation, threshold float64) {
	if len(recs) == 0 {
		fmt.Fprintf(out, "No upgrades within %.0f%% threshold.\n", threshold)
		return
	}
	fmt.Fprintf(out, "Upgrade recommendations (threshold %.0f%%):\n", threshold)
	fmt.Fprintf(out, "  %-16s %-16s %8s %8s %8s  %s\n", "Baseline", "Target", "Base", "Target", "Upsell%", "Reason")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 80))
	for _, r := range recs {
		upsellStr := fmt.Sprintf("%.1f%%", r.UpsellPercent)
		if r.UpsellPercent == 0 {
			upsellStr = "0.0% (free!)"
		}
		fmt.Fprintf(out, "  %-16s %-16s %8.2f %8.2f %11s  %s\n", r.Baseline, r.Target, r.BaselinePrice, r.TargetPrice, upsellStr, r.Reason)
	}
}
