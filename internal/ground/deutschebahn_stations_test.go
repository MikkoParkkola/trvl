package ground

import (
	"encoding/json"
	"testing"
)

// dbFinalHalts returns, for every journey in a raw DB fahrplan body, the stop
// parseDBVerbindungen reports as its arrival station (dbFinalHalt).
func dbFinalHalts(t *testing.T, body []byte) []dbOrt {
	t.Helper()
	var resp dbJourneysResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode fahrplan: %v", err)
	}
	var out []dbOrt
	for _, v := range resp.Verbindungen {
		abschnitte := v.VerbindungsAbschnitte
		if v.Verbindung != nil && len(v.Verbindung.VerbindungsAbschnitte) > 0 {
			abschnitte = v.Verbindung.VerbindungsAbschnitte
		}
		if end := dbFinalHalt(abschnitte); end != nil {
			out = append(out, *end)
		}
	}
	return out
}

// TestDBStationTableMatchesCaptures audits dbStations against dated captures
// of DB journey searches to each id: every journey must end at a halt with the
// table's EVA and name. The 8101003 capture is the reproduction: DB sends
// journeys for that id on to Wien Blumental.
func TestDBStationTableMatchesCaptures(t *testing.T) {
	for _, tc := range []struct{ city, capture string }{
		{"vienna", "db_fahrplan_praha_8103000_2026-11-15.json"},
		{"wien", "db_fahrplan_praha_8103000_2026-11-15.json"},
		{"warsaw", "db_fahrplan_berlin_5100065_2026-11-15.json"},
		{"krakow", "db_fahrplan_berlin_5100028_2026-11-15.json"},
		{"kraków", "db_fahrplan_berlin_5100028_2026-11-15.json"},
	} {
		t.Run(tc.city, func(t *testing.T) {
			station, ok := LookupDBStation(tc.city)
			if !ok {
				t.Fatalf("no DB station for %q", tc.city)
			}
			halts := dbFinalHalts(t, readFixture(t, tc.capture))
			if len(halts) == 0 {
				t.Fatal("capture has no journeys")
			}
			for _, h := range halts {
				if h.EvaNr != station.EVA || h.Name != station.Name {
					t.Errorf("journey ends at %q (%s), table has %q (%s)", h.Name, h.EvaNr, station.Name, station.EVA)
				}
			}
		})
	}

	halts := dbFinalHalts(t, readFixture(t, "db_fahrplan_praha_8101003_2026-11-15.json"))
	if len(halts) == 0 {
		t.Fatal("8101003 capture has no journeys")
	}
	for _, h := range halts {
		if h.Name != "Wien Blumental" || h.EvaNr != "8101003" {
			t.Errorf("8101003 capture ends at %q (%s), want Wien Blumental: the reproduction no longer holds", h.Name, h.EvaNr)
		}
	}
}
