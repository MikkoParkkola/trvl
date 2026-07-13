package flights

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func nativeRT(price float64, outDep, inDep string) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: "EUR",
		FareType: models.FareRoundTrip,
		Legs: []models.FlightLeg{
			{Direction: "outbound", DepartureTime: outDep},
			{Direction: "inbound", DepartureTime: inDep},
		},
	}
}

func composedRT(price float64, stops int) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: "EUR",
		Stops:    stops,
		FareType: models.FareSplitTickets,
		Legs:     []models.FlightLeg{{Direction: "outbound", DepartureTime: "2026-07-21T08:00"}},
	}
}

// #472: the cheapest window-compliant native round-trip is retained through
// truncation even when it is priced beyond the cutoff, displacing the most
// expensive kept slot — so it survives for the later window filter.
func TestRetainCompliantNativeRoundTrip_RetainsBeyondCutoff(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(263, "2026-07-21T11:00", "2026-07-25T07:00"), // cheapest, return 07:00 -> non-compliant
		nativeRT(274, "2026-07-21T09:45", "2026-07-25T07:00"), // non-compliant
		composedRT(300, 2),
		composedRT(310, 1),
		nativeRT(290, "2026-07-21T10:15", "2026-07-25T13:55"), // compliant, but pricier -> beyond cutoff
	}
	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 4)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}
	// The compliant €290 fare must be present despite being the 5th cheapest.
	found := false
	for _, f := range got {
		if f.Price == 290 {
			found = true
		}
	}
	if !found {
		t.Errorf("compliant native RT (290) was truncated away: %+v", got)
	}
	// It should occupy the displaced last slot; the €310 composed leaves.
	if got[3].Price != 290 {
		t.Errorf("expected 290 in last slot, got %.0f", got[3].Price)
	}
}

// #472: when a compliant native round-trip already survives the cut, the list is
// a plain cheapest-max truncation (no displacement).
func TestRetainCompliantNativeRoundTrip_AlreadyPresent(t *testing.T) {
	merged := []models.FlightResult{
		nativeRT(263, "2026-07-21T11:00", "2026-07-25T12:00"), // compliant, cheapest
		composedRT(300, 2),
		composedRT(310, 1),
	}
	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 2)
	if len(got) != 2 || got[0].Price != 263 || got[1].Price != 300 {
		t.Errorf("expected plain truncation [263,300], got %+v", got)
	}
}

// #472: with no departure-time window, retention is a plain cheapest-max cut.
func TestRetainCompliantNativeRoundTrip_NoWindow(t *testing.T) {
	merged := []models.FlightResult{
		composedRT(300, 2),
		composedRT(310, 1),
		nativeRT(290, "2026-07-21T05:00", "2026-07-25T05:00"),
	}
	got := retainCompliantNativeRoundTrip(merged, "", "", 2)
	if len(got) != 2 || got[0].Price != 300 || got[1].Price != 310 {
		t.Errorf("no window: expected [300,310], got %+v", got)
	}
}

// #472: nothing to retain when no native fare is window-compliant -> plain cut.
func TestRetainCompliantNativeRoundTrip_NoneCompliant(t *testing.T) {
	merged := []models.FlightResult{
		composedRT(300, 2),
		composedRT(310, 1),
		nativeRT(290, "2026-07-21T11:00", "2026-07-25T07:00"), // return 07:00 -> non-compliant
	}
	got := retainCompliantNativeRoundTrip(merged, "10:00", "", 2)
	if len(got) != 2 || got[0].Price != 300 || got[1].Price != 310 {
		t.Errorf("none compliant: expected plain [300,310], got %+v", got)
	}
}
