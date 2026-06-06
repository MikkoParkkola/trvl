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

// SunTimes holds sunrise/sunset and twilight times for a city on a given day.
// All times are UTC ISO-8601 (the API is queried with formatted=0).
//
// Data source: sunrise-sunset.org (free, no API key). Attribution required —
// callers MUST surface the "sunrise-sunset.org" credit in user-facing output.
type SunTimes struct {
	City               string `json:"city"`
	Sunrise            string `json:"sunrise"`              // UTC ISO8601
	Sunset             string `json:"sunset"`               // UTC ISO8601
	SolarNoon          string `json:"solar_noon"`           // UTC ISO8601
	DayLength          int    `json:"day_length"`           // seconds
	CivilTwilightBegin string `json:"civil_twilight_begin"` // UTC ISO8601 (dawn)
	CivilTwilightEnd   string `json:"civil_twilight_end"`   // UTC ISO8601 (dusk)
}

// SunTimesResult is the output of a sun-times lookup.
type SunTimesResult struct {
	Success bool   `json:"success"`
	City    string `json:"city"`
	SunTimes
	Error string `json:"error,omitempty"`
}

// SunAttribution is the required credit line for sunrise-sunset.org output.
const SunAttribution = "Sun times: sunrise-sunset.org"

type sunResponse struct {
	Results struct {
		Sunrise            string `json:"sunrise"`
		Sunset             string `json:"sunset"`
		SolarNoon          string `json:"solar_noon"`
		DayLength          int    `json:"day_length"`
		CivilTwilightBegin string `json:"civil_twilight_begin"`
		CivilTwilightEnd   string `json:"civil_twilight_end"`
	} `json:"results"`
	Status string `json:"status"`
}

// GetSunTimes fetches sunrise/sunset for a city on the given date (YYYY-MM-DD;
// empty = today) via sunrise-sunset.org (free, no API key). Attribution required.
func GetSunTimes(ctx context.Context, city, date string) (*SunTimesResult, error) {
	coord, err := geocodeCity(ctx, city)
	if err != nil {
		return &SunTimesResult{Success: false, City: city, Error: fmt.Sprintf("geocode: %v", err)}, nil
	}

	params := url.Values{
		"lat":       {strconv.FormatFloat(coord.lat, 'f', 6, 64)},
		"lng":       {strconv.FormatFloat(coord.lon, 'f', 6, 64)},
		"formatted": {"0"},
	}
	if date != "" {
		params.Set("date", date)
	}
	apiURL := "https://api.sunrise-sunset.org/json?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return &SunTimesResult{Success: false, City: city, Error: fmt.Sprintf("sunrise-sunset: %v", err)}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &SunTimesResult{Success: false, City: city, Error: fmt.Sprintf("sunrise-sunset: HTTP %d", resp.StatusCode)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return parseSunTimes(city, body)
}

// parseSunTimes decodes the sunrise-sunset.org response body.
func parseSunTimes(city string, body []byte) (*SunTimesResult, error) {
	var raw sunResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode sun-times response: %w", err)
	}
	if raw.Status != "OK" {
		return &SunTimesResult{Success: false, City: city, Error: fmt.Sprintf("sunrise-sunset: status %q", raw.Status)}, nil
	}
	return &SunTimesResult{
		Success: true,
		City:    city,
		SunTimes: SunTimes{
			City:               city,
			Sunrise:            raw.Results.Sunrise,
			Sunset:             raw.Results.Sunset,
			SolarNoon:          raw.Results.SolarNoon,
			DayLength:          raw.Results.DayLength,
			CivilTwilightBegin: raw.Results.CivilTwilightBegin,
			CivilTwilightEnd:   raw.Results.CivilTwilightEnd,
		},
	}, nil
}
