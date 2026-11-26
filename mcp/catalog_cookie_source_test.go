package mcp

import "testing"

// Every catalog skeleton's cookie source must be a value the provider runtime
// accepts (#844): a skeleton carrying anything else would be refused by
// Validate, or read as a closed door, the moment a user configured it.
// Skeletons mirror configure_provider's arguments, which take the source as the
// flat "cookies_source" (tools_providers.go); a nested cookies.source is
// checked too. configure_provider fills an empty source with "preflight".
func TestCatalogSkeletonCookieSourcesAreAllowed(t *testing.T) {
	allowed := map[string]bool{"": true, "none": true, "preflight": true, "browser": true}
	for _, p := range availableProviders {
		sources := map[string]string{}
		if s, ok := p.ConfigSkeleton["cookies_source"].(string); ok {
			sources["cookies_source"] = s
		}
		if c, ok := p.ConfigSkeleton["cookies"].(map[string]any); ok {
			if s, ok := c["source"].(string); ok {
				sources["cookies.source"] = s
			}
		}
		for key, source := range sources {
			if !allowed[source] {
				t.Errorf("catalog %s skeleton sets %s %q", p.ID, key, source)
			}
		}
	}
}
