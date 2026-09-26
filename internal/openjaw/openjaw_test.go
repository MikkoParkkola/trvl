package openjaw

import "testing"

func TestComposeFlyInTrainOut(t *testing.T) {
	bundle, err := Compose(
		Leg{Mode: "flight", Origin: "HEL", Destination: "VIE", Cost: 180, DurationMin: 140},
		Leg{Mode: "train", Origin: "Vienna", Destination: "Budapest", Cost: 40, DurationMin: 160},
	)
	if err == nil {
		t.Fatal("expected a city mismatch between VIE and Vienna")
	}
	bundle, err = Compose(
		Leg{Mode: "flight", Origin: "HEL", Destination: "VIE", Cost: 180, DurationMin: 140},
		Leg{Mode: "train", Origin: "VIE", Destination: "BUD", Cost: 40, DurationMin: 160},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Total != 220 {
		t.Fatalf("total = %v, want 220", bundle.Total)
	}
	if len(bundle.Legs) != 2 || bundle.Legs[0].TransferAfter != "VIE" {
		t.Fatalf("legs = %#v", bundle.Legs)
	}
	if bundle.Legs[0].Mode != "flight" || bundle.Legs[1].Mode != "train" {
		t.Fatalf("modes = %s %s", bundle.Legs[0].Mode, bundle.Legs[1].Mode)
	}
}

func TestComposeRejectsNegativeCost(t *testing.T) {
	_, err := Compose(
		Leg{Mode: "flight", Origin: "HEL", Destination: "VIE", Cost: -1},
		Leg{Mode: "train", Origin: "VIE", Destination: "BUD", Cost: 10},
	)
	if err == nil {
		t.Fatal("expected negative cost to fail")
	}
}
