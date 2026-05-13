package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// Live probe tests — opt-in via TRVL_TEST_LIVE_PROBES=1.
// These hit real endpoints to validate that provider configs generated
// from the suggest_providers catalog actually work.

func skipIfNoLiveProbes(t *testing.T) {
	t.Helper()
	if os.Getenv("TRVL_TEST_LIVE_PROBES") != "1" {
		t.Skip("live probes disabled (set TRVL_TEST_LIVE_PROBES=1)")
	}
	// Allow browser cookie access for live probes — needed for Booking.com
	// and other providers that rely on browser cookies for auth.
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
}

func TestLiveProbe_Hostelworld(t *testing.T) {
	skipIfNoLiveProbes(t)

	// Verified pattern: APIGEE_KEY:"..." in page HTML config block.
	// Uses ${city_id} resolved via CityLookup — Paris = 14 (production value).
	cfg := &ProviderConfig{
		ID:       "hostelworld",
		Name:     "Hostelworld",
		Category: "hotels",
		Auth: &AuthConfig{
			Type:         "preflight",
			PreflightURL: "https://www.hostelworld.com",
			Extractions: map[string]Extraction{
				"api_key": {
					Pattern:  `APIGEE_KEY:"([^"]+)"`,
					Variable: "api_key",
				},
			},
		},
		Endpoint: "https://prod.apigee.hostelworld.com/legacy-hwapi-service/2.2/cities/${city_id}/properties/",
		Method:   "GET",
		Headers: map[string]string{
			"api-key": "${api_key}",
		},
		QueryParams: map[string]string{
			"date-start": "${checkin}",
			"num-nights": "1",
			"guests":     "${guests}",
			"currency":   "${currency}",
			"per-page":   "30",
		},
		ResponseMapping: ResponseMapping{
			ResultsPath: "properties",
			RatingScale: 0.1, // Hostelworld returns 0-100, normalize to 0-10
			Fields: map[string]string{
				"name":         "name",
				"hotel_id":     "id",
				"rating":       "overallRating.overall",
				"review_count": "overallRating.numberOfRatings",
				"price":        "lowestPricePerNight.value",
				"currency":     "lowestPricePerNight.currency",
				"address":      "address1",
				"neighborhood": "district.name",
			},
		},
		CityLookup: map[string]string{
			"paris": "14",
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 1},
		TLS:       TLSConfig{Fingerprint: "standard"},
	}

	checkin := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, 15).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := TestProvider(ctx, cfg, "Paris", 48.8566, 2.3522, checkin, checkout, "EUR", 2)
	logResult(t, "Hostelworld", result)

	if !result.Success {
		t.Errorf("Hostelworld probe failed at step %q: %s", result.Step, result.Error)
	}
	if result.ResultsCount == 0 {
		t.Error("Hostelworld returned 0 results")
	}
}

func TestLiveProbe_Airbnb(t *testing.T) {
	skipIfNoLiveProbes(t)

	// SSR approach: fetch the search results page HTML and extract the
	// embedded JSON from <script id="data-deferred-state-0">. The v2/v3
	// API endpoints were deprecated; Airbnb now embeds all search data
	// in the server-rendered HTML via niobeClientData.
	cfg := &ProviderConfig{
		ID:       "airbnb",
		Name:     "Airbnb",
		Category: "hotels",
		Auth: &AuthConfig{
			Type:               "preflight",
			PreflightURL:       "https://www.airbnb.com",
			BrowserEscapeHatch: true,
		},
		Endpoint: "https://www.airbnb.com/s/${location}/homes?checkin=${checkin}&checkout=${checkout}&adults=${guests}&currency=${currency}",
		Method:   "GET",
		Headers: map[string]string{
			"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
			"Accept-Language":           "en-US,en;q=0.9",
			"User-Agent":                "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
			"Sec-Ch-Ua":                 `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
			"Sec-Ch-Ua-Mobile":          "?0",
			"Sec-Ch-Ua-Platform":        `"macOS"`,
			"Sec-Fetch-Dest":            "document",
			"Sec-Fetch-Mode":            "navigate",
			"Sec-Fetch-Site":            "none",
			"Sec-Fetch-User":            "?1",
			"Upgrade-Insecure-Requests": "1",
		},
		// No BodyTemplate needed: SSR GET replaces old POST GraphQL body.
		BodyTemplate: `{
			"_unused": "SSR extraction via body_extract_pattern",
			"staysSearchRequest": {
					"cursor": "",
					"maxMapItems": 9999,
					"metadataOnly": false,
					"requestedPageType": "STAYS_SEARCH",
					"source": "structured_search_input_header",
					"searchType": "user_map_move",
					"treatmentFlags": [
						"feed_map_decouple_m11_treatment",
						"stays_search_rehydration_treatment_desktop",
						"stays_search_rehydration_treatment_moweb",
						"selective_query_feed_map_homepage_desktop_treatment",
						"selective_query_feed_map_homepage_moweb_treatment"
					],
					"rawParams": [
						{"filterName": "cdnCacheSafe", "filterValues": ["false"]},
						{"filterName": "channel", "filterValues": ["EXPLORE"]},
						{"filterName": "checkin", "filterValues": ["${checkin}"]},
						{"filterName": "checkout", "filterValues": ["${checkout}"]},
						{"filterName": "datePickerType", "filterValues": ["calendar"]},
						{"filterName": "flexibleTripLengths", "filterValues": ["one_week"]},
						{"filterName": "itemsPerGrid", "filterValues": ["20"]},
						{"filterName": "monthlyLength", "filterValues": ["3"]},
						{"filterName": "monthlyStartDate", "filterValues": ["2024-02-01"]},
						{"filterName": "neLat", "filterValues": ["${ne_lat}"]},
						{"filterName": "neLng", "filterValues": ["${ne_lon}"]},
						{"filterName": "placeId", "filterValues": ["ChIJD7fiBh9u5kcRYJSMaMOCCwQ"]},
						{"filterName": "priceFilterInputType", "filterValues": ["0"]},
						{"filterName": "priceFilterNumNights", "filterValues": ["1"]},
						{"filterName": "query", "filterValues": ["Paris, France"]},
						{"filterName": "refinementPaths", "filterValues": ["/homes"]},
						{"filterName": "screenSize", "filterValues": ["large"]},
						{"filterName": "searchByMap", "filterValues": ["true"]},
						{"filterName": "swLat", "filterValues": ["${sw_lat}"]},
						{"filterName": "swLng", "filterValues": ["${sw_lon}"]},
						{"filterName": "tabId", "filterValues": ["home_tab"]},
						{"filterName": "version", "filterValues": ["1.8.3"]},
						{"filterName": "zoomLevel", "filterValues": ["11"]}
					]
				},
				"staysMapSearchRequestV2": {
					"cursor": "",
					"metadataOnly": false,
					"requestedPageType": "STAYS_SEARCH",
					"source": "structured_search_input_header",
					"searchType": "user_map_move",
					"treatmentFlags": [
						"feed_map_decouple_m11_treatment",
						"stays_search_rehydration_treatment_desktop",
						"stays_search_rehydration_treatment_moweb",
						"selective_query_feed_map_homepage_desktop_treatment",
						"selective_query_feed_map_homepage_moweb_treatment"
					],
					"rawParams": [
						{"filterName": "cdnCacheSafe", "filterValues": ["false"]},
						{"filterName": "channel", "filterValues": ["EXPLORE"]},
						{"filterName": "checkin", "filterValues": ["${checkin}"]},
						{"filterName": "checkout", "filterValues": ["${checkout}"]},
						{"filterName": "datePickerType", "filterValues": ["calendar"]},
						{"filterName": "flexibleTripLengths", "filterValues": ["one_week"]},
						{"filterName": "itemsPerGrid", "filterValues": ["20"]},
						{"filterName": "monthlyLength", "filterValues": ["3"]},
						{"filterName": "monthlyStartDate", "filterValues": ["2024-02-01"]},
						{"filterName": "neLat", "filterValues": ["${ne_lat}"]},
						{"filterName": "neLng", "filterValues": ["${ne_lon}"]},
						{"filterName": "placeId", "filterValues": ["ChIJD7fiBh9u5kcRYJSMaMOCCwQ"]},
						{"filterName": "priceFilterInputType", "filterValues": ["0"]},
						{"filterName": "priceFilterNumNights", "filterValues": ["1"]},
						{"filterName": "query", "filterValues": ["Paris, France"]},
						{"filterName": "refinementPaths", "filterValues": ["/homes"]},
						{"filterName": "screenSize", "filterValues": ["large"]},
						{"filterName": "searchByMap", "filterValues": ["true"]},
						{"filterName": "swLat", "filterValues": ["${sw_lat}"]},
						{"filterName": "swLng", "filterValues": ["${sw_lon}"]},
						{"filterName": "tabId", "filterValues": ["home_tab"]},
						{"filterName": "version", "filterValues": ["1.8.3"]},
						{"filterName": "zoomLevel", "filterValues": ["11"]}
					]
				},
				"includeMapResults": true,
				"isLeanTreatment": false
			}
		}`,
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.presentation.staysSearch.results.searchResults",
			RatingScale: 2.0, // Airbnb returns 0-5, normalize to 0-10
			Fields: map[string]string{
				"name":         "subtitle",
				"hotel_id":     "demandStayListing.id",
				"rating":       "avgRatingLocalized",
				"review_count": "avgRatingLocalized",
				"price":        "structuredDisplayPrice.primaryLine.price",
				"lat":          "demandStayListing.location.coordinate.latitude",
				"lon":          "demandStayListing.location.coordinate.longitude",
				"address":      "title",
			},
			BodyExtractPattern: `<script[^>]*data-deferred-state-0[^>]*>[\s\S]*?(\{"data":\{"presentation":.+\})\]\]\}</script>`,
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 0.5},
		TLS:       TLSConfig{Fingerprint: "chrome"},
		Cookies:   CookieConfig{Source: "preflight"},
	}

	checkin := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, 31).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := TestProvider(ctx, cfg, "Paris", 48.8566, 2.3522, checkin, checkout, "EUR", 2)
	logResult(t, "Airbnb", result)

	if !result.Success {
		t.Errorf("Airbnb probe failed at step %q: %s", result.Step, result.Error)
	}
	if result.ResultsCount == 0 {
		t.Error("Airbnb returned 0 results")
	}
	// SSR extraction provides all fields directly — verify key fields are populated.
	if result.SampleResult != nil {
		if id, _ := result.SampleResult["hotel_id"].(string); id == "" {
			t.Error("Airbnb sample result has empty hotel_id")
		}
		if name, _ := result.SampleResult["name"].(string); name == "" {
			t.Error("Airbnb sample result has empty name")
		}
	}
}

func TestLiveProbe_Booking(t *testing.T) {
	skipIfNoLiveProbes(t)

	// Booking.com uses the dml/graphql endpoint with browser cookies for auth.
	// No CSRF extraction needed — the production config relies on browser
	// cookies read from the user's installed browser via kooky.
	// This probe mirrors the production booking.json config exactly.
	cfg := &ProviderConfig{
		ID:       "booking",
		Name:     "Booking.com",
		Category: "hotels",
		Auth: &AuthConfig{
			Type:               "preflight",
			PreflightURL:       "https://www.booking.com/searchresults.en-gb.html?dest_id=-1456928&dest_type=city&group_adults=2&no_rooms=1&lang=en-gb",
			BrowserEscapeHatch: true,
		},
		Endpoint: "https://www.booking.com/dml/graphql?lang=en-gb",
		Method:   "POST",
		Headers: map[string]string{
			"Accept":                        "*/*",
			"Content-Type":                  "application/json",
			"Origin":                        "https://www.booking.com",
			"Referer":                       "https://www.booking.com/searchresults.en-gb.html?dest_id=${city_id}&dest_type=city",
			"User-Agent":                    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
			"x-booking-context-action-name": "searchresults",
			"x-booking-context-aid":         "304142",
			"x-booking-topic":               "capla/v1",
		},
		BodyTemplate: `{"operationName":"searchQueries","variables":{"input":{"dates":{"checkin":"${checkin}","checkout":"${checkout}"},"location":{"destId":${city_id},"destType":"CITY"},"nbAdults":${guests},"nbRooms":1,"pagination":{"offset":0,"rowsPerPage":25}}},"query":"query searchQueries($input: SearchQueryInput!) { searchQueries { search(input: $input) { ... on SearchQueryOutput { results { ... on SearchResultProperty { displayName { text } basicPropertyData { id starRating { value } location { address latitude longitude } reviews { totalScore reviewsCount } photos { main { highResUrl { absoluteUrl } } } } priceDisplayInfoIrene { displayPrice { amountPerStay { amountUnformatted currency } } } } } pagination { nbResultsTotal } } } } }"}`,
		ResponseMapping: ResponseMapping{
			ResultsPath: "data.searchQueries.search.results",
			Fields: map[string]string{
				"name":         "displayName.text",
				"hotel_id":     "basicPropertyData.id",
				"rating":       "basicPropertyData.reviews.totalScore",
				"review_count": "basicPropertyData.reviews.reviewsCount",
				"price":        "priceDisplayInfoIrene.displayPrice.amountPerStay.amountUnformatted",
				"currency":     "priceDisplayInfoIrene.displayPrice.amountPerStay.currency",
				"lat":          "basicPropertyData.location.latitude",
				"lon":          "basicPropertyData.location.longitude",
				"address":      "basicPropertyData.location.address",
				"stars":        "basicPropertyData.starRating.value",
			},
		},
		CityLookup: map[string]string{
			"paris": "-1456928",
		},
		RateLimit: RateLimitConfig{RequestsPerSecond: 0.5},
		TLS:       TLSConfig{Fingerprint: "chrome"},
		Cookies:   CookieConfig{Source: "browser"},
	}

	checkin := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, 31).Format("2006-01-02")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := TestProvider(ctx, cfg, "Paris", 48.8566, 2.3522, checkin, checkout, "EUR", 2)
	logResult(t, "Booking.com", result)

	if !result.Success {
		if result.AuthTier == "" || result.AuthTier == "browser-cookies-only" {
			t.Logf("Booking.com needs browser cookies. Visit booking.com in your browser to seed cookies.")
		}
		t.Errorf("Booking.com probe failed at step %q: %s", result.Step, result.Error)
	}
	if result.ResultsCount == 0 && result.Success {
		t.Log("Booking.com returned 0 results — browser cookies may be stale")
	}
}

func logResult(t *testing.T, name string, r *TestResult) {
	t.Helper()
	data, _ := json.MarshalIndent(r, "", "  ")
	t.Logf("%s result:\n%s", name, string(data))
	if r.Success {
		t.Logf("%s: PASS — %d results", name, r.ResultsCount)
	} else {
		t.Logf("%s: FAIL — step=%s error=%s", name, r.Step, r.Error)
	}
	if len(r.ExtractionResults) > 0 {
		t.Logf("%s extractions: %v", name, r.ExtractionResults)
	}
	if r.SampleResult != nil {
		sample, _ := json.MarshalIndent(r.SampleResult, "", "  ")
		t.Logf("%s sample:\n%s", name, string(sample))
	}
	_, _ = fmt.Fprintf(os.Stderr, "[PROBE] %s: success=%v step=%s results=%d\n", name, r.Success, r.Step, r.ResultsCount)
}
