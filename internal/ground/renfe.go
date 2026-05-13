package ground

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// renfeAPIBase is the Renfe REST API endpoint for price/calendar queries.
const renfeAPIBase = "https://wsrestcorp.renfe.es/api/wsrviajeros/vhi_priceCalendar"

// renfeLimiter: conservative 5 req/min.
var renfeLimiter = newProviderLimiter(12 * time.Second)

// renfeClient is a shared HTTP client for Renfe API calls.
var renfeClient = &http.Client{
	Timeout: 30 * time.Second,
}

// RenfeStation holds metadata for a Renfe station.
type RenfeStation struct {
	Code    string // IATA-style 3-letter code used by renfe.com
	Numeric string // Numeric station ID used by venta.renfe.com
	Name    string
	City    string
	Country string
}

// renfeStations maps lowercase city name to Renfe station metadata.
var renfeStations = map[string]RenfeStation{
	// Spain
	"madrid":                 {Code: "MAD", Numeric: "60000", Name: "Madrid Puerta de Atocha", City: "Madrid", Country: "ES"},
	"barcelona":              {Code: "BCN", Numeric: "71801", Name: "Barcelona Sants", City: "Barcelona", Country: "ES"},
	"seville":                {Code: "SVQ", Numeric: "51300", Name: "Sevilla Santa Justa", City: "Seville", Country: "ES"},
	"sevilla":                {Code: "SVQ", Numeric: "51300", Name: "Sevilla Santa Justa", City: "Seville", Country: "ES"},
	"valencia":               {Code: "VLC", Numeric: "65000", Name: "Valencia Joaquin Sorolla", City: "Valencia", Country: "ES"},
	"malaga":                 {Code: "AGP", Numeric: "61400", Name: "Malaga Maria Zambrano", City: "Malaga", Country: "ES"},
	"bilbao":                 {Code: "BIO", Numeric: "70200", Name: "Bilbao Abando", City: "Bilbao", Country: "ES"},
	"zaragoza":               {Code: "ZAZ", Numeric: "65100", Name: "Zaragoza Delicias", City: "Zaragoza", Country: "ES"},
	"cordoba":                {Code: "XWA", Numeric: "51600", Name: "Córdoba", City: "Cordoba", Country: "ES"},
	"alicante":               {Code: "ALC", Numeric: "65200", Name: "Alicante", City: "Alicante", Country: "ES"},
	"granada":                {Code: "GRX", Numeric: "61500", Name: "Granada", City: "Granada", Country: "ES"},
	"pamplona":               {Code: "PNA", Numeric: "70600", Name: "Pamplona Irunlarrea", City: "Pamplona", Country: "ES"},
	"san sebastian":          {Code: "EAS", Numeric: "70100", Name: "San Sebastián - Donostia", City: "San Sebastian", Country: "ES"},
	"donostia":               {Code: "EAS", Numeric: "70100", Name: "San Sebastián - Donostia", City: "San Sebastian", Country: "ES"},
	"valladolid":             {Code: "VLL", Numeric: "62200", Name: "Valladolid Campo Grande", City: "Valladolid", Country: "ES"},
	"murcia":                 {Code: "MJV", Numeric: "65300", Name: "Murcia del Carmen", City: "Murcia", Country: "ES"},
	"gijon":                  {Code: "GIJ", Numeric: "20101", Name: "Gijón Cercanías", City: "Gijón", Country: "ES"},
	"salamanca":              {Code: "SLM", Numeric: "63200", Name: "Salamanca", City: "Salamanca", Country: "ES"},
	"toledo":                 {Code: "TOJ", Numeric: "60901", Name: "Toledo", City: "Toledo", Country: "ES"},
	"cadiz":                  {Code: "XRY", Numeric: "51100", Name: "Cádiz", City: "Cadiz", Country: "ES"},
	"tarragona":              {Code: "TGN", Numeric: "71500", Name: "Tarragona", City: "Tarragona", Country: "ES"},
	"santiago de compostela": {Code: "SCQ", Numeric: "36205", Name: "Santiago de Compostela", City: "Santiago de Compostela", Country: "ES"},
	// International (SNCF high-speed connections via Renfe-SNCF)
	"paris":     {Code: "PAR", Numeric: "", Name: "Paris Gare de Lyon", City: "Paris", Country: "FR"},
	"marseille": {Code: "MRS", Numeric: "", Name: "Marseille Saint-Charles", City: "Marseille", Country: "FR"},
	"lyon":      {Code: "LYS", Numeric: "", Name: "Lyon Part-Dieu", City: "Lyon", Country: "FR"},
}

// LookupRenfeStation resolves a city name to a Renfe station (case-insensitive).
func LookupRenfeStation(city string) (RenfeStation, bool) {
	s, ok := renfeStations[strings.ToLower(strings.TrimSpace(city))]
	return s, ok
}

// HasRenfeRoute returns true if both cities have Renfe stations and at least
// one is a Spanish domestic station (Renfe primarily serves Spain).
func HasRenfeRoute(from, to string) bool {
	fromStation, fromOK := LookupRenfeStation(from)
	toStation, toOK := LookupRenfeStation(to)
	if !fromOK || !toOK {
		return false
	}
	// Require at least one Spanish station.
	return fromStation.Country == "ES" || toStation.Country == "ES"
}

// renfePriceCalendarRequest is the body sent to the Renfe price calendar API.
// Field names are the actual names validated by the server.
type renfePriceCalendarRequest struct {
	OriginID     string            `json:"originId"`
	DestinyID    string            `json:"destinyId"`
	InitDate     string            `json:"initDate"`
	EndDate      string            `json:"endDate"`
	SalesChannel renfeSalesChannel `json:"salesChannel"`
}

type renfeSalesChannel struct {
	CodApp string `json:"codApp"`
}

// renfePriceCalendarResponse is the top-level API response.
type renfePriceCalendarResponse struct {
	Origin      renfeStationInfo    `json:"origin"`
	Destination renfeStationInfo    `json:"destination"`
	Journeys    []renfeJourneyEntry `json:"journeysPriceCalendar"`
}

type renfeStationInfo struct {
	Name  string `json:"name"`
	ExtID string `json:"extId"`
}

// renfeJourneyEntry is one calendar day entry with minimum price.
type renfeJourneyEntry struct {
	Date              string  `json:"date"`
	MinPriceAvailable bool    `json:"minPriceAvailable"`
	MinPrice          float64 `json:"minPrice"`
}

// buildRenfeBookingURL builds a booking deep-link for venta.renfe.com.
func buildRenfeBookingURL(from, to RenfeStation, date string) string {
	return fmt.Sprintf(
		"https://venta.renfe.com/vol/buscarTren.do?tipoBusqueda=ida&origen=%s&destino=%s&fechaIda=%s",
		from.Numeric, to.Numeric, date,
	)
}

// SearchRenfe searches Renfe for train fares between two cities using the
// Renfe public REST API (wsrestcorp.renfe.es/api/wsrviajeros/vhi_priceCalendar).
func SearchRenfe(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromStation, ok := LookupRenfeStation(from)
	if !ok {
		return nil, fmt.Errorf("no Renfe station for %q", from)
	}
	toStation, ok := LookupRenfeStation(to)
	if !ok {
		return nil, fmt.Errorf("no Renfe station for %q", to)
	}

	// Both must have numeric IDs for the REST API.
	if fromStation.Numeric == "" || toStation.Numeric == "" {
		return nil, fmt.Errorf("renfe: no numeric station ID for route %q -> %q", from, to)
	}

	if currency == "" {
		currency = "EUR"
	}

	// Validate date format.
	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("invalid date %q: %w", date, err)
	}

	if err := renfeLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("renfe rate limiter: %w", err)
	}

	slog.Debug("renfe search", "from", fromStation.City, "to", toStation.City, "date", date)

	payload := renfePriceCalendarRequest{
		OriginID:     fromStation.Numeric,
		DestinyID:    toStation.Numeric,
		InitDate:     date,
		EndDate:      date,
		SalesChannel: renfeSalesChannel{CodApp: "VLP"},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("renfe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, renfeAPIBase, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := renfeClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("renfe search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("renfe: HTTP %d: %s", resp.StatusCode, respBody)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("renfe read: %w", err)
	}
	slog.Debug("renfe raw response", "status", resp.StatusCode, "body_len", len(respBody))

	var apiResp renfePriceCalendarResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("renfe decode: %w", err)
	}

	bookingURL := buildRenfeBookingURL(fromStation, toStation, date)
	var routes []models.GroundRoute

	for _, entry := range apiResp.Journeys {
		if entry.Date != date {
			continue
		}
		if !entry.MinPriceAvailable || entry.MinPrice <= 0 {
			continue
		}
		routes = append(routes, models.GroundRoute{
			Provider: "renfe",
			Type:     "train",
			Price:    entry.MinPrice,
			Currency: strings.ToUpper(currency),
			Departure: models.GroundStop{
				City:    fromStation.City,
				Station: fromStation.Name,
				Time:    date + "T00:00:00",
			},
			Arrival: models.GroundStop{
				City:    toStation.City,
				Station: toStation.Name,
			},
			BookingURL: bookingURL,
		})
	}

	slog.Debug("renfe results", "routes", len(routes))
	return routes, nil
}
