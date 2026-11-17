package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// The "not known" flags are additive: false leaves today's JSON exactly as it
// was (no key), true adds one key, and the count keeps its own field.
func TestTransfersFlagsSerializeOnlyWhenSet(t *testing.T) {
	for _, c := range []struct {
		name          string
		clear, set    any
		key, countKey string
	}{
		{"ground route", GroundRoute{Provider: "oebb"}, GroundRoute{Provider: "oebb", TransfersUnknown: true}, "transfers_unknown", `"transfers":0`},
		{"route leg", RouteLeg{Mode: "train"}, RouteLeg{Mode: "train", TransfersUnknown: true}, "transfers_unknown", `"transfers":0`},
		{"itinerary", RouteItinerary{}, RouteItinerary{TransfersUnknown: true}, "transfers_unknown", `"transfers":0`},
		{"transfer option", TransferOption{Mode: "train"}, TransferOption{Mode: "train", ChangesUnknown: true}, "changes_unknown", `"changes":0`},
	} {
		t.Run(c.name, func(t *testing.T) {
			clear, err := json.Marshal(c.clear)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(clear), c.key) || !strings.Contains(string(clear), c.countKey) {
				t.Errorf("flag false encodes as %s, want %s and no %q key", clear, c.countKey, c.key)
			}
			set, err := json.Marshal(c.set)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(set), `"`+c.key+`":true`) {
				t.Errorf("flag true encodes as %s, want %q true", set, c.key)
			}
		})
	}
}
