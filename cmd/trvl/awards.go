package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/awards"
	"github.com/spf13/cobra"
)

func awardsCmd() *cobra.Command {
	var (
		seatFlags    []string
		balanceFlags []string
		minCPP       float64
		cabin        string
		currency     string
	)

	cmd := &cobra.Command{
		Use:   "awards ORIGIN DESTINATION",
		Short: "Find cross-program award sweet spots (FB/Avios/Aeroplan/Virgin + transfer partners)",
		Long: `Find the cheapest redemption path for award seats across loyalty programs.

Provide pre-fetched seat fixtures via --seat flags (from seats.aero or known
availability), your point balances via --balance flags, and get ranked redemption
paths with miles cost, cash equivalent, cents-per-point, and transfer route.

Supported programs: FB (Flying Blue), BA (Avios), AC (Aeroplan), VS (Virgin),
AY (Finnair Plus), plus transfer currencies MR (Amex), UR (Chase), Bilt.

Examples:
  trvl awards HEL LHR \
    --seat VS:50000:35.00:650.00:2026-08-15:business \
    --seat AY:40000:30.00:620.00:2026-08-15:business \
    --balance MR:80000 --balance VS:20000 \
    --min-cpp 1.0

  trvl awards JFK LHR \
    --seat BA:60000:400.00:1200.00:2026-09-01:first \
    --balance MR:120000 --balance UR:50000`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			destination := strings.ToUpper(args[1])

			// Parse seat fixtures.
			if len(seatFlags) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No seat fixtures provided. Use --seat PROGRAM:MILES:CASH_FEES:CASH_EQUIV:DATE:CABIN")
				return nil
			}

			seats := make([]awards.AwardSeat, 0, len(seatFlags))
			for _, s := range seatFlags {
				seat, err := parseSeatFlag(s, origin, destination)
				if err != nil {
					return fmt.Errorf("invalid --seat %q: %w", s, err)
				}
				seats = append(seats, seat)
			}

			// Parse balances.
			balances := make([]awards.PointBalance, 0, len(balanceFlags))
			for _, b := range balanceFlags {
				bal, err := parseBalanceFlag(b)
				if err != nil {
					return fmt.Errorf("invalid --balance %q: %w", b, err)
				}
				balances = append(balances, bal)
			}

			// Find sweet spots (nil = use default transfer ratios).
			spots := awards.FindSweetSpots(seats, balances, nil)

			// Apply filters.
			var filtered []awards.SweetSpot
			for _, s := range spots {
				if s.CentsPerPoint < minCPP {
					continue
				}
				if cabin != "" && !strings.EqualFold(s.Seat.Cabin, cabin) {
					continue
				}
				filtered = append(filtered, s)
			}

			if len(filtered) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No affordable sweet spots found.")
				return nil
			}

			// Print table.
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%-6s  %-12s  %-12s  %-8s  %-7s  %-8s  %-10s  %-11s  %-5s  %-10s  %s\n",
				"SEAT", "ORIGIN-DEST", "DATE", "CABIN", "SOURCE", "MILES", "CASH_FEES", "CASH_EQUIV", "CPP", "AFFORDABLE", "ROUTE")
			_, _ = fmt.Fprintf(out, "%s\n", strings.Repeat("-", 110))

			for _, s := range filtered {
				route := fmt.Sprintf("%s-%s", s.Seat.Origin, s.Seat.Destination)
				affordable := "no"
				if s.Affordable {
					affordable = "yes"
				}
				_, _ = fmt.Fprintf(out, "%-6s  %-12s  %-12s  %-8s  %-7s  %-8d  %-10.2f  %-11.2f  %-5.2f  %-10s  %s\n",
					s.Seat.Program,
					route,
					s.Seat.Date,
					s.Seat.Cabin,
					s.SourceProgram,
					s.MilesSpentSource,
					s.CashFees,
					s.CashEquivalent,
					s.CentsPerPoint,
					affordable,
					s.TransferRoute,
				)
			}

			_ = currency // reserved for future currency conversion display
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&seatFlags, "seat", nil,
		"Seat fixture: PROGRAM:MILES:CASH_FEES:CASH_EQUIV:DATE:CABIN (e.g. VS:50000:35.00:650.00:2026-08-15:business)")
	cmd.Flags().StringArrayVar(&balanceFlags, "balance", nil,
		"Point balance: PROGRAM:AMOUNT (e.g. MR:80000, VS:20000)")
	cmd.Flags().Float64Var(&minCPP, "min-cpp", 0.5, "Minimum cents-per-point to show")
	cmd.Flags().StringVar(&cabin, "cabin", "", "Filter by cabin class (economy/premium_economy/business/first)")
	cmd.Flags().StringVar(&currency, "currency", "EUR", "Display currency for cash amounts")

	return cmd
}

// parseSeatFlag parses PROGRAM:MILES:CASH_FEES:CASH_EQUIV:DATE:CABIN.
func parseSeatFlag(s, origin, destination string) (awards.AwardSeat, error) {
	parts := strings.SplitN(s, ":", 6)
	if len(parts) != 6 {
		return awards.AwardSeat{}, fmt.Errorf("expected PROGRAM:MILES:CASH_FEES:CASH_EQUIV:DATE:CABIN, got %d fields", len(parts))
	}

	miles, err := strconv.Atoi(parts[1])
	if err != nil {
		return awards.AwardSeat{}, fmt.Errorf("miles must be an integer: %w", err)
	}

	cashFees, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return awards.AwardSeat{}, fmt.Errorf("cash_fees must be a number: %w", err)
	}

	cashEquiv, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return awards.AwardSeat{}, fmt.Errorf("cash_equivalent must be a number: %w", err)
	}

	return awards.AwardSeat{
		Program:          strings.ToUpper(parts[0]),
		Origin:           origin,
		Destination:      destination,
		Date:             parts[4],
		Cabin:            strings.ToLower(parts[5]),
		MilesCost:        miles,
		CashFees:         cashFees,
		CashEquivalent:   cashEquiv,
		BookableSegments: 1,
	}, nil
}

// parseBalanceFlag parses PROGRAM:AMOUNT.
func parseBalanceFlag(s string) (awards.PointBalance, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return awards.PointBalance{}, fmt.Errorf("expected PROGRAM:AMOUNT")
	}

	balance, err := strconv.Atoi(parts[1])
	if err != nil {
		return awards.PointBalance{}, fmt.Errorf("balance must be an integer: %w", err)
	}

	return awards.PointBalance{
		Program: strings.ToUpper(parts[0]),
		Balance: balance,
	}, nil
}
