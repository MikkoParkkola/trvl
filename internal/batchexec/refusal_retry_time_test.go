package batchexec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestCooldownErrorNamesTheRetryTime (#813): a Google call skipped inside the
// cooldown says when a retry can work, as a clock time with its zone and as a
// wait, and still matches ErrRefused.
func TestCooldownErrorNamesTheRetryTime(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()
	c := NewTestClient(ts.URL)
	c.SetNoCache(true)
	ctx := context.Background()

	if status, _, err := c.Get(ctx, "https://www.google.com/travel/hotels/a"); err != nil || status != http.StatusTooManyRequests {
		t.Fatalf("first call: status %d, err %v; want the 429 that arms the cooldown", status, err)
	}
	_, _, err := c.Get(ctx, "https://www.google.com/travel/hotels/b")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("second call: err = %v, want ErrRefused", err)
	}
	want := "retry after " + c.refuseUntil.Format("15:04:05 MST")
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to contain %q", err, want)
	}
	if !regexp.MustCompile(`\(in \d+s\)`).MatchString(err.Error()) {
		t.Fatalf("err = %q, want the remaining wait as (in Ns)", err)
	}
}

// TestCooldownLeavesOtherHostsAlone (preservation): the cooldown is Google's;
// another host is still dialled and its 429 is not turned into a refusal.
func TestCooldownLeavesOtherHostsAlone(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	c := NewTestClient(ts.URL)
	c.SetNoCache(true)
	c.armGoogleRefusal(googleRefusalFloor)

	status, _, err := c.Get(context.Background(), "https://example.com/x")
	if err != nil || status != http.StatusOK {
		t.Fatalf("non-Google call during a Google cooldown: status %d, err %v; want 200, nil", status, err)
	}
}
