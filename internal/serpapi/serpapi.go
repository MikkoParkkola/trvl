package serpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
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

func SearchHotels(ctx context.Context, query, checkIn, checkOut, currency string) (*Response, error) {
	apiKey := APIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("SERPAPI_KEY not set")
	}

	u, _ := url.Parse("https://serpapi.com/search")
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
	// Fallback: prezzo a notte × numero notti (approssimato)
	return h.PricePerNight()
}

func (h *Hotel) ProviderPrices() string {
	if len(h.Prices) == 0 {
		return ""
	}
	var s string
	for i, p := range h.Prices {
		if i > 0 {
			s += ", "
		}
		price := strconv.FormatFloat(p.RatePerNight.Extracted, 'f', 0, 64)
		s += p.Source + " €" + price + "/nt"
	}
	return s
}
