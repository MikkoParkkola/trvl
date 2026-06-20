package main

import (
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/spf13/cobra"
)

// statusCmd is the top-level operational snapshot: per-provider health from the
// ~/.trvl/health.jsonl call log merged with live circuit-breaker state. Unlike
// `trvl providers status` (which lists only configured opt-in providers), this
// surfaces every provider that has actually been called — the built-in Google
// Flights/Hotels, Kiwi, ground transport, and any custom providers alike. It is
// the CLI mirror of the MCP `provider_health` tool and the /dashboard route.
func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show provider health, circuit breakers, and rate-limit pressure",
		Long: `Show an operational snapshot of trvl's data providers.

Aggregates the local health log (~/.trvl/health.jsonl) into per-provider
success rate, latency, freshness, and result counts, then overlays live
circuit-breaker state so you can see at a glance which providers are healthy,
degraded, rate-limited, or tripped open.

This reads only local files — no network calls, no credentials touched.

Examples:
  trvl status
  trvl status --format json`,
		Args: cobra.NoArgs,
		RunE: runStatus,
	}
}

func init() {
	rootCmd.AddCommand(statusCmd())
}

func runStatus(cmd *cobra.Command, _ []string) error {
	dir, err := providers.HealthLogDir()
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}

	// reg may be nil-safe: a missing/empty registry just means no circuit
	// state for built-in providers, which is fine.
	reg, _ := providers.NewRegistry()
	rows := providers.BuildStatusReport(reg, dir, time.Now())

	f, _ := cmd.Flags().GetString("format")
	if f == "json" {
		return models.FormatJSON(os.Stdout, map[string]any{"providers": rows})
	}

	if len(rows) == 0 {
		fmt.Println("No provider activity recorded yet.")
		fmt.Println("Run a search (e.g. 'trvl flights HEL CDG') to populate the health log.")
		return nil
	}

	headers := []string{"Provider", "Calls", "OK%", "Avg ms", "Fresh", "Circuit", "Last error"}
	tableRows := make([][]string, 0, len(rows))
	var healthy, degraded, open, rateLimited, stale int

	for _, r := range rows {
		okPct := "-"
		if r.TotalCalls > 0 {
			okPct = fmt.Sprintf("%.0f%%", r.SuccessRate*100)
		}
		lastErr := "-"
		if r.LastErrorClass != "" {
			lastErr = r.LastErrorClass
		} else if r.LastError != "" {
			lastErr = truncateStr(r.LastError, 40)
		}

		tableRows = append(tableRows, []string{
			r.Provider,
			fmt.Sprintf("%d", r.TotalCalls),
			okPct,
			fmt.Sprintf("%d", r.AvgLatencyMs),
			colorFreshness(r.Freshness),
			colorCircuit(r.CircuitState),
			lastErr,
		})

		switch {
		case r.CircuitState == "open":
			open++
		case !r.IsHealthy():
			degraded++
		default:
			healthy++
		}
		if r.RateLimited() {
			rateLimited++
		}
		if r.Freshness == "stale" {
			stale++
		}
	}

	models.Banner(os.Stdout, "\U0001F4E1", "trvl status", fmt.Sprintf("%d provider(s) seen", len(rows)))
	fmt.Println()
	models.FormatTable(os.Stdout, headers, tableRows)
	fmt.Println()

	fmt.Printf("  %s %d healthy   %s %d degraded   %s %d circuit-open\n",
		models.Green("●"), healthy,
		models.Yellow("●"), degraded,
		models.Red("●"), open)
	if rateLimited > 0 {
		fmt.Printf("  %s %d provider(s) rate-limited (HTTP 429) on last failure\n", models.Yellow("!"), rateLimited)
	}
	if stale > 0 {
		fmt.Printf("  %s %d provider(s) stale (no activity in 24h)\n", models.Yellow("!"), stale)
	}

	return nil
}

// colorFreshness renders a freshness label with a status color.
func colorFreshness(freshness string) string {
	switch freshness {
	case "fresh":
		return models.Green(freshness)
	case "stale":
		return models.Yellow(freshness)
	default:
		return freshness
	}
}

// colorCircuit renders a circuit-breaker state with a status color.
func colorCircuit(state string) string {
	switch state {
	case "closed":
		return models.Green(state)
	case "half_open":
		return models.Yellow(state)
	case "open":
		return models.Red(state)
	default:
		return state
	}
}
