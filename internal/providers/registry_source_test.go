package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceRegistryIgnoresRuntimeDefinitionsAndPreservesThem(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "providers")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "oracle.json")
	canary := `{"id":"oracle","name":"Oracle","category":"hotels","endpoint":"https://example.com/private","response_mapping":{"results_path":"items"}}`
	if err := os.WriteFile(legacyPath, []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := newSourceRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if registry.Get("oracle") != nil {
		t.Fatal("runtime JSON definition became executable")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy definition was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "provider-definitions-source-only.notice")); err != nil {
		t.Fatalf("one-time migration notice was not recorded: %v", err)
	}
	if err := registry.Save(&ProviderConfig{ID: "oracle"}); err == nil || !strings.Contains(err.Error(), "not a reviewed provider") {
		t.Fatalf("arbitrary definition save error = %v", err)
	}
}

func TestSourceRegistryPersistsPreferenceWithoutChangingDefinition(t *testing.T) {
	root := t.TempDir()
	registry, err := newSourceRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	shipped := registry.Get("openstreetmap-hotels")
	if shipped == nil {
		t.Fatal("reviewed embedded definition missing")
	}
	originalEndpoint := shipped.Endpoint
	request := *shipped
	request.Endpoint = "https://attacker.example/collect"
	request.Headers = map[string]string{"Authorization": "Bearer canary-secret"}
	request.Consent = &ConsentRecord{Granted: true, Domain: shipped.EndpointDomain()}
	if err := registry.Save(&request); err != nil {
		t.Fatal(err)
	}
	if !registry.IsEnabled(shipped.ID) {
		t.Fatal("reviewed provider was not enabled")
	}
	if got := registry.Get(shipped.ID); got.Endpoint != originalEndpoint || got.Headers["Authorization"] != "" {
		t.Fatalf("runtime input changed immutable definition: endpoint=%q headers=%v", got.Endpoint, got.Headers)
	}

	reloaded, err := newSourceRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsEnabled(shipped.ID) || len(reloaded.ListByCategory("hotels")) != 1 {
		t.Fatal("enabled preference did not survive restart")
	}
	if err := reloaded.Delete(shipped.ID); err != nil {
		t.Fatal(err)
	}
	afterDisable, err := newSourceRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if afterDisable.IsEnabled(shipped.ID) || len(afterDisable.List()) != 0 {
		t.Fatal("disabled reviewed provider remained active")
	}
}

func TestSourceRegistryReadAPIsDoNotExposeMutableDefinitions(t *testing.T) {
	registry, err := newSourceRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shipped := registry.Get("openstreetmap-hotels")
	if shipped == nil {
		t.Fatal("reviewed embedded definition missing")
	}
	shipped.Endpoint = "https://attacker.example/collect"
	shipped.Headers["User-Agent"] = "attacker"
	shipped.QueryParams["data"] = "attacker"
	if got := registry.Get(shipped.ID); got.Endpoint == shipped.Endpoint {
		t.Fatal("Get exposed the live embedded definition")
	} else if got.Headers["User-Agent"] == "attacker" || got.QueryParams["data"] == "attacker" {
		t.Fatal("Get exposed nested maps from the live embedded definition")
	}

	if err := registry.Save(registry.Get(shipped.ID)); err != nil {
		t.Fatal(err)
	}
	listed := registry.List()
	if len(listed) != 1 {
		t.Fatalf("enabled provider count = %d, want 1", len(listed))
	}
	listed[0].Endpoint = "https://attacker.example/list"
	listed[0].ResponseMapping.Fields["name"] = "attacker"
	if got := registry.Get(shipped.ID); got.Endpoint == listed[0].Endpoint {
		t.Fatal("List exposed the live embedded definition")
	} else if got.ResponseMapping.Fields["name"] == "attacker" {
		t.Fatal("List exposed nested mappings from the live embedded definition")
	}

	reloaded, err := registry.Reload(shipped.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Headers["User-Agent"] = "attacker-reload"
	if got := registry.ReloadIfChanged(shipped.ID); got.Headers["User-Agent"] == "attacker-reload" {
		t.Fatal("Reload exposed nested maps from the live embedded definition")
	}
}
