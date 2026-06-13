package providers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestValidate_MissingID(t *testing.T) {
	cfg := ProviderConfig{Name: "x", Category: "hotel", Endpoint: "https://api.example.com/search", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing ID")
	}
}

func TestValidate_InvalidID(t *testing.T) {
	for _, id := range []string{"../prefs", "bad/id", `bad\id`, "-bad", ""} {
		t.Run(id, func(t *testing.T) {
			cfg := ProviderConfig{ID: id, Name: "x", Category: "hotel", Endpoint: "https://api.example.com/search", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
			if err := cfg.Validate(); err == nil {
				t.Error("expected error for invalid ID")
			}
		})
	}
}

func TestValidate_MissingName(t *testing.T) {
	cfg := ProviderConfig{ID: "x", Category: "hotel", Endpoint: "https://api.example.com/search", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing Name")
	}
}

func TestValidate_MissingCategory(t *testing.T) {
	cfg := ProviderConfig{ID: "x", Name: "x", Endpoint: "https://api.example.com/search", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing Category")
	}
}

func TestValidate_InvalidCategory(t *testing.T) {
	cfg := ProviderConfig{ID: "x", Name: "x", Category: "spaceship", Endpoint: "https://api.example.com/search", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid category")
	}
}

func TestValidate_MissingEndpoint(t *testing.T) {
	cfg := ProviderConfig{ID: "x", Name: "x", Category: "hotel", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing Endpoint")
	}
}

func TestValidate_EndpointNoScheme(t *testing.T) {
	cfg := ProviderConfig{ID: "x", Name: "x", Category: "hotel", Endpoint: "api.example.com/search", ResponseMapping: ResponseMapping{ResultsPath: "results"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for endpoint without scheme")
	}
}

func TestValidate_MissingResultsPath(t *testing.T) {
	cfg := ProviderConfig{ID: "x", Name: "x", Category: "hotel", Endpoint: "https://api.example.com/search"}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing results_path")
	}
}

func TestValidate_NegativeRateLimit(t *testing.T) {
	cfg := ProviderConfig{
		ID: "x", Name: "x", Category: "hotel",
		Endpoint:        "https://api.example.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
		RateLimit:       RateLimitConfig{RequestsPerSecond: -1},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative rate limit")
	}
}

func TestValidate_ExcessiveRateLimit(t *testing.T) {
	cfg := ProviderConfig{
		ID: "x", Name: "x", Category: "hotel",
		Endpoint:        "https://api.example.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
		RateLimit:       RateLimitConfig{RequestsPerSecond: 200},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for rate limit > 100")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := ProviderConfig{
		ID: "test", Name: "Test Provider", Category: "hotel",
		Endpoint:        "https://api.example.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
		RateLimit:       RateLimitConfig{RequestsPerSecond: 5, Burst: 1},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}
}

// ============================================================
// ProviderConfig.EndpointDomain
// ============================================================

func TestEndpointDomain_Valid(t *testing.T) {
	cfg := ProviderConfig{Endpoint: "https://api.booking.com/search?q=1"}
	if got := cfg.EndpointDomain(); got != "api.booking.com" {
		t.Errorf("EndpointDomain() = %q, want 'api.booking.com'", got)
	}
}

func TestEndpointDomain_Invalid(t *testing.T) {
	cfg := ProviderConfig{Endpoint: "://bad"}
	got := cfg.EndpointDomain()
	// url.Parse may succeed on some inputs; just verify no panic.
	_ = got
}

func TestEndpointDomain_Empty(t *testing.T) {
	cfg := ProviderConfig{Endpoint: ""}
	if got := cfg.EndpointDomain(); got != "" {
		t.Errorf("EndpointDomain() = %q, want empty", got)
	}
}

// ============================================================
// ProviderConfig.Status
// ============================================================

func TestStatus_New(t *testing.T) {
	cfg := ProviderConfig{}
	if got := cfg.Status(); got != "new" {
		t.Errorf("Status() = %q, want 'new'", got)
	}
}

func TestStatus_OK(t *testing.T) {
	cfg := ProviderConfig{LastSuccess: time.Now()}
	if got := cfg.Status(); got != "ok" {
		t.Errorf("Status() = %q, want 'ok'", got)
	}
}

func TestStatus_Error(t *testing.T) {
	cfg := ProviderConfig{ErrorCount: 3}
	if got := cfg.Status(); got != "error" {
		t.Errorf("Status() = %q, want 'error'", got)
	}
}

func TestStatus_ErrorTakesPrecedence(t *testing.T) {
	cfg := ProviderConfig{ErrorCount: 1, LastSuccess: time.Now()}
	if got := cfg.Status(); got != "error" {
		t.Errorf("Status() = %q, want 'error' (takes precedence over ok)", got)
	}
}

// ============================================================
// ProviderConfig.IsStale
// ============================================================

func TestIsStale_NoErrors(t *testing.T) {
	cfg := ProviderConfig{ErrorCount: 0}
	if cfg.IsStale() {
		t.Error("should not be stale with no errors")
	}
}

func TestIsStale_ErrorsNoSuccess(t *testing.T) {
	cfg := ProviderConfig{ErrorCount: 5}
	if !cfg.IsStale() {
		t.Error("should be stale with errors and no last success")
	}
}

func TestIsStale_ErrorsRecentSuccess(t *testing.T) {
	cfg := ProviderConfig{ErrorCount: 1, LastSuccess: time.Now()}
	if cfg.IsStale() {
		t.Error("should not be stale with recent success")
	}
}

func TestIsStale_ErrorsOldSuccess(t *testing.T) {
	cfg := ProviderConfig{ErrorCount: 1, LastSuccess: time.Now().Add(-48 * time.Hour)}
	if !cfg.IsStale() {
		t.Error("should be stale with old success and errors")
	}
}

// ============================================================
// Registry — CRUD operations
// ============================================================

func TestRegistry_SaveGetListDelete(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:              "test-provider",
		Name:            "Test",
		Category:        "hotel",
		Endpoint:        "https://api.example.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
	}

	// Save.
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get.
	got := reg.Get("test-provider")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != "Test" {
		t.Errorf("Name = %q, want 'Test'", got.Name)
	}

	// List.
	all := reg.List()
	if len(all) != 1 {
		t.Errorf("List() len = %d, want 1", len(all))
	}

	// ListByCategory.
	hotels := reg.ListByCategory("hotel")
	if len(hotels) != 1 {
		t.Errorf("ListByCategory('hotel') len = %d, want 1", len(hotels))
	}
	flights := reg.ListByCategory("flight")
	if len(flights) != 0 {
		t.Errorf("ListByCategory('flight') len = %d, want 0", len(flights))
	}

	// Delete.
	if err := reg.Delete("test-provider"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if reg.Get("test-provider") != nil {
		t.Error("Get after Delete should return nil")
	}

	// Delete nonexistent.
	if err := reg.Delete("nonexistent"); err == nil {
		t.Error("Delete nonexistent should return error")
	}
}

// ============================================================
// Registry.Reload
// ============================================================

func TestRegistry_Reload(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID:              "reload-test",
		Name:            "Original",
		Category:        "hotel",
		Endpoint:        "https://api.example.com/search",
		ResponseMapping: ResponseMapping{ResultsPath: "results"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Modify the file on disk.
	cfg.Name = "Updated"
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save updated: %v", err)
	}

	reloaded, err := reg.Reload("reload-test")
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if reloaded.Name != "Updated" {
		t.Errorf("Name = %q, want 'Updated'", reloaded.Name)
	}
}

func TestRegistry_ReloadMissing(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	_, err = reg.Reload("nonexistent")
	if err == nil {
		t.Error("Reload nonexistent should return error")
	}
}

func TestRegistry_SaveRejectsPathTraversalID(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	err = reg.Save(&ProviderConfig{
		ID:              "../preferences",
		Name:            "Bad",
		Category:        "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	})
	if err == nil {
		t.Fatal("expected invalid ID error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "..", "preferences.json")); !os.IsNotExist(statErr) {
		t.Fatalf("path traversal target exists or stat failed unexpectedly: %v", statErr)
	}
}

func TestRegistry_SaveSecuresProviderFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX permission bits in os.FileMode")
	}
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	cfg := &ProviderConfig{
		ID:              "secure-test",
		Name:            "Secure",
		Category:        "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %#o, want 0700", got)
	}
	info, err := os.Stat(filepath.Join(dir, "secure-test.json"))
	if err != nil {
		t.Fatalf("stat provider file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %#o, want 0600", got)
	}
}

// ============================================================
// Registry.ReloadIfChanged
// ============================================================

func TestRegistry_ReloadIfChanged_NoFile(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	got := reg.ReloadIfChanged("nonexistent")
	if got != nil {
		t.Error("ReloadIfChanged for nonexistent should return nil")
	}
}

func TestRegistry_ReloadIfChanged_Unchanged(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	cfg := &ProviderConfig{
		ID: "ric-test", Name: "Test", Category: "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := reg.ReloadIfChanged("ric-test")
	if got == nil {
		t.Fatal("expected non-nil config")
	}
	if got.Name != "Test" {
		t.Errorf("Name = %q, want 'Test'", got.Name)
	}
}

// ============================================================
// Registry.MarkSuccess / MarkError
// ============================================================

func TestRegistry_MarkSuccess(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	cfg := &ProviderConfig{
		ID: "ms-test", Name: "Test", Category: "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
		ErrorCount:      5,
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reg.MarkSuccess("ms-test")
	got := reg.Get("ms-test")
	if got.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0 after MarkSuccess", got.ErrorCount)
	}
	if got.LastSuccess.IsZero() {
		t.Error("LastSuccess should be set after MarkSuccess")
	}
}

func TestRegistry_MarkSuccess_Nonexistent(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	// Should not panic.
	reg.MarkSuccess("nonexistent")
}

func TestRegistry_MarkError(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	cfg := &ProviderConfig{
		ID: "me-test", Name: "Test", Category: "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reg.MarkError("me-test", "connection timeout")
	got := reg.Get("me-test")
	if got.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", got.ErrorCount)
	}
	if got.LastError != "connection timeout" {
		t.Errorf("LastError = %q, want 'connection timeout'", got.LastError)
	}
}

func TestRegistry_MarkError_Nonexistent(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	// Should not panic.
	reg.MarkError("nonexistent", "err")
}

func TestRegistry_ResetBreaker(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	lastSuccess := time.Now().Add(-time.Hour).Truncate(time.Second)
	cfg := &ProviderConfig{
		ID: "reset-test", Name: "Test", Category: "hotel",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
		ErrorCount:      7,
		LastError:       "browser cookies missing",
		LastErrorAt:     time.Now().Add(-time.Minute),
		LastSuccess:     lastSuccess,
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := reg.ResetBreaker("reset-test"); err != nil {
		t.Fatalf("ResetBreaker: %v", err)
	}
	got := reg.Get("reset-test")
	if got.ErrorCount != 0 {
		t.Errorf("ErrorCount = %d, want 0", got.ErrorCount)
	}
	if got.LastError != "" {
		t.Errorf("LastError = %q, want empty", got.LastError)
	}
	if !got.LastErrorAt.IsZero() {
		t.Errorf("LastErrorAt = %v, want zero", got.LastErrorAt)
	}
	if !got.LastSuccess.Equal(lastSuccess) {
		t.Errorf("LastSuccess = %v, want %v", got.LastSuccess, lastSuccess)
	}
}

func TestRegistry_ResetBreakerMissingProvider(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	if err := reg.ResetBreaker("missing"); err == nil {
		t.Fatal("expected error for missing provider")
	}
}

// ============================================================
// substituteVars
// ============================================================

func TestSubstituteVars_Basic(t *testing.T) {
	got := substituteVars("Hello ${name}, welcome to ${city}!", map[string]string{
		"${name}": "Alice",
		"${city}": "Prague",
	})
	if got != "Hello Alice, welcome to Prague!" {
		t.Errorf("got %q", got)
	}
}

func TestSubstituteVars_NoPlaceholders(t *testing.T) {
	got := substituteVars("no vars here", nil)
	if got != "no vars here" {
		t.Errorf("got %q", got)
	}
}

// ============================================================
// substituteEnvVars
// ============================================================

func TestSubstituteEnvVars_WithEnvVar(t *testing.T) {
	t.Setenv("TRVL_TEST_EXTRA_VAR", "secret123")
	got := substituteEnvVars("key=${env.TRVL_TEST_EXTRA_VAR}")
	if got != "key=secret123" {
		t.Errorf("got %q, want 'key=secret123'", got)
	}
}

func TestSubstituteEnvVars_NoEnvPattern(t *testing.T) {
	got := substituteEnvVars("no env vars here")
	if got != "no env vars here" {
		t.Errorf("got %q", got)
	}
}

func TestSubstituteEnvVars_UnsetEnvVar(t *testing.T) {
	got := substituteEnvVars("key=${env.TRVL_DEFINITELY_NOT_SET_XYZ}")
	if got != "key=" {
		t.Errorf("got %q, want 'key=' (unset env var replaced with empty)", got)
	}
}

// ============================================================
// stripUnresolvedPlaceholders
// ============================================================
