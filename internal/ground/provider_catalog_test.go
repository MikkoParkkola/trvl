package ground

import "testing"

func TestMarketedProviderNamesRemainUnique(t *testing.T) {
	names := MarketedProviderNames()
	if len(names) != 21 {
		t.Fatalf("marketed provider count = %d, want 21", len(names))
	}

	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			t.Fatalf("duplicate marketed provider %q", name)
		}
		seen[name] = struct{}{}
	}
}

func TestSearchResultBufferCapacityTracksProviderCatalog(t *testing.T) {
	if got, want := searchResultBufferCapacity(), MarketedProviderCount()+1; got != want {
		t.Fatalf("search result buffer capacity = %d, want %d", got, want)
	}
}
