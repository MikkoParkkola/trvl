package main

// MIK-3086: trvl rail-pass — rail-pass break-even advisor.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/railpass"
	"github.com/spf13/cobra"
)

func railPassCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rail-pass <pass-cost> <leg-cost>...",
		Short: "Rail-pass break-even: is the pass cheaper than point-to-point?",
		Long: `rail-pass compares a rail pass cost against the stack of point-to-point
segment prices and returns a verdict: buy_pass, skip_pass, or marginal.

The first argument is the pass activation cost.
Subsequent arguments are leg prices as a bare number (e.g. 120) or as
operator:origin:destination:price[:reservation-fee] (e.g. DB:AMS:BRU:89:19).

Examples:
  trvl rail-pass 299 120 95 110
  trvl rail-pass 349 DB:AMS:BRU:89:19 SNCF:PAR:LYS:45
  trvl rail-pass 299 120 95 110 --pass-name "Interrail 4-in-1month" --format json`,
		Args: cobra.MinimumNArgs(2),
		RunE: runRailPass,
	}
	cmd.Flags().String("pass-name", "Rail Pass", "Name of the pass being evaluated")
	cmd.Flags().Int("days", 0, "Number of travel days the pass covers")
	cmd.Flags().Bool("res-included", false, "Reservation fees already included in pass cost")
	return cmd
}

func runRailPass(cmd *cobra.Command, args []string) error {
	format, _ := cmd.Flags().GetString("format")
	passName, _ := cmd.Flags().GetString("pass-name")
	days, _ := cmd.Flags().GetInt("days")
	resIncluded, _ := cmd.Flags().GetBool("res-included")
	passCost, err := strconv.ParseFloat(strings.TrimSpace(args[0]), 64)
	if err != nil {
		return fmt.Errorf("rail-pass: pass-cost must be a number: %w", err)
	}
	segments, err := parseRailSegments(args[1:])
	if err != nil {
		return fmt.Errorf("rail-pass: %w", err)
	}
	pass := railpass.PassOption{Name: passName, Cost: passCost, Days: days, ReservationsIncluded: resIncluded}
	rec := railpass.EvaluatePass(pass, segments)
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(rec)
	}
	renderRailPassTable(os.Stdout, rec)
	return nil
}

func parseRailSegments(args []string) ([]railpass.PointToPointSegment, error) {
	out := make([]railpass.PointToPointSegment, 0, len(args))
	for _, arg := range args {
		parts := strings.Split(arg, ":")
		switch len(parts) {
		case 1:
			price, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid leg %q: must be a number", arg)
			}
			out = append(out, railpass.PointToPointSegment{Price: price})
		case 4:
			price, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid leg %q: price must be a number", arg)
			}
			out = append(out, railpass.PointToPointSegment{Operator: strings.TrimSpace(parts[0]), Origin: strings.TrimSpace(parts[1]), Destination: strings.TrimSpace(parts[2]), Price: price})
		case 5:
			price, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid leg %q: price must be a number", arg)
			}
			resFee, err := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
			if err != nil {
				return nil, fmt.Errorf("invalid leg %q: reservation-fee must be a number", arg)
			}
			out = append(out, railpass.PointToPointSegment{Operator: strings.TrimSpace(parts[0]), Origin: strings.TrimSpace(parts[1]), Destination: strings.TrimSpace(parts[2]), Price: price, ReservationFee: resFee})
		default:
			return nil, fmt.Errorf("invalid leg %q: use number or operator:origin:destination:price[:fee]", arg)
		}
	}
	return out, nil
}

func renderRailPassTable(out *os.File, rec railpass.Recommendation) {
	_, _ = fmt.Fprintf(out, "Rail pass evaluation: %s\n", rec.PassName)
	_, _ = fmt.Fprintf(out, "  Point-to-point total:  %.2f\n", rec.PointToPointTotal)
	_, _ = fmt.Fprintf(out, "  Pass effective cost:   %.2f\n", rec.PassTotalEffective)
	_, _ = fmt.Fprintf(out, "  Savings (p2p - pass):  %+.2f\n", rec.Savings)
	_, _ = fmt.Fprintf(out, "  Break-even segments:   %d\n", rec.BreakEvenSegments)
	_, _ = fmt.Fprintf(out, "  Segments scored:       %d\n", rec.SegmentsScored)
	_, _ = fmt.Fprintf(out, "  Verdict:               %s\n", rec.Verdict)
	_, _ = fmt.Fprintf(out, "  Reason:                %s\n", rec.Reason)
}
