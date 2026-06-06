// Package serpapi provides a client for SerpAPI's Google Hotels engine.
//
// OVERVIEW
//
// SerpAPI is a third-party service that scrapes Google Hotels and returns
// structured JSON with real hotel prices. It handles anti-bot protection
// (CloudFlare, rate limiting, TLS fingerprinting) so you don't have to.
//
// WHY USE IT
//
// The standard 'trvl hotels' command scrapes Google directly and may return
// estimated or partial prices (e.g. without taxes, or for sold-out rooms).
// SerpAPI returns verified prices from multiple booking providers such as
// Booking.com, Expedia, Trivago, Hotels.com, etc. — with per-night AND
// total cost for your exact dates.
//
// SETUP
//
//  1. Sign up at https://serpapi.com (free: 250 searches/month, no card)
//  2. Copy your API key from the dashboard
//  3. Set the environment variable: export SERPAPI_KEY=your_key_here
//
// USAGE
//
//	result, err := serpapi.SearchHotels(ctx, "Naoussa, Paros", "2026-08-03", "2026-08-10", "EUR")
//	if err != nil { /* handle */ }
//	for _, h := range result.Properties {
//	    fmt.Printf("%s: %.0f/nt (total: %.0f)\n", h.Name, h.PricePerNight(), h.TotalPrice())
//	}
//
// PRICE FIELDS
//
// Each hotel in the response includes:
//   - PricePerNight(): lowest price per night (float)
//   - TotalPrice():    total for the entire stay (float)
//   - Prices[]:        breakdown by provider (Booking, Expedia, etc.)
//
// LIMITATIONS
//
//   - Requires a free SerpAPI account and API key.
//   - Free plan allows 250 searches/month.
//   - Results depend on Google Hotels availability for the given dates.
package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Hotel struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Link        string  `json:"link"`
	HotelClass  int     `json:"extracted_hotel_class"`
	Rating      float64 `json:"overall_rating"`
	Reviews     int     `json:"reviews"`
	Type        string  `json:"type"`

	RatePerNight struct {
		Lowest     string  `json:"lowest"`
		Extracted  float64 `json:"extracted_lowest"`
		BeforeFees float64 `json:"extracted_before_taxes_fees,omitempty"`
	} `json:"rate_per_night"`

	TotalRate struct {
		Lowest     string  `json:"lowest"`
		Extracted  float64 `json:"extracted_lowest"`
		BeforeFees float64 `json:"extracted_before_taxes_fees,omitempty"`
	} `json:"total_rate"`

	Prices []struct {
		Source string `json:"source"`
		RatePerNight struct {
			Lowest    string  `json:"lowest"`
			Extracted float64 `json:"extracted_lowest"`
		} `json:"rate_per_night"`
	} `json:"prices"`

	Images []struct {
		Thumbnail string `json:"thumbnail"`
	} `json:"images"`

	Amenities []string `json:"amenities"`
	FreeCancellation bool `json:"free_cancellation"`
}

type Response struct {
	SearchMetadata struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"search_metadata"`

	SearchParameters struct {
		Q           string `json:"q"`
		CheckIn     string `json:"check_in_date"`
		CheckOut    string `json:"check_out_date"`
		Currency    string `json:"currency"`
	} `json:"search_parameters"`

	Properties []Hotel `json:"properties"`
	Ads        []Hotel `json:"ads"`
}

func APIKey() string {
	return os.Getenv("SERPAPI_KEY")
}

// searchURL is overridable in tests.
var searchURL = "https://serpapi.com/search"

func SearchHotels(ctx context.Context, query, checkIn, checkOut, currency string) (*Response, error) {
	apiKey := APIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("SERPAPI_KEY not set")
	}

	u, _ := url.Parse(searchURL)
	q := u.Query()
	q.Set("engine", "google_hotels")
	q.Set("q", query)
	q.Set("check_in_date", checkIn)
	q.Set("check_out_date", checkOut)
	q.Set("currency", currency)
	q.Set("adults", "2")
	q.Set("api_key", apiKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("serpapi: HTTP %d", resp.StatusCode)
	}

	var result Response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.SearchMetadata.Status == "Error" {
		return nil, fmt.Errorf("serpapi: error status")
	}

	return &result, nil
}

func (h *Hotel) PricePerNight() float64 {
	if h.RatePerNight.Extracted > 0 {
		return h.RatePerNight.Extracted
	}
	return 0
}

func (h *Hotel) TotalPrice() float64 {
	if h.TotalRate.Extracted > 0 {
		return h.TotalRate.Extracted
	}
	// Total not available — the caller should compute from nights.
	return 0
}

func (h *Hotel) ProviderPrices(currency string) string {
	if len(h.Prices) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range h.Prices {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Source)
		b.WriteString(" ")
		if currency != "" {
			b.WriteString(currency)
		}
		b.WriteString(strconv.FormatFloat(p.RatePerNight.Extracted, 'f', 0, 64))
		b.WriteString("/nt")
	}
	return b.String()
}
