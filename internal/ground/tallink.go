package ground

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// tallinkBookingBase is the Tallink booking SPA base URL.
// The timetables API lives under this domain and requires a JSESSIONID cookie
// obtained by first loading the booking page.
const tallinkBookingBase = "https://booking.tallink.com"

// tallinkDealThreshold is the price (EUR) below which a sailing is flagged as a deal.
// HEL-TAL typically costs EUR 20–40; anything below EUR 20 is promotional.
const tallinkDealThreshold = 20.0

// tallinkOvernightThreshold is the route duration (minutes) above which a route
// is considered overnight and requires a cabin. HEL↔STO (960 min), TUR↔STO (660 min),
// STO↔RIG (1020 min), HEL↔VIS (780 min) — all overnight ferry services where the
// timetable personPrice already includes a basic cabin.
const tallinkOvernightThreshold = 600

// tallinkLimiter: 10 req/min — allows multiple detectors in a single hacks run
// without hitting the context deadline (previously 5 req/min / 12s caused
// "rate limiter: Wait(n=1) would exceed context deadline" during hacks searches).
var tallinkLimiter = newProviderLimiter(6 * time.Second)

// tallinkClient is a shared HTTP client for Tallink API calls.
var tallinkClient = &http.Client{
	Timeout: 30 * time.Second,
}

// tallinkPort holds metadata for a Tallink ferry port.
type tallinkPort struct {
	Code string // Tallink port code (HEL, TAL, STO, RIG, TUR, ALA, PAL, KAP, VIS)
	Name string // Full port/terminal name
	City string // Display city name
}

// tallinkPorts maps lowercase city name / alias to Tallink port metadata.
var tallinkPorts = map[string]tallinkPort{
	// Helsinki
	"helsinki": {Code: "HEL", Name: "Helsinki West Terminal", City: "Helsinki"},
	"hel":      {Code: "HEL", Name: "Helsinki West Terminal", City: "Helsinki"},

	// Tallinn — new API uses TAL, not TLL
	"tallinn": {Code: "TAL", Name: "Tallinn D-Terminal", City: "Tallinn"},
	"tal":     {Code: "TAL", Name: "Tallinn D-Terminal", City: "Tallinn"},
	"tll":     {Code: "TAL", Name: "Tallinn D-Terminal", City: "Tallinn"}, // legacy alias
	"tln":     {Code: "TAL", Name: "Tallinn D-Terminal", City: "Tallinn"}, // legacy alias

	// Stockholm
	"stockholm": {Code: "STO", Name: "Stockholm Värtahamnen", City: "Stockholm"},
	"sto":       {Code: "STO", Name: "Stockholm Värtahamnen", City: "Stockholm"},

	// Riga
	"riga": {Code: "RIG", Name: "Riga Passenger Terminal", City: "Riga"},
	"rig":  {Code: "RIG", Name: "Riga Passenger Terminal", City: "Riga"},

	// Turku
	"turku": {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},
	"tur":   {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},
	"åbo":   {Code: "TUR", Name: "Turku Ferry Terminal", City: "Turku"},

	// Åland / Mariehamn (ALA replaces MAR and LNG in new API)
	"mariehamn": {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"mar":       {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"åland":     {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"aland":     {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"ala":       {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	// Långnäs is no longer a separate port; map to ALA.
	"långnäs": {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"langnäs": {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},
	"lng":     {Code: "ALA", Name: "Mariehamn Ferry Terminal", City: "Mariehamn"},

	// Paldiski
	"paldiski": {Code: "PAL", Name: "Paldiski South Harbour", City: "Paldiski"},
	"pal":      {Code: "PAL", Name: "Paldiski South Harbour", City: "Paldiski"},

	// Kapellskär
	"kapellskär": {Code: "KAP", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},
	"kapellskar": {Code: "KAP", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},
	"kap":        {Code: "KAP", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},

	// Visby
	"visby": {Code: "VIS", Name: "Visby Ferry Terminal", City: "Visby"},
	"vis":   {Code: "VIS", Name: "Visby Ferry Terminal", City: "Visby"},
}

// tallinkRouteDurations stores approximate journey durations in minutes for each route pair.
// Key format: "FROM-TO" using uppercase port codes.
var tallinkRouteDurations = map[string]int{
	"HEL-TAL": 120,
	"TAL-HEL": 120,
	"STO-TAL": 960,
	"TAL-STO": 960,
	"STO-HEL": 960,
	"HEL-STO": 960,
	"STO-RIG": 1020,
	"RIG-STO": 1020,
	"TUR-STO": 660,
	"STO-TUR": 660,
	"HEL-ALA": 360,
	"ALA-HEL": 360,
	"PAL-KAP": 540,
	"KAP-PAL": 540,
	"HEL-VIS": 780,
	"VIS-HEL": 780,
}

// LookupTallinkPort resolves a city name or alias to a Tallink port (case-insensitive).
func LookupTallinkPort(city string) (tallinkPort, bool) {
	p, ok := tallinkPorts[strings.ToLower(strings.TrimSpace(city))]
	return p, ok
}

// HasTallinkPort returns true if the city has a known Tallink port.
func HasTallinkPort(city string) bool {
	_, ok := LookupTallinkPort(city)
	return ok
}

// HasTallinkRoute returns true if both cities have Tallink ports.
func HasTallinkRoute(from, to string) bool {
	return HasTallinkPort(from) && HasTallinkPort(to)
}

// tallinkRouteDuration returns the approximate journey duration in minutes for a port pair.
// Falls back to 120 minutes if the route is unknown.
func tallinkRouteDuration(fromCode, toCode string) int {
	key := fromCode + "-" + toCode
	if d, ok := tallinkRouteDurations[key]; ok {
		return d
	}
	return 120
}

// tallinkIsOvernightRoute returns true if the route between two port codes is
// an overnight ferry requiring a cabin (duration > tallinkOvernightThreshold).
func tallinkIsOvernightRoute(fromCode, toCode string) bool {
	return tallinkRouteDuration(fromCode, toCode) >= tallinkOvernightThreshold
}

// tallinkCabinClass represents a cabin/travel class returned by the travelclasses API.
type tallinkCabinClass struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Capacity    int     `json:"capacity"`
}

// fetchTallinkCabinClasses attempts to fetch cabin class pricing for an overnight sailing.
//
// The booking.tallink.com SPA flow for cabin classes is:
//  1. GET / (with from/to/date params) → obtain JSESSIONID + sessionGUID
//  2. GET /api/timetables (with sessionGUID) → get sailings with sailId
//  3. POST /api/reservation/cruiseSummary (with sessionGUID) → initialize booking state
//  4. GET /api/travelclasses (with sessionGUID) → cabin categories with prices
//
// The POST to cruiseSummary requires a full browser-like session with F5 WAF cookies
// (TS01614805, iki3persistance) that must persist across requests. The WAF rejects
// POST requests from non-browser clients with 302 redirects, making steps 3-4
// unreliable from a server-side HTTP client.
//
// When the API call fails, this returns nil (no cabin classes) and the caller
// falls back to the timetable's personPrice which already includes a basic cabin
// on overnight routes.
func fetchTallinkCabinClasses(ctx context.Context, cookies []*http.Cookie, sessionGUID string, sailID int64) ([]tallinkCabinClass, error) {
	// Step 3: POST reservation/cruiseSummary to select the sail.
	summaryURL := fmt.Sprintf(
		"%s/api/reservation/cruiseSummary?locale=en&country=FI&sessionGUID=%s",
		tallinkBookingBase, sessionGUID,
	)

	summaryBody := fmt.Sprintf(
		`{"outwardSailId":%d,"returnSailId":null,"passengers":[{"passengerAge":"ADULT","passengerBirthDate":null}],"vehicles":[],"pets":[],"campaignCode":"","locale":"en","country":"FI"}`,
		sailID,
	)

	summaryReq, err := http.NewRequestWithContext(ctx, http.MethodPost, summaryURL, strings.NewReader(summaryBody))
	if err != nil {
		return nil, fmt.Errorf("cabin classes: build summary request: %w", err)
	}
	summaryReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	summaryReq.Header.Set("Accept", "application/json")
	summaryReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	for _, c := range cookies {
		summaryReq.AddCookie(c)
	}

	summaryResp, err := tallinkClient.Do(summaryReq)
	if err != nil {
		return nil, fmt.Errorf("cabin classes: summary request: %w", err)
	}
	defer func() { _ = summaryResp.Body.Close() }()
	_, _ = io.Copy(io.Discard, summaryResp.Body) //nolint:errcheck

	if summaryResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cabin classes: summary HTTP %d", summaryResp.StatusCode)
	}

	// Step 4: GET travelclasses.
	classesURL := fmt.Sprintf(
		"%s/api/travelclasses?locale=en&country=FI&sessionGUID=%s",
		tallinkBookingBase, sessionGUID,
	)

	classesReq, err := http.NewRequestWithContext(ctx, http.MethodGet, classesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cabin classes: build travelclasses request: %w", err)
	}
	classesReq.Header.Set("Accept", "application/json")
	classesReq.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	for _, c := range cookies {
		classesReq.AddCookie(c)
	}

	classesResp, err := tallinkClient.Do(classesReq)
	if err != nil {
		return nil, fmt.Errorf("cabin classes: travelclasses request: %w", err)
	}
	defer func() { _ = classesResp.Body.Close() }()

	if classesResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(classesResp.Body, 512))
		return nil, fmt.Errorf("cabin classes: travelclasses HTTP %d: %s", classesResp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(classesResp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("cabin classes: read travelclasses: %w", err)
	}

	var classes []tallinkCabinClass
	if err := json.Unmarshal(body, &classes); err != nil {
		return nil, fmt.Errorf("cabin classes: decode travelclasses: %w", err)
	}
	return classes, nil
}

// tallinkSail is a single sailing from the booking timetables API response.
type tallinkSail struct {
	SailID              int64   `json:"sailId"`
	ShipCode            string  `json:"shipCode"`
	DepartureIsoDate    string  `json:"departureIsoDate"` // "2026-05-01T07:30"
	ArrivalIsoDate      string  `json:"arrivalIsoDate"`   // "2026-05-01T09:30"
	PersonPrice         string  `json:"personPrice"`      // "38.90"
	VehiclePrice        *string `json:"vehiclePrice"`     // null or "45.00"
	Duration            float64 `json:"duration"`         // hours, e.g. 2.0
	SailPackageCode     string  `json:"sailPackageCode"`  // "HEL-TAL"
	SailPackageName     string  `json:"sailPackageName"`  // "Helsinki-Tallinn"
	CityFrom            string  `json:"cityFrom"`         // "HEL"
	CityTo              string  `json:"cityTo"`           // "TAL"
	PierFrom            string  `json:"pierFrom"`
	PierTo              string  `json:"pierTo"`
	HasRoom             bool    `json:"hasRoom"`
	IsOvernight         bool    `json:"isOvernight"`
	IsDisabled          bool    `json:"isDisabled"`
	PromotionApplied    bool    `json:"promotionApplied"`
	MarketingMessage    *string `json:"marketingMessage"`
	IsVoucherApplicable bool    `json:"isVoucherApplicable"`
}

// tallinkDayTrips holds outward and return sails for a single day.
type tallinkDayTrips struct {
	Outwards []tallinkSail `json:"outwards"`
	Returns  []tallinkSail `json:"returns"`
}

// tallinkTimetableResponse is the top-level response from the booking timetables API.
type tallinkTimetableResponse struct {
	DefaultSelections struct {
		OutwardSail int64 `json:"outwardSail"`
		ReturnSail  int64 `json:"returnSail"`
	} `json:"defaultSelections"`
	Trips map[string]tallinkDayTrips `json:"trips"` // key: "2026-05-01"
}

// buildTallinkBookingURL constructs a Tallink booking URL for the user.
func buildTallinkBookingURL(fromCode, toCode, date string) string {
	return fmt.Sprintf(
		"https://booking.tallink.com/?from=%s&to=%s&date=%s&locale=en&country=FI&voyageType=TRANSPORT",
		strings.ToLower(fromCode), strings.ToLower(toCode), date,
	)
}

// tallinkSession holds session state obtained from the booking page.
type tallinkSession struct {
	Cookies     []*http.Cookie
	SessionGUID string // from window.Env.sessionGuid in the page HTML
}

// tallinkGetSession loads the booking page to obtain a JSESSIONID cookie and
// sessionGUID, which are required for subsequent API calls.
func tallinkGetSession(ctx context.Context, fromCode, toCode, date string) (*tallinkSession, error) {
	pageURL := buildTallinkBookingURL(fromCode, toCode, date)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html")

	resp, err := tallinkClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tallink session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read body to extract sessionGuid (limited to 256KB).
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))

	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil, fmt.Errorf("tallink session: no cookies returned")
	}

	// Extract sessionGuid from the page HTML: sessionGuid: 'UUID-HERE',
	guid := tallinkExtractSessionGUID(string(body))

	return &tallinkSession{Cookies: cookies, SessionGUID: guid}, nil
}

// tallinkExtractSessionGUID extracts the sessionGuid value from the booking page HTML.
// The SPA embeds it as: sessionGuid: 'UUID',
func tallinkExtractSessionGUID(html string) string {
	const marker = "sessionGuid: '"
	idx := strings.Index(html, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], "'")
	if end < 0 || end > 64 {
		return ""
	}
	return html[start : start+end]
}

// tallinkFetchResult bundles a timetable response with the session that produced it,
// so callers can reuse the session for subsequent API calls (e.g. travelclasses).
type tallinkFetchResult struct {
	Timetable *tallinkTimetableResponse
	Session   *tallinkSession
}

// fetchTallinkTimetables calls the booking.tallink.com timetables API
// which supports arbitrary future dates (unlike the old voyage-avails endpoint).
// For overnight routes (duration >= tallinkOvernightThreshold), it uses
// voyageType=CRUISE with includeOvernight=true to get cabin-inclusive pricing.
func fetchTallinkTimetables(ctx context.Context, fromCode, toCode, date string) (*tallinkFetchResult, error) {
	// Step 1: obtain session cookie + sessionGUID
	session, err := tallinkGetSession(ctx, fromCode, toCode, date)
	if err != nil {
		return nil, err
	}

	// Step 2: call timetables API with the session cookie
	// dateFrom/dateTo: 3-day window like the SPA does
	dateTo := date // single day is fine; API returns what's in range
	parsedDate, err := models.ParseDate(date)
	if err == nil {
		dateTo = parsedDate.Add(2 * 24 * time.Hour).Format("2006-01-02")
	}

	overnight := tallinkIsOvernightRoute(fromCode, toCode)
	voyageType := "SHUTTLE"
	includeOvernight := "false"
	if overnight {
		voyageType = "CRUISE"
		includeOvernight = "true"
	}

	sessionParam := ""
	if session.SessionGUID != "" {
		sessionParam = "&sessionGUID=" + session.SessionGUID
	}

	apiURL := fmt.Sprintf(
		"%s/api/timetables?locale=en&country=FI&from=%s&to=%s&oneWay=%s&dateFrom=%s&dateTo=%s&voyageType=%s&includeOvernight=%s&searchFutureSails=false%s",
		tallinkBookingBase,
		strings.ToLower(fromCode), strings.ToLower(toCode),
		fmt.Sprintf("%t", overnight), // oneWay=true for overnight (one-leg cruises)
		date, dateTo,
		voyageType, includeOvernight,
		sessionParam,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	for _, c := range session.Cookies {
		req.AddCookie(c)
	}

	resp, err := tallinkClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tallink timetables: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tallink timetables: HTTP %d: %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("tallink timetables read: %w", err)
	}

	var result tallinkTimetableResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("tallink timetables decode: %w", err)
	}
	return &tallinkFetchResult{Timetable: &result, Session: session}, nil
}

// tallinkNormalizeDateTime normalizes the timetable API datetime format.
// Input: "2026-05-01T07:30" → "2026-05-01T07:30:00"
func tallinkNormalizeDateTime(s string) string {
	if s == "" {
		return ""
	}
	// The timetables API returns "2026-05-01T07:30" (no seconds).
	// Normalize to full ISO 8601 for consistency.
	if len(s) == 16 { // "2006-01-02T15:04"
		return s + ":00"
	}
	return s
}

// SearchTallink searches Tallink/Silja Line for ferry crossings between two cities.
// Uses the booking.tallink.com timetables API which supports any future date.
func SearchTallink(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromPort, ok := LookupTallinkPort(from)
	if !ok {
		return nil, fmt.Errorf("tallink: no port for %q", from)
	}
	toPort, ok := LookupTallinkPort(to)
	if !ok {
		return nil, fmt.Errorf("tallink: no port for %q", to)
	}

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("tallink: invalid date %q: %w", date, err)
	}

	if err := tallinkLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("tallink: rate limiter: %w", err)
	}

	overnight := tallinkIsOvernightRoute(fromPort.Code, toPort.Code)
	slog.Debug("tallink search", "from", fromPort.City, "to", toPort.City, "date", date, "overnight", overnight)

	result, err := fetchTallinkTimetables(ctx, fromPort.Code, toPort.Code, date)
	if err != nil {
		return nil, fmt.Errorf("tallink: %w", err)
	}

	// Collect outward sails for the requested date from the timetable.
	dayTrips, ok := result.Timetable.Trips[date]
	if !ok {
		slog.Debug("tallink: no trips for date", "date", date, "available_dates", len(result.Timetable.Trips))
		return nil, nil
	}

	sails := dayTrips.Outwards
	slog.Debug("tallink sails", "total", len(sails))

	if len(sails) == 0 {
		return nil, nil
	}

	// For overnight routes, attempt to fetch cabin class details.
	// This requires POSTing to reservation/cruiseSummary then GETting travelclasses.
	// The API often rejects non-browser clients (WAF), so cabin classes are best-effort.
	var cabinClasses []tallinkCabinClass
	if overnight && len(sails) > 0 {
		firstSail := sails[0]
		classes, cabinErr := fetchTallinkCabinClasses(ctx, result.Session.Cookies, result.Session.SessionGUID, firstSail.SailID)
		if cabinErr != nil {
			slog.Debug("tallink cabin classes unavailable (expected)", "error", cabinErr)
		} else {
			cabinClasses = classes
		}
	}

	bookingURL := buildTallinkBookingURL(fromPort.Code, toPort.Code, date)
	defaultDuration := tallinkRouteDuration(fromPort.Code, toPort.Code)

	var routes []models.GroundRoute
	for _, s := range sails {
		if s.IsDisabled {
			continue
		}

		depTime := tallinkNormalizeDateTime(s.DepartureIsoDate)
		arrTime := tallinkNormalizeDateTime(s.ArrivalIsoDate)

		duration := defaultDuration
		if computed := computeDurationMinutes(depTime, arrTime); computed > 0 {
			duration = computed
		}

		// Parse price from string ("38.90").
		var price float64
		if s.PersonPrice != "" {
			_, _ = fmt.Sscanf(s.PersonPrice, "%f", &price)
		}

		var amenities []string

		if overnight {
			// Overnight routes: personPrice includes a basic cabin.
			amenities = append(amenities, "Overnight", "Cabin included")
			// If we got cabin class details, add them as amenities.
			if len(cabinClasses) > 0 {
				amenities = append(amenities, tallinkFormatCabinClasses(cabinClasses))
			}
		}

		if price > 0 && price < tallinkDealThreshold {
			amenities = append(amenities, "Deal")
		}
		if s.PromotionApplied {
			amenities = append(amenities, "Promotion")
		}

		routes = append(routes, models.GroundRoute{
			Provider: "tallink",
			Type:     "ferry",
			Price:    price,
			Currency: "EUR",
			Duration: duration,
			Departure: models.GroundStop{
				City:    fromPort.City,
				Station: fromPort.Name + tallinkShipSuffix(s.ShipCode),
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toPort.City,
				Station: toPort.Name,
				Time:    arrTime,
			},
			Transfers:  0,
			BookingURL: bookingURL,
			Amenities:  amenities,
		})
	}

	slog.Debug("tallink results", "routes", len(routes))
	return routes, nil
}

// tallinkFormatCabinClasses formats cabin class details into a human-readable amenity string.
// Example: "Cabins: A2 €89, B4 €65, Deck €39"
func tallinkFormatCabinClasses(classes []tallinkCabinClass) string {
	if len(classes) == 0 {
		return ""
	}
	var parts []string
	for _, c := range classes {
		if c.Price > 0 {
			parts = append(parts, fmt.Sprintf("%s €%.0f", c.Code, c.Price))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Cabins: " + strings.Join(parts, ", ")
}

// tallinkShipSuffix returns a ship name suffix for the station display, or empty string.
func tallinkShipSuffix(shipName string) string {
	if shipName == "" {
		return ""
	}
	return " (" + shipName + ")"
}

// newUUID is retained for potential future use (session tracking etc).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
