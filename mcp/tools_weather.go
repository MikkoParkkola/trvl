package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/weather"
)

// getWeatherTool returns the MCP tool definition for weather forecasts.
func getWeatherTool() ToolDef {
	return ToolDef{
		Name:        "get_weather",
		Title:       "Weather Forecast",
		Description: "Get a weather forecast for any city using Open-Meteo (free, no API key). Returns up to 14 days of daily forecasts with temperature, precipitation, and conditions.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"city":      {Type: "string", Description: "City name (e.g. Prague, Helsinki, Tokyo)"},
				"from_date": {Type: "string", Description: "Start date (YYYY-MM-DD, default: today)"},
				"to_date":   {Type: "string", Description: "End date (YYYY-MM-DD, default: today+6)"},
			},
			Required: []string{"city"},
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
	city := argString(args, "city")
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

	result, err := weather.GetForecast(ctx, city, fromDate, toDate)
	if err != nil {
		return nil, nil, err
	}

	sendProgress(progress, 100, 100, fmt.Sprintf("Got %d day forecast", len(result.Forecasts)))

	summary := buildWeatherSummary(result)
	content := []ContentBlock{
		{Type: "text", Text: summary, Annotations: &ContentAnnotation{Audience: []string{"user"}, Priority: 1.0}},
		{Type: "text", Text: "Structured forecast data attached.", Annotations: &ContentAnnotation{Audience: []string{"assistant"}, Priority: 0.5}},
	}
	return content, result, nil
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
