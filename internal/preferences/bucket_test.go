package preferences

import "testing"

func TestMatchesBucketRegions(t *testing.T) {
	if !MatchesBucket("Reykjavik", "KEF", []string{"Iceland"}) {
		t.Fatal("Iceland should match Reykjavik")
	}
	if !MatchesBucket("Split", "", []string{"Balkan"}) {
		t.Fatal("Balkan should match Split")
	}
	if MatchesBucket("Brussels", "BRU", []string{"Iceland", "Balkans"}) {
		t.Fatal("Brussels is not on the Iceland or Balkans list")
	}
	if !MatchesBucket("Tokyo", "NRT", []string{"NRT"}) {
		t.Fatal("airport code should match")
	}
	if !MatchesBucket("Reykjavík", "", []string{"Iceland"}) || !MatchesBucket("Keflavík", "", []string{"Iceland"}) {
		t.Fatal("accented Icelandic names should match Iceland")
	}
}

// TestFold pins Fold, which ground's Ferryhopper gate also uses: it lowercases,
// strips accents and trims, and keeps the inner spaces and brackets that the
// gate splits names on.
func TestFold(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Reykjavík", "reykjavik"},
		{"TRAVEMÜNDE", "travemunde"},
		{"  Świnoujście  ", "swinoujscie"},
		{"THIRA (SANTORINI)", "thira (santorini)"},
		{"HELSINKI - VUOSAARI\r", "helsinki - vuosaari"},
		{"", ""},
	} {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
