package hotels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/searchctx"
	"golang.org/x/sync/singleflight"
)

var (
	defaultClient     *batchexec.Client
	defaultClientOnce sync.Once
)

// HotelRateManager is the shared rate manager for hotel providers.
var HotelRateManager = NewRateManager()

// SearchBooking searches hotels on Booking.com. Overridable in tests.
var SearchBooking = defaultSearchBooking

// hotelGroup deduplicates concurrent in-flight searches with identical parameters.
var hotelGroup singleflight.Group

const sharedHotelSearchTimeout = 30 * time.Second

// externalProviderRuntime is set by the MCP server when providers are configured.
// It is nil when no external providers are available.
var (
	externalProviderRuntime   *providers.Runtime
	externalProviderRuntimeMu sync.RWMutex
)

// SetExternalProviderRuntime configures the external provider runtime for hotel searches.
func SetExternalProviderRuntime(rt *providers.Runtime) {
	externalProviderRuntimeMu.Lock()
	externalProviderRuntime = rt
	externalProviderRuntimeMu.Unlock()
}

// getExternalProviderRuntime returns the current external provider runtime.
func getExternalProviderRuntime() *providers.Runtime {
	externalProviderRuntimeMu.RLock()
	defer externalProviderRuntimeMu.RUnlock()
	return externalProviderRuntime
}

// DefaultClient returns a shared batchexec.Client for the hotels package.
// The client is created once and reused across all requests, enabling
// connection reuse and shared rate limiting.
func DefaultClient() *batchexec.Client {
	defaultClientOnce.Do(func() {
		defaultClient = batchexec.NewClient()
		// Hotel searches make many sequential page requests across multiple
		// sort orders. Google Travel rate-limits at ~2 req/s; the default
		// 10 req/s triggers persistent 429 blocks.
		defaultClient.SetRateLimit(2)
	})
	return defaultClient
}

// HotelSearchOptions configures a hotel search.
type HotelSearchOptions struct {
	CheckIn  string // YYYY-MM-DD
	CheckOut string // YYYY-MM-DD
	Guests   int
	Stars    int    // 0 = any, 2-5 filter
	Sort     string // "cheapest", "rating", "distance", "stars"
	Currency string // default "USD"

	// Post-fetch filters.
	MinPrice      float64  // minimum price per night (0 = no filter)
	MaxPrice      float64  // maximum price per night (0 = no filter)
	MinRating     float64  // minimum guest rating on 0-10 scale, e.g. 8.0 (0 = no filter)
	MaxDistanceKm float64  // max km from city center (0 = no filter)
	Amenities     []string // required amenities, all must match (nil = no filter)
	CenterLat     float64  // city center latitude (resolved automatically if 0)
	CenterLon     float64  // city center longitude (resolved automatically if 0)

	// Enrichment options.
	EnrichAmenities bool // fetch detail pages for top hotels to get full amenity lists
	EnrichLimit     int  // max hotels to enrich (default: 5, max: 10)

	// MaxPages overrides the default pagination depth (maxPages).
	// Compound commands (trip-cost, weekend, multi-city) set this to 1
	// because they only need the cheapest result, not 75 hotels.
	// 0 means use the default (maxPages).
	MaxPages int

	// FreeCancellation filters for hotels offering free cancellation when true.
	FreeCancellation bool

	// PropertyType restricts results to a specific property category.
	// Accepted values: "hotel", "apartment", "hostel", "resort", "bnb", "villa".
	// Empty string means no filter.
	PropertyType string

	// Brand filters results to hotels whose name contains the brand string
	// (case-insensitive). Applied as a client-side post-filter since Google
	// Hotels does not expose a server-side brand/chain parameter.
	// Examples: "hilton", "marriott", "ibis", "hyatt".
	Brand string

	// EcoCertified filters for hotels with sustainability certifications
	// (Google's "Eco-certified" badge). Applied server-side via the &ecof=1
	// URL parameter. When true, all returned hotels are marked EcoCertified.
	EcoCertified bool

	// Extended provider-specific filters passed through to external providers.
	MinBedrooms    int    // minimum bedrooms (Airbnb)
	MinBathrooms   int    // minimum bathrooms (Airbnb)
	MinBeds        int    // minimum beds (Airbnb)
	RoomType       string // "entire_home", "private_room", "shared_room", "hotel_room" (Airbnb)
	Superhost      bool   // Superhost-only (Airbnb)
	InstantBook    bool   // instant-bookable only (Airbnb)
	MaxDistanceM   int    // max distance from center in meters (Booking nflt=distance)
	Sustainable    bool   // eco/sustainable properties (Booking nflt=sustainable)
	MealPlan       bool   // breakfast/meal included (Booking nflt=mealplan)
	IncludeSoldOut bool   // include sold-out properties (Booking nflt=oos)
}

// SearchHotels searches for hotels in the given location.
//
// The location can be a city name, address, or any text that Google Travel
// accepts as a destination query. We fetch the Google Travel Hotels page
// directly and parse the embedded JSON data from AF_initDataCallback blocks.
func SearchHotels(ctx context.Context, location string, opts HotelSearchOptions) (*models.HotelSearchResult, error) {
	return SearchHotelsWithClient(ctx, DefaultClient(), location, opts)
}

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
		Location         string   `json:"location"`
		CheckIn          string   `json:"check_in"`
		CheckOut         string   `json:"check_out"`
		Guests           int      `json:"guests"`
		Stars            int      `json:"stars"`
		Sort             string   `json:"sort"`
		Currency         string   `json:"currency"`
		MinPrice         float64  `json:"min_price"`
		MaxPrice         float64  `json:"max_price"`
		MinRating        float64  `json:"min_rating"`
		MaxDistanceKm    float64  `json:"max_distance_km"`
		Amenities        []string `json:"amenities,omitempty"`
		CenterLat        float64  `json:"center_lat"`
		CenterLon        float64  `json:"center_lon"`
		EnrichAmenities  bool     `json:"enrich_amenities"`
		EnrichLimit      int      `json:"enrich_limit"`
		MaxPages         int      `json:"max_pages"`
		FreeCancellation bool     `json:"free_cancellation"`
		PropertyType     string   `json:"property_type"`
		Brand            string   `json:"brand"`
		EcoCertified     bool     `json:"eco_certified"`
		MinBedrooms      int      `json:"min_bedrooms"`
		MinBathrooms     int      `json:"min_bathrooms"`
		MinBeds          int      `json:"min_beds"`
		RoomType         string   `json:"room_type"`
		Superhost        bool     `json:"superhost"`
		InstantBook      bool     `json:"instant_book"`
		MaxDistanceM     int      `json:"max_distance_m"`
		Sustainable      bool     `json:"sustainable"`
		MealPlan         bool     `json:"meal_plan"`
		IncludeSoldOut   bool     `json:"include_sold_out"`
	}{
		Location:         location,
		CheckIn:          opts.CheckIn,
		CheckOut:         opts.CheckOut,
		Guests:           opts.Guests,
		Stars:            opts.Stars,
		Sort:             opts.Sort,
		Currency:         strings.ToUpper(opts.Currency),
		MinPrice:         opts.MinPrice,
		MaxPrice:         opts.MaxPrice,
		MinRating:        opts.MinRating,
		MaxDistanceKm:    opts.MaxDistanceKm,
		Amenities:        amenities,
		CenterLat:        opts.CenterLat,
		CenterLon:        opts.CenterLon,
		EnrichAmenities:  opts.EnrichAmenities,
		EnrichLimit:      opts.EnrichLimit,
		MaxPages:         hotelPageLimit(opts.MaxPages),
		FreeCancellation: opts.FreeCancellation,
		PropertyType:     opts.PropertyType,
		Brand:            strings.ToLower(strings.TrimSpace(opts.Brand)),
		EcoCertified:     opts.EcoCertified,
		MinBedrooms:      opts.MinBedrooms,
		MinBathrooms:     opts.MinBathrooms,
		MinBeds:          opts.MinBeds,
		RoomType:         opts.RoomType,
		Superhost:        opts.Superhost,
		InstantBook:      opts.InstantBook,
		MaxDistanceM:     opts.MaxDistanceM,
		Sustainable:      opts.Sustainable,
		MealPlan:         opts.MealPlan,
		IncludeSoldOut:   opts.IncludeSoldOut,
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

// SearchHotelsWithClient is like SearchHotels but reuses the provided client.
func SearchHotelsWithClient(ctx context.Context, client *batchexec.Client, location string, opts HotelSearchOptions) (*models.HotelSearchResult, error) {
	location = normalizeHotelCity(location)
	if opts.CheckIn == "" || opts.CheckOut == "" {
		return nil, fmt.Errorf("check-in and check-out dates are required")
	}
	if opts.Guests <= 0 {
		opts.Guests = 2
	}
	if opts.Currency == "" {
		opts.Currency = "USD" // Google's default when no currency specified
	} else {
		opts.Currency = strings.ToUpper(opts.Currency)
	}

	// Validate dates.
	_, err := parseDateArray(opts.CheckIn)
	if err != nil {
		return nil, fmt.Errorf("parse check-in date: %w", err)
	}
	_, err = parseDateArray(opts.CheckOut)
	if err != nil {
		return nil, fmt.Errorf("parse check-out date: %w", err)
	}

	key := hotelSearchKey(location, opts)
	return doHotelSearchSingleflight(ctx, key, func(sharedCtx context.Context) (*models.HotelSearchResult, error) {
		return searchHotelsCore(sharedCtx, client, location, opts)
	})
}

func doHotelSearchSingleflight(ctx context.Context, key string, fn func(context.Context) (*models.HotelSearchResult, error)) (*models.HotelSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ch := hotelGroup.DoChan(key, func() (any, error) {
		sharedCtx, cancel := searchctx.DetachedWithin(ctx, sharedHotelSearchTimeout)
		defer cancel()
		return fn(sharedCtx)
	})

	select {
	case <-ctx.Done():
		// The winner keeps running until the detached shared deadline expires; if
		// the caller times out first, forget the key so later callers do not join
		// that doomed execution.
		hotelGroup.Forget(key)
		return nil, ctx.Err()
	case res := <-ch:
		return sharedHotelResult(res.Val, res.Err)
	}
}

// searchHotelsCore performs the actual hotel search without singleflight wrapping.
func searchHotelsCore(ctx context.Context, client *batchexec.Client, location string, opts HotelSearchOptions) (*models.HotelSearchResult, error) {
	pageLimit := hotelPageLimit(opts.MaxPages)

	// Determine which sort orders to use. When MaxPages is 1 (compound
	// commands that only need the cheapest result), skip sort diversity.
	sortOrders := googleSortOrders
	if pageLimit <= 1 {
		sortOrders = []string{""}
	}

	// Check rate limit status and warn the user.
	// When throttled, requests may fail until the cooldown period elapses.
	// Use 'trvl rate-status' to check current provider status.
	if HotelRateManager.IsThrottled("google") {
		slog.Warn("Google Hotels is throttled — requests may fail until cooldown elapses (60s). Use 'trvl rate-status'.")
	}
	if HotelRateManager.IsThrottled("booking") {
		slog.Warn("Booking.com is throttled — requests may fail until cooldown elapses (60s). Use 'trvl rate-status'.")
	}

	var totalAvailable int
	// Accumulate raw results per-page; MergeHotelResults deduplicates at the end.
	var rawBatches [][]models.HotelResult

	for sortIdx, googleSort := range sortOrders {
		// Bail if context is already cancelled (tool timeout hit).
		if ctx.Err() != nil {
			break
		}
		// Brief cooldown between sort orders to avoid Google 429 rate limits.
		if sortIdx > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
		}

		// Fetch first page for this sort order (with metadata on the primary sort).
		firstPage, err := fetchHotelPageFull(ctx, client, location, opts, 0, googleSort)
		if err != nil {
			if sortIdx == 0 {
				// Primary sort failed — fatal.
				return nil, err
			}
			// Secondary sort failed — non-fatal, keep what we have.
			HotelRateManager.Record429("google")
			break
		}
		HotelRateManager.RecordRequest("google")

		if sortIdx == 0 {
			totalAvailable = firstPage.TotalAvailable
		}

		tagged := tagHotelSource(firstPage.Hotels, "google_hotels")
		rawBatches = append(rawBatches, tagged)

		// Paginate within this sort order.
		for page := 1; page < pageLimit; page++ {
			pageHotels, err := fetchHotelPage(ctx, client, location, opts, page*pageSize, googleSort)
			if err != nil {
				// Non-fatal: keep what we have from previous pages.
				break
			}
			if len(pageHotels) == 0 {
				// End of results for this sort order.
				break
			}
			rawBatches = append(rawBatches, tagHotelSource(pageHotels, "google_hotels"))
		}
	}

	// Run parallel searches against Trivago, optional Booking.com, and
	// user-configured external providers. All auxiliary providers are non-fatal:
	// failures log a warning and contribute zero results.
	auxOpts := HotelSearchOptions{
		CheckIn:  opts.CheckIn,
		CheckOut: opts.CheckOut,
		Guests:   opts.Guests,
		Currency: opts.Currency,
	}
	var trivagoResults []models.HotelResult
	var hometogoResults []models.HotelResult
	var bookingResults []models.HotelResult
	var externalResults []models.HotelResult
	var providerStatuses []models.ProviderStatus
	var auxWg sync.WaitGroup

	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		res, err := SearchTrivago(ctx, location, auxOpts)
		if err != nil {
			slog.Warn("trivago search failed", "error", err)
			return
		}
		trivagoResults = res
	}()

	// HomeToGo vacation-rental aggregator (Airbnb/Vrbo/Booking + local hosts).
	// Non-fatal: failures log a warning and contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		res, err := SearchHomeToGo(ctx, location, auxOpts)
		if err != nil {
			slog.Warn("hometogo search failed", "error", err)
			return
		}
		hometogoResults = res
	}()

	// Booking.com search — parallel with Google + Trivago + HomeToGo.
	// Booking.com uses AWS WAF which blocks automated requests. The search
	// is attempted but failures are expected and handled silently — the
	// function falls back gracefully to Google + Trivago results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		res, err := SearchBooking(ctx, location, auxOpts)
		if err != nil {
			slog.Debug("booking search failed", "error", err)
			return
		}
		if len(res) > 0 {
			bookingResults = tagHotelSource(res, "booking.com")
		}
	}()

	// External providers (user-configured via configure_provider MCP tool).
	// This includes any provider the user has set up: Booking.com, Airbnb,
	// Hostelworld, VRBO, etc. — all configured through the provider system.
	if eprt := getExternalProviderRuntime(); eprt != nil {
		auxWg.Add(1)
		go func() {
			defer auxWg.Done()
			lat, lon, err := ResolveLocation(ctx, location)
			if err != nil {
				slog.Warn("external providers: geocode failed", "error", err)
				return
			}
			filters := &providers.HotelFilterParams{
				MinPrice:         opts.MinPrice,
				MaxPrice:         opts.MaxPrice,
				PropertyType:     opts.PropertyType,
				Sort:             opts.Sort,
				Stars:            opts.Stars,
				MinRating:        opts.MinRating,
				Amenities:        opts.Amenities,
				FreeCancellation: opts.FreeCancellation,
				MinBedrooms:      opts.MinBedrooms,
				MinBathrooms:     opts.MinBathrooms,
				MinBeds:          opts.MinBeds,
				RoomType:         opts.RoomType,
				Superhost:        opts.Superhost,
				InstantBook:      opts.InstantBook,
				MaxDistanceM:     opts.MaxDistanceM,
				Sustainable:      opts.Sustainable,
				MealPlan:         opts.MealPlan,
				IncludeSoldOut:   opts.IncludeSoldOut,
			}
			res, statuses, err := eprt.SearchHotels(ctx, location, lat, lon,
				auxOpts.CheckIn, auxOpts.CheckOut, auxOpts.Currency, auxOpts.Guests, filters)
			if err != nil {
				slog.Warn("external providers search failed", "error", err)
				providerStatuses = statuses // keep statuses even on error
				return
			}
			externalResults = res
			providerStatuses = statuses
		}()
	}

	auxWg.Wait()

	// Deduplicate across all pages, sort orders, Trivago, and external
	// providers using name-normalisation + geo-proximity. MergeHotelResults
	// preserves all provider price sources and keeps the lowest price as the
	// primary.
	allBatches := append(rawBatches, trivagoResults)
	allBatches = append(allBatches, tagHotelSource(hometogoResults, "hometogo"))
	allBatches = append(allBatches, bookingResults)
	allBatches = append(allBatches, externalResults)
	if len(externalResults) > 0 {
		slog.Info("external providers contributed results", "count", len(externalResults))
	}
	hotels := models.MergeHotelResults(allBatches...)

	// Resolve city center coordinates. Used for distance filter/sort and
	// for computing DistanceKm on every hotel (useful info for the user
	// even when no distance filter is active).
	if opts.CenterLat == 0 && opts.CenterLon == 0 {
		lat, lon, err := ResolveLocation(ctx, location)
		if err == nil {
			opts.CenterLat = lat
			opts.CenterLon = lon
		} else {
			slog.Warn("geocode failed, falling back to hotel median", "location", location, "error", err)
			// Fallback: use the median of hotel coordinates as the center.
			// This gives a reasonable approximation when the external geocoder
			// is unavailable (rate-limited, network error, etc.).
			if lat, lon, ok := medianHotelCoords(hotels); ok {
				opts.CenterLat = lat
				opts.CenterLon = lon
			}
		}
	}

	// Log pre-filter counts by source for debugging merge visibility.
	if len(externalResults) > 0 {
		slog.Info("pre-filter hotel counts",
			"total", len(hotels),
			"google", countBySource(hotels, "google_hotels"),
			"trivago", countBySource(hotels, "trivago"),
			"external", countExternalSources(hotels),
		)
	}

	// Apply post-filters.
	hotels = filterHotels(hotels, opts)

	if len(externalResults) > 0 {
		slog.Info("post-filter hotel counts",
			"total", len(hotels),
			"external_surviving", countExternalSources(hotels),
		)
	}

	// Sort results.
	sortHotels(hotels, opts.Sort, opts.CenterLat, opts.CenterLon)

	// Compute distance from city center for every hotel with coordinates.
	// This is always useful context for the user, not just for filtering.
	if opts.CenterLat != 0 || opts.CenterLon != 0 {
		for i := range hotels {
			if hotels[i].Lat != 0 || hotels[i].Lon != 0 {
				hotels[i].DistanceKm = Haversine(opts.CenterLat, opts.CenterLon, hotels[i].Lat, hotels[i].Lon)
			}
		}
	}

	// Enrich top hotels with full amenity data from detail pages.
	if opts.EnrichAmenities {
		hotels = enrichHotelAmenities(ctx, hotels, opts.EnrichLimit)
	}

	// When the eco-certified filter is active, all returned hotels have
	// Google's sustainability certification — mark them accordingly.
	if opts.EcoCertified {
		for i := range hotels {
			hotels[i].EcoCertified = true
		}
	}

	// Ensure every hotel has an openable booking URL without overwriting
	// provider-specific URLs that were already attached during source merges.
	for i := range hotels {
		if hotels[i].BookingURL == "" {
			hotels[i].BookingURL = buildHotelBookingURL(location, opts.CheckIn, opts.CheckOut)
		}
	}

	// Mark adults-only properties so renderers and child-aware filters can act
	// on them. Detection is centralised here so every provider path benefits.
	for i := range hotels {
		if models.IsAdultsOnly(hotels[i].Name, hotels[i].Description) {
			hotels[i].AdultsOnly = true
		}
	}

	return &models.HotelSearchResult{
		Success:          true,
		Count:            len(hotels),
		TotalAvailable:   totalAvailable,
		Hotels:           hotels,
		ProviderStatuses: providerStatuses,
	}, nil
}

func cloneHotelSearchResult(shared *models.HotelSearchResult) *models.HotelSearchResult {
	if shared == nil {
		return nil
	}

	// singleflight.Do shares the winner's *HotelSearchResult pointer across all
	// callers. Trip planning and preference filters rewrite Count / Hotels and may
	// mutate nested hotel slices, so each caller needs an independent deep copy.
	cp := *shared
	if shared.Hotels != nil {
		cp.Hotels = make([]models.HotelResult, len(shared.Hotels))
		for i, hotel := range shared.Hotels {
			hotelCopy := hotel
			if hotel.Amenities != nil {
				hotelCopy.Amenities = append([]string(nil), hotel.Amenities...)
			}
			if hotel.RoomTypes != nil {
				hotelCopy.RoomTypes = make([]models.Room, len(hotel.RoomTypes))
				for j, room := range hotel.RoomTypes {
					roomCopy := room
					if room.Amenities != nil {
						roomCopy.Amenities = append([]string(nil), room.Amenities...)
					}
					hotelCopy.RoomTypes[j] = roomCopy
				}
			}
			if hotel.Sources != nil {
				hotelCopy.Sources = append([]models.PriceSource(nil), hotel.Sources...)
			}
			cp.Hotels[i] = hotelCopy
		}
	}
	if shared.ProviderStatuses != nil {
		cp.ProviderStatuses = append([]models.ProviderStatus(nil), shared.ProviderStatuses...)
	}
	return &cp
}

func sharedHotelResult(v any, err error) (*models.HotelSearchResult, error) {
	if err != nil {
		if r, ok := v.(*models.HotelSearchResult); ok {
			return cloneHotelSearchResult(r), err
		}
		return nil, err
	}
	return cloneHotelSearchResult(v.(*models.HotelSearchResult)), nil
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
	if ptype := propertyTypeCode(opts.PropertyType); ptype != "" {
		query.Set("ptype", ptype)
	}
	if opts.EcoCertified {
		query.Set("ecof", "1")
	}

	return "https://www.google.com/travel/hotels/" + encoded + "?" + query.Encode()
}

// filterHotels applies all post-fetch filters to hotel results.
func filterHotels(hotels []models.HotelResult, opts HotelSearchOptions) []models.HotelResult {
	filtered := hotels[:0]
	for _, h := range hotels {
		// Stars filter: h.Stars==0 means Google didn't annotate this hotel
		// with star data (~92% of hotels). Pass those through rather than
		// treating "unknown" as "zero stars".
		if opts.Stars > 0 && h.Stars > 0 && h.Stars < opts.Stars {
			continue
		}
		if opts.MinPrice > 0 && h.Price > 0 && h.Price < opts.MinPrice {
			continue
		}
		if opts.MaxPrice > 0 && h.Price > 0 && h.Price > opts.MaxPrice {
			continue
		}
		// Rating filter: when MinRating is set, require rating data AND that
		// it meets the minimum. However, external-provider results (Airbnb,
		// Booking.com, Hostelworld) often lack a Google-scale rating — pass
		// those through rather than dropping valuable cross-provider results.
		if opts.MinRating > 0 {
			if h.Rating > 0 && h.Rating < opts.MinRating {
				continue
			}
			if h.Rating == 0 && !models.HasExternalProviderSource(h) {
				continue
			}
		}
		if h.Lat != 0 && h.Lon != 0 && opts.CenterLat != 0 {
			dist := Haversine(opts.CenterLat, opts.CenterLon, h.Lat, h.Lon)
			// Hard geo-outlier ceiling: reject hotels >100km from city
			// center. External providers (Airbnb) sometimes return
			// promoted listings from completely different cities.
			if dist > 100 {
				continue
			}
			if opts.MaxDistanceKm > 0 && dist > opts.MaxDistanceKm {
				continue
			}
		}
		if len(opts.Amenities) > 0 && !hasAllAmenities(h.Amenities, opts.Amenities) {
			continue
		}
		if opts.Brand != "" && !strings.Contains(strings.ToLower(h.Name), strings.ToLower(opts.Brand)) {
			continue
		}
		filtered = append(filtered, h)
	}
	return filtered
}

// filterByStars removes hotels below the requested star rating.
// Hotels with Stars==0 (no star data from Google) are kept, since "unknown"
// should not be treated as "zero stars".
func filterByStars(hotels []models.HotelResult, minStars int) []models.HotelResult {
	return filterHotels(hotels, HotelSearchOptions{Stars: minStars})
}

// hasAllAmenities returns true if the hotel's amenities contain every
// requested amenity (case-insensitive substring match).
func hasAllAmenities(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, a := range have {
		set[strings.ToLower(a)] = true
	}
	for _, req := range want {
		if !set[strings.ToLower(strings.TrimSpace(req))] {
			return false
		}
	}
	return true
}

// sortHotels sorts hotel results in-place by the given criteria.
func sortHotels(hotels []models.HotelResult, sortBy string, centerLat, centerLon float64) {
	switch strings.ToLower(sortBy) {
	case "cheapest", "price", "":
		// Sort by price ascending. Hotels with price=0 go to the end.
		sort.Slice(hotels, func(i, j int) bool {
			return lessPrice(hotels[i], hotels[j])
		})
	case "rating":
		// Sort by rating descending.
		sort.Slice(hotels, func(i, j int) bool {
			return hotels[i].Rating > hotels[j].Rating
		})
	case "stars":
		// Sort by star rating descending.
		sort.Slice(hotels, func(i, j int) bool {
			return hotels[i].Stars > hotels[j].Stars
		})
	case "distance":
		// Sort by distance from city center ascending.
		if centerLat != 0 || centerLon != 0 {
			sort.Slice(hotels, func(i, j int) bool {
				di := Haversine(centerLat, centerLon, hotels[i].Lat, hotels[i].Lon)
				dj := Haversine(centerLat, centerLon, hotels[j].Lat, hotels[j].Lon)
				return di < dj
			})
		}
	}
}

// medianHotelCoords computes the median lat/lon from hotels that have
// coordinates. Used as a fallback center when the geocoder is unavailable.
func medianHotelCoords(hotels []models.HotelResult) (lat, lon float64, ok bool) {
	var lats, lons []float64
	for _, h := range hotels {
		if h.Lat != 0 || h.Lon != 0 {
			lats = append(lats, h.Lat)
			lons = append(lons, h.Lon)
		}
	}
	if len(lats) == 0 {
		return 0, 0, false
	}
	sort.Float64s(lats)
	sort.Float64s(lons)
	mid := len(lats) / 2
	return lats[mid], lons[mid], true
}

// Haversine returns the great-circle distance in kilometers between two
// points specified in decimal degrees.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*math.Cos(degreesToRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}

func degreesToRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

func lessPrice(a, b models.HotelResult) bool {
	if a.Price == 0 {
		return false
	}
	if b.Price == 0 {
		return true
	}
	return a.Price < b.Price
}

func countBySource(hotels []models.HotelResult, provider string) int {
	count := 0
	for _, h := range hotels {
		for _, s := range h.Sources {
			if s.Provider == provider {
				count++
				break
			}
		}
	}
	return count
}

func countExternalSources(hotels []models.HotelResult) int {
	count := 0
	for _, h := range hotels {
		if models.HasExternalProviderSource(h) {
			count++
		}
	}
	return count
}

// parseDateArray converts "YYYY-MM-DD" to [year, month, day].
func parseDateArray(s string) ([3]int, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return [3]int{}, fmt.Errorf("invalid date %q: expected YYYY-MM-DD", s)
	}
	return [3]int{t.Year(), int(t.Month()), t.Day()}, nil
}

// SearchHotelByName searches for a specific hotel by name and returns its details.
// Unlike SearchHotels (which searches by area), this uses Google's entity
// resolution to find a specific property via name matching within search results.
//
// Strategy: Google Hotels returns listings when searching by city/area, not hotel
// names. We extract a location context from the query (text after comma, or last
