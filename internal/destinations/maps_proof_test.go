//go:build proof

package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestMapsPageScrape tests whether Google Maps search pages contain
// extractable place data (names, ratings, reviews) in embedded JSON.
//
// This mirrors the hotel search approach: GET the page, parse AF_initDataCallback.
//
// KILL: FF-1 if 403, FF-2 if no place data found.
func TestMapsPageScrape(t *testing.T) {
	c := batchexec.NewClient()
	c.SetNoCache(true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Search for restaurants near Barcelona (Sagrada Familia area)
	url := "https://www.google.com/maps/search/restaurants+near+41.4036,2.1744"

	status, body, err := c.Get(ctx, url)
	if err != nil {
		t.Fatalf("GET maps search failed: %v", err)
	}

	t.Logf("Status: %d, Body length: %d bytes", status, len(body))

	if status == 403 {
		t.Fatalf("FF-1 KILL: Google returned 403 — blocked")
	}

	if status == 302 || status == 301 {
		t.Logf("Redirect detected — may need to follow redirects or adjust URL")
		t.Logf("Response (first 2000): %s", truncateStr(string(body), 2000))
	}

	if status != 200 {
		t.Logf("Response (first 2000): %s", truncateStr(string(body), 2000))
		t.Fatalf("Unexpected status %d", status)
	}

	bodyStr := string(body)

	// Check for consent/CAPTCHA page
	if strings.Contains(bodyStr, "consent.google.com") || strings.Contains(bodyStr, "captcha") {
		t.Fatalf("FF-1 KILL: Got consent/CAPTCHA page instead of results")
	}

	// Look for AF_initDataCallback pattern (like hotels use)
	afPattern := regexp.MustCompile(`AF_initDataCallback\(\{[^}]*data:`)
	afMatches := afPattern.FindAllStringIndex(bodyStr, -1)
	t.Logf("AF_initDataCallback occurrences: %d", len(afMatches))

	// Look for place-related keywords in the response
	placeSignals := []string{
		"restaurant",
		"rating",
		"reviews",
		"stars",
		"4.", // partial rating like 4.5
		"price_level",
		"opening_hours",
		"formatted_address",
	}

	foundSignals := 0
	for _, signal := range placeSignals {
		if strings.Contains(strings.ToLower(bodyStr), signal) {
			foundSignals++
			t.Logf("FOUND signal: %q", signal)
		}
	}

	t.Logf("Place signals found: %d/%d", foundSignals, len(placeSignals))

	// Print first 5000 chars for analysis
	t.Logf("=== RAW MAPS PAGE (first 5000 chars) ===")
	t.Logf("%s", truncateStr(bodyStr, 5000))
	t.Logf("=== END RAW MAPS PAGE ===")

	// Print last 5000 chars (often has data)
	if len(bodyStr) > 5000 {
		t.Logf("=== RAW MAPS PAGE (last 5000 chars) ===")
		last := bodyStr
		if len(last) > 5000 {
			last = last[len(last)-5000:]
		}
		t.Logf("%s", last)
		t.Logf("=== END MAPS PAGE TAIL ===")
	}

	// Look for embedded JSON data blocks (Google embeds data as JS arrays)
	// Pattern: window.APP_INITIALIZATION_STATE or similar
	initPatterns := []string{
		`APP_INITIALIZATION_STATE`,
		`APP_OPTIONS`,
		`initDataCallback`,
		`window.APP_FLAGS`,
	}
	for _, p := range initPatterns {
		if strings.Contains(bodyStr, p) {
			t.Logf("FOUND initialization pattern: %s", p)
		}
	}
}

// TestMapsSearchRPCEndpoint tests the Google Maps search RPC endpoint directly.
//
// Google Maps uses protobuf-over-HTTP for its search. The endpoint is:
// https://www.google.com/search?tbm=map&authuser=0&hl=en&gl=us&pb=...
//
// The pb= parameter is a serialized protobuf. We try a known working format.
func TestMapsSearchRPCEndpoint(t *testing.T) {
	c := batchexec.NewClient()
	c.SetNoCache(true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The Maps search endpoint uses pb= parameter with protobuf encoding.
	// This is a known format from SerpApi's reverse engineering work.
	// We try the simplest form: text search near coordinates.
	//
	// tbm=map tells Google to return Maps results
	// q= is the search query
	url := "https://www.google.com/search?tbm=map&authuser=0&hl=en&gl=us&q=restaurants+near+Barcelona"

	status, body, err := c.Get(ctx, url)
	if err != nil {
		t.Fatalf("GET maps search RPC failed: %v", err)
	}

	t.Logf("Status: %d, Body length: %d bytes", status, len(body))

	if status == 403 {
		t.Fatalf("FF-1 KILL: Google returned 403 — blocked")
	}

	if status != 200 {
		t.Logf("Response (first 3000): %s", truncateStr(string(body), 3000))
		t.Fatalf("Unexpected status %d", status)
	}

	bodyStr := string(body)

	// The tbm=map response typically starts with )]}'  (anti-XSSI prefix)
	// followed by JSON containing place data
	stripped := strings.TrimSpace(bodyStr)
	if strings.HasPrefix(stripped, ")]}'") {
		stripped = strings.TrimPrefix(stripped, ")]}'")
		stripped = strings.TrimSpace(stripped)
		t.Log("FOUND anti-XSSI prefix — this is a JSON/protobuf response (same as flights/hotels)")
	}

	// Try to parse as JSON
	var parsed any
	if err := json.Unmarshal([]byte(stripped), &parsed); err == nil {
		t.Log("SUCCESS: Response parses as JSON")
		pretty, _ := json.MarshalIndent(parsed, "", "  ")
		prettyStr := string(pretty)
		t.Logf("=== PARSED JSON (first 5000 chars) ===")
		t.Logf("%s", truncateStr(prettyStr, 5000))
		t.Logf("=== END PARSED JSON ===")

		// Check for place names and ratings in the JSON
		if strings.Contains(prettyStr, "restaurant") || strings.Contains(prettyStr, "Restaurant") {
			t.Log("GO SIGNAL: Found restaurant names in JSON response")
		}
		ratingPattern := regexp.MustCompile(`[0-9]\.[0-9]`)
		ratings := ratingPattern.FindAllString(prettyStr[:min(len(prettyStr), 10000)], 20)
		if len(ratings) > 0 {
			t.Logf("GO SIGNAL: Found %d potential ratings: %v", len(ratings), ratings)
		}
	} else {
		t.Logf("Not pure JSON (error: %v)", err)
		t.Logf("Response (first 5000): %s", truncateStr(bodyStr, 5000))

		// Still check for place data in raw response
		if strings.Contains(bodyStr, "restaurant") || strings.Contains(bodyStr, "Restaurant") {
			t.Log("FOUND restaurant references in response")
		}
	}
}

// TestMapsLocalSearchBatchExecute tests using batchexecute with Maps-related rpcids.
//
// Google Maps web app makes batchexecute calls to:
// https://www.google.com/_/LocalListing/data/batchexecute (hypothesized)
//
// Known rpcids from reverse engineering blogs:
// - Xq7wdb: place details
// - qGo8Yb: place search
func TestMapsLocalSearchBatchExecute(t *testing.T) {
	c := batchexec.NewClient()
	c.SetNoCache(true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try different endpoint + rpcid combinations
	endpoints := []struct {
		name string
		url  string
	}{
		{"TravelFrontend", "https://www.google.com/_/TravelFrontendUi/data/batchexecute"},
		{"MapsBackend", "https://www.google.com/_/MapsBackendUi/data/batchexecute"},
		{"LocalListing", "https://www.google.com/_/LocalListing/data/batchexecute"},
		{"BrowseDesktop", "https://www.google.com/_/BrowseDesktopFrontendUi/data/batchexecute"},
	}

	rpcids := []struct {
		name string
		id   string
		args string
	}{
		{"place_search_v1", "qGo8Yb", `["restaurants","Barcelona",null,[41.39,2.17],null,null,20]`},
		{"place_search_v2", "qGo8Yb", `[null,"restaurants near Barcelona"]`},
		{"place_details_v1", "Xq7wdb", `["ChIJ5TCOcRaYpBIRCmZHTz37sEQ"]`}, // Sagrada Familia place ID
		{"place_details_v2", "Xq7wdb", `[null,"ChIJ5TCOcRaYpBIRCmZHTz37sEQ",null,null,null,null,null,null,null,null,null,null]`},
	}

	for _, ep := range endpoints {
		for _, rpc := range rpcids {
			label := fmt.Sprintf("%s/%s/%s", ep.name, rpc.id, rpc.name)
			encoded := batchexec.EncodeBatchExecute(rpc.id, rpc.args)
			payload := "f.req=" + encoded

			status, body, err := c.PostForm(ctx, ep.url, payload)
			if err != nil {
				t.Logf("[%s] error: %v", label, err)
				continue
			}

			bodyStr := string(body)
			hasData := len(body) > 200 && !strings.Contains(bodyStr, `"error"`)

			t.Logf("[%s] status=%d len=%d hasData=%v", label, status, len(body), hasData)

			if status == 200 && hasData {
				t.Logf("=== PROMISING: %s (first 3000) ===", label)
				t.Logf("%s", truncateStr(bodyStr, 3000))
				t.Logf("=== END ===")
			}
		}
	}
}

// TestMapsSearchViaGoogleSearch tests getting Maps place data via google.com/search
// with Maps-specific parameters. This is the approach used by SerpApi.
func TestMapsSearchViaGoogleSearch(t *testing.T) {
	c := batchexec.NewClient()
	c.SetNoCache(true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Google Maps local results appear in regular search with specific params.
	// The key is using the right parameters to get structured data back.
	urls := []struct {
		name string
		url  string
	}{
		{
			"maps_search_json",
			"https://www.google.com/maps/search/restaurants+near+Barcelona/@41.39,2.17,14z/data=!3m1!4b1",
		},
		{
			"maps_place_direct",
			"https://www.google.com/maps/place/Sagrada+Familia/@41.4036,2.1744,17z",
		},
	}

	for _, u := range urls {
		t.Logf("--- Testing: %s ---", u.name)

		status, body, err := c.Get(ctx, u.url)
		if err != nil {
			t.Logf("[%s] error: %v", u.name, err)
			continue
		}

		t.Logf("[%s] status=%d len=%d", u.name, status, len(body))

		if status == 403 {
			t.Logf("[%s] FF-1: BLOCKED", u.name)
			continue
		}

		bodyStr := string(body)

		// Count key data signals
		signals := map[string]bool{
			"rating":    strings.Contains(bodyStr, "rating") || strings.Contains(bodyStr, "stars"),
			"review":    strings.Contains(bodyStr, "review"),
			"address":   strings.Contains(bodyStr, "address") || strings.Contains(bodyStr, "Carrer"),
			"hours":     strings.Contains(bodyStr, "hours") || strings.Contains(bodyStr, "Open"),
			"phone":     strings.Contains(bodyStr, "phone") || strings.Contains(bodyStr, "+34"),
			"price":     strings.Contains(bodyStr, "price") || strings.Contains(bodyStr, "$$"),
			"name_data": strings.Contains(bodyStr, "Sagrada") || strings.Contains(bodyStr, "restaurant"),
		}

		found := 0
		for k, v := range signals {
			if v {
				found++
				t.Logf("[%s] FOUND: %s", u.name, k)
			}
		}
		t.Logf("[%s] Data signals: %d/7", u.name, found)

		// Check for embedded JSON data
		if strings.Contains(bodyStr, "AF_initDataCallback") {
			afCount := strings.Count(bodyStr, "AF_initDataCallback")
			t.Logf("[%s] AF_initDataCallback count: %d", u.name, afCount)

			// Extract first data block for inspection
			idx := strings.Index(bodyStr, "AF_initDataCallback({")
			if idx >= 0 {
				snippet := bodyStr[idx:]
				endIdx := strings.Index(snippet, "});")
				if endIdx > 0 && endIdx < 10000 {
					t.Logf("[%s] First AF_initDataCallback block (first 3000): %s",
						u.name, truncateStr(snippet[:endIdx+3], 3000))
				}
			}
		}

		if strings.Contains(bodyStr, "window.APP_INITIALIZATION_STATE") {
			t.Logf("[%s] FOUND APP_INITIALIZATION_STATE — page has embedded data", u.name)
		}

		// Print sample of body for analysis
		t.Logf("[%s] Body sample (chars 1000-4000): %s",
			u.name, truncateStr(safeSlice(bodyStr, 1000, 4000), 3000))
	}
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("... [truncated, %d total]", len(s))
}

func safeSlice(s string, start, end int) string {
	if start >= len(s) {
		return ""
	}
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestMapsTbmSearchWithPb tests the Maps search endpoint with the pb= protobuf
// parameter, which is the actual data-fetching mechanism used by the Maps web app.
//
// The Maps HTML page contains a <link rel="preload"> with the full pb= URL.
// We extract that URL and fetch it directly -- it returns JSON with place data.
func TestMapsTbmSearchWithPb(t *testing.T) {
	c := batchexec.NewClient()
	c.SetNoCache(true)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: Get the Maps page to extract the pb= URL
	pageURL := "https://www.google.com/maps/search/restaurants+near+Barcelona/@41.39,2.17,14z"
	status, body, err := c.Get(ctx, pageURL)
	if err != nil {
		t.Fatalf("GET maps page failed: %v", err)
	}
	if status != 200 {
		t.Fatalf("Maps page status %d", status)
	}

	bodyStr := string(body)

	// Extract the pb= URL from the <link> preload tag
	// Pattern: /search?tbm=map&...&pb=...
	pbPattern := regexp.MustCompile(`href="(/search\?tbm=map[^"]+)"`)
	pbMatch := pbPattern.FindStringSubmatch(bodyStr)

	if len(pbMatch) < 2 {
		t.Log("No pb= URL found in page, trying manual construction")
	} else {
		pbURL := "https://www.google.com" + strings.ReplaceAll(pbMatch[1], "&amp;", "&")
		t.Logf("Extracted pb URL (first 500 chars): %s", truncateStr(pbURL, 500))

		// Fetch the pb= URL
		status2, body2, err2 := c.Get(ctx, pbURL)
		if err2 != nil {
			t.Fatalf("GET pb URL failed: %v", err2)
		}
		t.Logf("pb= response: status=%d len=%d", status2, len(body2))

		if status2 == 200 {
			body2Str := string(body2)
			// Strip anti-XSSI prefix
			stripped := strings.TrimSpace(body2Str)
			if strings.HasPrefix(stripped, ")]}'") {
				stripped = strings.TrimPrefix(stripped, ")]}'")
				stripped = strings.TrimSpace(stripped)
				t.Log("HAS anti-XSSI prefix (same protocol as flights/hotels)")
			}

			// Try JSON parse
			var parsed any
			if err := json.Unmarshal([]byte(stripped), &parsed); err == nil {
				t.Log("SUCCESS: pb= response parses as JSON")

				pretty, _ := json.MarshalIndent(parsed, "", "  ")
				prettyStr := string(pretty)

				// Search for place data signals
				placeNames := []string{}
				ratings := regexp.MustCompile(`[1-5]\.\d`).FindAllString(prettyStr[:min(len(prettyStr), 50000)], 50)

				// Look for restaurant/place names
				namePattern := regexp.MustCompile(`"([A-Z][a-zA-ZÀ-ÿ\s'&]{3,40})"`)
				names := namePattern.FindAllStringSubmatch(prettyStr[:min(len(prettyStr), 50000)], 50)
				for _, n := range names {
					placeNames = append(placeNames, n[1])
				}

				t.Logf("Potential ratings found: %d -- %v", len(ratings), ratings[:min(len(ratings), 20)])
				t.Logf("Potential place names found: %d", len(placeNames))
				if len(placeNames) > 0 {
					t.Logf("Sample names: %v", placeNames[:min(len(placeNames), 15)])
				}

				// Look for review counts (large numbers like "1,234" or just "1234")
				reviewPattern := regexp.MustCompile(`\b[0-9]{2,5}\b`)
				reviews := reviewPattern.FindAllString(prettyStr[:min(len(prettyStr), 50000)], 30)
				if len(reviews) > 0 {
					t.Logf("Potential review counts: %v", reviews[:min(len(reviews), 15)])
				}

				// Print first chunk of response for visual inspection
				t.Logf("=== pb= JSON (first 8000 chars) ===")
				t.Logf("%s", truncateStr(prettyStr, 8000))
				t.Logf("=== END pb= JSON ===")

				if len(ratings) >= 3 && len(placeNames) >= 3 {
					t.Log("*** GO: Found ratings AND place names in JSON response ***")
				}
			} else {
				t.Logf("pb= response not JSON: %v", err)
				t.Logf("pb= raw (first 5000): %s", truncateStr(body2Str, 5000))
			}
		}
	}

	// Step 2: Also try a direct /search?tbm=map with manually constructed pb=
	// This is a minimal pb= parameter that requests local search results
	directURL := "https://www.google.com/search?tbm=map&authuser=0&hl=en&gl=us&pb=!4m12!1m3!1srestaurants!2s0x12a49816718e30e5%3A0x44b0fb3d4f47660a!3sBarcelona!2m3!1f41.39!2f2.17!3f14!5m1!4e2!6m6!1m2!1i1024!2i768!4f13.1!7i20!10b1"

	status3, body3, err3 := c.Get(ctx, directURL)
	if err3 != nil {
		t.Logf("Direct pb= request failed: %v", err3)
		return
	}

	t.Logf("Direct pb= response: status=%d len=%d", status3, len(body3))

	if status3 == 200 && len(body3) > 500 {
		body3Str := string(body3)
		stripped := strings.TrimSpace(body3Str)
		if strings.HasPrefix(stripped, ")]}'") {
			stripped = strings.TrimPrefix(stripped, ")]}'")
			stripped = strings.TrimSpace(stripped)
			t.Log("Direct pb= has anti-XSSI prefix")
		}

		var parsed any
		if err := json.Unmarshal([]byte(stripped), &parsed); err == nil {
			t.Log("Direct pb= parses as JSON")
			pretty, _ := json.MarshalIndent(parsed, "", "  ")
			prettyStr := string(pretty)

			// Check for actual place listing data
			hasRestaurant := strings.Contains(prettyStr, "restaurant") || strings.Contains(prettyStr, "Restaurant")
			hasRating := regexp.MustCompile(`[1-5]\.\d`).MatchString(prettyStr)
			hasReview := strings.Contains(prettyStr, "review")
			hasAddress := strings.Contains(prettyStr, "Carrer") || strings.Contains(prettyStr, "Barcelona")

			t.Logf("Direct pb= signals: restaurant=%v rating=%v review=%v address=%v",
				hasRestaurant, hasRating, hasReview, hasAddress)

			// Print a good chunk for analysis
			t.Logf("=== DIRECT pb= JSON (first 10000 chars) ===")
			t.Logf("%s", truncateStr(prettyStr, 10000))
			t.Logf("=== END DIRECT pb= ===")
		} else {
			t.Logf("Direct pb= not JSON: %v", err)
			t.Logf("Direct pb= raw (first 3000): %s", truncateStr(body3Str, 3000))
		}
	}
}
