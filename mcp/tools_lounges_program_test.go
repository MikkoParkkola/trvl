package mcp

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// TestFfStatusToCards_SKisFlyingBlue guards the airlineProgramNames correction
// in tools_lounges.go: SAS (SK) now maps to "Flying Blue" (SkyTeam), not the
// retired "EuroBonus". ffStatusToCards must emit the SK carrier card under the
// new program name so lounge-access matching stays correct.
func TestFfStatusToCards_SKisFlyingBlue(t *testing.T) {
	cards := ffStatusToCards([]preferences.FrequentFlyerStatus{
		{Alliance: "skyteam", Tier: "gold", AirlineCode: "SK"},
	})

	var flyingBlue, euroBonus bool
	for _, c := range cards {
		if strings.HasPrefix(c, "Flying Blue ") {
			flyingBlue = true
		}
		if strings.Contains(c, "EuroBonus") {
			euroBonus = true
		}
	}
	if !flyingBlue {
		t.Errorf("expected an SK card under \"Flying Blue\", got %v", cards)
	}
	if euroBonus {
		t.Errorf("SK must no longer map to EuroBonus, got %v", cards)
	}
}
