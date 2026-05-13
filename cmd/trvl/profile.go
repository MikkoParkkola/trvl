package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/profile"
	"github.com/spf13/cobra"
)

func profileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "View your travel profile (learned from booking history)",
		Long: `View and manage your traveller profile, derived from booking history.

Unlike preferences (what you want), the profile tracks patterns from what
you actually booked: airlines, routes, hotels, timing, and budget.

Examples:
  trvl profile                    # show current profile
  trvl profile add                # manually add a booking
  trvl profile summary            # one-page travel pattern summary
  trvl profile import-email       # instructions for email import`,
		RunE: runProfileShow,
	}

	cmd.AddCommand(profileAddCmd())
	cmd.AddCommand(profileSummaryCmd())
	cmd.AddCommand(profileImportEmailCmd())

	return cmd
}

// runProfileShow prints the current profile as formatted JSON.
func runProfileShow(_ *cobra.Command, _ []string) error {
	p, err := profile.Load()
	if err != nil {
		return fmt.Errorf("load profile: %w", err)
	}

	if len(p.Bookings) == 0 {
		fmt.Println("No booking history yet.")
		fmt.Println()
		fmt.Println("Add bookings with:  trvl profile add")
		fmt.Println("Or use the MCP tool: build_profile source=email")
		return nil
	}

	if format == "json" {
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	// Table format: human-readable summary.
	printProfileSummary(p)
	return nil
}

func profileAddCmd() *cobra.Command {
	var (
		bookingType string
		travelDate  string
		from        string
		to          string
		provider    string
		price       float64
		currency    string
		nights      int
		stars       int
		source      string
		reference   string
		notes       string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Manually add a booking to your profile",
		Long: `Add a booking record to your travel profile. The profile stats are
automatically rebuilt after each addition.

Examples:
  trvl profile add --type flight --provider KLM --from HEL --to AMS --price 189 --currency EUR --travel-date 2026-03-15
  trvl profile add --type hotel --provider "Marriott" --price 450 --currency EUR --nights 3 --stars 4 --travel-date 2026-03-15
  trvl profile add --type ground --provider FlixBus --from Prague --to Vienna --price 19 --currency EUR

Without flags, runs an interactive prompt.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no flags provided, run interactive mode.
			if bookingType == "" && provider == "" {
				return runProfileAddInteractive()
			}

			if bookingType == "" {
				return fmt.Errorf("--type is required (flight, hotel, ground, airbnb, hostel)")
			}
			if provider == "" {
				return fmt.Errorf("--provider is required")
			}
			if source == "" {
				source = "manual"
			}

			b := profile.Booking{
				Type:       bookingType,
				TravelDate: travelDate,
				From:       from,
				To:         to,
				Provider:   provider,
				Price:      price,
				Currency:   currency,
				Nights:     nights,
				Stars:      stars,
				Source:     source,
				Reference:  reference,
				Notes:      notes,
			}

			if err := profile.AddBooking(b); err != nil {
				return fmt.Errorf("add booking: %w", err)
			}

			fmt.Printf("Added %s booking: %s", b.Type, b.Provider)
			if b.From != "" && b.To != "" {
				fmt.Printf(" (%s -> %s)", b.From, b.To)
			}
			if b.Price > 0 {
				fmt.Printf(" %s %.0f", b.Currency, b.Price)
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVar(&bookingType, "type", "", "Booking type: flight, hotel, ground, airbnb, hostel")
	cmd.Flags().StringVar(&travelDate, "travel-date", "", "Travel/check-in date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&from, "from", "", "Origin (IATA code or city)")
	cmd.Flags().StringVar(&to, "to", "", "Destination (IATA code or city)")
	cmd.Flags().StringVar(&provider, "provider", "", "Airline, hotel, or transport company")
	cmd.Flags().Float64Var(&price, "price", 0, "Total price")
	cmd.Flags().StringVar(&currency, "currency", "EUR", "Currency code")
	cmd.Flags().IntVar(&nights, "nights", 0, "Number of nights (accommodation)")
	cmd.Flags().IntVar(&stars, "stars", 0, "Hotel stars (1-5)")
	cmd.Flags().StringVar(&source, "source", "manual", "Data source")
	cmd.Flags().StringVar(&reference, "reference", "", "Booking reference")
	cmd.Flags().StringVar(&notes, "notes", "", "Notes")

	return cmd
}

func runProfileAddInteractive() error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Add a booking to your travel profile")
	fmt.Println()

	bookingType := promptString(scanner, "Type (flight/hotel/ground/airbnb/hostel)", "flight")
	provider := promptString(scanner, "Provider (airline/hotel/company name)", "")
	if provider == "" {
		return fmt.Errorf("provider is required")
	}

	b := profile.Booking{
		Type:     bookingType,
		Provider: provider,
		Source:   "manual",
	}

	switch bookingType {
	case "flight":
		b.From = promptString(scanner, "From (IATA code)", "")
		b.To = promptString(scanner, "To (IATA code)", "")
	case "hotel", "airbnb", "hostel":
		b.To = promptString(scanner, "City", "")
		nightsStr := promptString(scanner, "Nights", "1")
		_, _ = fmt.Sscanf(nightsStr, "%d", &b.Nights)
		if bookingType == "hotel" {
			starsStr := promptString(scanner, "Stars (1-5, or 0 for unknown)", "0")
			_, _ = fmt.Sscanf(starsStr, "%d", &b.Stars)
		}
	case "ground":
		b.From = promptString(scanner, "From (city)", "")
		b.To = promptString(scanner, "To (city)", "")
	}

	b.TravelDate = promptString(scanner, "Travel date (YYYY-MM-DD)", "")

	priceStr := promptString(scanner, "Price", "0")
	_, _ = fmt.Sscanf(priceStr, "%f", &b.Price)
	b.Currency = promptString(scanner, "Currency", "EUR")

	b.Reference = promptString(scanner, "Reference (optional)", "")
	b.Notes = promptString(scanner, "Notes (optional)", "")

	if err := profile.AddBooking(b); err != nil {
		return fmt.Errorf("add booking: %w", err)
	}

	fmt.Println()
	fmt.Printf("Added %s booking: %s\n", b.Type, b.Provider)
	return nil
}

func profileSummaryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "summary",
		Short: "One-page travel pattern summary",
		RunE:  runProfileSummary,
	}
}

func runProfileSummary(_ *cobra.Command, _ []string) error {
	p, err := profile.Load()
	if err != nil {
		return fmt.Errorf("load profile: %w", err)
	}

	if len(p.Bookings) == 0 {
		fmt.Println("No booking history yet. Add bookings with: trvl profile add")
		return nil
	}

	// Rebuild to ensure fresh stats.
	p = profile.BuildProfile(p.Bookings)
	printProfileSummary(p)
	return nil
}

func printProfileSummary(p *profile.TravelProfile) {
	fmt.Println("Travel Profile")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println()

	fmt.Printf("  Trips: %d | Flights: %d | Hotel nights: %d\n", p.TotalTrips, p.TotalFlights, p.TotalHotelNights)
	fmt.Println()

	if len(p.TopAirlines) > 0 {
		fmt.Println("Airlines:")
		for _, a := range p.TopAirlines {
			name := a.Name
			if name == "" {
				name = a.Code
			}
			fmt.Printf("  %-20s %d flights\n", name, a.Flights)
		}
		if p.PreferredAlliance != "" {
			fmt.Printf("  Preferred alliance: %s\n", p.PreferredAlliance)
		}
		if p.AvgFlightPrice > 0 {
			fmt.Printf("  Average flight price: %.0f\n", p.AvgFlightPrice)
		}
		fmt.Println()
	}

	if len(p.TopRoutes) > 0 {
		fmt.Println("Top routes:")
		for _, r := range p.TopRoutes {
			fmt.Printf("  %s -> %s  %dx", r.From, r.To, r.Count)
			if r.AvgPrice > 0 {
				fmt.Printf("  (avg %.0f)", r.AvgPrice)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	if len(p.HomeDetected) > 0 {
		fmt.Printf("Home airport(s): %s\n", strings.Join(p.HomeDetected, ", "))
	}
	if len(p.TopDestinations) > 0 {
		fmt.Printf("Top destinations: %s\n", strings.Join(p.TopDestinations, ", "))
	}
	fmt.Println()

	if len(p.TopHotelChains) > 0 {
		fmt.Println("Hotels:")
		for _, h := range p.TopHotelChains {
			fmt.Printf("  %-20s %d nights\n", h.Name, h.Nights)
		}
		if p.AvgStarRating > 0 {
			fmt.Printf("  Average stars: %.1f\n", p.AvgStarRating)
		}
		if p.AvgNightlyRate > 0 {
			fmt.Printf("  Average rate: %.0f/night\n", p.AvgNightlyRate)
		}
		if p.PreferredType != "" {
			fmt.Printf("  Preferred type: %s\n", p.PreferredType)
		}
		fmt.Println()
	}

	if len(p.TopGroundModes) > 0 {
		fmt.Println("Ground transport:")
		for _, m := range p.TopGroundModes {
			fmt.Printf("  %-15s %dx\n", m.Mode, m.Count)
		}
		fmt.Println()
	}

	if p.AvgTripLength > 0 {
		fmt.Printf("Average trip length: %.1f days\n", p.AvgTripLength)
	}
	if len(p.PreferredDays) > 0 {
		fmt.Printf("Preferred departure days: %s\n", strings.Join(p.PreferredDays, ", "))
	}
	if p.AvgBookingLead > 0 {
		fmt.Printf("Books %d days ahead on average\n", p.AvgBookingLead)
	}
	fmt.Println()

	if p.BudgetTier != "" {
		fmt.Printf("Budget tier: %s\n", p.BudgetTier)
	}
	if p.AvgTripCost > 0 {
		fmt.Printf("Average trip cost: %.0f\n", p.AvgTripCost)
	}
}

func profileImportEmailCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import-email",
		Short: "Import bookings from email (via MCP/AI)",
		Long: `Scans Gmail for booking confirmations and imports them into your profile.

This command works best when used through the MCP server with an AI agent
that can access your Gmail. Run:

  trvl mcp

Then ask the AI: "Build my travel profile from email"

The AI will search your Gmail for booking confirmations from airlines,
hotels, and transport providers, extract the details, and add them to
your profile.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("Email import requires an AI agent with Gmail access.")
			fmt.Println()
			fmt.Println("Use the MCP server with an AI assistant:")
			fmt.Println("  1. Start: trvl mcp")
			fmt.Println("  2. Ask: \"Build my travel profile from email\"")
			fmt.Println()
			fmt.Println("The AI will search Gmail for booking confirmations and import them.")
			fmt.Println()
			fmt.Println("Supported providers: Finnair, KLM, Ryanair, easyJet, Norwegian,")
			fmt.Println("SAS, Lufthansa, British Airways, Air France, Booking.com, Airbnb,")
			fmt.Println("Marriott, Hilton, FlixBus, Eurostar, and many more.")
			return nil
		},
	}
}
