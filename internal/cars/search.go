package cars

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

const (
	ProviderSkyscanner = "skyscanner"

	skyscannerAPIKeyEnv      = "SKYSCANNER_API_KEY" // #nosec G101 -- environment variable name, not a credential
	defaultSkyscannerBaseURL = "https://partners.api.skyscanner.net/apiservices/v1/carhire"
)

var (
	skyscannerBaseURL    = defaultSkyscannerBaseURL
	skyscannerHTTPClient = &http.Client{Timeout: 25 * time.Second}
)

// SearchOptions configures a rental car search.
type SearchOptions struct {
	PickupLocation  string
	DropoffLocation string
	PickupDate      string
	DropoffDate     string
	PickupTime      string
	DropoffTime     string
	Currency        string
	Locale          string
	Market          string
	Passengers      int
	DriverAge       int
	MaxPrice        float64
	VehicleClass    string
	Providers       []string
}

// Search looks up rental car offers from configured car-rental providers.
func Search(ctx context.Context, opts SearchOptions) (*models.CarSearchResult, error) {
	if err := validateSearchOptions(&opts); err != nil {
		return nil, err
	}

	var (
		offers   []models.CarOffer
		statuses []models.ProviderStatus
	)
	for _, providerID := range normalizedProviders(opts.Providers) {
		switch providerID {
		case ProviderSkyscanner:
			providerOffers, status := searchSkyscanner(ctx, opts)
			statuses = append(statuses, status)
			offers = append(offers, providerOffers...)
		default:
			statuses = append(statuses, models.ProviderStatus{
				ID:          providerID,
				Name:        providerID,
				Status:      "skipped",
				Error:       "unknown car rental provider",
				FixHint:     "Use provider skyscanner or leave provider empty for the default car rental provider set.",
				FixHintCode: "UNSUPPORTED_PROVIDER",
			})
		}
	}

	offers = filterAndSortOffers(offers, opts)
	result := &models.CarSearchResult{
		Success:          len(offers) > 0,
		Count:            len(offers),
		Offers:           offers,
		ProviderStatuses: statuses,
	}
	if !result.Success {
		result.Error = carSearchError(statuses)
	}
	return result, nil
}

func validateSearchOptions(opts *SearchOptions) error {
	opts.PickupLocation = strings.TrimSpace(opts.PickupLocation)
	opts.DropoffLocation = strings.TrimSpace(opts.DropoffLocation)
	opts.PickupDate = strings.TrimSpace(opts.PickupDate)
	opts.DropoffDate = strings.TrimSpace(opts.DropoffDate)
	if opts.PickupLocation == "" || opts.PickupDate == "" || opts.DropoffDate == "" {
		return fmt.Errorf("pickup_location, pickup_date, and dropoff_date are required")
	}
	if err := models.ValidateDateRange(opts.PickupDate, opts.DropoffDate); err != nil {
		return err
	}
	if opts.DropoffLocation == "" {
		opts.DropoffLocation = opts.PickupLocation
	}
	if opts.PickupTime == "" {
		opts.PickupTime = "10:00"
	}
	if opts.DropoffTime == "" {
		opts.DropoffTime = "10:00"
	}
	if opts.Currency == "" {
		opts.Currency = "EUR"
	}
	opts.Currency = strings.ToUpper(opts.Currency)
	if opts.Locale == "" {
		opts.Locale = "en-US"
	}
	if opts.Market == "" {
		opts.Market = "US"
	}
	if opts.Passengers <= 0 {
		opts.Passengers = 1
	}
	if opts.DriverAge <= 0 {
		opts.DriverAge = 30
	}
	return nil
}

func normalizedProviders(input []string) []string {
	if len(input) == 0 {
		return []string{ProviderSkyscanner}
	}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		for _, part := range strings.Split(raw, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	if len(out) == 0 {
		return []string{ProviderSkyscanner}
	}
	return out
}

func searchSkyscanner(ctx context.Context, opts SearchOptions) ([]models.CarOffer, models.ProviderStatus) {
	start := time.Now()
	key := strings.TrimSpace(os.Getenv(skyscannerAPIKeyEnv))
	if key == "" {
		status := models.ProviderStatus{
			ID:          ProviderSkyscanner,
			Name:        "Skyscanner Car Hire",
			Status:      "skipped",
			Error:       skyscannerAPIKeyEnv + " is not configured",
			FixHint:     "Skyscanner car-hire access is partner-gated. Set SKYSCANNER_API_KEY after enabling Car Hire Live Prices, or use the provider status as a setup-required fallback.",
			FixHintCode: "MISSING_CREDENTIAL",
		}
		logCarProviderHealth(status, start, 0)
		return nil, status
	}

	session, err := createSkyscannerSession(ctx, key, opts)
	if err != nil {
		status := carProviderError(ProviderSkyscanner, "Skyscanner Car Hire", err)
		logCarProviderHealth(status, start, 0)
		return nil, status
	}
	offers, err := pollSkyscannerSession(ctx, key, session, opts)
	if err != nil {
		status := carProviderError(ProviderSkyscanner, "Skyscanner Car Hire", err)
		logCarProviderHealth(status, start, 0)
		return nil, status
	}

	status := models.ProviderStatus{
		ID:      ProviderSkyscanner,
		Name:    "Skyscanner Car Hire",
		Status:  "ok",
		Results: len(offers),
	}
	logCarProviderHealth(status, start, len(offers))
	return offers, status
}

func createSkyscannerSession(ctx context.Context, key string, opts SearchOptions) (string, error) {
	body := map[string]any{
		"market":          opts.Market,
		"locale":          opts.Locale,
		"currency":        opts.Currency,
		"pickupLocation":  opts.PickupLocation,
		"dropoffLocation": opts.DropoffLocation,
		"pickupDate":      opts.PickupDate,
		"dropoffDate":     opts.DropoffDate,
		"pickupTime":      opts.PickupTime,
		"dropoffTime":     opts.DropoffTime,
		"driverAge":       opts.DriverAge,
		"passengers":      opts.Passengers,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	u := strings.TrimRight(skyscannerBaseURL, "/") + "/search/create"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", key)

	resp, err := skyscannerHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := readProviderPayload(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create session HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("parse create response: %w", err)
	}
	for _, key := range []string{"sessionToken", "session_token", "sessionId", "session_id", "id"} {
		if session := stringValue(parsed[key]); session != "" {
			return session, nil
		}
	}
	return "", fmt.Errorf("create response did not include a session token")
}

func pollSkyscannerSession(ctx context.Context, key, session string, opts SearchOptions) ([]models.CarOffer, error) {
	u := strings.TrimRight(skyscannerBaseURL, "/") + "/search/poll/" + session
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", key)

	resp, err := skyscannerHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := readProviderPayload(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("poll session HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	return parseSkyscannerOffers(payload, opts)
}

func readProviderPayload(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func parseSkyscannerOffers(payload []byte, opts SearchOptions) ([]models.CarOffer, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("parse poll response: %w", err)
	}
	var offers []models.CarOffer
	for _, candidate := range findCarOfferMaps(root) {
		offer, ok := normalizeCarOffer(candidate, opts)
		if ok {
			offers = append(offers, offer)
		}
	}
	return offers, nil
}

func findCarOfferMaps(root any) []map[string]any {
	var out []map[string]any
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			if looksLikeCarOffer(x) {
				out = append(out, x)
				return
			}
			for key, child := range x {
				switch key {
				case "offers", "cars", "vehicles", "results", "items", "data":
					walk(child)
				}
			}
		}
	}
	walk(root)
	return out
}

func looksLikeCarOffer(m map[string]any) bool {
	hasPrice := firstFloat(m, "price.amount", "price.total", "totalPrice.amount", "total_price.amount", "total", "amount") > 0
	hasVehicle := firstString(m, "vehicleClass", "vehicle_class", "class", "category", "sipp", "vehicleName", "vehicle_name", "vehicle.name", "name", "model") != ""
	return hasPrice && hasVehicle
}

func normalizeCarOffer(m map[string]any, opts SearchOptions) (models.CarOffer, bool) {
	price := firstFloat(m, "price.amount", "price.total", "totalPrice.amount", "total_price.amount", "total", "amount")
	if price <= 0 {
		return models.CarOffer{}, false
	}
	currency := strings.ToUpper(firstString(m, "price.currency", "totalPrice.currency", "total_price.currency", "currency"))
	if currency == "" {
		currency = opts.Currency
	}

	offer := models.CarOffer{
		Provider:     firstString(m, "provider", "providerName", "agent", "broker"),
		Supplier:     firstString(m, "supplier", "supplierName", "vendor", "rentalCompany", "supplier.name", "vendor.name"),
		VehicleClass: firstString(m, "vehicleClass", "vehicle_class", "class", "category", "sipp", "vehicle.type"),
		VehicleName:  firstString(m, "vehicleName", "vehicle_name", "vehicle.name", "name", "model"),
		Transmission: firstString(m, "transmission", "vehicle.transmission"),
		FuelPolicy:   firstString(m, "fuelPolicy", "fuel_policy", "fuel"),
		Seats:        firstInt(m, "seats", "vehicle.seats", "passengerCapacity"),
		Bags:         firstInt(m, "bags", "luggage", "vehicle.bags", "vehicle.luggage"),
		Doors:        firstInt(m, "doors", "vehicle.doors"),
		Passengers:   opts.Passengers,
		Pickup:       endpointFrom(opts.PickupLocation, opts.PickupDate, opts.PickupTime, m, "pickup"),
		Dropoff:      endpointFrom(opts.DropoffLocation, opts.DropoffDate, opts.DropoffTime, m, "dropoff"),
		Price:        price,
		Currency:     currency,
		TaxesAndFees: firstFloat(m, "taxesAndFees", "taxes_and_fees", "fees", "price.taxesAndFees", "price.taxes_and_fees"),
		BookingURL:   firstString(m, "bookingUrl", "booking_url", "deepLink", "deeplink", "url"),
		Freshness:    time.Now().UTC().Format(time.RFC3339),
	}
	if offer.Provider == "" {
		offer.Provider = "Skyscanner"
	}
	if offer.VehicleName == "" {
		offer.VehicleName = offer.VehicleClass
	}
	if v, ok := firstBool(m, "freeCancellation", "free_cancel", "freeCancel", "cancellation.free"); ok {
		offer.FreeCancellation = &v
	}
	if v, ok := firstBool(m, "unlimitedMileage", "unlimited_mileage", "mileage.unlimited"); ok {
		offer.UnlimitedMileage = &v
	}
	return offer, true
}

func endpointFrom(location, date, clock string, m map[string]any, prefix string) models.CarEndpoint {
	endpoint := models.CarEndpoint{
		Location: location,
		Time:     strings.TrimSpace(date + "T" + clock),
	}
	if s := firstString(m, prefix+".location", prefix+"Location", prefix+"_location", prefix+".name"); s != "" {
		endpoint.Location = s
	}
	endpoint.Code = firstString(m, prefix+".code", prefix+"Code", prefix+"_code")
	endpoint.Address = firstString(m, prefix+".address", prefix+"Address", prefix+"_address")
	endpoint.Lat = firstFloat(m, prefix+".lat", prefix+".latitude", prefix+"Lat")
	endpoint.Lon = firstFloat(m, prefix+".lon", prefix+".lng", prefix+".longitude", prefix+"Lon")
	if t := firstString(m, prefix+".time", prefix+"Time", prefix+"_time"); t != "" {
		endpoint.Time = t
	}
	return endpoint
}

func filterAndSortOffers(offers []models.CarOffer, opts SearchOptions) []models.CarOffer {
	filtered := offers[:0]
	wantClass := strings.ToLower(strings.TrimSpace(opts.VehicleClass))
	for _, offer := range offers {
		if opts.MaxPrice > 0 && offer.Price > opts.MaxPrice {
			continue
		}
		if wantClass != "" && !strings.Contains(strings.ToLower(offer.VehicleClass+" "+offer.VehicleName), wantClass) {
			continue
		}
		filtered = append(filtered, offer)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Price < filtered[j].Price
	})
	return filtered
}

func carSearchError(statuses []models.ProviderStatus) string {
	if len(statuses) == 0 {
		return "no car rental providers were selected"
	}
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Error != "" {
			parts = append(parts, status.Name+": "+status.Error)
		}
	}
	if len(parts) == 0 {
		return "no car rental offers found"
	}
	return strings.Join(parts, "; ")
}

func carProviderError(id, name string, err error) models.ProviderStatus {
	return models.ProviderStatus{
		ID:          id,
		Name:        name,
		Status:      "error",
		Error:       err.Error(),
		FixHint:     "Check provider credentials, access tier, and response shape. Rental car APIs are often partner-gated.",
		FixHintCode: "RESPONSE_SHAPE_CHANGED",
	}
}

func logCarProviderHealth(status models.ProviderStatus, start time.Time, results int) {
	healthStatus := "error"
	if status.Status == "ok" {
		healthStatus = "ok"
	}
	providers.LogHealth(providers.HealthEntry{
		Provider:  "cars/" + status.ID,
		Operation: "search",
		Status:    healthStatus,
		LatencyMs: time.Since(start).Milliseconds(),
		Results:   results,
		Error:     status.Error,
		HintCode:  status.FixHintCode,
	})
}

func firstString(m map[string]any, paths ...string) string {
	for _, path := range paths {
		if s := stringValue(lookupPath(m, path)); s != "" {
			return s
		}
	}
	return ""
}

func firstFloat(m map[string]any, paths ...string) float64 {
	for _, path := range paths {
		if f, ok := floatValue(lookupPath(m, path)); ok {
			return f
		}
	}
	return 0
}

func firstInt(m map[string]any, paths ...string) int {
	for _, path := range paths {
		if f, ok := floatValue(lookupPath(m, path)); ok {
			return int(f)
		}
	}
	return 0
}

func firstBool(m map[string]any, paths ...string) (bool, bool) {
	for _, path := range paths {
		if b, ok := boolValue(lookupPath(m, path)); ok {
			return b, true
		}
	}
	return false, false
}

func lookupPath(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = lookupKey(obj, part)
		if cur == nil {
			return nil
		}
	}
	return cur
}

func lookupKey(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	normalized := normalizeKey(key)
	for k, v := range m {
		if normalizeKey(k) == normalized {
			return v
		}
	}
	return nil
}

func normalizeKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func floatValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		return parsePriceString(x)
	default:
		return 0, false
	}
}

func parsePriceString(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			b.WriteRune(r)
		}
	}
	cleaned := strings.ReplaceAll(b.String(), ",", "")
	if cleaned == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(cleaned, 64)
	return f, err == nil
}

func boolValue(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "yes", "1", "included":
			return true, true
		case "false", "no", "0":
			return false, true
		}
	}
	return false, false
}

func setSkyscannerBaseURLForTest(base string) func() {
	old := skyscannerBaseURL
	skyscannerBaseURL = base
	return func() { skyscannerBaseURL = old }
}
