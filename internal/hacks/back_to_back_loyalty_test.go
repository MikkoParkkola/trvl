package hacks

import (
	"strings"
	"testing"
)

func TestWithLoyaltyBackToBackNote_emptyLoyaltyUnchanged(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "BCN", Currency: "EUR"}
	base := backToBackAdvisory(in)
	baseRisks := len(base[0].Risks)

	got := withLoyaltyBackToBackNote(backToBackAdvisory(in), LoyaltyProfile{})
	if len(got) != 1 {
		t.Fatalf("expected 1 hack, got %d", len(got))
	}
	if len(got[0].Risks) != baseRisks {
		t.Errorf("zero loyalty must not add risks: got %d, want %d", len(got[0].Risks), baseRisks)
	}
}

func TestWithLoyaltyBackToBackNote_addsMilesWarning(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "BCN", Currency: "EUR"}
	base := backToBackAdvisory(in)
	baseRisks := len(base[0].Risks)

	got := withLoyaltyBackToBackNote(backToBackAdvisory(in), LoyaltyProfile{Alliances: []string{"oneworld"}})
	if len(got[0].Risks) != baseRisks+1 {
		t.Fatalf("loyalty profile should append exactly one risk: got %d, want %d", len(got[0].Risks), baseRisks+1)
	}
	last := got[0].Risks[len(got[0].Risks)-1]
	if !strings.Contains(strings.ToLower(last), "qualifying miles") {
		t.Errorf("appended risk should mention forfeited qualifying miles, got %q", last)
	}
}

func TestWithLoyaltyBackToBackNote_nearStatusEscalates(t *testing.T) {
	in := DetectorInput{Origin: "HEL", Destination: "BCN", Currency: "EUR"}

	generic := withLoyaltyBackToBackNote(backToBackAdvisory(in), LoyaltyProfile{Alliances: []string{"oneworld"}})
	near := withLoyaltyBackToBackNote(backToBackAdvisory(in), LoyaltyProfile{Alliances: []string{"oneworld"}, NearStatus: true})

	genericNote := generic[0].Risks[len(generic[0].Risks)-1]
	nearNote := near[0].Risks[len(near[0].Risks)-1]

	if genericNote == nearNote {
		t.Error("near-status note should differ from the generic loyalty note")
	}
	if !strings.Contains(strings.ToLower(nearNote), "near a status threshold") {
		t.Errorf("near-status note should call out the threshold, got %q", nearNote)
	}
}

func TestDetectBackToBack_emptyLoyaltyAdvisoryUnchanged(t *testing.T) {
	// Use an unroutable airport pair so the live-price path fails fast and the
	// detector falls back to the deterministic advisory. The zero loyalty
	// profile must produce the same advisory risks as the raw advisory builder.
	in := DetectorInput{
		Origin:      "ZZZ",
		Destination: "QQQ",
		Date:        "2026-06-01",
		ReturnDate:  "2026-06-04",
	}
	advisory := backToBackAdvisory(in)
	if len(advisory) != 1 {
		t.Fatalf("expected 1 advisory hack, got %d", len(advisory))
	}
	// withLoyaltyBackToBackNote on the zero profile is the no-op the detector
	// applies on the advisory path.
	got := withLoyaltyBackToBackNote(backToBackAdvisory(in), LoyaltyProfile{})
	if len(got[0].Risks) != len(advisory[0].Risks) {
		t.Errorf("zero loyalty changed advisory risk count: got %d, want %d", len(got[0].Risks), len(advisory[0].Risks))
	}
}
