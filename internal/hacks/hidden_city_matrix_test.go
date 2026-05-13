package hacks

// MIK-3078: tests for the hidden-city matrix expander.

import (
	"strings"
	"testing"
)

func sampleOffers() []HiddenCityOffer {
	return []HiddenCityOffer{
		{Origin: "AMS", Hub: "HEL", HubBeyond: "RIX", Carrier: "AY", Price: 180, Currency: "EUR", CarryOnOnly: true, LayoverMinutes: 120},
		{Origin: "BRU", Hub: "HEL", HubBeyond: "TLL", Carrier: "AY", Price: 220, Currency: "EUR", CarryOnOnly: true, LayoverMinutes: 90},
		{Origin: "AMS", Hub: "FRA", HubBeyond: "VIE", Carrier: "LH", Price: 160, Currency: "EUR", CarryOnOnly: false, LayoverMinutes: 75},
		{Origin: "AMS", Hub: "CDG", HubBeyond: "MRS", Carrier: "AF", Price: 195, Currency: "EUR", CarryOnOnly: true, LayoverMinutes: 50, SeparateTickets: true},
	}
}

func TestExpandMatrix_AllowFalseReturnsNil(t *testing.T) {
	got := ExpandMatrix(sampleOffers(), MatrixOptions{AllowHiddenCity: false})
	if got != nil {
		t.Errorf("AllowHiddenCity=false should yield nil, got %v", got)
	}
}

func TestExpandMatrix_RanksByPriceAscending(t *testing.T) {
	got := ExpandMatrix(sampleOffers(), MatrixOptions{
		AllowHiddenCity: true, MaxLayoverRisk: 100, TopK: 4,
	})
	if len(got) < 2 {
		t.Fatalf("got %d, want >= 2", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Price > got[i].Price {
			t.Errorf("not sorted ascending: %v then %v", got[i-1].Price, got[i].Price)
		}
	}
}

func TestExpandMatrix_TopKDefaultsTo3(t *testing.T) {
	got := ExpandMatrix(sampleOffers(), MatrixOptions{AllowHiddenCity: true, MaxLayoverRisk: 100})
	if len(got) > 3 {
		t.Errorf("expected default TopK=3, got %d", len(got))
	}
}

func TestExpandMatrix_RiskGateDropsHighRiskOffers(t *testing.T) {
	// All offers risk-gated to <=10 → only the AMS->HEL->RIX 120-min
	// carry-on offer survives (risk = 0).
	got := ExpandMatrix(sampleOffers(), MatrixOptions{
		AllowHiddenCity: true, MaxLayoverRisk: 10, TopK: 4,
	})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 surviving offer at MaxLayoverRisk=10", len(got))
	}
	if got[0].Hub != "HEL" {
		t.Errorf("Hub = %q, want HEL", got[0].Hub)
	}
}

func TestExpandMatrix_DegenerateOffersRejected(t *testing.T) {
	got := ExpandMatrix([]HiddenCityOffer{
		{Origin: "AMS", Hub: "HEL", HubBeyond: "HEL", Carrier: "AY", Price: 100, CarryOnOnly: true, LayoverMinutes: 120}, // Hub = HubBeyond
		{Origin: "", Hub: "HEL", HubBeyond: "RIX", Carrier: "AY", Price: 100, CarryOnOnly: true, LayoverMinutes: 120},    // empty origin
		{Origin: "AMS", Hub: "HEL", HubBeyond: "RIX", Carrier: "AY", Price: 0, CarryOnOnly: true, LayoverMinutes: 120},   // zero price
		{Origin: "AMS", Hub: "AMS", HubBeyond: "RIX", Carrier: "AY", Price: 100, CarryOnOnly: true, LayoverMinutes: 120}, // origin = hub
	}, MatrixOptions{AllowHiddenCity: true, MaxLayoverRisk: 100})
	if got != nil {
		t.Errorf("all-degenerate input should yield nil, got %v", got)
	}
}

func TestExpandMatrix_SavingsAgainstBaseline(t *testing.T) {
	got := ExpandMatrix(sampleOffers(), MatrixOptions{
		AllowHiddenCity: true,
		MaxLayoverRisk:  100,
		TopK:            4,
		DirectBaseline:  300,
	})
	for _, c := range got {
		want := 300 - c.Price
		if want < 0 {
			want = 0
		}
		if c.SavingsEUR != want {
			t.Errorf("Hub=%q SavingsEUR=%.2f, want %.2f", c.Hub, c.SavingsEUR, want)
		}
		if c.SavingsPct != (want/300)*100 {
			t.Errorf("Hub=%q SavingsPct=%.2f, want %.2f", c.Hub, c.SavingsPct, (want/300)*100)
		}
	}
}

func TestExpandMatrix_NoBaselineLeavesSavingsZero(t *testing.T) {
	got := ExpandMatrix(sampleOffers(), MatrixOptions{
		AllowHiddenCity: true, MaxLayoverRisk: 100, TopK: 4,
	})
	for _, c := range got {
		if c.SavingsEUR != 0 || c.SavingsPct != 0 {
			t.Errorf("Hub=%q expected zero savings without baseline, got %.2f / %.2f%%", c.Hub, c.SavingsEUR, c.SavingsPct)
		}
	}
}

func TestExpandMatrix_BookingURLPerCarrier(t *testing.T) {
	cases := []struct {
		carrier  string
		hostpart string
	}{
		{"KL", "airfrance.com"},
		{"AF", "airfrance.com"},
		{"DL", "airfrance.com"},
		{"AY", "finnair.com"},
		{"LH", "lufthansa.com"},
		{"OS", "lufthansa.com"},
		{"LX", "lufthansa.com"},
		{"BA", "google.com/travel/flights"}, // fallback
	}
	for _, tc := range cases {
		offer := HiddenCityOffer{
			Origin: "AMS", Hub: "HEL", HubBeyond: "RIX",
			Carrier: tc.carrier, Price: 100,
			CarryOnOnly: true, LayoverMinutes: 120,
		}
		got := ExpandMatrix([]HiddenCityOffer{offer}, MatrixOptions{
			AllowHiddenCity: true, MaxLayoverRisk: 100, TopK: 1, DepartDate: "2026-06-15",
		})
		if len(got) != 1 {
			t.Fatalf("carrier %q: got %d, want 1", tc.carrier, len(got))
		}
		if !strings.Contains(got[0].BookingURL, tc.hostpart) {
			t.Errorf("carrier %q: BookingURL=%q lacks %q", tc.carrier, got[0].BookingURL, tc.hostpart)
		}
	}
}

func TestScoreLayoverRisk_BoundsAndComposition(t *testing.T) {
	cases := []struct {
		name string
		in   HiddenCityOffer
		want int
	}{
		{"comfort zone carry-on", HiddenCityOffer{LayoverMinutes: 120, CarryOnOnly: true}, 0},
		{"too short", HiddenCityOffer{LayoverMinutes: 30, CarryOnOnly: true}, 60},
		{"60-90 band", HiddenCityOffer{LayoverMinutes: 75, CarryOnOnly: true}, 20},
		{"180-240 band", HiddenCityOffer{LayoverMinutes: 210, CarryOnOnly: true}, 10},
		{"too long", HiddenCityOffer{LayoverMinutes: 300, CarryOnOnly: true}, 25},
		{"checked bags penalty", HiddenCityOffer{LayoverMinutes: 120, CarryOnOnly: false}, 35},
		{"separate tickets penalty", HiddenCityOffer{LayoverMinutes: 120, CarryOnOnly: true, SeparateTickets: true}, 20},
		{"all worst capped", HiddenCityOffer{LayoverMinutes: 30, CarryOnOnly: false, SeparateTickets: true}, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreLayoverRisk(tc.in)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestExpandMatrix_TieBreakOnRiskWhenPriceEqual(t *testing.T) {
	in := []HiddenCityOffer{
		{Origin: "AMS", Hub: "HEL", HubBeyond: "RIX", Carrier: "AY", Price: 200, CarryOnOnly: true, LayoverMinutes: 120}, // risk 0
		{Origin: "AMS", Hub: "FRA", HubBeyond: "VIE", Carrier: "LH", Price: 200, CarryOnOnly: false, LayoverMinutes: 60}, // risk 55 (60-90 + checked-bag)
	}
	got := ExpandMatrix(in, MatrixOptions{AllowHiddenCity: true, MaxLayoverRisk: 100, TopK: 2})
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].LayoverRisk > got[1].LayoverRisk {
		t.Errorf("equal price: lower risk should sort first; got %d then %d", got[0].LayoverRisk, got[1].LayoverRisk)
	}
}
