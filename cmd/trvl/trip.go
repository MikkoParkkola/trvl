package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/spf13/cobra"
)

func tripCmd() *cobra.Command {
	var (
		departDate     string
		returnDate     string
		guests         int
		targetCurrency string
	)

	cmd := &cobra.Command{
		Use:   "trip ORIGIN DESTINATION",
		Short: "Plan a complete trip — flights + hotel in one search",
		Long: `Search outbound flights, return flights, and hotels in parallel,
then display the top options and a cheapest-combination cost summary.

ORIGIN and DESTINATION are IATA airport codes (e.g. HEL, BCN, JFK).

Examples:
  trvl trip HEL BCN --depart 2026-07-01 --return 2026-07-08 --currency EUR
  trvl trip AMS PRG --depart 2026-06-15 --return 2026-06-18 --guests 2
  trvl trip JFK LHR --depart 2026-08-01 --return 2026-08-10 --format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			dest := strings.ToUpper(args[1])

			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
			defer cancel()

			input := trip.PlanInput{
				Origin:      origin,
				Destination: dest,
				DepartDate:  departDate,
				ReturnDate:  returnDate,
				Guests:      guests,
				Currency:    targetCurrency,
			}

			result, err := trip.PlanTrip(ctx, input)
			if err != nil {
				return err
			}

			// Cache for `trvl share --last`.
			saveTripPlanLastSearch(result)

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printTripPlan(cmd.Context(), targetCurrency, result)
		},
	}

	cmd.Flags().StringVar(&departDate, "depart", "", "Departure date (YYYY-MM-DD, required)")
	cmd.Flags().StringVar(&returnDate, "return", "", "Return date (YYYY-MM-DD, required)")
	cmd.Flags().IntVar(&guests, "guests", 1, "Number of guests (must be >= 1)")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR). Empty = API default")

	_ = cmd.MarkFlagRequired("depart")
	_ = cmd.MarkFlagRequired("return")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

func printTripPlan(ctx context.Context, targetCurrency string, result *trip.PlanResult) error {
	if !result.Success && len(result.OutboundFlights) == 0 && len(result.ReturnFlights) == 0 && len(result.Hotels) == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Trip planning failed: %s\n", result.Error)
		return nil
	}
	if !result.Success && result.Error != "" {
		_, _ = fmt.Fprintf(os.Stderr, "Partial trip plan: %s\n\n", result.Error)
	}

	originName := models.LookupAirportName(result.Origin)
	destName := models.LookupAirportName(result.Destination)

	// Check for matching deals.
	bannerLines := []string{
		fmt.Sprintf("%s -> %s · %d nights · %d guest(s)", originName, destName, result.Nights, result.Guests),
	}
	matchedDeals := deals.MatchDeals(ctx, result.Origin, result.Destination)
	for _, d := range matchedDeals {
		dealLine := fmt.Sprintf("🔥 %s", d.Title)
		titleRunes := []rune(dealLine)
		if len(titleRunes) > 65 {
			dealLine = string(titleRunes[:62]) + "..."
		}
		bannerLines = append(bannerLines, dealLine)
	}

	models.Banner(os.Stdout, "🧳", "Trip Plan", bannerLines...)
	fmt.Println()

	// Outbound flights.
	if len(result.OutboundFlights) > 0 {
		fmt.Printf("  %s Outbound: %s -> %s (%s)\n\n", models.Bold("✈️"), result.Origin, result.Destination, result.DepartDate)
		headers := []string{"Price", "Airline", "Flight", "Stops", "Duration", "Departs", "Arrives"}
		var rows [][]string
		var prices priceScale
		for _, f := range result.OutboundFlights {
			prices = prices.With(f.Price)
		}
		for _, f := range result.OutboundFlights {
			p := f.Price
			cur := f.Currency
			if targetCurrency != "" && cur != targetCurrency && p > 0 {
				converted, c := destinations.ConvertCurrency(ctx, p, cur, targetCurrency)
				p = math.Round(converted)
				cur = c
			}
			rows = append(rows, []string{
				prices.Apply(p, formatPrice(p, cur)),
				f.Airline,
				f.Flight,
				colorizeStops(f.Stops),
				formatDuration(f.Duration),
				f.Departure,
				f.Arrival,
			})
		}
		models.FormatTable(os.Stdout, headers, rows)
		fmt.Println()
	}

	// Return flights.
	if len(result.ReturnFlights) > 0 {
		fmt.Printf("  %s Return: %s -> %s (%s)\n\n", models.Bold("✈️"), result.Destination, result.Origin, result.ReturnDate)
		headers := []string{"Price", "Airline", "Flight", "Stops", "Duration", "Departs", "Arrives"}
		var rows [][]string
		var prices priceScale
		for _, f := range result.ReturnFlights {
			prices = prices.With(f.Price)
		}
		for _, f := range result.ReturnFlights {
			p := f.Price
			cur := f.Currency
			if targetCurrency != "" && cur != targetCurrency && p > 0 {
				converted, c := destinations.ConvertCurrency(ctx, p, cur, targetCurrency)
				p = math.Round(converted)
				cur = c
			}
			rows = append(rows, []string{
				prices.Apply(p, formatPrice(p, cur)),
				f.Airline,
				f.Flight,
				colorizeStops(f.Stops),
				formatDuration(f.Duration),
				f.Departure,
				f.Arrival,
			})
		}
		models.FormatTable(os.Stdout, headers, rows)
		fmt.Println()
	}

	// Hotels.
	if len(result.Hotels) > 0 {
		fmt.Printf("  %s Hotels in %s (%d nights)\n\n", models.Bold("🏨"), destName, result.Nights)
		headers := []string{"Price/night", "Total", "Name", "Rating", "Reviews", "Amenities"}
		var rows [][]string
		var prices priceScale
		for _, h := range result.Hotels {
			prices = prices.With(h.PerNight)
		}
		for _, h := range result.Hotels {
			pn := h.PerNight
			total := h.Total
			cur := h.Currency
			if targetCurrency != "" && cur != targetCurrency && pn > 0 {
				converted, c := destinations.ConvertCurrency(ctx, pn, cur, targetCurrency)
				pn = math.Round(converted)
				cur = c
				total = pn * float64(result.Nights)
			}
			rows = append(rows, []string{
				prices.Apply(pn, formatPrice(pn, cur)),
				formatPrice(total, cur),
				truncateName(h.Name, 35),
				colorizeRating(h.Rating, fmt.Sprintf("%.1f", h.Rating)),
				fmt.Sprintf("%d", h.Reviews),
				h.Amenities,
			})
		}
		models.FormatTable(os.Stdout, headers, rows)
		fmt.Println()
	}

	// Destination context from Wikivoyage.
	if result.Context != nil {
		fmt.Printf("  %s About %s\n\n", models.Bold("📖"), destName)
		if result.Context.Summary != "" {
			fmt.Printf("  %s\n", result.Context.Summary)
		}
		if result.Context.WhenToGo != "" {
			fmt.Printf("  %s %s\n", models.Bold("When to go:"), result.Context.WhenToGo)
		}
		if result.Context.GetAround != "" {
			fmt.Printf("  %s %s\n", models.Bold("Getting around:"), result.Context.GetAround)
		}
		if result.Context.Source != "" {
			fmt.Printf("  Source: %s\n", result.Context.Source)
		}
		fmt.Println()
	}

	// Review snippets for the chosen hotel.
	if len(result.ReviewSnippets) > 0 {
		hotelName := result.ReviewSnippets[0].HotelName
		if hotelName != "" {
			fmt.Printf("  %s Guest reviews for %s\n\n", models.Bold("💬"), truncateName(hotelName, 40))
		} else {
			fmt.Printf("  %s Guest reviews\n\n", models.Bold("💬"))
		}
		for _, r := range result.ReviewSnippets {
			rating := ""
			if r.Rating > 0 {
				rating = fmt.Sprintf("%.1f★ ", r.Rating)
			}
			author := r.Author
			if author == "" {
				author = "anonymous"
			}
			fmt.Printf("  %s— %s (%s)\n", rating, author, r.Date)
			fmt.Printf("    \"%s\"\n", r.Text)
		}
		fmt.Println()
	}

	// Breakfast spots within walking distance of the chosen hotel.
	if len(result.Breakfast) > 0 {
		hotelName := result.Breakfast[0].HotelName
		if hotelName != "" {
			fmt.Printf("  %s Breakfast within 500m of %s\n\n", models.Bold("☕"), truncateName(hotelName, 40))
		} else {
			fmt.Printf("  %s Breakfast within walking distance\n\n", models.Bold("☕"))
		}
		headers := []string{"Name", "Type", "Distance", "Cuisine", "Hours"}
		var rows [][]string
		for _, b := range result.Breakfast {
			rows = append(rows, []string{
				truncateName(b.Name, 30),
				b.Type,
				fmt.Sprintf("%dm", b.Distance),
				b.Cuisine,
				truncateName(b.Hours, 25),
			})
		}
		models.FormatTable(os.Stdout, headers, rows)
		fmt.Println()
	}

	// Summary — compute from the cheapest displayed (converted) prices
	// to avoid raw-API-currency vs display-currency mismatch.
	cur := targetCurrency
	if cur == "" {
		cur = result.Summary.Currency
	}

	var cheapOut, cheapRet, cheapHotel float64
	if len(result.OutboundFlights) > 0 {
		f := result.OutboundFlights[0]
		cheapOut = f.Price
		if targetCurrency != "" && f.Currency != targetCurrency && cheapOut > 0 {
			converted, _ := destinations.ConvertCurrency(ctx, cheapOut, f.Currency, targetCurrency)
			cheapOut = math.Round(converted)
		}
	}
	if len(result.ReturnFlights) > 0 {
		f := result.ReturnFlights[0]
		cheapRet = f.Price
		if targetCurrency != "" && f.Currency != targetCurrency && cheapRet > 0 {
			converted, _ := destinations.ConvertCurrency(ctx, cheapRet, f.Currency, targetCurrency)
			cheapRet = math.Round(converted)
		}
	}
	if len(result.Hotels) > 0 {
		h := result.Hotels[0]
		cheapHotel = h.Total
		if targetCurrency != "" && h.Currency != targetCurrency && cheapHotel > 0 {
			converted, _ := destinations.ConvertCurrency(ctx, cheapHotel, h.Currency, targetCurrency)
			cheapHotel = math.Round(converted)
		}
	}

	fTotal := (cheapOut + cheapRet) * float64(result.Guests)
	hTotal := cheapHotel
	gTotal := fTotal + hTotal
	var pp, pd float64
	if result.Guests > 0 {
		pp = gTotal / float64(result.Guests)
	}
	if result.Nights > 0 {
		pd = gTotal / float64(result.Nights)
	}

	models.Summary(os.Stdout, fmt.Sprintf("Flights: %s %.0f + Hotel: %s %.0f = %s %s %.0f (%s %.0f/day, %s %.0f/person)",
		cur, fTotal, cur, hTotal, models.Bold("Total"), cur, gTotal, cur, pd, cur, pp))
	models.BookingHint(os.Stdout)

	return nil
}

func truncateName(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen-3]) + "..."
	}
	return s
}

// saveTripPlanLastSearch caches a trip plan result for `trvl share --last`.
func saveTripPlanLastSearch(r *trip.PlanResult) {
	if r == nil {
		return
	}
	ls := &LastSearch{
		Command:     "trip",
		Origin:      models.LookupAirportName(r.Origin),
		Destination: models.LookupAirportName(r.Destination),
		DepartDate:  r.DepartDate,
		ReturnDate:  r.ReturnDate,
		Nights:      r.Nights,
		Guests:      r.Guests,
	}
	if len(r.OutboundFlights) > 0 {
		f := r.OutboundFlights[0]
		ls.FlightPrice = f.Price
		ls.FlightCurrency = f.Currency
		ls.FlightAirline = f.Airline
		ls.FlightStops = f.Stops
	}
	if len(r.Hotels) > 0 {
		h := r.Hotels[0]
		ls.HotelPrice = h.Total
		ls.HotelCurrency = h.Currency
		ls.HotelName = h.Name
	}
	ls.TotalCurrency = r.Summary.Currency
	ls.TotalPrice = r.Summary.GrandTotal
	saveLastSearch(ls)
}
