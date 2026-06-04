package airportkb

import "testing"

func TestLookup_Success(t *testing.T) {
	p, ok := Lookup("BCN")
	if !ok {
		t.Fatal("BCN profile should be found")
	}
	if p.IntlBufferMin <= p.DomesticBufferMin {
		t.Errorf("intl buffer (%d) should exceed domestic (%d)", p.IntlBufferMin, p.DomesticBufferMin)
	}
	if p.TaxiCaveats == "" {
		t.Errorf("BCN should carry a taxi caveat")
	}
}

func TestLookup_CaseInsensitive(t *testing.T) {
	if _, ok := Lookup("bcn"); !ok {
		t.Errorf("lookup should be case-insensitive")
	}
}

func TestLookup_Missing(t *testing.T) {
	if _, ok := Lookup("ZZZ"); ok {
		t.Errorf("unknown code must return found=false, not an error")
	}
	if _, ok := Lookup(""); ok {
		t.Errorf("empty code must return found=false")
	}
}

func TestCodes_NonEmpty(t *testing.T) {
	codes := Codes()
	if len(codes) < 12 {
		t.Errorf("expected the expanded major-hub profile set (>=12), got %d: %v", len(codes), codes)
	}
	// Every listed code must load.
	for _, c := range codes {
		if _, ok := Lookup(c); !ok {
			t.Errorf("listed code %q failed to load", c)
		}
	}
}
