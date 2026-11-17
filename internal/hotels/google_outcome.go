package hotels

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

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
	err error
}

// merge folds one call into the lookup's outcome. settled is the caller's
// decision that it holds the target's Google data (or, for a by-name lookup,
// that Google answered for this exact hotel); it alone clears a refusal.
func (o *googleOutcome) merge(c googleCall, settled bool) {
	if settled {
		o.err = nil
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
// error itself.
func googleCooldownErr(op string, status int) error {
	return fmt.Errorf("%s refused (HTTP %d): %w: %w", op, status, models.ErrRateLimited, batchexec.ErrRefused)
}

// googleCooldown reports whether a refusal armed Google's cooldown. The text
// match covers a message already flattened into a string.
func googleCooldown(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, batchexec.ErrRefused) || strings.Contains(err.Error(), batchexec.ErrRefused.Error())
}
