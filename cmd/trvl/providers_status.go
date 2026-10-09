package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/providers"
)

// classifyProviderStatus returns the health classification for a provider:
//   - "healthy"      — last_success within 24 hours
//   - "stale"        — last_success more than 24 hours ago
//   - "error"        — has a recorded error
//   - "unconfigured" — no requests have been made yet
func classifyProviderStatus(cfg *providers.ProviderConfig) string {
	if cfg.ErrorCount > 0 && cfg.LastError != "" {
		return "error"
	}
	if cfg.LastSuccess.IsZero() {
		return "unconfigured"
	}
	if time.Since(cfg.LastSuccess) > 24*time.Hour {
		return "stale"
	}
	return "healthy"
}

// colorProviderStatus wraps a status string in the appropriate ANSI color.
func colorProviderStatus(status string) string {
	switch status {
	case "healthy":
		return models.Green(status)
	case "stale":
		return models.Yellow(status)
	case "error":
		return models.Red(status)
	default:
		return models.Dim(status)
	}
}

// relativeTimeStr formats a timestamp as a human-readable relative duration
// (e.g. "2h ago", "3d ago"). Returns "-" for zero times.
func relativeTimeStr(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

// truncateStr shortens s to maxLen characters, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// runStatusProbes executes TestProvider against each configured provider and
// prints a results table. Uses Helsinki as a default test location.
func runStatusProbes(parent context.Context, configs []*providers.ProviderConfig) {
	location := "Helsinki"
	lat, lon := 60.1699, 24.9384
	checkin := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
	checkout := time.Now().AddDate(0, 0, 15).Format("2006-01-02")
	currency := "EUR"
	guests := 2

	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()

	headers := []string{"Name", "Probe", "Results", "Detail"}
	rows := make([][]string, 0, len(configs))

	for _, cfg := range configs {
		result := providers.TestProvider(ctx, cfg, location, lat, lon, checkin, checkout, currency, guests)

		probeStatus := models.Green("pass")
		detail := "-"
		resultsStr := "-"

		if !result.Success {
			probeStatus = models.Red("fail")
			detail = truncateStr(result.Error, 80)
		} else {
			resultsStr = fmt.Sprintf("%d", result.ResultsCount)
		}

		rows = append(rows, []string{
			cfg.Name,
			probeStatus,
			resultsStr,
			detail,
		})
	}

	models.FormatTable(os.Stdout, headers, rows)
}
