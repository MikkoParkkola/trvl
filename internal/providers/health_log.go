package providers

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	healthLogMaxBytes = 1 * 1024 * 1024 // 1 MB rotate threshold
	healthLogBufSize  = 256             // channel buffer
)

// HealthEntry records a single external provider API call outcome.
type HealthEntry struct {
	Timestamp string `json:"ts"`
	Provider  string `json:"provider"`
	// Operation is "search", "preflight", or "auth".
	Operation string `json:"op"`
	// Status is "ok", "error", or "timeout".
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Results   int    `json:"results,omitempty"`
	Error     string `json:"error,omitempty"`
	HintCode  string `json:"hint_code,omitempty"` // typed root-cause code when status != "ok"
}

// ProviderHealth is the per-provider aggregate computed by HealthSummary.
type ProviderHealth struct {
	Provider     string  `json:"provider"`
	TotalCalls   int     `json:"total_calls"`
	SuccessCount int     `json:"success_count"`
	ErrorCount   int     `json:"error_count"`
	TimeoutCount int     `json:"timeout_count"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	LastError    string  `json:"last_error,omitempty"`
	LastHintCode string  `json:"last_hint_code,omitempty"` // most recent root-cause code for failures
}

// healthWriter is the package-level singleton that owns the write goroutine.
var (
	healthCh   chan HealthEntry
	healthOnce sync.Once
)

// HealthLogDir returns ~/.trvl, creating it if needed.
// It is exported so MCP tool handlers can locate the health log without
// importing an additional package.
func HealthLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".trvl")
	return dir, os.MkdirAll(dir, 0o700)
}

// healthLogDir is the unexported alias used internally.
func healthLogDir() (string, error) { return HealthLogDir() }

// startHealthWriter initialises the background goroutine once.
func startHealthWriter() {
	healthOnce.Do(func() {
		healthCh = make(chan HealthEntry, healthLogBufSize)
		go runHealthWriter(healthCh)
	})
}

// runHealthWriter consumes from ch and appends JSONL to ~/.trvl/health.jsonl,
// rotating when the file exceeds healthLogMaxBytes.
func runHealthWriter(ch <-chan HealthEntry) {
	for entry := range ch {
		if err := appendHealthEntry(entry); err != nil {
			slog.Warn("health_log: write failed", "error", err)
		}
	}
}

// appendHealthEntry writes a single JSONL line, rotating first if needed.
func appendHealthEntry(entry HealthEntry) error {
	dir, err := healthLogDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "health.jsonl")

	// Rotate if the file is too large.
	if info, statErr := os.Stat(path); statErr == nil && info.Size() >= healthLogMaxBytes {
		rotated := path + ".1"
		// Best-effort rename — ignore errors (e.g. Windows rename-over-existing).
		_ = os.Rename(path, rotated)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// LogHealth enqueues a health entry for async writing. It is non-blocking:
// if the channel is full the entry is silently dropped to avoid slowing
// down the provider search path.
func LogHealth(entry HealthEntry) {
	startHealthWriter()
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	select {
	case healthCh <- entry:
	default:
		// channel full — drop silently
	}
}

// ReadHealthLog reads the last N entries from the health log in dir.
// dir is the ~/.trvl directory (or an override for tests).
// If last <= 0 all entries are returned.
func ReadHealthLog(dir string, last int) ([]HealthEntry, error) {
	path := filepath.Join(dir, "health.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []HealthEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e HealthEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if last > 0 && len(entries) > last {
		entries = entries[len(entries)-last:]
	}
	return entries, nil
}

// HealthSummary reads the health log from dir and returns per-provider
// aggregate statistics.
func HealthSummary(dir string) map[string]ProviderHealth {
	entries, err := ReadHealthLog(dir, 0)
	if err != nil || len(entries) == 0 {
		return map[string]ProviderHealth{}
	}

	type agg struct {
		total        int
		successes    int
		errors       int
		timeouts     int
		latSum       int64
		lastErr      string
		lastHintCode string
	}
	m := make(map[string]*agg)

	for _, e := range entries {
		a, ok := m[e.Provider]
		if !ok {
			a = &agg{}
			m[e.Provider] = a
		}
		a.total++
		a.latSum += e.LatencyMs
		switch e.Status {
		case "ok":
			a.successes++
		case "timeout":
			a.timeouts++
			if e.Error != "" {
				a.lastErr = e.Error
			}
			if e.HintCode != "" {
				a.lastHintCode = e.HintCode
			}
		default:
			a.errors++
			if e.Error != "" {
				a.lastErr = e.Error
			}
			if e.HintCode != "" {
				a.lastHintCode = e.HintCode
			}
		}
	}

	result := make(map[string]ProviderHealth, len(m))
	for provider, a := range m {
		var avgLat int64
		if a.total > 0 {
			avgLat = a.latSum / int64(a.total)
		}
		var rate float64
		if a.total > 0 {
			rate = float64(a.successes) / float64(a.total)
		}
		result[provider] = ProviderHealth{
			Provider:     provider,
			TotalCalls:   a.total,
			SuccessCount: a.successes,
			ErrorCount:   a.errors,
			TimeoutCount: a.timeouts,
			SuccessRate:  rate,
			AvgLatencyMs: avgLat,
			LastError:    a.lastErr,
			LastHintCode: a.lastHintCode,
		}
	}
	return result
}
