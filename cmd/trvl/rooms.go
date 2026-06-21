package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/booking"
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
		// Smaller and island properties often have no Google Hotel ID in the
		// index, so room-level lookup can't resolve them. Don't fail silently —
		// point the traveller at the verification paths that do work there.
		area := location
		if area == "" {
			area = hotelQuery
		}
		return fmt.Errorf("hotel rooms: %w\n\n"+
			"No room-level data is available for %q. Smaller or island properties are often not in Google's hotel index. Try instead:\n"+
			"  trvl serpapi %q --checkin %s --checkout %s --currency %s   (detail-verified provider prices; needs SERPAPI_KEY)\n"+
			"  trvl hotels %q --checkin %s --checkout %s --format json    (find a Google place ID, then: trvl prices <id> ...)",
			err, hotelQuery, area, checkIn, checkOut, currency, area, checkIn, checkOut)
	}

	verdict := bookingReadinessForRooms(result)

	if format == "json" {
		out := struct {
			*hotels.RoomAvailability
			BookingReadiness string   `json:"booking_readiness,omitempty"`
			ReadinessReasons []string `json:"booking_readiness_reasons,omitempty"`
		}{
			RoomAvailability: result,
			BookingReadiness: string(verdict.Readiness),
			ReadinessReasons: verdict.Reasons,
		}
		return models.FormatJSON(os.Stdout, out)
	}

	if err := formatRoomsTable(result); err != nil {
		return err
	}
	fmt.Printf("\nBooking readiness: %s — %s\n", verdict.Label(), verdict.Summary())
	return nil
}

// bookingReadinessForRooms maps the signals available in a RoomAvailability
// result into a booking readiness verdict.
//
// Signal mapping:
//   - IdentityConfirmed: true when result.HotelID is non-empty (caller resolved
//     a Google place ID before fetching rooms).
//   - RefundabilityKnown: true when ANY room carries Refundable, FreeCancellation,
//     or CancellationPolicy evidence; nil (not known) when none do.
//   - Verified: true when any room PriceConfidence is "verified" or "room_level";
//     false when all rooms are explicitly "unverified"; nil when no confidence
//     signal is present.
//   - LinkStable: classified from a room's ProviderURL via
//     hotels.ClassifyLinkDurability — "stable" -> true, "expiring" -> false,
//     none present -> nil. With a stable booking link, "ready" is reachable
//     from rooms when refundability, identity, and verification also hold.
func bookingReadinessForRooms(result *hotels.RoomAvailability) booking.Verdict {
	var in booking.Input // all signals default to nil = not known

	if result.HotelID != "" {
		in.IdentityConfirmed = booking.True()
	}

	in.RefundabilityKnown = refundabilitySignal(result.Rooms)
	in.Verified = priceConfidenceSignal(result.Rooms)
	in.LinkStable = linkStableSignal(result.Rooms)

	return booking.Evaluate(in)
}

// linkStableSignal classifies the durability of room booking links: True when
// any room has a stable link, False when links exist but all are expiring/dead,
// nil when no link is present. A stable link is what lets "ready" be reached.
func linkStableSignal(rooms []hotels.RoomType) booking.Signal {
	sawExpiring := false
	for _, r := range rooms {
		urls := []string{r.ProviderURL}
		for _, opt := range r.InventoryOptions {
			urls = append(urls, opt.ProviderURL)
		}
		for _, u := range urls {
			switch hotels.ClassifyLinkDurability(u) {
			case "stable":
				return booking.True()
			case "expiring":
				sawExpiring = true
			}
		}
	}
	if sawExpiring {
		return booking.False()
	}
	return nil
}

// refundabilitySignal returns True when any room exposes a refundability
// signal, nil when none do. False is never returned: "known non-refundable"
// is still "known" per the booking.Input contract.
func refundabilitySignal(rooms []hotels.RoomType) booking.Signal {
	for _, r := range rooms {
		if r.Refundable != nil || r.FreeCancellation != nil || r.CancellationPolicy != "" {
			return booking.True()
		}
	}
	return nil
}

// priceConfidenceSignal returns True when any InventoryOption across all rooms
// carries "verified" or "room_level" PriceConfidence, False when all
// InventoryOptions are explicitly "unverified", or nil when no confidence
// signal is present. PriceConfidence lives on RoomInventoryQuote (inside
// room.InventoryOptions), not on RoomType itself.
func priceConfidenceSignal(rooms []hotels.RoomType) booking.Signal {
	hasAny := false
	allUnverified := true
	for _, r := range rooms {
		for _, opt := range r.InventoryOptions {
			switch opt.PriceConfidence {
			case models.PriceConfidenceVerified, models.PriceConfidenceRoomLevel:
				return booking.True()
			case models.PriceConfidenceUnverified:
				hasAny = true
				// allUnverified stays true
			default:
				if opt.PriceConfidence != "" {
					hasAny = true
					allUnverified = false
				}
			}
		}
	}
	if hasAny && allUnverified {
		return booking.False()
	}
	return nil
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
	if result.Notice != "" {
		fmt.Printf("Notice: %s\n\n", result.Notice)
	}

	headers := []string{"Room", "Price", "Guests", "Bed", "Board", "Cancellation", "Provider", "Amenities"}
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
			room.BedType,
			boardLabel(room),
			cancellationLabel(room),
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
		summary := fmt.Sprintf("Cheapest: %.0f %s (%s)", cheapest.Price, cheapest.Currency, cheapest.Name)
		if extras := roomHighlight(cheapest); extras != "" {
			summary += " — " + extras
		}
		models.Summary(os.Stdout, summary)
	}

	return nil
}

// boardLabel returns a short human-readable meal-plan label for a room, or an
// empty string when the provider did not surface board data. It never
// fabricates a value: an empty Board with no breakfast signal renders blank.
func boardLabel(room hotels.RoomType) string {
	if room.Board != "" {
		return prettyLabel(room.Board)
	}
	if room.BreakfastIncluded != nil {
		if *room.BreakfastIncluded {
			return "Breakfast included"
		}
		return "No breakfast"
	}
	return ""
}

// cancellationLabel returns a short human-readable cancellation label for a
// room, or an empty string when the provider did not surface cancellation
// data. Absent-safe: nil pointers and empty policy render blank.
func cancellationLabel(room hotels.RoomType) string {
	if room.CancellationPolicy != "" {
		return prettyLabel(room.CancellationPolicy)
	}
	if room.FreeCancellation != nil && *room.FreeCancellation {
		return "Free cancellation"
	}
	if room.Refundable != nil {
		if *room.Refundable {
			return "Refundable"
		}
		return "Non refundable"
	}
	return ""
}

// roomHighlight builds a compact one-line decision summary (board +
// cancellation) for the cheapest room, omitting absent fields entirely.
func roomHighlight(room hotels.RoomType) string {
	var parts []string
	if b := boardLabel(room); b != "" {
		parts = append(parts, b)
	}
	if c := cancellationLabel(room); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// prettyLabel converts a normalized snake_case enum value (e.g.
// "free_cancellation", "breakfast_included") into a human-readable,
// sentence-cased label ("Free cancellation", "Breakfast included").
func prettyLabel(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
