package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestClassifyEnrichment locks the typed-status contract for PLANCOMP.3: every
// source degrades to an honest label and the function never panics on nil. All
// fixtures are inline — no network, fully deterministic.
func TestClassifyEnrichment(t *testing.T) {
	full := &models.DestinationInfo{
		Weather:  models.WeatherInfo{Forecast: []models.WeatherDay{{Date: "2026-07-01", TempHigh: 30}}},
		Holidays: []models.Holiday{{Name: "Festa Major", Date: "2026-07-02"}},
	}
	noWeather := &models.DestinationInfo{
		Holidays: []models.Holiday{{Name: "Festa Major", Date: "2026-07-02"}},
	}
	noHolidays := &models.DestinationInfo{
		Weather: models.WeatherInfo{Forecast: []models.WeatherDay{{Date: "2026-07-01"}}},
	}
	events := []models.Event{{Name: "Primavera Sound"}}

	cases := []struct {
		name        string
		info        *models.DestinationInfo
		events      []models.Event
		keyOn       bool
		wantWeather string
		wantHoliday string
		wantEvent   string
	}{
		{"all present, key on", full, events, true, enrichOK, enrichOK, enrichOK},
		{"feed unreachable", nil, nil, true, enrichUnavailable, enrichUnavailable, enrichNone},
		{"no key configured", full, nil, false, enrichOK, enrichOK, enrichNotConfigured},
		{"weather missing only", noWeather, nil, false, enrichUnavailable, enrichOK, enrichNotConfigured},
		{"holidays empty window", noHolidays, nil, true, enrichOK, enrichNone, enrichNone},
		{"key on but no events found", full, nil, true, enrichOK, enrichOK, enrichNone},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyEnrichment(tc.info, tc.events, tc.keyOn)
			if got.WeatherStatus != tc.wantWeather {
				t.Errorf("WeatherStatus = %q, want %q", got.WeatherStatus, tc.wantWeather)
			}
			if got.HolidayStatus != tc.wantHoliday {
				t.Errorf("HolidayStatus = %q, want %q", got.HolidayStatus, tc.wantHoliday)
			}
			if got.EventStatus != tc.wantEvent {
				t.Errorf("EventStatus = %q, want %q", got.EventStatus, tc.wantEvent)
			}
		})
	}

	// A successful weather classification must carry the payload through, not
	// just flip the status flag.
	got := classifyEnrichment(full, events, true)
	if len(got.Weather.Forecast) != 1 || len(got.Holidays) != 1 || len(got.Events) != 1 {
		t.Errorf("payload not carried: weather=%d holidays=%d events=%d",
			len(got.Weather.Forecast), len(got.Holidays), len(got.Events))
	}
}
