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
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/google/uuid"
)

// Italo (NTV) high-speed rail provider.
//
// Italo retired the old big.ntvspa.it SOAP guest login; the live site now runs a
// modern JSON REST API that needs no personal key and no browser:
//
//  1. POST {loginHost}/api/login {"isAnonymous":true,"workingId":<uuid>} — a BFF
//     that mints an anonymous session and returns it as a BIGSessionToken cookie
//     (a JWT, sub "WWW_VS_Anonymous", ~1h TTL). That token is the Bearer.
//  2. POST {apiHost}/api/v1/booking (Authorization: Bearer <token>) with the
//     search body — returns 202 {operationId, pollAfter} (async search kickoff).
//  3. GET {apiHost}/api/v1/booking/status/{operationId} — poll until trips
//     appear, then read trips[].travelSolutions[].journeys[].segments[].
//
// The response also carries TRENITALIA-operated solutions (Italo resells them);
// we keep only serviceProvider=="ITALO" so this provider never duplicates the
// dedicated Trenitalia provider. One search is allowed per anonymous token
// (the backend rejects a second with "toomanyconnections"), so every call mints
// a fresh token.
var (
	// italoLoginHost is the BFF that mints the anonymous session token.
	// Overridable in tests via httptest.Server.
	italoLoginHost = "https://biglietti.italotreno.com"
	// italoAPIHost is the booking API host. Overridable in tests.
	italoAPIHost = "https://api-biglietti.italotreno.com"
)

// italoLimiter: conservative 1 search / 8s (each search is login + booking +
// several status polls, so keep the effective request rate gentle on the BFF).
var italoLimiter = newProviderLimiter(8 * time.Second)

// italoClient is a shared HTTP client for Italo API calls.
var italoClient = &http.Client{Timeout: 30 * time.Second}

// italoMaxPolls / italoPollInterval bound the async status polling. Solutions
// typically materialise by the 2nd poll (~3s); the anonymous flow rarely flips
// isCompleted, so we stop as soon as parseable trips appear rather than waiting
// for completion. italoPollInterval is a var so tests can zero the wait.
const italoMaxPolls = 8

var italoPollInterval = 1500 * time.Millisecond

// italoStationCodes maps a normalised name to an Italo station code. Keys cover
// bare city names (and English/Italian aliases) resolving to the city's primary
// high-speed station, PLUS specific secondary-station names so a station-precise
// input ("Roma Tiburtina", "Milano Rogoredo") resolves to the RIGHT code instead
// of silently snapping to the city centre. Italo serves a fixed ~80-station
// network, so a static exact-match map beats fetching the 3.4 MB getStations
// list per search AND avoids the "any city prefix + station word" fuzziness that
// would mis-route station-precise inputs. A name absent here fails safe:
// HasItaloRoute returns false and Italo is skipped. Codes are Italo's own (from
// getStations, isItaloStation=true) and are live-verified.
var italoStationCodes = map[string]string{
	// Bare cities -> primary station.
	"milano": "MC_", "milan": "MC_",
	"roma": "RMT", "rome": "RMT",
	"bologna": "BC_",
	"firenze": "SMN", "florence": "SMN",
	"napoli": "NAC", "naples": "NAC",
	"torino": "OUE", "turin": "OUE",
	"venezia": "VSL", "venice": "VSL",
	"padova": "PD_", "padua": "PD_",
	"verona":  "VPN",
	"brescia": "BSC",
	"salerno": "SAL",
	"bari":    "BAC",
	"genova":  "G__", "genoa": "G__",
	"trieste":         "TSC",
	"udine":           "UDN",
	"vicenza":         "VIC",
	"trento":          "TCN",
	"bolzano":         "BLZ",
	"treviso":         "TVC",
	"pordenone":       "PNE",
	"conegliano":      "CON",
	"caserta":         "CEA",
	"benevento":       "BEN",
	"foggia":          "FG_",
	"barletta":        "BLT",
	"reggio emilia":   "AAV",
	"reggio calabria": "RCE",
	"lamezia terme":   "LON",
	"paola":           "PAR",
	"rovigo":          "R__",
	"ferrara":         "F__",
	"maratea":         "MRT",
	"sapri":           "SRI",
	"scalea":          "SDC",
	"agropoli":        "AGR",
	"aversa":          "AVR",
	// Station-precise names for multi-station cities.
	"roma termini": "RMT", "roma tiburtina": "RTB",
	"milano centrale": "MC_", "milano rogoredo": "RG_",
	"milano porta garibaldi": "MPG", "milano rho fiera": "RRO",
	"napoli centrale": "NAC", "napoli afragola": "NAF",
	"torino porta susa": "OUE", "torino porta nuova": "TOP",
	"firenze santa maria novella": "SMN", "firenze s.m. novella": "SMN",
	"firenze smn": "SMN", "firenze campo di marte": "FCM", "firenze rifredi": "RIF",
	"venezia santa lucia": "VSL", "venezia s. lucia": "VSL", "venezia mestre": "VEM",
	"genova piazza principe": "G__", "genova brignole": "GB_",
	"reggio emilia av": "AAV",
	"bologna centrale": "BC_", "verona porta nuova": "VPN",
	"bari centrale": "BAC", "trieste centrale": "TSC", "treviso centrale": "TVC",
}

// italoStationNames maps an Italo station code to its display name (for the
// GroundStop.Station label). Covers every code reachable via italoStationCodes.
var italoStationNames = map[string]string{
	"MC_": "Milano Centrale", "RG_": "Milano Rogoredo", "MPG": "Milano Porta Garibaldi",
	"RRO": "Milano Rho Fiera", "RMT": "Roma Termini", "RTB": "Roma Tiburtina",
	"BC_": "Bologna Centrale", "SMN": "Firenze S.M. Novella", "FCM": "Firenze Campo di Marte",
	"RIF": "Firenze Rifredi", "NAC": "Napoli Centrale", "NAF": "Napoli Afragola",
	"OUE": "Torino Porta Susa", "TOP": "Torino Porta Nuova", "VSL": "Venezia S. Lucia",
	"VEM": "Venezia Mestre", "PD_": "Padova", "VPN": "Verona Porta Nuova", "BSC": "Brescia",
	"SAL": "Salerno", "BAC": "Bari Centrale", "G__": "Genova Piazza Principe",
	"GB_": "Genova Brignole", "TSC": "Trieste Centrale", "UDN": "Udine", "VIC": "Vicenza",
	"TCN": "Trento", "BLZ": "Bolzano", "TVC": "Treviso Centrale", "PNE": "Pordenone",
	"CON": "Conegliano", "CEA": "Caserta", "BEN": "Benevento", "FG_": "Foggia",
	"BLT": "Barletta", "AAV": "Reggio Emilia AV", "RCE": "Reggio Calabria",
	"LON": "Lamezia Terme", "PAR": "Paola", "R__": "Rovigo", "F__": "Ferrara",
	"MRT": "Maratea", "SRI": "Sapri", "SDC": "Scalea", "AGR": "Agropoli", "AVR": "Aversa",
}

// italoStationCode resolves a user city or station name to an Italo station code
// via exact (whitespace-collapsed, lowercased) lookup.
//
// Honours English aliases ("Turin"), the Italian form, station-precise names
// ("Roma Tiburtina" -> RTB, not RMT), and an Italy qualifier ("Milan, Italy").
// A non-Italy qualifier ("Milan, Ohio") or any name not in the map resolves to
// no code, so Italo is skipped rather than returning Italian fares for the wrong
// station or a foreign city.
func italoStationCode(city string) (string, bool) {
	raw := strings.TrimSpace(city)
	lower := strings.ToLower(raw)

	if i := strings.IndexByte(lower, ','); i >= 0 {
		switch strings.TrimSpace(lower[i+1:]) {
		case "italy", "italia", "it":
			lower = strings.ToLower(strings.TrimSpace(raw[:i]))
		default:
			return "", false
		}
	}

	// Collapse internal whitespace so "Milano   Centrale" == "Milano Centrale".
	lower = strings.Join(strings.Fields(lower), " ")
	if len(lower) < 3 {
		return "", false
	}
	code, ok := italoStationCodes[lower]
	return code, ok
}

// HasItaloRoute reports whether both endpoints are cities on Italo's network.
func HasItaloRoute(from, to string) bool {
	_, ok1 := italoStationCode(from)
	_, ok2 := italoStationCode(to)
	return ok1 && ok2
}

// italoUserAgent is a browser-like UA. The login BFF (biglietti.italotreno.com)
// sits behind Akamai, which is friendlier to a browser UA than a bare client.
const italoUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

type italoLoginRequest struct {
	IsAnonymous bool   `json:"isAnonymous"`
	WorkingID   string `json:"workingId"`
}

// italoBookingRequest mirrors the site's search payload. Fields we never vary
// (promo, private offers, employee offers) marshal to their captured defaults.
type italoBookingRequest struct {
	IsRoundTrip       bool   `json:"isRoundTrip"`
	DepartureStation  string `json:"departureStation"`
	ArrivalStation    string `json:"arrivalStation"`
	DepartureDate     string `json:"departureDate"`
	PromoCode         string `json:"promoCode"`
	PassengersAges    any    `json:"passengersAges"`
	SeniorPassengers  int    `json:"seniorPassengers"`
	AdultPassengers   int    `json:"adultPassengers"`
	YoungPassengers   int    `json:"youngPassengers"`
	ChildPassengers   int    `json:"childPassengers"`
	Culture           string `json:"culture"`
	ReferrerURL       string `json:"referrerURL"`
	PromocodeAlias    string `json:"promocodeAlias"`
	HasPet            bool   `json:"hasPet"`
	ShowBestPrices    bool   `json:"showBestPrices"`
	ShowPrivateOffers bool   `json:"showPrivateOffers"`
	EmployeeOffer     any    `json:"employeeOffer"`
	PortalType        string `json:"portalType"`
	IsExclusive       bool   `json:"isExclusive"`
	FlowType          string `json:"flowType"`
}

type italoBookingResponse struct {
	OperationID string `json:"operationId"`
	PollAfter   int    `json:"pollAfter"`
	IsCompleted bool   `json:"isCompleted"`
}

type italoStatusResponse struct {
	FareTypes string      `json:"fareTypes"`
	Trips     []italoTrip `json:"trips"`
}

type italoTrip struct {
	DepartureStation string                `json:"departureStation"`
	ArrivalStation   string                `json:"arrivalStation"`
	TravelSolutions  []italoTravelSolution `json:"travelSolutions"`
}

type italoTravelSolution struct {
	Journeys []italoJourney `json:"journeys"`
}

type italoJourney struct {
	ServiceProvider string         `json:"serviceProvider"`
	Segments        []italoSegment `json:"segments"`
}

type italoSegment struct {
	DepartureStation string      `json:"departureStation"`
	ArrivalStation   string      `json:"arrivalStation"`
	Std              string      `json:"std"`
	Sta              string      `json:"sta"`
	TrainNumber      string      `json:"trainNumber"`
	EquipmentType    string      `json:"equipmentType"`
	Fares            []italoFare `json:"fares"`
}

type italoFare struct {
	ProductClass string         `json:"productClass"`
	PaxFares     []italoPaxFare `json:"paxFares"`
}

type italoPaxFare struct {
	PaxType              string  `json:"paxType"`
	FullPaxFarePrice     float64 `json:"fullPaxFarePrice"`
	FullPaxDiscountPrice float64 `json:"fullPaxDiscountPrice"`
}

// italoLogin mints a fresh anonymous session and returns its Bearer token. Each
// token is single-use for a search (the backend caps connections per session),
// so callers must not share one across searches.
func italoLogin(ctx context.Context) (string, error) {
	body, err := json.Marshal(italoLoginRequest{IsAnonymous: true, WorkingID: uuid.NewString()})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, italoLoginHost+"/api/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", italoUserAgent)

	resp, err := italoClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("italo login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("italo login: HTTP %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "BIGSessionToken" && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("italo login: BIGSessionToken cookie not set")
}

// italoStartBooking kicks off an async availability search and returns its
// operationId. The backend answers 202 (accepted) with the id to poll.
func italoStartBooking(ctx context.Context, token, osc, dsc, date string) (string, error) {
	payload := italoBookingRequest{
		DepartureStation: osc,
		ArrivalStation:   dsc,
		DepartureDate:    date,
		AdultPassengers:  1,
		Culture:          "it-IT",
		PortalType:       "B2C",
		FlowType:         "Booking",
		ShowBestPrices:   true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, italoAPIHost+"/api/v1/booking", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", italoLoginHost)
	req.Header.Set("User-Agent", italoUserAgent)

	resp, err := italoClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("italo booking: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("italo booking: HTTP %d: %s", resp.StatusCode, data)
	}
	var br italoBookingResponse
	if err := json.Unmarshal(data, &br); err != nil {
		return "", fmt.Errorf("italo booking decode: %w", err)
	}
	if br.OperationID == "" {
		return "", fmt.Errorf("italo booking: no operationId")
	}
	return br.OperationID, nil
}

// italoPollStatus polls the async operation until trips appear (a completed
// answer) or the poll budget is exhausted. Trips present — even with zero
// travelSolutions — is a real result ("no Italo service on this pair"); the
// absence of trips means the search never finished, which is reported as a
// timeout error, NOT a clean zero-route success, so an unfinished search cannot
// masquerade as "no service". Transient transport/HTTP/decode failures are
// retried within the budget and surfaced as the final error if trips never
// arrive.
func italoPollStatus(ctx context.Context, token, opID string) (*italoStatusResponse, error) {
	statusURL := italoAPIHost + "/api/v1/booking/status/" + url.PathEscape(opID)
	var lastErr error
	for i := 0; i < italoMaxPolls; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(italoPollInterval):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", italoUserAgent)

		resp, err := italoClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("italo status: %w", err)
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("italo status: HTTP %d: %.200s", resp.StatusCode, data)
			continue
		}
		var st italoStatusResponse
		if err := json.Unmarshal(data, &st); err != nil {
			lastErr = fmt.Errorf("italo status decode: %w", err)
			continue
		}
		if len(st.Trips) > 0 {
			return &st, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("italo status: timed out waiting for trips after %d polls", italoMaxPolls)
}

// SearchItalo searches Italo (NTV) for high-speed rail between two Italian
// cities. from/to are city names (e.g. "Milan", "Rome"); date is the ISO
// calendar date (year-month-day). Prices are EUR (the currency argument is
// accepted for signature parity but never drives a conversion).
func SearchItalo(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	_ = currency

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("italo: invalid date %q: %w", date, err)
	}
	osc, ok := italoStationCode(from)
	if !ok {
		return nil, fmt.Errorf("italo: unsupported origin %q", from)
	}
	dsc, ok := italoStationCode(to)
	if !ok {
		return nil, fmt.Errorf("italo: unsupported destination %q", to)
	}

	if err := italoLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("italo rate limiter: %w", err)
	}

	token, err := italoLogin(ctx)
	if err != nil {
		return nil, err
	}
	opID, err := italoStartBooking(ctx, token, osc, dsc, date)
	if err != nil {
		return nil, err
	}
	st, err := italoPollStatus(ctx, token, opID)
	if err != nil {
		return nil, err
	}

	routes := parseItaloSolutions(st, from, to, buildItaloBookingURL(osc, dsc, date))
	slog.Debug("italo results", "routes", len(routes))
	return routes, nil
}

// parseItaloSolutions maps direct ITALO-operated journeys with a positive fare
// and a trustworthy schedule into GroundRoute values. TRENITALIA-operated
// solutions (Italo resells them) are dropped so this provider never duplicates
// the dedicated Trenitalia provider.
//
// Only single-segment (direct) journeys are emitted. A direct Italo train is one
// segment whose intermediate stops are legs; a multi-segment journey is a real
// train change whose per-segment fares are unreliable (later legs return empty
// fare arrays), so pricing it would synthesise a fare that may not be sold. We
// skip those rather than mislead — Italo's product here is direct high-speed.
func parseItaloSolutions(st *italoStatusResponse, from, to, bookingURL string) []models.GroundRoute {
	var routes []models.GroundRoute
	for _, trip := range st.Trips {
		for _, sol := range trip.TravelSolutions {
			for _, j := range sol.Journeys {
				if !strings.EqualFold(j.ServiceProvider, "ITALO") || len(j.Segments) != 1 {
					continue
				}
				seg := j.Segments[0]
				price := italoSegmentPrice(seg)
				if price <= 0 {
					continue // price-centric: skip unpriced / sold-out journeys
				}
				dep, ok1 := italoParseTime(seg.Std)
				arr, ok2 := italoParseTime(seg.Sta)
				if !ok1 || !ok2 || !arr.After(dep) {
					continue // no trustworthy schedule -> skip, never emit a 0-minute train
				}
				routes = append(routes, models.GroundRoute{
					Provider: "italo",
					Type:     "train",
					Price:    price,
					Currency: "EUR",
					Duration: int(arr.Sub(dep).Minutes()),
					Departure: models.GroundStop{
						City:    from,
						Station: italoStationLabel(seg.DepartureStation),
						Time:    dep.Format(italoTimeLayout),
					},
					Arrival: models.GroundStop{
						City:    to,
						Station: italoStationLabel(seg.ArrivalStation),
						Time:    arr.Format(italoTimeLayout),
					},
					Transfers:  0,
					BookingURL: bookingURL,
				})
			}
		}
	}
	return routes
}

// italoSegmentPrice returns the cheapest bookable adult (ADT) fare across a
// segment's fare classes, preferring the discounted price. 0 means no priced
// ADT fare (sold out / unpriceable), which the caller treats as skip.
func italoSegmentPrice(seg italoSegment) float64 {
	min := 0.0
	for _, f := range seg.Fares {
		for _, pf := range f.PaxFares {
			if !strings.EqualFold(pf.PaxType, "ADT") {
				continue
			}
			p := pf.FullPaxDiscountPrice
			if p <= 0 {
				p = pf.FullPaxFarePrice
			}
			if p > 0 && (min == 0 || p < min) {
				min = p
			}
		}
	}
	return min
}

// italoTimeLayout is Italo's naive-local timestamp form ("2006-01-02T15:04:05").
const italoTimeLayout = "2006-01-02T15:04:05"

// italoParseTime parses an Italo timestamp. The API emits naive local times;
// an RFC3339 form with an offset is accepted defensively. Returns ok=false when
// neither form parses, so the caller can skip an untrustworthy schedule.
func italoParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(italoTimeLayout, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// italoStationLabel maps a station code to its display name, falling back to
// the raw code for any code outside the supported map.
func italoStationLabel(code string) string {
	if name, ok := italoStationNames[code]; ok {
		return name
	}
	return code
}

// buildItaloBookingURL constructs the site's search deep-link. The form wants
// the date as day/month/year with slash separators.
func buildItaloBookingURL(osc, dsc, date string) string {
	od := date
	if t, err := time.Parse("2006-01-02", date); err == nil {
		od = t.Format("02/01/2006")
	}
	return fmt.Sprintf(
		"%s/it/booking/ricerca-treni?osc=%s&dsc=%s&jt=single&od=%s&adt=1&yng=0&chd=0&snr=0&inf=0&pet=0&lang=it&startSearch=true",
		italoLoginHost, url.QueryEscape(osc), url.QueryEscape(dsc), url.QueryEscape(od),
	)
}
