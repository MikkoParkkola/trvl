package hacks

import (
	"context"
	"fmt"

	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// railLegCost is the resolved cost of the rail segment of a rail-fly hack.
type railLegCost struct {
	Cost      float64 // 0 means bundled-free (included in the airline ticket)
	Currency  string
	Provider  string // operator the quote came from; empty when bundled
	Estimated bool   // true => Cost is a conservative internal estimate, not a live quote
	Note      string // short human-readable qualifier
}

// railGroundSearcher is the seam used to fetch a live ground quote for the
// station-to-hub pair. It defaults to ground.SearchByName and is overridable in
// tests so the detector can be exercised without live network calls.
var railGroundSearcher = func(ctx context.Context, from, to, date string, opts ground.SearchOptions) (*models.GroundSearchResult, error) {
	return ground.SearchByName(ctx, from, to, date, opts)
}

// resolveRailLegCost prices the rail segment for a station-to-hub pair.
//
// When the search origin IS the airline hub (e.g. AMS, FRA, CDG), the Air&Rail
// train is bundled into the flight ticket, so the headline cost is 0 with an
// honest note; a standalone live quote (with conservative estimate fallback) is
// still resolved and appended to the note as the separate-ticket price when the
// traveller skips the bundle.
//
// When the origin is a nearby alias airport (e.g. ANR, BRU), the substitute
// origin flies via a different hub, so the rail leg is a real out-of-pocket cost
// the traveller pays — the live quote (or labelled estimate) becomes the
// headline Cost so net savings can subtract it.
//
// A ground-provider failure degrades gracefully to a conservative estimate
// labelled as such; it never aborts the search.
func resolveRailLegCost(ctx context.Context, origin string, st railFlyStation, date string) railLegCost {
	standalone := standaloneRailQuote(ctx, st, date)

	// Alias origin: the rail leg is a real, separately-paid cost.
	if origin != st.HubIATA {
		return standalone
	}

	// Hub origin: the train is bundled in the ticket (cost 0); the standalone
	// price still rides along in the note for travellers who book it separately.
	bundled := railLegCost{
		Cost:     0,
		Currency: "EUR",
		Provider: st.TrainProvider,
		Note:     "included in airline ticket (bundled Air&Rail fare)",
	}
	if standalone.Note != "" {
		bundled.Note = bundled.Note + "; standalone leg " + standalone.Note
	}
	return bundled
}

// standaloneRailQuote fetches the standalone price of the rail leg, falling back
// to a conservative estimate on any provider failure, nil result, unsuccessful
// search, or empty route list. It never fabricates a price: the estimate path is
// explicitly labelled with Estimated=true.
func standaloneRailQuote(ctx context.Context, st railFlyStation, date string) railLegCost {
	estCost := groundCostBetween(st.IATA, st.HubIATA)
	estimate := railLegCost{
		Cost:      estCost,
		Currency:  "EUR",
		Estimated: true,
		Note:      fmt.Sprintf("estimate ~%.0f EUR", estCost),
	}

	gr, err := railGroundSearcher(ctx, st.City, st.HubIATA, date, ground.SearchOptions{
		Currency: "EUR",
		Type:     "train",
	})
	if err != nil || gr == nil || !gr.Success || len(gr.Routes) == 0 {
		return estimate
	}

	best := -1.0
	var provider, currency string
	for _, r := range gr.Routes {
		if r.Price > 0 && (best < 0 || r.Price < best) {
			best = r.Price
			provider = r.Provider
			currency = r.Currency
		}
	}
	if best < 0 {
		return estimate
	}
	if currency == "" {
		currency = "EUR"
	}
	if provider == "" {
		provider = st.TrainProvider
	}
	return railLegCost{
		Cost:      best,
		Currency:  currency,
		Provider:  provider,
		Estimated: false,
		Note:      fmt.Sprintf("live quote %.0f %s via %s", best, currency, provider),
	}
}

// attachRailLegCost records the priced rail leg on a hack and appends an honest
// note to the steps so the saving is transparent. Airline-bundled rail is
// represented as cost 0 with the bundled note; the standalone fallback price
// rides along in the note for travellers who book the train separately.
func attachRailLegCost(h *Hack, leg railLegCost) {
	if h == nil {
		return
	}
	h.RailCost = leg.Cost
	h.RailCostCurrency = leg.Currency
	h.RailProvider = leg.Provider
	h.RailCostEstimated = leg.Estimated
	h.RailCostNote = leg.Note

	if leg.Note != "" {
		h.Steps = append(h.Steps, "Rail leg cost: "+leg.Note)
	}
}
