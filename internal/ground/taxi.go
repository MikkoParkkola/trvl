package ground

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TaxiEstimateInput describes a door-to-door taxi estimate between two points.
type TaxiEstimateInput struct {
	FromName    string
	ToName      string
	FromLat     float64
	FromLon     float64
	ToLat       float64
	ToLon       float64
	CountryCode string
	Currency    string
}

var taxiFareCountryMultipliers = map[string]float64{
	"NO": 1.60,
	"CH": 1.55,
	"IS": 1.50,
	"DK": 1.45,
	"SE": 1.35,
	"GB": 1.35,
	"IE": 1.25,
	"FI": 1.15,
	"NL": 1.15,
	"BE": 1.10,
	"DE": 1.00,
	"FR": 1.00,
	"AT": 0.95,
	"IT": 0.95,
	"ES": 0.90,
	"PT": 0.85,
	"EE": 0.70,
	"CZ": 0.65,
	"PL": 0.65,
	"HR": 0.60,
	"SK": 0.60,
	"HU": 0.60,
	"LV": 0.55,
	"LT": 0.55,
	"RO": 0.50,
	"BG": 0.45,
}

// EstimateTaxiTransfer builds a taxi fare estimate using straight-line distance,
// a road-distance multiplier, and coarse country-level tariff adjustments.
func EstimateTaxiTransfer(ctx context.Context, input TaxiEstimateInput) (models.GroundRoute, error) {
	fromName := strings.TrimSpace(input.FromName)
	toName := strings.TrimSpace(input.ToName)
	if fromName == "" || toName == "" {
		return models.GroundRoute{}, fmt.Errorf("from_name and to_name are required")
	}

	crowFlightKm := hotels.Haversine(input.FromLat, input.FromLon, input.ToLat, input.ToLon)
	if crowFlightKm <= 0 {
		return models.GroundRoute{}, fmt.Errorf("taxi estimate requires distinct coordinates")
	}

	roadDistanceKm := estimateTaxiRoadDistanceKm(crowFlightKm)
	durationMinutes := estimateTaxiDurationMinutes(roadDistanceKm)
	lowEUR, highEUR := estimateTaxiFareEUR(roadDistanceKm, durationMinutes, input.CountryCode)
	low, high, currency := convertTaxiFare(ctx, lowEUR, highEUR, input.Currency, destinations.ConvertCurrency)

	return models.GroundRoute{
		Provider:  "taxi",
		Type:      "taxi",
		Price:     low,
		PriceMax:  high,
		Currency:  currency,
		Duration:  durationMinutes,
		Departure: models.GroundStop{City: fromName},
		Arrival:   models.GroundStop{City: toName},
		Transfers: 0,
		Amenities: []string{"door-to-door", "estimated fare"},
		BookingURL: buildTaxiDirectionsURL(
			input.FromLat,
			input.FromLon,
			input.ToLat,
			input.ToLon,
		),
	}, nil
}

func estimateTaxiRoadDistanceKm(crowFlightKm float64) float64 {
	switch {
	case crowFlightKm < 1:
		return 2
	case crowFlightKm < 8:
		return crowFlightKm * 1.45
	case crowFlightKm < 25:
		return crowFlightKm * 1.30
	default:
		return crowFlightKm * 1.18
	}
}

func estimateTaxiDurationMinutes(roadDistanceKm float64) int {
	baseMinutes := 6.0
	speedKmh := 28.0
	switch {
	case roadDistanceKm >= 60:
		baseMinutes = 10
		speedKmh = 60
	case roadDistanceKm >= 25:
		baseMinutes = 8
		speedKmh = 45
	case roadDistanceKm >= 10:
		baseMinutes = 7
		speedKmh = 35
	}
	return int(math.Round(baseMinutes + roadDistanceKm/speedKmh*60))
}

func estimateTaxiFareEUR(roadDistanceKm float64, durationMinutes int, countryCode string) (float64, float64) {
	multiplier := taxiFareMultiplier(countryCode)
	low := (4.5 + roadDistanceKm*1.45 + float64(durationMinutes)*0.28) * multiplier
	high := (7.5 + roadDistanceKm*2.40 + float64(durationMinutes)*0.45) * multiplier
	if high < low {
		high = low
	}
	return low, high
}

func taxiFareMultiplier(countryCode string) float64 {
	if multiplier, ok := taxiFareCountryMultipliers[strings.ToUpper(strings.TrimSpace(countryCode))]; ok {
		return multiplier
	}
	return 1.0
}

// convertTaxiFare takes the converter as a parameter (production passes
// destinations.ConvertCurrency) so tests convert without the live rates API.
func convertTaxiFare(ctx context.Context, lowEUR, highEUR float64, targetCurrency string, convert func(ctx context.Context, amount float64, from, to string) (float64, string)) (float64, float64, string) {
	currency := strings.ToUpper(strings.TrimSpace(targetCurrency))
	if currency == "" || currency == "EUR" {
		return roundTaxiMoney(lowEUR), roundTaxiMoney(highEUR), "EUR"
	}

	low, lowCurrency := convert(ctx, lowEUR, "EUR", currency)
	high, highCurrency := convert(ctx, highEUR, "EUR", currency)
	if lowCurrency != currency || highCurrency != currency {
		return roundTaxiMoney(lowEUR), roundTaxiMoney(highEUR), "EUR"
	}
	return roundTaxiMoney(low), roundTaxiMoney(high), currency
}

func roundTaxiMoney(amount float64) float64 {
	return math.Round(amount*100) / 100
}

func buildTaxiDirectionsURL(fromLat, fromLon, toLat, toLon float64) string {
	query := url.Values{}
	query.Set("api", "1")
	query.Set("origin", fmt.Sprintf("%.6f,%.6f", fromLat, fromLon))
	query.Set("destination", fmt.Sprintf("%.6f,%.6f", toLat, toLon))
	query.Set("travelmode", "driving")
	return "https://www.google.com/maps/dir/?" + query.Encode()
}
