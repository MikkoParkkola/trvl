package ground

import (
	"fmt"
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

// ValidProviderNames returns every name the provider filter accepts: the
// launch names in search plus their aliases, sorted.
func ValidProviderNames() []string {
	names := append(MarketedProviderNames(), providerAliases...)
	sort.Strings(names)
	return names
}

// ValidateProviderNames trims and checks a provider filter. Matching is
// case-insensitive, like the filter itself; empty entries are dropped. A name
// outside ValidProviderNames is an error that lists the valid names, so a typo
// never turns into a search that silently runs nothing.
func ValidateProviderNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	valid := ValidProviderNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		known := false
		for _, v := range valid {
			if strings.EqualFold(n, v) {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("no ground provider named %q; valid providers: %s", n, strings.Join(valid, ", "))
		}
		out = append(out, n)
	}
	return out, nil
}

func searchResultBufferCapacity() int {
	// Eurostar fan-out can emit both regular and snap results in parallel.
	return MarketedProviderCount() + 1
}
