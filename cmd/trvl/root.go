package main

import (
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var format string
var noCache bool
var cliTimeout time.Duration
var openFlag bool

var rootCmd = &cobra.Command{
	Use:   "trvl",
	Short: "Google Flights + Hotels from your terminal. Free, no API keys.",
	Long: `trvl — real-time flight and hotel search powered by Google's internal APIs.

No API keys. No monthly fees. API-first travel search with no scraping by default.
Optional browser-assisted fallbacks exist only for a few protected providers.

  trvl flights JFK LHR 2026-07-01 --cabin business --stops nonstop
  trvl hotels "Tokyo" --checkin 2026-06-15 --checkout 2026-06-18 --stars 4
  trvl dates HEL BCN --from 2026-07-01 --to 2026-08-31 --round-trip
  trvl explore HEL --format json
  trvl mcp                  # MCP server for AI agents`,
	SilenceUsage: true,
}

func init() {
	cobra.OnInitialize(initOutputStyles)

	rootCmd.PersistentFlags().StringVar(&format, "format", "table", "output format (table, json)")
	rootCmd.PersistentFlags().BoolVar(&noCache, "no-cache", false, "bypass response cache")
	rootCmd.PersistentFlags().DurationVar(&cliTimeout, "timeout", 120*time.Second, "request timeout (e.g. 30s, 2m)")
	rootCmd.PersistentFlags().BoolVar(&openFlag, "open", false, "open the first result's booking URL in the default browser")

	// Star nudge: shown once after a few successful searches (CLI interactive only).
	rootCmd.PersistentPostRun = func(cmd *cobra.Command, args []string) {
		f, _ := cmd.Flags().GetString("format")
		if f == "" {
			f = format // fall back to persistent flag
		}
		maybeShowStarNudge(cmd.Name(), f)
	}

	rootCmd.AddCommand(flightsCmd())
	rootCmd.AddCommand(findCmd()) // primary orchestrated search
	rootCmd.AddCommand(huntCmd()) // hidden back-compat alias of `find`
	rootCmd.AddCommand(datesCmd())
	rootCmd.AddCommand(hotelsCmd())
	rootCmd.AddCommand(pricesCmd())
	rootCmd.AddCommand(reviewsCmd)
	rootCmd.AddCommand(exploreCmd())
	rootCmd.AddCommand(gridCmd())
	rootCmd.AddCommand(destinationCmd())
	rootCmd.AddCommand(tripCostCmd())
	rootCmd.AddCommand(weekendCmd())
	rootCmd.AddCommand(suggestCmd())
	rootCmd.AddCommand(multiCityCmd())
	rootCmd.AddCommand(guideCmd())
	rootCmd.AddCommand(nearbyCmd())
	rootCmd.AddCommand(eventsCmd())
	rootCmd.AddCommand(restaurantsCmd)
	rootCmd.AddCommand(groundCmd())
	rootCmd.AddCommand(airportTransferCmd())
	rootCmd.AddCommand(tripCmd())
	rootCmd.AddCommand(dealsCmd())
	rootCmd.AddCommand(routeCmd())
	rootCmd.AddCommand(watchCmd())
	rootCmd.AddCommand(roomsCmd())
	rootCmd.AddCommand(hacksCmd())
	rootCmd.AddCommand(accomHackCmd())
	rootCmd.AddCommand(mcpCmd())
	rootCmd.AddCommand(prefsCmd())
	rootCmd.AddCommand(tripsCmd())
	rootCmd.AddCommand(weatherCmd())
	rootCmd.AddCommand(baggageCmd())
	rootCmd.AddCommand(whenCmd())
	rootCmd.AddCommand(discoverCmd())
	rootCmd.AddCommand(shareCmd())
	rootCmd.AddCommand(loungesCmd())
	rootCmd.AddCommand(visaCmd())
	rootCmd.AddCommand(searchCmd())
	rootCmd.AddCommand(calendarCmd())
	rootCmd.AddCommand(pointsValueCmd())
	rootCmd.AddCommand(setupCmd())
	rootCmd.AddCommand(upgradeCmd())
	rootCmd.AddCommand(optimizeCmd())
	rootCmd.AddCommand(profileCmd())
}

// airportCompletion provides IATA code completion for cobra commands.
func airportCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 2 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	toComplete = strings.ToUpper(toComplete)
	var suggestions []string
	for code, name := range models.AirportNames {
		if strings.HasPrefix(code, toComplete) || strings.Contains(strings.ToUpper(name), toComplete) {
			suggestions = append(suggestions, code+"\t"+name)
		}
	}
	return suggestions, cobra.ShellCompDirectiveNoFileComp
}

func initOutputStyles() {
	models.UseColor = shouldUseColor(os.Stdout, term.IsTerminal, os.Getenv)
}

func shouldUseColor(stdout interface{ Fd() uintptr }, isTerminal func(int) bool, getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" {
		return false
	}
	if strings.EqualFold(getenv("CLICOLOR"), "0") {
		return false
	}
	if strings.EqualFold(getenv("TERM"), "dumb") {
		return false
	}

	if force := getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if force := getenv("FORCE_COLOR"); force != "" && force != "0" {
		return true
	}

	return isTerminal(int(stdout.Fd()))
}

type priceScale struct {
	min float64
	max float64
	ok  bool
}

func (s priceScale) With(amount float64) priceScale {
	if amount <= 0 {
		return s
	}
	if !s.ok || amount < s.min {
		s.min = amount
	}
	if !s.ok || amount > s.max {
		s.max = amount
	}
	s.ok = true
	return s
}

func (s priceScale) Apply(amount float64, text string) string {
	if amount <= 0 || !s.ok || s.min == s.max {
		return text
	}
	switch {
	case amount <= s.min:
		return models.Green(text)
	case amount >= s.max:
		return models.Red(text)
	default:
		return text
	}
}

func colorizeStops(stops int) string {
	text := formatStops(stops)
	switch {
	case stops <= 0:
		return models.Green(text)
	case stops == 1:
		return models.Yellow(text)
	default:
		return models.Red(text)
	}
}

func colorizeRating(rating float64, text string) string {
	if rating <= 0 {
		return text
	}
	switch {
	case rating >= 9.0:
		return models.Green(text)
	case rating < 7.0:
		return models.Red(text)
	default:
		return models.Yellow(text)
	}
}
