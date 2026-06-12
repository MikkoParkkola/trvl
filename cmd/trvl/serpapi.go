package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
	"github.com/spf13/cobra"
)

func serpapiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serpapi <location>",
		Short: "Search hotels via SerpAPI with detail-verified provider totals",
		Long: `Search hotels using SerpAPI (google_hotels engine).

SerpAPI is a third-party service that scrapes Google Hotels and returns structured
JSON with real prices from multiple booking providers (Booking.com, Expedia, Trivago, etc.).

WHY USE IT:
  The standard 'trvl hotels' command scrapes Google Hotels directly and may return
  inaccurate or partial prices. SerpAPI handles the anti-bot protection. trvl then
  verifies the top list results through SerpAPI's property details endpoint so
  provider totals can replace list-level teaser prices when available.

SETUP:
  1. Sign up for a free account at https://serpapi.com (250 searches/month, no credit card)
  2. Copy your API key from the dashboard
  3. Export it: export SERPAPI_KEY=your_key_here
  4. Or add to ~/.zshrc: echo 'export SERPAPI_KEY=your_key_here' >> ~/.zshrc

DIFFERENCES FROM 'trvl hotels':
  - trvl hotels:  free, no API key, may show estimated prices
  - trvl serpapi: requires free API key, verifies provider totals for top candidates

Examples:
  trvl serpapi "Naoussa, Paros" --checkin 2026-08-03 --checkout 2026-08-10 --currency EUR
  trvl serpapi "Rhodes Greece" --checkin 2026-08-05 --checkout 2026-08-12 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: runSerpapi,
	}

	cmd.Flags().String("checkin", "", "Check-in date (YYYY-MM-DD, required)")
	cmd.Flags().String("checkout", "", "Check-out date (YYYY-MM-DD, required)")
	cmd.Flags().String("currency", "EUR", "Currency code (EUR, USD, etc.)")
	cmd.Flags().String("format", "", "Output format: json or table")
	cmd.Flags().Int("adults", 2, "Number of adult guests")
	cmd.Flags().Int("children", 0, "Number of children; prefer --children-ages for occupancy-aware pricing")
	cmd.Flags().String("children-ages", "", "Comma-separated child ages, e.g. 7,9")
	cmd.Flags().String("gl", "us", "Google country code for SerpAPI localization")
	cmd.Flags().String("hl", "en", "Google UI language for SerpAPI localization")
	cmd.Flags().String("sort-by", "", "SerpAPI sort: price/lowest_price/3, rating/highest_rating/8, reviews/most_reviewed/13")
	cmd.Flags().Float64("min-price", 0, "Minimum Google Hotels price filter")
	cmd.Flags().Float64("max-price", 0, "Maximum Google Hotels price filter")
	cmd.Flags().Float64("min-rating", 0, "Minimum guest rating: 3.5, 4.0, 4.5, or raw SerpAPI code 7/8/9")
	cmd.Flags().String("property-types", "", "Comma-separated Google Hotels property type IDs")
	cmd.Flags().String("amenities", "", "Comma-separated Google Hotels amenity IDs")
	cmd.Flags().String("brands", "", "Comma-separated Google Hotels brand IDs from a prior response")
	cmd.Flags().String("hotel-class", "", "Comma-separated hotel classes: 2,3,4,5")
	cmd.Flags().Bool("free-cancellation", false, "Only show hotel results offering free cancellation")
	cmd.Flags().Bool("special-offers", false, "Only show hotel results with special offers")
	cmd.Flags().Bool("eco-certified", false, "Only show eco-certified hotel results")
	cmd.Flags().Bool("vacation-rentals", false, "Search Google Vacation Rentals instead of hotels")
	cmd.Flags().Int("bedrooms", 0, "Minimum bedrooms for vacation rentals")
	cmd.Flags().Int("bathrooms", 0, "Minimum bathrooms for vacation rentals")
	cmd.Flags().String("next-page-token", "", "SerpAPI next_page_token for retrieving a follow-up page")
	cmd.Flags().Bool("no-cache", false, "Bypass local and SerpAPI caches; forces fresh Google Hotels results")
	cmd.Flags().Int("max-details", 8, "Maximum properties to verify through SerpAPI property details; each uncached detail can cost one API call")
	cmd.Flags().Bool("list-only", false, "Only return Google Hotels list results; saves property-detail API calls")

	_ = cmd.MarkFlagRequired("checkin")
	_ = cmd.MarkFlagRequired("checkout")

	return cmd
}

func runSerpapi(cmd *cobra.Command, args []string) error {
	location := args[0]
	checkIn, _ := cmd.Flags().GetString("checkin")
	checkOut, _ := cmd.Flags().GetString("checkout")
	currency, _ := cmd.Flags().GetString("currency")
	format, _ := cmd.Flags().GetString("format")
	adults, _ := cmd.Flags().GetInt("adults")
	children, _ := cmd.Flags().GetInt("children")
	childrenAgesRaw, _ := cmd.Flags().GetString("children-ages")
	gl, _ := cmd.Flags().GetString("gl")
	hl, _ := cmd.Flags().GetString("hl")
	sortBy, _ := cmd.Flags().GetString("sort-by")
	minPrice, _ := cmd.Flags().GetFloat64("min-price")
	maxPrice, _ := cmd.Flags().GetFloat64("max-price")
	minRating, _ := cmd.Flags().GetFloat64("min-rating")
	propertyTypesRaw, _ := cmd.Flags().GetString("property-types")
	amenitiesRaw, _ := cmd.Flags().GetString("amenities")
	brandsRaw, _ := cmd.Flags().GetString("brands")
	hotelClassRaw, _ := cmd.Flags().GetString("hotel-class")
	freeCancellation, _ := cmd.Flags().GetBool("free-cancellation")
	specialOffers, _ := cmd.Flags().GetBool("special-offers")
	ecoCertified, _ := cmd.Flags().GetBool("eco-certified")
	vacationRentals, _ := cmd.Flags().GetBool("vacation-rentals")
	bedrooms, _ := cmd.Flags().GetInt("bedrooms")
	bathrooms, _ := cmd.Flags().GetInt("bathrooms")
	nextPageToken, _ := cmd.Flags().GetString("next-page-token")
	noCache, _ := cmd.Flags().GetBool("no-cache")
	maxDetails, _ := cmd.Flags().GetInt("max-details")
	listOnly, _ := cmd.Flags().GetBool("list-only")

	childrenAges, err := parseSerpapiIntCSV(childrenAgesRaw, "children-ages")
	if err != nil {
		return err
	}
	if children > 0 && len(childrenAges) > 0 && children != len(childrenAges) {
		return fmt.Errorf("--children must match --children-ages count")
	}
	hotelClasses, err := parseSerpapiIntCSV(hotelClassRaw, "hotel-class")
	if err != nil {
		return err
	}
	rating, err := serpapiRatingParam(minRating)
	if err != nil {
		return err
	}

	if serpapi.APIKey() == "" {
		return fmt.Errorf("SERPAPI_KEY environment variable not set.\nGet a free key at https://serpapi.com (250 searches/month free)\nThen: export SERPAPI_KEY=your_key_here")
	}

	ctx, cancel := context.WithTimeout(context.Background(), serpapiTimeout(maxDetails, listOnly))
	defer cancel()

	opts := serpapi.SearchOptions{
		Query:            location,
		CheckIn:          checkIn,
		CheckOut:         checkOut,
		Currency:         currency,
		Adults:           adults,
		Children:         children,
		GL:               gl,
		HL:               hl,
		ChildrenAges:     childrenAges,
		SortBy:           sortBy,
		MinPrice:         minPrice,
		MaxPrice:         maxPrice,
		PropertyTypes:    parseSerpapiCSV(propertyTypesRaw),
		Amenities:        parseSerpapiCSV(amenitiesRaw),
		Rating:           rating,
		Brands:           parseSerpapiCSV(brandsRaw),
		HotelClasses:     hotelClasses,
		FreeCancellation: freeCancellation,
		SpecialOffers:    specialOffers,
		EcoCertified:     ecoCertified,
		VacationRentals:  vacationRentals,
		MinBedrooms:      bedrooms,
		MinBathrooms:     bathrooms,
		NextPageToken:    strings.TrimSpace(nextPageToken),
		NoCache:          noCache,
		MaxDetails:       maxDetails,
	}
	var result *serpapi.Response
	if listOnly {
		result, err = serpapi.SearchHotelsWithOptions(ctx, opts)
	} else {
		result, err = serpapi.SearchHotelsVerified(ctx, opts)
	}
	if err != nil {
		return fmt.Errorf("serpapi search: %w", err)
	}

	allHotels := append(result.Properties, result.Ads...)
	if len(allHotels) == 0 {
		fmt.Println("No hotels found.")
		return nil
	}

	if format == "json" {
		return models.FormatJSON(os.Stdout, result)
	}

	// Table output
	models.Banner(os.Stdout, "🏨", "Hotels", fmt.Sprintf("%s · %s to %s", location, checkIn, checkOut))
	fmt.Println()

	headers := []string{"Hotel", "Class", "Rating", "€/nt", "Totale", "Provider", "Status"}
	rows := make([][]string, 0, len(allHotels))
	unverifiedCount := 0
	for _, h := range allHotels {
		if h.Name == "" {
			continue
		}
		class := ""
		if h.HotelClass > 0 {
			class = fmt.Sprintf("%d★", h.HotelClass)
		}
		rating := ""
		if h.Rating > 0 {
			rating = fmt.Sprintf("%.1f⭐", h.Rating)
		}
		pn := ""
		if h.PricePerNight() > 0 {
			pn = fmt.Sprintf("%.0f", h.PricePerNight())
		}
		total := ""
		if h.TotalPrice() > 0 {
			total = fmt.Sprintf("%.0f", h.TotalPrice())
		}
		providers := providerSummary(h, currency, 3)
		if providers == "" {
			providers = "—"
		}
		status := serpapiPriceStatus(h)
		if status != "detail_verified" {
			unverifiedCount++
		}
		rows = append(rows, []string{h.Name, class, rating, pn, total, providers, status})
	}

	models.FormatTable(os.Stdout, headers, rows)

	if len(result.Properties) > 0 {
		cheapest := result.Properties[0]
		for _, h := range result.Properties[1:] {
			if h.TotalPrice() > 0 && (cheapest.TotalPrice() == 0 || h.TotalPrice() < cheapest.TotalPrice()) {
				cheapest = h
			}
		}
		if cheapest.TotalPrice() > 0 {
			label := "Lowest verified"
			if serpapiPriceStatus(cheapest) != "detail_verified" {
				label = "Lowest list candidate"
			}
			models.Summary(os.Stdout, fmt.Sprintf("%s: %s — %.0f %s total (%.0f/nt)", label, cheapest.Name, cheapest.TotalPrice(), currency, cheapest.PricePerNight()))
		}
	}
	if unverifiedCount > 0 {
		models.Summary(os.Stdout, fmt.Sprintf("%d result%s lack detail-verified provider totals; treat those list prices as candidates only.", unverifiedCount, serpapiPluralSuffix(unverifiedCount)))
	}

	return nil
}

func providerSummary(h serpapi.Hotel, currency string, limit int) string {
	options := h.ProviderOptions()
	if len(options) == 0 {
		return ""
	}
	out := ""
	for i, p := range options {
		if i >= limit {
			out += "..."
			break
		}
		if out != "" {
			out += ", "
		}
		price := p.TotalRate.Extracted
		suffix := " total"
		if price <= 0 {
			price = p.RatePerNight.Extracted
			suffix = "/nt"
		}
		out += fmt.Sprintf("%s: %s%.0f%s", p.Source, currency, price, suffix)
	}
	return out
}

func serpapiPriceStatus(h serpapi.Hotel) string {
	if h.PriceVerification != nil && h.PriceVerification.Status != "" {
		return h.PriceVerification.Status
	}
	if len(h.ProviderOptions()) > 0 {
		return "provider_prices_present"
	}
	return "list_only_unverified"
}

func serpapiPluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func serpapiTimeout(maxDetails int, listOnly bool) time.Duration {
	if listOnly {
		return 30 * time.Second
	}
	if maxDetails <= 0 {
		maxDetails = 8
	}
	timeout := time.Duration(30+maxDetails*10) * time.Second
	if timeout > 2*time.Minute {
		return 2 * time.Minute
	}
	return timeout
}

func parseSerpapiCSV(raw string) []string {
	var values []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func parseSerpapiIntCSV(raw, flag string) ([]int, error) {
	parts := parseSerpapiCSV(raw)
	values := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("--%s must contain integers: %q", flag, part)
		}
		values = append(values, value)
	}
	return values, nil
}

func serpapiRatingParam(value float64) (string, error) {
	if value <= 0 {
		return "", nil
	}
	if value == 7 || value == 8 || value == 9 {
		return strconv.Itoa(int(value)), nil
	}
	switch {
	case value >= 4.5 && value <= 5:
		return "9", nil
	case value >= 4.0 && value < 4.5:
		return "8", nil
	case value >= 3.5 && value < 4.0:
		return "7", nil
	default:
		return "", fmt.Errorf("--min-rating must be 3.5, 4.0, 4.5, or raw SerpAPI code 7/8/9")
	}
}
