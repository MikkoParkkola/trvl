// Package lounges provides airport lounge search across multiple programs.
//
// Data sources tried in order:
//  1. Priority Pass search API (prioritypass.com) — free, no auth required
//  2. Curated static dataset for top-30 hub airports
//
// Results are annotated with the user's lounge access cards so the caller
// knows immediately which lounges they can enter for free.
package lounges

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Lounge represents a single airport lounge.
type Lounge struct {
	// Name is the lounge name, e.g. "Finnair Lounge".
	Name string `json:"name"`
	// Airport is the IATA code of the airport where the lounge is located.
	Airport string `json:"airport"`
	// Terminal is a human-readable terminal designation, e.g. "Terminal 2, Gate D".
	Terminal string `json:"terminal,omitempty"`
	// Type categorises the lounge: "card" (Priority Pass etc.), "airline"
	// (airline status/class), "bank" (credit-card branded), "amex" (Centurion
	// network), or "independent" (pay-per-use).
	Type string `json:"type,omitempty"`
	// Cards lists the access card / program names that grant free entry, e.g.
	// ["Priority Pass", "Diners Club", "Oneworld Emerald"].
	Cards []string `json:"cards,omitempty"`
	// Amenities is a free-text list of available services.
	Amenities []string `json:"amenities,omitempty"`
	// OpenHours is a human-readable opening hours string, e.g. "04:30–23:30".
	OpenHours string `json:"open_hours,omitempty"`
	// AccessibleWith is populated by AnnotateAccess — the subset of the
	// user's own lounge cards/statuses that grant entry to this lounge.
	AccessibleWith []string `json:"accessible_with,omitempty"`
}

// SearchResult is the top-level response for a lounge search.
type SearchResult struct {
	Success bool     `json:"success"`
	Airport string   `json:"airport"`
	Count   int      `json:"count"`
	Lounges []Lounge `json:"lounges"`
	Source  string   `json:"source,omitempty"` // which data source was used
	Error   string   `json:"error,omitempty"`
}

// loungesClient is the shared HTTP client for lounge API calls.
var loungesClient = &http.Client{Timeout: 10 * time.Second}

// priorityPassBaseURL is the Priority Pass search API endpoint.
// Override in tests.
var priorityPassBaseURL = "https://www.prioritypass.com/api/inventoryloungesearchNpd"

// SearchLounges searches for airport lounges at the given airport (IATA code).
//
// It tries the Priority Pass search API first (free, no auth required).
// Falls back to a curated static dataset when the API is unreachable.
func SearchLounges(ctx context.Context, airport string) (*SearchResult, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if len(airport) != 3 || !isAlpha(airport) {
		return nil, fmt.Errorf("airport must be a 3-letter IATA code, got %q", airport)
	}

	// Try Priority Pass search API (free, no auth required).
	result, err := searchPriorityPass(ctx, airport)
	if err == nil && result.Success {
		return result, nil
	}

	// Fallback: static curated dataset for common hub airports.
	return staticFallback(airport), nil
}

// AnnotateAccess cross-references each lounge's Cards list against the user's
// own lounge card names (from preferences.LoungeCards). The intersection is
// stored in Lounge.AccessibleWith. This mutates the result in place.
func AnnotateAccess(result *SearchResult, userCards []string) {
	AnnotateAccessFull(result, userCards, nil)
}

// AnnotateAccessFull cross-references each lounge's Cards list against both
// the user's explicit lounge card names (e.g. "Priority Pass") and synthetic
// card names derived from frequent flyer status (e.g. "Oneworld Sapphire",
// "Finnair Plus Gold"). The union of matches is stored in
// Lounge.AccessibleWith. This mutates the result in place.
//
// The ffCards parameter should contain pre-built card names generated from
// the user's FrequentFlyerPrograms by the caller (keeping the lounges
// package decoupled from the preferences package).
func AnnotateAccessFull(result *SearchResult, userCards []string, ffCards []string) {
	if result == nil {
		return
	}
	total := len(userCards) + len(ffCards)
	if total == 0 {
		return
	}
	userSet := make(map[string]string, total)
	for _, c := range userCards {
		userSet[strings.ToLower(c)] = c
	}
	for _, c := range ffCards {
		key := strings.ToLower(c)
		if _, exists := userSet[key]; !exists {
			userSet[key] = c
		}
	}
	for i := range result.Lounges {
		l := &result.Lounges[i]
		var accessible []string
		for _, card := range l.Cards {
			if orig, ok := userSet[strings.ToLower(card)]; ok {
				accessible = append(accessible, orig)
			}
		}
		l.AccessibleWith = accessible
	}
}

// --- Priority Pass search API ---

// ppSearchResult is a single item from the Priority Pass search endpoint.
type ppSearchResult struct {
	Heading    string `json:"heading"`    // airport name, e.g. "Helsinki Airport"
	Subheading string `json:"subheading"` // "HEL, Helsinki, Finland"
	LocationID string `json:"locationId"` // "HEL-Helsinki Airport"
	URL        string `json:"url"`        // relative path, e.g. "/lounges/finland/helsinki-vantaa"
}

// searchPriorityPass queries the Priority Pass lounge search API.
// The API is free, requires no authentication, and returns JSON.
// It returns airport-level matches; lounge details come from the static
// dataset which is enriched with PP network membership.
func searchPriorityPass(ctx context.Context, airport string) (*SearchResult, error) {
	u := priorityPassBaseURL + "?term=" + url.QueryEscape(airport) + "&locale=en-GB"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create priority pass request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := loungesClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("priority pass request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("priority pass: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read priority pass response: %w", err)
	}

	var results []ppSearchResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("parse priority pass response: %w", err)
	}

	// The search API returns airport-level matches, not individual lounges.
	// Check if any match references our airport IATA code, confirming PP
	// has lounges there. Then merge with static data for full details.
	var ppConfirmed bool
	var ppURL string
	for _, r := range results {
		if strings.Contains(strings.ToUpper(r.Subheading), airport) ||
			strings.HasPrefix(strings.ToUpper(r.LocationID), airport) {
			ppConfirmed = true
			ppURL = r.URL
			break
		}
	}

	// Get static data as the base (has full details: cards, amenities, hours).
	static := staticFallback(airport)

	if ppConfirmed {
		static.Source = "prioritypass"
		if ppURL != "" {
			static.Source = "prioritypass" // confirmed in PP network
		}
	}

	// Even if PP didn't confirm, return static data (it covers top-30 airports).
	return static, nil
}

// --- Static fallback dataset ---

// staticLounge is the compact representation in the curated dataset.
type staticLounge struct {
	Name      string
	Terminal  string
	Cards     []string
	Amenities []string
	OpenHours string
}

// ppDragon are the card programs that accept Priority Pass, Diners Club,
// LoungeKey and Dragon Pass — the four most widely accepted programs.
var ppDragon = []string{"Priority Pass", "Diners Club", "LoungeKey", "Dragon Pass"}

// ppLK are lounges accepting Priority Pass and LoungeKey only.
var ppLK = []string{"Priority Pass", "LoungeKey"}

// amexPlatinum is access via American Express Platinum/Centurion cards.
var amexPlatinum = []string{"Amex Platinum", "Amex Centurion"}

// amexCenturion are Amex's own Centurion Lounge network.
var amexCenturion = []string{"Amex Centurion", "Amex Platinum"}

// merge combines multiple card slices into one deduplicated list.
func merge(lists ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, list := range lists {
		for _, c := range list {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// isAlpha returns true if all runes in s are ASCII letters.
func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return len(s) > 0
}

// classifyLounge infers a lounge type from its access card list.
func classifyLounge(cards []string) string {
	for _, c := range cards {
		lower := strings.ToLower(c)
		switch {
		case lower == "amex centurion":
			return "amex"
		case strings.Contains(lower, "business class") ||
			strings.Contains(lower, "first class") ||
			strings.Contains(lower, "oneworld") ||
			strings.Contains(lower, "star alliance") ||
			strings.Contains(lower, "skyteam") ||
			strings.Contains(lower, "plus gold") ||
			strings.Contains(lower, "plus platinum"):
			return "airline"
		case strings.Contains(lower, "visa platinum") ||
			strings.Contains(lower, "private banking"):
			return "bank"
		}
	}
	for _, c := range cards {
		if c == "Priority Pass" || c == "LoungeKey" || c == "Dragon Pass" || c == "Diners Club" {
			return "card"
		}
	}
	return "independent"
}

// staticFallback returns curated lounge data when no API is available.
// For airports not in the dataset it returns an empty-but-successful result.
func staticFallback(airport string) *SearchResult {
	entries, ok := staticData[airport]
	if !ok {
		return &SearchResult{
			Success: true,
			Airport: airport,
			Count:   0,
			Lounges: nil,
			Source:  "static",
		}
	}

	lounges := make([]Lounge, 0, len(entries))
	for _, e := range entries {
		lounges = append(lounges, Lounge{
			Name:      e.Name,
			Airport:   airport,
			Terminal:  e.Terminal,
			Type:      classifyLounge(e.Cards),
			Cards:     e.Cards,
			Amenities: e.Amenities,
			OpenHours: e.OpenHours,
		})
	}
	return &SearchResult{
		Success: true,
		Airport: airport,
		Count:   len(lounges),
		Lounges: lounges,
		Source:  "static",
	}
}
