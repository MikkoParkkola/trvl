package pricealert

import "testing"

func TestEvaluate_FirstObservationCapturesBaselineNoAlert(t *testing.T) {
	st, alert, fired := Evaluate(State{}, 200, Threshold{DropPercent: 10})
	if fired {
		t.Fatalf("first observation must not alert, got %+v", alert)
	}
	if st.Baseline != 200 {
		t.Fatalf("baseline = %v, want 200", st.Baseline)
	}
	if st.LastAlertedAt != 0 {
		t.Fatalf("LastAlertedAt = %v, want 0", st.LastAlertedAt)
	}
}

func TestEvaluate_DropBeyondThresholdFiresExactlyOnce(t *testing.T) {
	th := Threshold{DropPercent: 10}
	st := State{Baseline: 200}

	// 180 is a 10% drop — exactly at threshold, should fire.
	st, alert, fired := Evaluate(st, 180, th)
	if !fired {
		t.Fatalf("expected alert at 10%% drop")
	}
	if alert.DropPercent < 9.99 || alert.DropPercent > 10.01 {
		t.Fatalf("DropPercent = %v, want ~10", alert.DropPercent)
	}
	if alert.Baseline != 200 || alert.Current != 180 || alert.Drop != 20 {
		t.Fatalf("alert fields = %+v", alert)
	}

	// Re-check at the SAME price: must not re-alert (dedup).
	st2, _, fired2 := Evaluate(st, 180, th)
	if fired2 {
		t.Fatalf("same drop must not re-alert")
	}

	// A partial bounce that is still below baseline must not alert.
	_, _, fired3 := Evaluate(st2, 185, th)
	if fired3 {
		t.Fatalf("partial recovery must not alert")
	}
}

func TestEvaluate_DropBelowThresholdIsSilent(t *testing.T) {
	th := Threshold{DropPercent: 10}
	st := State{Baseline: 200}

	// 5% drop — below threshold.
	_, _, fired := Evaluate(st, 190, th)
	if fired {
		t.Fatalf("5%% drop must be silent under a 10%% threshold")
	}
}

func TestEvaluate_RaisedPriceDoesNotAlertAndRearms(t *testing.T) {
	th := Threshold{DropPercent: 10}
	st := State{Baseline: 200}

	// Price rises above baseline: no alert, baseline tracks the new peak.
	st, _, fired := Evaluate(st, 250, th)
	if fired {
		t.Fatalf("rising price must not alert")
	}
	if st.Baseline != 250 {
		t.Fatalf("baseline should rise to 250, got %v", st.Baseline)
	}

	// Now a 10% drop from the NEW baseline (225) should fire.
	_, alert, fired := Evaluate(st, 225, th)
	if !fired {
		t.Fatalf("expected alert measured from raised baseline")
	}
	if alert.Baseline != 250 {
		t.Fatalf("alert baseline = %v, want 250", alert.Baseline)
	}
}

func TestEvaluate_DeeperDropAfterFirstAlertFiresAgain(t *testing.T) {
	th := Threshold{DropPercent: 10}
	st := State{Baseline: 200}

	st, _, fired := Evaluate(st, 180, th) // first alert
	if !fired {
		t.Fatalf("expected first alert")
	}
	// A new, deeper low past threshold is a genuinely new event.
	_, alert, fired := Evaluate(st, 150, th)
	if !fired {
		t.Fatalf("deeper drop should fire a fresh alert")
	}
	if alert.Current != 150 {
		t.Fatalf("alert.Current = %v, want 150", alert.Current)
	}
}

func TestEvaluate_AbsoluteThreshold(t *testing.T) {
	// Only an absolute limb: alert when the fare falls by >= 30 units.
	th := Threshold{DropAbsolute: 30}
	st := State{Baseline: 500}

	// 20-unit fall — below absolute threshold.
	_, _, fired := Evaluate(st, 480, th)
	if fired {
		t.Fatalf("20-unit drop must be silent under a 30-unit threshold")
	}
	// 40-unit fall — qualifies.
	_, alert, fired := Evaluate(st, 460, th)
	if !fired {
		t.Fatalf("40-unit drop should fire")
	}
	if alert.Drop != 40 {
		t.Fatalf("alert.Drop = %v, want 40", alert.Drop)
	}
}

func TestEvaluate_DefaultThresholdWhenUnset(t *testing.T) {
	var th Threshold // both limbs zero -> DefaultDropPercent (10%)
	st := State{Baseline: 100}

	_, _, fired := Evaluate(st, 95, th) // 5% — silent
	if fired {
		t.Fatalf("5%% drop must be silent under the default 10%% threshold")
	}
	_, _, fired = Evaluate(st, 89, th) // 11% — fires
	if !fired {
		t.Fatalf("11%% drop should fire under the default threshold")
	}
}

func TestEvaluate_NonPositivePriceIsNoOp(t *testing.T) {
	st := State{Baseline: 200, LastAlertedAt: 180}
	got, _, fired := Evaluate(st, 0, Threshold{DropPercent: 10})
	if fired {
		t.Fatalf("zero price must not alert")
	}
	if got != st {
		t.Fatalf("state mutated on no-data: %+v != %+v", got, st)
	}
}

func TestEvaluate_EitherLimbQualifies(t *testing.T) {
	// Percent not met (5%) but absolute met (25 units).
	th := Threshold{DropPercent: 10, DropAbsolute: 25}
	st := State{Baseline: 500}
	_, _, fired := Evaluate(st, 475, th) // 25 units, 5%
	if !fired {
		t.Fatalf("absolute limb alone should qualify")
	}
}
