package ground

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	locs := oebbLocs(t, body)
	if len(locs) == 0 {
		t.Fatalf("LocMatch has no match: %s", body)
	}
	return locs[0]
}

// oebbLocs returns every station a raw HAFAS body names: LocMatch matches
// and TripSearch stops alike. A service error fails the test.
func oebbLocs(t *testing.T, body []byte) []oebbLoc {
	t.Helper()
	var env struct {
		SvcResL []struct {
			Err string `json:"err"`
			Res struct {
				Match struct {
					LocL []oebbLoc `json:"locL"`
				} `json:"match"`
				Common struct {
					LocL []oebbLoc `json:"locL"`
				} `json:"common"`
			} `json:"res"`
		} `json:"svcResL"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode HAFAS body: %v", err)
	}
	if len(env.SvcResL) == 0 || env.SvcResL[0].Err != "OK" {
		t.Fatalf("HAFAS service error: %s", body)
	}
	res := env.SvcResL[0].Res
	return append(res.Match.LocL, res.Common.LocL...)
}

// upstreamStationName drops a trailing qualifier such as " (U)" or
// " (Bahnsteige 3-12)": ÖBB names the same station with and without one.
func upstreamStationName(s string) string {
	if i := strings.Index(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		return s[:i]
	}
	return s
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestOebbStationTableMatchesCaptures audits oebbStations against dated
// captures of ÖBB's own data: each table ExtID must appear in a HAFAS capture
// and each Number in a shop station search, under the table's station name.
func TestOebbStationTableMatchesCaptures(t *testing.T) {
	for _, tc := range []struct{ city, hafas, shop string }{
		// HAFAS LocMatch never lists 8103000 by name; the trip search capture
		// names it as the Wien Hbf arrival platforms.
		{"vienna", oebbTripFixture, "oebb_shop_stations_wien_2026-11-15.json"},
		{"warsaw", "oebb_hafas_locmatch_warszawa_2026-11-15.json", "oebb_shop_stations_warszawa_2026-11-15.json"},
		{"krakow", "oebb_hafas_locmatch_krakow_2026-11-15.json", "oebb_shop_stations_krakow_2026-11-15.json"},
		{"kraków", "oebb_hafas_locmatch_krakow_2026-11-15.json", "oebb_shop_stations_krakow_2026-11-15.json"},
	} {
		t.Run(tc.city, func(t *testing.T) {
			station, ok := LookupOebbStation(tc.city)
			if !ok {
				t.Fatalf("no oebb station for %q", tc.city)
			}

			hafasName, found := "", false
			for _, l := range oebbLocs(t, readFixture(t, tc.hafas)) {
				if l.ExtID == station.ExtID {
					hafasName, found = l.Name, true
					break
				}
			}
			switch {
			case !found:
				t.Errorf("ExtID %s is not in %s", station.ExtID, tc.hafas)
			case upstreamStationName(hafasName) != station.Name:
				t.Errorf("ExtID %s is %q in HAFAS, table says %q", station.ExtID, hafasName, station.Name)
			}

			var shop []struct {
				Name   string `json:"name"`
				Number int    `json:"number"`
			}
			if err := json.Unmarshal(readFixture(t, tc.shop), &shop); err != nil {
				t.Fatalf("decode shop stations: %v", err)
			}
			shopName, found := "", false
			for _, s := range shop {
				if s.Number == station.Number {
					shopName, found = s.Name, true
					break
				}
			}
			switch {
			case !found:
				t.Errorf("Number %d is not in %s", station.Number, tc.shop)
			case upstreamStationName(shopName) != station.Name:
				t.Errorf("Number %d is %q in the shop, table says %q", station.Number, shopName, station.Name)
			}
		})
	}
}
