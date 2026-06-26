package hacks

import (
	"context"
	"fmt"
)

// Bundle leg transport modes.
const (
	BundleLegRail   = "rail"
	BundleLegFlight = "flight"
)

// BundleLeg is one segment of a composed multimodal itinerary. It carries its
// own timing and price so the whole journey is auditable leg by leg.
type BundleLeg struct {
	Mode            string  `json:"mode"`     // "rail" | "flight"
	From            string  `json:"from"`     // origin code/city of this leg
	To              string  `json:"to"`       // destination code/city of this leg
	Provider        string  `json:"provider"` // operator (e.g. "Eurostar", "KLM")
	DurationMinutes int     `json:"duration_minutes,omitempty"`
	Cost            float64 `json:"cost"` // leg cost in Currency; 0 when bundled into another leg's fare
	Currency        string  `json:"currency"`
	Estimated       bool    `json:"estimated,omitempty"` // true when Cost is an internal estimate, not a live quote
}

// RailFlyBundle is a composed rail+fly itinerary priced as a SINGLE total. It is
// exposed on Hack.Bundle so the saving is one combined number, not a pair of
// separate "rail" and "flight" figures. Per-leg timing plus the change window
// and connection-guarantee status make the whole multimodal journey auditable.
type RailFlyBundle struct {
	Legs      []BundleLeg `json:"legs"`
	TotalCost float64     `json:"total_cost"` // the single combined price of every leg
	Currency  string      `json:"currency"`

	// ChangeWindowMinutes is the recommended buffer between the rail arrival and
	// the flight departure (or vice-versa on the return leg).
	ChangeWindowMinutes int `json:"change_window_minutes"`
	// ConnectionGuaranteed is true when the airline sells the rail leg as part of
	// the ticket (Air&Rail), so a missed train is the carrier's responsibility.
	// False for a self-transfer alias origin where the traveller carries the risk.
	ConnectionGuaranteed bool `json:"connection_guaranteed"`
	// ConnectionStatus is the human-readable guarantee status, e.g.
	// "guaranteed (Air&Rail)" or "self-transfer — not guaranteed".
	ConnectionStatus string `json:"connection_status"`
}

// legCostSum returns the combined cost of every leg — the single total a
// composed bundle exposes.
func (b *RailFlyBundle) legCostSum() float64 {
	if b == nil {
		return 0
	}
	var total float64
	for _, l := range b.Legs {
		total += l.Cost
	}
	return total
}

// composeRailFlyBundle prices the rail leg + the KL/AF flight leg + (for
// round-trips) the return as a SINGLE total, returning a Hack whose saving is
// one combined number rather than separate rail and flight figures.
//
// The flight fare (flightCost) is supplied by the caller (already searched); the
// rail leg is priced via internal/ground with a deterministic groundCostBetween
// fallback (resolveRailLegCost), so the bundle never makes the saving dishonest
// and never requires a live call to succeed. baseline is the comparison fare
// (e.g. the direct airport price) used to derive the single Savings figure.
func composeRailFlyBundle(ctx context.Context, origin, destination, departDate, returnDate string, st railFlyStation, flightCost, baseline float64, flightCurrency string) Hack {
	if flightCurrency == "" {
		flightCurrency = "EUR"
	}

	railLeg := resolveRailLegCost(ctx, origin, st, departDate)
	roundTrip := returnDate != ""
	// bundled is true when the search origin IS the airline hub, so the Air&Rail
	// train is included in (and protected by) the ticket.
	bundled := origin == st.HubIATA

	legs := []BundleLeg{
		{
			Mode:            BundleLegRail,
			From:            st.City,
			To:              st.HubIATA,
			Provider:        st.TrainProvider,
			DurationMinutes: st.TrainMinutes,
			Cost:            railLeg.Cost,
			Currency:        railLeg.Currency,
			Estimated:       railLeg.Estimated,
		},
		{
			Mode:     BundleLegFlight,
			From:     st.HubIATA,
			To:       destination,
			Provider: st.AirlineName,
			Cost:     flightCost,
			Currency: flightCurrency,
		},
	}

	if roundTrip {
		// The return flight is already included in the round-trip fare
		// (flightCost), so it carries cost 0 here to avoid double counting; the
		// return rail leg from the hub back to the origin city is a real,
		// separately-needed segment, priced like the outbound rail leg.
		legs = append(legs,
			BundleLeg{
				Mode:     BundleLegFlight,
				From:     destination,
				To:       st.HubIATA,
				Provider: st.AirlineName,
				Cost:     0,
				Currency: flightCurrency,
			},
			BundleLeg{
				Mode:            BundleLegRail,
				From:            st.HubIATA,
				To:              st.City,
				Provider:        st.TrainProvider,
				DurationMinutes: st.TrainMinutes,
				Cost:            railLeg.Cost,
				Currency:        railLeg.Currency,
				Estimated:       railLeg.Estimated,
			},
		)
	}

	changeWindow := 120 // self-transfer: recommend a generous buffer
	status := "self-transfer — not guaranteed (rail leg is a separate ticket)"
	if bundled {
		changeWindow = 90 // typical Air&Rail minimum connection time
		status = "guaranteed (Air&Rail — protected connection)"
	}

	bundle := &RailFlyBundle{
		Legs:                 legs,
		Currency:             flightCurrency,
		ChangeWindowMinutes:  changeWindow,
		ConnectionGuaranteed: bundled,
		ConnectionStatus:     status,
	}
	// The single total is the sum of every leg — one number, never two.
	bundle.TotalCost = roundSavings(bundle.legCostSum())

	returnNote := ""
	if roundTrip {
		returnNote = " (return incl.)"
	}

	h := Hack{
		Type:     "rail_fly_bundle",
		Title:    fmt.Sprintf("Rail+fly bundle via %s — %.0f %s total", st.City, bundle.TotalCost, flightCurrency),
		Currency: flightCurrency,
		Savings:  roundSavings(baseline - bundle.TotalCost),
		Description: fmt.Sprintf(
			"Single priced itinerary: %s rail %s→%s + %s flight %s→%s%s = %.0f %s total. "+
				"Connection: %s; recommended change window %d min.",
			st.TrainProvider, st.City, st.HubIATA,
			st.AirlineName, st.HubIATA, destination, returnNote,
			bundle.TotalCost, flightCurrency, status, changeWindow,
		),
		Steps: []string{
			fmt.Sprintf("Rail %s→%s on %s (%s, %d min)", st.City, st.HubIATA, departDate, st.TrainProvider, st.TrainMinutes),
			fmt.Sprintf("Fly %s→%s%s with %s", st.HubIATA, destination, returnNote, st.AirlineName),
			fmt.Sprintf("Allow a %d-minute change window at %s — connection is %s", changeWindow, st.HubIATA, status),
		},
		Bundle: bundle,
	}
	attachRailLegCost(&h, railLeg)
	return h
}

// composeOpenJawRailReturn builds an open-jaw hack whose outbound is a flight
// (origin → flyInto) and whose return is composed as a rail leg
// (trainOutOf → origin): fly into one city, take the train out of another.
//
// The return rail leg is priced via internal/ground with a deterministic
// groundCostBetween fallback (openJawRailReturnQuote), so the hack is fully
// offline-deterministic and the total stays a single, honest number.
func composeOpenJawRailReturn(ctx context.Context, origin, flyInto, trainOutOf string, flightCost, baseline float64, currency, departDate, returnDate string) Hack {
	if currency == "" {
		currency = "EUR"
	}

	railReturn := openJawRailReturnQuote(ctx, trainOutOf, origin, returnDate)

	legs := []BundleLeg{
		{
			Mode:     BundleLegFlight,
			From:     origin,
			To:       flyInto,
			Provider: "flight",
			Cost:     flightCost,
			Currency: currency,
		},
		{
			Mode:      BundleLegRail,
			From:      trainOutOf,
			To:        origin,
			Provider:  railReturn.Provider,
			Cost:      railReturn.Cost,
			Currency:  railReturn.Currency,
			Estimated: railReturn.Estimated,
		},
	}

	bundle := &RailFlyBundle{
		Legs:                 legs,
		Currency:             currency,
		ChangeWindowMinutes:  0, // independent bookings; no airline-protected connection
		ConnectionGuaranteed: false,
		ConnectionStatus:     "self-transfer — independent flight + rail bookings",
	}
	bundle.TotalCost = roundSavings(bundle.legCostSum())

	railNote := railReturn.Note
	if railNote == "" {
		railNote = fmt.Sprintf("%.0f %s", railReturn.Cost, railReturn.Currency)
	}

	return Hack{
		Type:     "open_jaw_rail_return",
		Title:    fmt.Sprintf("Open-jaw: fly into %s, train out of %s back to %s", flyInto, trainOutOf, origin),
		Currency: currency,
		Savings:  roundSavings(baseline - bundle.TotalCost),
		Description: fmt.Sprintf(
			"Fly %s→%s (%.0f %s) one-way, then return by rail %s→%s (%s) = %.0f %s total. "+
				"Lets you exit from a different city and skip the return flight.",
			origin, flyInto, flightCost, currency,
			trainOutOf, origin, railNote,
			bundle.TotalCost, currency,
		),
		Risks: []string{
			"Outbound flight and return rail are separate bookings — lock both in together",
			"You must make your own way to the departure rail station in " + trainOutOf,
		},
		Steps: []string{
			fmt.Sprintf("Book one-way flight %s→%s on %s (%s %.0f)", origin, flyInto, departDate, currency, flightCost),
			fmt.Sprintf("Return by train %s→%s on %s (%s)", trainOutOf, origin, returnDate, railNote),
		},
		Bundle: bundle,
	}
}

// openJawRailReturnQuote prices the rail return leg for an open-jaw itinerary,
// degrading to a deterministic, explicitly-labelled estimate on any ground
// provider failure so the hack never needs a live call to be produced.
func openJawRailReturnQuote(ctx context.Context, fromCity, toCity, date string) railLegCost {
	return railQuoteOrEstimate(ctx, fromCity, toCity, date, fromCity, toCity)
}
