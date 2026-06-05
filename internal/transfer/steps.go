package transfer

import (
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer/airportkb"
)

// AssembleSteps builds grounded, numbered instructions for one transfer option.
//
// Grounding contract (the anti-hallucination core): a Step is marked
// Grounded=true ONLY when it is derived from real route legs or the curated
// airport KB. Anything else is Grounded=false and MUST be rendered
// "(estimated)" by callers. This function never synthesizes a terminal, line,
// or sign that is absent from its inputs.
func AssembleSteps(r models.GroundRoute, mode string, profile *airportkb.Profile) []models.Step {
	steps := make([]models.Step, 0, len(r.Legs)+2)
	order := 1

	// 1. Curated airport exit/ticket snippet (grounded) when available.
	if profile != nil {
		if mode == "taxi" && profile.TaxiCaveats != "" {
			steps = append(steps, models.Step{Order: order, Text: profile.TaxiCaveats, Grounded: true})
			order++
		} else if snippet, ok := profile.ExitSnippets[mode]; ok && snippet != "" {
			steps = append(steps, models.Step{Order: order, Text: snippet, Grounded: true})
			order++
		}
	}

	// 2. Per-leg skeleton from real route data (grounded).
	if len(r.Legs) > 0 {
		for _, leg := range r.Legs {
			steps = append(steps, models.Step{
				Order:    order,
				Text:     legText(leg),
				Grounded: true,
				DurMin:   leg.Duration,
			})
			order++
		}
	} else if mode != "taxi" && mode != "private_transfer" {
		// Single-segment route with no leg breakdown: still grounded from
		// departure/arrival stops + total duration.
		steps = append(steps, models.Step{
			Order:    order,
			Text:     singleSegmentText(r),
			Grounded: true,
			DurMin:   r.Duration,
		})
		order++
	}

	// 3. Taxi/private with no KB caveat: one honest, generic (estimated) step.
	if (mode == "taxi" || mode == "private_transfer") && len(steps) == 0 {
		steps = append(steps, models.Step{
			Order:    order,
			Text:     "Take a taxi from the official rank to your destination; confirm the fare or that the meter is running before departing.",
			Grounded: false,
		})
	}

	return steps
}

func legText(leg models.GroundLeg) string {
	from := stopLabel(leg.Departure)
	to := stopLabel(leg.Arrival)
	carrier := leg.Provider
	if carrier == "" {
		carrier = strings.TrimSpace(leg.Type)
	}
	if carrier == "" {
		return fmt.Sprintf("Travel from %s to %s (%d min).", from, to, leg.Duration)
	}
	return fmt.Sprintf("Take the %s from %s to %s (%d min).", carrier, from, to, leg.Duration)
}

func singleSegmentText(r models.GroundRoute) string {
	from := stopLabel(r.Departure)
	to := stopLabel(r.Arrival)
	carrier := r.Provider
	if carrier == "" {
		carrier = strings.TrimSpace(r.Type)
	}
	if carrier == "" {
		return fmt.Sprintf("Travel from %s to %s (%d min).", from, to, r.Duration)
	}
	return fmt.Sprintf("Take the %s from %s to %s (%d min).", carrier, from, to, r.Duration)
}

func stopLabel(s models.GroundStop) string {
	switch {
	case s.Station != "" && s.City != "":
		return fmt.Sprintf("%s (%s)", s.Station, s.City)
	case s.Station != "":
		return s.Station
	case s.City != "":
		return s.City
	default:
		return "the stop"
	}
}
