// Package batchexec — city code resolution via Google's H028ib rpcid.
//
// Google's CalendarGraph and CalendarGrid endpoints require city-level codes
// (e.g., "/m/04jpl" for London) rather than raw IATA airport codes. This file
// implements the resolution step used by the gflights library: send the IATA
// code (or city name) to the H028ib batchexecute endpoint, parse the response,
// and cache the result.
package batchexec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cityCache stores resolved city codes to avoid repeated lookups.
// City codes are stable identifiers that do not change.
var (
	cityCache   = make(map[string]string)
	cityCacheMu sync.RWMutex
)

// ResolveCityCode converts an IATA airport code or city name to a Google
// internal city code (e.g., "HEL" -> "/m/01lbs").
//
// The resolution uses rpcid H028ib via the FlightsFrontendUi batchexecute
// endpoint. Results are cached in memory since city codes are stable.
func ResolveCityCode(ctx context.Context, client *Client, query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("empty city query")
	}

	// Check cache first.
	cityCacheMu.RLock()
	if code, ok := cityCache[query]; ok {
		cityCacheMu.RUnlock()
		return code, nil
	}
	cityCacheMu.RUnlock()

	// Build the H028ib batchexecute request.
	cityReqData := fmt.Sprintf(`[[["H028ib","[\"%s\",[1,2,3,5,4],null,[1,1,1],1]",null,"generic"]]]`, query)
	encodedReq := url.QueryEscape(cityReqData)

	reqURL := "https://www.google.com/_/FlightsFrontendUi/data/batchexecute" +
		"?rpcids=H028ib" +
		"&source-path=%2Ftravel%2Fflights%2Fsearch" +
		"&hl=en&soc-app=162&soc-platform=1&soc-device=1&rt=c"

	formBody := "f.req=" + encodedReq +
		"&at=AAuQa1qJpLKW2Hl-i40OwJyzmo22%3A" + strconv.FormatInt(time.Now().Unix(), 10) + "&"

	status, body, err := client.PostForm(ctx, reqURL, formBody)
	if err != nil {
		return "", fmt.Errorf("city resolution request: %w", err)
	}

	if status == 403 {
		return "", ErrBlocked
	}
	// A refusal is not a cue to look the city up again, or to scan each day.
	if code, ok := WrbStatus(body); ok && code > 0 {
		return "", fmt.Errorf("city resolution declined (code %d): %w", code, ErrDeclined)
	}
	if status == 429 || QuotaRefusal(status, body) {
		return "", fmt.Errorf("city resolution: %w", ErrDeclined)
	}
	if status != 200 {
		return "", fmt.Errorf("city resolution: unexpected status %d", status)
	}

	code, err := parseCityResponse(body)
	if err != nil {
		return "", fmt.Errorf("parse city response for %q: %w", query, err)
	}

	// Cache the result.
	cityCacheMu.Lock()
	cityCache[query] = code
	cityCacheMu.Unlock()

	return code, nil
}

// parseCityResponse extracts the city code from an H028ib batchexecute response.
//
// Response format (after anti-XSSI prefix and length-prefixed lines):
//
//	[["wrb.fr","H028ib","<inner-json>",null,null,null,"generic"]]
//
// The inner JSON decodes to:
//
//	[[[[3,"CityName","CityName","Description","/m/code",...], [airports...]], ...]]
//
// We extract the "/m/code" at position [0][0][0][4].
func parseCityResponse(body []byte) (string, error) {
	// Strip anti-XSSI prefix.
	stripped := StripAntiXSSI(body)
	if len(stripped) == 0 {
		return "", ErrEmptyResponse
	}

	// The response is length-prefixed lines. Find the H028ib response line.
	text := string(stripped)
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "H028ib") {
			continue
		}

		// Parse the outer array: [["wrb.fr","H028ib","<json-string>",...]]
		var outer [][]any
		if err := json.Unmarshal([]byte(line), &outer); err != nil {
			continue
		}

		if len(outer) == 0 || len(outer[0]) < 3 {
			continue
		}

		innerStr, ok := outer[0][2].(string)
		if !ok || innerStr == "" {
			continue
		}

		// Parse inner JSON: [[[[3,"City","City","Desc","/m/code",...],[airports]],...]]]
		var inner [][][][]any
		if err := json.Unmarshal([]byte(innerStr), &inner); err != nil {
			continue
		}

		if len(inner) == 0 || len(inner[0]) == 0 || len(inner[0][0]) == 0 {
			continue
		}

		// First result's city info is at inner[0][0][0]
		cityInfo := inner[0][0][0]
		if len(cityInfo) < 5 {
			continue
		}

		code, ok := cityInfo[4].(string)
		if !ok || code == "" {
			continue
		}

		// Verify it looks like a Google city code.
		if strings.HasPrefix(code, "/m/") || strings.HasPrefix(code, "/g/") {
			return code, nil
		}
	}

	return "", fmt.Errorf("no city code found in response")
}

// ResetCityCache clears the in-memory city code cache. Useful for testing.
func ResetCityCache() {
	cityCacheMu.Lock()
	cityCache = make(map[string]string)
	cityCacheMu.Unlock()
}
