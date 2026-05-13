package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/models"
)

var (
	// enrichment regexps compiled once at package init.
	jsonLDScriptRe   = regexp.MustCompile(`<script[^>]*type="application/ld\+json"[^>]*>([\s\S]*?)</script>`)
	niobeScriptRe    = regexp.MustCompile(`<script[^>]*data-deferred-state-0[^>]*>([\s\S]*?)</script>`)
	htmlBRRe         = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTagRe        = regexp.MustCompile(`<[^>]+>`)
	htmlWhitespaceRe = regexp.MustCompile(`\s{2,}`)
)

// enrichRatings fetches hotel detail pages for results with rating=0 and a
// booking URL, extracting the aggregateRating from JSON-LD. This compensates
// for Booking.com's SSR response sometimes omitting review scores from the
// search results Apollo cache. Maximum 5 enrichments per call.
func enrichRatings(ctx context.Context, client *http.Client, hotels []models.HotelResult, cfg *ProviderConfig) {
	const maxEnrichments = 5
	enriched := 0

	for i := range hotels {
		if enriched >= maxEnrichments {
			break
		}
		if hotels[i].Rating > 0 || hotels[i].BookingURL == "" {
			continue
		}

		rating, reviewCount, err := fetchJSONLDRating(ctx, client, hotels[i].BookingURL)
		if err != nil {
			slog.Debug("rating enrichment failed", "url", hotels[i].BookingURL, "error", err.Error())
			continue
		}
		if rating > 0 {
			hotels[i].Rating = rating
			if reviewCount > 0 && hotels[i].ReviewCount == 0 {
				hotels[i].ReviewCount = reviewCount
			}
			slog.Debug("rating enriched from detail page",
				"hotel", hotels[i].Name, "rating", rating, "reviews", reviewCount)
		}
		enriched++
	}
	if enriched > 0 {
		slog.Info("enriched hotel ratings from detail pages",
			"provider", cfg.ID, "count", enriched)
	}
}

// fetchJSONLDRating fetches a hotel detail page and extracts the
// aggregateRating from the JSON-LD structured data. Booking.com embeds
// a <script type="application/ld+json"> block with the hotel's
// aggregateRating.ratingValue and aggregateRating.reviewCount.
func fetchJSONLDRating(ctx context.Context, client *http.Client, hotelURL string) (rating float64, reviewCount int, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", hotelURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := decompressBody(resp, maxResponseBytes)
	if err != nil {
		return 0, 0, err
	}

	// Extract JSON-LD blocks from the HTML.
	matches := jsonLDScriptRe.FindAllSubmatch(body, -1)

	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		var ld map[string]any
		if err := json.Unmarshal(m[1], &ld); err != nil {
			continue
		}
		// Look for aggregateRating in the top level or within @graph.
		if r, rc := extractAggregateRating(ld); r > 0 {
			return r, rc, nil
		}
		// Check @graph array.
		if graph, ok := ld["@graph"].([]any); ok {
			for _, item := range graph {
				if obj, ok := item.(map[string]any); ok {
					if r, rc := extractAggregateRating(obj); r > 0 {
						return r, rc, nil
					}
				}
			}
		}
	}

	return 0, 0, fmt.Errorf("no aggregateRating in JSON-LD")
}

// extractAggregateRating extracts ratingValue and reviewCount from a JSON-LD
// object that has an "aggregateRating" property.
func extractAggregateRating(obj map[string]any) (float64, int) {
	ar, ok := obj["aggregateRating"].(map[string]any)
	if !ok {
		return 0, 0
	}
	rating := toFloat64(ar["ratingValue"])
	count := toInt(ar["reviewCount"])
	return rating, count
}

// enrichAirbnbDescriptions fetches the listing detail page (PDP) for Airbnb
// results that have a BookingURL but no Description. It extracts the listing
// description from the embedded Niobe SSR cache. Capped at 3 enrichments.
func enrichAirbnbDescriptions(ctx context.Context, client *http.Client, hotels []models.HotelResult) {
	const maxEnrich = 3

	type indexedDesc struct {
		i    int
		desc string
	}
	ch := make(chan indexedDesc, maxEnrich)

	var wg sync.WaitGroup
	enriched := 0

	for i := range hotels {
		if enriched >= maxEnrich {
			break
		}
		if hotels[i].Description != "" || hotels[i].BookingURL == "" {
			continue
		}
		wg.Add(1)
		enriched++
		go func(idx int, listingURL string) {
			defer wg.Done()
			desc, err := fetchAirbnbDescription(ctx, client, listingURL)
			if err != nil {
				slog.Debug("airbnb description enrichment failed",
					"url", listingURL, "error", err.Error())
				return
			}
			if desc != "" {
				ch <- indexedDesc{i: idx, desc: desc}
			}
		}(i, hotels[i].BookingURL)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	count := 0
	for r := range ch {
		hotels[r.i].Description = r.desc
		count++
	}
	if count > 0 {
		slog.Info("enriched Airbnb descriptions from listing pages", "count", count)
	}
}

// fetchAirbnbDescription fetches an Airbnb listing page and extracts the
// property description from the embedded Niobe SSR cache.
func fetchAirbnbDescription(ctx context.Context, client *http.Client, listingURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", listingURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := decompressBody(resp, maxResponseBytes)
	if err != nil {
		return "", err
	}

	// Extract the Niobe SSR cache JSON from data-deferred-state-0.
	m := niobeScriptRe.FindSubmatch(body)
	if len(m) < 2 {
		return "", fmt.Errorf("data-deferred-state-0 script not found")
	}

	var raw any
	if err := json.Unmarshal(m[1], &raw); err != nil {
		return "", fmt.Errorf("parse niobe json: %w", err)
	}
	raw = unwrapNiobe(raw)

	// Path 1: sharingConfig.description
	if desc, ok := jsonPath(raw, "data.presentation.stayProductDetailPage.sections.metadata.sharingConfig.description").(string); ok && desc != "" {
		return desc, nil
	}

	// Path 2: DESCRIPTION_DEFAULT section body
	sections := jsonPath(raw, "data.presentation.stayProductDetailPage.sections.sections")
	if arr, ok := sections.([]any); ok {
		for _, s := range arr {
			smap, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if sType, _ := smap["sectionComponentType"].(string); sType != "DESCRIPTION_DEFAULT" {
				continue
			}
			if txt, ok := jsonPath(smap, "section.body.htmlText").(string); ok && txt != "" {
				return stripHTMLTags(txt), nil
			}
			if txt, ok := jsonPath(smap, "section.subtitle").(string); ok && txt != "" {
				return txt, nil
			}
			if txt, ok := jsonPath(smap, "section.title").(string); ok && txt != "" {
				return txt, nil
			}
		}
	}

	return "", fmt.Errorf("no description found in listing page")
}

// stripHTMLTags removes basic HTML tags, replacing <br> with a space.
func stripHTMLTags(s string) string {
	s = htmlBRRe.ReplaceAllString(s, " ")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = htmlWhitespaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
