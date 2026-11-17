package hotels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// One recorded Google outcome per lookup (#813, #826 design rev 6).
//
// A googleCall is the ordered events of one Google call or one search run,
// written by the goroutine making the requests and frozen once returned. A
// googleOutcome belongs to one lookup and changes only through merge, which
// takes the call and the caller's settled decision: settled clears, otherwise
// the call's strongest refusal is recorded, and nothing else touches it.

type googleEventKind int

const (
	googleAnswered googleEventKind = iota // a live Google reply, with or without data
	googleRefused                         // refusal class: 429, quota, cooldown skip, block, 503
	googleFailed                          // any other failure: timeout, parse, 404, empty page
)

type googleEvent struct {
	kind googleEventKind
	err  error
}

type googleCall struct {
	events []googleEvent
}

// note records one Google request. status is the raw HTTP status when the
// client returned one (a refusal arrives as a status with a nil error); err
// is the request's error. Classification lives here and nowhere else.
func (c *googleCall) note(status int, err error) {
	if err == nil && status != 0 && status != http.StatusOK {
		err = fmt.Errorf("google returned HTTP %d", status)
	}
	switch {
	case err == nil:
		c.events = append(c.events, googleEvent{kind: googleAnswered})
	case errors.Is(err, batchexec.ErrRefused) || models.ClassifyProviderError(err) == models.StatusRateLimited:
		c.events = append(c.events, googleEvent{kind: googleRefused, err: rateLimitWrap(err)})
	default:
		c.events = append(c.events, googleEvent{kind: googleFailed, err: err})
	}
}

// answered reports whether Google replied live to any request in the call.
func (c googleCall) answered() bool {
	for _, e := range c.events {
		if e.kind == googleAnswered {
			return true
		}
	}
	return false
}

// clone returns a copy whose events the caller may change freely.
func (c googleCall) clone() googleCall {
	return googleCall{events: slices.Clone(c.events)}
}

// primaryErr returns the first request's error when it was not answered: the
// search's primary Google fetch, which a provider status reports.
func (c googleCall) primaryErr() error {
	if len(c.events) == 0 || c.events[0].kind == googleAnswered {
		return nil
	}
	return c.events[0].err
}

// refusal returns the call's strongest refusal, or nil.
func (c googleCall) refusal() error {
	var r error
	for _, e := range c.events {
		if e.kind == googleRefused {
			r = strongerRefusal(r, e.err)
		}
	}
	return r
}

type googleOutcome struct {
	err     error
	settled bool
}

// merge folds one call into the lookup's outcome. settled is the caller's
// decision that it holds the target's Google data (or, for a by-name lookup,
// that Google answered for this exact hotel); it alone clears a refusal, and it
// is final: once the lookup holds its answer, a later refusal cannot have
// hidden anything it needs.
func (o *googleOutcome) merge(c googleCall, settled bool) {
	if settled || o.settled {
		o.err, o.settled = nil, true
		return
	}
	if r := c.refusal(); r != nil {
		o.err = strongerRefusal(o.err, r)
	}
}

// Err returns the recorded refusal, wrapping models.ErrRateLimited, or nil.
func (o *googleOutcome) Err() error { return o.err }

// strongerRefusal applies the precedence rule: a cooldown-armed refusal
// (carrying the retry clock) beats a plain one and is never replaced by one;
// between equals the newer wins.
func strongerRefusal(cur, next error) error {
	if cur != nil && googleCooldown(cur) && !googleCooldown(next) {
		return cur
	}
	return next
}

// rateLimitWrap makes a refusal satisfy errors.Is(models.ErrRateLimited)
// while keeping the original error (ErrBlocked, a bare 503) in the chain.
func rateLimitWrap(err error) error {
	if errors.Is(err, models.ErrRateLimited) {
		return err
	}
	return fmt.Errorf("%w: %w", models.ErrRateLimited, err)
}

// googleCooldownErr is the one constructor for a refusal that armed the
// client's cooldown: a Google 429, or a 200 whose body batchexec.QuotaRefusal
// accepts. It carries batchexec.ErrRefused, so googleCooldown keys on the
// error itself. The refusal has just armed the cooldown, so a Google call made
// now is skipped without dialling (the documented batchexec contract) and
// returns the skip error with its retry clock; wrapping that error lets the
// arming refusal say when a retry can work, as the later skips do (#813).
func googleCooldownErr(ctx context.Context, client *batchexec.Client, op string, status int) error {
	if _, _, skip := client.Get(ctx, googleCooldownProbeURL); errors.Is(skip, batchexec.ErrRefused) {
		return fmt.Errorf("%s refused (HTTP %d): %w: %w", op, status, models.ErrRateLimited, skip)
	}
	return fmt.Errorf("%s refused (HTTP %d): %w: %w", op, status, models.ErrRateLimited, batchexec.ErrRefused)
}

// googleCooldownProbeURL is any Google URL: inside the cooldown the client
// answers from its own state and never sends the request.
const googleCooldownProbeURL = "https://www.google.com/travel/hotels"

// googleCooldown reports whether a refusal armed Google's cooldown. Its only
// callers pass original error values (the recorded refusal, strongerRefusal's
// inputs), never a flattened status string, so the sentinel is enough.
func googleCooldown(err error) bool {
	return errors.Is(err, batchexec.ErrRefused)
}
