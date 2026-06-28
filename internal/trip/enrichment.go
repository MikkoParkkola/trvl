package trip

import (
	"github.com/MikkoParkkola/trvl/internal/models"
)

// Per-package enrichment for MIK-6530 (PLANCOMP.3).
//
// Each plan is annotated with the weather forecast and public holidays for its
// exact date window, plus events when the opt-in key is configured. The promise
// is "surface the surprises" — a heat wave, a public holiday that closes
// everything, a festival spiking prices. The hard contract: enrichment NEVER
// fails or blocks a plan. Every source carries a typed status so a missing
// feed degrades to an honest label instead of a hard error or a silent gap.

// Enrichment status values. Stable strings so the renderer and API consumers
// can branch on them.
const (
	enrichOK            = "ok"             // data present
	enrichNone          = "none"           // fetched cleanly, nothing in the window
	enrichUnavailable   = "unavailable"    // feed could not be reached; degrade, don't fail
	enrichNotConfigured = "not_configured" // opt-in source with no key set
)

// PackageEnrichment is the typed, never-failing enrichment attached to a plan.
type PackageEnrichment struct {
	Weather       models.WeatherInfo `json:"weather,omitempty"`
	WeatherStatus string             `json:"weather_status"`
	Holidays      []models.Holiday   `json:"holidays,omitempty"`
	HolidayStatus string             `json:"holiday_status"`
	Events        []models.Event     `json:"events,omitempty"`
	EventStatus   string             `json:"event_status"`
}

// classifyEnrichment turns best-effort fetch results into a typed status block.
// Pure and deterministic: the live fetchers run elsewhere and pass their
// (possibly nil) results in here, so this is fully unit-testable with fixtures
// and never touches the network.
//
//   - info == nil means the weather/holiday feed could not be reached at all.
//   - eventsKeyConfigured distinguishes "opt-in source switched off" from an
//     empty-but-configured result.
func classifyEnrichment(info *models.DestinationInfo, events []models.Event, eventsKeyConfigured bool) PackageEnrichment {
	e := PackageEnrichment{
		WeatherStatus: enrichUnavailable,
		HolidayStatus: enrichUnavailable,
		EventStatus:   enrichNotConfigured,
	}

	if info != nil {
		if len(info.Weather.Forecast) > 0 {
			e.Weather = info.Weather
			e.WeatherStatus = enrichOK
		}
		// Holidays reached cleanly: empty window is a real answer ("none"), not a
		// failure.
		e.Holidays = info.Holidays
		if len(info.Holidays) > 0 {
			e.HolidayStatus = enrichOK
		} else {
			e.HolidayStatus = enrichNone
		}
	}

	if eventsKeyConfigured {
		e.Events = events
		if len(events) > 0 {
			e.EventStatus = enrichOK
		} else {
			e.EventStatus = enrichNone
		}
	}

	return e
}
