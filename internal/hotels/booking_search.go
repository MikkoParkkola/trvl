package hotels

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

var bookingSearchLimiter = rate.NewLimiter(rate.Every(3*time.Second), 1)

func defaultSearchBooking(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if err := bookingSearchLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("booking rate limiter: %w", err)
	}

	searchURL := buildBookingSearchURL(location, opts.CheckIn, opts.CheckOut, opts.Currency)
	body, err := fetchBookingPage(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("fetch booking search page: %w", err)
	}

	hotels := parseBookingSearchResults(body)

	// Apply client-side filters
	var filtered []models.HotelResult
	for _, h := range hotels {
		if opts.MaxPrice > 0 && h.Price > opts.MaxPrice {
			continue
		}
		if opts.MinRating > 0 && h.Rating < opts.MinRating {
			continue
		}
		filtered = append(filtered, h)
	}

	if len(filtered) == 0 && len(hotels) > 0 {
		slog.Debug("booking search: all hotels filtered out", "total", len(hotels), "location", location)
	}

	return filtered, nil
}

func buildBookingSearchURL(location, checkIn, checkOut, currency string) string {
	q := url.Values{}
	q.Set("ss", location)
	q.Set("checkin", checkIn)
	q.Set("checkout", checkOut)
	q.Set("selected_currency", currency)
	q.Set("order", "price")
	return "https://www.booking.com/searchresults.html?" + q.Encode()
}

func parseBookingSearchResults(body string) []models.HotelResult {
	// Try JSON-LD first (structured data embedded in the page)
	hotels := parseJSONLDHotels(body)
	if len(hotels) > 0 {
		return hotels
	}
	return nil
}

func parseJSONLDHotels(body string) []models.HotelResult {
	// Simplistic JSON-LD extraction for Hotel types.
	// Booking.com embeds schema.org/LodgingBusiness JSON-LD in search pages.
	// We look for "@type":"Hotel" or "@type":"LodgingBusiness" blocks and
	// extract name, price range, and URL.
	var results []models.HotelResult

	// Find JSON-LD script blocks
	idx := 0
	for {
		start := strings.Index(body[idx:], `"@type":"Hotel"`)
		if start < 0 {
			start = strings.Index(body[idx:], `"@type":"LodgingBusiness"`)
		}
		if start < 0 {
			break
		}
		idx += start

		// Try to extract name
		name := extractJSONField(body[idx:idx+500], `"name":"`, `"`)
		if name == "" {
			idx += 10
			continue
		}

		priceRange := extractJSONField(body[idx:idx+500], `"priceRange":"`, `"`)
		url := extractJSONField(body[idx:idx+500], `"url":"`, `"`)

		price := 0.0
		if priceRange != "" {
			// Extract first number from "€60 - €120" or similar
			price = parsePriceFromRange(priceRange)
		}

		results = append(results, models.HotelResult{
			Name:       name,
			Price:      price,
			BookingURL: url,
		})

		idx += 100 // move past this match
	}

	return results
}

func extractJSONField(s, prefix, terminator string) string {
	start := strings.Index(s, prefix)
	if start < 0 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(s[start:], terminator)
	if end < 0 {
		return ""
	}
	return s[start : start+end]
}

func parsePriceFromRange(pr string) float64 {
	// Extract first number from strings like "€60 - €120", "$100", "EUR 80"
	var numStr string
	for _, c := range pr {
		if c >= '0' && c <= '9' || c == '.' {
			numStr += string(c)
		} else if numStr != "" {
			break
		}
	}
	if numStr == "" {
		return 0
	}
	var result float64
	fmt.Sscanf(numStr, "%f", &result)
	return result
}
