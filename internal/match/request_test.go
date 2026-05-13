package match

import (
	"testing"
)

func TestExactMatch(t *testing.T) {
	req := Request{
		OriginIATA:       "HEL",
		DestIATA:         "BCN",
		DepartDateCenter: "2026-07-01",
		FlexDays:         0,
		Nights:           7,
		MaxNightsDrift:   0,
		PreferDirect:     false,
		Currency:         "EUR",
	}
	off := Offered{
		OriginIATA: "HEL",
		DestIATA:   "BCN",
		DepartDate: "2026-07-01",
		ReturnDate: "2026-07-08",
		Stops:      0,
		Currency:   "EUR",
	}
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100, got %d", s.Total)
	}
	if s.Reason != "exact_match" {
		t.Errorf("expected exact_match, got %s", s.Reason)
	}
}

func TestDateDriftWithinFlex(t *testing.T) {
	req := Request{
		DepartDateCenter: "2026-07-01",
		FlexDays:         3,
	}
	off := Offered{DepartDate: "2026-07-03"} // 2 days drift, within flex
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 (within flex), got %d", s.Total)
	}
}

func TestDateDriftOutsideFlex(t *testing.T) {
	req := Request{
		DepartDateCenter: "2026-07-01",
		FlexDays:         1,
	}
	off := Offered{DepartDate: "2026-07-04"} // 3 days drift → 2 outside flex → 10 pts
	s := Compute(req, off)
	if s.Total != 90 {
		t.Errorf("expected 90, got %d (reason: %s)", s.Total, s.Reason)
	}
	if s.Reason != "date_drift" {
		t.Errorf("expected date_drift reason, got %s", s.Reason)
	}
}

func TestDateDriftMaxPenalty(t *testing.T) {
	req := Request{
		DepartDateCenter: "2026-07-01",
		FlexDays:         0,
	}
	// 10 days drift → 50pts but capped at 40
	off := Offered{DepartDate: "2026-07-11"}
	s := Compute(req, off)
	if s.Total != 60 {
		t.Errorf("expected 60 (100-40), got %d", s.Total)
	}
}

func TestAirportSubstitutionExact(t *testing.T) {
	req := Request{DestIATA: "BCN"}
	off := Offered{DestIATA: "BCN"}
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 for exact dest match, got %d", s.Total)
	}
}

func TestAirportSubstitutionNotAccepted(t *testing.T) {
	req := Request{DestIATA: "BCN", AcceptedAirports: []string{"GRO"}}
	off := Offered{DestIATA: "REU"} // not in accepted list
	s := Compute(req, off)
	if s.Total != 80 {
		t.Errorf("expected 80 (20pt penalty), got %d", s.Total)
	}
	if s.Reason != "airport_substitution" {
		t.Errorf("expected airport_substitution, got %s", s.Reason)
	}
}

func TestAirportSubstitutionAccepted(t *testing.T) {
	req := Request{DestIATA: "BCN", AcceptedAirports: []string{"GRO", "REU"}}
	off := Offered{DestIATA: "GRO"} // in accepted list
	s := Compute(req, off)
	if s.Total != 90 {
		t.Errorf("expected 90 (10pt penalty), got %d", s.Total)
	}
}

func TestNightsDriftExact(t *testing.T) {
	req := Request{Nights: 7, MaxNightsDrift: 0, DepartDateCenter: ""}
	off := Offered{DepartDate: "2026-07-01", ReturnDate: "2026-07-08"} // 7 nights
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 for exact nights, got %d", s.Total)
	}
}

func TestNightsDriftPenalty(t *testing.T) {
	req := Request{Nights: 7, MaxNightsDrift: 0}
	off := Offered{DepartDate: "2026-07-01", ReturnDate: "2026-07-11"} // 10 nights → 3 drift → 15pts
	s := Compute(req, off)
	if s.Total != 85 {
		t.Errorf("expected 85, got %d", s.Total)
	}
	if s.Reason != "nights_drift" {
		t.Errorf("expected nights_drift, got %s", s.Reason)
	}
}

func TestCurrencyMismatch(t *testing.T) {
	req := Request{Currency: "EUR"}
	off := Offered{Currency: "USD"}
	s := Compute(req, off)
	if s.Total != 90 {
		t.Errorf("expected 90 (10pt currency penalty), got %d", s.Total)
	}
	if s.Reason != "currency_mismatch" {
		t.Errorf("expected currency_mismatch, got %s", s.Reason)
	}
}

func TestCurrencyMatchNopenalty(t *testing.T) {
	req := Request{Currency: "EUR"}
	off := Offered{Currency: "EUR"}
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 for matching currency, got %d", s.Total)
	}
}

func TestDirectPreference(t *testing.T) {
	req := Request{PreferDirect: true}
	off := Offered{Stops: 1}
	s := Compute(req, off)
	if s.Total != 90 {
		t.Errorf("expected 90 (10pt direct penalty), got %d", s.Total)
	}
	if s.Reason != "direct_preference" {
		t.Errorf("expected direct_preference, got %s", s.Reason)
	}
}

func TestDirectPreferenceNoStops(t *testing.T) {
	req := Request{PreferDirect: true}
	off := Offered{Stops: 0}
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 for direct flight when preferred, got %d", s.Total)
	}
}

func TestStackedPenalties(t *testing.T) {
	req := Request{
		DestIATA:         "BCN",
		DepartDateCenter: "2026-07-01",
		FlexDays:         0,
		Nights:           7,
		MaxNightsDrift:   0,
		PreferDirect:     true,
		Currency:         "EUR",
	}
	off := Offered{
		DestIATA:   "GRO",        // not in AcceptedAirports → 20pts
		DepartDate: "2026-07-05", // 4 days drift → 20pts
		ReturnDate: "2026-07-16", // 11 nights vs 7 → 4 drift → 20pts
		Stops:      1,            // +10pts
		Currency:   "USD",        // +10pts
	}
	// Total penalty: 20+20+20+10+10 = 80 → score = 20
	s := Compute(req, off)
	if s.Total != 20 {
		t.Errorf("expected 20 for stacked penalties, got %d (reason: %s)", s.Total, s.Reason)
	}
}

func TestDestAnyEmpty(t *testing.T) {
	// Empty DestIATA means "any" — no airport substitution penalty.
	req := Request{DestIATA: ""}
	off := Offered{DestIATA: "TXL"}
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 for empty dest (any), got %d", s.Total)
	}
}

func TestNightsZeroAny(t *testing.T) {
	// Nights=0 means "any" — no nights drift penalty.
	req := Request{Nights: 0}
	off := Offered{DepartDate: "2026-07-01", ReturnDate: "2026-07-30"} // 29 nights
	s := Compute(req, off)
	if s.Total != 100 {
		t.Errorf("expected 100 when nights=0 (any), got %d", s.Total)
	}
}
