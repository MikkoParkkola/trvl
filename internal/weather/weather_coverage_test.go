package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// replaceHTTPClient swaps the package-level httpClient with a custom one
// and returns a restore function.
func replaceHTTPClient(client *http.Client) func() {
	old := httpClient
	httpClient = client
	return func() { httpClient = old }
}

// clearGeoCache empties the geocache so tests don't interfere with each other.
func clearGeoCache() {
	geocache.Lock()
	geocache.entries = make(map[string]geoCoord)
	geocache.Unlock()
}

// ------------------------------------------------------------------ geocodeCity

func TestGeocodeCity_Success(t *testing.T) {
	clearGeoCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]struct {
			Lat string `json:"lat"`
			Lon string `json:"lon"`
		}{
			{Lat: "50.0755", Lon: "14.4378"},
		})
	}))
	defer srv.Close()

	// Replace the Nominatim URL by monkey-patching the package-level client.
	// We need a custom roundtripper that rewrites the host.
	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	coord, err := geocodeCity(context.Background(), "Prague")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coord.lat == 0 || coord.lon == 0 {
		t.Errorf("expected non-zero coord, got %+v", coord)
	}
}

func TestGeocodeCity_Cache(t *testing.T) {
	clearGeoCache()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode([]struct {
			Lat string `json:"lat"`
			Lon string `json:"lon"`
		}{
			{Lat: "48.8566", Lon: "2.3522"},
		})
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	// Two calls — second should hit cache.
	if _, err := geocodeCity(context.Background(), "Paris"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := geocodeCity(context.Background(), "Paris"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (cache hit), got %d", calls)
	}
}

func TestGeocodeCity_CacheKeyIsLowercaseTrimmed(t *testing.T) {
	clearGeoCache()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode([]struct {
			Lat string `json:"lat"`
			Lon string `json:"lon"`
		}{
			{Lat: "52.5200", Lon: "13.4050"},
		})
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	if _, err := geocodeCity(context.Background(), "  Berlin  "); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Same city, different case/spacing — should still hit cache.
	if _, err := geocodeCity(context.Background(), "berlin"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call for case-insensitive cache, got %d", calls)
	}
}

func TestGeocodeCity_EmptyResults(t *testing.T) {
	clearGeoCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	_, err := geocodeCity(context.Background(), "Nonexistent City XYZ")
	if err == nil {
		t.Fatal("expected error for empty geocode results")
	}
}

func TestGeocodeCity_HTTPError(t *testing.T) {
	clearGeoCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	_, err := geocodeCity(context.Background(), "BrokenCity")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestGeocodeCity_InvalidJSON(t *testing.T) {
	clearGeoCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	_, err := geocodeCity(context.Background(), "BadJSON")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGeocodeCity_InvalidLatitude(t *testing.T) {
	clearGeoCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]struct {
			Lat string `json:"lat"`
			Lon string `json:"lon"`
		}{
			{Lat: "not-a-float", Lon: "14.4378"},
		})
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	_, err := geocodeCity(context.Background(), "BadLat")
	if err == nil {
		t.Fatal("expected error for unparseable latitude")
	}
}

func TestGeocodeCity_InvalidLongitude(t *testing.T) {
	clearGeoCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]struct {
			Lat string `json:"lat"`
			Lon string `json:"lon"`
		}{
			{Lat: "50.0755", Lon: "not-a-float"},
		})
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	_, err := geocodeCity(context.Background(), "BadLon")
	if err == nil {
		t.Fatal("expected error for unparseable longitude")
	}
}

// ------------------------------------------------------------------ GetForecast

// buildNominatimHandler returns an HTTP handler that serves a single geocode result.
func buildNominatimHandler(lat, lon string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]struct {
			Lat string `json:"lat"`
			Lon string `json:"lon"`
		}{
			{Lat: lat, Lon: lon},
		})
	}
}

// buildOpenMeteoHandler returns an HTTP handler serving a minimal Open-Meteo response.
func buildOpenMeteoHandler(times []string, maxTemps, minTemps, precips []float64, codes []int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := openMeteoResponse{}
		resp.Daily.Time = times
		resp.Daily.TemperatureMax = maxTemps
		resp.Daily.TemperatureMin = minTemps
		resp.Daily.PrecipitationSum = precips
		resp.Daily.WeatherCode = codes
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// dualMux returns a ServeMux that routes nominatim-shaped requests (/search)
// to geocodeHandler and everything else to weatherHandler.
func dualMux(geocodeHandler, weatherHandler http.HandlerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", geocodeHandler)
	mux.HandleFunc("/v1/forecast", weatherHandler)
	// Catch-all for mismatches in test URL rewriting.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func TestGetForecast_Success(t *testing.T) {
	clearGeoCache()

	srv := httptest.NewServer(dualMux(
		buildNominatimHandler("50.0755", "14.4378"),
		buildOpenMeteoHandler(
			[]string{"2026-05-01", "2026-05-02"},
			[]float64{20.0, 18.0},
			[]float64{10.0, 9.0},
			[]float64{0.0, 3.5},
			[]int{0, 61},
		),
	))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	result, err := GetForecast(context.Background(), "Prague", "2026-05-01", "2026-05-02")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.City != "Prague" {
		t.Errorf("city = %q, want Prague", result.City)
	}
	if len(result.Forecasts) != 2 {
		t.Fatalf("forecasts = %d, want 2", len(result.Forecasts))
	}
	if result.Forecasts[0].Description != "Sunny" {
		t.Errorf("day1 description = %q, want Sunny", result.Forecasts[0].Description)
	}
	if result.Forecasts[1].Description != "Rain" {
		t.Errorf("day2 description = %q, want Rain", result.Forecasts[1].Description)
	}
}

func TestGetForecast_GeocodeFailure(t *testing.T) {
	clearGeoCache()

	// Nominatim returns empty results.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	result, err := GetForecast(context.Background(), "NowhereAtAll", "2026-05-01", "2026-05-07")
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// GeocodeCity failure is surfaced as WeatherResult.Success=false, not a Go error.
	if result.Success {
		t.Error("expected Success=false when geocode fails")
	}
	if result.Error == "" {
		t.Error("expected non-empty Error when geocode fails")
	}
}

func TestGetForecast_OpenMeteoHTTPError(t *testing.T) {
	clearGeoCache()

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/search" {
			buildNominatimHandler("48.8566", "2.3522")(w, r)
			return
		}
		// Open-Meteo returns 503.
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	result, err := GetForecast(context.Background(), "Paris", "2026-05-01", "2026-05-07")
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Success {
		t.Error("expected Success=false for HTTP 503 from Open-Meteo")
	}
}

func TestGetForecast_OpenMeteoInvalidJSON(t *testing.T) {
	clearGeoCache()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			buildNominatimHandler("52.5200", "13.4050")(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	_, err := GetForecast(context.Background(), "Berlin", "2026-05-01", "2026-05-07")
	if err == nil {
		t.Fatal("expected error for invalid JSON from Open-Meteo")
	}
}

func TestGetForecast_EmptyForecasts(t *testing.T) {
	clearGeoCache()

	srv := httptest.NewServer(dualMux(
		buildNominatimHandler("41.9028", "12.4964"),
		buildOpenMeteoHandler(nil, nil, nil, nil, nil),
	))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	result, err := GetForecast(context.Background(), "Rome", "2026-05-01", "2026-05-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected Success=true even for empty forecast data, got error: %s", result.Error)
	}
	if len(result.Forecasts) != 0 {
		t.Errorf("expected 0 forecasts for empty response, got %d", len(result.Forecasts))
	}
}

func TestGetForecast_MissingDatesInForecast(t *testing.T) {
	clearGeoCache()

	// Three time entries but one is empty — should be skipped.
	srv := httptest.NewServer(dualMux(
		buildNominatimHandler("51.5074", "0.1278"),
		buildOpenMeteoHandler(
			[]string{"2026-05-01", "", "2026-05-03"},
			[]float64{15.0, 0, 18.0},
			[]float64{8.0, 0, 11.0},
			[]float64{0.0, 0, 2.0},
			[]int{0, 0, 2},
		),
	))
	defer srv.Close()

	restore := replaceHTTPClient(&http.Client{
		Transport: &hostRewriter{base: http.DefaultTransport, target: srv.URL},
	})
	defer restore()

	result, err := GetForecast(context.Background(), "London", "2026-05-01", "2026-05-03")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success: %s", result.Error)
	}
	// Empty date at index 1 should be skipped.
	if len(result.Forecasts) != 2 {
		t.Errorf("expected 2 forecasts (empty date skipped), got %d", len(result.Forecasts))
	}
}

// ------------------------------------------------------------------ describeWeatherCode extra branches

func TestDescribeWeatherCode_ExtraBranches(t *testing.T) {
	tests := []struct {
		code   int
		precip float64
		want   string
	}{
		{56, 0, "Freezing drizzle"},
		{57, 0, "Freezing drizzle"},
		{62, 1, "Rain"},
		{65, 6, "Heavy rain"},
		{66, 0, "Freezing rain"},
		{67, 0, "Freezing rain"},
		{73, 0, "Snow"},
		{75, 0, "Snow"},
		{77, 0, "Snow grains"},
		{81, 0, "Rain showers"},
		{82, 0, "Rain showers"},
		{85, 0, "Snow showers"},
		{86, 0, "Snow showers"},
		{99, 0, "Thunderstorm with hail"},
	}
	for _, tc := range tests {
		got := describeWeatherCode(tc.code, tc.precip)
		if got != tc.want {
			t.Errorf("describeWeatherCode(%d, %.1f) = %q, want %q", tc.code, tc.precip, got, tc.want)
		}
	}
}

// ------------------------------------------------------------------ hostRewriter

// hostRewriter is a test http.RoundTripper that rewrites every request URL
// so its scheme+host match a test server, allowing package-level functions that
// hardcode external URLs (Nominatim, Open-Meteo) to be intercepted.
type hostRewriter struct {
	base   http.RoundTripper
	target string // e.g. "http://127.0.0.1:PORT"
}

func (rw *hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	// Rewrite only scheme + host; preserve path + query.
	rewritten := fmt.Sprintf("%s%s", rw.target, req.URL.RequestURI())
	u, err := req.URL.Parse(rewritten)
	if err != nil {
		return nil, err
	}
	clone.URL = u
	clone.Host = u.Host
	return rw.base.RoundTrip(clone)
}
