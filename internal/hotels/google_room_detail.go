package hotels

// Google Hotels room-level detail fetch (opt-in, endpoint-gated).
//
// Google Hotels' headline search/entity price is a property "lead-in" — the
// cheapest nightly number across booking partners, NOT a specific bookable room
// rate. The richer per-room-per-night matrix users see AFTER selecting a
// property on google.com/travel/hotels is served by the entity page's deferred
// `yY52ce` batchexecute RPC.
//
// PROBED 2026-06-25 (static Go client with Chrome TLS impersonation, and a plain
// curl with a Chrome User-Agent), Paris hotel ~30 days out, 2 guests:
//
//	POST https://www.google.com/_/TravelFrontendUi/data/batchexecute?hl=en
//	     f.req=[[["yY52ce", "[null,[Y,M,D],[Y,M,D],[2,[],0],\"<id>\",\"EUR\"]", ...]]]
//	  -> HTTP 200, 108 bytes, body:
//	     [["wrb.fr","yY52ce",null,null,null,[3],"generic"], ...]
//
// i.e. the request is ACCEPTED (HTTP 200) but the wrb.fr envelope carries a NULL
// payload with internal status [3] — zero booking-partner / room data. The same
// null-payload outcome held across every hotel-ID encoding (full, cid hex, cid
// decimal) and even the known-good reviews RPC now returns null. The live
// room-level matrix is gated behind browser session context (consent/session
// cookies + page tokens) that a static binary cannot mint without a
// headless/bot-evasion layer. This is captured as a regression guard in
// testdata/price_empty_payload.txt and TestParseHotelPriceResponse_EmptyPayload.
//
// Per the locked repo decisions (no browser/headless deps; honest typed status
// over fabricated data; "API-first with optional opt-ins") we do NOT ship a
// browser shim that mints Google session tokens. Instead this mirrors the
// easyJet / Expedia opt-in pattern: it is a no-op skip unless the operator
// supplies a reachable room-detail base URL via GOOGLE_HOTELS_DETAIL_API_BASE
// (e.g. an authorised partner endpoint, a SerpAPI-style relay, or a self-hosted
// reverse proxy that holds a browser session and replays the RPC). When
// configured it maps an ASSUMED room-detail JSON shape to canonical RoomType
// entries tagged provider "Google Hotels" with room-level price confidence.
//
// SCHEMA NOT VERIFIED: the public RPC returns a null payload to static clients,
// so this parser is written against a representative/assumed shape, NOT confirmed
// against a populated live response. The field tags below may need adjustment
// once an operator supplies a reachable endpoint that returns the full matrix;
// the parser is intentionally tolerant of two price encodings (structured
// per-night rate, or a flat lead-in number) and never emits a zero-priced stub.
//
// When UNconfigured, GetRoomAvailabilityWithOpts keeps its existing free-path
// behaviour (entity page, partner-price lead-in, search fallback, SerpAPI,
// Agoda, Booking) — this provider is purely additive and silent. When a
// configured fetch is bot-walled / rate-limited, the caller surfaces an honest
// retryable Notice rather than a fabricated empty result.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

// googleHotelsDefaultHost is the canonical Google Travel host used to build a
// human-bookable deep link when a room detail omits its own provider URL.
const googleHotelsDefaultHost = "https" + "://" + "www.google.com"

var (
	googleRoomDetailLimiter = rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	googleRoomDetailClient  = &http.Client{Timeout: 25 * time.Second}
)

// googleRoomDetailEnabled mirrors the other auxiliary providers' test gate. It is
// set to false in TestMain so deterministic tests never fire real network calls;
// tests that exercise the configured path inject a mock server via
// GOOGLE_HOTELS_DETAIL_API_BASE.
var googleRoomDetailEnabled = true

// googleRoomDetailAPIBase returns the operator-supplied Google Hotels room-detail
// base URL, or "". When empty the provider is a no-op (honest skip), because the
// public yY52ce RPC returns a null payload to static clients.
func googleRoomDetailAPIBase() string {
	return strings.TrimSpace(os.Getenv("GOOGLE_HOTELS_DETAIL_API_BASE"))
}

// googleRoomDetailConfigured reports whether an operator has opted in by
// supplying a reachable room-detail base URL. Mirrors expediaConfigured().
func googleRoomDetailConfigured() bool {
	return googleRoomDetailAPIBase() != ""
}

// googleRoomDetailRate is one structured price encoding of a room's nightly rate.
type googleRoomDetailRate struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currencyCode"`
}

// googleRoomDetailRoom is a single bookable room in the room-detail response.
// The shape is an ASSUMED/representative Google Hotels room-matrix payload — NOT
// verified against the live RPC (which returns a null payload to static clients).
// The parser is intentionally tolerant of the two common price encodings (a
// structured per-night rate, or a flat lead-in number).
type googleRoomDetailRoom struct {
	Name         string               `json:"roomName"`
	RatePlan     string               `json:"ratePlanName"`
	Provider     string               `json:"provider"`
	ProviderURL  string               `json:"bookingUrl"`
	NightlyRate  googleRoomDetailRate `json:"nightlyRate"`  // structured per-night rate (room-level)
	TotalRate    googleRoomDetailRate `json:"totalRate"`    // optional stay total
	LeadInPrice  float64              `json:"leadInPrice"`  // flat lead-in fallback
	Currency     string               `json:"currencyCode"` // currency for LeadInPrice
	TaxIncluded  bool                 `json:"taxesIncluded"`
	Refundable   bool                 `json:"refundable"`
	FreeCancel   bool                 `json:"freeCancellation"`
	Board        string               `json:"board"`
	MaxOccupancy int                  `json:"maxOccupancy"`
}

type googleRoomDetailResponse struct {
	HotelName string                 `json:"hotelName"`
	Rooms     []googleRoomDetailRoom `json:"rooms"`
}

// tryGoogleRoomDetail queries an operator-configured Google Hotels room-detail
// endpoint for the requested property and stay, returning canonical RoomType
// entries tagged provider "Google Hotels". It returns (nil, "", "") when disabled
// (test mode) or unconfigured, mirroring the Expedia opt-in pattern — the public
// yY52ce RPC returns a null payload to static clients. On a retryable bot-wall /
// rate-limit it returns an honest Notice (third value) so a withheld room price
// is never mistaken for absent inventory.
func tryGoogleRoomDetail(ctx context.Context, opts RoomSearchOptions) (rooms []RoomType, hotelName, notice string) {
	if !googleRoomDetailEnabled {
		return nil, "", ""
	}
	base := googleRoomDetailAPIBase()
	if base == "" {
		return nil, "", ""
	}
	if strings.TrimSpace(opts.HotelID) == "" {
		return nil, "", ""
	}
	currency := strings.TrimSpace(opts.Currency)
	if currency == "" {
		currency = "USD"
	}
	if err := googleRoomDetailLimiter.Wait(ctx); err != nil {
		return nil, "", ""
	}

	q := url.Values{}
	q.Set("hotelId", opts.HotelID)
	q.Set("checkIn", opts.CheckIn)
	q.Set("checkOut", opts.CheckOut)
	q.Set("currency", currency)
	q.Set("adults", fmt.Sprintf("%d", max(opts.Guests, 1)))
	reqURL := strings.TrimRight(base, "/") + "/rooms/query?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl/1.0")

	resp, err := googleRoomDetailClient.Do(req)
	if err != nil {
		return nil, "", providerRateLimitNotice("Google Hotels", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusTooManyRequests {
		// The configured base is still serving a bot challenge, not real JSON.
		// Classify as retryable so the caller surfaces a Notice, not a hard fail.
		return nil, "", providerRateLimitNotice("Google Hotels", models.ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", ""
	}

	parsed, name := parseGoogleRoomDetail(body, currency)
	return parsed, name, ""
}

// parseGoogleRoomDetail maps a Google Hotels room-detail JSON payload to RoomType
// entries. Only price-bearing rooms are mapped (zero-priced stubs are skipped) so
// every returned entry is actually bookable. A structured per-night rate is
// treated as room-level (exact when a room name is present, similar otherwise); a
// flat lead-in number is tagged property_level_only so the bookability sort never
// leads with an unverified headline over a real room rate.
func parseGoogleRoomDetail(raw []byte, fallbackCurrency string) ([]RoomType, string) {
	var resp googleRoomDetailResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, ""
	}
	rooms := make([]RoomType, 0, len(resp.Rooms))
	for _, r := range resp.Rooms {
		price, currency, basis, match := googleRoomDetailPrice(r, fallbackCurrency)
		if price <= 0 {
			continue // no comparable price -> skip, never a fabricated stub
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			// Room-level price without a nameable identity is "similar", not "exact".
			name = "Standard Room"
			if match == models.RoomInventoryMatchExact {
				match = models.RoomInventoryMatchSimilar
			}
		}
		rt := RoomType{
			Name:            name,
			Price:           price,
			NightlyPrice:    googleRoomDetailNightly(r, price, basis),
			TotalPrice:      r.TotalRate.Amount,
			Currency:        currency,
			Provider:        "Google Hotels",
			ProviderURL:     googleRoomDetailBookingURL(r),
			RatePlanName:    strings.TrimSpace(r.RatePlan),
			MatchConfidence: match,
			Board:           strings.TrimSpace(r.Board),
			MaxGuests:       r.MaxOccupancy,
		}
		if r.TaxIncluded {
			v := true
			rt.TaxesFeesIncluded = &v
		}
		if r.Refundable {
			v := true
			rt.Refundable = &v
		}
		if r.FreeCancel {
			v := true
			rt.FreeCancellation = &v
		}
		rooms = append(rooms, rt)
	}
	return rooms, strings.TrimSpace(resp.HotelName)
}

// googleRoomDetailPrice resolves the comparable price, currency, basis and room
// match confidence for a room. A structured per-night rate is room-level (exact
// match — refined to similar later when no room name is present); a flat lead-in
// number is property-level only.
func googleRoomDetailPrice(r googleRoomDetailRoom, fallbackCurrency string) (price float64, currency, basis, match string) {
	if r.NightlyRate.Amount > 0 {
		return r.NightlyRate.Amount,
			firstNonEmptyGoogleRoom(r.NightlyRate.Currency, r.Currency, fallbackCurrency),
			models.PriceBasisRoomNightly,
			models.RoomInventoryMatchExact
	}
	if r.LeadInPrice > 0 {
		return r.LeadInPrice,
			firstNonEmptyGoogleRoom(r.Currency, fallbackCurrency),
			models.PriceBasisLeadIn,
			models.RoomInventoryMatchPropertyLevelOnly
	}
	return 0, firstNonEmptyGoogleRoom(r.Currency, fallbackCurrency), "", ""
}

// googleRoomDetailNightly returns the per-night rate to record. A room-nightly
// basis is already per-night; a lead-in falls back to the headline price so the
// downstream quote always carries a usable nightly figure.
func googleRoomDetailNightly(r googleRoomDetailRoom, price float64, basis string) float64 {
	if basis == models.PriceBasisRoomNightly && r.NightlyRate.Amount > 0 {
		return r.NightlyRate.Amount
	}
	return price
}

func firstNonEmptyGoogleRoom(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return strings.ToUpper(s)
		}
	}
	return "USD"
}

// googleRoomDetailBookingURL returns the room's provider booking link, falling
// back to the public Google Travel host so a result is always actionable.
func googleRoomDetailBookingURL(r googleRoomDetailRoom) string {
	link := strings.TrimSpace(r.ProviderURL)
	switch {
	case link == "":
		return googleHotelsDefaultHost + "/travel/hotels"
	case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"):
		return link
	case strings.HasPrefix(link, "//"):
		return "https:" + link
	case strings.HasPrefix(link, "/"):
		return googleHotelsDefaultHost + link
	default:
		return googleHotelsDefaultHost + "/" + link
	}
}
