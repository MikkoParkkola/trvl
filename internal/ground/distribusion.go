package ground

// Distribusion ground transport provider.
//
// Distribusion is the GDS for ground transport — aggregates buses, ferries,
// trains, and airport transfers from 2,000+ carriers worldwide. Powers
// Google Maps, Expedia, Rome2Rio, Kayak, Trip.com.
//
// API: https://api.distribusion.com/retailers/v4/ (JSONAPI spec)
// Auth: DISTRIBUSION_API_KEY environment variable
// Docs: https://docs.distribusion.com/dt/api/getting-started
// Partnership: partner@distribusion.com (free, commission-based)
//
// Station codes follow a 6-character pattern: 2-letter ISO country code +
// 4-letter city/airport code (e.g. FIHELS = Finland Helsinki, EETLLS = Estonia Tallinn).
// The exact codes are confirmed via GET /stations once an API key is available.
// The map below uses our best guesses; they will be updated after the first
// live /stations call.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// distribusionAPIBase is the Distribusion retailers API base URL.
const distribusionAPIBase = "https://api.distribusion.com/retailers/v4"

// distribusionLimiter: conservative 10 req/min until actual limits are known.
var distribusionLimiter = newProviderLimiter(6 * time.Second)

var distribusionTitleCaser = cases.Title(language.English)

// distribusionHTTPClient is a shared HTTP client for Distribusion API calls.
var distribusionHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// distribusionStationCodes maps lowercase city name / alias to Distribusion
// station codes. These are best-guess codes following the 2+4 ISO pattern.
// They will be verified and extended once GET /stations is available with a live key.
var distribusionStationCodes = map[string]string{
	// Finland
	"helsinki": "FIHELS",
	"hel":      "FIHELS",
	"turku":    "FITKUS",
	"tampere":  "FITMPX",

	// Estonia
	"tallinn": "EETLLS",
	"tll":     "EETLLS",
	"tartu":   "EETARX",

	// Latvia
	"riga": "LVRIXS",
	"rix":  "LVRIXS",

	// Lithuania
	"vilnius": "LTVNOS",
	"kaunas":  "LTKAUN",

	// Sweden
	"stockholm":  "SESTON",
	"sto":        "SESTON",
	"gothenburg": "SEGOTS",
	"göteborg":   "SEGOTS",
	"goteborg":   "SEGOTS",
	"malmö":      "SEMMAX",
	"malmo":      "SEMMAX",

	// Germany
	"berlin":    "DEBERZ",
	"ber":       "DEBERZ",
	"hamburg":   "DEHAMS",
	"munich":    "DEMUCZ",
	"münchen":   "DEMUCZ",
	"munchen":   "DEMUCZ",
	"frankfurt": "DEFRAS",
	"cologne":   "DECGNX",
	"köln":      "DECGNX",
	"koln":      "DECGNX",

	// Poland
	"warsaw": "PLWAWS",
	"waw":    "PLWAWS",
	"krakow": "PLKRKS",
	"kraków": "PLKRKS",
	"gdansk": "PLGDNS",
	"gdańsk": "PLGDNS",

	// Czech Republic
	"prague": "CZPRGS",
	"brno":   "CZBRNX",

	// Austria
	"vienna": "ATVIES",
	"wie":    "ATVIES",

	// Netherlands
	"amsterdam": "NLAMSZ",
	"ams":       "NLAMSZ",
	"rotterdam": "NLRTMS",
	"the hague": "NLHAGS",

	// Belgium
	"brussels": "BEBRUS",
	"bru":      "BEBRUS",
	"antwerp":  "BEANTS",

	// France
	"paris":     "FRPARS",
	"cdg":       "FRPARS",
	"lyon":      "FRLYSZ",
	"marseille": "FRMRSS",
	"bordeaux":  "FRBODX",
	"toulouse":  "FRTLSS",

	// United Kingdom
	"london":     "GBLONS",
	"lon":        "GBLONS",
	"manchester": "GBMANS",
	"birmingham": "GBBHXS",
	"edinburgh":  "GBEDIS",

	// Denmark
	"copenhagen": "DKCPHS",
	"cph":        "DKCPHS",
	"aarhus":     "DKAAAS",

	// Norway
	"oslo":   "NOOSX",
	"bergen": "NOBGOS",

	// Spain
	"madrid":    "ESMADS",
	"barcelona": "ESBCNS",
	"seville":   "ESSEVS",
	"valencia":  "ESVLCS",

	// Italy
	"rome":   "ITROMS",
	"milan":  "ITMILS",
	"naples": "ITNAPS",
	"turin":  "ITTORS",
	"venice": "ITIVEZ",

	// Switzerland
	"zurich": "CHZRHS",
	"geneva": "CHGEVS",
	"basel":  "CHBSLS",

	// Hungary
	"budapest": "HUBUBS",

	// Romania
	"bucharest": "ROBUHX",

	// Bulgaria
	"sofia": "BGSOFA",

	// Croatia
	"zagreb": "HRZAGS",

	// Serbia
	"belgrade": "RSBELS",

	// Greece
	"athens":       "GRATHL",
	"thessaloniki": "GRSKGS",

	// Portugal
	"lisbon": "PTLISS",
	"lis":    "PTLISS",
	"porto":  "PTOPOX",
}

// HasDistribusionKey returns true if the DISTRIBUSION_API_KEY environment
// variable is set to a non-empty value.
func HasDistribusionKey() bool {
	return os.Getenv("DISTRIBUSION_API_KEY") != ""
}

// distribusionStationCode returns the Distribusion station code for a city
// name (case-insensitive). Returns an empty string if the city is unknown.
func distribusionStationCode(city string) string {
	return distribusionStationCodes[strings.ToLower(strings.TrimSpace(city))]
}

// ---- JSONAPI response types ----

// distribusionJSONAPI is the top-level JSONAPI envelope returned by the
// Distribusion API.
type distribusionJSONAPI struct {
	Data     []distribusionData     `json:"data"`
	Included []distribusionIncluded `json:"included"`
	Meta     distribusionMeta       `json:"meta"`
}

// distribusionData is a single resource in the JSONAPI data array.
type distribusionData struct {
	ID         string                      `json:"id"`
	Type       string                      `json:"type"`
	Attributes distribusionConnectionAttrs `json:"attributes"`
}

// distribusionConnectionAttrs holds the attributes of a connection resource.
type distribusionConnectionAttrs struct {
	DepartureTime        string `json:"departure_time"` // ISO 8601
	ArrivalTime          string `json:"arrival_time"`   // ISO 8601
	DurationInMinutes    int    `json:"duration_in_minutes"`
	LowestPrice          int    `json:"lowest_price"` // price in cents
	Currency             string `json:"currency"`
	TrafficType          string `json:"traffic_type"` // "bus", "train", "ferry"
	MarketingCarrierCode string `json:"marketing_carrier_code"`
	DepartureStationCode string `json:"departure_station_code"`
	ArrivalStationCode   string `json:"arrival_station_code"`
	BookingURL           string `json:"booking_url,omitempty"`
	Available            bool   `json:"available"`
	SeatsAvailable       *int   `json:"seats_available,omitempty"`
}

// distribusionIncluded holds related resources (stations, carriers) included
// in the response per JSONAPI compound-document spec.
type distribusionIncluded struct {
	ID         string                    `json:"id"`
	Type       string                    `json:"type"`
	Attributes distribusionIncludedAttrs `json:"attributes"`
}

// distribusionIncludedAttrs holds attributes for included resources (stations
// and marketing carriers).
type distribusionIncludedAttrs struct {
	// Station attributes
	Name string  `json:"name"`
	City string  `json:"city"`
	Lat  float64 `json:"latitude,omitempty"`
	Lon  float64 `json:"longitude,omitempty"`
	// Carrier attributes
	TradeNameEn string `json:"trade_name_en,omitempty"`
}

// distribusionMeta holds metadata from the Distribusion API response.
type distribusionMeta struct {
	DepartureStationCode string `json:"departure_station_code"`
	ArrivalStationCode   string `json:"arrival_station_code"`
}

// SearchDistribusion searches Distribusion for ground transport connections
// between two cities. Returns nil, nil when no station code is known for
// either city (allows graceful degradation in the provider fan-out).
func SearchDistribusion(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	apiKey := os.Getenv("DISTRIBUSION_API_KEY")
	if apiKey == "" {
		slog.Debug("distribusion: DISTRIBUSION_API_KEY not set, skipping")
		return nil, nil
	}

	fromCode := distribusionStationCode(from)
	if fromCode == "" {
		slog.Debug("distribusion: no station code for origin", "city", from)
		return nil, nil
	}
	toCode := distribusionStationCode(to)
	if toCode == "" {
		slog.Debug("distribusion: no station code for destination", "city", to)
		return nil, nil
	}

	if currency == "" {
		currency = "EUR"
	}

	if err := distribusionLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("distribusion: rate limiter: %w", err)
	}

	apiURL := fmt.Sprintf(
		"%s/connections/find?departure_stations=%s&arrival_stations=%s&departure_date=%s&pax=1&currency=%s",
		distribusionAPIBase, fromCode, toCode, date, strings.ToUpper(currency),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("distribusion: build request: %w", err)
	}
	req.Header.Set("Api-Key", apiKey)
	req.Header.Set("Accept", "application/json")

	slog.Debug("distribusion search", "from", from, "to", to, "date", date,
		"fromCode", fromCode, "toCode", toCode)

	resp, err := distribusionHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("distribusion: HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("distribusion: invalid API key (HTTP 401)")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("distribusion: rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("distribusion: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, fmt.Errorf("distribusion: read response: %w", err)
	}

	var envelope distribusionJSONAPI
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("distribusion: decode response: %w", err)
	}

	// Build a lookup map from included resources (stations and carriers) so
	// we can enrich connection data with human-readable names.
	stationNames := make(map[string]string) // code -> city name
	carrierNames := make(map[string]string) // code -> trade name
	for _, inc := range envelope.Included {
		switch inc.Type {
		case "station":
			city := inc.Attributes.City
			if city == "" {
				city = inc.Attributes.Name
			}
			stationNames[inc.ID] = city
		case "marketing_carrier":
			name := inc.Attributes.TradeNameEn
			if name == "" {
				name = inc.ID
			}
			carrierNames[inc.ID] = name
		}
	}

	routes := make([]models.GroundRoute, 0, len(envelope.Data))
	for _, conn := range envelope.Data {
		a := conn.Attributes
		if !a.Available {
			continue
		}

		priceFloat := float64(a.LowestPrice) / 100.0
		if priceFloat <= 0 {
			continue
		}

		// Resolve city names from included resources; fall back to city
		// names derived from the station code parameter.
		depCity := stationNames[a.DepartureStationCode]
		if depCity == "" {
			depCity = distribusionTitleCaser.String(strings.ToLower(from))
		}
		arrCity := stationNames[a.ArrivalStationCode]
		if arrCity == "" {
			arrCity = distribusionTitleCaser.String(strings.ToLower(to))
		}

		transportType := normaliseDistribusionType(a.TrafficType)

		carrier := carrierNames[a.MarketingCarrierCode]
		if carrier == "" {
			carrier = a.MarketingCarrierCode
		}

		bookingURL := a.BookingURL
		if bookingURL == "" {
			bookingURL = "https://www.distribusion.com/"
		}

		route := models.GroundRoute{
			Provider: "distribusion",
			Type:     transportType,
			Price:    priceFloat,
			Currency: strings.ToUpper(a.Currency),
			Duration: a.DurationInMinutes,
			Departure: models.GroundStop{
				City: depCity,
				Time: a.DepartureTime,
			},
			Arrival: models.GroundStop{
				City: arrCity,
				Time: a.ArrivalTime,
			},
			Transfers:  0,
			BookingURL: bookingURL,
		}

		if a.SeatsAvailable != nil {
			route.SeatsLeft = a.SeatsAvailable
		}

		if carrier != "" {
			route.Amenities = []string{carrier}
		}

		routes = append(routes, route)
	}

	slog.Debug("distribusion results", "connections", len(envelope.Data), "routes", len(routes))
	return routes, nil
}

// normaliseDistribusionType converts a Distribusion traffic_type string to the
// trvl canonical transport type ("bus", "train", "ferry").
func normaliseDistribusionType(trafficType string) string {
	switch strings.ToLower(trafficType) {
	case "train", "rail":
		return "train"
	case "ferry", "sea":
		return "ferry"
	default:
		return "bus"
	}
}
