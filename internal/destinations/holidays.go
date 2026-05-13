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

const nagerDateURL = "https://date.nager.at/api/v3/publicholidays"

// holidayCache stores holiday lists keyed by "year/countryCode".
var holidayCache = struct {
	sync.RWMutex
	entries map[string]holidayCacheEntry
}{entries: make(map[string]holidayCacheEntry)}

type holidayCacheEntry struct {
	holidays []models.Holiday
	fetched  time.Time
}

const holidayCacheTTL = 24 * time.Hour

// nagerHoliday is the JSON shape from Nager.Date.
type nagerHoliday struct {
	Date      string   `json:"date"`
	LocalName string   `json:"localName"`
	Name      string   `json:"name"`
	Types     []string `json:"types"`
}

// FetchHolidays retrieves public holidays for a country and year from Nager.Date.
// If startDate and endDate are non-empty, results are filtered to that range.
func FetchHolidays(ctx context.Context, countryCode string, year int, startDate, endDate string) ([]models.Holiday, error) {
	if countryCode == "" {
		return nil, fmt.Errorf("empty country code")
	}

	key := fmt.Sprintf("%d/%s", year, countryCode)

	holidayCache.RLock()
	if entry, ok := holidayCache.entries[key]; ok && time.Since(entry.fetched) < holidayCacheTTL {
		holidayCache.RUnlock()
		return filterHolidays(entry.holidays, startDate, endDate), nil
	}
	holidayCache.RUnlock()

	apiURL := fmt.Sprintf("%s/%d/%s", nagerDateAPIURL, year, countryCode)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create holidays request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (destination holidays)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("holidays request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read holidays response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nager.date returned status %d: %s", resp.StatusCode, string(body))
	}

	var nagerHolidays []nagerHoliday
	if err := json.Unmarshal(body, &nagerHolidays); err != nil {
		return nil, fmt.Errorf("parse holidays response: %w", err)
	}

	holidays := make([]models.Holiday, 0, len(nagerHolidays))
	for _, h := range nagerHolidays {
		hType := "public"
		if len(h.Types) > 0 {
			hType = h.Types[0]
		}
		holidays = append(holidays, models.Holiday{
			Date: h.Date,
			Name: h.Name,
			Type: hType,
		})
	}

	holidayCache.Lock()
	holidayCache.entries[key] = holidayCacheEntry{holidays: holidays, fetched: time.Now()}
	holidayCache.Unlock()

	return filterHolidays(holidays, startDate, endDate), nil
}

// filterHolidays returns only holidays within [start, end].
func filterHolidays(holidays []models.Holiday, startDate, endDate string) []models.Holiday {
	if startDate == "" || endDate == "" {
		return holidays
	}

	start, err1 := models.ParseDate(startDate)
	end, err2 := models.ParseDate(endDate)
	if err1 != nil || err2 != nil {
		return holidays
	}

	var filtered []models.Holiday
	for _, h := range holidays {
		t, err := models.ParseDate(h.Date)
		if err != nil {
			continue
		}
		if !t.Before(start) && !t.After(end) {
			filtered = append(filtered, h)
		}
	}
	return filtered
}
