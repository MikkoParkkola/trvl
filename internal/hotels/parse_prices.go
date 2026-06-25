package hotels

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ParseHotelPriceResponse parses hotel price lookup results from a decoded
// batchexecute response for the yY52ce rpcid.
func ParseHotelPriceResponse(entries []any) ([]models.ProviderPrice, error) {
	payload, err := extractBatchPayload(entries, "yY52ce")
	if err != nil {
		return parsePricesFromRaw(entries)
	}

	return parsePricesFromPayload(payload)
}

// parsePricesFromPayload extracts provider prices from the yY52ce response.
func parsePricesFromPayload(payload any) ([]models.ProviderPrice, error) {
	var prices []models.ProviderPrice

	found := findPriceArrays(payload, 0)
	for _, p := range found {
		price := parseOneProvider(p)
		if price.Provider != "" && price.Price > 0 {
			prices = append(prices, price)
		}
	}

	if len(prices) == 0 {
		return nil, fmt.Errorf("no provider prices found in response")
	}

	return prices, nil
}

// findPriceArrays searches the response for arrays that look like provider
// price entries.
func findPriceArrays(v any, depth int) [][]any {
	if depth > 8 {
		return nil
	}

	arr, ok := v.([]any)
	if !ok {
		return nil
	}

	var results [][]any

	if looksLikePriceList(arr) {
		for _, item := range arr {
			if provArr, ok := item.([]any); ok && looksLikeProviderEntry(provArr) {
				results = append(results, provArr)
			}
		}
		if len(results) > 0 {
			return results
		}
	}

	for _, item := range arr {
		if subArr, ok := item.([]any); ok {
			found := findPriceArrays(subArr, depth+1)
			if len(found) > 0 {
				return found
			}
		}
	}

	return nil
}

func looksLikePriceList(arr []any) bool {
	if len(arr) < 1 {
		return false
	}
	provCount := 0
	for _, item := range arr {
		if subArr, ok := item.([]any); ok && looksLikeProviderEntry(subArr) {
			provCount++
		}
	}
	return provCount >= 1
}

func looksLikeProviderEntry(arr []any) bool {
	if len(arr) < 2 {
		return false
	}
	hasName := false
	hasPrice := false
	for _, v := range arr {
		switch val := v.(type) {
		case string:
			if len(val) > 2 && !strings.HasPrefix(val, "http") && !strings.HasPrefix(val, "/") {
				hasName = true
			}
		case float64:
			if val > 10 && val < 100000 {
				hasPrice = true
			}
		}
	}
	return hasName && hasPrice
}

func parseOneProvider(arr []any) models.ProviderPrice {
	p := models.ProviderPrice{}
	for _, v := range arr {
		switch val := v.(type) {
		case string:
			if p.Provider == "" && len(val) > 2 && !strings.HasPrefix(val, "http") && !strings.HasPrefix(val, "/") {
				p.Provider = val
			}
			if len(val) == 3 && val == strings.ToUpper(val) {
				p.Currency = val
			}
		case float64:
			if val > 10 && val < 100000 && p.Price == 0 {
				p.Price = val
			}
		case []any:
			for _, sub := range val {
				switch sv := sub.(type) {
				case float64:
					if sv > 10 && sv < 100000 && p.Price == 0 {
						p.Price = sv
					}
				case string:
					if len(sv) == 3 && sv == strings.ToUpper(sv) && p.Currency == "" {
						p.Currency = sv
					}
				}
			}
		}
	}
	// The yY52ce matrix is a list of booking-partner headline prices, not
	// specific room rates, so every parsed entry is a property lead-in. Tag it
	// honestly (lead_in / unverified) rather than leaving the trust fields empty:
	// partnersToRoomTypes promotes only the cheapest to room-level "similar", and
	// the price-trust sort must never treat an untagged Google partner price as a
	// verified room rate. (When Google's public RPC is unwalled it returns a null
	// payload, so in practice this guards the operator-relay / fixture paths.)
	if p.Provider != "" && p.Price > 0 {
		p.PriceBasis = models.PriceBasisLeadIn
		p.PriceConfidence = models.PriceConfidenceUnverified
	}
	return p
}

func parsePricesFromRaw(entries []any) ([]models.ProviderPrice, error) {
	var prices []models.ProviderPrice
	for _, entry := range entries {
		found := findPriceArrays(entry, 0)
		for _, p := range found {
			price := parseOneProvider(p)
			if price.Provider != "" && price.Price > 0 {
				prices = append(prices, price)
			}
		}
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("no provider prices found in raw response")
	}
	return prices, nil
}
