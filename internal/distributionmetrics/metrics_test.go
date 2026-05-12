package distributionmetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectWritesSnapshotsAndDashboard(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/MikkoParkkola/trvl/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"tag_name": "v1.2.3",
				"published_at": "2026-05-12T11:00:21Z",
				"assets": [
					{"name": "trvl_1.2.3_darwin_arm64.tar.gz", "download_count": 7},
					{"name": "checksums.txt", "download_count": 2}
				]
			},
			{
				"tag_name": "v1.2.2",
				"published_at": "2026-05-12T10:21:50Z",
				"assets": [
					{"name": "trvl_1.2.2_linux_amd64.tar.gz", "download_count": 3}
				]
			}
		]`))
	})
	mux.HandleFunc("/downloads/range/2026-03-25:2026-05-19/trvl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"downloads": [
				{"day": "2026-05-11", "downloads": 4},
				{"day": "2026-05-12", "downloads": 6},
				{"day": "2026-05-18", "downloads": 10}
			],
			"start": "2026-03-25",
			"end": "2026-05-19",
			"package": "trvl"
		}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Owner:         "MikkoParkkola",
		Repo:          "trvl",
		NPMPackage:    "trvl",
		MetricsDir:    filepath.Join(dir, ".internal", "metrics"),
		DashboardPath: filepath.Join(dir, "docs", "internal", "distribution-metrics.md"),
		Weeks:         8,
		Now:           time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		LaunchDate:    time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		HTTPClient:    server.Client(),
		GitHubAPIBase: server.URL,
		NPMAPIBase:    server.URL + "/downloads/range",
	}
	report, err := Collect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if report.WeekKey != "202621" {
		t.Fatalf("week key = %q, want 202621", report.WeekKey)
	}
	if report.GitHubTotal != 12 {
		t.Fatalf("GitHub total = %d, want 12", report.GitHubTotal)
	}
	if report.NPMTotal != 20 {
		t.Fatalf("npm total = %d, want 20", report.NPMTotal)
	}

	for _, path := range []string{report.GitHubPath, report.NPMPath, report.DashboardPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
	dashboard, err := os.ReadFile(report.DashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(dashboard)
	for _, needle := range []string{
		"GitHub release asset downloads: 12",
		"npm `trvl` downloads",
		"| 202620 | 10 |",
		"| 202621 | 10 |",
		"No user IDs, IP addresses, referrers, or other PII are collected.",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("dashboard missing %q:\n%s", needle, text)
		}
	}
}

func TestFetchNPMRecordsNotFoundAsZeroBaseline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	snap, err := FetchNPM(context.Background(), Config{
		NPMPackage: "trvl",
		Weeks:      8,
		Now:        time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC),
		HTTPClient: server.Client(),
		NPMAPIBase: server.URL,
	})
	if err != nil {
		t.Fatalf("FetchNPM() error = %v", err)
	}
	if snap.TotalDownload != 0 {
		t.Fatalf("total downloads = %d, want 0", snap.TotalDownload)
	}
	if snap.Error == "" {
		t.Fatal("expected 404 to be recorded in snapshot error")
	}
}
