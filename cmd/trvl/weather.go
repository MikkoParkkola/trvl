package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/weather"
	"github.com/spf13/cobra"
)

func weatherCmd() *cobra.Command {
	var (
		fromDate string
		toDate   string
	)

	cmd := &cobra.Command{
		Use:   "weather CITY",
		Short: "Show weather forecast for a city (free, no API key)",
		Long: `Fetch a weather forecast for any city using Open-Meteo (free, no API key).

Examples:
  trvl weather "Prague" --from 2026-04-12 --to 2026-04-15
  trvl weather "Helsinki" --from 2026-04-08 --to 2026-04-12
  trvl weather "Amsterdam" --from 2026-04-20 --to 2026-04-23 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			city := args[0]

			// Default date range: today + 6 days
			if fromDate == "" {
				fromDate = time.Now().Format("2006-01-02")
			}
			if toDate == "" {
				toDate = time.Now().AddDate(0, 0, 6).Format("2006-01-02")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			result, err := weather.GetForecast(ctx, city, fromDate, toDate)
			if err != nil {
				return fmt.Errorf("weather: %w", err)
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			if !result.Success {
				_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", result.Error)
				return nil
			}

			return printWeatherTable(result, fromDate, toDate)
		},
	}

	cmd.Flags().StringVar(&fromDate, "from", "", "Start date (YYYY-MM-DD, default: today)")
	cmd.Flags().StringVar(&toDate, "to", "", "End date (YYYY-MM-DD, default: today+6)")
	return cmd
}

func printWeatherTable(result *weather.WeatherResult, fromDate, toDate string) error {
	// Header
	from := weather.FormatDateShort(fromDate)
	to := weather.FormatDateShort(toDate)
	models.Banner(os.Stdout, "🌤️", fmt.Sprintf("Weather · %s · %s-%s", result.City, from, to))
	fmt.Println()

	if len(result.Forecasts) == 0 {
		fmt.Println("No forecast data available.")
		return nil
	}

	headers := []string{"Date", "Day", "Min", "Max", "Rain", "Conditions"}
	var rows [][]string
	for _, f := range result.Forecasts {
		emoji := weather.WeatherEmoji(f.Description)
		cond := emoji + " " + f.Description

		minTemp := fmt.Sprintf("%d°", int(f.TempMin))
		maxTemp := fmt.Sprintf("%d°", int(f.TempMax))
		rain := fmt.Sprintf("%.0fmm", f.Precipitation)

		// Colorize temperature extremes
		if f.TempMax >= 25 {
			maxTemp = models.Red(maxTemp)
		} else if f.TempMax <= 5 {
			maxTemp = models.Yellow(maxTemp)
		}

		// Colorize rain
		if f.Precipitation >= 5 {
			rain = models.Yellow(rain)
		}

		rows = append(rows, []string{
			weather.FormatDateShort(f.Date),
			weather.DayOfWeek(f.Date),
			minTemp,
			maxTemp,
			rain,
			cond,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
	return nil
}
