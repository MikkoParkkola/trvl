// Package weather provides free weather forecasts via Open-Meteo (no API key required).
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Forecast holds a single day's weather forecast for a city.
type Forecast struct {
	City          string  `json:"city"`
	Date          string  `json:"date"`          // YYYY-MM-DD
	TempMax       float64 `json:"temp_max"`      // Celsius
	TempMin       float64 `json:"temp_min"`      // Celsius
	Precipitation float64 `json:"precipitation"` // mm
	Description   string  `json:"description"`   // "Sunny", "Partly cloudy", "Rain"
}

// WeatherResult is the output of a weather forecast lookup.
type WeatherResult struct {
	Success   bool       `json:"success"`
	City      string     `json:"city"`
	Forecasts []Forecast `json:"forecasts"`
	Error     string     `json:"error,omitempty"`
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// geocache caches Nominatim results to avoid repeated lookups.
var geocache = struct {
	sync.RWMutex
	entries map[string]geoCoord
}{entries: make(map[string]geoCoord)}

type geoCoord struct {
	lat float64
	lon float64
}

// geocodeCity resolves a city name to lat/lon via Nominatim (same approach as ground package).
func geocodeCity(ctx context.Context, city string) (geoCoord, error) {
	key := strings.ToLower(strings.TrimSpace(city))

	geocache.RLock()
	if entry, ok := geocache.entries[key]; ok {
		geocache.RUnlock()
		return entry, nil
	}
	geocache.RUnlock()

	params := url.Values{
		"q":      {city},
		"format": {"json"},
		"limit":  {"1"},
	}
	apiURL := "https://nominatim.openstreetmap.org/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return geoCoord{}, err
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return geoCoord{}, fmt.Errorf("nominatim: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return geoCoord{}, fmt.Errorf("nominatim: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return geoCoord{}, fmt.Errorf("nominatim read: %w", err)
	}

	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return geoCoord{}, fmt.Errorf("nominatim decode: %w", err)
	}
	if len(results) == 0 {
		return geoCoord{}, fmt.Errorf("no geocoding results for %q", city)
	}

	lat, err := strconv.ParseFloat(results[0].Lat, 64)
	if err != nil {
		return geoCoord{}, fmt.Errorf("parse lat: %w", err)
	}
	lon, err := strconv.ParseFloat(results[0].Lon, 64)
	if err != nil {
		return geoCoord{}, fmt.Errorf("parse lon: %w", err)
	}

	coord := geoCoord{lat: lat, lon: lon}
	geocache.Lock()
	geocache.entries[key] = coord
	geocache.Unlock()

	return coord, nil
}

// openMeteoResponse is the raw API response from Open-Meteo.
type openMeteoResponse struct {
	Daily struct {
		Time             []string  `json:"time"`
		TemperatureMax   []float64 `json:"temperature_2m_max"`
		TemperatureMin   []float64 `json:"temperature_2m_min"`
		PrecipitationSum []float64 `json:"precipitation_sum"`
		WeatherCode      []int     `json:"weathercode"`
	} `json:"daily"`
}

// GetForecast fetches weather forecasts for a city between fromDate and toDate (inclusive).
// Uses Open-Meteo (free, no API key required).
// Dates must be in YYYY-MM-DD format.
func GetForecast(ctx context.Context, city string, fromDate, toDate string) (*WeatherResult, error) {
	coord, err := geocodeCity(ctx, city)
	if err != nil {
		return &WeatherResult{
			Success: false,
			City:    city,
			Error:   fmt.Sprintf("geocode: %v", err),
		}, nil
	}

	params := url.Values{
		"latitude":   {strconv.FormatFloat(coord.lat, 'f', 6, 64)},
		"longitude":  {strconv.FormatFloat(coord.lon, 'f', 6, 64)},
		"daily":      {"temperature_2m_max,temperature_2m_min,precipitation_sum,weathercode"},
		"timezone":   {"auto"},
		"start_date": {fromDate},
		"end_date":   {toDate},
	}
	apiURL := "https://api.open-meteo.com/v1/forecast?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return &WeatherResult{
			Success: false,
			City:    city,
			Error:   fmt.Sprintf("open-meteo: %v", err),
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &WeatherResult{
			Success: false,
			City:    city,
			Error:   fmt.Sprintf("open-meteo: HTTP %d", resp.StatusCode),
		}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var raw openMeteoResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	forecasts := parseForecasts(city, raw)

	return &WeatherResult{
		Success:   true,
		City:      city,
		Forecasts: forecasts,
	}, nil
}

// parseForecasts converts the raw Open-Meteo response into Forecast slices.
func parseForecasts(city string, raw openMeteoResponse) []Forecast {
	n := len(raw.Daily.Time)
	if n == 0 {
		return nil
	}

	forecasts := make([]Forecast, 0, n)
	for i := 0; i < n; i++ {
		date := safeStringAt(raw.Daily.Time, i)
		if date == "" {
			continue
		}

		tmax := safeFloat64At(raw.Daily.TemperatureMax, i)
		tmin := safeFloat64At(raw.Daily.TemperatureMin, i)
		precip := safeFloat64At(raw.Daily.PrecipitationSum, i)
		wcode := safeIntAt(raw.Daily.WeatherCode, i)

		forecasts = append(forecasts, Forecast{
			City:          city,
			Date:          date,
			TempMax:       tmax,
			TempMin:       tmin,
			Precipitation: precip,
			Description:   describeWeatherCode(wcode, precip),
		})
	}
	return forecasts
}

// describeWeatherCode converts WMO weather codes to human-readable descriptions.
// https://open-meteo.com/en/docs — WMO Weather interpretation codes (WW)
func describeWeatherCode(code int, precipitation float64) string {
	switch {
	case code == 0:
		return "Sunny"
	case code == 1:
		return "Mostly sunny"
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
		if precipitation >= 5 {
			return "Heavy rain"
		}
		return "Rain"
	case code >= 66 && code <= 67:
		return "Freezing rain"
	case code >= 71 && code <= 75:
		return "Snow"
	case code == 77:
		return "Snow grains"
	case code >= 80 && code <= 82:
		return "Rain showers"
	case code >= 85 && code <= 86:
		return "Snow showers"
	case code == 95:
		return "Thunderstorm"
	case code >= 96 && code <= 99:
		return "Thunderstorm with hail"
	default:
		if precipitation >= 5 {
			return "Rain"
		}
		if precipitation > 0 {
			return "Light rain"
		}
		return "Partly cloudy"
	}
}

// WeatherEmoji returns a simple emoji for a description.
func WeatherEmoji(description string) string {
	desc := strings.ToLower(description)
	switch {
	case strings.Contains(desc, "sunny") || strings.Contains(desc, "mostly sunny"):
		return "☀️"
	case strings.Contains(desc, "partly"):
		return "⛅"
	case strings.Contains(desc, "overcast"):
		return "☁️"
	case strings.Contains(desc, "fog"):
		return "🌫️"
	case strings.Contains(desc, "thunder"):
		return "⛈️"
	case strings.Contains(desc, "snow"):
		return "❄️"
	case strings.Contains(desc, "rain") || strings.Contains(desc, "drizzle") || strings.Contains(desc, "shower"):
		return "🌧️"
	default:
		return "🌤️"
	}
}

// FormatDateShort formats a YYYY-MM-DD date as "Apr 12".
func FormatDateShort(date string) string {
	t, err := models.ParseDate(date)
	if err != nil {
		return date
	}
	return t.Format("Jan 02")
}

// DayOfWeek returns the 3-letter day name for a YYYY-MM-DD date.
func DayOfWeek(date string) string {
	t, err := models.ParseDate(date)
	if err != nil {
		return ""
	}
	return t.Format("Mon")
}

func safeStringAt(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

func safeFloat64At(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func safeIntAt(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return 0
}
