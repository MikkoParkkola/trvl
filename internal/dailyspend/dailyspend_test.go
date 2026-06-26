package dailyspend

import "testing"

func TestLookupKnownCity(t *testing.T) {
	got := Lookup("Bangkok")
	if got.Tier != "budget" {
		t.Errorf("Bangkok should be budget tier, got %q", got.Tier)
	}
	if got.Fallback {
		t.Error("a mapped city must not be marked Fallback")
	}
	if got.PerPersonPerDay != 25 {
		t.Errorf("budget tier should be 25 EUR/day, got %v", got.PerPersonPerDay)
	}
	if got.Currency != "EUR" {
		t.Errorf("currency should be EUR, got %q", got.Currency)
	}
	if got.Source == "" {
		t.Error("every estimate must carry a Source so it reads as an estimate")
	}
}

func TestLookupCaseAndWhitespaceInsensitive(t *testing.T) {
	a := Lookup("  London ")
	if a.Tier != "premium" || a.Fallback {
		t.Errorf("London should resolve premium without fallback, got tier=%q fallback=%v", a.Tier, a.Fallback)
	}
}

func TestLookupCountryFallbackFromCityCountryString(t *testing.T) {
	// "Krabi" is not a mapped city, but Thailand is a mapped country.
	got := Lookup("Krabi, Thailand")
	if got.Tier != "budget" {
		t.Errorf("Krabi, Thailand should resolve via country=Thailand to budget, got %q", got.Tier)
	}
	if got.Fallback {
		t.Error("a country match is a real match, not a fallback")
	}
}

func TestLookupUnknownIsLabelledFallback(t *testing.T) {
	got := Lookup("Atlantis")
	if !got.Fallback {
		t.Error("an unknown destination MUST be marked Fallback so it is never read as destination-specific")
	}
	if got.Tier != "moderate" {
		t.Errorf("default tier should be moderate, got %q", got.Tier)
	}
	if got.PerPersonPerDay <= 0 {
		t.Error("fallback estimate must still be a usable positive number")
	}
}

func TestTotalScalesByGuestsAndNights(t *testing.T) {
	e := Estimate{PerPersonPerDay: 45}
	if got := e.Total(2, 3); got != 270 {
		t.Errorf("2 guests x 3 nights x 45 = 270, got %v", got)
	}
	// A same-day or party-less plan has no daily spend.
	if got := e.Total(2, 0); got != 0 {
		t.Errorf("zero nights should add nothing, got %v", got)
	}
	if got := e.Total(0, 3); got != 0 {
		t.Errorf("zero guests should add nothing, got %v", got)
	}
	if got := e.Total(-1, 3); got != 0 {
		t.Errorf("negative guests should add nothing, got %v", got)
	}
}

// TestDatasetIntegrity guards the embedded JSON at build/test time: every city
// and country must map to a real tier, so a typo in data.json fails here rather
// than silently degrading a live plan to the fallback.
func TestDatasetIntegrity(t *testing.T) {
	for city, tier := range idx.Cities {
		if _, ok := idx.Tiers[tier]; !ok {
			t.Errorf("city %q maps to unknown tier %q", city, tier)
		}
	}
	for country, tier := range idx.Countries {
		if _, ok := idx.Tiers[tier]; !ok {
			t.Errorf("country %q maps to unknown tier %q", country, tier)
		}
	}
}
