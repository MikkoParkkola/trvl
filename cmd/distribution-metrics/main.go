package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/distributionmetrics"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, distributionmetrics.Collect); err != nil {
		log.Fatal(err)
	}
}

type collectFunc func(context.Context, distributionmetrics.Config) (distributionmetrics.Report, error)

func run(ctx context.Context, args []string, stdout, stderr io.Writer, collect collectFunc) error {
	cfg := distributionmetrics.DefaultConfig()
	var now string
	var launchDate string

	fs := flag.NewFlagSet("distribution-metrics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.Owner, "owner", cfg.Owner, "GitHub repository owner")
	fs.StringVar(&cfg.Repo, "repo", cfg.Repo, "GitHub repository name")
	fs.StringVar(&cfg.NPMPackage, "npm-package", cfg.NPMPackage, "npm package name to query")
	fs.StringVar(&cfg.MetricsDir, "metrics-dir", cfg.MetricsDir, "directory for weekly JSON snapshots")
	fs.StringVar(&cfg.DashboardPath, "dashboard", cfg.DashboardPath, "markdown dashboard path")
	fs.IntVar(&cfg.Weeks, "weeks", cfg.Weeks, "rolling npm window in weeks")
	fs.StringVar(&now, "date", "", "capture date in YYYY-MM-DD format; defaults to today UTC")
	fs.StringVar(&launchDate, "launch-date", cfg.LaunchDate.Format(time.DateOnly), "positioning launch date in YYYY-MM-DD format")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if now != "" {
		parsed, err := time.Parse(time.DateOnly, now)
		if err != nil {
			return fmt.Errorf("invalid -date: %w", err)
		}
		cfg.Now = parsed
	}
	if launchDate != "" {
		parsed, err := time.Parse(time.DateOnly, launchDate)
		if err != nil {
			return fmt.Errorf("invalid -launch-date: %w", err)
		}
		cfg.LaunchDate = parsed
	}

	if err := distributionmetrics.ValidateConfig(cfg); err != nil {
		return err
	}
	report, err := collect(ctx, cfg)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "week=%s\n", report.WeekKey)
	_, _ = fmt.Fprintf(stdout, "github_snapshot=%s total_downloads=%d\n", report.GitHubPath, report.GitHubTotal)
	_, _ = fmt.Fprintf(stdout, "npm_snapshot=%s total_downloads=%d\n", report.NPMPath, report.NPMTotal)
	if report.NPMError != "" {
		_, _ = fmt.Fprintf(stdout, "npm_note=%s\n", report.NPMError)
	}
	_, _ = fmt.Fprintf(stdout, "dashboard=%s\n", report.DashboardPath)
	return nil
}
