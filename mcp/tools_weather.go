package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/weather"
)

// getForecastFunc is the weather lookup; tests swap it to stay offline.
var getForecastFunc = weather.GetForecast

// getWeatherTool returns the MCP tool definition for weather forecasts.
func getWeatherTool() ToolDef {
	return ToolDef{
		Name:        "get_weather",
		Title:       "Weather Forecast",
		Description: "Get a weather forecast for any city using Open-Meteo (free, no API key). Returns up to 14 days of daily forecasts with temperature, precipitation, and conditions.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"city":      {Type: "string", Description: "City name (e.g. Prague, Helsinki, Tokyo). Pass city or location."},
				"location":  {Type: "string", Description: "Alias of city. Pass city or location; both only if they name the same place."},
				"from_date": {Type: "string", Description: "Start date (YYYY-MM-DD, default: today)"},
				"to_date":   {Type: "string", Description: "End date (YYYY-MM-DD, default: today+6)"},
			},
		},
		OutputSchema: weatherOutputSchema(),
		Annotations: &ToolAnnotations{
			Title:          "Weather Forecast",
			ReadOnlyHint:   true,
			OpenWorldHint:  true,
			IdempotentHint: true,
		},
	}
}

func weatherOutputSchema() interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": schemaBool(),
			"city":    schemaString(),
			"forecasts": schemaArray(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city":          schemaString(),
					"date":          schemaString(),
					"temp_max":      schemaNum(),
					"temp_min":      schemaNum(),
					"precipitation": schemaNum(),
					"description":   schemaString(),
				},
			}),
			"error": schemaString(),
		},
		"required": []string{"success", "city", "forecasts"},
	}
}

func handleGetWeather(ctx context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	city, err := weatherCity(args)
	if err != nil {
		return nil, nil, err
	}
	fromDate := argString(args, "from_date")
	toDate := argString(args, "to_date")

	if fromDate == "" {
		fromDate = time.Now().Format("2006-01-02")
	}
	if toDate == "" {
		toDate = time.Now().AddDate(0, 0, 6).Format("2006-01-02")
	}

	sendProgress(progress, 10, 100, fmt.Sprintf("Geocoding %s...", city))

	sendProgress(progress, 40, 100, "Fetching forecast from Open-Meteo...")

	result, err := getForecastFunc(ctx, city, fromDate, toDate)
	if err != nil {
		return nil, nil, toolExecutionError("Weather forecast", err)
	}
	if !result.Success {
		return nil, nil, toolResultError("Weather forecast for "+city, result.Error)
	}

	sendProgress(progress, 100, 100, fmt.Sprintf("Got %d day forecast", len(result.Forecasts)))

	summary := buildWeatherSummary(result)
	content, err := buildAnnotatedContentBlocks(summary, result)
	if err != nil {
		return nil, nil, err
	}
	return content, result, nil
}

// weatherCity resolves the place from city or its alias location. Agents send
// location, so either is accepted; both are accepted only when they agree.
func weatherCity(args map[string]any) (string, error) {
	city := strings.TrimSpace(argString(args, "city"))
	location := strings.TrimSpace(argString(args, "location"))
	switch {
	case city == "":
		city = location
	case location != "" && !strings.EqualFold(city, location):
		return "", fmt.Errorf("city %q and location %q differ; pass only one of them", city, location)
	}
	if city == "" {
		return "", fmt.Errorf("city is required (location is accepted as an alias)")
	}
	return city, nil
}

func buildWeatherSummary(result *weather.WeatherResult) string {
	if !result.Success {
		return fmt.Sprintf("Weather forecast for %s unavailable: %s", result.City, result.Error)
	}
	if len(result.Forecasts) == 0 {
		return fmt.Sprintf("No forecast data for %s.", result.City)
	}

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "Weather forecast for %s:\n\n", result.City)
	for _, f := range result.Forecasts {
		emoji := weather.WeatherEmoji(f.Description)
		_, _ = fmt.Fprintf(&sb, "  %s %s (%s)  %d°/%d°C",
			weather.FormatDateShort(f.Date),
			weather.DayOfWeek(f.Date),
			emoji,
			int(f.TempMin), int(f.TempMax))
		if f.Precipitation > 0 {
			_, _ = fmt.Fprintf(&sb, "  %.0fmm rain", f.Precipitation)
		}
		_, _ = fmt.Fprintf(&sb, "  %s\n", f.Description)
	}
	return sb.String()
}
