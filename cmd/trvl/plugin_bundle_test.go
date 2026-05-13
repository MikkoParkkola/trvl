package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMIK3400PluginBundle(t *testing.T) {
	root := filepath.Join("..", "..", "plugin")

	manifestBytes := readPluginFile(t, root, ".claude-plugin", "plugin.json")
	var manifest struct {
		Name       string   `json:"name"`
		Version    string   `json:"version"`
		Skills     string   `json:"skills"`
		Commands   string   `json:"commands"`
		Agents     []string `json:"agents"`
		MCPServers string   `json:"mcpServers"`
		Keywords   []string `json:"keywords"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("plugin manifest must be valid JSON: %v", err)
	}
	if manifest.Name != "trvl" {
		t.Fatalf("plugin name = %q, want trvl", manifest.Name)
	}
	if manifest.Version == "" {
		t.Fatal("plugin manifest must declare a version")
	}
	assertJSONPath(t, "skills", manifest.Skills, "./skills/")
	assertJSONPath(t, "commands", manifest.Commands, "./commands/")
	assertJSONPathIn(t, "agents", manifest.Agents, "./agents/trip-coordinator.md")
	assertJSONPath(t, "mcpServers", manifest.MCPServers, "./.mcp.json")
	if !containsString(manifest.Keywords, "travel") || !containsString(manifest.Keywords, "mcp") {
		t.Fatalf("plugin keywords = %v, want travel and mcp", manifest.Keywords)
	}

	skillExpectations := map[string][]string{
		filepath.Join("skills", "trvl-trip-planner", "SKILL.md"): {
			"name: trvl-trip-planner",
			"trip plan",
			"plan trip",
			"vacation",
			"holiday",
			"weekend getaway",
			"plan_trip",
			"search_flights",
			"search_hotels",
			"search_hotels_with_details",
			"assess_trip",
		},
		filepath.Join("skills", "trvl-price-watch", "SKILL.md"): {
			"name: trvl-price-watch",
			"price watch",
			"monitor flights",
			"hotel deal alert",
			"watch_price",
			"watch_room_availability",
			"watch_opportunities",
			"/loop-compatible",
		},
		filepath.Join("skills", "trvl-destination-research", "SKILL.md"): {
			"name: trvl-destination-research",
			"destination research",
			"what to do in",
			"things to see in",
			"destination_info",
			"travel_guide",
			"local_events",
			"nearby_places",
			"check_visa",
		},
	}
	for rel, want := range skillExpectations {
		assertFileContainsAll(t, root, rel, want...)
	}

	assertFileContainsAll(t, root, filepath.Join("commands", "trvl.md"),
		"/trvl",
		"trvl-trip-planner",
		"trvl-price-watch",
		"trvl-destination-research",
		"falls back to MCP tool selection",
	)
	assertFileContainsAll(t, root, filepath.Join("agents", "trip-coordinator.md"),
		"name: trip-coordinator",
		"plan_flight_bundle",
		"optimize_multi_city",
		"add_trip_leg",
		"detect_travel_hacks",
	)
	assertFileContainsAll(t, root, ".mcp.json",
		"mcpServers",
		"trvl",
		"mcp",
	)
	assertFileContainsAll(t, root, "README.md",
		"claude plugin install",
		"brew install MikkoParkkola/tap/trvl",
		"/trvl plan",
		"/trvl price-watch",
		"/trvl destination-research",
		"43 underlying tools",
		"1 smart MCP tool plus 63 compatibility aliases",
	)
}

func readPluginFile(t *testing.T, root string, parts ...string) []byte {
	t.Helper()
	pathParts := append([]string{root}, parts...)
	b, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		t.Fatalf("read plugin file %s: %v", filepath.Join(pathParts...), err)
	}
	return b
}

func assertFileContainsAll(t *testing.T, root string, rel string, want ...string) {
	t.Helper()
	content := string(readPluginFile(t, root, rel))
	for _, needle := range want {
		if !strings.Contains(content, needle) {
			t.Fatalf("%s missing %q", filepath.Join(root, rel), needle)
		}
	}
}

func assertJSONPath(t *testing.T, field, got, want string) {
	t.Helper()
	normalizedGot := strings.TrimSuffix(got, "/")
	normalizedWant := strings.TrimSuffix(want, "/")
	if normalizedGot != normalizedWant {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}

func assertJSONPathIn(t *testing.T, field string, got []string, want string) {
	t.Helper()
	normalizedWant := strings.TrimSuffix(want, "/")
	for _, value := range got {
		if strings.TrimSuffix(value, "/") == normalizedWant {
			return
		}
	}
	t.Fatalf("%s = %v, want to include %q", field, got, want)
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
