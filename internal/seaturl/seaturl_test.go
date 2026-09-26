package seaturl

import "testing"

func TestApplyKiwiAndKLM(t *testing.T) {
	kiwi := Apply("https://www.kiwi.com/booking/1", "window")
	if kiwi != "https://www.kiwi.com/booking/1?seat=window" {
		t.Fatalf("kiwi = %s", kiwi)
	}
	klm := Apply("https://www.klm.com/search?from=AMS", "aisle")
	if klm != "https://www.klm.com/search?from=AMS&seatPreference=aisle" && klm != "https://www.klm.com/search?seatPreference=aisle&from=AMS" {
		t.Fatalf("klm = %s", klm)
	}
}

func TestApplyLeavesOtherHosts(t *testing.T) {
	raw := "https://www.google.com/travel/flights"
	if got := Apply(raw, "window"); got != raw {
		t.Fatalf("google = %s", got)
	}
	if got := Apply("https://www.kiwi.com/booking/1", "no_preference"); got != "https://www.kiwi.com/booking/1" {
		t.Fatalf("no_preference = %s", got)
	}
}
