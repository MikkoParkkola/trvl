package providers

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TRVL.LOGLEAK.11 -- the provider registry file is the OTHER copy at rest.
//
// health.jsonl was the leak found first. This one is its sibling and it is
// worse in one respect: MarkError writes cfg.LastError to
// ~/.trvl/providers/<id>.json, where it stays until the next failure overwrites
// it, and the same string is rendered into the MCP dashboard's HTML. Neither
// surface scrolls away.
//
// The call site in runtime_search.go wrapped this exact error with
// logredact.Err for its log line and then passed it RAW to MarkError on the
// next line -- the redaction and the leak one line apart. That is why the rule
// now lives inside MarkError: a call site that forgets cannot leak.
//
// Asserted through a RELOAD FROM DISK rather than the in-memory config, because
// what matters is the bytes that persist. An in-memory check would pass even if
// saveLocked wrote something else entirely.
func TestMarkErrorDoesNotPersistTheSearchURL(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}

	cfg := &ProviderConfig{
		ID: "leaky", Name: "Leaky", Category: "flight",
		Endpoint:        "https://api.example.com",
		ResponseMapping: ResponseMapping{ResultsPath: "r"},
	}
	if err := reg.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	transportErr := &url.Error{
		Op:  "Get",
		URL: "https://api.example.com/v2/search?from=HEL&to=CDG&date=2026-09-01&adults=2",
		Err: errRegistryDeadline{},
	}
	reg.MarkError("leaky", transportErr.Error())

	// Read the bytes actually on disk, not the struct in memory.
	raw, readErr := os.ReadFile(filepath.Join(dir, "leaky.json"))
	if readErr != nil {
		t.Fatalf("reading the persisted provider config: %v", readErr)
	}
	var onDisk ProviderConfig
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("the persisted provider config is not valid JSON: %v", err)
	}

	for _, leaked := range []struct{ needle, what string }{
		{"api.example.com", "the provider host"},
		{"/v2/search", "the request path"},
		{"from=HEL", "the origin airport"},
		{"to=CDG", "the destination airport"},
		{"date=2026-09-01", "the travel date"},
		{"adults=2", "the party size"},
	} {
		if strings.Contains(onDisk.LastError, leaked.needle) {
			t.Errorf("%s (%q) is stored in ~/.trvl/providers/leaky.json and kept until the next "+
				"failure overwrites it, and the same string is rendered into the MCP dashboard. "+
				"This is the user's itinerary at rest.\n  got: %s",
				leaked.what, leaked.needle, onDisk.LastError)
		}
	}

	// The control. LastError drives circuit-breaker status text and the
	// dashboard's error column; blanking it would "fix" the leak by destroying
	// the diagnosis, and an empty column gets the feature removed.
	if onDisk.LastError == "" {
		t.Fatal("LastError was emptied rather than redacted; the dashboard and the circuit-breaker " +
			"status now have nothing to show")
	}
	if !strings.Contains(onDisk.LastError, "url#") {
		t.Errorf("the URL was removed without leaving its fingerprint, so repeated failures against "+
			"the same endpoint can no longer be recognised as the same failure: %s", onDisk.LastError)
	}
	if !strings.Contains(onDisk.LastError, "Get") {
		t.Errorf("the operation was lost with the URL; the record no longer says what was attempted: %s",
			onDisk.LastError)
	}
}

type errRegistryDeadline struct{}

func (errRegistryDeadline) Error() string { return "context deadline exceeded" }
