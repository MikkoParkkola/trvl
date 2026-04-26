package main

// MIK-3087: trvl los — length-of-stay rate-flip scanner.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/los"
	"github.com/spf13/cobra"
)

func losCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "los <nights:total-price>...",
		Short: "Detect length-of-stay rate flips — stay longer, pay less",
		Long: `los scans hotel stay quotes and flags cases where extending or shortening
the stay results in absolute savings or a lower per-night rate (>= 5% lower).

Each positional argument is a nights:total-price pair. Append :r for refundable.
The --baseline flag picks which nights value is the user's requested stay.

Examples:
  trvl los --baseline 7 5:420 6:480 7:560 8:530 9:495
  trvl los --baseline 5 4:320 5:400 6:450 7:420:r --format json`,
		Args: cobra.MinimumNArgs(2),
		RunE: runLos,
	}
	cmd.Flags().Int("baseline", 0, "Baseline nights (the stay length the user requested) — required")
	_ = cmd.MarkFlagRequired("baseline")
	return cmd
}

func runLos(cmd *cobra.Command, args []string) error {
	baseline, _ := cmd.Flags().GetInt("baseline")
	format, _ := cmd.Flags().GetString("format")
	quotes, err := parseLOSQuotes(args)
	if err != nil {
		return fmt.Errorf("los: %w", err)
	}
	flips := los.ScanLengthOfStay(baseline, quotes)
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(flips)
	}
	renderLosTable(os.Stdout, baseline, flips)
	return nil
}

func parseLOSQuotes(args []string) ([]los.LOSQuote, error) {
	out := make([]los.LOSQuote, 0, len(args))
	for _, arg := range args {
		parts := strings.Split(arg, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid quote %q: expected nights:total-price[:r]", arg)
		}
		nights, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || nights <= 0 {
			return nil, fmt.Errorf("invalid quote %q: nights must be a positive integer", arg)
		}
		total, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid quote %q: price must be a number: %w", arg, err)
		}
		refundable := len(parts) >= 3 && strings.EqualFold(strings.TrimSpace(parts[2]), "r")
		out = append(out, los.LOSQuote{Nights: nights, TotalPrice: total, Refundable: refundable})
	}
	return out, nil
}

func renderLosTable(out *os.File, baseline int, flips []los.Flip) {
	if len(flips) == 0 {
		fmt.Fprintf(out, "No rate flips detected for %d-night baseline.\n", baseline)
		return
	}
	fmt.Fprintf(out, "Length-of-stay alternatives (baseline %d nights):\n", baseline)
	fmt.Fprintf(out, "  %-20s %7s %7s %10s %10s %10s  %s\n", "Kind", "Base N", "Alt N", "Base Total", "Alt Total", "Delta", "Reason")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 90))
	for _, f := range flips {
		refStr := ""
		if f.Refundable {
			refStr = " (refundable)"
		}
		fmt.Fprintf(out, "  %-20s %7d %7d %10.2f %10.2f %+10.2f  %s%s\n", f.Kind, f.BaselineNights, f.AlternativeNights, f.BaselineTotal, f.AlternativeTotal, f.TotalDelta, f.Reason, refStr)
	}
}
