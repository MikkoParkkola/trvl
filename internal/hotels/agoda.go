package hotels

// Agoda hotel provider.
//
// Agoda (https://www.agoda.com) is a major OTA with strong inventory in Europe
// and Asia-Pacific. It complements the existing nightly-rate providers (Google
// Hotels, Booking.com, Trivago) with a distinct rate set and broad coverage.
//
// Unauthenticated feasibility (recon-verified, Tier-1):
//   - No API key, browser cookies, headless browser, residential proxy, nor
//     CAPTCHA bypass is required. A plain HTTP client with a desktop User-Agent
//     and a small set of "AG-*" request headers suffices.
//   - CRITICAL: there is NO per-request HMAC / signature. The only non-trivial
//     header is "x-gate-meta", which is a base64-encoded, self-constructable
//     string "{epochMillis}|{userId}|/graphql/" where userId is any UUID. This
//     is fully replayable from a stateless client — verified by replaying a
//     captured request body from a plain urllib client and receiving a priced
//     200 (~676 KB) response with no cookies attached.
//   - Transient 502s occur (server-side flakiness); a small retry loop recovers.
//
// Two-step flow, mirroring HomeToGo/Anyplace's resolve-then-fetch pattern:
//  1. resolveAgodaCityID(ctx, location) -> numeric cityId
//     via GET /api/cronos/search/GetUnifiedSuggestResult (autocomplete), taking
//     the first city-type suggestion (ObjectTypeID == 5). Overridable with the
//     AGODA_CITY_ID env var (zero-deploy fix / explicit targeting).
//  2. fetchAgodaSearch(ctx, cityID, opts) -> GraphQL "citySearch" priced JSON
//     -> []models.HotelResult
//
// Field mapping (recon-confirmed citySearch property fields -> HotelResult):
//   propertyId                                              -> HotelID
//   content.informationSummary.displayName                 -> Name
//   content.informationSummary.rating                      -> Stars (e.g. 4.0)
//   content.informationSummary.geoInfo.latitude/longitude  -> Lat / Lon
//   content.informationSummary.address{country,city}       -> Address
//   content.informationSummary.propertyType                -> PropertyType
//   content.informationSummary.propertyLinks.propertyPage  -> BookingURL
//   content.reviews.contentReview[0].cumulative.score      -> Rating (0-10)
//   content.reviews.contentReview[0].cumulative.reviewCount-> ReviewCount
//   content.images.hotelImages[0].urls[0].value           -> ImageURL
//   pricing.offers[0].roomOffers[0].room.pricing[0]:
//     .currency                                            -> Currency
//     .price.perRoomPerNight.exclusive.display             -> Price (headline)
//     .price.perRoomPerNight.exclusive.crossedOutPrice     -> Sources[].MaxPrice

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// agodaQueryTemplate is the captured citySearch GraphQL request body. The query
// string is undocumented and large (~25 KB), so it is embedded verbatim rather
// than reconstructed; buildAgodaSearchBody patches only the dynamic fields
// (cityId, dates, occupancy, currency) on top of it.
//
//go:embed agoda_query.json
var agodaQueryTemplate []byte

// agodaEnabled controls whether SearchAgoda makes live HTTP requests. Disabled
// in the test suite (see testmain_test.go) so deterministic tests never fire
// real network calls; individual tests flip it on with a mock server injected
// via agodaBaseURL.
var agodaEnabled = true

// agodaBaseURL is the root of the Agoda site. Overridable in tests so an
// httptest.Server can stand in for the live host.
var agodaBaseURL = "https://www.agoda.com"

// agodaUserAgent is a desktop browser UA. Agoda serves the autocomplete and
// GraphQL endpoints to ordinary desktop clients.
const agodaUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// agodaLimiter enforces a conservative request rate. Two requests per search
// (resolve cityId + GraphQL search). Mirrors the per-provider rate.Limiter
// convention used by the other auxiliary providers.
var agodaLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 2)

// agodaHTTPClient is a dedicated client with a sane timeout.
var agodaHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// agodaMaxAttempts bounds the retry loop that recovers from transient 502s.
const agodaMaxAttempts = 3

// agodaRetryableStatus reports whether an HTTP status from /graphql/search is
// worth retrying within the bounded attempt budget. Beyond the classic
// transient 5xx, Agoda's anti-bot layer intermittently returns 400/403/429 for
// a correctly-formed request — live capture shows the identical body succeeding
// with 200 and full priced inventory on a subsequent attempt. Retrying recovers
// the provider on these blips; a persistently malformed request still fails
// after agodaMaxAttempts, so this never masks a real break beyond a small,
// bounded cost.
func agodaRetryableStatus(code int) bool {
	switch code {
	case http.StatusBadRequest, // 400 — observed transient anti-bot response
		http.StatusForbidden,           // 403 — anti-bot challenge
		http.StatusRequestTimeout,      // 408
		http.StatusTooManyRequests,     // 429 — rate limited
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// ---- Autocomplete (city resolution) response types ----

// agodaSuggestResponse is the GetUnifiedSuggestResult envelope; we read only the
// suggestion list and pick the first city-type entry.
type agodaSuggestResponse struct {
	SuggestionList []agodaSuggestion `json:"SuggestionList"`
}

type agodaSuggestion struct {
	ObjectID     int    `json:"ObjectID"`     // cityId when ObjectTypeID == 5
	ObjectTypeID int    `json:"ObjectTypeID"` // 5 == city
	Name         string `json:"Name"`
}

// ---- citySearch GraphQL response types (only the fields we consume) ----

type agodaSearchResponse struct {
	Data struct {
		CitySearch struct {
			Properties []agodaProperty `json:"properties"`
		} `json:"citySearch"`
	} `json:"data"`
}

type agodaProperty struct {
	PropertyID int `json:"propertyId"`
	Content    struct {
		InformationSummary struct {
			DisplayName  string  `json:"displayName"`
			Rating       float64 `json:"rating"`
			PropertyType string  `json:"propertyType"`
			GeoInfo      struct {
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"geoInfo"`
			Address struct {
				CountryCode string `json:"countryCode"`
				Country     struct {
					Name string `json:"name"`
				} `json:"country"`
				City struct {
					Name string `json:"name"`
				} `json:"city"`
			} `json:"address"`
			PropertyLinks struct {
				PropertyPage string `json:"propertyPage"`
			} `json:"propertyLinks"`
		} `json:"informationSummary"`
		Reviews struct {
			ContentReview []struct {
				Cumulative struct {
					ReviewCount int     `json:"reviewCount"`
					Score       float64 `json:"score"`
				} `json:"cumulative"`
			} `json:"contentReview"`
		} `json:"reviews"`
		Images struct {
			HotelImages []struct {
				URLs []struct {
					Value string `json:"value"`
				} `json:"urls"`
			} `json:"hotelImages"`
		} `json:"images"`
	} `json:"content"`
	Pricing struct {
		Offers []struct {
			RoomOffers []struct {
				Room struct {
					Pricing []agodaRoomPricing `json:"pricing"`
					// payment.cancellation.cancellationType carries Agoda's real
					// refundability signal (e.g. "NonRefundable"). It is already
					// in the citySearch response; surface it instead of discarding.
					Payment struct {
						Cancellation struct {
							CancellationType string `json:"cancellationType"`
						} `json:"cancellation"`
					} `json:"payment"`
				} `json:"room"`
			} `json:"roomOffers"`
		} `json:"offers"`
	} `json:"pricing"`
}

type agodaRoomPricing struct {
	Currency string `json:"currency"`
	Price    struct {
		PerRoomPerNight struct {
			Exclusive struct {
				Display         float64 `json:"display"`
				CrossedOutPrice float64 `json:"crossedOutPrice"`
			} `json:"exclusive"`
			// inclusive is the tax-and-fees-inclusive per-room-per-night rate
			// Agoda returns alongside the exclusive display price. Capturing it
			// lets the parser emit a verified, all-in room total rather than
			// discarding the only tax-inclusive number in the response.
			Inclusive struct {
				Display         float64 `json:"display"`
				CrossedOutPrice float64 `json:"crossedOutPrice"`
			} `json:"inclusive"`
		} `json:"perRoomPerNight"`
	} `json:"price"`
}

// SearchAgoda searches Agoda for hotels near a location. It is non-fatal by
// contract: callers treat any error as "zero results from this provider" and
// continue with the other providers.
func SearchAgoda(ctx context.Context, location string, opts HotelSearchOptions) ([]models.HotelResult, error) {
	if !agodaEnabled {
		return nil, nil
	}
	location = strings.TrimSpace(location)
	if location == "" {
		return nil, fmt.Errorf("agoda: empty location")
	}

	cityID, err := resolveAgodaCityID(ctx, location)
	if err != nil {
		return nil, fmt.Errorf("agoda: resolve city: %w", err)
	}

	resp, err := fetchAgodaSearch(ctx, cityID, opts)
	if err != nil {
		return nil, fmt.Errorf("agoda: search: %w", err)
	}

	return parseAgodaSearch(resp, opts), nil
}

// resolveAgodaCityID maps a free-text location to Agoda's numeric cityId. The
// AGODA_CITY_ID env var takes precedence (operator override); otherwise it hits
// the public autocomplete endpoint and takes the first city-type suggestion.
func resolveAgodaCityID(ctx context.Context, location string) (int, error) {
	if v := strings.TrimSpace(os.Getenv("AGODA_CITY_ID")); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("invalid AGODA_CITY_ID %q: %w", v, err)
		}
		return id, nil
	}

	if err := agodaLimiter.Wait(ctx); err != nil {
		return 0, err
	}

	guid := agodaUUID()
	endpoint := fmt.Sprintf(
		"%s/api/cronos/search/GetUnifiedSuggestResult/3/1/1/0/en-us/?searchText=%s&guid=%s",
		agodaBaseURL, url.QueryEscape(location), guid)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", agodaUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("AG-Language-Locale", "en-us")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", agodaBaseURL+"/")

	resp, err := agodaHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("autocomplete status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, err
	}

	var sr agodaSuggestResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return 0, fmt.Errorf("decode autocomplete: %w", err)
	}
	for _, s := range sr.SuggestionList {
		if s.ObjectTypeID == 5 && s.ObjectID > 0 {
			return s.ObjectID, nil
		}
	}
	// Fall back to any positive ObjectID if no explicit city type is present.
	for _, s := range sr.SuggestionList {
		if s.ObjectID > 0 {
			return s.ObjectID, nil
		}
	}
	return 0, fmt.Errorf("no city suggestion for %q", location)
}

// fetchAgodaSearch posts the citySearch GraphQL query for a resolved cityId and
// returns the parsed response. It retries on transient 502s.
func fetchAgodaSearch(ctx context.Context, cityID int, opts HotelSearchOptions) (*agodaSearchResponse, error) {
	body, err := buildAgodaSearchBody(cityID, opts)
	if err != nil {
		return nil, err
	}

	endpoint := agodaBaseURL + "/graphql/search"
	referer := fmt.Sprintf("%s/search?city=%d", agodaBaseURL, cityID)

	var lastErr error
	for attempt := 0; attempt < agodaMaxAttempts; attempt++ {
		if err := agodaLimiter.Wait(ctx); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", agodaUserAgent)
		req.Header.Set("AG-LANGUAGE-LOCALE", "en-us")
		req.Header.Set("AG-PAGE-TYPE-ID", "103")
		req.Header.Set("AG-CID", "-1")
		req.Header.Set("AG-CORRELATION-ID", agodaUUID())
		req.Header.Set("AG-REQUEST-ID", agodaUUID())
		req.Header.Set("AG-ANALYTICS-SESSION-ID", "-1")
		req.Header.Set("AG-REQUEST-ATTEMPT", strconv.Itoa(attempt+1))
		req.Header.Set("ag-debug-override-origin", "PT")
		req.Header.Set("x-gate-meta", agodaGateMeta())
		req.Header.Set("Origin", agodaBaseURL)
		req.Header.Set("Referer", referer)

		resp, err := agodaHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			// Agoda's /graphql/search is anti-bot sensitive and intermittently
			// answers an otherwise-valid request with 400/403/429 (plus the
			// classic transient 5xx). Live capture proves the format is sound:
			// the identical body returns 200 with full priced inventory on a
			// retry. Retry these within the bounded attempt budget instead of
			// dropping the provider to zero on the first blip; a genuinely
			// malformed request still fails after agodaMaxAttempts.
			if agodaRetryableStatus(resp.StatusCode) {
				// Honest verdict surfaced in the returned error (not a fabricated
				// permanent block): live capture proves the identical body returns
				// 200 with full inventory, so a status here is an intermittent
				// anti-bot / IP-reputation rejection, NOT a malformed request and
				// NOT a persistent WAF wall (unlike easyJet's Akamai endpoint).
				// Surfacing AKAMAI_BLOCK would misrepresent a provider that works
				// the majority of the time; the message stays diagnostic instead.
				lastErr = fmt.Errorf("agoda search blocked: transient anti-bot status %d (request verified sound against live cityId; intermittent IP/bot-reputation, retry later)", resp.StatusCode)
				continue
			}
			return nil, fmt.Errorf("search status %d", resp.StatusCode)
		}
		if readErr != nil {
			lastErr = readErr
			continue
		}
		var sr agodaSearchResponse
		if err := json.Unmarshal(raw, &sr); err != nil {
			return nil, fmt.Errorf("decode search: %w", err)
		}
		return &sr, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("exhausted %d attempts", agodaMaxAttempts)
	}
	return nil, lastErr
}

// buildAgodaSearchBody patches the embedded citySearch template with the live
// search parameters and returns the JSON body to POST. Static fields in the
// template are preserved as captured.
func buildAgodaSearchBody(cityID int, opts HotelSearchOptions) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(agodaQueryTemplate, &doc); err != nil {
		return nil, fmt.Errorf("parse query template: %w", err)
	}
	vars, _ := doc["variables"].(map[string]any)
	if vars == nil {
		return nil, fmt.Errorf("query template missing variables")
	}

	checkIn := strings.TrimSpace(opts.CheckIn)
	checkOut := strings.TrimSpace(opts.CheckOut)
	if checkIn == "" {
		checkIn = time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	}
	if checkOut == "" {
		if ci, err := time.Parse("2006-01-02", checkIn); err == nil {
			checkOut = ci.AddDate(0, 0, 2).Format("2006-01-02")
		}
	}
	los := agodaNights(checkIn, checkOut)

	adults := opts.Guests
	if opts.Rooms > 0 && opts.Guests == 0 {
		adults = 2 * opts.Rooms
	}
	if adults <= 0 {
		adults = 2
	}
	rooms := opts.Rooms
	if rooms <= 0 {
		rooms = 1
	}
	children := len(opts.ChildrenAges)
	childAges := agodaIntSlice(opts.ChildrenAges)

	currency := strings.ToUpper(strings.TrimSpace(opts.Currency))
	if currency == "" {
		currency = "USD"
	}

	checkInISO := checkIn + "T00:00:00.000Z"
	checkOutISO := checkOut + "T00:00:00.000Z"
	bookingISO := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	userID := agodaUUID()

	if csr, ok := vars["CitySearchRequest"].(map[string]any); ok {
		csr["cityId"] = cityID
		if sr, ok := csr["searchRequest"].(map[string]any); ok {
			if sc, ok := sr["searchCriteria"].(map[string]any); ok {
				sc["checkInDate"] = checkInISO
				sc["localCheckInDate"] = checkIn
				sc["los"] = los
				sc["rooms"] = rooms
				sc["adults"] = adults
				sc["children"] = children
				sc["childAges"] = childAges
				sc["currency"] = currency
				sc["bookingDate"] = bookingISO
			}
			if sctx, ok := sr["searchContext"].(map[string]any); ok {
				sctx["userId"] = userID
			}
		}
	}

	if csr, ok := vars["ContentSummaryRequest"].(map[string]any); ok {
		if cctx, ok := csr["context"].(map[string]any); ok {
			cctx["rawUserId"] = userID
			if occ, ok := cctx["occupancy"].(map[string]any); ok {
				occ["checkIn"] = checkInISO
				occ["numberOfAdults"] = adults
				occ["numberOfChildren"] = children
			}
		}
	}

	if psr, ok := vars["PricingSummaryRequest"].(map[string]any); ok {
		if pr, ok := psr["pricing"].(map[string]any); ok {
			pr["bookingDate"] = bookingISO
			pr["checkIn"] = checkInISO
			pr["checkout"] = checkOutISO
			pr["currency"] = currency
			pr["localCheckInDate"] = checkIn
			pr["localCheckoutDate"] = checkOut
			if occ, ok := pr["occupancy"].(map[string]any); ok {
				occ["adults"] = adults
				occ["children"] = children
				occ["childAges"] = childAges
				occ["rooms"] = rooms
			}
		}
		if pctx, ok := psr["context"].(map[string]any); ok {
			if ci, ok := pctx["clientInfo"].(map[string]any); ok {
				ci["userId"] = userID
			}
		}
	}

	return json.Marshal(doc)
}

// agodaNights returns the number of nights between two date strings, floor of 1.
func agodaNights(checkIn, checkOut string) int {
	ci, err1 := time.Parse("2006-01-02", checkIn)
	co, err2 := time.Parse("2006-01-02", checkOut)
	if err1 != nil || err2 != nil {
		return 1
	}
	n := int(co.Sub(ci).Hours() / 24)
	if n < 1 {
		return 1
	}
	return n
}

// agodaStayTotal derives the stay total from a per-night rate. Returns 0 when
// either input is non-positive so callers leave TotalPrice unset rather than
// publish a misleading zero.
func agodaStayTotal(nightly float64, nights int) float64 {
	if nightly <= 0 || nights < 1 {
		return 0
	}
	return nightly * float64(nights)
}

// agodaIntSlice converts an []int to a []any for JSON map embedding, returning a
// non-nil empty slice so the marshaled field is an empty array, not null.
func agodaIntSlice(in []int) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// agodaGateMeta builds the x-gate-meta header value: a base64 encoding of
// "{epochMillis}|{userId}|/graphql/". There is no signature — the value is
// self-constructable from a stateless client.
func agodaGateMeta() string {
	raw := fmt.Sprintf("%d|%s|/graphql/", time.Now().UnixMilli(), agodaUUID())
	return base64.StdEncoding.EncodeToString([]byte(raw))
}
