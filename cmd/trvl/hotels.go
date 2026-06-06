package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/scoring"
	"github.com/spf13/cobra"
)

func hotelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hotels <location>",
		Short: "Search hotels by location",
		Long: `Search Google Hotels for a given location (city, address, or landmark).

Examples:
  trvl hotels "Helsinki" --checkin 2026-06-15 --checkout 2026-06-18
  trvl hotels "Tokyo" --checkin 2026-06-15 --checkout 2026-06-18 --guests 2 --stars 4
  trvl hotels "Paris" --checkin 2026-06-15 --checkout 2026-06-18 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runHotels,
	}

	cmd.Flags().String("checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().String("checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().Int("guests", 2, "Number of guests")
	cmd.Flags().Int("children", 0, "Number of children in the party (excludes adults-only properties when > 0)")
	cmd.Flags().Int("stars", 0, "Minimum star rating (0=any, 2-5)")
	cmd.Flags().String("sort", "cheapest", "Sort by: cheapest, rating, distance, stars")
	cmd.Flags().String("currency", "", "Target currency (e.g. EUR, USD). Empty = API default. Passed to Google if supported, otherwise converted")
	cmd.Flags().Float64("min-price", 0, "Minimum price per night")
	cmd.Flags().Float64("max-price", 0, "Maximum price per night")
	cmd.Flags().Float64("min-rating", 0, "Minimum guest rating on 0-10 scale (e.g. 8.0)")
	cmd.Flags().Float64("max-distance", 0, "Maximum distance from city center in km")
	cmd.Flags().String("amenities", "", "Filter by amenities (comma-separated, e.g. pool,wifi,breakfast)")
	cmd.Flags().Bool("free-cancellation", false, "Only show hotels with free cancellation")
	cmd.Flags().String("property-type", "", "Property type: hotel, apartment, hostel, resort, bnb, villa")
	cmd.Flags().String("brand", "", "Filter by hotel brand/chain name (e.g. hilton, marriott)")
	cmd.Flags().Bool("eco-certified", false, "Only show eco-certified/sustainable hotels")
	cmd.Flags().Int("min-bedrooms", 0, "Minimum bedrooms (Airbnb)")
	cmd.Flags().Int("min-bathrooms", 0, "Minimum bathrooms (Airbnb)")
	cmd.Flags().Int("min-beds", 0, "Minimum beds (Airbnb)")
	cmd.Flags().String("room-type", "", "Room type: entire_home, private_room, shared_room, hotel_room (Airbnb)")
	cmd.Flags().Bool("superhost", false, "Only show Superhost listings (Airbnb)")
	cmd.Flags().Bool("instant-book", false, "Only show instant-bookable listings (Airbnb)")
	cmd.Flags().Int("max-distance-m", 0, "Maximum distance from city center in meters (Booking)")
	cmd.Flags().Bool("sustainable", false, "Only show eco/sustainable properties (Booking)")
	cmd.Flags().Bool("meal-plan", false, "Only show properties with breakfast/meals included (Booking)")
	cmd.Flags().Bool("include-sold-out", false, "Include sold-out properties (Booking)")
	cmd.Flags().Bool("explain", false, "Show per-factor profile match breakdown for each result")

	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")

	return cmd
}

// initProviderRuntime wires external provider configs (Booking, Airbnb, etc.)
// into the hotel search pipeline. Safe to call multiple times; executes once.
var initProviderRuntime = sync.OnceFunc(func() {
	reg, err := providers.NewRegistry()
	if err != nil {
		return
	}
	if len(reg.List()) == 0 {
		return
	}
	hotels.SetExternalProviderRuntime(providers.NewRuntime(reg))
})

func runHotels(cmd *cobra.Command, args []string) error {
	initProviderRuntime()

	location := args[0]

	checkin, _ := cmd.Flags().GetString("checkin")
	checkout, _ := cmd.Flags().GetString("checkout")
	guests, _ := cmd.Flags().GetInt("guests")
	children, _ := cmd.Flags().GetInt("children")
	stars, _ := cmd.Flags().GetInt("stars")
	sortBy, _ := cmd.Flags().GetString("sort")
	currency, _ := cmd.Flags().GetString("currency")
	format, _ := cmd.Flags().GetString("format")
	minPrice, _ := cmd.Flags().GetFloat64("min-price")
	maxPrice, _ := cmd.Flags().GetFloat64("max-price")
	minRating, _ := cmd.Flags().GetFloat64("min-rating")
	maxDistance, _ := cmd.Flags().GetFloat64("max-distance")
	amenitiesStr, _ := cmd.Flags().GetString("amenities")
	freeCancellation, _ := cmd.Flags().GetBool("free-cancellation")
	propertyType, _ := cmd.Flags().GetString("property-type")
	brand, _ := cmd.Flags().GetString("brand")
	ecoCertified, _ := cmd.Flags().GetBool("eco-certified")
	minBedrooms, _ := cmd.Flags().GetInt("min-bedrooms")
	minBathrooms, _ := cmd.Flags().GetInt("min-bathrooms")
	minBeds, _ := cmd.Flags().GetInt("min-beds")
	roomType, _ := cmd.Flags().GetString("room-type")
	superhost, _ := cmd.Flags().GetBool("superhost")
	instantBook, _ := cmd.Flags().GetBool("instant-book")
	maxDistanceM, _ := cmd.Flags().GetInt("max-distance-m")
	sustainable, _ := cmd.Flags().GetBool("sustainable")
	mealPlan, _ := cmd.Flags().GetBool("meal-plan")
	includeSoldOut, _ := cmd.Flags().GetBool("include-sold-out")
	explain, _ := cmd.Flags().GetBool("explain")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = providers.WithInteractive(ctx) // allow browser escape hatch for WAF challenges

	// Load preferences and apply defaults where flags weren't explicitly set.
	prefs, _ := preferences.Load() // non-fatal; nil prefs means no defaults applied

	if currency == "" && prefs != nil {
		currency = prefs.DisplayCurrency
	}

	// Parse amenities filter: comma-separated, trimmed, lowercased.
	var amenities []string
	if amenitiesStr != "" {
		for _, a := range strings.Split(amenitiesStr, ",") {
			a = strings.ToLower(strings.TrimSpace(a))
			if a != "" {
				amenities = append(amenities, a)
			}
		}
	}

	opts := hotels.HotelSearchOptions{
		CheckIn:          checkin,
		CheckOut:         checkout,
		Guests:           guests,
		Stars:            stars,
		Sort:             sortBy,
		Currency:         currency,
		MinPrice:         minPrice,
		MaxPrice:         maxPrice,
		MinRating:        minRating,
		MaxDistanceKm:    maxDistance,
		Amenities:        amenities,
		FreeCancellation: freeCancellation,
		PropertyType:     propertyType,
		Brand:            brand,
		EcoCertified:     ecoCertified,
		MinBedrooms:      minBedrooms,
		MinBathrooms:     minBathrooms,
		MinBeds:          minBeds,
		RoomType:         roomType,
		Superhost:        superhost,
		InstantBook:      instantBook,
		MaxDistanceM:     maxDistanceM,
		Sustainable:      sustainable,
		MealPlan:         mealPlan,
		IncludeSoldOut:   includeSoldOut,
	}

	// Apply preference-based filters (only when not already set via flags).
	if prefs != nil {
		if opts.Stars == 0 && prefs.MinHotelStars > 0 {
			opts.Stars = prefs.MinHotelStars
		}
		if opts.MinRating == 0 && prefs.MinHotelRating > 0 {
			opts.MinRating = prefs.MinHotelRating
		}
	}

	result, err := hotels.SearchHotels(ctx, location, opts)
	if err == nil && prefs != nil {
		result.Hotels = preferences.FilterHotels(result.Hotels, location, prefs)
		result.Count = len(result.Hotels)
	}
	if err != nil {
		return fmt.Errorf("hotel search: %w", err)
	}

	// When the party includes children, never surface adults-only properties.
	if children > 0 && result != nil {
		var hidden int
		result.Hotels, hidden = excludeAdultsOnly(result.Hotels)
		if hidden > 0 {
			result.Count = len(result.Hotels)
			plural := "property"
			if hidden != 1 {
				plural = "properties"
			}
			fmt.Printf("Note: %d adults-only %s hidden (party includes children).\n", hidden, plural)
		}
	}

	// Cache best result for `trvl share --last`.
	if result != nil && len(result.Hotels) > 0 {
		h := result.Hotels[0]
		saveLastSearch(&LastSearch{
			Command:       "hotels",
			Destination:   location,
			DepartDate:    checkin,
			ReturnDate:    checkout,
			HotelPrice:    h.Price,
			HotelCurrency: h.Currency,
			HotelName:     h.Name,
			TotalPrice:    h.Price,
			TotalCurrency: h.Currency,
		})
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, result)
	}

	if err := formatHotelsTable(cmd.Context(), currency, location, result, explain); err != nil {
		return err
	}

	if openFlag && len(result.Hotels) > 0 && result.Hotels[0].BookingURL != "" {
		_ = openBrowser(result.Hotels[0].BookingURL)
	}

	// Auto-trigger: if the stay is >= 4 nights, silently check for an
	// accommodation split and print a tip when one is found.
	maybeShowAccomHackTip(cmd.Context(), location, checkin, checkout, currency, guests)
	return nil
}

func formatHotelsTable(ctx context.Context, targetCurrency, location string, result *models.HotelSearchResult, explain bool) error {
	if len(result.Hotels) == 0 {
		fmt.Println("No hotels found.")
		return nil
	}

	summary := fmt.Sprintf("Found %d hotels", result.Count)
	if result.TotalAvailable > result.Count {
		summary = fmt.Sprintf("Showing %d of %d hotels", result.Count, result.TotalAvailable)
	}
	models.Banner(os.Stdout, "🏨", "Hotels", summary)
	fmt.Println()

	// Convert prices if --currency specified and differs from API result.
	if targetCurrency != "" && len(result.Hotels) > 0 && result.Hotels[0].Currency != targetCurrency {
		for i := range result.Hotels {
			if result.Hotels[i].Price > 0 && result.Hotels[i].Currency != targetCurrency {
				converted, cur := destinations.ConvertCurrency(ctx, result.Hotels[i].Price, result.Hotels[i].Currency, targetCurrency)
				result.Hotels[i].Price = math.Round(converted)
				result.Hotels[i].Currency = cur
			}
		}
	}

	showSources := false
	for _, h := range result.Hotels {
		if sources := hotelSourceLabels(h); sources != "" && sources != "Google" {
			showSources = true
			break
		}
	}

	showSavings := false
	for _, h := range result.Hotels {
		if h.Savings > 0 {
			showSavings = true
			break
		}
	}

	headers := []string{"#", "Name", "Stars", "Rating", "Reviews", "Price"}
	if showSources {
		headers = append(headers, "Sources")
	}
	if showSavings {
		headers = append(headers, "Savings")
	}
	headers = append(headers, "Amenities")
	rows := make([][]string, 0, len(result.Hotels))
	var prices priceScale

	for _, h := range result.Hotels {
		prices = prices.With(h.Price)
	}
	for i, h := range result.Hotels {
		starsStr := ""
		if h.Stars > 0 {
			starsStr = fmt.Sprintf("%d", h.Stars)
		}
		ratingStr := ""
		if h.Rating > 0 {
			ratingStr = fmt.Sprintf("%.1f", h.Rating)
		}
		reviewsStr := ""
		if h.ReviewCount > 0 {
			reviewsStr = fmt.Sprintf("%d", h.ReviewCount)
		}
		priceStr := ""
		if h.Price > 0 {
			priceStr = prices.Apply(h.Price, fmt.Sprintf("%.0f %s", h.Price, h.Currency))
		}
		amenStr := strings.Join(h.Amenities, ", ")
		if h.AdultsOnly {
			if amenStr != "" {
				amenStr = "adults-only, " + amenStr
			} else {
				amenStr = "adults-only"
			}
		}
		if len(amenStr) > 40 {
			amenStr = amenStr[:37] + "..."
		}
		row := []string{fmt.Sprintf("%d", i+1), h.Name, starsStr, colorizeRating(h.Rating, ratingStr), reviewsStr, priceStr}
		if showSources {
			row = append(row, hotelSourceLabels(h))
		}
		if showSavings {
			savingsStr := ""
			if h.Savings > 0 {
				savingsStr = fmt.Sprintf("Save %.0f %s via %s", h.Savings, h.Currency, hotelSourceLabel(h.CheapestSource))
			}
			row = append(row, savingsStr)
		}
		row = append(row, amenStr)
		rows = append(rows, row)
	}

	models.FormatTable(os.Stdout, headers, rows)

	// Per-property links (photos + booking), numbered to the table's "#"
	// column, so every hotel shown is directly viewable and bookable.
	printHotelLinks(os.Stdout, result.Hotels, location)

	// Transparency: surface providers that errored or were disabled so the
	// user knows the listed prices may be incomplete (e.g. Booking.com — the
	// primary discount source — being unavailable). Additive: JSON output is
	// unchanged and already carries provider_statuses.
	if warn := formatProviderWarning(result.ProviderStatuses); warn != "" {
		fmt.Println()
		fmt.Println(warn)
	}

	// Summary
	if len(result.Hotels) > 0 {
		cheapest := result.Hotels[0]
		bestRated := result.Hotels[0]
		for _, h := range result.Hotels[1:] {
			if h.Price > 0 && (cheapest.Price == 0 || h.Price < cheapest.Price) {
				cheapest = h
			}
			if h.Rating > bestRated.Rating {
				bestRated = h
			}
		}
		parts := []string{}
		if cheapest.Price > 0 {
			parts = append(parts, fmt.Sprintf("Cheapest: %.0f %s (%s)", cheapest.Price, cheapest.Currency, cheapest.Name))
		}
		if bestRated.Rating > 0 {
			parts = append(parts, fmt.Sprintf("Top rated: %.1f (%s)", bestRated.Rating, bestRated.Name))
		}
		if len(parts) > 0 {
			models.Summary(os.Stdout, strings.Join(parts, " · "))
		}
	}

	// --explain: per-hotel profile match breakdown.
	if explain {
		fmt.Println()
		prefs, _ := preferences.Load()
		for i, h := range result.Hotels {
			matchScore, breakdown := scoring.ComputeProfileMatch(prefs, scoring.DiscoverInput{
				CityName:    location,
				HotelPrice:  h.Price,
				Total:       h.Price,
				HotelRating: h.Rating,
				HotelName:   h.Name,
			})
			printMatchBreakdown(fmt.Sprintf("#%d %s", i+1, truncateName(h.Name, 30)), matchScore, breakdown)
		}
	}

	return nil
}

// hotelSearchLinks builds per-property links so the user can view photos and
// book. Booking.com is listed first (captures the user's negotiated rate and
// shows full photo galleries); Google Hotels is the cross-check. Both are
// deterministic search-interface URLs keyed on the hotel name (+ location),
// not fabricated per-resource deep links.
func hotelSearchLinks(h models.HotelResult, location string) (booking, google string) {
	q := strings.TrimSpace(h.Name)
	loc := strings.TrimSpace(location)
	if loc != "" && !strings.Contains(strings.ToLower(q), strings.ToLower(loc)) {
		q = q + " " + loc
	}
	enc := url.QueryEscape(q)
	booking = "https://www.booking.com/searchresults.html?ss=" + enc
	google = "https://www.google.com/travel/search?q=" + enc
	return
}

// printHotelLinks lists per-property photo/booking links beneath the hotel
// table, numbered to match the "#" column. Always prints when hotels exist so
// every option is directly viewable; the direct image URL is included when the
// provider supplied one.
func printHotelLinks(w io.Writer, hotels []models.HotelResult, location string) {
	if len(hotels) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Links (photos & booking):")
	for i, h := range hotels {
		booking, google := hotelSearchLinks(h, location)
		_, _ = fmt.Fprintf(w, "  [%d] %s\n", i+1, h.Name)
		_, _ = fmt.Fprintf(w, "      Booking.com:   %s\n", booking)
		_, _ = fmt.Fprintf(w, "      Google Hotels: %s\n", google)
		if u := strings.TrimSpace(h.ImageURL); u != "" {
			_, _ = fmt.Fprintf(w, "      Photo:         %s\n", u)
		}
	}
}

// excludeAdultsOnly returns the hotels with every adults-only property
// removed, plus the count removed. The input slice is not mutated.
func excludeAdultsOnly(in []models.HotelResult) (kept []models.HotelResult, hidden int) {
	kept = make([]models.HotelResult, 0, len(in))
	for _, h := range in {
		if h.AdultsOnly {
			hidden++
			continue
		}
		kept = append(kept, h)
	}
	return kept, hidden
}

// formatProviderWarning builds a one-line transparency warning when any
// provider errored or was disabled, so the user knows the listed prices may be
// incomplete. Returns "" when every provider reported "ok" (or there are
// none), in which case the caller prints nothing. Pure function — no I/O — so
// it can be unit-tested with fabricated ProviderStatus structs.
func formatProviderWarning(statuses []models.ProviderStatus) string {
	total := len(statuses)
	if total == 0 {
		return ""
	}

	var down, hints []string
	for _, s := range statuses {
		if s.Status != "error" && s.Status != "disabled" {
			continue
		}
		label := s.Name
		if label == "" {
			label = s.ID
		}
		detail := truncateErr(s.Error)
		if detail == "" {
			detail = s.Status
		}
		down = append(down, fmt.Sprintf("%s: %s", label, detail))
		if s.FixHint != "" {
			hints = append(hints, fmt.Sprintf("%s — %s", label, s.FixHint))
		}
	}
	if len(down) == 0 {
		return ""
	}

	warn := fmt.Sprintf("%s %d of %d sources unavailable (%s) — listed prices may be incomplete.",
		models.Yellow("⚠"), len(down), total, strings.Join(down, ", "))
	if len(hints) > 0 {
		warn += "\n  Fix: " + strings.Join(hints, "; ")
	}
	return warn
}

// truncateErr trims provider error strings to a single short, single-line
// fragment suitable for inline display in the warning.
func truncateErr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const max = 60
	if len(s) > max {
		s = strings.TrimSpace(s[:max-1]) + "…"
	}
	return s
}

func hotelSourceLabels(h models.HotelResult) string {
	if len(h.Sources) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(h.Sources))
	labels := make([]string, 0, len(h.Sources))
	for _, src := range h.Sources {
		label := hotelSourceLabel(src.Provider)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}

func hotelSourceLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google_hotels":
		return "Google"
	case "trivago":
		return "Trivago"
	case "airbnb":
		return "Airbnb"
	case "booking":
		return "Booking"
	default:
		return strings.TrimSpace(provider)
	}
}

// maybeShowAccomHackTip checks whether the stay is >= 4 nights and, if so,
// runs an accommodation split search. When a saving is found it prints a
// one-line tip pointing to `trvl hacks-accom`.
func maybeShowAccomHackTip(ctx context.Context, city, checkIn, checkOut, currency string, guests int) {
	if checkIn == "" || checkOut == "" {
		return
	}

	// Quick night count without importing time directly.
	in, err1 := models.ParseDate(checkIn)
	out, err2 := models.ParseDate(checkOut)
	if err1 != nil || err2 != nil {
		return
	}
	nights := int(out.Sub(in).Hours() / 24)
	if nights < 4 {
		return
	}

	// Run split detection with a short timeout so it doesn't slow down the
	// main hotel search output.
	tipCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	detected := hacks.DetectAccommodationSplit(tipCtx, hacks.AccommodationSplitInput{
		City:      city,
		CheckIn:   checkIn,
		CheckOut:  checkOut,
		Currency:  currency,
		MaxSplits: 3,
		Guests:    guests,
	})

	if len(detected) > 0 {
		h := detected[0]
		cur := h.Currency
		if cur == "" {
			cur = "EUR"
		}
		fmt.Println()
		fmt.Printf("  %s Tip: split this %d-night stay across hotels — saves %s %.0f\n",
			models.Yellow("!"), nights, cur, h.Savings)
		fmt.Printf("    Run: trvl hacks-accom %q --checkin %s --checkout %s\n",
			city, checkIn, checkOut)
	}
}
