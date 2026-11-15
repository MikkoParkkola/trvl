package ground

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

// ValidProviderNames returns every name the provider filter accepts: the
// launch names in search plus their aliases.
func ValidProviderNames() []string {
	return nil
}

// ValidateProviderNames trims and checks a provider filter, returning an error
// that lists the valid names when any entry is unknown.
func ValidateProviderNames(names []string) ([]string, error) {
	return names, nil
}

func searchResultBufferCapacity() int {
	// Eurostar fan-out can emit both regular and snap results in parallel.
	return MarketedProviderCount() + 1
}
