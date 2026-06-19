package main

// Innovation #8: trvl arbitrage-report — unified arbitrage lens.
//
// Aggregates trvl's independent arbitrage engines (currency, cabin-class,
// hotel rate/points) into one ranked report for a trip context. Engines that
// lack the inputs they need for the given context are skipped gracefully and
// listed, never fabricated.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/arbreport"
	"github.com/MikkoParkkola/trvl/internal/hotelarb"
	"github.com/spf13/cobra"
)

func arbitrageReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "arbitrage-report <origin> <destination> <depart-date>",
		Short: "Unified arbitrage report across currency, cabin, and hotel engines",
		Long: `arbitrage-report runs trvl's arbitrage engines for one trip context and
prints a single ranked table of opportunities, each with type, description,
estimated saving, currency, and confidence.

Engines:
  currency  airline currency arbitrage (live flight lookup; needs depart date)
  cabin     near-flat cabin-class upgrades (needs a fare ladder via --cabin)
  hotel     hotel rate re-book savings (needs holds via --rebook)

Engines without the inputs they need for this trip are skipped and listed,
never guessed.

Cabin fares (repeatable): --cabin cabin:price[:carrier]
  valid cabins: economy, premium_economy, business, first
Hotel re-book candidates (repeatable): --rebook name:original:current[:currency]

Examples:
  trvl arbitrage-report HEL LHR 2026-08-01
  trvl arbitrage-report HEL BUD 2026-08-01 --cabin economy:500 --cabin premium_economy:540
  trvl arbitrage-report HEL LHR 2026-08-01 --rebook "Grand:300:240:EUR" --format json`,
		Args: cobra.ExactArgs(3),
		RunE: runArbitrageReport,
	}
	cmd.Flags().StringArray("cabin", nil, "cabin fare as cabin:price[:carrier] (repeatable)")
	cmd.Flags().StringArray("rebook", nil, "hotel re-book candidate as name:original:current[:currency] (repeatable)")
	cmd.Flags().String("currency", "", "trip currency (default EUR)")
	cmd.Flags().Int("travelers", 1, "number of travelers")
	return cmd
}

func runArbitrageReport(cmd *cobra.Command, args []string) error {
	origin := strings.ToUpper(strings.TrimSpace(args[0]))
	destination := strings.ToUpper(strings.TrimSpace(args[1]))
	departDate := strings.TrimSpace(args[2])

	cabinArgs, _ := cmd.Flags().GetStringArray("cabin")
	rebookArgs, _ := cmd.Flags().GetStringArray("rebook")
	currency, _ := cmd.Flags().GetString("currency")
	travelers, _ := cmd.Flags().GetInt("travelers")

	fares, err := parseCabinFares(cabinArgs)
	if err != nil {
		return fmt.Errorf("arbitrage-report: %w", err)
	}
	rebooks, err := parseRebookCandidates(rebookArgs)
	if err != nil {
		return fmt.Errorf("arbitrage-report: %w", err)
	}

	params := arbreport.Params{
		Origin:       origin,
		Destination:  destination,
		DepartDate:   departDate,
		Currency:     currency,
		Travelers:    travelers,
		CabinFares:   fares,
		HotelRebooks: rebooks,
	}

	report := arbreport.Aggregate(context.Background(), params)

	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	renderArbReportTableTo(os.Stdout, report)
	return nil
}

// parseRebookCandidates parses name:original:current[:currency] tuples.
func parseRebookCandidates(args []string) ([]arbreport.HotelRebook, error) {
	out := make([]arbreport.HotelRebook, 0, len(args))
	for _, arg := range args {
		parts := strings.SplitN(arg, ":", 4)
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid rebook %q: expected name:original:current[:currency]", arg)
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			return nil, fmt.Errorf("invalid rebook %q: hotel name must not be empty", arg)
		}
		original, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rebook %q: original price must be a number: %w", arg, err)
		}
		current, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid rebook %q: current price must be a number: %w", arg, err)
		}
		cur := "EUR"
		if len(parts) == 4 && strings.TrimSpace(parts[3]) != "" {
			cur = strings.ToUpper(strings.TrimSpace(parts[3]))
		}
		out = append(out, arbreport.HotelRebook{
			Hold: hotelarb.Hold{
				HotelName:     name,
				OriginalPrice: original,
				Currency:      cur,
				Refundable:    true,
			},
			Quote: hotelarb.PriceQuote{Price: current, Currency: cur},
		})
	}
	return out, nil
}

func renderArbReportTableTo(out io.Writer, report arbreport.ArbReport) {
	_, _ = fmt.Fprintf(out, "Unified arbitrage report — %s→%s on %s (%s)\n",
		report.Origin, report.Destination, report.DepartDate, report.Currency)
	if report.Count == 0 {
		_, _ = fmt.Fprintln(out, "\nNo arbitrage opportunities found for this trip context.")
	} else {
		_, _ = fmt.Fprintf(out, "\n%d opportunit%s (ranked by estimated saving):\n", report.Count, plural(report.Count))
		_, _ = fmt.Fprintf(out, "  %-9s %-16s %10s %-9s  %s\n", "Engine", "Type", "Saving", "Confidence", "Description")
		_, _ = fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 90))
		for _, o := range report.Opportunities {
			saving := fmt.Sprintf("%.2f %s", o.EstimatedSaving, o.Currency)
			_, _ = fmt.Fprintf(out, "  %-9s %-16s %10s %-9s  %s\n",
				o.Engine, o.Type, saving, o.Confidence, o.Description)
		}
	}
	if len(report.Skipped) > 0 {
		_, _ = fmt.Fprintf(out, "\nSkipped engines:\n")
		for _, s := range report.Skipped {
			_, _ = fmt.Fprintf(out, "  %-9s %s\n", s.Engine, s.Reason)
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
