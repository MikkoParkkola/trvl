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

	"github.com/MikkoParkkola/trvl/internal/logredact"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// europeanSleeperAPI is the booking site's own JSON API
// (booking.europeansleeper.eu talks to it from the browser).
const europeanSleeperAPI = "https://europeansleeperprod-api.azurewebsites.net"

// europeanSleeperAdultType is the booking system's adult passenger type.
const europeanSleeperAdultType = 72

// europeanSleeperLimiter: conservative 1 req/6s, shared by search and
// availability calls.
var europeanSleeperLimiter = newProviderLimiter(6 * time.Second)

// europeanSleeperClient is a shared HTTP client for European Sleeper API calls.
var europeanSleeperClient = &http.Client{
	Timeout: 30 * time.Second,
}

// EuropeanSleeperStation holds metadata for a European Sleeper station.
type EuropeanSleeperStation struct {
	LocationID string // booking-system id (not the UIC code)
	Name       string
	City       string
	Country    string
}

// europeanSleeperStations maps lowercase city name to station info. Ids come
// from the booking site's constants (__NEXT_DATA__ props.pageProps.constants)
// and each one was confirmed by a live search on 2026-11-15 or 2026-11-16.
// Stops the site maps onto another station (Zürich onto Aarau, Amsterdam onto
// Eindhoven on the Milan line) and Chiasso (no trip in a live search) are left
// out until a search confirms them as stops.
var europeanSleeperStations = map[string]EuropeanSleeperStation{
	"brussels":  {LocationID: "8800104", Name: "Bruxelles Midi/Brussel Zuid", City: "Brussels", Country: "BE"},
	"antwerp":   {LocationID: "8800210", Name: "Antwerpen Centraal", City: "Antwerp", Country: "BE"},
	"rotterdam": {LocationID: "8400530", Name: "Rotterdam Centraal", City: "Rotterdam", Country: "NL"},
	"amsterdam": {LocationID: "8400058", Name: "Amsterdam Centraal", City: "Amsterdam", Country: "NL"},
	"breda":     {LocationID: "8400131", Name: "Breda", City: "Breda", Country: "NL"},
	"eindhoven": {LocationID: "8400206", Name: "Eindhoven Centraal", City: "Eindhoven", Country: "NL"},
	"berlin":    {LocationID: "8010100", Name: "Berlin Hbf", City: "Berlin", Country: "DE"},
	"dresden":   {LocationID: "8001305", Name: "Dresden Hbf", City: "Dresden", Country: "DE"},
	"hamburg":   {LocationID: "8020401", Name: "Hamburg-Harburg", City: "Hamburg", Country: "DE"},
	"köln":      {LocationID: "8050500", Name: "Köln Hbf", City: "Köln", Country: "DE"},
	"prague":    {LocationID: "5457076", Name: "Praha hl.n.", City: "Prague", Country: "CZ"},
	"paris":     {LocationID: "8700015", Name: "Paris-Nord", City: "Paris", Country: "FR"},
	"milan":     {LocationID: "8300112", Name: "Milano Porta Garibaldi", City: "Milan", Country: "IT"},

	"roosendaal":        {LocationID: "8400526", Name: "Roosendaal", City: "Roosendaal", Country: "NL"},
	"den haag":          {LocationID: "8400280", Name: "Den Haag HS", City: "Den Haag", Country: "NL"},
	"amersfoort":        {LocationID: "8400055", Name: "Amersfoort Centraal", City: "Amersfoort", Country: "NL"},
	"deventer":          {LocationID: "8400173", Name: "Deventer", City: "Deventer", Country: "NL"},
	"berlin ostbahnhof": {LocationID: "8010110", Name: "Berlin Ostbahnhof", City: "Berlin", Country: "DE"},
	"bad schandau":      {LocationID: "8001311", Name: "Bad Schandau", City: "Bad Schandau", Country: "DE"},
	"děčín":             {LocationID: "5455659", Name: "Děčín hl.n.", City: "Děčín", Country: "CZ"},
	"ústí nad labem":    {LocationID: "5453179", Name: "Ústí nad Labem hl.n.", City: "Ústí nad Labem", Country: "CZ"},
	"verviers":          {LocationID: "8800440", Name: "Verviers-Central", City: "Verviers", Country: "BE"},
	"aachen":            {LocationID: "8052101", Name: "Aachen Hbf", City: "Aachen", Country: "DE"},
	"aarau":             {LocationID: "8500375", Name: "Aarau", City: "Aarau", Country: "CH"},
	"arth-goldau":       {LocationID: "8500004", Name: "Arth-Goldau", City: "Arth-Goldau", Country: "CH"},
	"göschenen":         {LocationID: "8500689", Name: "Göschenen", City: "Göschenen", Country: "CH"},
	"bellinzona":        {LocationID: "8500521", Name: "Bellinzona", City: "Bellinzona", Country: "CH"},
	"lugano":            {LocationID: "8500011", Name: "Lugano", City: "Lugano", Country: "CH"},
	"como":              {LocationID: "8300133", Name: "Como San Giovanni", City: "Como", Country: "IT"},
	"saint-quentin":     {LocationID: "8700433", Name: "Saint-Quentin", City: "Saint-Quentin", Country: "FR"},
	"aulnoye-aymeries":  {LocationID: "8700021", Name: "Aulnoye-Aymeries", City: "Aulnoye-Aymeries", Country: "FR"},
	"mons":              {LocationID: "8800810", Name: "Mons", City: "Mons", Country: "BE"},
	"liège":             {LocationID: "8800410", Name: "Liège-Guillemins", City: "Liège", Country: "BE"},
}

// europeanSleeperAliases maps alternative city spellings to table keys.
var europeanSleeperAliases = map[string]string{
	"praha":          "prague",
	"prag":           "prague",
	"bruxelles":      "brussels",
	"brussel":        "brussels",
	"antwerpen":      "antwerp",
	"cologne":        "köln",
	"koln":           "köln",
	"koeln":          "köln",
	"milano":         "milan",
	"the hague":      "den haag",
	"decin":          "děčín",
	"usti nad labem": "ústí nad labem",
	"goschenen":      "göschenen",
	"liege":          "liège",
	"st-quentin":     "saint-quentin",
}

// europeanSleeperLines lists each line's confirmed stops in outbound order.
// Every line also runs the reverse direction, so any two stops on one line
// form a served pair (subject to the domestic rule below).
var europeanSleeperLines = [][]string{
	{"brussels", "antwerp", "roosendaal", "rotterdam", "den haag", "amsterdam", "amersfoort", "deventer",
		"berlin", "berlin ostbahnhof", "dresden", "bad schandau", "děčín", "ústí nad labem", "prague"}, // ES453/452
	{"paris", "saint-quentin", "aulnoye-aymeries", "mons", "brussels", "liège", "hamburg",
		"berlin ostbahnhof", "berlin"}, // ES475/474
	{"brussels", "verviers", "antwerp", "breda", "eindhoven", "aachen", "köln",
		"aarau", "arth-goldau", "göschenen", "bellinzona", "lugano", "como", "milan"}, // ES401/400
}

// europeanSleeperNoDomestic holds the countries where the booking site refuses
// a journey that starts and ends in the same country
// (domesticJourneyDisabledCountries: 84 NL, 88 BE, 54 CZ, 87 FR, 85 CH, 83 IT).
var europeanSleeperNoDomestic = map[string]bool{"NL": true, "BE": true, "CZ": true, "FR": true, "CH": true, "IT": true}

func europeanSleeperKey(city string) string {
	k := strings.ToLower(strings.TrimSpace(city))
	if alias, ok := europeanSleeperAliases[k]; ok {
		return alias
	}
	return k
}

// LookupEuropeanSleeperStation resolves a city name to a European Sleeper station (case-insensitive).
func LookupEuropeanSleeperStation(city string) (EuropeanSleeperStation, bool) {
	s, ok := europeanSleeperStations[europeanSleeperKey(city)]
	return s, ok
}

// HasEuropeanSleeperStation returns true if the city has a known European Sleeper station.
func HasEuropeanSleeperStation(city string) bool {
	_, ok := LookupEuropeanSleeperStation(city)
	return ok
}

// HasEuropeanSleeperRoute reports whether European Sleeper sells a ticket
// between the two cities: both stop on one line and the pair is not a
// domestic journey the booking site refuses.
func HasEuropeanSleeperRoute(from, to string) bool {
	fromKey, toKey := europeanSleeperKey(from), europeanSleeperKey(to)
	fromSt, okFrom := europeanSleeperStations[fromKey]
	toSt, okTo := europeanSleeperStations[toKey]
	if !okFrom || !okTo || fromKey == toKey {
		return false
	}
	if fromSt.Country == toSt.Country && europeanSleeperNoDomestic[fromSt.Country] {
		return false
	}
	for _, line := range europeanSleeperLines {
		var hasFrom, hasTo bool
		for _, stop := range line {
			hasFrom = hasFrom || stop == fromKey
			hasTo = hasTo || stop == toKey
		}
		if hasFrom && hasTo {
			return true
		}
	}
	return false
}

// europeanSleeperSearchResponse is the /api/search/trains answer: one entry
// per line per day (requested day -1..+3), trip null when no train runs.
type europeanSleeperSearchResponse struct {
	RouteSearchResults []struct {
		SearchResultOutward []struct {
			RouteID string               `json:"routeId"`
			Trip    *europeanSleeperTrip `json:"trip"`
		} `json:"searchResultOutward"`
	} `json:"routeSearchResults"`
}

type europeanSleeperTrip struct {
	TrainNumber    string `json:"trainNumber"`
	TravelDate     string `json:"travelDate"` // boarding date
	DepartureTime  string `json:"departureTime"`
	ArrivalTime    string `json:"arrivalTime"`
	FromLocationID string `json:"fromLocationId"`
	ToLocationID   string `json:"toLocationId"`
	Bookable       bool   `json:"bookable"`
}

// europeanSleeperAvailabilityResponse is the /api/search/availability answer
// for one departure. Station ids and names reflect detours.
type europeanSleeperAvailabilityResponse struct {
	AvailabilityResult struct {
		Availability struct {
			DepartureStationID   string `json:"departureStationId"`
			DepartureStationName string `json:"departureStationName"`
			ArrivalStationID     string `json:"arrivalStationId"`
			ArrivalStationName   string `json:"arrivalStationName"`
			PriceClasses         []struct {
				Bookable  bool `json:"bookable"`
				FareTypes []struct {
					Bookable bool               `json:"bookable"`
					Prices   map[string]float64 `json:"prices"` // lowercase currency -> amount
				} `json:"fareTypes"`
			} `json:"priceClasses"`
		} `json:"availability"`
	} `json:"availabilityResult"`
}

// SearchEuropeanSleeper searches European Sleeper night trains between two
// cities on the booking site's API. Each departure on the requested boarding
// date is priced with its own availability call; when that call fails the
// departure stays listed with an unavailable price.
func SearchEuropeanSleeper(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	if !HasEuropeanSleeperRoute(from, to) {
		return nil, fmt.Errorf("european sleeper: no line sells %s to %s: %w", from, to, models.ErrNotApplicable)
	}
	fromSt, _ := LookupEuropeanSleeperStation(from)
	toSt, _ := LookupEuropeanSleeperStation(to)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}

	var search europeanSleeperSearchResponse
	err := europeanSleeperPost(ctx, "/api/search/trains", map[string]any{
		"fromLocationId": fromSt.LocationID,
		"toLocationId":   toSt.LocationID,
		"departureDate":  date + "T00:00:00.000Z",
		"returnDate":     nil,
		"passengerTypes": []int{europeanSleeperAdultType},
		"bicycleCount":   0,
		"petsCount":      0,
	}, &search)
	if err != nil {
		return nil, fmt.Errorf("european sleeper search: %w", err)
	}
	if search.RouteSearchResults == nil {
		return nil, fmt.Errorf("european sleeper search: no routeSearchResults: %w", models.ErrParseFailed)
	}

	bookingURL := buildEuropeanSleeperBookingURL(fromSt, toSt, date)
	routes := []models.GroundRoute{}
	for _, result := range search.RouteSearchResults {
		for _, day := range result.SearchResultOutward {
			if day.Trip == nil || day.Trip.TravelDate != date {
				continue
			}
			routes = append(routes, europeanSleeperRoute(ctx, day.RouteID, *day.Trip, fromSt, toSt, currency, bookingURL))
		}
	}
	slog.Debug("european sleeper results", "from", fromSt.City, "to", toSt.City, "date", date, "routes", len(routes))
	return routes, nil
}

// europeanSleeperRoute builds one route and prices it. Price and detour
// station names come from the availability call; its failure only marks the
// price unavailable.
func europeanSleeperRoute(ctx context.Context, routeID string, trip europeanSleeperTrip, fromSt, toSt EuropeanSleeperStation, currency, bookingURL string) models.GroundRoute {
	dep := models.GroundStop{City: fromSt.City, Station: fromSt.Name, Time: normaliseTimeString(trip.DepartureTime)}
	arr := models.GroundStop{City: toSt.City, Station: toSt.Name, Time: normaliseTimeString(trip.ArrivalTime)}
	price, priceCurrency, provenance := 0.0, currency, models.PriceUnavailable

	if trip.Bookable {
		var avail europeanSleeperAvailabilityResponse
		err := europeanSleeperPost(ctx, "/api/search/availability", map[string]any{
			"trainRouteId":             routeID,
			"fromLocationId":           fromSt.LocationID,
			"toLocationId":             toSt.LocationID,
			"passengerTypes":           []int{europeanSleeperAdultType},
			"trainNumber":              trip.TrainNumber,
			"travelDate":               trip.TravelDate,
			"bicycleCount":             0,
			"connectingTrainSelection": nil,
		}, &avail)
		if err != nil {
			slog.Debug("european sleeper availability failed", "train", trip.TrainNumber, "date", trip.TravelDate, "err", logredact.Err(err))
		} else {
			a := avail.AvailabilityResult.Availability
			// A detour moves the stop; only the availability answer names it.
			if a.DepartureStationID != "" && a.DepartureStationID != fromSt.LocationID && a.DepartureStationName != "" {
				dep.Station = a.DepartureStationName
			}
			if a.ArrivalStationID != "" && a.ArrivalStationID != toSt.LocationID && a.ArrivalStationName != "" {
				arr.Station = a.ArrivalStationName
			}
			cheapest := func(key string) float64 {
				best := 0.0
				for _, pc := range a.PriceClasses {
					if !pc.Bookable {
						continue
					}
					for _, f := range pc.FareTypes {
						if p := f.Prices[key]; f.Bookable && p > 0 && (best == 0 || p < best) {
							best = p
						}
					}
				}
				return best
			}
			if p := cheapest(strings.ToLower(currency)); p > 0 {
				price, provenance = p, models.PriceLive
			} else if p := cheapest("eur"); p > 0 {
				price, priceCurrency, provenance = p, "EUR", models.PriceLive
			}
		}
	}

	return models.GroundRoute{
		Provider:        "european_sleeper",
		Type:            "train",
		Price:           price,
		Currency:        priceCurrency,
		PriceProvenance: provenance,
		Duration:        computeDurationMinutes(dep.Time, arr.Time),
		Departure:       dep,
		Arrival:         arr,
		Legs: []models.GroundLeg{{
			Type:      "train",
			Provider:  "European Sleeper ES" + trip.TrainNumber,
			Departure: dep,
			Arrival:   arr,
			Duration:  computeDurationMinutes(dep.Time, arr.Time),
		}},
		BookingURL: bookingURL,
	}
}

// europeanSleeperPost sends one JSON POST behind the shared limiter and
// decodes the answer into out. 429 and 5xx (the API goes down for nightly
// maintenance, roughly 23:00-03:30 CET) wrap ErrRateLimited; any other
// non-2xx or an undecodable body wraps ErrParseFailed.
func europeanSleeperPost(ctx context.Context, path string, body, out any) error {
	if err := europeanSleeperLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, europeanSleeperAPI+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://booking.europeansleeper.eu")
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := europeanSleeperClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return err
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("HTTP %d: %w", resp.StatusCode, models.ErrRateLimited)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		snippet := strings.TrimSpace(string(raw[:min(len(raw), 200)]))
		return fmt.Errorf("HTTP %d: %s: %w", resp.StatusCode, snippet, models.ErrParseFailed)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode: %v: %w", err, models.ErrParseFailed)
	}
	return nil
}

// buildEuropeanSleeperBookingURL links to the booking site with the search
// pre-filled (the site reads these query keys back into its search request).
func buildEuropeanSleeperBookingURL(from, to EuropeanSleeperStation, date string) string {
	return fmt.Sprintf("https://booking.europeansleeper.eu/en?departureStation=%s&arrivalStation=%s&departureDate=%s&passengerTypes=%d&isSelected=true",
		url.QueryEscape(from.LocationID), url.QueryEscape(to.LocationID), url.QueryEscape(date), europeanSleeperAdultType)
}

// normaliseTimeString trims an ISO 8601 datetime string to the canonical
// "2006-01-02T15:04:05" format, stripping timezone suffixes.
func normaliseTimeString(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("2006-01-02T15:04:05")
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04:05")
		}
	}
	return s
}
