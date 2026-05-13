package distributionmetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

type Config struct {
	Owner         string
	Repo          string
	NPMPackage    string
	MetricsDir    string
	DashboardPath string
	Weeks         int
	LaunchDate    time.Time
	Now           time.Time
	HTTPClient    *http.Client
	GitHubAPIBase string
	NPMAPIBase    string
}

type Report struct {
	WeekKey       string
	GitHubPath    string
	NPMPath       string
	DashboardPath string
	GitHubTotal   int
	NPMTotal      int
	NPMError      string
}

type GitHubSnapshot struct {
	SchemaVersion int             `json:"schema_version"`
	CapturedAt    string          `json:"captured_at"`
	ISOWeek       string          `json:"iso_week"`
	Owner         string          `json:"owner"`
	Repo          string          `json:"repo"`
	SourceURL     string          `json:"source_url"`
	TotalDownload int             `json:"total_downloads"`
	Releases      []ReleaseMetric `json:"releases"`
}

type ReleaseMetric struct {
	TagName       string        `json:"tag_name"`
	PublishedAt   string        `json:"published_at"`
	TotalDownload int           `json:"total_downloads"`
	Assets        []AssetMetric `json:"assets"`
}

type AssetMetric struct {
	Name          string `json:"name"`
	DownloadCount int    `json:"download_count"`
}

type NPMSnapshot struct {
	SchemaVersion int      `json:"schema_version"`
	CapturedAt    string   `json:"captured_at"`
	ISOWeek       string   `json:"iso_week"`
	Package       string   `json:"package"`
	SourceURL     string   `json:"source_url"`
	RangeStart    string   `json:"range_start"`
	RangeEnd      string   `json:"range_end"`
	TotalDownload int      `json:"total_downloads"`
	Downloads     []NPMDay `json:"downloads"`
	Error         string   `json:"error,omitempty"`
}

type NPMDay struct {
	Day       string `json:"day"`
	Downloads int    `json:"downloads"`
}

type githubReleaseAPI struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name          string `json:"name"`
		DownloadCount int    `json:"download_count"`
	} `json:"assets"`
}

type npmRangeAPI struct {
	Package   string   `json:"package"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Downloads []NPMDay `json:"downloads"`
}

func DefaultConfig() Config {
	return Config{
		Owner:         "MikkoParkkola",
		Repo:          "trvl",
		NPMPackage:    "trvl",
		MetricsDir:    ".internal/metrics",
		DashboardPath: "docs/internal/distribution-metrics.md",
		Weeks:         8,
		LaunchDate:    time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		GitHubAPIBase: "https://api.github.com",
		NPMAPIBase:    "https://api.npmjs.org/downloads/range",
	}
}

func Collect(ctx context.Context, cfg Config) (Report, error) {
	cfg = normalizeConfig(cfg)
	weekKey := isoWeekKey(cfg.Now)
	if err := os.MkdirAll(cfg.MetricsDir, 0o700); err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DashboardPath), 0o755); err != nil {
		return Report{}, err
	}

	githubSnapshot, err := FetchGitHub(ctx, cfg)
	if err != nil {
		return Report{}, err
	}
	npmSnapshot, err := FetchNPM(ctx, cfg)
	if err != nil {
		return Report{}, err
	}

	githubPath := filepath.Join(cfg.MetricsDir, fmt.Sprintf("downloads-%s.json", weekKey))
	npmPath := filepath.Join(cfg.MetricsDir, fmt.Sprintf("npm-%s.json", weekKey))
	if err := writeJSON(githubPath, githubSnapshot); err != nil {
		return Report{}, err
	}
	if err := writeJSON(npmPath, npmSnapshot); err != nil {
		return Report{}, err
	}

	githubSnapshots, err := loadGitHubSnapshots(cfg.MetricsDir, githubSnapshot)
	if err != nil {
		return Report{}, err
	}
	npmSnapshots, err := loadNPMSnapshots(cfg.MetricsDir, npmSnapshot)
	if err != nil {
		return Report{}, err
	}
	dashboard := RenderDashboard(cfg, githubSnapshots, npmSnapshots)
	if err := os.WriteFile(cfg.DashboardPath, []byte(dashboard), 0o644); err != nil {
		return Report{}, err
	}

	return Report{
		WeekKey:       weekKey,
		GitHubPath:    githubPath,
		NPMPath:       npmPath,
		DashboardPath: cfg.DashboardPath,
		GitHubTotal:   githubSnapshot.TotalDownload,
		NPMTotal:      npmSnapshot.TotalDownload,
		NPMError:      npmSnapshot.Error,
	}, nil
}

func FetchGitHub(ctx context.Context, cfg Config) (GitHubSnapshot, error) {
	cfg = normalizeConfig(cfg)
	var apiReleases []githubReleaseAPI
	baseURL := fmt.Sprintf("%s/repos/%s/%s/releases", strings.TrimRight(cfg.GitHubAPIBase, "/"), cfg.Owner, cfg.Repo)
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s?per_page=100&page=%d", baseURL, page)
		var pageReleases []githubReleaseAPI
		if err := getJSON(ctx, cfg, url, &pageReleases); err != nil {
			return GitHubSnapshot{}, err
		}
		apiReleases = append(apiReleases, pageReleases...)
		if len(pageReleases) < 100 {
			break
		}
	}

	snapshot := GitHubSnapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    cfg.Now.Format(time.RFC3339),
		ISOWeek:       isoWeekKey(cfg.Now),
		Owner:         cfg.Owner,
		Repo:          cfg.Repo,
		SourceURL:     baseURL,
	}
	for _, rel := range apiReleases {
		metric := ReleaseMetric{
			TagName:     rel.TagName,
			PublishedAt: rel.PublishedAt,
		}
		for _, asset := range rel.Assets {
			metric.Assets = append(metric.Assets, AssetMetric{
				Name:          asset.Name,
				DownloadCount: asset.DownloadCount,
			})
			metric.TotalDownload += asset.DownloadCount
		}
		snapshot.TotalDownload += metric.TotalDownload
		snapshot.Releases = append(snapshot.Releases, metric)
	}
	sort.Slice(snapshot.Releases, func(i, j int) bool {
		return snapshot.Releases[i].TagName > snapshot.Releases[j].TagName
	})
	return snapshot, nil
}

func FetchNPM(ctx context.Context, cfg Config) (NPMSnapshot, error) {
	cfg = normalizeConfig(cfg)
	end := dateOnly(cfg.Now)
	start := end.AddDate(0, 0, -(cfg.Weeks*7 - 1))
	url := fmt.Sprintf("%s/%s:%s/%s", strings.TrimRight(cfg.NPMAPIBase, "/"), start.Format(time.DateOnly), end.Format(time.DateOnly), cfg.NPMPackage)

	snapshot := NPMSnapshot{
		SchemaVersion: SchemaVersion,
		CapturedAt:    cfg.Now.Format(time.RFC3339),
		ISOWeek:       isoWeekKey(cfg.Now),
		Package:       cfg.NPMPackage,
		SourceURL:     url,
		RangeStart:    start.Format(time.DateOnly),
		RangeEnd:      end.Format(time.DateOnly),
	}
	var api npmRangeAPI
	status, body, err := get(ctx, cfg, url)
	if err != nil {
		return NPMSnapshot{}, err
	}
	if status == http.StatusNotFound {
		snapshot.Error = "npm package or range not found"
		return snapshot, nil
	}
	if status < 200 || status >= 300 {
		return NPMSnapshot{}, fmt.Errorf("npm downloads API returned %d: %s", status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &api); err != nil {
		return NPMSnapshot{}, err
	}
	snapshot.Package = api.Package
	snapshot.Downloads = api.Downloads
	for _, day := range api.Downloads {
		snapshot.TotalDownload += day.Downloads
	}
	return snapshot, nil
}

func RenderDashboard(cfg Config, githubSnapshots []GitHubSnapshot, npmSnapshots []NPMSnapshot) string {
	cfg = normalizeConfig(cfg)
	sort.Slice(githubSnapshots, func(i, j int) bool {
		return githubSnapshots[i].CapturedAt < githubSnapshots[j].CapturedAt
	})
	sort.Slice(npmSnapshots, func(i, j int) bool {
		return npmSnapshots[i].CapturedAt < npmSnapshots[j].CapturedAt
	})
	githubSnapshots = lastGitHub(githubSnapshots, cfg.Weeks)
	npmSnapshots = lastNPM(npmSnapshots, cfg.Weeks)

	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "# Distribution Metrics\n\n")
	_, _ = fmt.Fprintf(&b, "Generated by `go run ./cmd/distribution-metrics`.\n\n")
	_, _ = fmt.Fprintf(&b, "Privacy: uses only public aggregate GitHub release-asset and npm download APIs. No user IDs, IP addresses, referrers, or other PII are collected.\n\n")
	_, _ = fmt.Fprintf(&b, "Launch baseline anchor: %s. Re-check targets: +30d %s, +90d %s.\n\n",
		cfg.LaunchDate.Format(time.DateOnly),
		cfg.LaunchDate.AddDate(0, 0, 30).Format(time.DateOnly),
		cfg.LaunchDate.AddDate(0, 0, 90).Format(time.DateOnly))
	_, _ = fmt.Fprintf(&b, "Baseline note: this snapshot is before the remaining directory listings are live; the official MCP Registry listing went live earlier on 2026-05-12.\n\n")

	if len(githubSnapshots) > 0 {
		latest := githubSnapshots[len(githubSnapshots)-1]
		_, _ = fmt.Fprintf(&b, "## Latest Snapshot\n\n")
		_, _ = fmt.Fprintf(&b, "- Captured: %s\n", latest.CapturedAt)
		_, _ = fmt.Fprintf(&b, "- GitHub release asset downloads: %d\n", latest.TotalDownload)
		if len(npmSnapshots) > 0 {
			npm := npmSnapshots[len(npmSnapshots)-1]
			_, _ = fmt.Fprintf(&b, "- npm `%s` downloads (%s to %s): %d", npm.Package, npm.RangeStart, npm.RangeEnd, npm.TotalDownload)
			if npm.Error != "" {
				_, _ = fmt.Fprintf(&b, " (%s)", npm.Error)
			}
			_, _ = fmt.Fprintf(&b, "\n")
		}
		_, _ = fmt.Fprintf(&b, "\n")
	}

	_, _ = fmt.Fprintf(&b, "## GitHub Release Downloads\n\n")
	_, _ = fmt.Fprintf(&b, "| ISO week | Captured | Total downloads | Delta | Top release |\n")
	_, _ = fmt.Fprintf(&b, "| --- | --- | ---: | ---: | --- |\n")
	prev := 0
	for i, snap := range githubSnapshots {
		delta := 0
		if i > 0 {
			delta = snap.TotalDownload - prev
		}
		prev = snap.TotalDownload
		_, _ = fmt.Fprintf(&b, "| %s | %s | %d | %+d | %s |\n",
			snap.ISOWeek,
			dateFromRFC3339(snap.CapturedAt),
			snap.TotalDownload,
			delta,
			topRelease(snap))
	}
	if len(githubSnapshots) == 0 {
		_, _ = fmt.Fprintf(&b, "| n/a | n/a | 0 | +0 | n/a |\n")
	}

	_, _ = fmt.Fprintf(&b, "\n## npm Downloads (8-week rolling)\n\n")
	weeks := aggregateNPMWeeks(npmSnapshots, cfg.Weeks)
	_, _ = fmt.Fprintf(&b, "| ISO week | Downloads | Chart |\n")
	_, _ = fmt.Fprintf(&b, "| --- | ---: | --- |\n")
	max := maxWeekDownloads(weeks)
	for _, wk := range weeks {
		_, _ = fmt.Fprintf(&b, "| %s | %d | `%s` |\n", wk.Week, wk.Downloads, bar(wk.Downloads, max))
	}
	if len(weeks) == 0 {
		_, _ = fmt.Fprintf(&b, "| n/a | 0 | `` |\n")
	}

	_, _ = fmt.Fprintf(&b, "\n## Files\n\n")
	_, _ = fmt.Fprintf(&b, "- Weekly GitHub snapshots: `.internal/metrics/downloads-$YYYYWW.json`\n")
	_, _ = fmt.Fprintf(&b, "- Weekly npm snapshots: `.internal/metrics/npm-$YYYYWW.json`\n")
	_, _ = fmt.Fprintf(&b, "- This dashboard: `%s`\n\n", cfg.DashboardPath)
	_, _ = fmt.Fprintf(&b, "Run weekly:\n\n")
	_, _ = fmt.Fprintf(&b, "```bash\nmake distribution-metrics\n```\n")
	return b.String()
}

type npmWeek struct {
	Week      string
	Downloads int
}

func normalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	if cfg.Owner == "" {
		cfg.Owner = def.Owner
	}
	if cfg.Repo == "" {
		cfg.Repo = def.Repo
	}
	if cfg.NPMPackage == "" {
		cfg.NPMPackage = def.NPMPackage
	}
	if cfg.MetricsDir == "" {
		cfg.MetricsDir = def.MetricsDir
	}
	if cfg.DashboardPath == "" {
		cfg.DashboardPath = def.DashboardPath
	}
	if cfg.Weeks <= 0 {
		cfg.Weeks = def.Weeks
	}
	if cfg.LaunchDate.IsZero() {
		cfg.LaunchDate = def.LaunchDate
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now().UTC()
	}
	cfg.Now = cfg.Now.UTC()
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.GitHubAPIBase == "" {
		cfg.GitHubAPIBase = def.GitHubAPIBase
	}
	if cfg.NPMAPIBase == "" {
		cfg.NPMAPIBase = def.NPMAPIBase
	}
	return cfg
}

func getJSON(ctx context.Context, cfg Config, url string, out any) error {
	status, body, err := get(ctx, cfg, url)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("GET %s returned %d: %s", url, status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

func get(ctx context.Context, cfg Config, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "trvl-distribution-metrics")
	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func loadGitHubSnapshots(dir string, current GitHubSnapshot) ([]GitHubSnapshot, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "downloads-*.json"))
	if err != nil {
		return nil, err
	}
	snapshots := make([]GitHubSnapshot, 0, len(matches)+1)
	seen := map[string]bool{}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var snap GitHubSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		snapshots = append(snapshots, snap)
		seen[snap.ISOWeek] = true
	}
	if !seen[current.ISOWeek] {
		snapshots = append(snapshots, current)
	}
	return snapshots, nil
}

func loadNPMSnapshots(dir string, current NPMSnapshot) ([]NPMSnapshot, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "npm-*.json"))
	if err != nil {
		return nil, err
	}
	snapshots := make([]NPMSnapshot, 0, len(matches)+1)
	seen := map[string]bool{}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var snap NPMSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		snapshots = append(snapshots, snap)
		seen[snap.ISOWeek] = true
	}
	if !seen[current.ISOWeek] {
		snapshots = append(snapshots, current)
	}
	return snapshots, nil
}

func aggregateNPMWeeks(snapshots []NPMSnapshot, limit int) []npmWeek {
	if len(snapshots) == 0 {
		return nil
	}
	latest := snapshots[len(snapshots)-1]
	if latest.Error != "" {
		return zeroNPMWeeks(latest, limit)
	}
	byWeek := map[string]int{}
	for _, day := range latest.Downloads {
		t, err := time.Parse(time.DateOnly, day.Day)
		if err != nil {
			continue
		}
		byWeek[isoWeekKey(t)] += day.Downloads
	}
	weeks := make([]npmWeek, 0, len(byWeek))
	for week, downloads := range byWeek {
		weeks = append(weeks, npmWeek{Week: week, Downloads: downloads})
	}
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Week < weeks[j].Week })
	if len(weeks) > limit {
		weeks = weeks[len(weeks)-limit:]
	}
	return weeks
}

func zeroNPMWeeks(snapshot NPMSnapshot, limit int) []npmWeek {
	start, err := time.Parse(time.DateOnly, snapshot.RangeStart)
	if err != nil {
		return []npmWeek{{Week: snapshot.ISOWeek, Downloads: 0}}
	}
	end, err := time.Parse(time.DateOnly, snapshot.RangeEnd)
	if err != nil {
		return []npmWeek{{Week: snapshot.ISOWeek, Downloads: 0}}
	}
	seen := map[string]bool{}
	var weeks []npmWeek
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		week := isoWeekKey(day)
		if seen[week] {
			continue
		}
		seen[week] = true
		weeks = append(weeks, npmWeek{Week: week})
	}
	if len(weeks) > limit {
		weeks = weeks[len(weeks)-limit:]
	}
	return weeks
}

func topRelease(snap GitHubSnapshot) string {
	if len(snap.Releases) == 0 {
		return "n/a"
	}
	best := snap.Releases[0]
	for _, rel := range snap.Releases[1:] {
		if rel.TotalDownload > best.TotalDownload {
			best = rel
		}
	}
	if best.TagName == "" {
		return "n/a"
	}
	return fmt.Sprintf("%s (%d)", best.TagName, best.TotalDownload)
}

func dateFromRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Format(time.DateOnly)
}

func maxWeekDownloads(weeks []npmWeek) int {
	max := 0
	for _, week := range weeks {
		if week.Downloads > max {
			max = week.Downloads
		}
	}
	return max
}

func bar(value, max int) string {
	if value <= 0 || max <= 0 {
		return ""
	}
	width := value * 20 / max
	if width == 0 {
		width = 1
	}
	return strings.Repeat("#", width)
}

func lastGitHub(snapshots []GitHubSnapshot, limit int) []GitHubSnapshot {
	if limit <= 0 || len(snapshots) <= limit {
		return snapshots
	}
	return snapshots[len(snapshots)-limit:]
}

func lastNPM(snapshots []NPMSnapshot, limit int) []NPMSnapshot {
	if limit <= 0 || len(snapshots) <= limit {
		return snapshots
	}
	return snapshots[len(snapshots)-limit:]
}

func isoWeekKey(t time.Time) string {
	year, week := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d%02d", year, week)
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func ValidateConfig(cfg Config) error {
	cfg = normalizeConfig(cfg)
	if cfg.Owner == "" || cfg.Repo == "" || cfg.NPMPackage == "" {
		return errors.New("owner, repo, and npm package are required")
	}
	if cfg.Weeks < 1 {
		return errors.New("weeks must be at least 1")
	}
	return nil
}
