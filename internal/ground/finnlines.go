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

// finnlinesGraphQL is the AWS AppSync GraphQL endpoint for Finnlines booking.
const finnlinesGraphQL = "https://dm3xyy44wbeivgqmeymvmw22be.appsync-api.eu-central-1.amazonaws.com/graphql"

// finnlinesAPIKey is the public API key embedded in the booking SPA JS bundle.
const finnlinesAPIKey = "da2-zvuktusyubbstlw7khps4vyeie"

// finnlinesLimiter: 10 req/min to be respectful.
var finnlinesLimiter = newProviderLimiter(6 * time.Second)

// finnlinesClient is a shared HTTP client for Finnlines API calls.
var finnlinesClient = &http.Client{
	Timeout: 30 * time.Second,
}

// finnlinesPort holds metadata for a Finnlines ferry port.
type finnlinesPort struct {
	Code string // Finnlines port code (FIHEL, DETRV, FINLI, SEKPS, etc.)
	Name string // Full port/terminal name
	City string // Display city name
}

// finnlinesPorts maps lowercase city name / alias to Finnlines port metadata.
var finnlinesPorts = map[string]finnlinesPort{
	// Helsinki
	"helsinki": {Code: "FIHEL", Name: "Helsinki Vuosaari Harbour", City: "Helsinki"},
	"hel":      {Code: "FIHEL", Name: "Helsinki Vuosaari Harbour", City: "Helsinki"},

	// Naantali
	"naantali": {Code: "FINLI", Name: "Naantali Harbour", City: "Naantali"},
	"nli":      {Code: "FINLI", Name: "Naantali Harbour", City: "Naantali"},

	// Travemünde (Germany)
	"travemünde": {Code: "DETRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},
	"travemunde": {Code: "DETRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},
	"trv":        {Code: "DETRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},

	// Rostock (Germany) — same terminal area as Travemünde for some services
	"rostock": {Code: "DETRV", Name: "Travemünde Ferry Terminal", City: "Travemünde"},

	// Kapellskär (Sweden)
	"kapellskär": {Code: "SEKPS", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},
	"kapellskar": {Code: "SEKPS", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},
	"kps":        {Code: "SEKPS", Name: "Kapellskär Ferry Terminal", City: "Kapellskär"},

	// Malmö (Sweden)
	"malmö": {Code: "SEMMA", Name: "Malmö Ferry Terminal", City: "Malmö"},
	"malmo": {Code: "SEMMA", Name: "Malmö Ferry Terminal", City: "Malmö"},
	"mma":   {Code: "SEMMA", Name: "Malmö Ferry Terminal", City: "Malmö"},

	// Świnoujście (Poland)
	"świnoujście": {Code: "PLSWI", Name: "Świnoujście Ferry Terminal", City: "Świnoujście"},
	"swinoujscie": {Code: "PLSWI", Name: "Świnoujście Ferry Terminal", City: "Świnoujście"},
	"swi":         {Code: "PLSWI", Name: "Świnoujście Ferry Terminal", City: "Świnoujście"},

	// Långnäs (Åland)
	"långnäs": {Code: "FILAN", Name: "Långnäs Ferry Terminal", City: "Långnäs"},
	"langnäs": {Code: "FILAN", Name: "Långnäs Ferry Terminal", City: "Långnäs"},
	"langnas": {Code: "FILAN", Name: "Långnäs Ferry Terminal", City: "Långnäs"},
}

// finnlinesOvernightRoutes identifies route pairs where crossing exceeds 12 hours
// and a cabin is required for booking. Keyed by "DEPARTURE-ARRIVAL".
var finnlinesOvernightRoutes = map[string]bool{
	"FIHEL-DETRV": true, // Helsinki → Travemünde (~29h)
	"DETRV-FIHEL": true, // Travemünde → Helsinki (~30h)
	"FIHEL-PLSWI": true, // Helsinki → Świnoujście (~19h)
	"PLSWI-FIHEL": true, // Świnoujście → Helsinki (~19h)
	"SEMMA-DETRV": true, // Malmö → Travemünde (~9-15h, overnight sailings)
	"DETRV-SEMMA": true, // Travemünde → Malmö (~9-15h, overnight sailings)
}

// isFinnlinesOvernightRoute returns true if the route typically requires cabin accommodation.
func isFinnlinesOvernightRoute(fromCode, toCode string) bool {
	return finnlinesOvernightRoutes[fromCode+"-"+toCode]
}

// finnlinesProduct represents a single product from the ListProductsAvailability query.
type finnlinesProduct struct {
	Code          string  `json:"code"`
	Type          string  `json:"type"`
	Name          string  `json:"name"`
	Desc          string  `json:"desc"`
	MaxPeople     int     `json:"maxPeople"`
	Available     bool    `json:"available"`
	ChargePerUnit float64 `json:"chargePerUnit"` // cents
}

// finnlinesProductResponse wraps the GraphQL response for product availability.
type finnlinesProductResponse struct {
	Data struct {
		ListProductsAvailability []finnlinesProduct `json:"listProductsAvailability"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// fetchFinnlinesProducts queries available products (cabins, seats) for a sailing.
func fetchFinnlinesProducts(ctx context.Context, fromCode, toCode, date, depTime string) ([]finnlinesProduct, error) {
	query := `query ListProductsAvailability($query:ProductsQuery!){listProductsAvailability(query:$query){...on Product{code type name desc maxPeople available chargePerUnit}...on ApiError{errorCode errorMessage}}}`

	variables := map[string]any{
		"query": map[string]any{
			"currency":      "EUR",
			"language":      "EN",
			"departurePort": fromCode,
			"arrivalPort":   toCode,
			"departureDate": date,
			"departureTime": depTime,
		},
	}

	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("finnlines products: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, finnlinesGraphQL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", finnlinesAPIKey)

	resp, err := finnlinesClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnlines products: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("finnlines products: read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnlines products: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var gqlResp finnlinesProductResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("finnlines products: decode: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("finnlines products: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data.ListProductsAvailability, nil
}

// cheapestFinnlinesCabin finds the cheapest available ACCOMMODATION product.
// Returns the product and true, or zero-value and false if none available.
func cheapestFinnlinesCabin(products []finnlinesProduct) (finnlinesProduct, bool) {
	var best finnlinesProduct
	found := false
	for _, p := range products {
		if p.Type != "ACCOMMODATION" || !p.Available {
			continue
		}
		if !found || p.ChargePerUnit < best.ChargePerUnit {
			best = p
			found = true
		}
	}
	return best, found
}

// fetchFinnlinesTimetablesWithCabin retries a timetable query with a cabin included.
func fetchFinnlinesTimetablesWithCabin(ctx context.Context, fromCode, toCode, date, cabinCode string) ([]finnlinesTimetableEntry, error) {
	query := `query ListTimeTableAvailability($query:TimetableQuery!){listTimeTableAvailability(query:$query){...on Timetable{sailingCode departureDate departureTime arrivalDate arrivalTime departurePort arrivalPort isAvailable shipName crossingTime chargeTotal}}}`

	variables := map[string]any{
		"query": map[string]any{
			"currency": "EUR",
			"language": "EN",
			"tariff": []map[string]any{
				{"legCode": 1, "type": "SPECIAL"},
			},
			"sailings": []map[string]any{
				{
					"legCode":       1,
					"departurePort": fromCode,
					"arrivalPort":   toCode,
					"startDate":     date,
					"numberOfDays":  1,
				},
			},
			"passengers": []map[string]any{
				{"legCode": 1, "id": 1, "type": "ADULT"},
			},
			"accommodations": []map[string]any{
				{
					"legCode": 1,
					"type":    "ACCOMMODATION",
					"code":    cabinCode,
					"passengers": []map[string]any{
						{"id": 1, "type": "ADULT"},
					},
				},
			},
		},
	}

	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("finnlines: marshal cabin request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, finnlinesGraphQL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", finnlinesAPIKey)

	resp, err := finnlinesClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnlines: cabin request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("finnlines: read cabin response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnlines: cabin HTTP %d: %s", resp.StatusCode, respBody)
	}

	var gqlResp finnlinesGraphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("finnlines: decode cabin response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("finnlines: cabin GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data.ListTimeTableAvailability, nil
}

// formatCabinPrice formats a cent amount as "cabin from EUR X.XX".
func formatCabinPrice(cents float64) string {
	return fmt.Sprintf("cabin from €%.2f", cents/100.0)
}

// LookupFinnlinesPort resolves a city name or alias to a Finnlines port.
func LookupFinnlinesPort(city string) (finnlinesPort, bool) {
	p, ok := finnlinesPorts[strings.ToLower(strings.TrimSpace(city))]
	return p, ok
}

// HasFinnlinesPort returns true if the city has a known Finnlines port.
func HasFinnlinesPort(city string) bool {
	_, ok := LookupFinnlinesPort(city)
	return ok
}

// HasFinnlinesRoute returns true if both cities have Finnlines ports.
func HasFinnlinesRoute(from, to string) bool {
	return HasFinnlinesPort(from) && HasFinnlinesPort(to)
}

// finnlinesTimetableResponse is a single timetable entry from the GraphQL response.
type finnlinesTimetableEntry struct {
	SailingCode   string `json:"sailingCode"`
	DepartureDate string `json:"departureDate"` // "2026-05-01"
	DepartureTime string `json:"departureTime"` // "10:00"
	ArrivalDate   string `json:"arrivalDate"`
	ArrivalTime   string `json:"arrivalTime"`
	DeparturePort string `json:"departurePort"`
	ArrivalPort   string `json:"arrivalPort"`
	IsAvailable   bool   `json:"isAvailable"`
	ShipName      string `json:"shipName"`
	CrossingTime  string `json:"crossingTime"` // "7:45"
	ChargeTotal   *int   `json:"chargeTotal"`  // cents, nullable
}

// finnlinesGraphQLResponse wraps the GraphQL response envelope.
type finnlinesGraphQLResponse struct {
	Data struct {
		ListTimeTableAvailability []finnlinesTimetableEntry `json:"listTimeTableAvailability"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// fetchFinnlinesTimetables queries the Finnlines GraphQL API for timetables with prices.
func fetchFinnlinesTimetables(ctx context.Context, fromCode, toCode, date string) ([]finnlinesTimetableEntry, error) {
	query := `query ListTimeTableAvailability($query:TimetableQuery!){listTimeTableAvailability(query:$query){...on Timetable{sailingCode departureDate departureTime arrivalDate arrivalTime departurePort arrivalPort isAvailable shipName crossingTime chargeTotal}}}`

	variables := map[string]any{
		"query": map[string]any{
			"currency": "EUR",
			"language": "EN",
			"tariff": []map[string]any{
				{"legCode": 1, "type": "SPECIAL"},
			},
			"sailings": []map[string]any{
				{
					"legCode":       1,
					"departurePort": fromCode,
					"arrivalPort":   toCode,
					"startDate":     date,
					"numberOfDays":  1,
				},
			},
			"passengers": []map[string]any{
				{"legCode": 1, "id": 1, "type": "ADULT"},
			},
			"accommodations": []any{},
		},
	}

	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("finnlines: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, finnlinesGraphQL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", finnlinesAPIKey)

	resp, err := finnlinesClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("finnlines: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("finnlines: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("finnlines: HTTP %d: %s", resp.StatusCode, respBody)
	}

	var gqlResp finnlinesGraphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("finnlines: decode: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("finnlines: GraphQL error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data.ListTimeTableAvailability, nil
}

// buildFinnlinesBookingURL constructs a Finnlines booking URL.
func buildFinnlinesBookingURL(fromCode, toCode, date string) string {
	return fmt.Sprintf(
		"https://booking.finnlines.com/search?departurePort=%s&arrivalPort=%s&departureDate=%s&adults=1",
		fromCode, toCode, date,
	)
}

// parseFinnlinesCrossingMinutes parses a crossing time like "7:45" to minutes (465).
func parseFinnlinesCrossingMinutes(s string) int {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0
	}
	var h, m int
	_, _ = fmt.Sscanf(parts[0], "%d", &h)
	_, _ = fmt.Sscanf(parts[1], "%d", &m)
	return h*60 + m
}

// SearchFinnlines searches Finnlines for ferry crossings between two cities.
func SearchFinnlines(ctx context.Context, from, to, date, currency string) ([]models.GroundRoute, error) {
	fromPort, ok := LookupFinnlinesPort(from)
	if !ok {
		return nil, fmt.Errorf("finnlines: no port for %q", from)
	}
	toPort, ok := LookupFinnlinesPort(to)
	if !ok {
		return nil, fmt.Errorf("finnlines: no port for %q", to)
	}

	if _, err := models.ParseDate(date); err != nil {
		return nil, fmt.Errorf("finnlines: invalid date %q: %w", date, err)
	}

	if err := finnlinesLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("finnlines: rate limiter: %w", err)
	}

	slog.Debug("finnlines search", "from", fromPort.City, "to", toPort.City, "date", date)

	entries, err := fetchFinnlinesTimetables(ctx, fromPort.Code, toPort.Code, date)
	if err != nil {
		return nil, fmt.Errorf("finnlines: %w", err)
	}

	overnight := isFinnlinesOvernightRoute(fromPort.Code, toPort.Code)

	// For overnight routes with unavailable entries, try cabin pricing enrichment.
	type cabinInfo struct {
		cabinCode  string
		cabinPrice float64 // cents
		retried    *finnlinesTimetableEntry
	}
	cabinByIdx := map[int]cabinInfo{}

	if overnight {
		for i, e := range entries {
			if e.DepartureDate != date {
				continue
			}
			if e.IsAvailable || e.ChargeTotal != nil {
				continue
			}

			// Rate limit before cabin product query.
			if err := finnlinesLimiter.Wait(ctx); err != nil {
				slog.Warn("finnlines: cabin rate limit", "err", err)
				break
			}

			products, err := fetchFinnlinesProducts(ctx, fromPort.Code, toPort.Code, e.DepartureDate, e.DepartureTime)
			if err != nil {
				slog.Warn("finnlines: cabin products query failed", "sailing", e.SailingCode, "err", err)
				continue
			}

			cabin, ok := cheapestFinnlinesCabin(products)
			if !ok {
				slog.Debug("finnlines: no cabins available", "sailing", e.SailingCode)
				continue
			}

			info := cabinInfo{
				cabinCode:  cabin.Code,
				cabinPrice: cabin.ChargePerUnit,
			}

			// Retry timetable with cabin to get total price.
			if err := finnlinesLimiter.Wait(ctx); err != nil {
				slog.Warn("finnlines: cabin retry rate limit", "err", err)
				cabinByIdx[i] = info
				continue
			}

			retried, err := fetchFinnlinesTimetablesWithCabin(ctx, fromPort.Code, toPort.Code, date, cabin.Code)
			if err != nil {
				slog.Warn("finnlines: cabin retry failed", "sailing", e.SailingCode, "err", err)
				cabinByIdx[i] = info
				continue
			}

			// Find the matching sailing in the retried results.
			for j := range retried {
				if retried[j].SailingCode == e.SailingCode {
					info.retried = &retried[j]
					break
				}
			}
			cabinByIdx[i] = info
		}
	}

	bookingURL := buildFinnlinesBookingURL(fromPort.Code, toPort.Code, date)

	var routes []models.GroundRoute
	for i, e := range entries {
		// Skip if not on the requested date.
		if e.DepartureDate != date {
			continue
		}

		// Use retried entry if cabin retry made it available with a price.
		if info, ok := cabinByIdx[i]; ok && info.retried != nil && info.retried.IsAvailable && info.retried.ChargeTotal != nil {
			e = *info.retried
		}

		depTime := e.DepartureDate + "T" + e.DepartureTime + ":00"
		arrTime := e.ArrivalDate + "T" + e.ArrivalTime + ":00"

		duration := parseFinnlinesCrossingMinutes(e.CrossingTime)
		if duration == 0 {
			if computed := computeDurationMinutes(depTime, arrTime); computed > 0 {
				duration = computed
			}
		}

		// Price is in cents; convert to EUR.
		var price float64
		if e.ChargeTotal != nil {
			price = float64(*e.ChargeTotal) / 100.0
		}

		var amenities []string
		if !e.IsAvailable {
			if info, ok := cabinByIdx[i]; ok && info.cabinPrice > 0 {
				// Still unavailable even with cabin, but show cabin price info.
				amenities = append(amenities, formatCabinPrice(info.cabinPrice))
			}
			amenities = append(amenities, "Sold out")
		} else if info, ok := cabinByIdx[i]; ok && info.retried != nil {
			// Available with cabin — note the cabin in amenities.
			amenities = append(amenities, fmt.Sprintf("incl. %s cabin", info.cabinCode))
		}

		if overnight && !e.IsAvailable {
			amenities = append(amenities, "Overnight route")
		}

		routes = append(routes, models.GroundRoute{
			Provider: "finnlines",
			Type:     "ferry",
			Price:    price,
			Currency: "EUR",
			Duration: duration,
			Departure: models.GroundStop{
				City:    fromPort.City,
				Station: fromPort.Name + finnlinesShipSuffix(e.ShipName),
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

	slog.Debug("finnlines results", "routes", len(routes))
	return routes, nil
}

// finnlinesShipSuffix returns a ship name suffix for display.
func finnlinesShipSuffix(shipName string) string {
	if shipName == "" {
		return ""
	}
	// Capitalize nicely: "FINNCANOPUS" → "Finncanopus"
	name := strings.ToUpper(shipName[:1]) + strings.ToLower(shipName[1:])
	return " (" + name + ")"
}
