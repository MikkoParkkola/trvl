package destinations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const openMeteoURL = "https://api.open-meteo.com/v1/forecast"

// weatherCache stores forecast results keyed by "lat,lon".
var weatherCache = struct {
	sync.RWMutex
	entries map[string]weatherCacheEntry
}{entries: make(map[string]weatherCacheEntry)}

type weatherCacheEntry struct {
	info     models.WeatherInfo
	timezone string
	fetched  time.Time
}

const weatherCacheTTL = 1 * time.Hour

// openMeteoResponse is the JSON shape returned by Open-Meteo.
type openMeteoResponse struct {
	Timezone string `json:"timezone"`
	Daily    struct {
		Time             []string  `json:"time"`
		TemperatureMax   []float64 `json:"temperature_2m_max"`
		TemperatureMin   []float64 `json:"temperature_2m_min"`
		PrecipitationSum []float64 `json:"precipitation_sum"`
		WeatherCode      []int     `json:"weather_code"`
	} `json:"daily"`
}

// FetchWeather retrieves a 7-day forecast from Open-Meteo.
// Returns the timezone string as a second value.
func FetchWeather(ctx context.Context, lat, lon float64) (models.WeatherInfo, string, error) {
	key := fmt.Sprintf("%.4f,%.4f", lat, lon)

	weatherCache.RLock()
	if entry, ok := weatherCache.entries[key]; ok && time.Since(entry.fetched) < weatherCacheTTL {
		weatherCache.RUnlock()
		return entry.info, entry.timezone, nil
	}
	weatherCache.RUnlock()

	u, _ := url.Parse(openMeteoAPIURL)
	q := u.Query()
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lon))
	q.Set("daily", "temperature_2m_max,temperature_2m_min,precipitation_sum,weather_code")
	q.Set("timezone", "auto")
	q.Set("forecast_days", "7")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return models.WeatherInfo{}, "", fmt.Errorf("create weather request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (destination weather)")

	resp, err := destinationsClient.Do(req)
	if err != nil {
		return models.WeatherInfo{}, "", fmt.Errorf("weather request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return models.WeatherInfo{}, "", fmt.Errorf("read weather response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return models.WeatherInfo{}, "", fmt.Errorf("open-meteo returned status %d: %s", resp.StatusCode, string(body))
	}

	var omResp openMeteoResponse
	if err := json.Unmarshal(body, &omResp); err != nil {
		return models.WeatherInfo{}, "", fmt.Errorf("parse weather response: %w", err)
	}

	info := models.WeatherInfo{}
	for i, date := range omResp.Daily.Time {
		day := models.WeatherDay{
			Date:        date,
			Description: weatherCodeDescription(omResp.Daily.WeatherCode[i]),
		}
		if i < len(omResp.Daily.TemperatureMax) {
			day.TempHigh = omResp.Daily.TemperatureMax[i]
		}
		if i < len(omResp.Daily.TemperatureMin) {
			day.TempLow = omResp.Daily.TemperatureMin[i]
		}
		if i < len(omResp.Daily.PrecipitationSum) {
			day.Precipitation = omResp.Daily.PrecipitationSum[i]
		}
		info.Forecast = append(info.Forecast, day)
	}

	// First day is "current".
	if len(info.Forecast) > 0 {
		info.Current = info.Forecast[0]
	}

	weatherCache.Lock()
	weatherCache.entries[key] = weatherCacheEntry{info: info, timezone: omResp.Timezone, fetched: time.Now()}
	weatherCache.Unlock()

	return info, omResp.Timezone, nil
}

// weatherCodeDescription maps WMO weather codes to human-readable descriptions.
func weatherCodeDescription(code int) string {
	switch {
	case code == 0:
		return "Clear sky"
	case code == 1:
		return "Mainly clear"
	case code == 2:
		return "Partly cloudy"
	case code == 3:
		return "Overcast"
	case code >= 45 && code <= 48:
		return "Fog"
	case code >= 51 && code <= 55:
		return "Drizzle"
	case code >= 56 && code <= 57:
		return "Freezing drizzle"
	case code >= 61 && code <= 65:
		return "Rain"
	case code >= 66 && code <= 67:
		return "Freezing rain"
	case code >= 71 && code <= 77:
		return "Snow"
	case code >= 80 && code <= 82:
		return "Rain showers"
	case code >= 85 && code <= 86:
		return "Snow showers"
	case code >= 95 && code <= 99:
		return "Thunderstorm"
	default:
		return "Unknown"
	}
}
