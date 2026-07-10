package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// filter_mikko_boost_test.go — hits FilterByAamuyo (alias, was 0%), more branches in
// FilterByLoungeAccess / loungeOKForFlight (no-layover pass, zero-min layover, empty airport code, etc).

func TestFilterByAamuyo_IsAlias(t *testing.T) {
	// overnight layover (660m >= 480) + early dep 09:00 < default floor "10:00" -> REJECTED
	reject := mkFlightMikko(100,
		[4]any{"HEL", "AMS", "2026-06-01T22:00", 0},
		[4]any{"AMS", "PRG", "2026-06-02T09:00", 660})
	got := FilterByAamuyo([]models.FlightResult{reject}, "")
	if len(got) != 0 {
		t.Errorf("early 09:00 after overnight layover should be filtered, got %d", len(got))
	}

	// overnight + dep at or after 10:00 -> legitimately PASSES the rule
	pass := mkFlightMikko(110,
		[4]any{"HEL", "AMS", "2026-06-01T22:00", 0},
		[4]any{"AMS", "PRG", "2026-06-02T10:30", 660})
	got = FilterByAamuyo([]models.FlightResult{pass}, "")
	if len(got) != 1 {
		t.Errorf("10:30 after overnight should be kept, got %d", len(got))
	}

	// non-overnight (<480m) should always pass regardless of time (other branch)
	shortLay := mkFlightMikko(120,
		[4]any{"HEL", "AMS", "2026-06-01T22:00", 0},
		[4]any{"AMS", "PRG", "2026-06-02T08:00", 300})
	got = FilterByAamuyo([]models.FlightResult{shortLay}, "")
	if len(got) != 1 {
		t.Errorf("short layover should be kept (no overnight rule), got %d", len(got))
	}
}

func TestFilterByLoungeAccess_Branches(t *testing.T) {
	// flight with NO layovers (first leg only) must pass
	noLay := mkFlightMikko(50, [4]any{"HEL", "LHR", "2026-07-01T10:00", 0})
	got := FilterByLoungeAccess([]models.FlightResult{noLay}, []string{"PP"}, nil)
	if len(got) != 1 {
		t.Error("no-layover flight must pass lounge filter")
	}

	// layover with LayoverMinutes==0 is skipped in check (short conn)
	shortLay := mkFlightMikko(60,
		[4]any{"HEL", "AMS", "2026-07-01T08:00", 0},
		[4]any{"AMS", "LHR", "2026-07-01T08:30", 0})
	got = FilterByLoungeAccess([]models.FlightResult{shortLay}, []string{"PP"}, func(string) map[string]bool { return map[string]bool{} })
	if len(got) != 1 {
		t.Error("zero-min layover leg must not trigger lounge check")
	}

	// empty airport code leg is skipped
	emptyAp := mkFlightMikko(70,
		[4]any{"HEL", "AMS", "2026-07-01T08:00", 0},
		[4]any{"", "LHR", "2026-07-01T09:00", 30})
	got = FilterByLoungeAccess([]models.FlightResult{emptyAp}, []string{"PP"}, func(string) map[string]bool { return map[string]bool{} })
	if len(got) != 1 {
		t.Error("empty airport code must not drop")
	}

	// no user cards falls back to default and query
	q := func(ap string) map[string]bool {
		if ap == "AMS" {
			return map[string]bool{"Priority Pass": true}
		}
		return map[string]bool{}
	}
	layAMS := mkFlightMikko(80,
		[4]any{"HEL", "AMS", "2026-07-01T08:00", 0},
		[4]any{"AMS", "LHR", "2026-07-01T10:00", 60})
	got = FilterByLoungeAccess([]models.FlightResult{layAMS}, nil /*empty->default*/, q)
	if len(got) != 1 {
		t.Error("default cards + coverage should pass")
	}

	// mismatch drops
	got = FilterByLoungeAccess([]models.FlightResult{layAMS}, []string{"FB"}, q)
	if len(got) != 0 {
		t.Error("no matching card should drop")
	}
}

func TestLoungeOKForFlight_Direct(t *testing.T) {
	f := mkFlightMikko(90,
		[4]any{"HEL", "CDG", "2026-07-01T08:00", 0},
		[4]any{"CDG", "LHR", "2026-07-01T11:00", 120})
	cards := map[string]bool{"LoungeKey": true}
	q := func(ap string) map[string]bool { return map[string]bool{"LoungeKey": ap == "CDG"} }
	if !loungeOKForFlight(f, cards, q) {
		t.Error("loungeOK direct should pass when covered")
	}
}
