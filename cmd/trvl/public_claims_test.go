package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/trip"
	trvlmcp "github.com/MikkoParkkola/trvl/mcp"
)

// cliCommandCountMarketed is the marketed CLI command count. It excludes
// meta/utility commands (version, providers, help, completion) that are
// not user-facing search or action commands. Update this constant when
// adding new user-facing commands to the CLI.
// See root.go init() for the full list of registered commands.
// The dynamic count from rootCmd.Commands() includes all cobra-registered
// commands, but marketing materials only count functional travel commands.
// Current exclusions: version, providers (both are utility/meta commands).
const cliCommandCountMarketed = 50

var readmeToolMarkers = []string{
	"travel",
	"search_flights",
	"search_dates",
	"search_hotels",
	"search_hotels_with_details",
	"hotel_prices",
	"hotel_reviews",
	"hotel_rooms",
	"destination_info",
	"calculate_trip_cost",
	"weekend_getaway",
	"suggest_dates",
	"optimize_multi_city",
	"nearby_places",
	"travel_guide",
	"local_events",
	"search_ground",
	"search_airport_transfers",
	"search_restaurants",
	"search_deals",
	"plan_trip",
	"search_route",
	"get_preferences",
	"detect_travel_hacks",
	"detect_accommodation_hacks",
	"search_natural",
	"list_trips",
	"get_trip",
	"create_trip",
	"add_trip_leg",
	"mark_trip_booked",
	"get_weather",
	"get_baggage_rules",
	"find_trip_window",
	"search_lounges",
	"check_visa",
	"calculate_points_value",
	"search_awards",
}

func bundledSkillMarkdownCount(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join("..", "..", ".claude", "skills"))
	if err != nil {
		t.Fatalf("ReadDir(.claude/skills): %v", err)
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			count++
		}
	}
	return count
}

func marketedGroundProviderCount() int {
	return ground.MarketedProviderCount()
}

func marketedTransportProviderCount() int {
	seen := make(map[string]struct{})
	for _, provider := range ground.MarketedProviderNames() {
		seen[provider] = struct{}{}
	}
	for _, provider := range trip.MarketedAdditionalProviderNames() {
		seen[provider] = struct{}{}
	}
	return len(seen)
}

func registeredMCPToolCount(t *testing.T) int {
	t.Helper()

	serverValue := reflect.ValueOf(trvlmcp.NewServer())
	if serverValue.Kind() != reflect.Pointer || serverValue.IsNil() {
		t.Fatal("mcp.NewServer should return a non-nil server pointer")
	}

	tools := serverValue.Elem().FieldByName("tools")
	if !tools.IsValid() {
		t.Fatal("mcp.Server should expose an internal tools field")
	}

	return tools.Len()
}

func registeredMCPCompatibilityAliasCount(t *testing.T) int {
	t.Helper()

	serverValue := reflect.ValueOf(trvlmcp.NewServer())
	if serverValue.Kind() != reflect.Pointer || serverValue.IsNil() {
		t.Fatal("mcp.NewServer should return a non-nil server pointer")
	}

	handlers := serverValue.Elem().FieldByName("handlers")
	if !handlers.IsValid() {
		t.Fatal("mcp.Server should expose an internal handlers field")
	}

	return handlers.Len() - 1 // Exclude the primary travel smart router.
}

func TestPublicDocsAdvertiseCurrentCounts(t *testing.T) {
	t.Parallel()

	toolCount := registeredMCPToolCount(t)
	compatAliasCount := registeredMCPCompatibilityAliasCount(t)
	// Use marketed count (excludes version, providers, help, completion).
	// See cliCommandCountMarketed constant for rationale.
	cliCommandCount := cliCommandCountMarketed
	watchSubcommandCount := len(watchCmd().Commands())
	skillCount := bundledSkillMarkdownCount(t)
	groundProviderCount := marketedGroundProviderCount()
	totalProviderCount := marketedTransportProviderCount()

	checks := []struct {
		path      string
		optional  bool
		required  []string
		forbidden []string
	}{
		{
			path: filepath.Join("..", "..", "README.md"),
			required: []string{
				fmt.Sprintf("https://img.shields.io/badge/providers-%d-brightgreen", totalProviderCount),
				fmt.Sprintf("%d smart MCP tool for your AI assistant", toolCount),
				fmt.Sprintf("%d compatibility aliases", compatAliasCount),
				fmt.Sprintf("standalone CLI with %d commands", cliCommandCount),
				fmt.Sprintf("%d smart travel tool available", toolCount),
				fmt.Sprintf("Full v2025-11-25 — %d smart MCP tool, %d compatibility aliases", toolCount, compatAliasCount),
				fmt.Sprintf("%d commands (+ %d watch subcommands)", cliCommandCount, watchSubcommandCount),
				fmt.Sprintf("Full JSON Schema validation for the `travel` smart router and all %d compatibility tool responses", compatAliasCount),
				"install the bundled skill that teaches Claude how to use trvl",
				fmt.Sprintf("trvl searches %d ground transport providers in parallel, covering most of Europe. Airport transfers add taxi estimates on top of that, so trvl exposes %d transport providers overall:", groundProviderCount, totalProviderCount),
				fmt.Sprintf("Searches %d providers in parallel:", groundProviderCount),
			},
			forbidden: []string{
				"https://img.shields.io/badge/providers-16-brightgreen",
				"31 travel tools for your AI assistant",
				"62 travel tools for your AI assistant",
				"standalone CLI with 31 commands",
				"31 travel tools available",
				"62 travel tools available",
				"Full v2025-11-25 — 31 tools",
				"Full v2025-11-25 — 62 tools",
				"31 commands (+ 6 watch subcommands)",
				"Full JSON Schema validation for all 31 tool responses",
				"Full JSON Schema validation for all 62 tool responses",
				"29 travel tools for your AI assistant",
				"standalone CLI with 29 commands",
				"29 travel tools available",
				"Full v2025-11-25 — 29 tools",
				"29 commands (+ 6 watch subcommands)",
				"Full JSON Schema validation for all 29 tool responses",
				"The repo includes 4 skill files",
			},
		},
		{
			path: filepath.Join("..", "..", "AGENTS.md"),
			required: []string{
				fmt.Sprintf("trvl is installed with %d smart MCP tool, %d compatibility aliases, and %d bundled Claude skill", toolCount, compatAliasCount, skillCount),
				fmt.Sprintf("| `display_currency` | Price display across the smart router and all %d compatibility aliases |", compatAliasCount),
				"### search_lounges — Find airport lounges at a given airport",
				"### check_visa — Check visa requirements for a passport→destination pair",
				"### calculate_points_value — Compare points vs cash for a redemption",
			},
			forbidden: []string{
				"trvl is installed with 32 MCP tools and 5 skills",
				"trvl is installed with 32 MCP tools and 4 skills",
				"trvl is installed with 31 MCP tools and 5 skills",
				"trvl is installed with 31 MCP tools and 4 skills",
				"installed with 22 MCP tools and 5 skills",
				"trvl is installed with 62 MCP tools",
				"You now have 22 MCP tools available.",
				"You now have 62 MCP tools available.",
				"| `display_currency` | Price display across all 35 tools |",
			},
		},
		{
			path: filepath.Join("..", "..", "demo.tape"),
			required: []string{
				fmt.Sprintf("# %d smart MCP tool · %d aliases · %d CLI commands · %d providers · No API keys", toolCount, compatAliasCount, cliCommandCount, totalProviderCount),
			},
			forbidden: []string{
				"# 31 MCP tools · 31 CLI commands · 17 providers · No API keys",
				"# 29 MCP tools · 29 CLI commands · 17 providers · No API keys",
				"# 62 MCP tools",
			},
		},
		{
			path: filepath.Join("..", "..", ".claude-plugin", "plugin.json"),
			required: []string{
				fmt.Sprintf("%d smart MCP tool", toolCount),
				fmt.Sprintf("%d compatibility aliases", compatAliasCount),
			},
			forbidden: []string{
				"16 MCP tools",
				"31 MCP tools",
				"62 MCP tools",
			},
		},
		{
			path: filepath.Join("..", "..", ".claude", "skills", "trvl.md"),
			required: []string{
				fmt.Sprintf("## CORE TOOL ROUTING (primary `travel` tool + %d compatibility aliases)", compatAliasCount),
				fmt.Sprintf("Bus/train/ferry (%d providers)", groundProviderCount),
				"`travel`",
				"`search_airport_transfers`",
				"`plan_trip`",
				"`search_route`",
				"`get_weather`",
				"`get_baggage_rules`",
			},
			forbidden: []string{
				"## TOOLS (via gateway_invoke server=\"trvl\")",
				"trvl exposes 31 MCP tools overall",
				"trvl exposes 62 MCP tools overall",
				"Bus/train (6 providers)",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "ARCHITECTURE.md"),
			required: []string{
				fmt.Sprintf("Bus + train + ferry search (%d providers in parallel)", groundProviderCount),
				fmt.Sprintf("max(all %d providers)", groundProviderCount),
			},
			forbidden: []string{
				"Bus + train + ferry search (11 providers in parallel)",
				"max(all 11 providers)",
			},
		},
		{
			path:     filepath.Join("..", "..", "docs", "ROADMAP.md"),
			optional: true,
			required: []string{
				fmt.Sprintf("## Current: %d providers", groundProviderCount),
			},
			forbidden: []string{
				"## Current: 11 providers",
			},
		},
	}

	for _, check := range checks {
		check := check
		t.Run(filepath.Base(check.path), func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(check.path)
			if err != nil {
				if os.IsNotExist(err) && check.optional {
					t.Skipf("optional public doc %q not present", check.path)
				}
				t.Fatalf("ReadFile(%q): %v", check.path, err)
			}
			text := string(data)

			for _, needle := range check.required {
				if !strings.Contains(text, needle) {
					t.Errorf("%s missing required text %q", check.path, needle)
				}
			}
			for _, needle := range check.forbidden {
				if strings.Contains(text, needle) {
					t.Errorf("%s still contains stale text %q", check.path, needle)
				}
			}

			if filepath.Base(check.path) == "README.md" {
				for _, tool := range readmeToolMarkers {
					marker := fmt.Sprintf("**%s**", tool)
					if count := strings.Count(text, marker); count != 1 {
						t.Errorf("%s should mention %s exactly once in the MCP tool table, got %d", check.path, marker, count)
					}
				}
			}
		})
	}
}
