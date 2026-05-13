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

const travelAdvisoryURL = "https://www.travel-advisory.info/api"

// safetyCache stores advisory info keyed by country code.
var safetyCache = struct {
	sync.RWMutex
	entries map[string]safetyCacheEntry
}{entries: make(map[string]safetyCacheEntry)}

type safetyCacheEntry struct {
	info    models.SafetyInfo
	fetched time.Time
}

const safetyCacheTTL = 24 * time.Hour

// travelAdvisoryResponse is the JSON shape from travel-advisory.info.
type travelAdvisoryResponse struct {
	APIStatus struct {
		Request struct {
			Item string `json:"item"`
		} `json:"request"`
	} `json:"api_status"`
	Data map[string]struct {
		Name      string `json:"name"`
		Continent string `json:"continent"`
		Advisory  struct {
			Score   float64 `json:"score"`
			Message string  `json:"message"`
			Updated string  `json:"updated"`
			Source  string  `json:"source"`
		} `json:"advisory"`
	} `json:"data"`
}

// FetchSafety retrieves the travel advisory for a country from travel-advisory.info.
func FetchSafety(ctx context.Context, countryCode string) (models.SafetyInfo, error) {
	if countryCode == "" {
		return models.SafetyInfo{}, fmt.Errorf("empty country code")
	}

	safetyCache.RLock()
	if entry, ok := safetyCache.entries[countryCode]; ok && time.Since(entry.fetched) < safetyCacheTTL {
		safetyCache.RUnlock()
		return entry.info, nil
	}
	safetyCache.RUnlock()

	apiURL := fmt.Sprintf("%s?countrycode=%s", travelAdvisoryAPIURL, countryCode)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return models.SafetyInfo{}, fmt.Errorf("create safety request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (destination safety)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return models.SafetyInfo{}, fmt.Errorf("safety request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return models.SafetyInfo{}, fmt.Errorf("read safety response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return models.SafetyInfo{}, fmt.Errorf("travel-advisory.info returned status %d: %s", resp.StatusCode, string(body))
	}

	var taResp travelAdvisoryResponse
	if err := json.Unmarshal(body, &taResp); err != nil {
		return models.SafetyInfo{}, fmt.Errorf("parse safety response: %w", err)
	}

	data, ok := taResp.Data[countryCode]
	if !ok {
		return models.SafetyInfo{}, fmt.Errorf("no safety data for country %q", countryCode)
	}

	info := models.SafetyInfo{
		Level:       data.Advisory.Score,
		Advisory:    advisoryText(data.Advisory.Score),
		Source:      "travel-advisory.info",
		LastUpdated: data.Advisory.Updated,
	}

	safetyCache.Lock()
	safetyCache.entries[countryCode] = safetyCacheEntry{info: info, fetched: time.Now()}
	safetyCache.Unlock()

	return info, nil
}

// advisoryText converts a numeric score to a human-readable advisory.
func advisoryText(score float64) string {
	switch {
	case score <= 2.5:
		return "Exercise normal caution"
	case score <= 3.5:
		return "Exercise increased caution"
	case score <= 4.0:
		return "Reconsider travel"
	default:
		return "Do not travel"
	}
}
