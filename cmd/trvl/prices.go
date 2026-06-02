package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotelarb"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func pricesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prices <hotel_id>",
		Short: "Look up booking prices for a specific hotel",
		Long: `Get prices from multiple booking providers for a specific hotel.

The hotel_id is a Google place ID (e.g. "/g/11b6d4_v_4") returned by the
hotels search command.

Examples:
  trvl prices "/g/11b6d4_v_4" --checkin 2026-06-15 --checkout 2026-06-18
  trvl prices "ChIJy7MSZP0LkkYRZw2dDekQP78" --checkin 2026-06-15 --checkout 2026-06-18 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runPrices,
	}

	cmd.Flags().String("checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().String("checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().String("currency", "", "Currency code (e.g. EUR, USD). Empty = API default")
	cmd.Flags().String("location", "", "City or hotel name hint for fallback when no booking partner prices found")

	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")

	cmd.AddCommand(pricesHoldCmd(), pricesRebookCmd())

	return cmd
}

func runPrices(cmd *cobra.Command, args []string) error {
	hotelID := args[0]

	checkin, _ := cmd.Flags().GetString("checkin")
	checkout, _ := cmd.Flags().GetString("checkout")
	currency, _ := cmd.Flags().GetString("currency")
	location, _ := cmd.Flags().GetString("location")
	format, _ := cmd.Flags().GetString("format")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	result, err := hotels.GetHotelPricesWithOpts(ctx, hotels.HotelPriceOpts{
		HotelID:  hotelID,
		CheckIn:  checkin,
		CheckOut: checkout,
		Currency: currency,
		Location: location,
	})
	if err != nil {
		return fmt.Errorf("hotel prices: %w", err)
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, result)
	}

	return formatPricesTable(result)
}

func formatPricesTable(result *models.HotelPriceResult) error {
	if len(result.Providers) == 0 {
		fmt.Println("No prices found.")
		return nil
	}

	fmt.Printf("Prices for hotel %s (%s to %s):\n\n", result.HotelID, result.CheckIn, result.CheckOut)

	headers := []string{"Provider", "Price", "Currency"}
	rows := make([][]string, 0, len(result.Providers))
	for _, p := range result.Providers {
		rows = append(rows, []string{
			p.Provider,
			fmt.Sprintf("%.2f", p.Price),
			p.Currency,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}

func pricesHoldCmd() *cobra.Command {
	var (
		name       string
		location   string
		checkIn    string
		checkOut   string
		price      float64
		currency   string
		provider   string
		refundable bool
		guests     int
		bookingURL string
		notes      string
	)

	cmd := &cobra.Command{
		Use:   "hold <hotel_id>",
		Short: "Save an active hotel reservation for re-book checks",
		Long: `Save an existing refundable hotel reservation to ~/.trvl/active_holds.json.

The saved hold can later be checked with:
  trvl prices rebook <hold_id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := hotelarb.DefaultHoldStore()
			if err != nil {
				return err
			}
			if _, err := store.Load(); err != nil {
				return err
			}
			hotelID := args[0]
			hotelName := strings.TrimSpace(name)
			if hotelName == "" {
				hotelName = hotelID
			}
			id, err := store.Add(hotelarb.Hold{
				HotelID:       hotelID,
				HotelName:     hotelName,
				Location:      location,
				CheckIn:       checkIn,
				CheckOut:      checkOut,
				Guests:        guests,
				Provider:      provider,
				OriginalPrice: price,
				Currency:      strings.ToUpper(currency),
				Refundable:    refundable,
				BookingURL:    bookingURL,
				Notes:         notes,
			})
			if err != nil {
				return fmt.Errorf("save hold: %w", err)
			}
			fmt.Printf("Saved hotel hold %s: %s (%s to %s) at %.0f %s\n",
				id, hotelName, checkIn, checkOut, price, strings.ToUpper(currency))
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Hotel name (defaults to hotel_id)")
	cmd.Flags().StringVar(&location, "location", "", "City or location for fallback searches")
	cmd.Flags().StringVar(&checkIn, "checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().StringVar(&checkOut, "checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().Float64Var(&price, "price", 0, "Original held reservation price (required)")
	cmd.Flags().StringVar(&currency, "currency", "USD", "Currency code for the held price")
	cmd.Flags().StringVar(&provider, "provider", "", "Original booking provider")
	cmd.Flags().BoolVar(&refundable, "refundable", false, "Reservation can be cancelled/re-booked without penalty")
	cmd.Flags().IntVar(&guests, "guests", 2, "Number of guests")
	cmd.Flags().StringVar(&bookingURL, "booking-url", "", "Original booking URL")
	cmd.Flags().StringVar(&notes, "notes", "", "Free-form notes about cancellation deadline or room type")
	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")
	_ = cmd.MarkFlagRequired("price")
	return cmd
}

func pricesRebookCmd() *cobra.Command {
	var (
		minSavings      float64
		currentPrice    float64
		currentCurrency string
		currentProvider string
		currentURL      string
	)

	cmd := &cobra.Command{
		Use:   "rebook <hold_id>",
		Short: "Check whether an active hotel hold should be re-booked",
		Long: `Fetch the current hotel price for a saved hold and show a hold-current
vs. re-book-at-lower-price decision. TRVL never cancels or books for you; any
lower-price recommendation is gated behind manual confirmation.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := hotelarb.DefaultHoldStore()
			if err != nil {
				return err
			}
			if _, err := store.Load(); err != nil {
				return err
			}
			hold, ok := store.Get(args[0])
			if !ok {
				return fmt.Errorf("hold %s not found", args[0])
			}

			quote := hotelarb.PriceQuote{
				Price:      currentPrice,
				Currency:   strings.ToUpper(currentCurrency),
				Provider:   currentProvider,
				BookingURL: currentURL,
				CheckedAt:  time.Now().UTC(),
			}
			if quote.Price <= 0 {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				defer cancel()
				quote, err = fetchHoldCurrentQuote(ctx, hold)
				if err != nil {
					return err
				}
			}

			decision := hotelarb.EvaluateRebook(hold, quote, hotelarb.RebookOptions{MinSavings: minSavings})
			hold.LastSeenPrice = quote.Price
			hold.LastSeenAt = quote.CheckedAt
			if err := store.Update(hold); err != nil {
				return fmt.Errorf("update hold: %w", err)
			}
			if format == "json" {
				return models.FormatJSON(os.Stdout, decision)
			}
			printRebookDecision(decision)
			return nil
		},
	}

	cmd.Flags().Float64Var(&minSavings, "min-savings", 0, "Minimum absolute savings before recommending a manual re-book")
	cmd.Flags().Float64Var(&currentPrice, "current-price", 0, "Use this current price instead of fetching live prices")
	cmd.Flags().StringVar(&currentCurrency, "currency", "", "Currency for --current-price; defaults to the hold currency")
	cmd.Flags().StringVar(&currentProvider, "provider", "manual", "Provider label for --current-price")
	cmd.Flags().StringVar(&currentURL, "booking-url", "", "Booking URL for the current lower-price quote")
	return cmd
}

func fetchHoldCurrentQuote(ctx context.Context, hold hotelarb.Hold) (hotelarb.PriceQuote, error) {
	if hold.HotelID == "" {
		return hotelarb.PriceQuote{}, fmt.Errorf("hold has no hotel_id; pass --current-price for a manual re-book check")
	}
	result, err := hotels.GetHotelPrices(ctx, hold.HotelID, hold.CheckIn, hold.CheckOut, hold.Currency)
	if err != nil {
		return hotelarb.PriceQuote{}, fmt.Errorf("hotel prices: %w", err)
	}
	if len(result.Providers) == 0 {
		return hotelarb.PriceQuote{}, fmt.Errorf("no current provider prices found for hold %s", hold.ID)
	}
	cheapest := result.Providers[0]
	for _, provider := range result.Providers[1:] {
		if provider.Price > 0 && (cheapest.Price == 0 || provider.Price < cheapest.Price) {
			cheapest = provider
		}
	}
	return hotelarb.PriceQuote{
		Price:     cheapest.Price,
		Currency:  cheapest.Currency,
		Provider:  cheapest.Provider,
		CheckedAt: time.Now().UTC(),
	}, nil
}

func printRebookDecision(decision hotelarb.RebookDecision) {
	status := "HOLD"
	if decision.Action == hotelarb.ActionRebookLowerPrice {
		status = "REBOOK"
	}
	fmt.Printf("%s  %s\n", status, decision.HotelName)
	fmt.Printf("  Held:    %.0f %s\n", decision.OriginalPrice, decision.Currency)
	fmt.Printf("  Current: %.0f %s", decision.CurrentPrice, decision.Currency)
	if decision.Provider != "" {
		fmt.Printf(" via %s", decision.Provider)
	}
	fmt.Println()
	if decision.Savings > 0 {
		fmt.Printf("  Savings: %.0f %s (%.1f%%)\n", decision.Savings, decision.Currency, decision.SavingsPercent)
	}
	if decision.ManualConfirmRequired {
		fmt.Println("  Manual confirmation required before cancelling the existing reservation.")
	}
	if decision.BookingURL != "" {
		fmt.Printf("  Book: %s\n", decision.BookingURL)
	}
	if decision.Reason != "" {
		models.Summary(os.Stdout, decision.Reason)
	}
}
