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
}
