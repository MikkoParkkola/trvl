package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/optimizer"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/scoring"
	"github.com/spf13/cobra"
)

func optimizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "optimize ORIGIN DESTINATION",
		Short: "Find the optimal booking strategy using all pricing primitives",
		Long: `Searches all combinations of origins, destinations, dates, airlines,
and transport modes to find the cheapest booking strategy.

Examples:
  trvl optimize HEL BCN --depart 2026-06-15 --return 2026-06-22
  trvl optimize HEL BCN --depart 2026-06-15 --return 2026-06-22 --flex 5
  trvl optimize HEL AMS --depart 2026-07-01 --return 2026-07-08 --currency EUR`,
		Args: cobra.ExactArgs(2),
		RunE: runOptimize,
	}
	cmd.Flags().String("depart", "", "Departure date (YYYY-MM-DD)")
	cmd.Flags().String("return", "", "Return date (YYYY-MM-DD)")
	cmd.Flags().Int("flex", 3, "Date flexibility ±N days")
	cmd.Flags().Int("guests", 1, "Number of guests")
	cmd.Flags().String("currency", "", "Display currency")
	cmd.Flags().Int("results", 5, "Number of results")
	cmd.Flags().Bool("explain", false, "Show per-factor profile match breakdown for each result")
	_ = cmd.MarkFlagRequired("depart")
	return cmd
}

func runOptimize(cmd *cobra.Command, args []string) error {
	origin := strings.ToUpper(args[0])
	dest := strings.ToUpper(args[1])

	depart, _ := cmd.Flags().GetString("depart")
	returnDate, _ := cmd.Flags().GetString("return")
	flex, _ := cmd.Flags().GetInt("flex")
	guests, _ := cmd.Flags().GetInt("guests")
	currency, _ := cmd.Flags().GetString("currency")
	maxResults, _ := cmd.Flags().GetInt("results")

	// Load user preferences for FF status, carry-on preference, home airports.
	prefs, _ := preferences.Load()

	input := optimizer.OptimizeInput{
		Origin:      origin,
		Destination: dest,
		DepartDate:  depart,
		ReturnDate:  returnDate,
		FlexDays:    flex,
		Guests:      guests,
		Currency:    currency,
		MaxResults:  maxResults,
	}

	if prefs != nil {
		if currency == "" {
			input.Currency = prefs.DisplayCurrency
		}
		input.CarryOnOnly = prefs.CarryOnOnly
		input.NeedCheckedBag = !prefs.CarryOnOnly
		input.HomeAirports = prefs.HomeAirports
		for _, fp := range prefs.FrequentFlyerPrograms {
			input.FFStatuses = append(input.FFStatuses, optimizer.FFStatus{
				Alliance: fp.Alliance,
				Tier:     fp.Tier,
			})
		}
	}

	if format == "json" {
		result, err := optimizer.Optimize(cmd.Context(), input)
		if err != nil {
			return err
		}
		return models.FormatJSON(os.Stdout, result)
	}

	result, err := optimizer.Optimize(cmd.Context(), input)
	if err != nil {
		return err
	}

	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Optimization failed: %s\n", result.Error)
		return nil
	}

	explain, _ := cmd.Flags().GetBool("explain")
	printOptimizeResults(result, dest, prefs, explain)
	return nil
}

func printOptimizeResults(result *optimizer.OptimizeResult, destCode string, prefs *preferences.Preferences, explain bool) {
	models.Banner(os.Stdout, "⚡", "Optimize", fmt.Sprintf("Found %d booking strategies", len(result.Options)))
	fmt.Println()

	headers := []string{"Rank", "Strategy", "All-in Cost", "Savings", "Hacks Applied"}
	var rows [][]string
	var prices priceScale

	for _, opt := range result.Options {
		prices = prices.With(opt.AllInCost)
	}

	for _, opt := range result.Options {
		savings := ""
		if opt.SavingsVsBaseline > 0 {
			savings = fmt.Sprintf("%s %.0f", opt.Currency, opt.SavingsVsBaseline)
		}
		hacksStr := "-"
		if len(opt.HacksApplied) > 0 {
			hacksStr = strings.Join(opt.HacksApplied, ", ")
		}
		rows = append(rows, []string{
			fmt.Sprintf("#%d", opt.Rank),
			opt.Strategy,
			prices.Apply(opt.AllInCost, fmt.Sprintf("%s %.0f", opt.Currency, opt.AllInCost)),
			savings,
			hacksStr,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)

	// Baseline comparison.
	if result.Baseline != nil && len(result.Options) > 0 {
		best := result.Options[0]
		if best.SavingsVsBaseline > 0 {
			pct := 0.0
			if result.Baseline.AllInCost > 0 {
				pct = (best.SavingsVsBaseline / result.Baseline.AllInCost) * 100
			}
			models.Summary(os.Stdout, fmt.Sprintf(
				"Naive: %s %.0f -> Optimized: %s %.0f -> Saved: %s %.0f (%.0f%%)",
				result.Baseline.Currency, result.Baseline.AllInCost,
				best.Currency, best.AllInCost,
				best.Currency, best.SavingsVsBaseline,
				pct,
			))
		} else {
			models.Summary(os.Stdout, fmt.Sprintf(
				"Direct booking is already the cheapest: %s %.0f",
				result.Baseline.Currency, result.Baseline.AllInCost,
			))
		}
	}

	// --explain: per-strategy profile match breakdown.
	if explain {
		fmt.Println()
		for i, opt := range result.Options {
			// Extract destination from legs: last flight leg's destination.
			code := destCode
			if len(opt.Legs) > 0 {
				for j := len(opt.Legs) - 1; j >= 0; j-- {
					if strings.EqualFold(opt.Legs[j].Type, "flight") && opt.Legs[j].To != "" {
						code = opt.Legs[j].To
						break
					}
				}
			}
			matchScore, breakdown := scoring.ComputeProfileMatch(prefs, scoring.DiscoverInput{
				AirportCode: code,
				FlightPrice: opt.AllInCost,
				Total:       opt.AllInCost,
			})
			printMatchBreakdown(fmt.Sprintf("#%d %s", i+1, opt.Strategy), matchScore, breakdown)
		}
	}
}
