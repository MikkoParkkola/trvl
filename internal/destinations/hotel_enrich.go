package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HotelEnrichment holds extra metadata scraped from OpenStreetMap for a hotel
// matched by name + proximity.
type HotelEnrichment struct {
	Stars      int    // 1..5 from `stars` tag
	Website    string // `website` or `contact:website` tag
	Wheelchair string // `wheelchair` accessibility tag ("yes", "limited", "no")
	Phone      string // `phone` or `contact:phone`
	Operator   string // `operator` tag (hotel chain)
}

// EnrichHotelFromOSM queries Overpass for tourism=hotel POIs near the given
// coordinates and fuzzy-matches one by name. Returns nil on any error or when
// no match is found within 300m.
//
// This is a "best effort" enrichment — failures are silent and return nil.
func EnrichHotelFromOSM(ctx context.Context, hotelName string, lat, lon float64) *HotelEnrichment {
	if hotelName == "" || (lat == 0 && lon == 0) {
		return nil
	}

	// 300m radius: close enough to be the same building.
	query := fmt.Sprintf(`[out:json][timeout:10];
(
  node(around:300,%.6f,%.6f)[tourism=hotel];
  way(around:300,%.6f,%.6f)[tourism=hotel];
);
out tags center 20;`, lat, lon, lat, lon)

	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := waitForOSMRateLimit(reqCtx); err != nil {
		return nil
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, overpassAPIURL,
		strings.NewReader("data="+query))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "trvl/1.0 (hotel enrichment)")

	resp, err := destinationsSlowClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}

	var ovResp overpassResponse
	if err := json.Unmarshal(body, &ovResp); err != nil {
		return nil
	}

	// Find the best name match (case-insensitive substring).
	wantName := strings.ToLower(strings.TrimSpace(hotelName))
	var best *overpassElement
	var bestScore int
	for i := range ovResp.Elements {
		e := &ovResp.Elements[i]
		if e.Tags == nil {
			continue
		}
		osmName := strings.ToLower(e.Tags["name"])
		if osmName == "" {
			continue
		}
		score := nameMatchScore(wantName, osmName)
		if score > bestScore {
			bestScore = score
			best = e
		}
	}

	// Require at least a partial match. bestScore is the longest common word
	// count; anything below 1 is considered no match.
	if best == nil || bestScore < 1 {
		return nil
	}

	enrich := &HotelEnrichment{}
	if stars := best.Tags["stars"]; stars != "" {
		// Parse "3", "4", "5", tolerating "4S", "5*", etc.
		for _, r := range stars {
			if r >= '0' && r <= '9' {
				enrich.Stars = int(r - '0')
				break
			}
		}
	}
	if w := best.Tags["website"]; w != "" {
		enrich.Website = w
	} else if w := best.Tags["contact:website"]; w != "" {
		enrich.Website = w
	}
	if wc := best.Tags["wheelchair"]; wc != "" {
		enrich.Wheelchair = wc
	}
	if p := best.Tags["phone"]; p != "" {
		enrich.Phone = p
	} else if p := best.Tags["contact:phone"]; p != "" {
		enrich.Phone = p
	}
	if op := best.Tags["operator"]; op != "" {
		enrich.Operator = op
	}

	return enrich
}

// nameMatchScore returns the number of whitespace-separated words from want
// that appear as substrings in have. Higher is a better match.
func nameMatchScore(want, have string) int {
	if want == "" || have == "" {
		return 0
	}
	score := 0
	for _, w := range strings.Fields(want) {
		if len(w) < 3 {
			continue // ignore stop-words like "de", "la"
		}
		if strings.Contains(have, w) {
			score++
		}
	}
	return score
}
