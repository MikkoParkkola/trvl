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

const restCountriesURL = "https://restcountries.com/v3.1/alpha/"

// countryCache stores country info keyed by ISO alpha-2 code.
var countryCache = struct {
	sync.RWMutex
	entries map[string]countryCacheEntry
}{entries: make(map[string]countryCacheEntry)}

type countryCacheEntry struct {
	info    models.CountryInfo
	fetched time.Time
}

const countryCacheTTL = 24 * time.Hour

// restCountryResponse is the JSON shape for a single country from restcountries.com.
type restCountryResponse struct {
	Name struct {
		Common string `json:"common"`
	} `json:"name"`
	CCA2       string            `json:"cca2"`
	Capital    []string          `json:"capital"`
	Languages  map[string]string `json:"languages"`
	Currencies map[string]struct {
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
	} `json:"currencies"`
	Region    string   `json:"region"`
	Timezones []string `json:"timezones"`
}

// FetchCountry retrieves country information from restcountries.com.
func FetchCountry(ctx context.Context, countryCode string) (models.CountryInfo, error) {
	if countryCode == "" {
		return models.CountryInfo{}, fmt.Errorf("empty country code")
	}

	countryCache.RLock()
	if entry, ok := countryCache.entries[countryCode]; ok && time.Since(entry.fetched) < countryCacheTTL {
		countryCache.RUnlock()
		return entry.info, nil
	}
	countryCache.RUnlock()

	apiURL := restCountriesAPIURL + countryCode

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return models.CountryInfo{}, fmt.Errorf("create country request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (destination country)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return models.CountryInfo{}, fmt.Errorf("country request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return models.CountryInfo{}, fmt.Errorf("read country response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return models.CountryInfo{}, fmt.Errorf("restcountries returned status %d: %s", resp.StatusCode, string(body))
	}

	var countries []restCountryResponse
	if err := json.Unmarshal(body, &countries); err != nil {
		return models.CountryInfo{}, fmt.Errorf("parse country response: %w", err)
	}

	if len(countries) == 0 {
		return models.CountryInfo{}, fmt.Errorf("no country data for code %q", countryCode)
	}

	c := countries[0]

	info := models.CountryInfo{
		Name:   c.Name.Common,
		Code:   c.CCA2,
		Region: c.Region,
	}

	if len(c.Capital) > 0 {
		info.Capital = c.Capital[0]
	}

	for _, lang := range c.Languages {
		info.Languages = append(info.Languages, lang)
	}

	for code := range c.Currencies {
		info.Currencies = append(info.Currencies, code)
	}

	countryCache.Lock()
	countryCache.entries[countryCode] = countryCacheEntry{info: info, fetched: time.Now()}
	countryCache.Unlock()

	return info, nil
}
