package mcp

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSortAccommodationOffersLeadsWithRealPrices pins the ordering contract that
// #290 is built around: results lead with the offers that carry a proper,
// verified room-level price, and demote lead-in candidate prices. The ranker is
// a pure function, so this test is deterministic and needs no network.
//
// The ladder under test (tools_accommodations_evidence.go sortAccommodationOffers):
// final-trip-cost ready, then booking ready, then criteria matched, then price
// confidence, then price basis, then cheaper comparable price, then name.
func TestSortAccommodationOffersLeadsWithRealPrices(t *testing.T) {
	// A cheap lead-in candidate must NOT outrank a real room-level price, even
	// though it is the cheapest offer in the set. This is the literal "lead with
	// the ones that have a proper price" requirement.
	leadInCheap := models.AccommodationOffer{
		PropertyName:    "Budget Lead-In",
		NightlyPrice:    50,
		PriceBasis:      models.PriceBasisLeadIn,
		PriceConfidence: models.PriceConfidenceUnverified,
	}
	roomLevel := models.AccommodationOffer{
		PropertyName:    "Real Room Price",
		TotalPrice:      400,
		PriceBasis:      models.PriceBasisRoomTotal,
		PriceConfidence: models.PriceConfidenceRoomLevel,
	}
	verifiedBookable := models.AccommodationOffer{
		PropertyName:       "Verified Bookable",
		TotalPrice:         420,
		PriceBasis:         models.PriceBasisTaxInclusiveTotal,
		PriceConfidence:    models.PriceConfidenceVerified,
		CriteriaMatched:    true,
		BookingReadyStatus: true,
	}

	// Input deliberately ordered worst-first so a no-op would fail.
	offers := []models.AccommodationOffer{leadInCheap, roomLevel, verifiedBookable}
	sortAccommodationOffers(offers)

	wantOrder := []string{"Verified Bookable", "Real Room Price", "Budget Lead-In"}
	for i, want := range wantOrder {
		if offers[i].PropertyName != want {
			t.Fatalf("rank %d = %q, want %q (full order: %s)", i, offers[i].PropertyName, want, names(offers))
		}
	}
}

// TestSortAccommodationOffersTiebreaks walks each rung of the ladder in
// isolation: every case is two offers identical except for one signal, proving
// that signal alone decides the order.
func TestSortAccommodationOffersTiebreaks(t *testing.T) {
	base := func() models.AccommodationOffer {
		return models.AccommodationOffer{
			PropertyName:    "X",
			TotalPrice:      300,
			PriceBasis:      models.PriceBasisRoomTotal,
			PriceConfidence: models.PriceConfidenceRoomLevel,
		}
	}
	with := func(mut func(*models.AccommodationOffer)) models.AccommodationOffer {
		o := base()
		mut(&o)
		return o
	}

	cases := []struct {
		name       string
		winner     models.AccommodationOffer
		loser      models.AccommodationOffer
		winnerName string
	}{
		{
			name:       "final trip cost ready beats not",
			winner:     with(func(o *models.AccommodationOffer) { o.PropertyName = "ready"; o.FinalTripCostReadyStatus = true }),
			loser:      with(func(o *models.AccommodationOffer) { o.PropertyName = "notready" }),
			winnerName: "ready",
		},
		{
			name:       "booking ready beats not",
			winner:     with(func(o *models.AccommodationOffer) { o.PropertyName = "bookable"; o.BookingReadyStatus = true }),
			loser:      with(func(o *models.AccommodationOffer) { o.PropertyName = "lookonly" }),
			winnerName: "bookable",
		},
		{
			name:       "criteria matched beats not",
			winner:     with(func(o *models.AccommodationOffer) { o.PropertyName = "matched"; o.CriteriaMatched = true }),
			loser:      with(func(o *models.AccommodationOffer) { o.PropertyName = "unmatched" }),
			winnerName: "matched",
		},
		{
			name: "higher price confidence beats lower",
			winner: with(func(o *models.AccommodationOffer) {
				o.PropertyName = "verified"
				o.PriceConfidence = models.PriceConfidenceVerified
			}),
			loser:      with(func(o *models.AccommodationOffer) { o.PropertyName = "roomlevel" }),
			winnerName: "verified",
		},
		{
			name: "stronger price basis beats weaker",
			winner: with(func(o *models.AccommodationOffer) {
				o.PropertyName = "taxincl"
				o.PriceBasis = models.PriceBasisTaxInclusiveTotal
			}),
			loser:      with(func(o *models.AccommodationOffer) { o.PropertyName = "roomtotal" }),
			winnerName: "taxincl",
		},
		{
			name:       "cheaper comparable price wins when trust is equal",
			winner:     with(func(o *models.AccommodationOffer) { o.PropertyName = "cheap"; o.TotalPrice = 200 }),
			loser:      with(func(o *models.AccommodationOffer) { o.PropertyName = "dear"; o.TotalPrice = 500 }),
			winnerName: "cheap",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// loser-first input so an unsorted slice fails the assertion.
			offers := []models.AccommodationOffer{tc.loser, tc.winner}
			sortAccommodationOffers(offers)
			if offers[0].PropertyName != tc.winnerName {
				t.Fatalf("leader = %q, want %q", offers[0].PropertyName, tc.winnerName)
			}
		})
	}
}

func names(offers []models.AccommodationOffer) string {
	out := ""
	for i, o := range offers {
		if i > 0 {
			out += " > "
		}
		out += o.PropertyName
	}
	return out
}
