package mcp

// Compatibility-alias accounting. This is the single source of truth for the
// "1 smart tool + N compatibility aliases" numbers cited in docs and tests.
// Counting from a live server keeps the number honest: it can never drift from
// the actual registered handler surface.

// nonAliasCapabilities are handlers reachable via the `travel` router intent
// (or by direct tools/call) that are NOT legacy per-domain compatibility
// aliases, so they must not inflate the marketed alias count. Keep this in sync
// with registerTools in tools.go.
var nonAliasCapabilities = map[string]bool{
	"travel":           true, // the smart router itself
	"plan_journey":     true, // Leave-By Scheduler capability (MIK-5734 B)
	"plan_natural":     true, // NL travel-planner supertool (innovation #7)
	"arbitrage_report": true, // unified arbitrage aggregator (innovation #8)
	"plan_multimodal":  true, // Multimodal composer capability (innovation #2)
	"coalesce_trip":    true, // unified trip coalescer (innovation #5)
	"travel_nudges":    true, // personal travel-graph nudges — new capability via the router intent, not a legacy alias
}

// AdvertisedToolCount returns the number of tools advertised in tools/list by
// default. The smart-router design advertises exactly one tool (`travel`); the
// legacy surface is opt-in via TRVL_MCP_TOOL_MODE=legacy.
func AdvertisedToolCount() int {
	return len(NewServer().tools)
}

// CompatibilityAliasCount returns the number of legacy per-domain tool names
// that remain callable as compatibility aliases (via the `travel` router's
// intent field or direct tools/call), excluding the smart router and the
// newer non-alias smart capabilities. Docs and marketing copy that need the
// exact number derive it from this function rather than hand-typing it.
func CompatibilityAliasCount() int {
	count := 0
	for name := range NewServer().handlers {
		if !nonAliasCapabilities[name] {
			count++
		}
	}
	return count
}
