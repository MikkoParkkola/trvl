package hotels

import (
	"context"
	"strings"
	"testing"
)

// Regression tests for GH #187 / #188 Bug 2: a Booking.com WAF challenge (HTTP
// 202, also 403/503) must trigger exactly one token re-harvest (forceRefresh)
// and a retry, rather than returning an empty result. Verified deterministically
// via the bookingAcquireTokenFn / bookingGetFn seams.

func TestFetchBookingSearch_202ReharvestsAndRetries(t *testing.T) {
	origAcquire := bookingAcquireTokenFn
	origGet := bookingGetFn
	defer func() { bookingAcquireTokenFn = origAcquire; bookingGetFn = origGet }()

	var forceFlags []bool
	bookingAcquireTokenFn = func(_ context.Context, _ string, force bool) (string, error) {
		forceFlags = append(forceFlags, force)
		if force {
			return "fresh-token", nil
		}
		return "stale-token", nil
	}

	var gets []string
	bookingGetFn = func(_ context.Context, _ string, token string) (int, []byte, error) {
		gets = append(gets, token)
		if token == "stale-token" {
			return 202, nil, nil // WAF challenge
		}
		return 200, []byte("<html>listings</html>"), nil
	}

	body, err := fetchBookingSearch(context.Background(), "https://booking.com/searchresults.html")
	if err != nil {
		t.Fatalf("fetchBookingSearch: %v", err)
	}
	if !strings.Contains(body, "listings") {
		t.Errorf("expected listings body after retry, got %q", body)
	}
	// First acquire (force=false), then re-harvest (force=true).
	if len(forceFlags) != 2 || forceFlags[0] != false || forceFlags[1] != true {
		t.Errorf("expected [false true] acquire flags, got %v", forceFlags)
	}
	if len(gets) != 2 || gets[0] != "stale-token" || gets[1] != "fresh-token" {
		t.Errorf("expected stale then fresh token GETs, got %v", gets)
	}
}

func TestFetchBookingSearch_WAFChallengeStatuses(t *testing.T) {
	for _, status := range []int{202, 403, 503} {
		if !bookingWAFChallenged(status) {
			t.Errorf("status %d should be treated as a WAF challenge", status)
		}
	}
	for _, status := range []int{200, 404, 500} {
		if bookingWAFChallenged(status) {
			t.Errorf("status %d should NOT be treated as a WAF challenge", status)
		}
	}
}

func TestFetchBookingSearch_PersistentChallengeErrors(t *testing.T) {
	origAcquire := bookingAcquireTokenFn
	origGet := bookingGetFn
	defer func() { bookingAcquireTokenFn = origAcquire; bookingGetFn = origGet }()

	bookingAcquireTokenFn = func(_ context.Context, _ string, _ bool) (string, error) {
		return "tok", nil
	}
	calls := 0
	bookingGetFn = func(_ context.Context, _ string, _ string) (int, []byte, error) {
		calls++
		return 202, nil, nil // never recovers
	}

	_, err := fetchBookingSearch(context.Background(), "https://booking.com/searchresults.html")
	if err == nil {
		t.Fatal("expected error when WAF challenge persists after re-harvest")
	}
	if !strings.Contains(err.Error(), "after token re-harvest") {
		t.Errorf("expected re-harvest error, got: %v", err)
	}
	// Exactly two GETs: initial + one retry (no infinite loop).
	if calls != 2 {
		t.Errorf("expected 2 GET attempts, got %d", calls)
	}
}
