package mcp

import (
	"context"
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
	// location, so the schema must declare location and not hard-require city.
	if _, ok := tool.InputSchema.Properties["location"]; !ok {
		t.Error("schema must declare location as an alias of city")
	}
	if slices.Contains(tool.InputSchema.Required, "city") {
		t.Errorf("Required = %v, city must not be hard-required once location is accepted", tool.InputSchema.Required)
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
// for. A nil result means "succeed with one day for that city". Tests using it
// must not call t.Parallel: getForecastFunc is package state.
func stubForecast(t *testing.T, result *weather.WeatherResult) *[]string {
	t.Helper()
	var cities []string
	orig := getForecastFunc
	getForecastFunc = func(_ context.Context, city, _, _ string) (*weather.WeatherResult, error) {
		cities = append(cities, city)
		if result != nil {
			return result, nil
		}
		return &weather.WeatherResult{Success: true, City: city, Forecasts: []weather.Forecast{
			{City: city, Date: "2026-12-01", TempMax: 6, TempMin: 1, Description: "Cloudy"},
		}}, nil
	}
	t.Cleanup(func() { getForecastFunc = orig })
	return &cities
}

func TestGetWeather_LocationAliasReachesLookup(t *testing.T) {
	cases := map[string]func(*testing.T, *Server) (string, bool){
		"direct": func(t *testing.T, s *Server) (string, bool) {
			text, _, isErr := callTool(t, s, "get_weather", map[string]any{"location": "Vienna"})
			return text, isErr
		},
		"router": func(t *testing.T, s *Server) (string, bool) {
			text, _, isErr := callTravel(t, s, map[string]any{"intent": "get_weather", "params": map[string]any{"location": "Vienna"}})
			return text, isErr
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			cities := stubForecast(t, nil)
			text, isErr := call(t, NewServer())
			if isErr {
				t.Fatalf("location alias rejected: %s", text)
			}
			if !slices.Equal(*cities, []string{"Vienna"}) {
				t.Fatalf("weather lookup got cities %q, want [Vienna]", *cities)
			}
		})
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
		{"city and location differ", map[string]any{"city": "Vienna", "location": "Paris"}, "location"},
	}
	for _, tc := range cases {
		for _, via := range []string{"direct", "router"} {
			t.Run(tc.name+"/"+via, func(t *testing.T) {
				cities := stubForecast(t, nil)
				s := NewServer()
				var text string
				var isErr bool
				if via == "direct" {
					text, _, isErr = callTool(t, s, "get_weather", tc.args)
				} else {
					text, _, isErr = callTravel(t, s, map[string]any{"intent": "get_weather", "params": tc.args})
				}
				if !isErr {
					t.Fatalf("isError = false, want true; text: %q", text)
				}
				if !strings.Contains(text, tc.wantText) {
					t.Fatalf("error text %q does not name %q", text, tc.wantText)
				}
				if len(*cities) != 0 {
					t.Fatalf("weather lookup ran with %q; bad arguments must be rejected first", *cities)
				}
			})
		}
	}
}

// Inferred from "rejects both when they differ": the same value twice is fine.
func TestGetWeather_CityAndLocationAgreeing(t *testing.T) {
	cities := stubForecast(t, nil)
	text, _, isErr := callTool(t, NewServer(), "get_weather", map[string]any{"city": "Vienna", "location": "Vienna"})
	if isErr {
		t.Fatalf("matching city and location rejected: %s", text)
	}
	if !slices.Equal(*cities, []string{"Vienna"}) {
		t.Fatalf("weather lookup got cities %q, want [Vienna]", *cities)
	}
}

func TestGetWeather_LookupFailureSetsIsError(t *testing.T) {
	stubForecast(t, &weather.WeatherResult{
		Success: false,
		City:    "Atlantis",
		Error:   `geocode: no geocoding results for "Atlantis"`,
	})
	text, _, isErr := callTool(t, NewServer(), "get_weather", map[string]any{"city": "Atlantis"})
	if !isErr {
		t.Fatalf("isError = false for a failed geocode; text: %q", text)
	}
	if !strings.Contains(text, "no geocoding results") {
		t.Fatalf("error text %q must carry the lookup failure", text)
	}
}
