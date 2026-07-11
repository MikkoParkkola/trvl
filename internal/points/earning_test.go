package points

import (
	"math"
	"strings"
	"testing"
)

func TestEstimateMilesEarned_FlyingBlue_KL_Economy(t *testing.T) {
	// KL economy, EUR 200 ticket: ~4 miles/EUR = 800 miles
	est := EstimateMilesEarned("AMS", "HEL", "economy", "KL", "skyteam", 200)
	if est.Program != "Flying Blue" {
		t.Errorf("expected programme 'Flying Blue', got %q", est.Program)
	}
	if est.Method != "revenue" {
		t.Errorf("expected method 'revenue', got %q", est.Method)
	}
	if est.Miles != 800 {
		t.Errorf("expected 800 miles, got %d", est.Miles)
	}
}

func TestEstimateMilesEarned_FlyingBlue_KL_Business(t *testing.T) {
	// KL business, EUR 1000 ticket: ~8 miles/EUR = 8000 miles
	est := EstimateMilesEarned("AMS", "NRT", "business", "KL", "skyteam", 1000)
	if est.Miles != 8000 {
		t.Errorf("expected 8000 miles, got %d", est.Miles)
	}
}

func TestEstimateMilesEarned_FlyingBlue_Partner(t *testing.T) {
	// DL (SkyTeam partner) economy, EUR 300: ~2 miles/EUR = 600
	est := EstimateMilesEarned("AMS", "JFK", "economy", "DL", "skyteam", 300)
	if est.Miles != 600 {
		t.Errorf("expected 600 miles, got %d", est.Miles)
	}
	if est.Note == "" {
		t.Error("expected a partner note")
	}
	if !strings.Contains(est.Note, "SkyTeam partner") {
		t.Errorf("expected SkyTeam partner note, got %q", est.Note)
	}
}

func TestEstimateMilesEarned_FlyingBlue_NoPrice(t *testing.T) {
	// No price: falls back to distance-based.
	est := EstimateMilesEarned("AMS", "HEL", "economy", "KL", "skyteam", 0)
	if est.Method != "distance" {
		t.Errorf("expected method 'distance', got %q", est.Method)
	}
	if est.Miles <= 0 {
		t.Errorf("expected positive miles, got %d", est.Miles)
	}
}

func TestEstimateMilesEarned_Oneworld_Economy(t *testing.T) {
	// BA economy HEL->LHR: ~1150 miles * 0.5 = ~575 miles, but min 500
	est := EstimateMilesEarned("HEL", "LHR", "economy", "BA", "oneworld", 0)
	if est.Method != "distance" {
		t.Errorf("expected method 'distance', got %q", est.Method)
	}
	if est.Miles < 500 {
		t.Errorf("expected at least 500 miles (min earning), got %d", est.Miles)
	}
}

func TestEstimateMilesEarned_Oneworld_Business(t *testing.T) {
	// RJ business AMM->LHR: ~3650 km ~= 2268 miles * 1.5 = ~3402
	est := EstimateMilesEarned("AMM", "LHR", "business", "RJ", "oneworld", 0)
	if est.Miles < 2000 {
		t.Errorf("expected > 2000 miles for AMM->LHR business, got %d", est.Miles)
	}
}

func TestEstimateMilesEarned_UnknownAirports(t *testing.T) {
	// Unknown airports: should return 0 miles (can't calculate distance)
	est := EstimateMilesEarned("XXX", "YYY", "economy", "BA", "oneworld", 0)
	if est.Miles != 0 && est.Miles != 500 {
		// With 0 distance, 0*mult = 0 but min-500 only applies if distMiles > 0
		t.Errorf("expected 0 miles for unknown airports, got %d", est.Miles)
	}
}

func TestMilesRedemptionValue(t *testing.T) {
	tests := []struct {
		name     string
		cashEUR  float64
		miles    int
		wantCPM  float64
		wantZero bool
	}{
		{"good redemption", 150, 15000, 1.0, false},
		{"excellent", 300, 15000, 2.0, false},
		{"poor", 50, 15000, 0.333, false},
		{"zero miles", 100, 0, 0, true},
		{"zero price", 0, 15000, 0, true},
		{"negative", -100, 15000, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MilesRedemptionValue(tt.cashEUR, tt.miles)
			if tt.wantZero {
				if got != 0 {
					t.Errorf("expected 0, got %.4f", got)
				}
				return
			}
			if math.Abs(got-tt.wantCPM) > 0.01 {
				t.Errorf("expected ~%.3f cpp, got %.3f", tt.wantCPM, got)
			}
		})
	}
}

func TestIsGoodRedemption(t *testing.T) {
	tests := []struct {
		name     string
		cpp      float64
		alliance string
		want     bool
	}{
		{"skyteam good", 1.5, "skyteam", true},
		{"skyteam bad", 0.8, "skyteam", false},
		{"skyteam borderline", 1.2, "skyteam", false}, // > 1.2, not >=
		{"oneworld good", 2.0, "oneworld", true},
		{"oneworld bad", 1.0, "oneworld", false},
		{"other good", 1.5, "star_alliance", true},
		{"other bad", 1.0, "star_alliance", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGoodRedemption(tt.cpp, tt.alliance)
			if got != tt.want {
				t.Errorf("IsGoodRedemption(%.2f, %q) = %v, want %v", tt.cpp, tt.alliance, got, tt.want)
			}
		})
	}
}

func TestGreatCircleDistanceKm(t *testing.T) {
	// HEL -> AMS is approximately 1509 km
	d := GreatCircleDistanceKm("HEL", "AMS")
	if d < 1400 || d > 1600 {
		t.Errorf("HEL->AMS: expected ~1509 km, got %d", d)
	}

	// Same airport
	d = GreatCircleDistanceKm("HEL", "HEL")
	if d != 0 {
		t.Errorf("HEL->HEL: expected 0, got %d", d)
	}

	// Unknown airport
	d = GreatCircleDistanceKm("HEL", "XXX")
	if d != 0 {
		t.Errorf("HEL->XXX: expected 0, got %d", d)
	}
}

func TestEstimateMilesEarned_FlyingBlue_PremiumEconomy(t *testing.T) {
	// KL premium economy, EUR 500: ~6 miles/EUR = 3000
	est := EstimateMilesEarned("AMS", "NRT", "premium_economy", "KL", "skyteam", 500)
	if est.Miles != 3000 {
		t.Errorf("expected 3000 miles, got %d", est.Miles)
	}
}

func TestEstimateMilesEarned_Oneworld_First(t *testing.T) {
	// QR first AMM->DOH: distance-based, 200% multiplier
	est := EstimateMilesEarned("AMM", "DOH", "first", "QR", "oneworld", 0)
	if est.Miles < 500 {
		t.Errorf("expected at least 500 miles, got %d", est.Miles)
	}
}

func TestProgramNameForOneworld(t *testing.T) {
	tests := map[string]string{
		"BA": "Avios",
		"AY": "Finnair Plus",
		"RJ": "Royal Plus",
		"QR": "Privilege Club",
		"XX": "Oneworld programme",
	}
	for code, want := range tests {
		got := programNameForOneworld(code)
		if got != want {
			t.Errorf("programNameForOneworld(%q) = %q, want %q", code, got, want)
		}
	}
}

// SK SkyTeam earn: SK segment with skyteam alliance earns toward Flying Blue.
func TestEstimateMilesEarned_SK_SkyTeam_FlyingBlue(t *testing.T) {
	est := EstimateMilesEarned("AMS", "CPH", "economy", "SK", "skyteam", 150)
	if est.Program != "Flying Blue" {
		t.Errorf("SK skyteam: Program = %q, want Flying Blue", est.Program)
	}
	if est.Miles <= 0 {
		t.Errorf("SK skyteam: expected positive miles, got %d", est.Miles)
	}
}
