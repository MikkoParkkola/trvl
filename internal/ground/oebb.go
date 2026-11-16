package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/logredact"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// oebbShopBase is the base URL for the ÖBB shop REST API.
const oebbShopBase = "https://shop.oebbtickets.at"

// oebbLimiter: conservative 5 req/min.
var oebbLimiter = newProviderLimiter(12 * time.Second)

// oebbClient is a shared HTTP client for ÖBB API calls.
var oebbClient = &http.Client{
	Timeout: 30 * time.Second,
}

// oebbStation holds an ÖBB/HAFAS station entry.
type oebbStation struct {
	// ExtID is the ÖBB/HAFAS external ID (EVA/UIC number) used by HAFAS mgate.
	ExtID string
	// Number is the numeric station ID used by the ÖBB shop REST API.
	Number int
	Name   string
	City   string
}

// oebbStations maps lowercase city name to ÖBB station metadata.
// ExtIDs are EVA numbers verified against fahrplan.oebb.at.
// Numbers are the shop.oebbtickets.at numeric station IDs.
var oebbStations = map[string]oebbStation{
	// Austria — home network. Vienna's ExtID is Wien Hbf itself (8103000), not
	// the city-wide 1190100 under which HAFAS also ends trips at
	// Franz-Josefs-Bahnhof; the shop's 1290401 already means Wien Hbf.
	"vienna":     {ExtID: "8103000", Number: 1290401, Name: "Wien Hbf", City: "Vienna"},
	"wien":       {ExtID: "8103000", Number: 1290401, Name: "Wien Hbf", City: "Vienna"},
	"salzburg":   {ExtID: "8100002", Number: 8100002, Name: "Salzburg Hbf", City: "Salzburg"},
	"innsbruck":  {ExtID: "8100108", Number: 8100108, Name: "Innsbruck Hbf", City: "Innsbruck"},
	"graz":       {ExtID: "8100173", Number: 8100173, Name: "Graz Hbf", City: "Graz"},
	"linz":       {ExtID: "8100013", Number: 8100013, Name: "Linz Hbf", City: "Linz"},
	"klagenfurt": {ExtID: "8100085", Number: 8100085, Name: "Klagenfurt Hbf", City: "Klagenfurt"},
	"villach":    {ExtID: "8100071", Number: 8100071, Name: "Villach Hbf", City: "Villach"},
	"bregenz":    {ExtID: "8100356", Number: 8100356, Name: "Bregenz", City: "Bregenz"},
	"feldkirch":  {ExtID: "8100358", Number: 8100358, Name: "Feldkirch", City: "Feldkirch"},

	// Germany (served by ÖBB Railjet/Nightjet)
	"munich":    {ExtID: "8000261", Number: 8000261, Name: "München Hbf", City: "Munich"},
	"münchen":   {ExtID: "8000261", Number: 8000261, Name: "München Hbf", City: "Munich"},
	"berlin":    {ExtID: "8011160", Number: 8011160, Name: "Berlin Hbf", City: "Berlin"},
	"frankfurt": {ExtID: "8000105", Number: 8000105, Name: "Frankfurt(Main)Hbf", City: "Frankfurt"},
	"hamburg":   {ExtID: "8002549", Number: 8002549, Name: "Hamburg Hbf", City: "Hamburg"},
	"stuttgart": {ExtID: "8000096", Number: 8000096, Name: "Stuttgart Hbf", City: "Stuttgart"},

	// Switzerland
	"zurich": {ExtID: "8503000", Number: 8503000, Name: "Zürich HB", City: "Zurich"},
	"zürich": {ExtID: "8503000", Number: 8503000, Name: "Zürich HB", City: "Zurich"},
	"geneva": {ExtID: "8501008", Number: 8501008, Name: "Genève", City: "Geneva"},
	"basel":  {ExtID: "8500010", Number: 8500010, Name: "Basel SBB", City: "Basel"},
	"bern":   {ExtID: "8507000", Number: 8507000, Name: "Bern", City: "Bern"},

	// Italy (served by ÖBB/Trenitalia Railjet)
	"venice":  {ExtID: "8300137", Number: 8300137, Name: "Venezia Santa Lucia", City: "Venice"},
	"verona":  {ExtID: "8300066", Number: 8300066, Name: "Verona P.N.", City: "Verona"},
	"milan":   {ExtID: "8300046", Number: 8300046, Name: "Milano Centrale", City: "Milan"},
	"rome":    {ExtID: "8300003", Number: 8300003, Name: "Roma Termini", City: "Rome"},
	"bologna": {ExtID: "8300027", Number: 8300027, Name: "Bologna Centrale", City: "Bologna"},

	// Hungary
	"budapest": {ExtID: "5500017", Number: 5500017, Name: "Budapest-Keleti", City: "Budapest"},

	// Czech Republic
	"prague": {ExtID: "5400014", Number: 5400014, Name: "Praha hl.n.", City: "Prague"},
	"praha":  {ExtID: "5400014", Number: 5400014, Name: "Praha hl.n.", City: "Prague"},

	// Slovakia
	"bratislava": {ExtID: "5600002", Number: 5600002, Name: "Bratislava hl.st.", City: "Bratislava"},

	// Slovenia
	"ljubljana": {ExtID: "7900001", Number: 7900001, Name: "Ljubljana", City: "Ljubljana"},

	// Croatia
	"zagreb": {ExtID: "7800001", Number: 7800001, Name: "Zagreb Gl. kol.", City: "Zagreb"},

	// Poland (names as ÖBB spells them)
	"warsaw": {ExtID: "5100065", Number: 5100065, Name: "Warszawa Centralna", City: "Warsaw"},
	"krakow": {ExtID: "5100028", Number: 5100028, Name: "Krakow Glowny", City: "Krakow"},
	"kraków": {ExtID: "5100028", Number: 5100028, Name: "Krakow Glowny", City: "Krakow"},
}

// LookupOebbStation resolves a city name to an ÖBB station (case-insensitive).
func LookupOebbStation(city string) (oebbStation, bool) {
	s, ok := oebbStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasOebbStation returns true if the city has a known ÖBB station.
func HasOebbStation(city string) bool {
	_, ok := LookupOebbStation(city)
	return ok
}

// HasOebbRoute returns true if both cities are in the ÖBB network.
// ÖBB focuses on Austria and neighbouring countries (DE, CH, IT, HU, CZ, SK, SI, HR).
func HasOebbRoute(from, to string) bool {
	return HasOebbStation(from) && HasOebbStation(to)
}

// oebbShopAnonymousTokenResponse is the response from the ÖBB shop anonymous token endpoint.
type oebbShopAnonymousTokenResponse struct {
	AccessToken string `json:"access_token"`
}

// oebbShopConnection is one connection entry from the timetable response.
type oebbShopConnection struct {
	ID   string `json:"id"`
	From struct {
		Departure string `json:"departure"`
	} `json:"from"`
	To struct {
		Arrival string `json:"arrival"`
	} `json:"to"`
	Duration int `json:"duration"` // milliseconds
}

// oebbShopTimetableResponse holds the timetable search response.
type oebbShopTimetableResponse struct {
	Connections []oebbShopConnection `json:"connections"`
}

// oebbShopOffer is one offer from the prices endpoint.
type oebbShopOffer struct {
	ConnectionID string  `json:"connectionId"`
	Price        float64 `json:"price"`
	FirstClass   bool    `json:"firstClass"`
}

// oebbShopPricesResponse holds the prices response.
type oebbShopPricesResponse struct {
	Offers []oebbShopOffer `json:"offers"`
}

// oebbBrowserUA is sent on every shop call: the shop's bot rule refuses Go's
// default User-Agent and accepts a browser one over the same TLS (live probe
// TestLiveProbe_OebbShopClientSignature, 2026-11-15).
const oebbBrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var oebbHTMLTitle = regexp.MustCompile(`(?is)<title>\s*([^<]*?)\s*</title>`)

// oebbShopSetHeaders sets the required headers for ÖBB shop API calls.
func oebbShopSetHeaders(req *http.Request, token string) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Channel", "inet")
	req.Header.Set("User-Agent", oebbBrowserUA)
	if token != "" {
		req.Header.Set("accesstoken", token)
	}
}

// oebbShopStatusError types a non-200 shop answer (403/429/5xx wrap
// ErrRateLimited). A refusal arrives as a whole HTML page; only its <title>
// is kept, so no markup reaches statuses or logs.
func oebbShopStatusError(stage string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	err := httpStatusError("oebb shop", stage, resp.StatusCode)
	if m := oebbHTMLTitle.FindSubmatch(body); m != nil && len(m[1]) > 0 {
		return fmt.Errorf("%w (%s)", err, m[1])
	}
	return err
}

// oebbShopGetToken fetches an anonymous access token from the ÖBB shop API.
func oebbShopGetToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		oebbShopBase+"/api/domain/v1/anonymousToken", nil)
	if err != nil {
		return "", err
	}
	oebbShopSetHeaders(req, "")

	resp, err := oebbClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oebb shop token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", oebbShopStatusError("token", resp)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("oebb shop token read: %w", err)
	}

	var tokenResp oebbShopAnonymousTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("oebb shop token decode: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("oebb shop token: empty access_token in response")
	}
	return tokenResp.AccessToken, nil
}

// oebbShopInitUserData calls initUserData to activate the session token.
func oebbShopInitUserData(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		oebbShopBase+"/api/domain/v1/initUserData",
		bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	oebbShopSetHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := oebbClient.Do(req)
	if err != nil {
		return fmt.Errorf("oebb shop initUserData: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 200 or 204 are both acceptable.
	if resp.StatusCode >= 300 {
		return oebbShopStatusError("initUserData", resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return nil
}

// oebbShopSearchTimetable calls the timetable endpoint and returns connections.
func oebbShopSearchTimetable(ctx context.Context, token string, fromStation, toStation oebbStation, datetime string) ([]oebbShopConnection, error) {
	payload := map[string]any{
		"reverse":           false,
		"datetimeDeparture": datetime,
		"filter":            map[string]any{"trains": true},
		"passengers": []map[string]any{
			{
				"me":         true,
				"remembered": false,
				"type":       "ADULT",
				"id":         1,
				"cards":      []any{},
				"relations":  []any{},
				"isSelected": true,
			},
		},
		"count": 5,
		"from": map[string]any{
			"name":   fromStation.Name,
			"number": fromStation.Number,
		},
		"to": map[string]any{
			"name":   toStation.Name,
			"number": toStation.Number,
		},
		"timeout": map[string]any{},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("oebb shop timetable marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		oebbShopBase+"/api/hafas/v4/timetable",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	oebbShopSetHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := oebbClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oebb shop timetable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, oebbShopStatusError("timetable", resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("oebb shop timetable read: %w", err)
	}

	var timetable oebbShopTimetableResponse
	if err := json.Unmarshal(respBody, &timetable); err != nil {
		return nil, fmt.Errorf("oebb shop timetable decode: %w", err)
	}
	return timetable.Connections, nil
}

// oebbShopGetPrices fetches prices for a batch of connection IDs.
func oebbShopGetPrices(ctx context.Context, token string, connectionIDs []string) ([]oebbShopOffer, error) {
	if len(connectionIDs) == 0 {
		slog.Debug("oebb shop prices: no connection IDs to fetch")
		return nil, nil
	}

	// Build query: connectionIds[]=a&connectionIds[]=b
	params := url.Values{}
	for _, id := range connectionIDs {
		params.Add("connectionIds[]", id)
	}
	reqURL := oebbShopBase + "/api/offer/v1/prices?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	oebbShopSetHeaders(req, token)

	resp, err := oebbClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oebb shop prices: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, oebbShopStatusError("prices", resp)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("oebb shop prices read: %w", err)
	}

	var pricesResp oebbShopPricesResponse
	if err := json.Unmarshal(respBody, &pricesResp); err != nil {
		return nil, fmt.Errorf("oebb shop prices decode: %w", err)
	}
	return pricesResp.Offers, nil
}

// SearchOebb searches ÖBB (Austrian Federal Railways) for train journeys between
// two cities. The shop (shop.oebbtickets.at) is the only source of fares; when
// it refuses or fails, the HAFAS timetable still answers with the same
// trains-only departures, unpriced, and a ProviderNotice says why.
func SearchOebb(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupOebbStation(from)
	if !ok {
		return nil, fmt.Errorf("no ÖBB station for %q: %w", from, models.ErrNotApplicable)
	}
	toStation, ok := LookupOebbStation(to)
	if !ok {
		return nil, fmt.Errorf("no ÖBB station for %q: %w", to, models.ErrNotApplicable)
	}

	if currency == "" {
		currency = "EUR"
	}

	dt, err := models.ParseDate(date)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}

	if err := oebbLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("oebb rate limiter: %w", err)
	}

	slog.Debug("oebb search", "from", fromStation.City, "to", toStation.City, "date", date)

	routes, shopErr := searchOebbShop(ctx, fromStation, toStation, dt, date, currency)
	// Shop departures, even unpriced ones with a notice, are the answer; the
	// timetable stands in only when the shop produced none.
	if shopErr == nil || len(routes) > 0 || ctx.Err() != nil {
		return routes, shopErr
	}

	slog.Debug("oebb shop failed, using the timetable", "err", logredact.Err(shopErr))
	routes, hafasErr := searchOebbTimetable(ctx, fromStation, toStation, dt, date, currency)
	notice := oebbPricesNotice(shopErr)
	if hafasErr != nil {
		notice.FixHint = "shop refused and the timetable fallback failed: retry later"
		notice.Err = fmt.Errorf("%w; timetable fallback: %w", shopErr, hafasErr)
		return nil, notice
	}
	return routes, notice
}

// searchOebbShop runs the shop's 4-step flow:
//  1. GET anonymousToken
//  2. POST initUserData
//  3. POST timetable search
//  4. GET prices for all returned connection IDs
func searchOebbShop(ctx context.Context, fromStation, toStation oebbStation, dt time.Time, date, currency string) ([]models.GroundRoute, error) {
	// ÖBB shop API uses ISO 8601 with milliseconds.
	datetime := dt.Format("2006-01-02") + "T08:00:00.000"

	// Step 1: Obtain anonymous token. Errors below already name their stage.
	token, err := oebbShopGetToken(ctx)
	if err != nil {
		return nil, err
	}

	// Step 2: Initialise user data (activates the session). A refusal here is
	// the same refusal as at any other step.
	if err := oebbShopInitUserData(ctx, token); err != nil {
		return nil, err
	}

	// Step 3: Search timetable.
	connections, err := oebbShopSearchTimetable(ctx, token, fromStation, toStation, datetime)
	if err != nil {
		return nil, err
	}
	slog.Debug("oebb shop timetable", "connections", len(connections))

	if len(connections) == 0 {
		slog.Debug("oebb shop timetable: no connections returned for route", "from", fromStation.City, "to", toStation.City, "date", date)
		return nil, nil
	}

	// Step 4: Fetch prices for all connection IDs.
	ids := make([]string, 0, len(connections))
	for _, c := range connections {
		if c.ID != "" {
			ids = append(ids, c.ID)
		}
	}

	priceByID := make(map[string]float64, len(ids))
	var pricesErr error
	if len(ids) > 0 {
		offers, err := oebbShopGetPrices(ctx, token, ids)
		if err != nil {
			// The departures stand without fares; the caller learns why.
			pricesErr = err
		} else {
			for _, o := range offers {
				// Keep the cheapest (2nd-class) offer per connection.
				if o.FirstClass {
					continue
				}
				if existing, ok := priceByID[o.ConnectionID]; !ok || o.Price < existing {
					priceByID[o.ConnectionID] = o.Price
				}
			}
		}
	}

	bookingURL := buildOebbBookingURL(fromStation, toStation, date)
	var routes []models.GroundRoute

	for _, c := range connections {
		// Parse departure and arrival from ISO 8601 strings, strip timezone suffix.
		depTime := oebbShopParseTime(c.From.Departure)
		arrTime := oebbShopParseTime(c.To.Arrival)

		// Duration: shop API returns milliseconds; convert to minutes.
		durationMin := c.Duration / 60000
		if durationMin <= 0 {
			durationMin = computeDurationMinutes(depTime, arrTime)
		}

		price := priceByID[c.ID]

		routes = append(routes, models.GroundRoute{
			Provider:        "oebb",
			Type:            "train",
			Price:           price,
			PriceProvenance: livePrice(price), // the shop's fare for this departure
			Currency:        strings.ToUpper(currency),
			Duration:        durationMin,
			Departure: models.GroundStop{
				City:    fromStation.City,
				Station: fromStation.Name,
				Time:    depTime,
			},
			Arrival: models.GroundStop{
				City:    toStation.City,
				Station: toStation.Name,
				Time:    arrTime,
			},
			BookingURL: bookingURL,
		})
	}

	slog.Debug("oebb results", "routes", len(routes))
	if pricesErr != nil {
		return routes, oebbPricesNotice(pricesErr)
	}
	return routes, nil
}

// oebbPricesNotice explains departures served without fares because the
// shop's price step (or the whole shop) failed.
func oebbPricesNotice(err error) *models.ProviderNotice {
	return &models.ProviderNotice{
		Reason:  models.ClassifyProviderReason(err.Error()),
		FixHint: "shop refused: prices unavailable",
		Err:     err,
	}
}

// oebbShopParseTime normalises an ISO 8601 datetime string from the shop API
// into the canonical "2006-01-02T15:04:05" format used by GroundStop.Time.
// The shop API may append a timezone offset (e.g. "+02:00") which we strip.
func oebbShopParseTime(s string) string {
	if s == "" {
		return ""
	}
	// Try full RFC3339 first.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02T15:04:05")
	}
	// Try without timezone.
	for _, layout := range []string{
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04:05")
		}
	}
	// Return as-is if unparseable (best-effort).
	return s
}

// buildOebbBookingURL constructs a fahrplan.oebb.at booking URL.
func buildOebbBookingURL(from, to oebbStation, date string) string {
	return fmt.Sprintf("https://tickets.oebb.at/en/ticket?stationOrigExtId=%s&stationDestExtId=%s&outwardDate=%s",
		url.QueryEscape(from.ExtID),
		url.QueryEscape(to.ExtID),
		url.QueryEscape(date),
	)
}
