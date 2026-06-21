package multimodal

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func legs(modes ...string) []models.GroundLeg {
	out := make([]models.GroundLeg, len(modes))
	for i, m := range modes {
		out[i] = models.GroundLeg{Type: m, Provider: "rome2rio"}
	}
	return out
}

func TestResolveLegMode(t *testing.T) {
	cases := []struct {
		name       string
		disc       string
		route      models.GroundRoute
		wantMode   string
		wantDetail string
	}{
		{
			name:     "ferry stays ferry, never relabeled as a bus",
			disc:     "ferry",
			route:    models.GroundRoute{Type: "ferry", Legs: legs("ferry")},
			wantMode: "ferry", wantDetail: "",
		},
		{
			// A coach that boards a ferry (Helsinki–Tallinn Lux Express style):
			// the bookable vehicle is a bus, but the ferry crossing must show.
			name:     "coach-on-ferry discloses the ferry segment",
			disc:     "bus",
			route:    models.GroundRoute{Type: "bus", Legs: legs("bus", "ferry")},
			wantMode: "bus", wantDetail: "via bus + ferry",
		},
		{
			name:     "plain bus has no spurious disclosure",
			disc:     "bus",
			route:    models.GroundRoute{Type: "bus", Legs: legs("bus")},
			wantMode: "bus", wantDetail: "",
		},
		{
			name:     "concrete discovered mode wins over a contradictory provider type",
			disc:     "train",
			route:    models.GroundRoute{Type: "bus", Legs: legs("bus")},
			wantMode: "train", wantDetail: "",
		},
		{
			name:     "non-concrete mode falls back to the leg breakdown",
			disc:     "mixed",
			route:    models.GroundRoute{Type: "", Legs: legs("train", "fly")},
			wantMode: "train", wantDetail: "via train + fly",
		},
		{
			name:     "empty mode with no legs falls back to route type",
			disc:     "",
			route:    models.GroundRoute{Type: "ferry"},
			wantMode: "ferry", wantDetail: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, detail := resolveLegMode(tc.disc, tc.route)
			if mode != tc.wantMode || detail != tc.wantDetail {
				t.Errorf("resolveLegMode(%q) = (%q, %q), want (%q, %q)",
					tc.disc, mode, detail, tc.wantMode, tc.wantDetail)
			}
		})
	}
}

func TestDistinctLegModes(t *testing.T) {
	got := distinctLegModes(legs("bus", "bus", "ferry", "ferry"))
	want := []string{"bus", "ferry"}
	if len(got) != len(want) {
		t.Fatalf("distinctLegModes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("distinctLegModes = %v, want %v", got, want)
		}
	}
	if m := distinctLegModes(nil); len(m) != 0 {
		t.Errorf("distinctLegModes(nil) = %v, want empty", m)
	}
}
