package hotels

import (
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Booking-readiness contract (MIK-6232).
//
// "Bookable" is not binary. A room's price can be live while its deep link is
// already dead, its room identity is a property-level lead-in rather than a
// real rate, or its refundability is simply unknown. trvl surfaced those
// signals separately (link durability #168, match confidence, refundability)
// but never composed them, so an agent could not tell a safe save from a risky
// one at a glance.
//
// roomReadiness composes the four signals into one verdict. It is conservative
// by construction: any unknown input forces a downgrade, and a missing field
// never upgrades readiness. Over-claiming "ready" on a dead link is the exact
// trust failure this prevents.
const (
	// ReadinessReady — a real bookable price, a durable (non-expiring) link, an
	// exact room-level identity, and known refundability. Safe to act on.
	ReadinessReady = "ready"
	// ReadinessCaution — a real bookable price, but at least one trust signal is
	// weak or unknown: an expiring link, an unconfirmed identity, or unknown
	// refundability. Bookable, but verify before relying on it.
	ReadinessCaution = "caution"
	// ReadinessUnverified — no usable bookable price, so neither the link nor the
	// identity can be trusted to land on a real rate.
	ReadinessUnverified = "unverified"
)

// roomReadiness returns the booking-readiness verdict for a single room.
//
// The four contract inputs (MIK-6232) map to existing room fields:
//
//   - verified:            roomEffectivePrice(room) > 0 (a real bookable price)
//   - link_stable:         the provider link is durable, not an expiring redirect
//   - identity_confirmed:  an exact room-level match, not a similar/property lead-in
//   - refundability_known: the provider stated whether the rate is refundable
//
// Any unknown input downgrades; readiness is never upgraded on missing data.
func roomReadiness(room RoomType) string {
	// No usable price -> nothing to trust. The cheapest, most common failure.
	if roomEffectivePrice(room) <= 0 {
		return ReadinessUnverified
	}
	// An empty URL classifies as "" (not "stable"), so a missing link cannot
	// reach "ready" -- the conservative outcome.
	linkStableOK := ClassifyLinkDurability(room.ProviderURL) == linkStable
	identityConfirmed := strings.TrimSpace(room.MatchConfidence) == models.RoomInventoryMatchExact
	refundabilityKnown := room.Refundable != nil

	if linkStableOK && identityConfirmed && refundabilityKnown {
		return ReadinessReady
	}
	return ReadinessCaution
}
