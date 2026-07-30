package watch

import "fmt"

// WatchUpdate is an explicit edit to a stored watch's notification settings
// (#510). A nil field is left untouched; a non-nil field is written verbatim,
// so writing the zero value IS the clear. The pointers exist because a plain Go
// struct cannot distinguish "the caller omitted this" from "the caller wants
// zero here", and that ambiguity is exactly what made clearing impossible.
//
// Identity fields (route, dates, currency, BelowPrice) are deliberately absent:
// they compose the dedupe key, so changing one would make the record a
// different watch rather than an edited one (#509).
//
// WebhookURL has no third state on disk — it is `omitempty`, so the empty
// string IS "no webhook". Clearing and setting-to-empty are therefore the same
// operation by construction.
type WatchUpdate struct {
	WebhookURL        *string
	AlertDropPct      *float64
	AlertDropAbs      *float64
	LastMinuteMode    *bool
	LastMinuteDropPct *float64
}

// Empty reports whether the update would write nothing at all.
func (u WatchUpdate) Empty() bool {
	return u.WebhookURL == nil && u.AlertDropPct == nil && u.AlertDropAbs == nil &&
		u.LastMinuteMode == nil && u.LastMinuteDropPct == nil
}

func (u WatchUpdate) apply(w *Watch) {
	if u.WebhookURL != nil {
		w.WebhookURL = *u.WebhookURL
	}
	if u.AlertDropPct != nil {
		w.AlertDropPct = *u.AlertDropPct
	}
	if u.AlertDropAbs != nil {
		w.AlertDropAbs = *u.AlertDropAbs
	}
	if u.LastMinuteMode != nil {
		w.LastMinuteMode = *u.LastMinuteMode
	}
	if u.LastMinuteDropPct != nil {
		w.LastMinuteDropPct = *u.LastMinuteDropPct
	}
}

// Update applies u to the watch with the given ID and returns the stored record.
//
// It persists through Mutate, so the edit lands on the freshly reloaded record
// field by field and cannot revert a concurrent writer's other fields (#512).
// Only the fields u names are written: price history, LowestPrice, CreatedAt and
// every identity field are untouched (TRVL.WATCH.UNSET.4).
func (s *Store) Update(id string, u WatchUpdate) (Watch, error) {
	if u.Empty() {
		return Watch{}, fmt.Errorf("no fields to update")
	}
	if err := s.Load(); err != nil {
		return Watch{}, err
	}
	cur, ok := s.Get(id)
	if !ok {
		return Watch{}, fmt.Errorf("watch %s not found", id)
	}
	// Validate a candidate up front: Mutate's callback runs under the store lock
	// and has no way to reject, so an invalid combination must be caught before
	// the transaction opens rather than persisted and discovered later.
	candidate := cur
	u.apply(&candidate)
	if err := candidate.Validate(); err != nil {
		return Watch{}, err
	}
	return s.Mutate(id, u.apply)
}
