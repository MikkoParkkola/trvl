package weather

// Sea-state enrichment via the keyless Open-Meteo Marine API.
//
// https://marine-api.open-meteo.com/v1/marine — no API key required. Used to
// add a coarse calm/moderate/rough label to ferry legs. This is a "nice to
// have" enrichment: on any error or timeout it degrades silently (zero value +
// nil-ish), so callers never block the core ground result.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// marineAPIURL is the Open-Meteo Marine endpoint. It is a var (not a const) so
// tests can point GetSeaState at a local httptest server.
var marineAPIURL = "https://marine-api.open-meteo.com/v1/marine"

// marineClient has a short 5s timeout — sea state is a best-effort enrichment
// and must never slow down the core ground result.
var marineClient = &http.Client{Timeout: 5 * time.Second}

// marineResponse is the raw daily payload from the Open-Meteo Marine API.
type marineResponse struct {
	Daily struct {
		Time           []string  `json:"time"`
		WaveHeightMax  []float64 `json:"wave_height_max"`
		SwellHeightMax []float64 `json:"swell_wave_height_max"`
	} `json:"daily"`
}

// GetSeaState fetches the maximum daily wave (and swell) height at a coordinate
// from the keyless Open-Meteo Marine API and returns a coarse sea-state label.
//
// It is silent-failure by contract: on any error, timeout, non-200 response, or
// empty payload it returns a zero-value models.SeaState and a non-nil error so
// callers can simply ignore the result and leave the ferry leg unchanged. A nil
// error is only returned when a real wave-height reading was parsed.
func GetSeaState(ctx context.Context, lat, lon float64) (models.SeaState, error) {
	params := url.Values{
		"latitude":      {strconv.FormatFloat(lat, 'f', 4, 64)},
		"longitude":     {strconv.FormatFloat(lon, 'f', 4, 64)},
		"daily":         {"wave_height_max,swell_wave_height_max"},
		"timezone":      {"auto"},
		"forecast_days": {"1"},
	}
	apiURL := marineAPIURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return models.SeaState{}, fmt.Errorf("marine: build request: %w", err)
	}
	req.Header.Set("User-Agent", "trvl/1.0 (travel agent; github.com/MikkoParkkola/trvl)")

	resp, err := marineClient.Do(req)
	if err != nil {
		return models.SeaState{}, fmt.Errorf("marine: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return models.SeaState{}, fmt.Errorf("marine: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return models.SeaState{}, fmt.Errorf("marine: read response: %w", err)
	}

	return parseSeaState(body)
}

// SeaStateForCity resolves a port/city name to coordinates (via the same
// Nominatim path used for weather) and returns its sea state. Silent-failure:
// a geocode or fetch failure yields a zero value and a non-nil error.
func SeaStateForCity(ctx context.Context, city string) (models.SeaState, error) {
	coord, err := geocodeCity(ctx, city)
	if err != nil {
		return models.SeaState{}, fmt.Errorf("marine geocode: %w", err)
	}
	return GetSeaState(ctx, coord.lat, coord.lon)
}

// parseSeaState decodes a Marine API body into a SeaState. It is pure (no
// network) so it is exercised directly by offline fixture tests.
func parseSeaState(body []byte) (models.SeaState, error) {
	var raw marineResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return models.SeaState{}, fmt.Errorf("marine: decode response: %w", err)
	}
	if len(raw.Daily.WaveHeightMax) == 0 {
		return models.SeaState{}, fmt.Errorf("marine: no wave-height data")
	}

	wave := raw.Daily.WaveHeightMax[0]
	if wave < 0 {
		return models.SeaState{}, fmt.Errorf("marine: invalid wave height %.2f", wave)
	}

	var swell float64
	if len(raw.Daily.SwellHeightMax) > 0 && raw.Daily.SwellHeightMax[0] > 0 {
		swell = raw.Daily.SwellHeightMax[0]
	}

	return models.SeaState{
		WaveHeight:  wave,
		SwellHeight: swell,
		Label:       seaStateLabel(wave),
	}, nil
}

// seaStateLabel maps a maximum wave height (metres) to a coarse, honest label.
// Thresholds follow the WMO/Douglas sea-scale boundaries: smooth-to-slight
// (< 1.25 m) reads as calm; moderate up to 2.5 m; rough beyond that.
func seaStateLabel(waveHeightMeters float64) string {
	switch {
	case waveHeightMeters < 1.25:
		return "calm"
	case waveHeightMeters < 2.5:
		return "moderate"
	default:
		return "rough"
	}
}
