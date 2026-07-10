package models

// HackSaving is the single best money-saving option the travel-hacks savings
// engine found when auto-composed into a normal flight or ground search. It is
// attached to a search result so savings surface by default, rather than only
// when the user separately runs the standalone hacks command.
//
// Honesty contract: a HackSaving is only ever populated when a detector found a
// real, lower-priced synthesized option (Savings > 0 and Price < NaivePrice).
// The naive result is never replaced — the cheapest naive option still leads the
// result list; HackSaving is an additive "here is a cheaper way" delta that the
// traveller must verify (see Risks).
type HackSaving struct {
	// Type is the detector that produced the option (e.g. "hidden_city",
	// "split", "positioning", "rail_fly", "multimodal_skip_flight").
	Type string `json:"type"`
	// Title is the human-readable hack name.
	Title string `json:"title"`
	// Description explains the option to the traveller.
	Description string `json:"description"`
	// Price is the synthesized option's total price, strictly below NaivePrice.
	Price float64 `json:"price"`
	// NaivePrice is the cheapest naive fare/route the search returned — the
	// baseline the saving is measured against.
	NaivePrice float64 `json:"naive_price"`
	// Savings is NaivePrice - Price, in Currency.
	Savings float64 `json:"savings"`
	// SavingsPct is the saving as a percentage of NaivePrice (one decimal).
	SavingsPct float64 `json:"savings_pct"`
	// Currency is the currency for Price / NaivePrice / Savings.
	Currency string `json:"currency"`
	// Risks carries the detector's caveats verbatim (e.g. hidden-city contract
	// violations, throwaway return-leg cancellation). Never stripped.
	Risks []string `json:"risks,omitempty"`
	// Steps is how to execute the option.
	Steps []string `json:"steps,omitempty"`
	// Citations are booking URLs / provider names backing the option.
	Citations []string `json:"citations,omitempty"`

	// Candidates, when populated, carries concrete bookable FlightResults
	// (real legs + real price) that were produced by the detector for this
	// saving (e.g. the actual flights searched from a rail station for
	// rail_fly_arbitrage). The ApplySharedFlightPolicy layer appends copies
	// of these into the ranked Flights list when the hack category is not
	// suppressed. Never fabricate — only real results from a search are put here.
	Candidates []FlightResult `json:"candidates,omitempty"`
}
