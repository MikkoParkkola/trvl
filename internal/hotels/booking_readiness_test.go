package hotels

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func boolPtrRO(b bool) *bool { return &b }

// readyRoom returns a room that satisfies every readiness input, so each test
// can knock out exactly one signal and assert the downgrade. (MIK-6232 AC.4)
func readyRoom() RoomType {
	return RoomType{
		Name:            "Deluxe Double",
		Price:           120,
		Currency:        "EUR",
		ProviderURL:     "https://www.booking.com/hotel/example.html", // durable -> stable
		MatchConfidence: models.RoomInventoryMatchExact,               // identity confirmed
		Refundable:      boolPtrRO(true),                              // refundability known
	}
}

func TestRoomReadiness_AllSignalsKnown_Ready(t *testing.T) {
	if got := roomReadiness(readyRoom()); got != ReadinessReady {
		t.Fatalf("all signals known: want %q, got %q", ReadinessReady, got)
	}
}

// AC.1/AC.2: no usable price -> unverified, regardless of other signals.
func TestRoomReadiness_NoPrice_Unverified(t *testing.T) {
	r := readyRoom()
	r.Price, r.NightlyPrice, r.TotalPrice = 0, 0, 0
	if got := roomReadiness(r); got != ReadinessUnverified {
		t.Fatalf("no price: want %q, got %q", ReadinessUnverified, got)
	}
}

// AC.2: expiring link forces a downgrade from ready.
func TestRoomReadiness_ExpiringLink_Caution(t *testing.T) {
	r := readyRoom()
	r.ProviderURL = "https://www.google.com/aclk?ad=redirect" // expiring redirect
	if got := roomReadiness(r); got != ReadinessCaution {
		t.Fatalf("expiring link: want %q, got %q", ReadinessCaution, got)
	}
}

// AC.2: a missing link is unknown -> never "ready".
func TestRoomReadiness_NoLink_Caution(t *testing.T) {
	r := readyRoom()
	r.ProviderURL = ""
	if got := roomReadiness(r); got != ReadinessCaution {
		t.Fatalf("no link: want %q, got %q", ReadinessCaution, got)
	}
}

// AC.2: a non-exact (similar) identity is not confirmed -> downgrade.
func TestRoomReadiness_UnconfirmedIdentity_Caution(t *testing.T) {
	r := readyRoom()
	r.MatchConfidence = models.RoomInventoryMatchSimilar
	if got := roomReadiness(r); got != ReadinessCaution {
		t.Fatalf("unconfirmed identity: want %q, got %q", ReadinessCaution, got)
	}
}

// AC.2: unknown refundability (nil) forces a downgrade -- the exact "never
// upgrade on missing data" rule.
func TestRoomReadiness_RefundabilityUnknown_Caution(t *testing.T) {
	r := readyRoom()
	r.Refundable = nil
	if got := roomReadiness(r); got != ReadinessCaution {
		t.Fatalf("unknown refundability: want %q, got %q", ReadinessCaution, got)
	}
}

// A non-refundable but *known* rate still satisfies refundability_known; with
// every other signal present it is ready. Guards against conflating "known
// non-refundable" with "unknown".
func TestRoomReadiness_KnownNonRefundable_Ready(t *testing.T) {
	r := readyRoom()
	r.Refundable = boolPtrRO(false)
	if got := roomReadiness(r); got != ReadinessReady {
		t.Fatalf("known non-refundable: want %q, got %q", ReadinessReady, got)
	}
}
