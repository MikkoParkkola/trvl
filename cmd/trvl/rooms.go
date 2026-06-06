package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
)

func roomsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rooms <hotel_name_or_id>",
		Short: "Look up room-level prices for a hotel",
		Long: `Get room-type availability and pricing for a specific hotel.

You can pass either a Google hotel ID from search results or a hotel name.

Examples:
  trvl rooms "/g/11b6d4_v_4" --checkin 2026-06-15 --checkout 2026-06-18
  trvl rooms "Hotel Lutetia Paris" --checkin 2026-06-15 --checkout 2026-06-18 --currency EUR
  trvl rooms "The Hoxton, Barcelona" --checkin 2026-06-15 --checkout 2026-06-18 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runRooms,
	}

	cmd.Flags().String("checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().String("checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().String("currency", "USD", "Currency code (e.g. EUR, USD)")
	cmd.Flags().String("location", "", "City or area hint for raw hotel ID lookups (e.g. Paris)")

	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")

	return cmd
}

func runRooms(cmd *cobra.Command, args []string) error {
	hotelQuery := args[0]

	checkIn, _ := cmd.Flags().GetString("checkin")
	checkOut, _ := cmd.Flags().GetString("checkout")
	currency, _ := cmd.Flags().GetString("currency")
	location, _ := cmd.Flags().GetString("location")
	format, _ := cmd.Flags().GetString("format")

	if err := models.ValidateDateRange(checkIn, checkOut); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	result, err := resolveRoomAvailability(ctx, hotelQuery, checkIn, checkOut, currency, location)
	if err != nil {
		return fmt.Errorf("hotel rooms: %w", err)
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, result)
	}

	return formatRoomsTable(result)
}

func resolveRoomAvailability(ctx context.Context, hotelQuery, checkIn, checkOut, currency, location string) (*hotels.RoomAvailability, error) {
	if looksLikeGoogleHotelID(hotelQuery) {
		// Direct ID lookup. Pass any caller-provided location hint so the
		// search-page fallback can fire when the entity page has deferred data.
		// If no hint is provided, tryEntityPage will attempt to extract one from
		// the page itself before falling back.
		opts := hotels.RoomSearchOptions{
			HotelID:  hotelQuery,
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Currency: currency,
			Location: location,
		}
		return hotels.GetRoomAvailabilityWithOpts(ctx, opts)
	}

	// If --location is provided, append it to the query so SearchHotelByName
	// uses it as the search area instead of trying to infer location from
	// the hotel name alone (which fails for generic names like "Lemon Grove
	// Hotel" that match hotels in different cities).
	searchQuery := hotelQuery
	if location != "" && !strings.Contains(strings.ToLower(hotelQuery), strings.ToLower(location)) {
		searchQuery = hotelQuery + ", " + location
	}

	hotel, err := hotels.SearchHotelByName(ctx, searchQuery, checkIn, checkOut, currency)
	if err != nil {
		return nil, fmt.Errorf("hotel lookup for %q: %w", hotelQuery, err)
	}
	if hotel.HotelID == "" {
		return nil, fmt.Errorf("hotel %q found (%s) but has no Google ID", hotelQuery, hotel.Name)
	}

	// Pass the search query (name + location) as a location hint so the
	// search-page fallback can find the hotel when the entity page has
	// deferred data. Use searchQuery when available (includes --location),
	// fall back to the original hotelQuery.
	hint := hotelQuery
	if searchQuery != hotelQuery {
		hint = searchQuery
	}
	opts := hotels.RoomSearchOptions{
		HotelID:  hotel.HotelID,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: currency,
		Location: hint,
	}
	result, err := hotels.GetRoomAvailabilityWithOpts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("room availability for %s: %w", hotel.Name, err)
	}
	if result.Name == "" {
		result.Name = hotel.Name
	}
	return result, nil
}

func looksLikeGoogleHotelID(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/g/") {
		return true
	}

	upper := strings.ToUpper(value)
	if strings.HasPrefix(upper, "CHIJ") {
		return true
	}

	return strings.Count(value, ":") == 1 && !strings.ContainsAny(value, " \t")
}

func formatRoomsTable(result *hotels.RoomAvailability) error {
	name := result.Name
	if name == "" {
		name = result.HotelID
	}

	if len(result.Rooms) == 0 {
		fmt.Printf("No room types found for %s.\n", name)
		return nil
	}

	models.Banner(os.Stdout, "🛏️", "Rooms", fmt.Sprintf("%s · %s to %s", name, result.CheckIn, result.CheckOut))
	fmt.Println()

	headers := []string{"Room", "Price", "Guests", "Provider", "Amenities"}
	rows := make([][]string, 0, len(result.Rooms))
	var prices priceScale

	for _, room := range result.Rooms {
		prices = prices.With(room.Price)
	}

	for _, room := range result.Rooms {
		priceText := ""
		if room.Price > 0 {
			priceText = prices.Apply(room.Price, fmt.Sprintf("%.0f %s", room.Price, room.Currency))
		}

		guestsText := ""
		if room.MaxGuests > 0 {
			guestsText = fmt.Sprintf("%d", room.MaxGuests)
		}

		amenitiesText := strings.Join(room.Amenities, ", ")
		if len(amenitiesText) > 40 {
			amenitiesText = amenitiesText[:37] + "..."
		}

		rows = append(rows, []string{
			room.Name,
			priceText,
			guestsText,
			room.Provider,
			amenitiesText,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)

	cheapest := result.Rooms[0]
	for _, room := range result.Rooms[1:] {
		if room.Price > 0 && (cheapest.Price == 0 || room.Price < cheapest.Price) {
			cheapest = room
		}
	}
	if cheapest.Price > 0 {
		models.Summary(os.Stdout, fmt.Sprintf("Cheapest: %.0f %s (%s)", cheapest.Price, cheapest.Currency, cheapest.Name))
	}

	return nil
}
