package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/scoring"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/spf13/cobra"
)

func tripCostCmd() *cobra.Command {
	var (
		departDate string
		returnDate string
		guests     int
		currency   string
		format     string
		explain    bool
	)

	cmd := &cobra.Command{
		Use:   "trip-cost ORIGIN DESTINATION",
		Short: "Estimate total trip cost (flights + hotel)",
		Long: `Calculate the total estimated cost for a trip including outbound flight,
return flight, and hotel accommodation at the destination.

ORIGIN and DESTINATION are IATA airport codes (e.g. HEL, BCN, JFK).
Flights are priced per person; hotels are per room.

Examples:
  trvl trip-cost HEL BCN --depart 2026-07-01 --return 2026-07-08
  trvl trip-cost HEL BCN --depart 2026-07-01 --return 2026-07-08 --guests 2
  trvl trip-cost JFK LHR --depart 2026-08-01 --return 2026-08-10 --currency USD --format json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin := strings.ToUpper(args[0])
			dest := strings.ToUpper(args[1])

			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()

			input := trip.TripCostInput{
				Origin:      origin,
				Destination: dest,
				DepartDate:  departDate,
				ReturnDate:  returnDate,
				Guests:      guests,
				Currency:    currency,
			}

			result, err := trip.CalculateTripCost(ctx, input)
			if err != nil {
				return err
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			return printTripCostTable(result, origin, dest, guests, explain)
		},
	}

	cmd.Flags().StringVar(&departDate, "depart", "", "Departure date (YYYY-MM-DD, required)")
	cmd.Flags().StringVar(&returnDate, "return", "", "Return date (YYYY-MM-DD, required)")
	cmd.Flags().IntVar(&guests, "guests", 1, "Number of guests (must be >= 1)")
	cmd.Flags().StringVar(&currency, "currency", "", "Convert prices to this currency (e.g. EUR). Empty = API default")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().BoolVar(&explain, "explain", false, "Show per-factor profile match breakdown for the trip")

	_ = cmd.MarkFlagRequired("depart")
	_ = cmd.MarkFlagRequired("return")

	return cmd
}

func printTripCostTable(result *trip.TripCostResult, origin, dest string, guests int, explain bool) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Trip cost estimation failed: %s\n", result.Error)
		return nil
	}

	if result.Error != "" {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: %s\n\n", result.Error)
	}

	cur := result.Currency
	fmt.Printf("Trip: %s -> %s (%d nights, %d guest(s))\n\n", origin, dest, result.Nights, guests)

	headers := []string{"Component", "Amount", "Details"}
	var rows [][]string

	rows = append(rows, []string{
		"Outbound flight",
		formatPrice(result.Flights.Outbound, result.Flights.Currency),
		tripCostFlightDetail(result.Flights.Outbound, origin, dest, result.Flights.OutboundStops),
	})
	rows = append(rows, []string{
		"Return flight",
		formatPrice(result.Flights.Return, result.Flights.Currency),
		tripCostFlightDetail(result.Flights.Return, dest, origin, result.Flights.ReturnStops),
	})

	rows = append(rows, []string{
		"Hotel",
		formatPrice(result.Hotels.Total, result.Hotels.Currency),
		tripCostHotelDetail(result.Hotels, result.Nights),
	})

	rows = append(rows, []string{"", "", ""})
	rows = append(rows, []string{"Total", fmt.Sprintf("%s %.0f", cur, result.Total), ""})
	rows = append(rows, []string{"Per person", fmt.Sprintf("%s %.0f", cur, result.PerPerson), ""})
	rows = append(rows, []string{"Per day", fmt.Sprintf("%s %.0f", cur, result.PerDay), ""})

	models.FormatTable(os.Stdout, headers, rows)

	// --explain: show profile match breakdown for this trip.
	if explain {
		fmt.Println()
		prefs, _ := preferences.Load()
		matchScore, breakdown := scoring.ComputeProfileMatch(prefs, scoring.DiscoverInput{
			AirportCode: dest,
			FlightPrice: result.Flights.Outbound + result.Flights.Return,
			HotelPrice:  result.Hotels.Total,
			Total:       result.Total,
			HotelName:   result.Hotels.Name,
			Stops:       result.Flights.OutboundStops,
		})
		printMatchBreakdown(fmt.Sprintf("%s → %s", origin, dest), matchScore, breakdown)
	}

	return nil
}

func tripCostFlightDetail(amount float64, origin, dest string, stops int) string {
	if amount <= 0 {
		return "Unavailable"
	}
	return fmt.Sprintf("%s -> %s, %s", origin, dest, formatStops(stops))
}

func tripCostHotelDetail(hotel trip.HotelCost, nights int) string {
	if hotel.Total <= 0 || hotel.PerNight <= 0 {
		return "Unavailable"
	}

	detail := ""
	if hotel.Name != "" {
		detail = hotel.Name + ", "
	}
	return detail + fmt.Sprintf("%s %.0f/night x %d nights", hotel.Currency, hotel.PerNight, nights)
}
