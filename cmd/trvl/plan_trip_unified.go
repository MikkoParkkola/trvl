package main

// Innovation #5: trvl plan-all — unified trip coalescer.
//
// Today a user runs `trvl flights`, `trvl hotels`, and `trvl ground` as three
// separate commands. plan-all fans all three out concurrently through the
// bounded coalescer and prints one combined plan with a floor cost estimate.
// One domain failing yields a partial plan, never an aborted run.

import (
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/tripcoalesce"
	"github.com/spf13/cobra"
)

func planAllCmd() *cobra.Command {
	var (
		returnDate    string
		hotelLocation string
		nights        int
		travelers     int
		currency      string
		allowBrowser  bool
	)

	cmd := &cobra.Command{
		Use:   "plan-all <origin> <destination> <depart-date>",
		Short: "Coalesce flights, hotels, and ground into one trip plan (concurrent fan-out)",
		Long: `plan-all issues trvl's flight, hotel, and ground searches concurrently for a
single origin→destination trip and assembles one combined plan: the cheapest
priced option per domain plus a floor total-cost estimate.

Each domain runs in isolation — if one provider fails or times out, the other
domains still return, clearly flagged in the per-domain status list. Nothing is
fabricated: a domain with no priced result is reported, never guessed.

origin and destination are IATA airport codes for flights; the same values seed
the ground "from"/"to" and the hotel location (override the hotel location with
--hotel).

Examples:
  trvl plan-all HEL LHR 2026-07-01
  trvl plan-all HEL LHR 2026-07-01 --return 2026-07-08 --nights 7 --travelers 2
  trvl plan-all HEL BCN 2026-08-01 --hotel "Barcelona" --format json`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(strings.TrimSpace(args[0]))
			destination := strings.ToUpper(strings.TrimSpace(args[1]))
			departDate := strings.TrimSpace(args[2])

			plan := tripcoalesce.Plan(cmd.Context(), tripcoalesce.Params{
				Origin:                origin,
				Destination:           destination,
				DepartDate:            departDate,
				ReturnDate:            strings.TrimSpace(returnDate),
				HotelLocation:         strings.TrimSpace(hotelLocation),
				Nights:                nights,
				Travelers:             travelers,
				Currency:              currency,
				AllowBrowserFallbacks: allowBrowser,
			})

			if format == "json" {
				return models.FormatJSON(os.Stdout, plan)
			}
			printCoalescedPlan(plan)
			return nil
		},
	}

	cmd.Flags().StringVar(&returnDate, "return", "", "return date (YYYY-MM-DD) for round-trip flights / hotel checkout")
	cmd.Flags().StringVar(&hotelLocation, "hotel", "", "hotel search location (default: destination)")
	cmd.Flags().IntVar(&nights, "nights", 0, "nights for the hotel cost estimate (0 = per-night only)")
	cmd.Flags().IntVar(&travelers, "travelers", 1, "number of travelers")
	cmd.Flags().StringVar(&currency, "currency", "EUR", "trip currency")
	cmd.Flags().BoolVar(&allowBrowser, "allow-browser-fallbacks", false, "allow browser/cookie-assisted ground providers")

	_ = cmd.RegisterFlagCompletionFunc("hotel", cobra.NoFileCompletions)
	cmd.ValidArgsFunction = airportCompletion
	return cmd
}

func printCoalescedPlan(plan *tripcoalesce.TripPlan) {
	header := fmt.Sprintf("Trip plan · %s → %s", plan.Origin, plan.Destination)
	sub := plan.DepartDate
	if plan.ReturnDate != "" {
		sub += " → " + plan.ReturnDate
	}
	models.Banner(os.Stdout, "🧳", header, sub)
	fmt.Println()

	if plan.CheapestFlight != nil {
		f := plan.CheapestFlight
		fmt.Printf("✈  Flights   cheapest %s %.2f  ·  %s, %s\n",
			f.Currency, f.Price, formatStops(f.Stops), formatDuration(f.Duration))
	} else {
		fmt.Printf("✈  Flights   %s\n", domainNote(plan, "flights"))
	}

	if plan.CheapestHotel != nil {
		h := plan.CheapestHotel
		fmt.Printf("🏨 Hotels    cheapest %s %.2f/night  ·  %s\n", h.Currency, h.Price, h.Name)
	} else {
		fmt.Printf("🏨 Hotels    %s\n", domainNote(plan, "hotels"))
	}

	if plan.CheapestGround != nil {
		r := plan.CheapestGround
		fmt.Printf("🚆 Ground    cheapest %s %.2f  ·  %s (%s)\n", r.Currency, r.Price, r.Provider, r.Type)
	} else {
		fmt.Printf("🚆 Ground    %s\n", domainNote(plan, "ground"))
	}

	fmt.Println()
	if plan.TotalCostEstimate > 0 {
		fmt.Printf("Floor estimate: %s %.2f\n", plan.Currency, plan.TotalCostEstimate)
		for _, c := range plan.CostBreakdown {
			note := ""
			if c.Currency != plan.Currency {
				note = "  (not in trip currency; excluded from floor)"
			}
			fmt.Printf("   • %-8s %-26s %s %.2f%s\n", c.Domain, c.Label, c.Currency, c.Amount, note)
		}
	} else {
		fmt.Println("No priced options to total — see per-domain status below.")
	}

	if len(plan.Notes) > 0 {
		fmt.Println()
		for _, n := range plan.Notes {
			fmt.Printf("Note: %s\n", n)
		}
	}
}

// domainNote returns a short status string for a domain that produced no
// cheapest pick, distinguishing a failure from a clean empty.
func domainNote(plan *tripcoalesce.TripPlan, domain string) string {
	for _, s := range plan.Statuses {
		if s.Domain != domain {
			continue
		}
		if s.Error != "" {
			return "unavailable: " + s.Error
		}
		if !s.OK {
			return "unavailable"
		}
		return "no priced results"
	}
	return "no results"
}
