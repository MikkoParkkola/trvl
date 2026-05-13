package main

// MIK-3084: `trvl forecast <route>` — empirical-CDF buy-now-vs-wait advisor.
//
// Reads the same deal-history.json that internal/dealquality writes and
// runs internal/forecast.ForecastAgainst on every (kind, season) bucket
// the user has data for. Output is intentionally compact — the curve
// renders as a sparkline, the verdict comes through with a one-line
// summary, and JSON is available for scripted callers (watcher etc.).
//
// The command is purely read-only: it never appends samples or mutates
// preferences. Pairing this with the existing watch_daemon (which does
// the writing) keeps the buy-vs-wait surface explainable from outside.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/dealquality"
	"github.com/MikkoParkkola/trvl/internal/forecast"
	"github.com/spf13/cobra"
)

func forecastCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forecast <route>",
		Short: "Buy-now-vs-wait recommendation from your price history",
		Long: `Apply an empirical-CDF forecast to your locally captured price history
for the given route (e.g. "AMS-NYC" or "amsterdam-rome"). Output includes
the commit-now confidence (0-100), the probability of seeing a lower price
within the horizon, the expected savings if you wait, and a sparkline of
the historical price distribution against the current quote.

The recommendation is only as good as the history. Routes with fewer than
30 observations are flagged as insufficient-data and produce a neutral
verdict. The forecast assumes price observations are roughly i.i.d. across
the 90-day window — useful for stable corridors, less so during
fare-class structural breaks (sales, seat-map exhaustion).`,
		Args:    cobra.ExactArgs(1),
		Example: "  trvl forecast AMS-NYC --kind flight --price 540\n  trvl forecast amsterdam-rome --kind hotel --price 95 --horizon 14 --format json",
		RunE:    runForecast,
	}
	cmd.Flags().String("kind", "flight", "Sample kind (flight or hotel)")
	cmd.Flags().Float64("price", 0, "Current quoted price to forecast against (required)")
	cmd.Flags().Int("horizon", forecast.DefaultHorizonDays, "Wait-window in days")
	cmd.Flags().String("trip-date", "", "Trip date (ISO 8601) — defaults to today (used to pick season)")
	cmd.Flags().String("history-path", "", "Override path to deal-history.json (default: ~/.trvl/deal-history.json)")
	_ = cmd.MarkFlagRequired("price")
	return cmd
}

func runForecast(cmd *cobra.Command, args []string) error {
	route := strings.TrimSpace(args[0])
	if route == "" {
		return fmt.Errorf("forecast: route must not be empty")
	}
	kind, _ := cmd.Flags().GetString("kind")
	price, _ := cmd.Flags().GetFloat64("price")
	horizon, _ := cmd.Flags().GetInt("horizon")
	tripDateStr, _ := cmd.Flags().GetString("trip-date")
	historyPath, _ := cmd.Flags().GetString("history-path")
	format, _ := cmd.Flags().GetString("format")

	tripDate := time.Now().UTC()
	if tripDateStr != "" {
		t, err := time.Parse("2006-01-02", tripDateStr)
		if err != nil {
			return fmt.Errorf("forecast: invalid trip-date %q: %w", tripDateStr, err)
		}
		tripDate = t
	}
	season := forecast.SeasonOf(tripDate)

	store, err := openHistoryStore(historyPath)
	if err != nil {
		return err
	}

	samples := store.Query(route, kind, season)
	prices := samplesToFloat64(samples)
	f := forecast.ForecastAgainst(price, prices, horizon)
	curve := forecast.HistogramOf(prices, 12, price)

	_ = context.Background() // ctx reserved for future net calls

	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Route    string            `json:"route"`
			Kind     string            `json:"kind"`
			Season   string            `json:"season"`
			Price    float64           `json:"price"`
			Forecast forecast.Forecast `json:"forecast"`
			Curve    forecast.Curve    `json:"curve"`
			Samples  int               `json:"samples"`
		}{
			Route: route, Kind: kind, Season: season, Price: price,
			Forecast: f, Curve: curve, Samples: len(samples),
		})
	}

	renderForecastTable(os.Stdout, route, kind, season, price, f, curve)
	return nil
}

func openHistoryStore(override string) (*dealquality.Store, error) {
	if override != "" {
		return dealquality.NewStore(override), nil
	}
	return dealquality.DefaultStore()
}

// samplesToFloat64 extracts prices from a []dealquality.Sample for use with
// the forecast package which works on plain float64 slices.
func samplesToFloat64(samples []dealquality.Sample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.Price
	}
	return out
}

func renderForecastTable(out *os.File, route, kind, season string, price float64, f forecast.Forecast, c forecast.Curve) {
	_, _ = fmt.Fprintf(out, "Forecast for %s [%s, %s] @ %.2f\n", route, kind, season, price)
	if f.InsufficientData {
		_, _ = fmt.Fprintf(out, "  ⚠ %s — recommendation suppressed.\n", f.Reason)
		return
	}
	_, _ = fmt.Fprintf(out, "  Commit-now confidence: %d/100\n", f.CommitNowConfidence)
	_, _ = fmt.Fprintf(out, "  P(lower price in %dd):  %.0f%% (instantaneous %.0f%%)\n",
		f.HorizonDays, f.HorizonProbability*100, f.DropProbability*100)
	_, _ = fmt.Fprintf(out, "  Expected savings:       %.2f (same currency as quote)\n", f.ExpectedSavingsIfWait)
	_, _ = fmt.Fprintf(out, "  Samples backing model:  %d (90-day rolling window)\n", f.Samples)
	_, _ = fmt.Fprintf(out, "  Verdict:                %s\n", f.Reason)
	if len(c.Buckets) > 0 {
		_, _ = fmt.Fprintf(out, "\n  Price distribution (▏ = current quote):\n  %s\n", renderSparkline(c))
		_, _ = fmt.Fprintf(out, "  range %.0f .. %.0f\n", c.Min, c.Max)
	}
}

// renderSparkline turns the histogram into 8-level Unicode bars; the
// current quote is overlaid as a vertical mark in the bucket it falls
// into. Pure formatting — no I/O — so a forecast_test.go can exercise
// it without touching disk.
func renderSparkline(c forecast.Curve) string {
	const blocks = "▁▂▃▄▅▆▇█"
	if len(c.Buckets) == 0 {
		return "(no data)"
	}
	max := 0
	for _, b := range c.Buckets {
		if b > max {
			max = b
		}
	}
	if max == 0 {
		return strings.Repeat("·", len(c.Buckets))
	}
	thresholdBucket := -1
	if c.Max > c.Min {
		idx := int((c.Threshold - c.Min) / (c.Max - c.Min) * float64(len(c.Buckets)))
		if idx >= 0 && idx < len(c.Buckets) {
			thresholdBucket = idx
		}
	}
	var b strings.Builder
	for i, n := range c.Buckets {
		if i == thresholdBucket {
			b.WriteRune('▏')
		}
		level := (n * (len(blocks) - 1)) / max
		b.WriteByte(blocks[level])
	}
	return b.String()
}

// _ = sort.Float64s reserves the sort import for an upcoming history
// flag that orders observations chronologically before rendering.
var _ = sort.Float64s
