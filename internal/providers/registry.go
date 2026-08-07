package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
	"github.com/MikkoParkkola/trvl/internal/logredact"
)

// Registry stores and manages provider configurations on disk.
type Registry struct {
	dir         string
	configs     map[string]*ProviderConfig
	definitions map[string]ProviderConfig
	loadedAt    map[string]time.Time // file mtime seen on last load; used by ReloadIfChanged
	sourceOnly  bool
	enabled     map[string]bool
	statePath   string
	mu          sync.RWMutex
}

// NewRegistry loads reviewed provider definitions embedded in the binary.
// Runtime files hold state only; user-supplied definitions under
// ~/.trvl/providers are never executable (#538).
func NewRegistry() (*Registry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("providers: user home dir: %w", err)
	}
	return newSourceRegistry(filepath.Join(home, ".trvl"))
}

// NewRegistryAt creates a Registry backed by the given directory.
// This is useful for testing with a temporary directory.
func NewRegistryAt(dir string) (*Registry, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("providers: create dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("providers: secure dir: %w", err)
	}

	r := &Registry{
		dir:      dir,
		configs:  make(map[string]*ProviderConfig),
		loadedAt: make(map[string]time.Time),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("providers: read dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("providers: read %s: %w", entry.Name(), err)
		}
		var cfg ProviderConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("providers: parse %s: %w", entry.Name(), err)
		}
		// MIK-3075: forward-migrate legacy configs (no schema_version) to
		// the current schema. Future-versioned configs are rejected here
		// rather than loaded silently.
		if err := Migrate(&cfg); err != nil {
			return nil, fmt.Errorf("providers: migrate %s: %w", entry.Name(), err)
		}
		if !validProviderID(cfg.ID) {
			return nil, fmt.Errorf("providers: invalid id %q in %s", cfg.ID, entry.Name())
		}
		r.configs[cfg.ID] = &cfg
		if info, err := os.Stat(path); err == nil {
			r.loadedAt[cfg.ID] = info.ModTime()
		}
	}

	return r, nil
}

// List returns all loaded provider configurations.
func (r *Registry) List() []*ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*ProviderConfig, 0, len(r.configs))
	for id, cfg := range r.configs {
		if r.sourceOnly && !r.enabled[id] {
			continue
		}
		if r.sourceOnly {
			if copy := cloneSourceConfigForRead(cfg); copy != nil {
				out = append(out, copy)
			}
			continue
		}
		out = append(out, cfg)
	}
	return out
}

// ListPublic returns all loaded provider configurations that are not marked
// personal. Use this whenever exporting or sharing provider configs with other
// users — personal providers carry individually-obtained API keys and must
// never be included in shared output.
func (r *Registry) ListPublic() []*ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*ProviderConfig, 0, len(r.configs))
	for id, cfg := range r.configs {
		if r.sourceOnly && !r.enabled[id] {
			continue
		}
		if !cfg.Personal {
			if r.sourceOnly {
				if copy := cloneSourceConfigForRead(cfg); copy != nil {
					out = append(out, copy)
				}
				continue
			}
			out = append(out, cfg)
		}
	}
	return out
}

// Get returns the provider configuration with the given ID, or nil.
func (r *Registry) Get(id string) *ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg := r.configs[id]
	if cfg == nil || !r.sourceOnly {
		return cfg
	}
	return cloneSourceConfigForRead(cfg)
}

// Save writes a provider configuration to disk and updates the in-memory map.
func (r *Registry) Save(config *ProviderConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked(config)
}

func (r *Registry) saveLocked(config *ProviderConfig) error {
	if config == nil {
		return fmt.Errorf("providers: nil config")
	}
	if r.sourceOnly {
		return r.saveSourceStateLocked(config, true)
	}
	// MIK-3075: stamp the schema version on every save so freshly written
	// configs always carry the version this binary supports. Older
	// in-memory configs that have already passed Migrate are safe — the
	// stamp is idempotent.
	if config.SchemaVersion == "" {
		config.SchemaVersion = CurrentSchemaVersion
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("providers: marshal %s: %w", config.ID, err)
	}
	path, err := r.configPath(config.ID)
	if err != nil {
		return err
	}
	if err := writeProviderFile(path, data); err != nil {
		return fmt.Errorf("providers: write %s: %w", config.ID, err)
	}
	r.configs[config.ID] = config
	// Record our own write time so ReloadIfChanged does not re-parse the
	// file we just wrote (avoids a lock-step reload on every MarkSuccess).
	if info, err := os.Stat(path); err == nil {
		r.loadedAt[config.ID] = info.ModTime()
	}
	return nil
}

// Delete removes a provider configuration from disk and memory.
// Returns an error if the provider does not exist.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.configs[id]; !ok {
		return fmt.Errorf("providers: %s not found", id)
	}
	if r.sourceOnly {
		previousEnabled := r.enabled[id]
		previousConsent := r.configs[id].Consent
		r.enabled[id] = false
		r.configs[id].Consent = nil
		if err := r.persistSourceStateLocked(); err != nil {
			r.enabled[id] = previousEnabled
			r.configs[id].Consent = previousConsent
			return err
		}
		return nil
	}

	path, err := r.configPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("providers: delete %s: %w", id, err)
	}
	delete(r.configs, id)
	delete(r.loadedAt, id)
	return nil
}

// ListByCategory returns all provider configurations with the given category.
func (r *Registry) ListByCategory(category string) []*ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []*ProviderConfig
	for id, cfg := range r.configs {
		if (!r.sourceOnly || r.enabled[id]) && cfg.Category == category {
			if r.sourceOnly {
				if copy := cloneSourceConfigForRead(cfg); copy != nil {
					out = append(out, copy)
				}
				continue
			}
			out = append(out, cfg)
		}
	}
	return out
}

// Reload re-reads the provider config JSON for the given ID from disk and
// swaps the in-memory copy. Returns the reloaded config, or an error if the
// file is missing or malformed. Intended for tools like test_provider that
// want to pick up manual edits to ~/.trvl/providers/*.json without a full
// MCP-server restart. Returns the existing in-memory config unchanged if
// the file has not been modified since the last load.
func (r *Registry) Reload(id string) (*ProviderConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sourceOnly {
		cfg := r.configs[id]
		if cfg == nil {
			return nil, fmt.Errorf("providers: %s not found", id)
		}
		return cloneSourceConfigForRead(cfg), nil
	}

	path, err := r.configPath(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("providers: reload %s: %w", id, err)
	}
	var cfg ProviderConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("providers: parse %s: %w", id, err)
	}
	if err := Migrate(&cfg); err != nil {
		return nil, fmt.Errorf("providers: migrate %s: %w", id, err)
	}
	if !validProviderID(cfg.ID) {
		return nil, fmt.Errorf("providers: invalid id %q in %s", cfg.ID, path)
	}
	r.configs[cfg.ID] = &cfg
	if info, err := os.Stat(path); err == nil {
		r.loadedAt[cfg.ID] = info.ModTime()
	}
	return &cfg, nil
}

// ReloadIfChanged reloads the provider config from disk only when the file's
// mtime is newer than the last load. Returns the current (possibly reloaded)
// in-memory config. Safe to call on every request — the common path is a
// single os.Stat and no JSON parse or write-lock acquisition.
func (r *Registry) ReloadIfChanged(id string) *ProviderConfig {
	if r.sourceOnly {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return cloneSourceConfigForRead(r.configs[id])
	}
	path, err := r.configPath(id)
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.configs[id]
	}

	r.mu.RLock()
	last := r.loadedAt[id]
	existing := r.configs[id]
	r.mu.RUnlock()

	if existing != nil && !info.ModTime().After(last) {
		return existing
	}

	// File is newer — take the write lock and reparse.
	r.mu.Lock()
	defer r.mu.Unlock()
	// Re-check under lock: another goroutine may have already reloaded.
	if last2, ok := r.loadedAt[id]; ok && !info.ModTime().After(last2) {
		return r.configs[id]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return r.configs[id]
	}
	var cfg ProviderConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return r.configs[id]
	}
	if err := Migrate(&cfg); err != nil {
		// Stale-but-valid in-memory config beats a hard failure on the
		// hot path; surface the migration error via slog only.
		return r.configs[id]
	}
	if !validProviderID(cfg.ID) {
		return r.configs[id]
	}
	r.configs[cfg.ID] = &cfg
	r.loadedAt[cfg.ID] = info.ModTime()
	return &cfg
}

func (r *Registry) configPath(id string) (string, error) {
	if !validProviderID(id) {
		return "", fmt.Errorf("providers: invalid id %q", id)
	}
	dir, err := filepath.Abs(r.dir)
	if err != nil {
		return "", fmt.Errorf("providers: resolve dir: %w", err)
	}
	path := filepath.Join(dir, id+".json")
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", fmt.Errorf("providers: resolve path for %s: %w", id, err)
	}
	if rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("providers: invalid path for id %q", id)
	}
	return path, nil
}

func writeProviderFile(path string, data []byte) error {
	return atomicjson.WriteBytes(path, data)
}

// BreakerState holds breaker fields copied under the registry lock.
type BreakerState struct {
	ErrorCount  int
	LastError   string
	LastErrorAt time.Time
	LastSuccess time.Time
}

// BreakerSnapshot copies breaker fields for the given provider under RLock.
func (r *Registry) BreakerSnapshot(id string) (BreakerState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cfg, ok := r.configs[id]
	if !ok {
		return BreakerState{}, false
	}
	return BreakerState{
		ErrorCount:  cfg.ErrorCount,
		LastError:   cfg.LastError,
		LastErrorAt: cfg.LastErrorAt,
		LastSuccess: cfg.LastSuccess,
	}, true
}

// ListSafe returns value copies of all loaded provider configurations, taken
// under RLock. Use this on any path that may run concurrently with
// MarkSuccess/MarkError/ResetBreaker (the MCP server tool handlers, the HTTP
// dashboard, and BuildStatusReport): the breaker fields
// (ErrorCount/LastError/LastErrorAt/LastSuccess) are value types, so the
// struct copy snapshots them atomically under the lock. Reading those fields
// off the live shared pointers returned by List()/Get() on a concurrent path
// is the #144 data-race class. Display-only single-threaded callers (the CLI
// commands, which run one command and exit) may keep using List()/Get().
//
// The remaining reference-typed fields (Headers, lookups, Consent, …) are
// shared with the live config, which is safe: they are populated at load time
// and never mutated by the breaker writers, so concurrent readers never race
// on them.
func (r *Registry) ListSafe() []*ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*ProviderConfig, 0, len(r.configs))
	for id, cfg := range r.configs {
		if r.sourceOnly && !r.enabled[id] {
			continue
		}
		if r.sourceOnly {
			if copy := cloneSourceConfigForRead(cfg); copy != nil {
				out = append(out, copy)
			}
			continue
		}
		c := *cfg // value copy snapshots breaker fields under the lock
		out = append(out, &c)
	}
	return out
}

// GetSafe returns a value copy of the provider configuration with the given ID
// taken under RLock, or nil. It is the single-provider companion to ListSafe
// for concurrent readers; see ListSafe for the rationale.
func (r *Registry) GetSafe(id string) *ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cfg, ok := r.configs[id]
	if !ok {
		return nil
	}
	if r.sourceOnly {
		return cloneSourceConfigForRead(cfg)
	}
	c := *cfg // value copy snapshots breaker fields under the lock
	return &c
}

// MarkSuccess records a successful request for the given provider.
func (r *Registry) MarkSuccess(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg, ok := r.configs[id]
	if !ok {
		return
	}
	cfg.LastSuccess = time.Now()
	cfg.ErrorCount = 0
	if r.sourceOnly {
		_ = r.persistSourceStateLocked()
		return
	}
	_ = r.saveLocked(cfg)
}

// MarkError records a failed request for the given provider.
//
// The message is redacted HERE rather than at the call site, so that no caller
// can leak by forgetting. What lands in cfg.LastError is written to
// ~/.trvl/providers/<id>.json and kept until the next failure overwrites it --
// a surface well outside the log stream, and one that does not scroll away.
//
// It is read back by BreakerSnapshot, for the circuit breaker. NOT by the MCP
// dashboard: that renders StatusRow.LastError, which status_report.go fills
// from the health journal, a separate store redacted on write. An earlier
// version of this comment claimed the dashboard as a second surface and was
// wrong. Corrected rather than quietly dropped, because a comment that
// overstates what it protects is the same defect as one that understates it,
// and this file has spent the evening fixing the second kind.
//
// The value arrives as err.Error(), and every net/http transport failure is a
// *url.Error carrying the full request URL: origin, destination, dates, and
// whatever else the query held. The caller in runtime_search.go already wrapped
// that same error for its slog line and then passed it raw to this function --
// the redaction and the leak one line apart. Putting the rule inside the
// function is what makes that impossible rather than merely fixed once.
func (r *Registry) MarkError(id string, errMsg string) {
	errMsg = logredact.Text(errMsg)

	r.mu.Lock()
	defer r.mu.Unlock()

	cfg, ok := r.configs[id]
	if !ok {
		return
	}
	cfg.ErrorCount++
	cfg.LastError = errMsg
	cfg.LastErrorAt = time.Now()
	if r.sourceOnly {
		_ = r.persistSourceStateLocked()
		return
	}
	_ = r.saveLocked(cfg)
}

// ResetBreaker clears the circuit-breaker error fields for a configured
// provider after login, session, or endpoint details have been fixed.
func (r *Registry) ResetBreaker(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg, ok := r.configs[id]
	if !ok {
		return fmt.Errorf("providers: %s not found", id)
	}
	cfg.ErrorCount = 0
	cfg.LastError = ""
	cfg.LastErrorAt = time.Time{}
	if r.sourceOnly {
		return r.persistSourceStateLocked()
	}
	return r.saveLocked(cfg)
}
