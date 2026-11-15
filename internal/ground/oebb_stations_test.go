package ground

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// oebbLocMatchRequest builds a HAFAS station lookup in the same envelope as
// oebbTripSearchRequest.
func oebbLocMatchRequest(name string) map[string]any {
	env := oebbTripSearchRequest("", "", "", "")
	env["svcReqL"] = []map[string]any{{
		"meth": "LocMatch",
		"req": map[string]any{"input": map[string]any{
			"field":  "S",
			"loc":    map[string]any{"name": name, "type": "S"},
			"maxLoc": 5,
		}},
	}}
	return env
}

// oebbFirstLocMatch returns the best match of a raw HAFAS LocMatch body.
func oebbFirstLocMatch(t *testing.T, body []byte) oebbLoc {
	t.Helper()
	var env struct {
		SvcResL []struct {
			Err string `json:"err"`
			Res struct {
				Match struct {
					LocL []oebbLoc `json:"locL"`
				} `json:"match"`
			} `json:"res"`
		} `json:"svcResL"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode LocMatch: %v", err)
	}
	if len(env.SvcResL) == 0 || env.SvcResL[0].Err != "OK" || len(env.SvcResL[0].Res.Match.LocL) == 0 {
		t.Fatalf("LocMatch has no match: %s", body)
	}
	return env.SvcResL[0].Res.Match.LocL[0]
}

// TestOebbStationTableMatchesCaptures audits the Polish entries of
// oebbStations against dated captures of ÖBB's own lookups: HAFAS LocMatch
// for ExtID and the shop station search for Number.
func TestOebbStationTableMatchesCaptures(t *testing.T) {
	for _, tc := range []struct {
		city, slug, name string
	}{
		{"warsaw", "warszawa", "Warszawa Centralna"},
		{"krakow", "krakow", "Krakow Glowny"},
		{"kraków", "krakow", "Krakow Glowny"},
	} {
		t.Run(tc.city, func(t *testing.T) {
			station, ok := LookupOebbStation(tc.city)
			if !ok {
				t.Fatalf("no oebb station for %q", tc.city)
			}

			body, err := os.ReadFile(filepath.Join("testdata", "oebb_hafas_locmatch_"+tc.slug+"_2026-11-15.json"))
			if err != nil {
				t.Fatal(err)
			}
			loc := oebbFirstLocMatch(t, body)
			if loc.Name != tc.name {
				t.Fatalf("capture's first match is %q, want %q", loc.Name, tc.name)
			}
			if station.ExtID != loc.ExtID {
				t.Errorf("ExtID = %s, HAFAS has %q as %s", station.ExtID, loc.Name, loc.ExtID)
			}

			body, err = os.ReadFile(filepath.Join("testdata", "oebb_shop_stations_"+tc.slug+"_2026-11-15.json"))
			if err != nil {
				t.Fatal(err)
			}
			var shop []struct {
				Name   string `json:"name"`
				Number int    `json:"number"`
			}
			if err := json.Unmarshal(body, &shop); err != nil || len(shop) == 0 {
				t.Fatalf("decode shop stations: %v", err)
			}
			if shop[0].Name != tc.name {
				t.Fatalf("shop capture's first match is %q, want %q", shop[0].Name, tc.name)
			}
			if station.Number != shop[0].Number {
				t.Errorf("Number = %d, shop has %q as %d", station.Number, shop[0].Name, shop[0].Number)
			}
		})
	}
}
