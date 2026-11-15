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
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Snälltåget's booking app (www.snalltaget.se) fetches an anonymous guest
// token from snalltagetTokenURL and searches journeys on snalltagetAPIBase,
// the apiUrl in the page's #booking-app data-settings. No account is involved.
const (
	snalltagetTokenURL = "https://www.snalltaget.se/token/v2"
	snalltagetAPIBase  = "https://apiv2.snalltaget.se"
	// The token endpoint answers 403 to Go's default User-Agent.
	snalltagetUserAgent = "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)"
)

// snalltagetLimiter: conservative 1 req/6s.
var snalltagetLimiter = newProviderLimiter(6 * time.Second)

// snalltagetClient is a shared HTTP client for Snälltåget API calls.
var snalltagetClient = &http.Client{
	Timeout: 30 * time.Second,
}

// SnalltagetStation holds metadata for a Snälltåget stop.
type SnalltagetStation struct {
	UIC     string // apiv2 stop code (its uicStationCode): a UIC number or a literal such as "Malmö C"
	Name    string
	City    string
	Country string
}

// snalltagetStations maps lowercase city name to stop info. Codes match the
// apiv2 /navigation/stops list (checked 2026-11-15).
var snalltagetStations = map[string]SnalltagetStation{
	// Sweden — Stockholm–Malmö and onward to Copenhagen, Hamburg and Berlin
	"stockholm":  {UIC: "740000001", Name: "Stockholm C", City: "Stockholm", Country: "SE"},
	"norrköping": {UIC: "740000007", Name: "Norrköping C", City: "Norrköping", Country: "SE"},
	"norrkoping": {UIC: "740000007", Name: "Norrköping C", City: "Norrköping", Country: "SE"},
	"linköping":  {UIC: "740000009", Name: "Linköping C", City: "Linköping", Country: "SE"},
	"linkoping":  {UIC: "740000009", Name: "Linköping C", City: "Linköping", Country: "SE"},
	"alvesta":    {UIC: "740000004", Name: "Alvesta", City: "Alvesta", Country: "SE"},
	"hässleholm": {UIC: "740000006", Name: "Hässleholm C", City: "Hässleholm", Country: "SE"},
	"hassleholm": {UIC: "740000006", Name: "Hässleholm C", City: "Hässleholm", Country: "SE"},
	"lund":       {UIC: "740000120", Name: "Lund C", City: "Lund", Country: "SE"},
	"malmö":      {UIC: "Malmö C", Name: "Malmö C", City: "Malmö", Country: "SE"},
	"malmo":      {UIC: "Malmö C", Name: "Malmö C", City: "Malmö", Country: "SE"},
	"malmö c":    {UIC: "Malmö C", Name: "Malmö C", City: "Malmö", Country: "SE"},

	// Sweden — winter night train (Stockholm–Åre/Duved)
	"åre":   {UIC: "740000115", Name: "Åre", City: "Åre", Country: "SE"},
	"are":   {UIC: "740000115", Name: "Åre", City: "Åre", Country: "SE"},
	"duved": {UIC: "740000308", Name: "Duved", City: "Duved", Country: "SE"},

	// Denmark and Germany
	"copenhagen": {UIC: "Köpenhamn", Name: "Copenhagen", City: "Copenhagen", Country: "DK"},
	"københavn":  {UIC: "Köpenhamn", Name: "Copenhagen", City: "Copenhagen", Country: "DK"},
	"kobenhavn":  {UIC: "Köpenhamn", Name: "Copenhagen", City: "Copenhagen", Country: "DK"},
	"köpenhamn":  {UIC: "Köpenhamn", Name: "Copenhagen", City: "Copenhagen", Country: "DK"},
	"kopenhamn":  {UIC: "Köpenhamn", Name: "Copenhagen", City: "Copenhagen", Country: "DK"},
	"hamburg":    {UIC: "Hamburg", Name: "Hamburg Hbf", City: "Hamburg", Country: "DE"},
	"berlin":     {UIC: "Berlin", Name: "Berlin Hbf", City: "Berlin", Country: "DE"},
}

// LookupSnalltagetStation resolves a city name to a Snälltåget station (case-insensitive).
func LookupSnalltagetStation(city string) (SnalltagetStation, bool) {
	s, ok := snalltagetStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasSnalltagetStation returns true if the city has a known Snälltåget station.
func HasSnalltagetStation(city string) bool {
	_, ok := LookupSnalltagetStation(city)
	return ok
}

// HasSnalltagetRoute returns true if at least one of the two cities is on the
// Snälltåget network. Snälltåget is a niche night-train operator so we query
// when either endpoint matches.
func HasSnalltagetRoute(from, to string) bool {
	return HasSnalltagetStation(from) || HasSnalltagetStation(to)
}

// snalltagetSearchResponse is the apiv2 /orientation/searchjourney answer.
// Offer is a pointer so a body without one (a retired or foreign shape) is a
// parse failure, while an offer with no routes is an honest empty day.
type snalltagetSearchResponse struct {
	Offer *struct {
		Travels []struct {
			Routes []snalltagetRoute `json:"routes"`
		} `json:"travels"`
	} `json:"offer"`
}

type snalltagetRoute struct {
	Legs []snalltagetLeg `json:"legs"`
	// BundleAvailability is per route: the booking app disables a route
	// whose availability is false, so none of its bundles can be booked.
	BundleAvailability struct {
		Availability bool `json:"availability"`
	} `json:"bundleAvailability"`
	Bundles []struct {
		Price float64 `json:"price"`
	} `json:"bundles"`
}

type snalltagetLeg struct {
	ServiceType struct {
		Modality string `json:"modality"` // LONG_DISTANCE_TRAINS, SLEEPER_RAIL_SERVICE, BUS, ...
		Name     string `json:"name"`
	} `json:"serviceType"`
	DepartureStation struct {
		Name string `json:"name"`
		Time string `json:"departureTimestamp"`
	} `json:"departureStation"`
	ArrivalStation struct {
		Name string `json:"name"`
		Time string `json:"arrivalTimestamp"`
	} `json:"arrivalStation"`
}

// SearchSnalltaget searches Snälltåget journeys for one adult. Fares are the
// booking app's bundles for each departure, charged in SEK (the site's
// checkout hard-codes SEK; the answer carries no currency), so currency is
// ignored and the merge pass converts.
func SearchSnalltaget(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupSnalltagetStation(from)
	if !ok {
		return nil, fmt.Errorf("no Snälltåget station for %q: %w", from, models.ErrNotApplicable)
	}
	toStation, ok := LookupSnalltagetStation(to)
	if !ok {
		return nil, fmt.Errorf("no Snälltåget station for %q: %w", to, models.ErrNotApplicable)
	}

	if err := snalltagetLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("snalltaget rate limiter: %w", err)
	}
	slog.Debug("snalltaget search", "from", fromStation.City, "to", toStation.City, "date", date)

	payload, err := json.Marshal(map[string]any{
		"origin":        fromStation.UIC,
		"destination":   toStation.UIC,
		"departure":     date,
		"oppositedate":  nil,
		"passengers":    []map[string]string{{"type": "AD"}},
		"travelWithpet": false,
	})
	if err != nil {
		return nil, fmt.Errorf("snalltaget marshal: %w", err)
	}
	resp, err := snalltagetSearchJourney(ctx, payload)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, httpStatusError("snalltaget", "searchjourney", resp.StatusCode)
	}

	var answer snalltagetSearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*1024*1024)).Decode(&answer); err != nil {
		return nil, fmt.Errorf("snalltaget searchjourney decode: %v: %w", err, models.ErrParseFailed)
	}
	if answer.Offer == nil {
		return nil, fmt.Errorf("snalltaget searchjourney: no offer in the answer: %w", models.ErrParseFailed)
	}

	routes := parseSnalltagetRoutes(answer, fromStation, toStation, buildSnalltagetBookingURL(fromStation, toStation, date))
	slog.Debug("snalltaget results", "routes", len(routes))
	return routes, nil
}

// snalltagetSearchJourney posts the search with the cached guest token. A
// 401 means the token expired or was revoked early: it is dropped and the
// search retried once with a fresh one; a second 401 goes to the caller.
func snalltagetSearchJourney(ctx context.Context, payload []byte) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		token, err := snalltagetGuestToken(ctx)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, snalltagetAPIBase+"/orientation/searchjourney", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Accept-Language", "en")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", snalltagetUserAgent)

		resp, err := snalltagetClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("snalltaget search: %w", err)
		}
		if resp.StatusCode != http.StatusUnauthorized || attempt == 2 {
			return resp, nil
		}
		_ = resp.Body.Close()
		snalltagetForgetToken(token)
	}
}

// snalltagetTokenCache keeps the guest token between searches until
// snalltagetTokenMargin before its expires_in runs out.
var snalltagetTokenCache struct {
	sync.Mutex
	token   string
	expires time.Time
}

const snalltagetTokenMargin = time.Minute

// snalltagetForgetToken drops token from the cache unless another search has
// already replaced it.
func snalltagetForgetToken(token string) {
	snalltagetTokenCache.Lock()
	defer snalltagetTokenCache.Unlock()
	if snalltagetTokenCache.token == token {
		snalltagetTokenCache.token = ""
	}
}

// snalltagetGuestToken returns the cached anonymous bearer token the booking
// app uses, fetching a new one when none is valid. The lock is held across
// the fetch so concurrent searches share one token request.
func snalltagetGuestToken(ctx context.Context) (string, error) {
	c := &snalltagetTokenCache
	c.Lock()
	defer c.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snalltagetTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", snalltagetUserAgent)

	resp, err := snalltagetClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("snalltaget token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", httpStatusError("snalltaget", "token", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&tok); err != nil {
		return "", fmt.Errorf("snalltaget token decode: %v: %w", err, models.ErrParseFailed)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("snalltaget token: no access_token: %w", models.ErrParseFailed)
	}
	// A token with expires_in inside the margin serves this search only.
	c.token = tok.AccessToken
	c.expires = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - snalltagetTokenMargin)
	return c.token, nil
}

// parseSnalltagetRoutes turns each route into a GroundRoute priced at its
// cheapest priced bundle when the route is bookable (what the booking app
// offers for that departure),
// with station names taken from the legs: Copenhagen trains, for one, end at
// København Syd.
func parseSnalltagetRoutes(answer snalltagetSearchResponse, from, to SnalltagetStation, bookingURL string) []models.GroundRoute {
	var routes []models.GroundRoute
	for _, travel := range answer.Offer.Travels {
		for _, r := range travel.Routes {
			if len(r.Legs) == 0 {
				continue
			}
			first, last := r.Legs[0], r.Legs[len(r.Legs)-1]
			dep := normaliseTimeString(first.DepartureStation.Time)
			arr := normaliseTimeString(last.ArrivalStation.Time)

			price := 0.0 // no bookable bundle: the merge pass shows it unavailable
			if r.BundleAvailability.Availability {
				for _, b := range r.Bundles {
					if b.Price > 0 && (price == 0 || b.Price < price) {
						price = b.Price
					}
				}
			}

			legs := make([]models.GroundLeg, 0, len(r.Legs))
			for i, l := range r.Legs {
				legDep, legArr := normaliseTimeString(l.DepartureStation.Time), normaliseTimeString(l.ArrivalStation.Time)
				// Only the route's ends have a known city; a change station has just its name.
				depCity, arrCity := "", ""
				if i == 0 {
					depCity = from.City
				}
				if i == len(r.Legs)-1 {
					arrCity = to.City
				}
				legs = append(legs, models.GroundLeg{
					Type:      snalltagetLegType(l.ServiceType.Modality),
					Provider:  firstNonEmpty(l.ServiceType.Name, "Snälltåget"),
					Departure: models.GroundStop{City: depCity, Station: l.DepartureStation.Name, Time: legDep},
					Arrival:   models.GroundStop{City: arrCity, Station: l.ArrivalStation.Name, Time: legArr},
					Duration:  computeDurationMinutes(legDep, legArr),
				})
			}

			routes = append(routes, models.GroundRoute{
				Provider:        "snalltaget",
				Type:            "train",
				Price:           price,
				Currency:        "SEK",
				PriceProvenance: livePrice(price),
				Duration:        computeDurationMinutes(dep, arr),
				Departure: models.GroundStop{
					City:    from.City,
					Station: firstNonEmpty(first.DepartureStation.Name, from.Name),
					Time:    dep,
				},
				Arrival: models.GroundStop{
					City:    to.City,
					Station: firstNonEmpty(last.ArrivalStation.Name, to.Name),
					Time:    arr,
				},
				Transfers:  len(r.Legs) - 1,
				Legs:       legs,
				BookingURL: bookingURL,
			})
		}
	}
	return routes
}

// snalltagetLegType maps an apiv2 modality to a leg type; replacement buses
// are the only non-rail legs seen on these routes.
func snalltagetLegType(modality string) string {
	if modality == "BUS" {
		return "bus"
	}
	return "train"
}

// buildSnalltagetBookingURL links to the booking app, which runs on any page
// holding #booking-app: it reads from, to and date, matches from and to by
// exact stop code, and treats single as one-way.
func buildSnalltagetBookingURL(from, to SnalltagetStation, date string) string {
	q := url.Values{"from": {from.UIC}, "to": {to.UIC}, "date": {date}, "single": {"1"}}
	return "https://www.snalltaget.se/en?" + q.Encode()
}
