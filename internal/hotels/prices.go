package hotels

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// isNoProviderPricesError reports whether err is the routine "Google's
// payload contained no booking partner prices" outcome we want to surface
// gracefully. We match on the substring rather than a typed error so the
// behaviour stays robust if the parser layer is refactored.
func isNoProviderPricesError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no provider prices found")
}

// HotelPriceOpts configures a hotel price lookup with optional fallback search
// for hotels where Google's batchexecute RPC has no booking partner data.
type HotelPriceOpts struct {
	HotelID  string // Google place ID
	CheckIn  string // YYYY-MM-DD
	CheckOut string // YYYY-MM-DD
	Currency string // e.g. "EUR", "USD"
	Location string // optional city/hotel name hint for search-page fallback
}

// GetHotelPrices looks up booking provider prices for a specific hotel.
//
// The hotelID should be a Google place ID (e.g. "/g/11b6d4_v_4" or
// "ChIJ..."). These IDs are returned in hotel search results.
//
// When the internal batchexecute RPC returns no booking partner prices and
// a Location hint is provided, the function falls back to searching the
// Google Hotels search page. This ensures small hotels that don't participate
// in Google's booking feed still return a price.
//
// Dates should be in YYYY-MM-DD format.
func GetHotelPrices(ctx context.Context, hotelID string, checkIn, checkOut string, currency string) (*models.HotelPriceResult, error) {
	return GetHotelPricesWithOpts(ctx, HotelPriceOpts{
		HotelID:  hotelID,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: currency,
	})
}

// GetHotelPricesWithOpts is like GetHotelPrices but accepts a full
// HotelPriceOpts struct including an optional Location for fallback.
func GetHotelPricesWithOpts(ctx context.Context, opts HotelPriceOpts) (*models.HotelPriceResult, error) {
	if opts.HotelID == "" {
		return nil, fmt.Errorf("hotel ID is required")
	}
	if opts.CheckIn == "" || opts.CheckOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if opts.Currency == "" {
		opts.Currency = "USD"
	}

	checkInArr, err := parseDateArray(opts.CheckIn)
	if err != nil {
		return nil, fmt.Errorf("parse check-in date: %w", err)
	}
	checkOutArr, err := parseDateArray(opts.CheckOut)
	if err != nil {
		return nil, fmt.Errorf("parse check-out date: %w", err)
	}

	client := DefaultClient()
	encoded := batchexec.BuildHotelPricePayload(opts.HotelID, checkInArr, checkOutArr, opts.Currency)

	status, body, err := client.BatchExecute(ctx, encoded)
	if err != nil {
		return nil, fmt.Errorf("hotel price request: %w", err)
	}

	if status == 403 {
		return nil, batchexec.ErrBlocked
	}
	if status != 200 {
		return nil, fmt.Errorf("hotel price lookup returned status %d", status)
	}
	if len(body) < 50 {
		return nil, fmt.Errorf("hotel price lookup returned empty response")
	}

	entries, err := batchexec.DecodeBatchResponse(body)
	if err != nil {
		return nil, fmt.Errorf("decode hotel price response: %w", err)
	}

	providers, err := ParseHotelPriceResponse(entries)
	if err != nil {
		// "no provider prices found" is a routine outcome. Fall back to
		// the search-page price when we have a location hint.
		if isNoProviderPricesError(err) {
			fallback := tryPriceFallback(ctx, opts)
			if fallback != nil {
				return fallback, nil
			}
			return &models.HotelPriceResult{
				Success:   true,
				HotelID:   opts.HotelID,
				CheckIn:   opts.CheckIn,
				CheckOut:  opts.CheckOut,
				Providers: nil,
				Notice:    "no live booking partners returned prices for this hotel and date range",
			}, nil
		}
		return nil, fmt.Errorf("parse hotel prices: %w", err)
	}

	// Set currency on providers that don't have one.
	for i := range providers {
		if providers[i].Currency == "" {
			providers[i].Currency = opts.Currency
		}
	}

	return &models.HotelPriceResult{
		Success:   true,
		HotelID:   opts.HotelID,
		CheckIn:   opts.CheckIn,
		CheckOut:  opts.CheckOut,
		Providers: providers,
	}, nil
}

// tryPriceFallback searches the Google Hotels search page for the hotel
// when the batchexecute RPC has no booking partner data. Uses the same
// approach as trySearchPageFallback in rooms.go.
func tryPriceFallback(ctx context.Context, opts HotelPriceOpts) *models.HotelPriceResult {
	if opts.Location == "" {
		return nil
	}

	searchOpts := HotelSearchOptions{
		CheckIn:  opts.CheckIn,
		CheckOut: opts.CheckOut,
		Guests:   2,
		Currency: opts.Currency,
		MaxPages: 1,
	}

	client := DefaultClient()
	candidates := buildLocationCandidates(opts.Location)
	var result *models.HotelSearchResult
	for _, loc := range candidates {
		r, err := SearchHotelsWithClient(ctx, client, loc, searchOpts)
		if err == nil && len(r.Hotels) > 0 {
			result = r
			break
		}
	}
	if result == nil || len(result.Hotels) == 0 {
		return nil
	}

	// Try ID match first, then name match.
	var hotel *models.HotelResult
	for i := range result.Hotels {
		if result.Hotels[i].HotelID == opts.HotelID {
			hotel = &result.Hotels[i]
			break
		}
	}
	if hotel == nil {
		hotel = findBestNameMatch(result.Hotels, opts.Location)
	}
	if hotel == nil || hotel.Price <= 0 {
		return nil
	}

	cur := opts.Currency
	if cur == "" {
		cur = hotel.Currency
	}
	return &models.HotelPriceResult{
		Success:   true,
		HotelID:   opts.HotelID,
		CheckIn:   opts.CheckIn,
		CheckOut:  opts.CheckOut,
		Providers: []models.ProviderPrice{
			{
				Provider: "Google Hotels",
				Price:    hotel.Price,
				Currency: cur,
			},
		},
	}
}
