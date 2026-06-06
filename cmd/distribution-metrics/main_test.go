package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/distributionmetrics"
)

func TestRunParsesFlagsAndPrintsReport(t *testing.T) {
	var gotCfg distributionmetrics.Config
	collect := func(_ context.Context, cfg distributionmetrics.Config) (distributionmetrics.Report, error) {
		gotCfg = cfg
		return distributionmetrics.Report{
			WeekKey:       "2026-W20",
			GitHubPath:    "/tmp/github.json",
			NPMPath:       "/tmp/npm.json",
			DashboardPath: "/tmp/dashboard.md",
			GitHubTotal:   42,
			NPMTotal:      7,
			NPMError:      "npm range unavailable",
		}, nil
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--owner", "example",
		"--repo", "repo",
		"--npm-package", "@example/trvl",
		"--metrics-dir", "/tmp/metrics",
		"--dashboard", "/tmp/dashboard.md",
		"--weeks", "4",
		"--date", "2026-05-13",
		"--launch-date", "2026-05-01",
	}, &stdout, &stderr, collect)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if gotCfg.Owner != "example" || gotCfg.Repo != "repo" || gotCfg.NPMPackage != "@example/trvl" {
		t.Fatalf("parsed repo config = %#v", gotCfg)
	}
	if gotCfg.Weeks != 4 {
		t.Fatalf("Weeks = %d, want 4", gotCfg.Weeks)
	}
	if got := gotCfg.Now.Format(time.DateOnly); got != "2026-05-13" {
		t.Fatalf("Now = %s, want 2026-05-13", got)
	}
	if got := gotCfg.LaunchDate.Format(time.DateOnly); got != "2026-05-01" {
		t.Fatalf("LaunchDate = %s, want 2026-05-01", got)
	}

	out := stdout.String()
	for _, want := range []string{
		"week=2026-W20",
		"github_snapshot=/tmp/github.json total_downloads=42",
		"npm_snapshot=/tmp/npm.json total_downloads=7",
		"npm_note=npm range unavailable",
		"dashboard=/tmp/dashboard.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunRejectsInvalidDateBeforeCollect(t *testing.T) {
	called := false
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--date", "May 13"}, &stdout, &stderr,
		func(context.Context, distributionmetrics.Config) (distributionmetrics.Report, error) {
			called = true
			return distributionmetrics.Report{}, nil
		})
	if err == nil || !strings.Contains(err.Error(), "invalid -date") {
		t.Fatalf("run() error = %v, want invalid -date", err)
	}
	if called {
		t.Fatal("collector was called after invalid date")
	}
}

func TestRunPropagatesCollectorError(t *testing.T) {
	wantErr := errors.New("collector failed")
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), nil, &stdout, &stderr,
		func(context.Context, distributionmetrics.Config) (distributionmetrics.Report, error) {
			return distributionmetrics.Report{}, wantErr
		})
	if !errors.Is(err, wantErr) {
		t.Fatalf("run() error = %v, want %v", err, wantErr)
	}
}
