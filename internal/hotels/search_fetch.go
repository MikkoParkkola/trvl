package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// hotelCityAliases maps common English city names to the form that Google
// Hotels actually resolves correctly. Without this, "Prague" returns zero
// results while "Praha" works fine.
var hotelCityAliases = map[string]string{
	"prague":     "Praha",
	"munich":     "München",
	"vienna":     "Wien",
	"cologne":    "Köln",
	"copenhagen": "København",
	"warsaw":     "Warszawa",
	"bucharest":  "București",
	"gothenburg": "Göteborg",
	"nuremberg":  "Nürnberg",
}

// normalizeHotelCity replaces known English city names with the form Google
// Hotels expects. Passthrough for unknown cities.
func normalizeHotelCity(location string) string {
	if mapped, ok := hotelCityAliases[strings.ToLower(strings.TrimSpace(location))]; ok {
		return mapped
	}
	return location
}

// maxPages is the maximum number of paginated requests per sort order.
// Each page returns ~20-26 hotels; 3 sort orders x 3 pages = up to ~180 unique.
// Kept at 3 per sort to limit total requests (9 max) and avoid 429 rate limits.
const maxPages = 3

// pageSize is the offset step between paginated requests. Google Travel
// Hotels returns ~20 results per page and uses a "start" query parameter
// for offset-based pagination.
const pageSize = 20

// googleSortOrders are the Google Hotels &sort= parameter values used to
// diversify results. The primary sort (empty string = Google's default
// relevance) is always fetched first. Additional sort orders pull in hotels
// that rank differently, significantly increasing unique coverage.
//
// Known values: 3=highest rated, 4=most reviewed, 8=price low-to-high.
var googleSortOrders = []string{"", "3", "8"}

// hotelSearchKey builds a singleflight dedup key from every option that can
// affect fetched, enriched, or post-filtered hotel results.
func hotelSearchKey(location string, opts HotelSearchOptions) string {
	amenities := normalizedHotelSearchAmenities(opts.Amenities)
	sort.Strings(amenities)
	key := struct {
		Location          string   `json:"location"`
		CheckIn           string   `json:"check_in"`
		CheckOut          string   `json:"check_out"`
		Guests            int      `json:"guests"`
		ChildrenAges      []int    `json:"children_ages,omitempty"`
		Rooms             int      `json:"rooms"`
		Stars             int      `json:"stars"`
		Sort              string   `json:"sort"`
		Currency          string   `json:"currency"`
		MinPrice          float64  `json:"min_price"`
		MaxPrice          float64  `json:"max_price"`
		MinRating         float64  `json:"min_rating"`
		MaxDistanceKm     float64  `json:"max_distance_km"`
		Amenities         []string `json:"amenities,omitempty"`
		CenterLat         float64  `json:"center_lat"`
		CenterLon         float64  `json:"center_lon"`
		EnrichAmenities   bool     `json:"enrich_amenities"`
		EnrichLimit       int      `json:"enrich_limit"`
		MaxPages          int      `json:"max_pages"`
		FreeCancellation  bool     `json:"free_cancellation"`
		Refundable        bool     `json:"refundable"`
		PropertyType      string   `json:"property_type"`
		Brand             string   `json:"brand"`
		EcoCertified      bool     `json:"eco_certified"`
		MinBedrooms       int      `json:"min_bedrooms"`
		MinBathrooms      int      `json:"min_bathrooms"`
		MinBeds           int      `json:"min_beds"`
		RoomType          string   `json:"room_type"`
		Superhost         bool     `json:"superhost"`
		InstantBook       bool     `json:"instant_book"`
		MaxDistanceM      int      `json:"max_distance_m"`
		Sustainable       bool     `json:"sustainable"`
		MealPlan          bool     `json:"meal_plan"`
		IncludeSoldOut    bool     `json:"include_sold_out"`
		MustHaveKitchen   bool     `json:"must_have_kitchen"`
		MustHaveWifi      bool     `json:"must_have_wifi"`
		MustHaveWorkspace bool     `json:"must_have_workspace"`
	}{
		Location:          location,
		CheckIn:           opts.CheckIn,
		CheckOut:          opts.CheckOut,
		Guests:            opts.Guests,
		ChildrenAges:      append([]int(nil), opts.ChildrenAges...),
		Rooms:             opts.Rooms,
		Stars:             opts.Stars,
		Sort:              opts.Sort,
		Currency:          strings.ToUpper(opts.Currency),
		MinPrice:          opts.MinPrice,
		MaxPrice:          opts.MaxPrice,
		MinRating:         opts.MinRating,
		MaxDistanceKm:     opts.MaxDistanceKm,
		Amenities:         amenities,
		CenterLat:         opts.CenterLat,
		CenterLon:         opts.CenterLon,
		EnrichAmenities:   opts.EnrichAmenities,
		EnrichLimit:       opts.EnrichLimit,
		MaxPages:          hotelPageLimit(opts.MaxPages),
		FreeCancellation:  opts.FreeCancellation,
		Refundable:        opts.RefundableRequired,
		PropertyType:      opts.PropertyType,
		Brand:             strings.ToLower(strings.TrimSpace(opts.Brand)),
		EcoCertified:      opts.EcoCertified,
		MinBedrooms:       opts.MinBedrooms,
		MinBathrooms:      opts.MinBathrooms,
		MinBeds:           opts.MinBeds,
		RoomType:          opts.RoomType,
		Superhost:         opts.Superhost,
		InstantBook:       opts.InstantBook,
		MaxDistanceM:      opts.MaxDistanceM,
		Sustainable:       opts.Sustainable,
		MealPlan:          opts.MealPlan,
		IncludeSoldOut:    opts.IncludeSoldOut,
		MustHaveKitchen:   opts.MustHaveKitchen,
		MustHaveWifi:      opts.MustHaveWifi,
		MustHaveWorkspace: opts.MustHaveWorkspace,
	}
	data, err := json.Marshal(key)
	if err != nil {
		return fmt.Sprintf("hotel|%s|%s|%s|%d|%s", location, opts.CheckIn, opts.CheckOut, opts.Guests, opts.Currency)
	}
	return "hotel|" + string(data)
}

func normalizedHotelSearchAmenities(amenities []string) []string {
	normalized := make([]string, 0, len(amenities))
	for _, amenity := range amenities {
		amenity = strings.ToLower(strings.TrimSpace(amenity))
		if amenity != "" {
			normalized = append(normalized, amenity)
		}
	}
	return normalized
}

func hotelPageLimit(requested int) int {
	pageLimit := maxPages
	if requested > 0 && requested < maxPages {
		pageLimit = requested
	}
	return pageLimit
}

// fetchHotelPage fetches a single page of hotel results at the given offset.
// offset=0 is the first page, offset=20 is the second, etc.
// googleSort is the Google Hotels &sort= parameter value ("" for default).
func fetchHotelPage(ctx context.Context, client *batchexec.Client, location string, opts HotelSearchOptions, offset int, googleSort string) ([]models.HotelResult, error) {
	pr, err := fetchHotelPageFull(ctx, client, location, opts, offset, googleSort)
	if err != nil {
		return nil, err
	}
	return pr.Hotels, nil
}

// googleConsentCookie is the cookie string sent on consent-bypass retries.
//
// Google's EU consent gate is gated by two cookies:
//   - SOCS: a base64-encoded proto that records the user's consent choice.
//     The value below decodes to "accept all" with no personalisation (a
//     valid consent record Google itself generates). It bypasses the redirect
//     to consent.google.com without granting ad-tracking consent.
//   - CONSENT: the older fallback cookie still honoured by some Google
//     front-ends.
//
// Both cookies are domain=.google.com and do not carry session secrets; they
// are safe to pre-seed and contain no personally-identifiable information.
const googleConsentCookie = "SOCS=CAESNQgDEitib3FfdW5kZWZpbmVkX2NvbnNlbnRfYm9keV9lbl9nYl92M18xNzI2NjI4MDgQlYoBGgYIgJCLsgY; CONSENT=YES+srp.gws-20230810-0-RC1.en+FX"

// isGoogleConsentPage reports whether the response body is Google's EU
// consent/cookie-wall page rather than a real search-results page.
func isGoogleConsentPage(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "consent.google.com") ||
		strings.Contains(s, "action=\"https://consent.google.") ||
		strings.Contains(s, "id=\"SOCS\"") ||
		(strings.Contains(s, "SOCS") && strings.Contains(s, "consentheading"))
}

// fetchHotelPageFull fetches a single page and returns the full parseResult
// including metadata like total available count.
// googleSort is the Google Hotels &sort= parameter value ("" for default).
func fetchHotelPageFull(ctx context.Context, client *batchexec.Client, location string, opts HotelSearchOptions, offset int, googleSort string) (parseResult, error) {
	travelURL := buildTravelURL(location, opts)
	if googleSort != "" {
		travelURL += "&sort=" + googleSort
	}
	if offset > 0 {
		travelURL += "&start=" + strconv.Itoa(offset)
	}

	status, body, err := client.Get(ctx, travelURL)
	if err != nil {
		return parseResult{}, fmt.Errorf("hotel search request: %w", err)
	}

	if status == 403 {
		return parseResult{}, batchexec.ErrBlocked
	}
	if status != 200 {
		return parseResult{}, fmt.Errorf("hotel search returned status %d", status)
	}
	if len(body) < 1000 {
		return parseResult{}, fmt.Errorf("hotel search returned empty response")
	}

	// Detect Google's EU consent/cookie-wall page. When Google redirects EU
	// users to consent.google.com the response body contains distinctive
	// markers instead of the AF_initDataCallback hotel data. Retry once with
	// pre-seeded consent cookies to bypass the wall transparently.
	if isGoogleConsentPage(body) {
		slog.Info("google consent page detected, retrying with consent cookies")
		status2, body2, err2 := client.GetWithCookie(ctx, travelURL, googleConsentCookie)
		if err2 == nil && status2 == 200 && len(body2) >= 1000 && !isGoogleConsentPage(body2) {
			body = body2
		} else {
			slog.Warn("consent cookie retry did not bypass consent page",
				"status", status2, "err", err2)
			return parseResult{}, fmt.Errorf("google consent page: unable to bypass (EU cookie wall)")
		}
	}

	pr := parseHotelsFromPageFull(string(body), opts.Currency)
	if len(pr.Hotels) == 0 {
		return parseResult{}, fmt.Errorf("parse hotel results: no hotels found in response payload")
	}

	return pr, nil
}

// buildHotelBookingURL constructs a Google Hotels deep link for a location and dates.
func buildHotelBookingURL(location, checkIn, checkOut string) string {
	encoded := url.PathEscape(location)
	return "https://www.google.com/travel/hotels/" + encoded + "?q=" + url.QueryEscape(location) + "+hotels&dates=" + checkIn + "," + checkOut
}

// buildTravelURL constructs the Google Travel Hotels search URL.
//
// Format: https://www.google.com/travel/hotels/{location}?q={location}&dates={checkin},{checkout}&adults={n}&hl=en-US&currency={cur}
func buildTravelURL(location string, opts HotelSearchOptions) string {
	encoded := url.PathEscape(location)
	query := url.Values{}
	query.Set("q", location)
	query.Set("dates", opts.CheckIn+","+opts.CheckOut)
	query.Set("adults", strconv.Itoa(opts.Guests))
	if len(opts.ChildrenAges) > 0 {
		query.Set("children", strconv.Itoa(len(opts.ChildrenAges)))
		query.Set("children_ages", joinInts(opts.ChildrenAges, ","))
	}
	if opts.Rooms > 0 {
		query.Set("rooms", strconv.Itoa(opts.Rooms))
	}
	query.Set("hl", "en")
	query.Set("currency", opts.Currency)

	// Server-side filters — let Google do the heavy lifting.
	// Client-side filterHotels() remains as a safety net.
	if opts.MinPrice > 0 {
		query.Set("min_price", strconv.FormatFloat(opts.MinPrice, 'f', 0, 64))
	}
	if opts.MaxPrice > 0 {
		query.Set("max_price", strconv.FormatFloat(opts.MaxPrice, 'f', 0, 64))
	}
	if opts.Stars > 0 {
		query.Set("class", strconv.Itoa(opts.Stars))
	}
	if opts.MinRating > 0 {
		// Google's rating param is on 0-10 scale (same as our internal scale).
		query.Set("rating", strconv.FormatFloat(opts.MinRating, 'f', 0, 64))
	}
	if opts.MaxDistanceKm > 0 {
		// Google uses meters for the lrad (location radius) parameter.
		query.Set("lrad", strconv.FormatFloat(opts.MaxDistanceKm*1000, 'f', 0, 64))
	}
	if opts.FreeCancellation {
		query.Set("fc", "1")
	}
	if opts.RefundableRequired {
		query.Set("refundable", "1")
	}
	if ptype := propertyTypeCode(opts.PropertyType); ptype != "" {
		query.Set("ptype", ptype)
	}
	if opts.EcoCertified {
		query.Set("ecof", "1")
	}

	return "https://www.google.com/travel/hotels/" + encoded + "?" + query.Encode()
}
