package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
	"github.com/MikkoParkkola/trvl/internal/weather"
)

func TestGetWeatherTool_Definition(t *testing.T) {
	t.Parallel()
	tool := getWeatherTool()
	if tool.Name != "get_weather" {
		t.Errorf("Name = %q, want get_weather", tool.Name)
	}
	// Agents send location; the handler enforces exactly one of city or
	// location, so both are declared and neither is hard-required.
	if _, ok := tool.InputSchema.Properties["city"]; !ok {
		t.Error("schema must keep declaring city")
	}
	loc, ok := tool.InputSchema.Properties["location"]
	if !ok {
		t.Error("schema must declare location as an alias of city")
	} else if loc.Type != "string" || loc.Description == "" {
		t.Errorf("location = %+v, want a described string", loc)
	}
	for _, k := range []string{"city", "location"} {
		if slices.Contains(tool.InputSchema.Required, k) {
			t.Errorf("Required = %v, %s must not be hard-required: either one names the place", tool.InputSchema.Required, k)
		}
	}
}

func TestHandleGetWeather_Success(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveProbe(t)
	content, structured, err := handleGetWeather(context.Background(), map[string]any{
		"city": "Rome",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content")
	}
	if structured == nil {
		t.Fatal("expected structured result")
	}
}

// stubForecast swaps the weather lookup and records each city it is asked
// for. With a nil result and nil error it succeeds with one day for that city;
// otherwise it returns result and err as given. Tests using it must not call
// t.Parallel: getForecastFunc is package state.
func stubForecast(t *testing.T, result *weather.WeatherResult, err error) *[]string {
	t.Helper()
	var cities []string
	orig := getForecastFunc
	getForecastFunc = func(_ context.Context, city, _, _ string) (*weather.WeatherResult, error) {
		cities = append(cities, city)
		if result != nil || err != nil {
			return result, err
		}
		return &weather.WeatherResult{Success: true, City: city, Forecasts: []weather.Forecast{
			{City: city, Date: "2026-12-01", TempMax: 6, TempMin: 1, Description: "Cloudy"},
		}}, nil
	}
	t.Cleanup(func() { getForecastFunc = orig })
	return &cities
}

// callWeather calls get_weather directly or through the travel router. The
// router passes city and location through untouched (location is declared on
// get_weather, so no rename note is expected) and the handler resolves them.
func callWeather(t *testing.T, via string, args map[string]any) (text string, isError bool) {
	t.Helper()
	s := NewServer()
	if via == "router" {
		text, _, isError = callTravel(t, s, map[string]any{"intent": "get_weather", "params": args})
		return text, isError
	}
	text, _, isError = callTool(t, s, "get_weather", args)
	return text, isError
}

func TestGetWeather_AcceptsCityOrLocation(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"city only", map[string]any{"city": "Vienna"}},
		{"location only", map[string]any{"location": "Vienna"}},
		{"empty city counts as absent", map[string]any{"city": "", "location": "Vienna"}},
		{"empty location counts as absent", map[string]any{"city": "Vienna", "location": ""}},
		// Design decision: lenient when both are present and equal.
		{"city and location agree", map[string]any{"city": "Vienna", "location": "Vienna"}},
	}
	for _, tc := range cases {
		for _, via := range []string{"direct", "router"} {
			t.Run(tc.name+"/"+via, func(t *testing.T) {
				cities := stubForecast(t, nil, nil)
				text, isErr := callWeather(t, via, tc.args)
				if isErr {
					t.Fatalf("rejected: %s", text)
				}
				if !slices.Equal(*cities, []string{"Vienna"}) {
					t.Fatalf("weather lookup got cities %q, want [Vienna]", *cities)
				}
			})
		}
	}
}

func TestGetWeather_RejectsBadCityArguments(t *testing.T) {
	cases := []struct {
		name     string
		args     map[string]any
		wantText string
	}{
		{"no city or location", map[string]any{}, "city"},
		{"empty location", map[string]any{"location": ""}, "city"},
		{"whitespace location", map[string]any{"location": "   "}, "city"},
		{"whitespace city", map[string]any{"city": "   "}, "city"},
		{"city and location differ", map[string]any{"city": "Vienna", "location": "Paris"}, "location"},
	}
	for _, tc := range cases {
		for _, via := range []string{"direct", "router"} {
			t.Run(tc.name+"/"+via, func(t *testing.T) {
				cities := stubForecast(t, nil, nil)
				text, isErr := callWeather(t, via, tc.args)
				if !isErr {
					t.Fatalf("isError = false, want true; text: %q", text)
				}
				if !strings.Contains(strings.ToLower(text), tc.wantText) {
					t.Fatalf("error text %q does not name %q", text, tc.wantText)
				}
				if len(*cities) != 0 {
					t.Fatalf("weather lookup ran with %q; bad arguments must be rejected first", *cities)
				}
			})
		}
	}
}

func TestGetWeather_LookupFailureSetsIsError(t *testing.T) {
	cases := []struct {
		name       string
		result     *weather.WeatherResult
		err        error
		wantReason string
	}{
		{"failed geocode result", &weather.WeatherResult{
			Success: false,
			City:    "Atlantis",
			Error:   `geocode: no geocoding results for "Atlantis"`,
		}, nil, "no geocoding results"},
		{"lookup error", nil, errors.New("open-meteo: connection refused"), "connection refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cities := stubForecast(t, tc.result, tc.err)
			text, isErr := callWeather(t, "direct", map[string]any{"city": "Atlantis"})
			if !slices.Equal(*cities, []string{"Atlantis"}) {
				t.Fatalf("weather lookup got cities %q, want [Atlantis]", *cities)
			}
			if !isErr {
				t.Fatalf("isError = false for a failed lookup; text: %q", text)
			}
			if !strings.Contains(text, tc.wantReason) {
				t.Fatalf("error text %q must carry the lookup failure %q", text, tc.wantReason)
			}
		})
	}
}
