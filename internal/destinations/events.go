package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const ticketmasterURL = "https://app.ticketmaster.com/discovery/v2/events.json"

var ticketmasterAPIURL = ticketmasterURL

// eventsCache stores event results keyed by "location,start,end".
var eventsCache = struct {
	sync.RWMutex
	entries map[string]eventsCacheEntry
}{entries: make(map[string]eventsCacheEntry)}

type eventsCacheEntry struct {
	events  []models.Event
	fetched time.Time
}

const eventsCacheTTL = 6 * time.Hour

// ticketmasterResponse is the JSON shape from the Ticketmaster API.
type ticketmasterResponse struct {
	Embedded struct {
		Events []ticketmasterEvent `json:"events"`
	} `json:"_embedded"`
}

type ticketmasterEvent struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	URL   string `json:"url"`
	Dates struct {
		Start struct {
			LocalDate string `json:"localDate"`
			LocalTime string `json:"localTime"`
		} `json:"start"`
	} `json:"dates"`
	Embedded struct {
		Venues []struct {
			Name string `json:"name"`
		} `json:"venues"`
	} `json:"_embedded"`
	Classifications []struct {
		Segment struct {
			Name string `json:"name"`
		} `json:"segment"`
	} `json:"classifications"`
	PriceRanges []struct {
		Min      float64 `json:"min"`
		Max      float64 `json:"max"`
		Currency string  `json:"currency"`
	} `json:"priceRanges"`
}

// GetEvents fetches events from Ticketmaster for a location and date range.
// Returns an empty slice (no error) if TICKETMASTER_API_KEY is not set.
func GetEvents(ctx context.Context, location string, startDate, endDate string) ([]models.Event, error) {
	apiKey := os.Getenv("TICKETMASTER_API_KEY")
	if apiKey == "" {
		return nil, nil
	}

	cacheKey := fmt.Sprintf("%s,%s,%s", strings.ToLower(location), startDate, endDate)

	eventsCache.RLock()
	if entry, ok := eventsCache.entries[cacheKey]; ok && time.Since(entry.fetched) < eventsCacheTTL {
		eventsCache.RUnlock()
		return entry.events, nil
	}
	eventsCache.RUnlock()

	u, _ := url.Parse(ticketmasterAPIURL)
	q := u.Query()
	q.Set("apikey", apiKey)
	q.Set("city", location)
	q.Set("startDateTime", startDate+"T00:00:00Z")
	q.Set("endDateTime", endDate+"T23:59:59Z")
	q.Set("size", "20")
	q.Set("sort", "date,asc")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create ticketmaster request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (event search)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ticketmaster request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ticketmaster response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ticketmaster returned status %d: %s", resp.StatusCode, string(body))
	}

	var tmResp ticketmasterResponse
	if err := json.Unmarshal(body, &tmResp); err != nil {
		return nil, fmt.Errorf("parse ticketmaster response: %w", err)
	}

	events := make([]models.Event, 0, len(tmResp.Embedded.Events))
	for _, e := range tmResp.Embedded.Events {
		event := models.Event{
			Name: e.Name,
			Date: e.Dates.Start.LocalDate,
			Time: e.Dates.Start.LocalTime,
			URL:  e.URL,
		}

		if len(e.Embedded.Venues) > 0 {
			event.Venue = e.Embedded.Venues[0].Name
		}

		if len(e.Classifications) > 0 {
			event.Type = strings.ToLower(e.Classifications[0].Segment.Name)
		}

		if len(e.PriceRanges) > 0 {
			pr := e.PriceRanges[0]
			event.PriceRange = fmt.Sprintf("%.0f-%.0f %s", pr.Min, pr.Max, pr.Currency)
		}

		events = append(events, event)
	}

	eventsCache.Lock()
	eventsCache.entries[cacheKey] = eventsCacheEntry{events: events, fetched: time.Now()}
	eventsCache.Unlock()

	return events, nil
}
