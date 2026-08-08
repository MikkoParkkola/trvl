package providers

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/MikkoParkkola/trvl/internal/atomicjson"
)

//go:embed definitions/*.json
var providerDefinitions embed.FS

type sourceProviderState struct {
	Enabled     bool           `json:"enabled"`
	Consent     *ConsentRecord `json:"consent,omitempty"`
	LastSuccess time.Time      `json:"last_success,omitempty"`
	LastError   string         `json:"last_error,omitempty"`
	LastErrorAt time.Time      `json:"last_error_at,omitempty"`
	ErrorCount  int            `json:"error_count,omitempty"`
}

type sourceRegistryState struct {
	SchemaVersion int                            `json:"schema_version"`
	Providers     map[string]sourceProviderState `json:"providers"`
}

func newSourceRegistry(root string) (*Registry, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("providers: create state dir: %w", err)
	}
	// #nosec G302 -- root is a directory; 0700 is owner-only and stricter than
	// the directory-specific G301 threshold.
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("providers: secure state dir: %w", err)
	}
	r := &Registry{
		dir:         filepath.Join(root, "providers"),
		configs:     make(map[string]*ProviderConfig),
		definitions: make(map[string]ProviderConfig),
		loadedAt:    make(map[string]time.Time),
		sourceOnly:  true,
		enabled:     make(map[string]bool),
		statePath:   filepath.Join(root, "provider-state.json"),
	}
	if err := r.loadEmbeddedDefinitions(); err != nil {
		return nil, err
	}
	if err := r.loadSourceState(); err != nil {
		return nil, err
	}
	r.noticeLegacyDefinitionsOnce(root)
	return r, nil
}

func (r *Registry) loadEmbeddedDefinitions() error {
	entries, err := fs.ReadDir(providerDefinitions, "definitions")
	if err != nil {
		return fmt.Errorf("providers: read embedded definitions: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := providerDefinitions.ReadFile("definitions/" + entry.Name())
		if err != nil {
			return fmt.Errorf("providers: read embedded %s: %w", entry.Name(), err)
		}
		var cfg ProviderConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("providers: parse embedded %s: %w", entry.Name(), err)
		}
		if err := Migrate(&cfg); err != nil {
			return fmt.Errorf("providers: migrate embedded %s: %w", entry.Name(), err)
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("providers: validate embedded %s: %w", entry.Name(), err)
		}
		if err := CheckDestinationURL(cfg.Endpoint); err != nil {
			return fmt.Errorf("providers: destination in embedded %s: %w", entry.Name(), err)
		}
		if _, exists := r.configs[cfg.ID]; exists {
			return fmt.Errorf("providers: duplicate embedded id %q", cfg.ID)
		}
		definition, err := cloneProviderConfig(cfg)
		if err != nil {
			return fmt.Errorf("providers: clone embedded %s: %w", entry.Name(), err)
		}
		runtimeConfig, err := cloneProviderConfig(cfg)
		if err != nil {
			return fmt.Errorf("providers: clone embedded %s: %w", entry.Name(), err)
		}
		r.configs[cfg.ID] = &runtimeConfig
		r.definitions[cfg.ID] = definition
	}
	if len(r.configs) == 0 {
		return fmt.Errorf("providers: binary contains no reviewed provider definitions")
	}
	return nil
}

func (r *Registry) loadSourceState() error {
	data, err := os.ReadFile(r.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("providers: read state: %w", err)
	}
	var state sourceRegistryState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("providers: parse state: %w", err)
	}
	for id, saved := range state.Providers {
		cfg := r.configs[id]
		if cfg == nil {
			continue
		}
		r.enabled[id] = saved.Enabled
		cfg.Consent = saved.Consent
		cfg.LastSuccess = saved.LastSuccess
		cfg.LastError = saved.LastError
		cfg.LastErrorAt = saved.LastErrorAt
		cfg.ErrorCount = saved.ErrorCount
	}
	return nil
}

func (r *Registry) saveSourceStateLocked(config *ProviderConfig, enable bool) error {
	definition, ok := r.definitions[config.ID]
	if !ok {
		return fmt.Errorf("providers: %s is not a reviewed provider shipped with this binary; contribute a definition under internal/providers/definitions", config.ID)
	}
	// Only mutable operational state crosses this boundary. Endpoint, auth,
	// headers, body templates and mappings always remain the embedded values.
	canonical, err := cloneProviderConfig(definition)
	if err != nil {
		return fmt.Errorf("providers: restore reviewed definition %s: %w", config.ID, err)
	}
	if config.Consent != nil {
		consent := *config.Consent
		canonical.Consent = &consent
	} else {
		canonical.Consent = nil
	}
	canonical.LastSuccess = config.LastSuccess
	canonical.LastError = config.LastError
	canonical.LastErrorAt = config.LastErrorAt
	canonical.ErrorCount = config.ErrorCount
	previousConfig := r.configs[config.ID]
	previousEnabled := r.enabled[config.ID]
	r.configs[config.ID] = &canonical
	if enable {
		r.enabled[config.ID] = true
	}
	if err := r.persistSourceStateLocked(); err != nil {
		r.configs[config.ID] = previousConfig
		r.enabled[config.ID] = previousEnabled
		return err
	}
	return nil
}

func cloneProviderConfig(config ProviderConfig) (ProviderConfig, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return ProviderConfig{}, err
	}
	var copy ProviderConfig
	if err := json.Unmarshal(data, &copy); err != nil {
		return ProviderConfig{}, err
	}
	return copy, nil
}

// cloneSourceConfigForRead returns a detached copy of an embedded definition.
// A shallow struct copy would still expose headers, query parameters, mappings,
// and lookup maps to mutation by callers of Registry.Get/List. JSON cloning is
// guaranteed for ProviderConfig's data-only field graph; if a future field
// violates that assumption, fail closed by hiding the definition.
func cloneSourceConfigForRead(config *ProviderConfig) *ProviderConfig {
	if config == nil {
		return nil
	}
	copy, err := cloneProviderConfig(*config)
	if err != nil {
		return nil
	}
	return &copy
}

func (r *Registry) persistSourceStateLocked() error {
	state := sourceRegistryState{SchemaVersion: 1, Providers: make(map[string]sourceProviderState, len(r.configs))}
	for id, cfg := range r.configs {
		state.Providers[id] = sourceProviderState{
			Enabled:     r.enabled[id],
			Consent:     cfg.Consent,
			LastSuccess: cfg.LastSuccess,
			LastError:   cfg.LastError,
			LastErrorAt: cfg.LastErrorAt,
			ErrorCount:  cfg.ErrorCount,
		}
	}
	if err := atomicjson.Write(r.statePath, state); err != nil {
		return fmt.Errorf("providers: write state: %w", err)
	}
	return nil
}

// ListShipped returns every reviewed definition, including disabled ones.
func (r *Registry) ListShipped() []*ProviderConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ProviderConfig, 0, len(r.configs))
	for _, cfg := range r.configs {
		if copy := cloneSourceConfigForRead(cfg); copy != nil {
			out = append(out, copy)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) IsEnabled(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.sourceOnly || r.enabled[id]
}

func (r *Registry) IsSourceOnly() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sourceOnly
}

func (r *Registry) noticeLegacyDefinitionsOnce(root string) {
	matches, err := filepath.Glob(filepath.Join(r.dir, "*.json"))
	if err != nil || len(matches) == 0 {
		return
	}
	marker := filepath.Join(root, "provider-definitions-source-only.notice")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	slog.Warn("custom provider definitions are no longer loaded; files were retained for manual migration or rollback",
		"directory", r.dir,
		"files", len(matches),
		"action", "contribute reviewed JSON under internal/providers/definitions or use a fork")
	_ = atomicjson.Write(marker, map[string]any{
		"noticed_at": time.Now().UTC(),
		"files":      len(matches),
		"message":    "Legacy custom provider definitions were retained but are no longer executable.",
	})
}
