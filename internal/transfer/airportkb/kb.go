// Package airportkb provides curated, per-airport ground-transfer knowledge
// (recommended arrival buffers, exit/ticket snippets, taxi caveats, last
// public-transport departure). This data grounds the step-by-step instructions
// and the Leave-By Scheduler so trvl never invents a terminal, sign, or buffer.
//
// Profiles are embedded JSON under data/. Coverage grows incrementally (top
// airports by traffic) with NO code change — add a data/<CODE>.json file.
package airportkb

import (
	"embed"
	"encoding/json"
	"strings"
)

//go:embed data/*.json
var profiles embed.FS

// Profile is the curated knowledge for one airport. Every field is hand-curated
// and treated as ground truth; absence is honest (empty => "unknown", callers
// fall back to a labelled estimate).
type Profile struct {
	Code              string            `json:"code"`
	Name              string            `json:"name"`
	IntlBufferMin     int               `json:"intl_buffer_min"`
	DomesticBufferMin int               `json:"domestic_buffer_min"`
	LastTransitDepart string            `json:"last_transit_depart,omitempty"` // local "HH:MM", "" if unknown
	ExitSnippets      map[string]string `json:"exit_snippets,omitempty"`       // mode -> grounded "exit T1, follow A1 signs"
	TaxiCaveats       string            `json:"taxi_caveats,omitempty"`
}

// Lookup returns the curated profile for an IATA airport code (case-insensitive)
// and whether it was found. A missing profile is not an error: callers degrade
// to conservative defaults and labelled-estimate steps.
func Lookup(code string) (Profile, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return Profile{}, false
	}
	raw, err := profiles.ReadFile("data/" + code + ".json")
	if err != nil {
		return Profile{}, false
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, false
	}
	return p, true
}

// Codes lists every airport code with a curated profile (for coverage tests).
func Codes() []string {
	entries, err := profiles.ReadDir("data")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			out = append(out, strings.TrimSuffix(name, ".json"))
		}
	}
	return out
}
