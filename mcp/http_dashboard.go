package mcp

import (
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// dashboardTemplate renders a self-contained, dependency-free operational view.
// No external CSS/JS, no framework — a single server-rendered HTML page, in
// keeping with trvl's no-frameworks rule. Values are auto-escaped by
// html/template; provider error strings are already secret-redacted upstream.
var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"pct": formatPct,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>trvl status</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 2rem auto; max-width: 1100px; padding: 0 1rem; }
  h1 { font-size: 1.4rem; margin: 0 0 .25rem; }
  .meta { color: #888; margin: 0 0 1.5rem; font-size: .85rem; }
  .summary { display: flex; gap: 1.5rem; flex-wrap: wrap; margin-bottom: 1.5rem; }
  .summary span { font-weight: 600; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: .4rem .6rem; border-bottom: 1px solid #8884; white-space: nowrap; }
  th { font-weight: 600; border-bottom: 2px solid #8886; }
  td.num { text-align: right; font-variant-numeric: tabular-nums; }
  td.err { white-space: normal; color: #b00; font-size: .85rem; }
  .dot { display: inline-block; width: .7rem; height: .7rem; border-radius: 50%; margin-right: .35rem; vertical-align: middle; }
  .ok { background: #2e9e44; } .warn { background: #d99e00; } .bad { background: #d12f2f; } .idle { background: #999; }
  .pill { padding: .1rem .45rem; border-radius: .6rem; font-size: .8rem; }
  .pill.closed { background: #2e9e4422; color: #2e9e44; }
  .pill.half_open { background: #d99e0022; color: #b8860b; }
  .pill.open { background: #d12f2f22; color: #d12f2f; }
  .pill.unknown { background: #8884; color: #888; }
  footer { color: #888; font-size: .8rem; margin-top: 2rem; }
</style>
</head>
<body>
<h1>trvl status</h1>
<p class="meta">{{.Server}} v{{.Version}} &middot; {{len .Rows}} provider(s) &middot; refreshed {{.Now}} &middot; auto-refresh 30s</p>

<div class="summary">
  <span><span class="dot ok"></span>{{.Healthy}} healthy</span>
  <span><span class="dot warn"></span>{{.Degraded}} degraded</span>
  <span><span class="dot bad"></span>{{.Open}} circuit-open</span>
  {{if .RateLimited}}<span><span class="dot warn"></span>{{.RateLimited}} rate-limited</span>{{end}}
  {{if .Stale}}<span><span class="dot idle"></span>{{.Stale}} stale</span>{{end}}
</div>

{{if .Rows}}
<table>
<thead><tr>
  <th>Provider</th><th class="num">Calls</th><th class="num">OK%</th><th class="num">Avg ms</th>
  <th>Freshness</th><th>Circuit</th><th>Last error</th>
</tr></thead>
<tbody>
{{range .Rows}}
<tr>
  <td>{{.Provider}}{{if and .Name (ne .Name .Provider)}} <small>({{.Name}})</small>{{end}}</td>
  <td class="num">{{.TotalCalls}}</td>
  <td class="num">{{if .TotalCalls}}{{pct .SuccessRate}}{{else}}&mdash;{{end}}</td>
  <td class="num">{{.AvgLatencyMs}}</td>
  <td>{{.Freshness}}</td>
  <td><span class="pill {{.CircuitState}}">{{.CircuitState}}</span></td>
  <td class="err">{{if .LastErrorClass}}{{.LastErrorClass}}{{else if .LastError}}{{.LastError}}{{else}}&mdash;{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p>No provider activity recorded yet. Run a search (e.g. <code>trvl flights HEL CDG</code>) to populate the health log.</p>
{{end}}

<footer>Read-only. Reads ~/.trvl/health.jsonl and live circuit-breaker state. No credentials are exposed; error strings are secret-redacted.</footer>
</body>
</html>`))

// formatPct renders a success rate (0.0–1.0) as a one-decimal percentage,
// e.g. "92.3%". Used by the dashboard template's {{pct}} helper.
func formatPct(f float64) string {
	return fmt.Sprintf("%.1f%%", f*100)
}

// dashboardData is the view model rendered by dashboardTemplate.
type dashboardData struct {
	Server      string
	Version     string
	Now         string
	Rows        []providers.StatusRow
	Healthy     int
	Degraded    int
	Open        int
	RateLimited int
	Stale       int
}

// handleDashboard serves a read-only HTML operational dashboard: per-provider
// health from ~/.trvl/health.jsonl merged with live circuit-breaker state.
//
// Auth posture: when authentication is configured (mandatory for any non-
// loopback bind via requireRemoteAuth), a valid read token is required. An
// unauthenticated server is, by that same startup invariant, loopback-only —
// safe to serve without a token.
func (h *HTTPServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if h.auth != nil && h.auth.Configured() {
		if _, ok := h.authorize(r); !ok {
			h.audit.denied.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	dir, err := providers.HealthLogDir()
	if err != nil {
		http.Error(w, "status unavailable", http.StatusInternalServerError)
		return
	}
	reg, _ := providers.NewRegistry()
	rows := providers.BuildStatusReport(reg, dir, time.Now())

	data := dashboardData{
		Server:  serverName,
		Version: serverVersion,
		Now:     time.Now().UTC().Format(time.RFC3339),
		Rows:    rows,
	}
	for _, row := range rows {
		switch {
		case row.CircuitState == "open":
			data.Open++
		case !row.IsHealthy():
			data.Degraded++
		default:
			data.Healthy++
		}
		if row.RateLimited() {
			data.RateLimited++
		}
		if row.Freshness == "stale" {
			data.Stale++
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := dashboardTemplate.Execute(w, data); err != nil {
		// Headers may already be flushed; best-effort.
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
