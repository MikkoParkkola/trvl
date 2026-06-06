package hotels

import (
	"testing"
	"time"
)

func TestRateManager_BackoffIncreases(t *testing.T) {
	rm := NewRateManager()
	rm.Record429("google")

	backoff := rm.Backoff("google")
	if backoff < 1800*time.Millisecond || backoff > 2200*time.Millisecond {
		t.Errorf("backoff after 1x429 = %v, want ~2s", backoff)
	}

	rm.Record429("google")
	backoff2 := rm.Backoff("google")
	if backoff2 <= backoff {
		t.Errorf("backoff should increase: %v -> %v", backoff, backoff2)
	}
}

func TestRateManager_ThrottleAfter3_429s(t *testing.T) {
	rm := NewRateManager()
	for i := 0; i < 5; i++ {
		rm.Record429("google")
	}
	if !rm.IsThrottled("google") {
		t.Error("expected IsThrottled after 5 x 429")
	}
}

func TestRateManager_ResetAfterCooldown(t *testing.T) {
	rm := NewRateManager()
	rm.Record429("google")
	rm.Reset("google")
	if rm.IsThrottled("google") {
		t.Error("expected IsThrottled=false after Reset")
	}
}

func TestRateManager_BaseBackoff(t *testing.T) {
	rm := NewRateManager()
	b := rm.Backoff("google")
	if b < 800*time.Millisecond || b > 1200*time.Millisecond {
		t.Errorf("base backoff = %v, want ~1s", b)
	}
}

func TestRateManager_RecordsStats(t *testing.T) {
	rm := NewRateManager()
	rm.RecordRequest("google")
	rm.RecordRequest("google")
	rm.Record429("google")

	reqs, recent429s, throttled := rm.Stats("google")
	if reqs != 2 {
		t.Errorf("requests = %d, want 2", reqs)
	}
	if recent429s != 1 {
		t.Errorf("recent429s = %d, want 1", recent429s)
	}
	if throttled {
		t.Error("expected not throttled after 1x429")
	}
}

func TestRateManager_MultipleProviders(t *testing.T) {
	rm := NewRateManager()
	rm.Record429("google")
	rm.Record429("booking")

	b1 := rm.Backoff("google")
	b2 := rm.Backoff("booking")
	if b1 != b2 {
		t.Errorf("both providers should have same base = %v vs %v", b1, b2)
	}

	rm.Record429("google")
	rm.Record429("google")
	rm.Record429("google")

	if !rm.IsThrottled("google") {
		t.Error("google should be throttled")
	}
	if rm.IsThrottled("booking") {
		t.Error("booking should not be throttled")
	}
}
