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

func airCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "air CITY",
		Short: "Show current air quality for a city (free, no API key)",
		Long: `Fetch current air quality for any city using the Open-Meteo Air Quality API
(free, no API key). Reports the European AQI band plus key pollutants.

Examples:
  trvl air "Funchal"
  trvl air "Helsinki" --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()

			res, err := weather.GetAirQuality(ctx, args[0])
			if err != nil {
				return err
			}
			f, _ := cmd.Flags().GetString("format")
			if f == "json" {
				return models.FormatJSON(os.Stdout, res)
			}
			if !res.Success {
				_, _ = fmt.Fprintf(os.Stderr, "Air quality lookup failed: %s\n", res.Error)
				return nil
			}
			models.Banner(os.Stdout, "💨", fmt.Sprintf("Air quality · %s", res.City),
				fmt.Sprintf("%s AQI %.0f — %s", weather.AQIEmoji(res.AQI), res.AQI, res.Category))
			fmt.Println()
			fmt.Printf("  European AQI : %.0f (%s)\n", res.AQI, res.Category)
			fmt.Printf("  PM2.5        : %.1f µg/m³\n", res.PM25)
			fmt.Printf("  PM10         : %.1f µg/m³\n", res.PM10)
			fmt.Printf("  Ozone        : %.1f µg/m³\n", res.Ozone)
			fmt.Printf("  NO₂          : %.1f µg/m³\n", res.NO2)
			if res.Time != "" {
				fmt.Printf("  As of        : %s\n", res.Time)
			}
			return nil
		},
	}
	return cmd
}

func sunCmd() *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "sun CITY",
		Short: "Show sunrise, sunset & twilight for a city (free, no API key)",
		Long: `Fetch sunrise, sunset, solar noon and civil twilight for any city using
sunrise-sunset.org (free, no API key). Times are UTC.

Examples:
  trvl sun "Funchal"
  trvl sun "Helsinki" --date 2026-06-21 --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
			defer cancel()

			res, err := weather.GetSunTimes(ctx, args[0], date)
			if err != nil {
				return err
			}
			f, _ := cmd.Flags().GetString("format")
			if f == "json" {
				return models.FormatJSON(os.Stdout, res)
			}
			if !res.Success {
				_, _ = fmt.Fprintf(os.Stderr, "Sun-times lookup failed: %s\n", res.Error)
				return nil
			}
			models.Banner(os.Stdout, "🌅", fmt.Sprintf("Sun times · %s (UTC)", res.City))
			fmt.Println()
			fmt.Printf("  Dawn (civil) : %s\n", res.CivilTwilightBegin)
			fmt.Printf("  Sunrise      : %s\n", res.Sunrise)
			fmt.Printf("  Solar noon   : %s\n", res.SolarNoon)
			fmt.Printf("  Sunset       : %s\n", res.Sunset)
			fmt.Printf("  Dusk (civil) : %s\n", res.CivilTwilightEnd)
			fmt.Printf("  Day length   : %dh %02dm\n", res.DayLength/3600, (res.DayLength%3600)/60)
			fmt.Println()
			fmt.Println("  " + weather.SunAttribution)
			return nil
		},
	}
	cmd.Flags().StringVar(&date, "date", "", "Date (YYYY-MM-DD); default today")
	return cmd
}
