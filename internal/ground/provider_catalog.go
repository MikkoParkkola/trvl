package ground

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

var marketedProviderNames = []string{
	"flixbus",
	"regiojet",
	"eurostar",
	"db",
	"oebb",
	"ns",
	"vr",
	"sncf",
	"trainline",
	"transitous",
	"renfe",
	"trenitalia",
	"italo",
	"european_sleeper",
	"snalltaget",
	"tallink",
	"vikingline",
	"eckeroline",
	"finnlines",
	"stenaline",
	"dfds",
	"ferryhopper",
}

// MarketedProviderNames returns the user-facing ground-provider catalog used by
// public docs and claims. The returned slice is a copy so callers cannot mutate
// the package-level source of truth.
func MarketedProviderNames() []string {
	return append([]string(nil), marketedProviderNames...)
}

// MarketedProviderCount returns the user-facing ground-provider count.
func MarketedProviderCount() int {
	return len(marketedProviderNames)
}

// providerAliases are filter names accepted beyond the marketed catalog:
// providers search launches that are not marketed, the Snap fare search, and
// the API name behind VR.
var providerAliases = []string{"distribusion", "rome2rio", "eurostar snap", "digitransit"}

// providerCanonical maps an accepted alternative name to the launch id it
// names, so status rows and cache keys use one id per provider.
var providerCanonical = map[string]string{"digitransit": "vr"}

// ValidProviderNames returns every name the provider filter accepts: the
// launch names in search plus their aliases, sorted.
func ValidProviderNames() []string {
	names := append(MarketedProviderNames(), providerAliases...)
	sort.Strings(names)
	return names
}

// launchProviderIDs returns the canonical id of every provider search can
// launch, sorted: the valid names without the alternative spellings.
func launchProviderIDs() []string {
	var ids []string
	for _, n := range ValidProviderNames() {
		if _, alias := providerCanonical[n]; !alias {
			ids = append(ids, n)
		}
	}
	return ids
}

// ValidateProviderNames trims, checks and canonicalises a provider filter:
// matching is case-insensitive, an alternative name resolves to its launch id,
// and empty entries and duplicates are dropped. A name outside
// ValidProviderNames is an error that lists the valid names, so a typo never
// turns into a search that silently runs nothing.
func ValidateProviderNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	valid := ValidProviderNames()
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if !slices.Contains(valid, n) {
			return nil, fmt.Errorf("no ground provider named %q; valid providers: %s", n, strings.Join(valid, ", "))
		}
		if canon, ok := providerCanonical[n]; ok {
			n = canon
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func searchResultBufferCapacity() int {
	// Eurostar fan-out can emit both regular and snap results in parallel.
	return MarketedProviderCount() + 1
}
