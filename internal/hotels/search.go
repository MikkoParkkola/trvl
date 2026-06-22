package hotels

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/fareintel"
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

const (
	sharedHotelSearchTimeout = 60 * time.Second
	hotelAuxProviderTimeout  = 10 * time.Second
)

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
	CheckIn      string // YYYY-MM-DD
	CheckOut     string // YYYY-MM-DD
	Guests       int
	ChildrenAges []int
	Rooms        int
	Stars        int    // 0 = any, 2-5 filter
	Sort         string // "cheapest", "rating", "distance", "stars"
	Currency     string // default "USD"

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

	// RefundableRequired filters for refundable rates when provider support
	// exists. Providers that cannot pre-filter still expose refundability in
	// room-level offers so callers can reject non-matching rates.
	RefundableRequired bool

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

	// Need-level amenities that should survive into provider/detail searches.
	MustHaveKitchen   bool
	MustHaveWifi      bool
	MustHaveWorkspace bool
}

// SearchHotels searches for hotels in the given location.
//
// The location can be a city name, address, or any text that Google Travel
// accepts as a destination query. We fetch the Google Travel Hotels page
// directly and parse the embedded JSON data from AF_initDataCallback blocks.
func SearchHotels(ctx context.Context, location string, opts HotelSearchOptions) (*models.HotelSearchResult, error) {
	return SearchHotelsWithClient(ctx, DefaultClient(), location, opts)
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
	var providerStatuses []models.ProviderStatus
	var providerStatusesMu sync.Mutex
	addProviderStatus := func(status models.ProviderStatus) {
		providerStatusesMu.Lock()
		providerStatuses = append(providerStatuses, status)
		providerStatusesMu.Unlock()
	}
	addProviderStatuses := func(statuses []models.ProviderStatus) {
		if len(statuses) == 0 {
			return
		}
		providerStatusesMu.Lock()
		providerStatuses = append(providerStatuses, statuses...)
		providerStatusesMu.Unlock()
	}

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
	addProviderStatus(hotelProviderStatusFromResults("google_hotels", "Google Hotels", countHotelBatchResults(rawBatches)))

	// Run parallel searches against Trivago, optional Booking.com, and
	// user-configured external providers. All auxiliary providers are non-fatal:
	// failures log a warning and contribute zero results.
	auxOpts := opts
	var trivagoResults []models.HotelResult
	var hometogoResults []models.HotelResult
	var anyplaceResults []models.HotelResult
	var uniplacesResults []models.HotelResult
	var wunderflatsResults []models.HotelResult
	var housinganywhereResults []models.HotelResult
	var landingResults []models.HotelResult
	var spotahomeResults []models.HotelResult
	var flatioResults []models.HotelResult
	var bluegroundResults []models.HotelResult
	var agodaResults []models.HotelResult
	var bookingResults []models.HotelResult
	var externalResults []models.HotelResult
	var auxWg sync.WaitGroup

	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchTrivago(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("trivago search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("trivago", "Trivago", err))
			return
		}
		trivagoResults = res
		addProviderStatus(hotelProviderStatusFromResults("trivago", "Trivago", len(res)))
	}()

	// HomeToGo vacation-rental aggregator (Airbnb/Vrbo/Booking + local hosts).
	// Non-fatal: failures log a warning and contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchHomeToGo(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("hometogo search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("hometogo", "HomeToGo", err))
			return
		}
		hometogoResults = res
		addProviderStatus(hotelProviderStatusFromResults("hometogo", "HomeToGo", len(res)))
	}()

	// Anyplace mid-term / nomad furnished-apartment provider (monthly-priced
	// furnished rentals, 30-night minimum — the relocation/nomad segment).
	// Non-fatal: failures log a warning and contribute zero results.
	// Wunderflats mid-term / furnished-apartment provider (monthly rentals that
	// hotel and short-term sources miss). Non-fatal: failures log a warning and
	// contribute zero results.
	// HousingAnywhere mid-term rental marketplace (furnished monthly lettings).
	// Algolia-backed; credentials are runtime-harvested. Non-fatal: failures log
	// a warning and contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchAnyplace(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("anyplace search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("anyplace", "Anyplace", err))
			return
		}
		anyplaceResults = res
		addProviderStatus(hotelProviderStatusFromResults("anyplace", "Anyplace", len(res)))
	}()

	// Uniplaces mid-term / student-housing provider (rooms, studios, apartments
	// for weeks-to-months stays that nightly-rate providers miss). Non-fatal:
	// failures log a warning and contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchUniplaces(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("uniplaces search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("uniplaces", "Uniplaces", err))
			return
		}
		uniplacesResults = res
		addProviderStatus(hotelProviderStatusFromResults("uniplaces", "Uniplaces", len(res)))
	}()

	// Wunderflats mid-term furnished-apartment provider (Germany & Europe,
	// monthly-priced furnished flats). Non-fatal: failures log a warning and
	// Landing (hellolanding.com) furnished mid-term apartment provider.
	// US month-to-month furnished apartments — fills the monthly-stay gap that
	// hotel/short-let providers miss. Non-fatal: failures log a warning and
	// contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchWunderflats(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("wunderflats search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("wunderflats", "Wunderflats", err))
			return
		}
		wunderflatsResults = res
		addProviderStatus(hotelProviderStatusFromResults("wunderflats", "Wunderflats", len(res)))
	}()

	// HousingAnywhere mid-term marketplace (largest EU furnished-rental
	// inventory, Algolia-backed). Non-fatal: failures log a warning and
	// contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchHousingAnywhere(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("housinganywhere search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("housinganywhere", "HousingAnywhere", err))
			return
		}
		housinganywhereResults = res
		addProviderStatus(hotelProviderStatusFromResults("housinganywhere", "HousingAnywhere", len(res)))
	}()

	// Landing (hellolanding.com) US furnished month-to-month apartments.
	// Non-fatal: failures log a warning and contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchLanding(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("landing search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("landing", "Landing", err))
			return
		}
		landingResults = res
		addProviderStatus(hotelProviderStatusFromResults("landing", "Landing", len(res)))
	}()

	// Spotahome mid-term furnished apartments (turbo-stream single-fetch .data).
	// Non-fatal: failures log a warning and contribute zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchSpotahome(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("spotahome search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("spotahome", "Spotahome", err))
			return
		}
		spotahomeResults = res
		addProviderStatus(hotelProviderStatusFromResults("spotahome", "Spotahome", len(res)))
	}()

	// Flatio monthly furnished apartments (SSR markerData JSON). Non-fatal.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchFlatio(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("flatio search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("flatio", "Flatio", err))
			return
		}
		flatioResults = res
		addProviderStatus(hotelProviderStatusFromResults("flatio", "Flatio", len(res)))
	}()

	// Blueground monthly furnished apartments (__INITIAL_STATE__ list + detail
	// hop for price). Non-fatal: failures log a warning and contribute zero.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchBlueground(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("blueground search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("blueground", "Blueground", err))
			return
		}
		bluegroundResults = res
		addProviderStatus(hotelProviderStatusFromResults("blueground", "Blueground", len(res)))
	}()

	// Agoda OTA hotel search (GraphQL citySearch; resolve cityId via public
	// autocomplete, then a self-constructed x-gate-meta header — no API key,
	// cookies, or signature). Non-fatal: failures log a warning and contribute
	// zero results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchAgoda(providerCtx, location, auxOpts)
		if err != nil {
			slog.Warn("agoda search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("agoda", "Agoda", err))
			return
		}
		agodaResults = res
		addProviderStatus(hotelProviderStatusFromResults("agoda", "Agoda", len(res)))
	}()

	// Booking.com search — parallel with Google + Trivago + HomeToGo.
	// Booking.com uses AWS WAF which blocks automated requests. The search
	// is attempted but failures are expected and handled silently — the
	// function falls back gracefully to Google + Trivago results.
	auxWg.Add(1)
	go func() {
		defer auxWg.Done()
		providerCtx, cancel := context.WithTimeout(ctx, hotelAuxProviderTimeout)
		defer cancel()
		res, err := SearchBooking(providerCtx, location, auxOpts)
		if err != nil {
			slog.Debug("booking search failed", "error", err)
			addProviderStatus(hotelProviderStatusFromError("booking", "Booking.com", err))
			return
		}
		if len(res) > 0 {
			bookingResults = tagHotelSource(res, "booking.com")
		}
		addProviderStatus(hotelProviderStatusFromResults("booking", "Booking.com", len(res)))
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
				MinPrice:          opts.MinPrice,
				MaxPrice:          opts.MaxPrice,
				PropertyType:      opts.PropertyType,
				Sort:              opts.Sort,
				Stars:             opts.Stars,
				MinRating:         opts.MinRating,
				Amenities:         opts.Amenities,
				FreeCancellation:  opts.FreeCancellation,
				Refundable:        opts.RefundableRequired,
				ChildrenAges:      opts.ChildrenAges,
				Rooms:             opts.Rooms,
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
			res, statuses, err := eprt.SearchHotels(ctx, location, lat, lon,
				auxOpts.CheckIn, auxOpts.CheckOut, auxOpts.Currency, auxOpts.Guests, filters)
			if err != nil {
				slog.Warn("external providers search failed", "error", err)
				addProviderStatuses(statuses) // keep statuses even on error
				return
			}
			externalResults = res
			addProviderStatuses(statuses)
		}()
	}

	auxWg.Wait()

	// Deduplicate across all pages, sort orders, Trivago, and external
	// providers using name-normalisation + geo-proximity. MergeHotelResults
	// preserves all provider price sources and keeps the lowest price as the
	// primary.
	allBatches := append(rawBatches, trivagoResults)
	allBatches = append(allBatches, tagHotelSource(hometogoResults, "hometogo"))
	allBatches = append(allBatches, tagHotelSource(anyplaceResults, "anyplace"))
	allBatches = append(allBatches, tagHotelSource(uniplacesResults, "uniplaces"))
	allBatches = append(allBatches, tagHotelSource(wunderflatsResults, "wunderflats"))
	allBatches = append(allBatches, tagHotelSource(housinganywhereResults, "housinganywhere"))
	allBatches = append(allBatches, tagHotelSource(landingResults, "landing"))
	allBatches = append(allBatches, tagHotelSource(spotahomeResults, "spotahome"))
	allBatches = append(allBatches, tagHotelSource(flatioResults, "flatio"))
	allBatches = append(allBatches, tagHotelSource(bluegroundResults, "blueground"))
	allBatches = append(allBatches, tagHotelSource(agodaResults, "agoda"))
	allBatches = append(allBatches, bookingResults)
	allBatches = append(allBatches, externalResults)
	if len(externalResults) > 0 {
		slog.Info("external providers contributed results", "count", len(externalResults))
	}
	hotels := models.MergeHotelResults(allBatches...)
	models.FinalizeHotelPriceTrust(hotels, opts.Currency, time.Now())

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

	providerStatuses = coalesceProviderStatuses(providerStatuses)

	annotateHotelConfidence(hotels, time.Now())

	return &models.HotelSearchResult{
		Success:          true,
		Count:            len(hotels),
		TotalAvailable:   totalAvailable,
		Hotels:           hotels,
		ProviderStatuses: providerStatuses,
		Completeness:     models.ComputeCompleteness(providerStatuses),
	}, nil
}

// annotateHotelConfidence attaches an honest bookability Confidence to every
// hotel (innovation #3), scored via the shared fareintel scorer from the
// property's price freshness, provider reliability, multi-source corroboration,
// and price-basis verification. Hotels with no usable signal are left unrated,
// never faked.
func annotateHotelConfidence(hotels []models.HotelResult, now time.Time) {
	for i := range hotels {
		h := &hotels[i]
		provider := h.CheapestSource
		if provider == "" && len(h.Sources) > 0 {
			provider = h.Sources[0].Provider
		}
		retrievedAt := h.RetrievedAt
		if retrievedAt.IsZero() {
			retrievedAt = now
		}
		c := fareintel.ScoreConfidence(fareintel.ConfidenceInput{
			Price:             h.Price,
			Currency:          h.Currency,
			Provider:          provider,
			RetrievedAt:       retrievedAt,
			Now:               now,
			Sources:           h.Sources,
			PriceVerification: h.PriceConfidence,
		})
		h.Confidence = &c
	}
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
			if hotel.PriceWarnings != nil {
				hotelCopy.PriceWarnings = append([]string(nil), hotel.PriceWarnings...)
			}
			cp.Hotels[i] = hotelCopy
		}
	}
	if shared.ProviderStatuses != nil {
		cp.ProviderStatuses = append([]models.ProviderStatus(nil), shared.ProviderStatuses...)
	}
	if shared.Completeness.Missing != nil {
		cp.Completeness.Missing = append([]string(nil), shared.Completeness.Missing...)
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
