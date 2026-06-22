// Package serpapi provides a client for SerpAPI's Google Hotels engine.
//
// # OVERVIEW
//
// SerpAPI is a third-party service that scrapes Google Hotels and returns
// structured JSON with real hotel prices. It handles anti-bot protection
// (CloudFlare, rate limiting, TLS fingerprinting) so you don't have to.
//
// # WHY USE IT
//
// The standard 'trvl hotels' command scrapes Google directly and may return
// estimated or partial prices (e.g. without taxes, or for sold-out rooms).
// SerpAPI list results can still be lead-in candidates, so trvl verifies
// shortlisted properties through the property details endpoint before treating
// provider totals as traveller-facing price evidence.
//
// SETUP
//
//  1. Sign up at https://serpapi.com (free: 250 searches/month, no card)
//  2. Copy your API key from the dashboard
//  3. Set the environment variable: export SERPAPI_KEY=your_key_here
//
// USAGE
//
//	result, err := serpapi.SearchHotels(ctx, "Naoussa, Paros", "2026-08-03", "2026-08-10", "EUR")
//	if err != nil { /* handle */ }
//	for _, h := range result.Properties {
//	    fmt.Printf("%s: %.0f/nt (total: %.0f)\n", h.Name, h.PricePerNight(), h.TotalPrice())
//	}
//
// # PRICE FIELDS
//
// Each hotel in the response includes:
//   - PricePerNight(): lowest price per night (float)
//   - TotalPrice():    total for the entire stay (float)
//   - Prices[]:        detail-endpoint breakdown by provider when available
//
// LIMITATIONS
//
//   - Requires a free SerpAPI account and API key.
//   - Free plan allows 250 searches/month.
//   - Results depend on Google Hotels availability for the given dates.
package serpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Hotel struct {
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	Link          string  `json:"link"`
	PropertyToken string  `json:"property_token,omitempty"`
	HotelClass    int     `json:"extracted_hotel_class"`
	Rating        float64 `json:"overall_rating"`
	Reviews       int     `json:"reviews"`
	Type          string  `json:"type"`

	RatePerNight Rate `json:"rate_per_night"`
	TotalRate    Rate `json:"total_rate"`

	// ListRatePerNight and ListTotalRate preserve Google Hotels list-level
	// teaser prices when detail verification replaces RatePerNight/TotalRate
	// with the lowest provider total from the property details endpoint.
	ListRatePerNight *Rate `json:"list_rate_per_night,omitempty"`
	ListTotalRate    *Rate `json:"list_total_rate,omitempty"`

	Prices         []PriceOption `json:"prices"`
	FeaturedPrices []PriceOption `json:"featured_prices,omitempty"`

	Images []struct {
		Thumbnail string `json:"thumbnail"`
	} `json:"images"`

	Amenities         []string           `json:"amenities"`
	FreeCancellation  bool               `json:"free_cancellation"`
	PriceVerification *PriceVerification `json:"price_verification,omitempty"`
}

type Rate struct {
	Lowest              string  `json:"lowest"`
	Extracted           float64 `json:"extracted_lowest"`
	BeforeFees          string  `json:"before_taxes_fees,omitempty"`
	BeforeFeesExtracted float64 `json:"extracted_before_taxes_fees,omitempty"`
}

type PriceOption struct {
	Source                    string       `json:"source"`
	Link                      string       `json:"link,omitempty"`
	Logo                      string       `json:"logo,omitempty"`
	RatePerNight              Rate         `json:"rate_per_night"`
	TotalRate                 Rate         `json:"total_rate"`
	Benefits                  string       `json:"benefits,omitempty"`
	FreeCancellation          bool         `json:"free_cancellation,omitempty"`
	FreeCancellationUntilDate string       `json:"free_cancellation_until_date,omitempty"`
	FreeCancellationUntilTime string       `json:"free_cancellation_until_time,omitempty"`
	Rooms                     []RoomOption `json:"rooms,omitempty"`
}

type RoomOption struct {
	Name         string        `json:"name,omitempty"`
	Link         string        `json:"link,omitempty"`
	NumGuests    int           `json:"num_guests,omitempty"`
	RatePerNight Rate          `json:"rate_per_night"`
	TotalRate    Rate          `json:"total_rate"`
	Rates        []PriceOption `json:"rates,omitempty"`
}

type PriceVerification struct {
	Status        string    `json:"status"`
	Source        string    `json:"source,omitempty"`
	ListTotal     float64   `json:"list_total,omitempty"`
	VerifiedTotal float64   `json:"verified_total,omitempty"`
	Delta         float64   `json:"delta,omitempty"`
	DeltaPct      float64   `json:"delta_pct,omitempty"`
	Currency      string    `json:"currency,omitempty"`
	CheckedAt     time.Time `json:"checked_at,omitempty"`
	Warnings      []string  `json:"warnings,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type MapsPlace struct {
	Title          string   `json:"title"`
	Type           []string `json:"type"`
	Address        string   `json:"address"`
	DataID         string   `json:"data_id"`
	DataCID        string   `json:"data_cid"`
	Rating         float64  `json:"rating"`
	Reviews        int      `json:"reviews"`
	GPSCoordinates struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"gps_coordinates"`
}

type mapsPlaceResponse struct {
	SearchMetadata struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"search_metadata"`
	Error       string    `json:"error,omitempty"`
	PlaceResult MapsPlace `json:"place_results"`
}

type Response struct {
	SearchMetadata struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"search_metadata"`

	SearchParameters struct {
		Q        string `json:"q"`
		CheckIn  string `json:"check_in_date"`
		CheckOut string `json:"check_out_date"`
		Currency string `json:"currency"`
	} `json:"search_parameters"`

	Properties []Hotel `json:"properties"`
	Ads        []Hotel `json:"ads"`
}

type propertyDetailsResponse struct {
	SearchMetadata struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"search_metadata"`

	Hotel
}

type SearchOptions struct {
	Query            string
	CheckIn          string
	CheckOut         string
	Currency         string
	Adults           int
	Children         int
	GL               string
	HL               string
	ChildrenAges     []int
	SortBy           string
	MinPrice         float64
	MaxPrice         float64
	PropertyTypes    []string
	Amenities        []string
	Rating           string
	Brands           []string
	HotelClasses     []int
	FreeCancellation bool
	SpecialOffers    bool
	EcoCertified     bool
	VacationRentals  bool
	MinBedrooms      int
	MinBathrooms     int
	NextPageToken    string
	NoCache          bool
	MaxDetails       int
}

func APIKey() string {
	return os.Getenv("SERPAPI_KEY")
}

const (
	defaultSearchURL      = "https://serpapi.com/search"
	serpapiCacheDirEnv    = "TRVL_SERPAPI_CACHE_DIR"
	serpapiCacheTTL       = 55 * time.Minute
	serpapiCacheDisable   = "TRVL_SERPAPI_CACHE"
	serpapiCacheDisableV2 = "TRVL_SERPAPI_DISABLE_CACHE"
)

// searchURL is overridable in tests.
var searchURL = defaultSearchURL

func SearchHotels(ctx context.Context, query, checkIn, checkOut, currency string) (*Response, error) {
	return SearchHotelsWithOptions(ctx, SearchOptions{
		Query:    query,
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Currency: currency,
		Adults:   2,
	})
}

func SearchHotelsWithOptions(ctx context.Context, opts SearchOptions) (*Response, error) {
	return searchHotels(ctx, opts, "")
}

func SearchHotelsVerified(ctx context.Context, opts SearchOptions) (*Response, error) {
	result, err := SearchHotelsWithOptions(ctx, opts)
	if err != nil {
		return nil, err
	}
	verifyPropertyDetails(ctx, result, opts)
	return result, nil
}

func ResolveGoogleMapsPlace(ctx context.Context, googleID string) (*MapsPlace, error) {
	dataCID, err := GoogleMapsDataCID(googleID)
	if err != nil {
		return nil, err
	}
	req, err := googleMapsPlaceRequest(ctx, dataCID)
	if err != nil {
		return nil, err
	}

	var result mapsPlaceResponse
	if err := doSerpAPIRequest(req, &result); err != nil {
		return nil, err
	}
	if result.SearchMetadata.Status == "Error" {
		if result.Error != "" {
			return nil, fmt.Errorf("serpapi: %s", result.Error)
		}
		return nil, fmt.Errorf("serpapi: error status")
	}
	if result.Error != "" {
		return nil, fmt.Errorf("serpapi: %s", result.Error)
	}
	if result.PlaceResult.Title == "" && result.PlaceResult.DataID == "" {
		return nil, fmt.Errorf("serpapi: maps place response did not include place results")
	}
	return &result.PlaceResult, nil
}

func GetPropertyDetails(ctx context.Context, opts SearchOptions, propertyToken string) (*Hotel, error) {
	if strings.TrimSpace(propertyToken) == "" {
		return nil, fmt.Errorf("serpapi: property_token is required")
	}
	req, err := googleHotelsRequest(ctx, opts, propertyToken)
	if err != nil {
		return nil, err
	}

	var result propertyDetailsResponse
	if err := doGoogleHotelsRequest(req, &result); err != nil {
		return nil, err
	}
	if result.SearchMetadata.Status == "Error" {
		return nil, fmt.Errorf("serpapi: error status")
	}
	if result.Hotel.Name == "" && result.Hotel.PropertyToken == "" && len(result.Hotel.Prices) == 0 && len(result.Hotel.FeaturedPrices) == 0 {
		return nil, fmt.Errorf("serpapi: property details response did not include hotel details")
	}
	return &result.Hotel, nil
}

func GoogleMapsDataCID(googleID string) (string, error) {
	id := strings.TrimSpace(googleID)
	if id == "" {
		return "", fmt.Errorf("serpapi: google hotel ID is required")
	}
	if strings.Contains(id, ":") {
		parts := strings.Split(id, ":")
		id = strings.TrimSpace(parts[len(parts)-1])
	}
	id = strings.TrimSpace(id)
	lower := strings.ToLower(id)
	if strings.HasPrefix(lower, "0x") {
		n := new(big.Int)
		if _, ok := n.SetString(id[2:], 16); !ok {
			return "", fmt.Errorf("serpapi: invalid google hotel CID %q", googleID)
		}
		return n.String(), nil
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("serpapi: invalid google hotel CID %q", googleID)
		}
	}
	return id, nil
}

func searchHotels(ctx context.Context, opts SearchOptions, propertyToken string) (*Response, error) {
	req, err := googleHotelsRequest(ctx, opts, propertyToken)
	if err != nil {
		return nil, err
	}

	var result Response
	if err := doGoogleHotelsRequest(req, &result); err != nil {
		return nil, err
	}
	if result.SearchMetadata.Status == "Error" {
		return nil, fmt.Errorf("serpapi: error status")
	}
	return &result, nil
}

func googleMapsPlaceRequest(ctx context.Context, dataCID string) (*http.Request, error) {
	apiKey := APIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("SERPAPI_KEY not set")
	}
	if strings.TrimSpace(dataCID) == "" {
		return nil, fmt.Errorf("serpapi: data_cid is required")
	}

	u, _ := url.Parse(searchURL)
	q := u.Query()
	q.Set("engine", "google_maps")
	q.Set("type", "place")
	q.Set("data_cid", strings.TrimSpace(dataCID))
	q.Set("api_key", apiKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func googleHotelsRequest(ctx context.Context, opts SearchOptions, propertyToken string) (*http.Request, error) {
	apiKey := APIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("SERPAPI_KEY not set")
	}
	if opts.Adults <= 0 {
		opts.Adults = 2
	}
	if len(opts.ChildrenAges) > 0 {
		if opts.Children > 0 && opts.Children != len(opts.ChildrenAges) {
			return nil, fmt.Errorf("serpapi: children must match children_ages count")
		}
		opts.Children = len(opts.ChildrenAges)
	}

	u, _ := url.Parse(searchURL)
	q := u.Query()
	q.Set("engine", "google_hotels")
	q.Set("q", opts.Query)
	q.Set("check_in_date", opts.CheckIn)
	q.Set("check_out_date", opts.CheckOut)
	q.Set("currency", opts.Currency)
	q.Set("adults", strconv.Itoa(opts.Adults))
	if opts.GL != "" {
		q.Set("gl", opts.GL)
	}
	if opts.HL != "" {
		q.Set("hl", opts.HL)
	}
	if propertyToken != "" {
		q.Set("property_token", propertyToken)
	}
	if opts.NoCache {
		q.Set("no_cache", "true")
	}
	if opts.NextPageToken != "" {
		q.Set("next_page_token", opts.NextPageToken)
	}
	if opts.Children > 0 {
		q.Set("children", strconv.Itoa(opts.Children))
	}
	if len(opts.ChildrenAges) > 0 {
		q.Set("children_ages", joinInts(opts.ChildrenAges))
	}
	if sortBy := normalizeSortBy(opts.SortBy); sortBy != "" {
		q.Set("sort_by", sortBy)
	}
	if opts.MinPrice > 0 {
		q.Set("min_price", formatNumber(opts.MinPrice))
	}
	if opts.MaxPrice > 0 {
		q.Set("max_price", formatNumber(opts.MaxPrice))
	}
	if len(opts.PropertyTypes) > 0 {
		q.Set("property_types", joinTrimmed(opts.PropertyTypes))
	}
	if len(opts.Amenities) > 0 {
		q.Set("amenities", joinTrimmed(opts.Amenities))
	}
	if opts.Rating != "" {
		q.Set("rating", strings.TrimSpace(opts.Rating))
	}
	if len(opts.Brands) > 0 {
		q.Set("brands", joinTrimmed(opts.Brands))
	}
	if len(opts.HotelClasses) > 0 {
		q.Set("hotel_class", joinInts(opts.HotelClasses))
	}
	if opts.FreeCancellation {
		q.Set("free_cancellation", "true")
	}
	if opts.SpecialOffers {
		q.Set("special_offers", "true")
	}
	if opts.EcoCertified {
		q.Set("eco_certified", "true")
	}
	if opts.VacationRentals {
		q.Set("vacation_rentals", "true")
	}
	if opts.MinBedrooms > 0 {
		q.Set("bedrooms", strconv.Itoa(opts.MinBedrooms))
	}
	if opts.MinBathrooms > 0 {
		q.Set("bathrooms", strconv.Itoa(opts.MinBathrooms))
	}
	q.Set("api_key", apiKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	return req, nil
}

func doGoogleHotelsRequest(req *http.Request, result any) error {
	return doSerpAPIRequest(req, result)
}

func doSerpAPIRequest(req *http.Request, result any) error {
	if data, ok := readCachedGoogleHotelsResponse(req); ok {
		return json.Unmarshal(data, result)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("serpapi: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return err
	}
	writeCachedGoogleHotelsResponse(req, data)
	return nil
}

func readCachedGoogleHotelsResponse(req *http.Request) ([]byte, bool) {
	if !shouldUseSerpapiCache(req) {
		return nil, false
	}
	path, err := serpapiCachePath(req)
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) > serpapiCacheTTL {
		_ = os.Remove(path)
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		_ = os.Remove(path)
		return nil, false
	}
	return data, true
}

func writeCachedGoogleHotelsResponse(req *http.Request, data []byte) {
	if !shouldUseSerpapiCache(req) || !cacheableSerpapiResponse(data) {
		return
	}
	path, err := serpapiCachePath(req)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func shouldUseSerpapiCache(req *http.Request) bool {
	if req == nil || req.Method != http.MethodGet || req.URL == nil {
		return false
	}
	if os.Getenv(serpapiCacheDisable) == "0" || os.Getenv(serpapiCacheDisableV2) == "1" {
		return false
	}
	if strings.EqualFold(req.URL.Query().Get("no_cache"), "true") {
		return false
	}
	if os.Getenv(serpapiCacheDirEnv) == "" && searchURL != defaultSearchURL {
		return false
	}
	return true
}

func serpapiCachePath(req *http.Request) (string, error) {
	dir := os.Getenv(serpapiCacheDirEnv)
	if dir == "" {
		userCacheDir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(userCacheDir, "trvl", "serpapi")
	}
	return filepath.Join(dir, serpapiCacheKey(req)+".json"), nil
}

func serpapiCacheKey(req *http.Request) string {
	u := *req.URL
	q := u.Query()
	q.Del("api_key")
	u.RawQuery = q.Encode()
	u.Fragment = ""
	sum := sha256.Sum256([]byte(u.String()))
	return hex.EncodeToString(sum[:])
}

func cacheableSerpapiResponse(data []byte) bool {
	var meta struct {
		Error          string `json:"error"`
		SearchMetadata struct {
			Status string `json:"status"`
		} `json:"search_metadata"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return false
	}
	return meta.Error == "" && !strings.EqualFold(meta.SearchMetadata.Status, "Error")
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func normalizeSortBy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "price", "lowest_price", "lowest-price", "cheapest", "3":
		return "3"
	case "rating", "highest_rating", "highest-rating", "8":
		return "8"
	case "reviews", "reviewed", "most_reviewed", "most-reviewed", "13":
		return "13"
	default:
		return strings.TrimSpace(value)
	}
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func joinTrimmed(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, ",")
}
