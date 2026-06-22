package hotels

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// parseTrivagoSuggestions extracts the first location's ns and id from a
// trivago-search-suggestions response.
func parseTrivagoSuggestions(raw json.RawMessage) (trivagoLocationRef, error) {
	// Try structured suggestions wrapper (new API format).
	var result trivagoSuggestionsResult
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Suggestions) > 0 {
		s := result.Suggestions[0]
		if s.NS != 0 || s.ID != 0 {
			return trivagoLocationRef{NS: s.NS, ID: s.ID}, nil
		}
	}

	// Some responses embed suggestions in an outer object with a different key.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return trivagoLocationRef{}, fmt.Errorf("unexpected suggestions format")
	}

	// Walk possible key names.
	for _, key := range []string{"suggestions", "results", "data", "items"} {
		if v, ok := outer[key]; ok {
			var arr []trivagoSuggestionEntry
			if err := json.Unmarshal(v, &arr); err == nil && len(arr) > 0 {
				s := arr[0]
				if s.NS != 0 || s.ID != 0 {
					return trivagoLocationRef{NS: s.NS, ID: s.ID}, nil
				}
			}
			// Try as raw array and look for ns/id keys.
			var rawArr []map[string]json.RawMessage
			if err := json.Unmarshal(v, &rawArr); err == nil && len(rawArr) > 0 {
				ref, err := extractNSIDFromMap(rawArr[0])
				if err == nil {
					return ref, nil
				}
			}
		}
	}

	return trivagoLocationRef{}, fmt.Errorf("no location ns/id found in suggestions")
}

// extractNSIDFromMap attempts to pull ns and id from a generic JSON object.
func extractNSIDFromMap(m map[string]json.RawMessage) (trivagoLocationRef, error) {
	var ref trivagoLocationRef
	var found bool

	for _, nsKey := range []string{"ns", "NS"} {
		if v, ok := m[nsKey]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil {
				ref.NS = n
				found = true
				break
			}
		}
	}
	for _, idKey := range []string{"id", "ID"} {
		if v, ok := m[idKey]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil {
				ref.ID = n
				found = true
				break
			}
		}
	}

	if !found {
		return trivagoLocationRef{}, fmt.Errorf("ns/id not found")
	}
	return ref, nil
}

// parseTrivagoAccommodations maps a trivago-accommodation-search response to
// a slice of HotelResult values, each tagged with a "trivago" PriceSource.
func parseTrivagoAccommodations(raw json.RawMessage, currency string) ([]models.HotelResult, error) {
	// Try the typed struct first.
	var result trivagoAccomResult
	if err := json.Unmarshal(raw, &result); err == nil && len(result.Accommodations) > 0 {
		return mapTrivagoAccommodations(result.Accommodations, currency), nil
	}

	// Accommodations might be at a different key.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, fmt.Errorf("unexpected accommodations format")
	}

	for _, key := range []string{"accommodations", "hotels", "results", "data"} {
		if v, ok := outer[key]; ok {
			var accoms []trivagoAccommodation
			if err := json.Unmarshal(v, &accoms); err == nil {
				return mapTrivagoAccommodations(accoms, currency), nil
			}
		}
	}

	// Return empty list if we genuinely got no hotels (e.g. obscure location).
	return nil, nil
}

// sanitizeBookingURL returns rawURL if it has an http or https scheme, or ""
// if it is empty, has an unexpected scheme (e.g. javascript:, data:), or is
// not a valid URL. This prevents a malicious MCP response from injecting
// non-HTTP URLs into booking links that a client might follow.
func sanitizeBookingURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return ""
	}
	return rawURL
}

// trivagoParsePrice extracts a numeric value from a formatted price string
// like "€211" or "$150" or "£89". Delegates to the shared parsePriceString
// in parse.go, discarding the currency return (which is handled separately).
func trivagoParsePrice(s string) float64 {
	amount, _ := parsePriceString(s)
	return amount
}

// parseRatingString converts a rating string like "8.4" to a float on a
// 0-10 scale, then normalizes to 0-5 (dividing by 2) for consistency with
// the HotelResult model.
func parseRatingString(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	// Trivago ratings are on a 0-10 scale; normalize to 0-5.
	if v > 5 {
		v = v / 2
	}
	return v
}

// mapTrivagoAccommodations converts trivago API accommodation records to the
// canonical HotelResult model. Handles both the new API format (accommodation_name,
// price_per_night, review_rating) and the legacy format (name, price.amount,
// rating, bookingLinks).
func mapTrivagoAccommodations(accoms []trivagoAccommodation, defaultCurrency string) []models.HotelResult {
	results := make([]models.HotelResult, 0, len(accoms))

	for _, a := range accoms {
		// Determine hotel name — new API uses accommodation_name, legacy uses name.
		hotelName := a.AccommodationName
		if hotelName == "" {
			hotelName = a.Name
		}
		if hotelName == "" {
			continue
		}

		// Determine price — new API uses formatted price_per_night string,
		// legacy uses price.amount and bookingLinks.
		var price float64
		cur := defaultCurrency

		if a.PricePerNight != "" {
			// New API format: parse formatted string like "€211".
			price = trivagoParsePrice(a.PricePerNight)
			if a.Currency != "" {
				cur = a.Currency
			}
		} else {
			// Legacy format: numeric price object + booking links.
			price = a.Price.Amount
			if a.Price.Currency != "" {
				cur = a.Price.Currency
			}
			for _, link := range a.BookingLinks {
				if link.Price <= 0 {
					continue
				}
				if price == 0 || link.Price < price {
					price = link.Price
					if link.Currency != "" {
						cur = link.Currency
					}
				}
			}
		}

		// Determine rating.
		rating := a.Rating
		if rating == 0 && a.ReviewRating != "" {
			rating = parseRatingString(a.ReviewRating)
		}

		// Determine stars.
		stars := a.Stars
		if stars == 0 {
			stars = a.HotelRating
		}

		// Determine review count.
		reviewCount := int(a.ReviewCount)

		// Determine booking URL.
		bookingURL := sanitizeBookingURL(a.BookingURL)
		if bookingURL == "" {
			bookingURL = sanitizeBookingURL(a.AccommodationURL)
		}
		// Legacy: check bookingLinks for URL.
		if bookingURL == "" {
			for _, link := range a.BookingLinks {
				if u := sanitizeBookingURL(link.URL); u != "" {
					bookingURL = u
					break
				}
			}
		}

		// Determine address.
		address := a.Address
		if address == "" && a.CountryCity != "" {
			address = a.CountryCity
		}

		h := models.HotelResult{
			Name:        hotelName,
			Rating:      rating,
			ReviewCount: reviewCount,
			Stars:       stars,
			Price:       price,
			Currency:    strings.ToUpper(cur),
			Address:     address,
			Lat:         a.Lat,
			Lon:         a.Lon,
			BookingURL:  bookingURL,
			Sources: []models.PriceSource{{
				Provider:   "trivago",
				Price:      price,
				Currency:   strings.ToUpper(cur),
				BookingURL: bookingURL,
			}},
		}

		results = append(results, h)
	}

	return results
}
