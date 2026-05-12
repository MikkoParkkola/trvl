package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/MikkoParkkola/trvl/internal/distributionmetrics"
)

func main() {
	cfg := distributionmetrics.DefaultConfig()
	var now string
	var launchDate string

	flag.StringVar(&cfg.Owner, "owner", cfg.Owner, "GitHub repository owner")
	flag.StringVar(&cfg.Repo, "repo", cfg.Repo, "GitHub repository name")
	flag.StringVar(&cfg.NPMPackage, "npm-package", cfg.NPMPackage, "npm package name to query")
	flag.StringVar(&cfg.MetricsDir, "metrics-dir", cfg.MetricsDir, "directory for weekly JSON snapshots")
	flag.StringVar(&cfg.DashboardPath, "dashboard", cfg.DashboardPath, "markdown dashboard path")
	flag.IntVar(&cfg.Weeks, "weeks", cfg.Weeks, "rolling npm window in weeks")
	flag.StringVar(&now, "date", "", "capture date in YYYY-MM-DD format; defaults to today UTC")
	flag.StringVar(&launchDate, "launch-date", cfg.LaunchDate.Format(time.DateOnly), "positioning launch date in YYYY-MM-DD format")
	flag.Parse()

	if now != "" {
		parsed, err := time.Parse(time.DateOnly, now)
		if err != nil {
			log.Fatalf("invalid -date: %v", err)
		}
		cfg.Now = parsed
	}
	if launchDate != "" {
		parsed, err := time.Parse(time.DateOnly, launchDate)
		if err != nil {
			log.Fatalf("invalid -launch-date: %v", err)
		}
		cfg.LaunchDate = parsed
	}

	if err := distributionmetrics.ValidateConfig(cfg); err != nil {
		log.Fatal(err)
	}
	report, err := distributionmetrics.Collect(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Fprintf(os.Stdout, "week=%s\n", report.WeekKey)
	fmt.Fprintf(os.Stdout, "github_snapshot=%s total_downloads=%d\n", report.GitHubPath, report.GitHubTotal)
	fmt.Fprintf(os.Stdout, "npm_snapshot=%s total_downloads=%d\n", report.NPMPath, report.NPMTotal)
	if report.NPMError != "" {
		fmt.Fprintf(os.Stdout, "npm_note=%s\n", report.NPMError)
	}
	fmt.Fprintf(os.Stdout, "dashboard=%s\n", report.DashboardPath)
}
