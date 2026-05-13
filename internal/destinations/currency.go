package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const exchangeRateURL = "https://api.exchangerate-api.com/v4/latest/EUR"

// currencyCache stores the full exchange rate table.
var currencyCache = struct {
	sync.RWMutex
	rates   map[string]float64
	fetched time.Time
}{rates: make(map[string]float64)}

const currencyCacheTTL = 6 * time.Hour

// exchangeRateResponse is the JSON shape from exchangerate-api.com.
type exchangeRateResponse struct {
	Base  string             `json:"base"`
	Rates map[string]float64 `json:"rates"`
}

// FetchCurrency retrieves the exchange rate for a currency code vs EUR.
func FetchCurrency(ctx context.Context, currencyCode string) (models.CurrencyInfo, error) {
	if currencyCode == "" {
		return models.CurrencyInfo{BaseCurrency: "EUR"}, nil
	}

	currencyCache.RLock()
	if len(currencyCache.rates) > 0 && time.Since(currencyCache.fetched) < currencyCacheTTL {
		rate, ok := currencyCache.rates[currencyCode]
		currencyCache.RUnlock()
		if ok {
			return models.CurrencyInfo{
				LocalCurrency: currencyCode,
				ExchangeRate:  rate,
				BaseCurrency:  "EUR",
			}, nil
		}
		return models.CurrencyInfo{
			LocalCurrency: currencyCode,
			BaseCurrency:  "EUR",
		}, nil
	}
	currencyCache.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, exchangeRateURL, nil)
	if err != nil {
		return models.CurrencyInfo{}, fmt.Errorf("create currency request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (destination currency)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return models.CurrencyInfo{}, fmt.Errorf("currency request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return models.CurrencyInfo{}, fmt.Errorf("read currency response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return models.CurrencyInfo{}, fmt.Errorf("exchangerate-api returned status %d: %s", resp.StatusCode, string(body))
	}

	var erResp exchangeRateResponse
	if err := json.Unmarshal(body, &erResp); err != nil {
		return models.CurrencyInfo{}, fmt.Errorf("parse currency response: %w", err)
	}

	currencyCache.Lock()
	currencyCache.rates = erResp.Rates
	currencyCache.fetched = time.Now()
	currencyCache.Unlock()

	rate, ok := erResp.Rates[currencyCode]
	if !ok {
		return models.CurrencyInfo{
			LocalCurrency: currencyCode,
			BaseCurrency:  "EUR",
		}, nil
	}

	return models.CurrencyInfo{
		LocalCurrency: currencyCode,
		ExchangeRate:  rate,
		BaseCurrency:  "EUR",
	}, nil
}

// ConvertCurrency converts an amount from one currency to another.
// Uses EUR as the base (ExchangeRate-API returns rates vs EUR).
// Returns the original amount and currency if conversion fails.
func ConvertCurrency(ctx context.Context, amount float64, from, to string) (float64, string) {
	if from == to || from == "" || to == "" || amount == 0 {
		return amount, to
	}

	// Ensure rates are cached.
	_, _ = FetchCurrency(ctx, from)

	currencyCache.RLock()
	fromRate, fromOK := currencyCache.rates[from]
	toRate, toOK := currencyCache.rates[to]
	currencyCache.RUnlock()

	// EUR is the base (rate = 1.0)
	if from == "EUR" {
		fromRate, fromOK = 1.0, true
	}
	if to == "EUR" {
		toRate, toOK = 1.0, true
	}

	if !fromOK || !toOK || fromRate == 0 {
		return amount, from // can't convert
	}

	// Convert: amount in "from" → EUR → "to"
	// EUR amount = amount / fromRate, then target = EUR * toRate
	return amount / fromRate * toRate, to
}

// ConvertToEUR is a convenience wrapper for ConvertCurrency(ctx, amount, from, "EUR").
func ConvertToEUR(ctx context.Context, amount float64, fromCurrency string) (float64, string) {
	return ConvertCurrency(ctx, amount, fromCurrency, "EUR")
}
