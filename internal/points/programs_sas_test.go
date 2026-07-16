package points

import (
	"strings"
	"testing"
)

// TestNoStaleEuroBonusProgram guards issue #466: SAS (SK) joined SkyTeam in 2024
// and its SkyTeam earning currency is Flying Blue, not the retired EuroBonus
// mapping. The loyalty catalog must not model SAS as a "SAS EuroBonus" program.
func TestNoStaleEuroBonusProgram(t *testing.T) {
	if p := LookupProgram("sas-eurobonus"); p != nil {
		t.Errorf("stale program %q still present: %+v", "sas-eurobonus", p)
	}
	for _, prog := range Programs {
		if strings.Contains(strings.ToLower(prog.Name), "eurobonus") {
			t.Errorf("stale EuroBonus program in catalog: %q (%s)", prog.Name, prog.Slug)
		}
		if strings.Contains(strings.ToLower(prog.Slug), "eurobonus") {
			t.Errorf("stale EuroBonus slug in catalog: %q", prog.Slug)
		}
	}
	// Flying Blue must exist as the SkyTeam currency SAS members earn into.
	if p := LookupProgram("flying-blue"); p == nil {
		t.Fatal("expected flying-blue program to exist as SAS/SkyTeam earning currency")
	}
}
