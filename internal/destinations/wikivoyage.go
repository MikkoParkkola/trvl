package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const wikivoyageURL = "https://en.wikivoyage.org/w/api.php"

var wikivoyageAPIURL = wikivoyageURL

// wikivoyageCache stores guide results keyed by location name.
var wikivoyageCache = struct {
	sync.RWMutex
	entries map[string]wikivoyageCacheEntry
}{entries: make(map[string]wikivoyageCacheEntry)}

type wikivoyageCacheEntry struct {
	guide   *models.WikivoyageGuide
	fetched time.Time
}

const wikivoyageCacheTTL = 24 * time.Hour

// wikivoyageResponse is the JSON shape from the MediaWiki API.
type wikivoyageResponse struct {
	Query struct {
		Pages map[string]struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Extract string `json:"extract"`
		} `json:"pages"`
	} `json:"query"`
}

// GetWikivoyageGuide fetches a travel guide from Wikivoyage for the given location.
func GetWikivoyageGuide(ctx context.Context, location string) (*models.WikivoyageGuide, error) {
	cacheKey := strings.ToLower(location)

	wikivoyageCache.RLock()
	if entry, ok := wikivoyageCache.entries[cacheKey]; ok && time.Since(entry.fetched) < wikivoyageCacheTTL {
		wikivoyageCache.RUnlock()
		return entry.guide, nil
	}
	wikivoyageCache.RUnlock()

	u, _ := url.Parse(wikivoyageAPIURL)
	q := u.Query()
	q.Set("action", "query")
	q.Set("titles", location)
	q.Set("prop", "extracts")
	q.Set("explaintext", "1")
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create wikivoyage request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel guide)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikivoyage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read wikivoyage response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikivoyage returned status %d: %s", resp.StatusCode, string(body))
	}

	var wvResp wikivoyageResponse
	if err := json.Unmarshal(body, &wvResp); err != nil {
		return nil, fmt.Errorf("parse wikivoyage response: %w", err)
	}

	// Find the first valid page (not -1 which means "missing").
	var extract, title string
	for id, page := range wvResp.Query.Pages {
		if id == "-1" {
			continue
		}
		extract = page.Extract
		title = page.Title
		break
	}

	if extract == "" {
		return nil, fmt.Errorf("no Wikivoyage article found for %q", location)
	}

	guide := parseWikivoyageExtract(title, extract)

	wikivoyageCache.Lock()
	wikivoyageCache.entries[cacheKey] = wikivoyageCacheEntry{guide: guide, fetched: time.Now()}
	wikivoyageCache.Unlock()

	return guide, nil
}

// parseWikivoyageExtract splits a plain-text extract into sections.
// Wikivoyage uses "== Section ==" headers in the plain text.
func parseWikivoyageExtract(title, extract string) *models.WikivoyageGuide {
	guide := &models.WikivoyageGuide{
		Location: title,
		Sections: make(map[string]string),
		URL:      fmt.Sprintf("https://en.wikivoyage.org/wiki/%s", url.PathEscape(strings.ReplaceAll(title, " ", "_"))),
	}

	lines := strings.Split(extract, "\n")
	var currentSection string
	var sectionLines []string
	var summaryLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for section header: == Title == or === Subtitle ===
		if strings.HasPrefix(trimmed, "==") && strings.HasSuffix(trimmed, "==") {
			// Save previous section.
			if currentSection != "" {
				guide.Sections[currentSection] = strings.TrimSpace(strings.Join(sectionLines, "\n"))
			}
			// Parse new section name (strip == markers).
			name := strings.Trim(trimmed, "= ")
			currentSection = name
			sectionLines = nil
			continue
		}

		if currentSection == "" {
			summaryLines = append(summaryLines, line)
		} else {
			sectionLines = append(sectionLines, line)
		}
	}

	// Save last section.
	if currentSection != "" {
		guide.Sections[currentSection] = strings.TrimSpace(strings.Join(sectionLines, "\n"))
	}

	guide.Summary = strings.TrimSpace(strings.Join(summaryLines, "\n"))

	return guide
}
