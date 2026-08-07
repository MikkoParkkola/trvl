package pricefeed

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// trvl#535, TRVL.TRUST.4 -- refundability is RECORDED where the seller states
// it, and marked unknown where the seller is silent. Never inferred.
//
// The hotel-price path used to declare refundability permanently unavailable,
// so every verdict from it was capped at Caution no matter how good the data
// was. That ceiling was self-imposed: the seller's cancellation terms were
// present upstream and were being dropped on the way into the per-seller list.
//
// The external tester who prompted this saw Caution on six indexed properties
// and could not tell whether he was looking at six uncertain properties or a
// scale that never says better. Both halves below matter for that reason: the
// ceiling must lift when terms exist, and must REMAIN when they do not.

// A seller that states free cancellation lifts the ceiling.
func TestHotelPricesReadinessUsesStatedRefundability(t *testing.T) {
	yes := true
	withTerms := []models.ProviderPrice{{
		Provider:         "someseller",
		Price:            180,
		Currency:         "EUR",
		PriceConfidence:  models.PriceConfidenceVerified,
		LinkDurability:   "stable",
		ProviderURL:      "https://seller.example/book",
		FreeCancellation: &yes,
	}}

	got := HotelPricesReadiness("hotel-abc", withTerms)

	if got.Capped() {
		t.Errorf("verdict still reports a ceiling (%q) when the seller stated its cancellation "+
			"terms -- Capped means this source could never reach Ready for any property, which "+
			"stops being true the moment the terms are carried through", got.Ceiling)
	}
}

// Silence must NOT lift it. This is the half that keeps the fix honest: a
// verdict that improved merely because a field exists would be the same
// guess-as-fact defect in a new place.
func TestHotelPricesReadinessKeepsTheCeilingWhenNoSellerStatesTerms(t *testing.T) {
	silent := []models.ProviderPrice{{
		Provider:        "someseller",
		Price:           180,
		Currency:        "EUR",
		PriceConfidence: models.PriceConfidenceVerified,
		LinkDurability:  "stable",
		ProviderURL:     "https://seller.example/book",
		// No FreeCancellation, no FreeCancellationUntil: the seller said nothing.
	}}

	got := HotelPricesReadiness("hotel-abc", silent)

	if !got.Capped() {
		t.Error("the ceiling was lifted when NO seller stated any cancellation terms -- absence of " +
			"terms is not evidence of refundability, and dropping the ceiling on silence tells a " +
			"caller the path could do better when it cannot")
	}
}

// A deadline alone counts as stated terms, even without the boolean: a seller
// that says "free cancellation until 3 June" has told us something, and
// requiring both fields would discard it.
func TestHotelPricesReadinessAcceptsADeadlineAlone(t *testing.T) {
	withDeadline := []models.ProviderPrice{{
		Provider:              "someseller",
		Price:                 180,
		Currency:              "EUR",
		PriceConfidence:       models.PriceConfidenceVerified,
		LinkDurability:        "stable",
		ProviderURL:           "https://seller.example/book",
		FreeCancellationUntil: "2026-06-03",
	}}

	silent := []models.ProviderPrice{{
		Provider:        "someseller",
		Price:           180,
		Currency:        "EUR",
		PriceConfidence: models.PriceConfidenceVerified,
		LinkDurability:  "stable",
		ProviderURL:     "https://seller.example/book",
	}}

	if HotelPricesReadiness("hotel-abc", withDeadline).Capped() ==
		HotelPricesReadiness("hotel-abc", silent).Capped() {
		t.Error("a stated cancellation deadline changed nothing; a seller that names a date has " +
			"told us its terms even without the boolean flag")
	}
}

// Explicitly non-refundable is still known refundability. It must differ from
// silence even though it does not become a positive free-cancellation claim.
func TestHotelPricesReadinessTreatsExplicitNonRefundableAsKnown(t *testing.T) {
	no := false
	providers := []models.ProviderPrice{{
		Provider:         "someseller",
		Price:            180,
		Currency:         "EUR",
		PriceConfidence:  models.PriceConfidenceVerified,
		LinkDurability:   "stable",
		FreeCancellation: &no,
	}}

	if got := HotelPricesReadiness("hotel-abc", providers); got.Capped() {
		t.Errorf("explicit non-refundable terms were treated as missing evidence: %q", got.Ceiling)
	}
}
