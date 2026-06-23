package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fxCache holds live FX rates fetched from the Frankfurter API (free, no key,
// wraps ECB daily reference rates). Rates are refreshed at most once per 24h.
// On fetch failure the cache falls back to hardcoded approximations so that
// price normalisation never blocks on network errors.
type fxCache struct {
	mu      sync.RWMutex
	rates   map[string]map[string]float64 // base -> target -> rate
	fetched time.Time
	ttl     time.Duration
	client  *http.Client
	baseURL string // overridable for tests
}

// frankfurterResponse is the JSON shape returned by
// https://api.frankfurter.app/latest?from=CUR
type frankfurterResponse struct {
	Base  string             `json:"base"`
	Rates map[string]float64 `json:"rates"`
}

// fallbackRates are used when the live fetch fails. They are intentionally
// approximate — close enough for cross-provider comparison, never for billing.
var fallbackRates = map[string]map[string]float64{
	"EUR": {"USD": 1.09, "GBP": 0.86},
	"USD": {"EUR": 0.92, "GBP": 0.79},
	"GBP": {"EUR": 1.16, "USD": 1.38},
}

// peggedToEUR holds irrevocable fixed conversion rates for currencies that
// adopted the euro: the value is "legacy units per 1 EUR". The ECB/Frankfurter
// feed stops publishing these after adoption, so without this a still-quoted
// HRK price (Croatia, euro from 2023) is rejected as unconvertible and its
// offer dropped for currency mismatch. These rates are legally fixed forever.
// ponytail: HRK only — the one observed leaking; add another when it shows up.
var peggedToEUR = map[string]float64{"HRK": 7.53450}

// defaultFXCache is the package-level singleton used by normalizePrice.
var defaultFXCache = newFXCache()

// ConvertRate returns the FX conversion rate from→to and whether a rate is
// available. Callers that need to convert several price fields with the same
// pair (and want to know whether conversion actually happened, so they can
// rewrite the currency label only on success) should use this instead of
// normalizePrice. from/to are matched case-insensitively against ISO codes.
// Identical currencies return (1, true); a pair with no known rate returns
// (0, false) so the caller can leave the price — and its currency — untouched.
func ConvertRate(from, to string) (float64, bool) {
	from = strings.ToUpper(strings.TrimSpace(from))
	to = strings.ToUpper(strings.TrimSpace(to))
	if from == "" || to == "" {
		return 0, false
	}
	if from == to {
		return 1, true
	}
	if r := defaultFXCache.getRate(from, to); r > 0 {
		return r, true
	}
	if r, ok := convertViaPeg(from, to); ok {
		return r, true
	}
	return 0, false
}

// convertViaPeg handles currencies with a legally fixed euro peg that the live
// FX feed no longer carries. It triangulates through EUR: the peg supplies the
// fixed leg and the cache supplies the other. Returns (0,false) when neither
// side is pegged or the non-pegged leg has no rate.
func convertViaPeg(from, to string) (float64, bool) {
	fromPeg, fromPegged := peggedToEUR[from]
	toPeg, toPegged := peggedToEUR[to]
	if !fromPegged && !toPegged {
		return 0, false
	}
	// from→EUR
	fromToEUR := 1.0
	if fromPegged {
		fromToEUR = 1 / fromPeg
	} else if from != "EUR" {
		if r := defaultFXCache.getRate(from, "EUR"); r > 0 {
			fromToEUR = r
		} else {
			return 0, false
		}
	}
	// EUR→to
	eurToTo := 1.0
	if toPegged {
		eurToTo = toPeg
	} else if to != "EUR" {
		if r := defaultFXCache.getRate("EUR", to); r > 0 {
			eurToTo = r
		} else {
			return 0, false
		}
	}
	return fromToEUR * eurToTo, true
}

func newFXCache() *fxCache {
	return &fxCache{
		rates:   make(map[string]map[string]float64),
		ttl:     24 * time.Hour,
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: "https://api.frankfurter.app",
	}
}

// getRate returns the conversion rate from→to. It refreshes the cache when
// stale and falls back to hardcoded rates on error. Returns 0 when no rate
// is known for the pair (caller should leave the price unchanged).
func (fc *fxCache) getRate(from, to string) float64 {
	fc.mu.RLock()
	fresh := !fc.fetched.IsZero() && time.Since(fc.fetched) < fc.ttl
	fc.mu.RUnlock()

	if !fresh {
		fc.refresh()
	}

	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// Direct rate: from→to.
	if targets, ok := fc.rates[from]; ok {
		if r, ok := targets[to]; ok {
			return r
		}
	}

	// Triangulate through EUR (ECB publishes EUR-based rates).
	// from→EUR then EUR→to.
	if from != "EUR" && to != "EUR" {
		var fromToEUR, eurToTo float64
		if targets, ok := fc.rates[from]; ok {
			fromToEUR = targets["EUR"]
		}
		if targets, ok := fc.rates["EUR"]; ok {
			eurToTo = targets[to]
		}
		if fromToEUR > 0 && eurToTo > 0 {
			return fromToEUR * eurToTo
		}
	}

	return 0
}

// refresh fetches live rates for the three base currencies most commonly seen
// in hotel results (EUR, USD, GBP). On any error it populates the cache from
// fallbackRates so that normalizePrice always has something to work with.
func (fc *fxCache) refresh() {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	// Double-check after acquiring write lock — another goroutine may have
	// refreshed while we were waiting.
	if !fc.fetched.IsZero() && time.Since(fc.fetched) < fc.ttl {
		return
	}

	bases := []string{"EUR", "USD", "GBP"}
	newRates := make(map[string]map[string]float64, len(bases))
	ok := true

	for _, base := range bases {
		rates, err := fc.fetchBase(base)
		if err != nil {
			ok = false
			break
		}
		newRates[base] = rates
	}

	if ok {
		fc.rates = newRates
	} else {
		// Use fallback rates — copy so we don't alias the package var.
		for base, targets := range fallbackRates {
			m := make(map[string]float64, len(targets))
			for k, v := range targets {
				m[k] = v
			}
			newRates[base] = m
		}
		fc.rates = newRates
	}
	fc.fetched = time.Now()
}

// fetchBase calls the Frankfurter API for a single base currency.
func (fc *fxCache) fetchBase(base string) (map[string]float64, error) {
	url := fmt.Sprintf("%s/latest?from=%s", fc.baseURL, base)
	resp, err := fc.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("frankfurter: %s", resp.Status)
	}

	var fr frankfurterResponse
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		return nil, err
	}
	return fr.Rates, nil
}
