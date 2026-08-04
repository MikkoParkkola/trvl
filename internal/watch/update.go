package watch

import "fmt"

// WatchUpdate is an explicit edit to a stored watch's notification settings
// (#510). A nil field is left untouched; a non-nil field is written verbatim,
// so writing the zero value IS the clear. The pointers exist because a plain Go
// struct cannot distinguish "the caller omitted this" from "the caller wants
// zero here", and that ambiguity is exactly what made clearing impossible.
//
// Identity fields (route, dates, currency) are deliberately absent: they
// compose the dedupe key, so changing one would make the record a different
// watch rather than an edited one.
//
// BelowPrice IS editable here, unlike the identity fields. Re-watching the
// route sets a price, but it cannot UNSET one: applyIntent only applies a
// positive threshold, so zero is indistinguishable from omission on that path
// and an existing target could never be removed while keeping the watch and its
// history (trvl#510). A pointer field can say "clear this", which is the whole
// reason WatchUpdate uses pointers.
//
// WebhookURL has no third state on disk — it is `omitempty`, so the empty
// string IS "no webhook". Clearing and setting-to-empty are therefore the same
// operation by construction.
type WatchUpdate struct {
	BelowPrice        *float64
	WebhookURL        *string
	AlertDropPct      *float64
	AlertDropAbs      *float64
	LastMinuteMode    *bool
	LastMinuteDropPct *float64
}

// Empty reports whether the update would write nothing at all.
func (u WatchUpdate) Empty() bool {
	return u.BelowPrice == nil && u.WebhookURL == nil && u.AlertDropPct == nil &&
		u.AlertDropAbs == nil && u.LastMinuteMode == nil && u.LastMinuteDropPct == nil
}

func (u WatchUpdate) apply(w *Watch) {
	if u.BelowPrice != nil {
		w.BelowPrice = *u.BelowPrice
	}
	if u.WebhookURL != nil {
		w.WebhookURL = *u.WebhookURL
	}
	if u.AlertDropPct != nil {
		w.AlertDropPct = *u.AlertDropPct
	}
	if u.AlertDropAbs != nil {
		w.AlertDropAbs = *u.AlertDropAbs
	}
	// Naming either alert limb is currency reconfirmation, INCLUDING clearing it.
	//
	// A currency change force-zeroes AlertDropAbs when it is the watch's only
	// threshold and sets AlertDropAbsClearedByCurrency, so the checker suspends
	// alerting rather than letting Threshold.effective() substitute the built-in
	// default for a policy the user never chose (round 17). applyIntent clears
	// that marker as soon as a re-watch supplies a fresh value; this path did
	// not, so `watch update --clear-alert-drop` left AlertDropPct == 0,
	// AlertDropAbs == 0 and the marker still set -- precisely the state the
	// guard suppresses. The user asked for default behaviour back and silently
	// got no alerts at all (trvl#558).
	//
	// Gated on the caller NAMING a limb, not on the resulting value: an update
	// that touches only the webhook must not count as reconfirming a currency
	// the user has said nothing about.
	if u.AlertDropPct != nil || u.AlertDropAbs != nil {
		w.AlertDropAbsClearedByCurrency = false
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
// It persists through MutateValidated, so the edit lands on the freshly reloaded
// record field by field and cannot revert a concurrent writer's other fields
// (#512). Only the fields u names are written: price history, LowestPrice,
// CreatedAt and every identity field are untouched (TRVL.WATCH.UNSET.4).
//
// Validation happens INSIDE the transaction, against the committed record.
// Earlier this loaded a snapshot, validated a candidate built from it, and only
// then opened the transaction -- so two edits that were each valid against
// their own snapshot could combine into a record Validate would refuse, with
// neither caller seeing an error (#544). The separate Load is gone with it: the
// transaction reloads anyway, so it was redundant I/O as well as a stale basis
// for the check.
func (s *Store) Update(id string, u WatchUpdate) (Watch, error) {
	if u.Empty() {
		return Watch{}, fmt.Errorf("no fields to update")
	}
	return s.MutateValidated(id, u.apply)
}
