package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// capture redirects stdout for the duration of fn and returns what was written.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

// A nil info must print nothing — callers pass best-effort enrichment straight
// through, so the footer is a no-op when the lookup degraded.
func TestPrintDestinationFooter_NilPrintsNothing(t *testing.T) {
	if out := capture(t, func() { printDestinationFooter(nil) }); out != "" {
		t.Errorf("expected no output for nil info, got %q", out)
	}
}

// An info with no usable signal (every field empty) must also print nothing —
// no bare "Destination:" header with an empty body.
func TestPrintDestinationFooter_EmptyInfoPrintsNothing(t *testing.T) {
	out := capture(t, func() { printDestinationFooter(&models.DestinationInfo{Location: "Nowhere"}) })
	if out != "" {
		t.Errorf("expected no output when no signal present, got %q", out)
	}
}

// A populated info renders the location header plus each present signal line.
func TestPrintDestinationFooter_RendersPresentSignals(t *testing.T) {
	info := &models.DestinationInfo{
		Location: "Paris, France",
		Weather:  models.WeatherInfo{Current: models.WeatherDay{Description: "clear", TempLow: 14, TempHigh: 24}},
		Safety:   models.SafetyInfo{Level: 2, Advisory: "normal caution", Source: "travel-advisory"},
		Holidays: []models.Holiday{{Name: "Bastille Day", Date: "2026-07-14", Type: "public"}},
		Currency: models.CurrencyInfo{LocalCurrency: "EUR", BaseCurrency: "EUR", ExchangeRate: 1},
	}
	out := capture(t, func() { printDestinationFooter(info) })
	for _, want := range []string{"Paris, France", "Weather: clear", "Safety: 2.0/5.0", "Bastille Day", "Currency: 1 EUR"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q\n--- output ---\n%s", want, out)
		}
	}
}
