package hacks

// MIK-3077: tests for the home-stopover detector.

import (
	"strings"
	"testing"
)

func TestDetectHomeStopover_FlagsLayoverAtHomeAirport(t *testing.T) {
	conns := []LayoverConnection{
		{Airport: "AMS", Hours: 18, Carrier: "KL"},
	}
	got := DetectHomeStopover(conns, []string{"AMS", "HEL"}, 120.0)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Kind != HomeStopoverHome {
		t.Errorf("Kind = %q, want %q", got[0].Kind, HomeStopoverHome)
	}
	if got[0].SavingsEUR <= 0 {
		t.Errorf("SavingsEUR = %v, want > 0", got[0].SavingsEUR)
	}
	if !strings.Contains(got[0].Reason, "layover at home") {
		t.Errorf("Reason missing 'layover at home': %q", got[0].Reason)
	}
}

func TestDetectHomeStopover_BelowMinHoursSkipped(t *testing.T) {
	conns := []LayoverConnection{{Airport: "HEL", Hours: 6}}
	if got := DetectHomeStopover(conns, []string{"HEL"}, 100); len(got) != 0 {
		t.Errorf("6h layover should be skipped, got %d", len(got))
	}
}

func TestDetectHomeStopover_AboveMaxHoursSkipped(t *testing.T) {
	conns := []LayoverConnection{{Airport: "HEL", Hours: 72}}
	if got := DetectHomeStopover(conns, []string{"HEL"}, 100); len(got) != 0 {
		t.Errorf("72h layover should be skipped (effective separate trip), got %d", len(got))
	}
}

func TestDetectHomeStopover_PublisherFreeStopover(t *testing.T) {
	conns := []LayoverConnection{
		{Airport: "REK", Hours: 24, Carrier: "FI"},
	}
	got := DetectHomeStopover(conns, []string{"AMS", "HEL"}, 100)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Kind != HomeStopoverPublisher {
		t.Errorf("Kind = %q, want %q", got[0].Kind, HomeStopoverPublisher)
	}
	if !strings.Contains(got[0].Reason, "free stopover") {
		t.Errorf("Reason should mention free stopover: %q", got[0].Reason)
	}
}

func TestDetectHomeStopover_HomeBeatsPublisherWhenBoth(t *testing.T) {
	// AY (Finnair) publishes HEL as a free stopover hub. If HEL is also
	// the user's home airport, we should classify as home (real value).
	conns := []LayoverConnection{{Airport: "HEL", Hours: 18, Carrier: "AY"}}
	got := DetectHomeStopover(conns, []string{"HEL"}, 100)
	if len(got) != 1 || got[0].Kind != HomeStopoverHome {
		t.Errorf("expected home classification, got %+v", got)
	}
}

func TestDetectHomeStopover_NoHomeAirportsNoLayoverAtHomeHits(t *testing.T) {
	conns := []LayoverConnection{{Airport: "AMS", Hours: 18}}
	got := DetectHomeStopover(conns, nil, 100)
	if len(got) != 0 {
		t.Errorf("no home airports should yield no home hits, got %d", len(got))
	}
}

func TestDetectHomeStopover_CaseAndWhitespaceFolded(t *testing.T) {
	conns := []LayoverConnection{{Airport: " ams ", Hours: 18}}
	got := DetectHomeStopover(conns, []string{"AMS"}, 100)
	if len(got) != 1 {
		t.Errorf("case/trim should fold; got %d", len(got))
	}
}

func TestDetectHomeStopover_NegativeAvgHotelClampsToZero(t *testing.T) {
	conns := []LayoverConnection{{Airport: "AMS", Hours: 18}}
	got := DetectHomeStopover(conns, []string{"AMS"}, -50)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].SavingsEUR != 0 {
		t.Errorf("SavingsEUR = %v, want 0 (negative clamped)", got[0].SavingsEUR)
	}
}

func TestNightsCovered(t *testing.T) {
	cases := map[float64]int{
		11.5: 0, // below the 12h threshold
		12:   1,
		18:   1,
		28:   2,
		36:   2,
		48:   2,
	}
	for hours, want := range cases {
		if got := nightsCovered(hours); got != want {
			t.Errorf("nightsCovered(%v) = %d, want %d", hours, got, want)
		}
	}
}

func TestDetectHomeStopover_EmptyInputReturnsNil(t *testing.T) {
	if got := DetectHomeStopover(nil, []string{"AMS"}, 100); got != nil {
		t.Errorf("nil connections should return nil, got %v", got)
	}
}
