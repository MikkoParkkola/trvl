package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// AirQuality holds current air-quality readings for a city.
type AirQuality struct {
	City     string  `json:"city"`
	Time     string  `json:"time"`             // ISO8601 local time of reading
	AQI      float64 `json:"aqi"`              // European AQI (0 = best)
	Category string  `json:"category"`         // human band derived from AQI
	PM25     float64 `json:"pm2_5"`            // µg/m³
	PM10     float64 `json:"pm10"`             // µg/m³
	Ozone    float64 `json:"ozone"`            // µg/m³
	NO2      float64 `json:"nitrogen_dioxide"` // µg/m³
}

// AirQualityResult is the output of an air-quality lookup.
type AirQualityResult struct {
	Success bool   `json:"success"`
	City    string `json:"city"`
	AirQuality
	Error string `json:"error,omitempty"`
}

// airQualityResponse is the raw Open-Meteo Air Quality API response.
type airQualityResponse struct {
	Current struct {
		Time            string  `json:"time"`
		EuropeanAQI     float64 `json:"european_aqi"`
		PM25            float64 `json:"pm2_5"`
		PM10            float64 `json:"pm10"`
		Ozone           float64 `json:"ozone"`
		NitrogenDioxide float64 `json:"nitrogen_dioxide"`
	} `json:"current"`
}

// GetAirQuality fetches current air quality for a city via the Open-Meteo
// Air Quality API (free, no API key for non-commercial use).
func GetAirQuality(ctx context.Context, city string) (*AirQualityResult, error) {
	coord, err := geocodeCity(ctx, city)
	if err != nil {
		return &AirQualityResult{Success: false, City: city, Error: fmt.Sprintf("geocode: %v", err)}, nil
	}

	params := url.Values{
		"latitude":  {strconv.FormatFloat(coord.lat, 'f', 6, 64)},
		"longitude": {strconv.FormatFloat(coord.lon, 'f', 6, 64)},
		"current":   {"european_aqi,pm2_5,pm10,ozone,nitrogen_dioxide"},
		"timezone":  {"auto"},
	}
	apiURL := "https://air-quality-api.open-meteo.com/v1/air-quality?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return &AirQualityResult{Success: false, City: city, Error: fmt.Sprintf("open-meteo air-quality: %v", err)}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &AirQualityResult{Success: false, City: city, Error: fmt.Sprintf("open-meteo air-quality: HTTP %d", resp.StatusCode)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return parseAirQuality(city, body)
}

// parseAirQuality decodes the Open-Meteo air-quality response body.
func parseAirQuality(city string, body []byte) (*AirQualityResult, error) {
	var raw airQualityResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode air-quality response: %w", err)
	}
	aqi := raw.Current.EuropeanAQI
	return &AirQualityResult{
		Success: true,
		City:    city,
		AirQuality: AirQuality{
			City:     city,
			Time:     raw.Current.Time,
			AQI:      aqi,
			Category: AQICategory(aqi),
			PM25:     raw.Current.PM25,
			PM10:     raw.Current.PM10,
			Ozone:    raw.Current.Ozone,
			NO2:      raw.Current.NitrogenDioxide,
		},
	}, nil
}

// AQICategory maps a European AQI value to its human band.
// Bands per the European AQI scale (lower is better).
func AQICategory(aqi float64) string {
	switch {
	case aqi <= 20:
		return "Good"
	case aqi <= 40:
		return "Fair"
	case aqi <= 60:
		return "Moderate"
	case aqi <= 80:
		return "Poor"
	case aqi <= 100:
		return "Very poor"
	default:
		return "Extremely poor"
	}
}

// AQIEmoji returns a simple indicator for an AQI band.
func AQIEmoji(aqi float64) string {
	switch {
	case aqi <= 40:
		return "🟢"
	case aqi <= 60:
		return "🟡"
	case aqi <= 80:
		return "🟠"
	default:
		return "🔴"
	}
}
