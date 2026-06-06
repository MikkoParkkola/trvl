package hotels

import (
	"context"
	"fmt"
	"html"
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

	hotels := parseBookingSearchResults(body, opts.Currency)

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

func parseBookingSearchResults(body, currency string) []models.HotelResult {
	// Try JSON-LD first (fast path for pages that include it)
	hotels := parseJSONLDHotels(body, currency)
	if len(hotels) > 0 {
		return hotels
	}
	// Fallback: extract from HTML property cards
	hotels = parseBookingHTMLHotels(body, currency)
	return hotels
}

func parseJSONLDHotels(body, currency string) []models.HotelResult {
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

		// Clamp the end index to avoid panics when near EOF.
		window := body[idx:]
		if len(window) > 600 {
			window = window[:600]
		}

		name := extractJSONField(window, `"name":"`, `"`)
		if name == "" {
			idx += 10
			continue
		}

		priceRange := extractJSONField(window, `"priceRange":"`, `"`)
		url := extractJSONField(window, `"url":"`, `"`)

		price := 0.0
		if priceRange != "" {
			// Extract first number from "€60 - €120" or similar
			price = parsePriceFromRange(priceRange)
		}

		results = append(results, models.HotelResult{
			Name:       name,
			Price:      price,
			Currency:   currency,
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

func parseBookingHTMLHotels(body, currency string) []models.HotelResult {
	var results []models.HotelResult
	seen := make(map[string]bool)

	cardMarker := `data-testid="property-card"`
	titleMarker := `data-testid="title"`
	priceMarker := `data-testid="price-and-discounted-price"`
	reviewMarker := `data-testid="review-score"`

	pos := 0
	for {
		cardStart := strings.Index(body[pos:], cardMarker)
		if cardStart < 0 {
			break
		}
		cardStart += pos

		nextCard := strings.Index(body[cardStart+50:], cardMarker)
		cardEnd := len(body)
		if nextCard >= 0 {
			cardEnd = cardStart + 50 + nextCard
		}
		card := body[cardStart:cardEnd]
		pos = cardEnd

		name := extractBookingField(card, titleMarker, `>`, `<`)
		if name == "" {
			continue
		}
		if strings.Contains(name, "<") {
			name = stripHTMLTags(name)
		}
		name = html.UnescapeString(name)
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		priceStr := extractBookingField(card, priceMarker, `>`, `<`)
		price := parsePriceFromHTML(priceStr)

		rating, reviewCount := extractBookingRating(card, reviewMarker)

		url := extractBookingURL(card, name)

		results = append(results, models.HotelResult{
			Name:        name,
			Price:       price,
			Currency:    currency,
			Rating:      rating,
			ReviewCount: reviewCount,
			BookingURL:  url,
		})
	}

	return results
}

func extractBookingField(body, marker, startTag, endTag string) string {
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	contentStart := strings.Index(body[start:], startTag)
	if contentStart < 0 {
		return ""
	}
	contentStart += start + len(startTag)
	contentEnd := strings.Index(body[contentStart:], endTag)
	if contentEnd < 0 {
		return ""
	}
	return body[contentStart : contentStart+contentEnd]
}

func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for i, c := range s {
		if c == '<' {
			inTag = true
			continue
		}
		if c == '>' {
			inTag = false
			continue
		}
		if !inTag {
			if c == '&' && strings.HasPrefix(s[i:], "&amp;") {
				result.WriteRune('&')
			} else if c != '&' {
				result.WriteRune(c)
			}
		}
	}
	return result.String()
}

func parsePriceFromHTML(priceStr string) float64 {
	var numStr string
	for _, c := range priceStr {
		if c >= '0' && c <= '9' || c == '.' || c == ',' {
			if c == ',' {
				numStr += "."
			} else {
				numStr += string(c)
			}
		}
	}
	if numStr == "" {
		return 0
	}
	var result float64
	fmt.Sscanf(numStr, "%f", &result)
	return result
}

func extractBookingRating(card, marker string) (rating float64, count int) {
	start := strings.Index(card, marker)
	if start < 0 {
		return 0, 0
	}

	end := start + 300
	if end > len(card) {
		end = len(card)
	}
	section := card[start:end]

	ratingStr := extractBookingField(section, `aria-label="Scored `, `"`, `"`)
	if ratingStr == "" {
		ratingStr = extractBookingField(section, `aria-label="`, `"`, `"`)
	}
	var numStr string
	for _, c := range ratingStr {
		if c >= '0' && c <= '9' || c == '.' {
			numStr += string(c)
		}
	}
	if numStr != "" {
		fmt.Sscanf(numStr, "%f", &rating)
	}

	end2 := start + 400
	if end2 > len(card) {
		end2 = len(card)
	}
	reviewSection := card[start:end2]
	var numStr2 string
	for _, c := range reviewSection {
		if c >= '0' && c <= '9' {
			numStr2 += string(c)
		} else if numStr2 != "" {
			if len(numStr2) > 2 {
				fmt.Sscanf(numStr2, "%d", &count)
			}
			break
		}
	}

	return rating, count
}

func extractBookingURL(card, name string) string {
	hrefMarker := `<a href="`
	start := strings.Index(card, hrefMarker)
	if start < 0 {
		return ""
	}
	start += len(hrefMarker)
	end := strings.Index(card[start:], `"`)
	if end < 0 {
		return ""
	}
	href := card[start : start+end]
	if strings.HasPrefix(href, "/") {
		return "https://www.booking.com" + href
	}
	if strings.HasPrefix(href, "http") {
		return href
	}
	return ""
}
