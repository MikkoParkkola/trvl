package transfer

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// annotateProsCons fills Pros/Cons for every option from STRUCTURED SIGNALS
// only (price ratios, change count, mode, amenities) — never free-text opinion.
// This keeps the comparison honest and reproducible. Options with unknown
// price AND duration (e.g. ride-hail deep-links) keep their pre-set pros/cons
// and are excluded from the cheapest/fastest comparison.
func annotateProsCons(opts []models.TransferOption) {
	if len(opts) == 0 {
		return
	}
	// Cheapest/fastest computed over priced+timed options only.
	cheapest, fastest := 0.0, 0
	for _, o := range opts {
		if o.TotalPrice > 0 && (cheapest == 0 || o.TotalPrice < cheapest) {
			cheapest = o.TotalPrice
		}
		if o.DoorToDoorMin > 0 && (fastest == 0 || o.DoorToDoorMin < fastest) {
			fastest = o.DoorToDoorMin
		}
	}

	for i := range opts {
		o := &opts[i]
		// Skip options with unknown price AND duration (ride-hail deep-links):
		// they carry their own pre-set pros/cons; do not overwrite or rank them.
		if o.TotalPrice <= 0 && o.DoorToDoorMin <= 0 {
			continue
		}
		var pros, cons []string

		if o.TotalPrice > 0 && o.TotalPrice <= cheapest {
			pros = append(pros, "cheapest option")
		} else if cheapest > 0 && o.TotalPrice >= cheapest*2 {
			cons = append(cons, fmt.Sprintf("%.1fx the cheapest option", o.TotalPrice/cheapest))
		}

		if o.DoorToDoorMin > 0 && o.DoorToDoorMin <= fastest {
			pros = append(pros, "fastest option")
		}

		switch o.Mode {
		case "taxi", "private_transfer":
			pros = append(pros, "door-to-door, no luggage hassle")
		case "airport_express":
			pros = append(pros, "direct, frequent")
		}

		switch {
		case o.Changes == 0 && o.Mode != "taxi" && o.Mode != "private_transfer":
			pros = append(pros, "no changes")
		case o.Changes == 1:
			cons = append(cons, "1 change")
		case o.Changes >= 2:
			cons = append(cons, fmt.Sprintf("%d changes, harder with luggage", o.Changes))
		}

		if o.Mode == "private_transfer" {
			cons = append(cons, "must book ahead")
		}
		if o.PriceIsEstimate {
			cons = append(cons, "price is an estimate")
		}

		o.Pros = dedupe(pros)
		o.Cons = dedupe(cons)
	}
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
