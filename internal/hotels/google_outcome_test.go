package hotels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// primedRefusal returns the error the real client gives for a Google call
// skipped inside an armed cooldown: ErrRefused with its retry clock.
func primedRefusal(t *testing.T) error {
	t.Helper()
	roomLookupWorld(t, googleRoutes{answer(429), answer(429), answer(429)}, true)
	_, _, err := DefaultClient().Get(context.Background(), "https://www.google.com/travel/hotels/x")
	if !errors.Is(err, batchexec.ErrRefused) {
		t.Fatalf("precondition: want ErrRefused from a primed client, got %v", err)
	}
	return err
}

func callOf(events ...[2]any) googleCall {
	var c googleCall
	for _, e := range events {
		status, _ := e[0].(int)
		err, _ := e[1].(error)
		c.note(status, err)
	}
	return c
}

// TestGoogleOutcome_Merge covers the merge table and the precedence rule
// (#826 design rev 6, sections 1 and 2) over real errors.
func TestGoogleOutcome_Merge(t *testing.T) {
	clock := primedRefusal(t)
	plain := fmt.Errorf("hotel search request: %w", batchexec.ErrBlocked) // a 403 block
	bare503 := errors.New("hotel search returned status 503")
	answered := [2]any{http.StatusOK, nil}

	cases := []struct {
		name    string
		prior   error
		call    googleCall
		settled bool
		want    error // nil, or the error the outcome must keep (errors.Is)
	}{
		{"settled clears a recorded refusal", plain, callOf(answered), true, nil},
		{"settled clears even when the call refused", plain, callOf([2]any{0, clock}), true, nil},
		{"refusal is recorded", nil, callOf([2]any{0, plain}), false, batchexec.ErrBlocked},
		{"status with nil error is a refusal", nil, callOf([2]any{http.StatusTooManyRequests, nil}), false, models.ErrRateLimited},
		{"answered empty keeps a refusal", plain, callOf(answered), false, batchexec.ErrBlocked},
		{"failure keeps a refusal", plain, callOf([2]any{0, ErrPriceNoData}), false, batchexec.ErrBlocked},
		{"failure leaves nothing recorded", nil, callOf([2]any{http.StatusNotFound, nil}), false, nil},
		{"no request leaves a refusal", plain, googleCall{}, false, batchexec.ErrBlocked},
		{"clock then plain keeps the clock", clock, callOf([2]any{0, plain}), false, batchexec.ErrRefused},
		{"plain then clock takes the clock", plain, callOf([2]any{0, clock}), false, batchexec.ErrRefused},
		{"two plain refusals: the newer wins", plain, callOf([2]any{0, bare503}), false, bare503},
		{"bare 503 is a rate limit", nil, callOf([2]any{0, bare503}), false, models.ErrRateLimited},
		{"ErrBlocked is a rate limit", nil, callOf([2]any{0, plain}), false, models.ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out googleOutcome
			if tc.prior != nil {
				out.merge(callOf([2]any{0, tc.prior}), false) // the only way a refusal is recorded
			}
			out.merge(tc.call, tc.settled)
			got := out.Err()
			if tc.want == nil {
				if got != nil {
					t.Fatalf("Err() = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) || !errors.Is(got, models.ErrRateLimited) {
				t.Fatalf("Err() = %v, want it to wrap %v and ErrRateLimited", got, tc.want)
			}
		})
	}
}

// TestGoogleCall_Events: a call keeps every request's event; a live answer
// and a refusal in one call are both visible.
func TestGoogleCall_Events(t *testing.T) {
	c := callOf([2]any{http.StatusOK, nil}, [2]any{0, fmt.Errorf("page 2: %w", batchexec.ErrBlocked)})
	if !c.answered() || c.refusal() == nil {
		t.Fatalf("answered %v, refusal %v; want both", c.answered(), c.refusal())
	}
	if (googleCall{}).answered() || (googleCall{}).refusal() != nil {
		t.Fatal("an empty call must report neither an answer nor a refusal")
	}
}

// TestGoogleCooldownErr: the cooldown constructor carries both sentinels and
// the canonical text; a plain refusal carries no marker.
func TestGoogleCooldownErr(t *testing.T) {
	err := googleCooldownErr("google hotel search", http.StatusTooManyRequests)
	if !errors.Is(err, batchexec.ErrRefused) || !errors.Is(err, models.ErrRateLimited) || !googleCooldown(err) {
		t.Fatalf("googleCooldownErr = %v, want ErrRefused, ErrRateLimited and a cooldown", err)
	}
	var c googleCall
	c.note(http.StatusForbidden, nil)
	if googleCooldown(c.refusal()) {
		t.Fatalf("a 403 refusal %v must not claim a cooldown", c.refusal())
	}
}
