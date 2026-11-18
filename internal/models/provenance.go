package models

// PriceProvenance says where a ground price came from, so a static table or a
// day-best minimum is never presented as a fare for the shown departure.
type PriceProvenance string

const (
	// PriceLive is one fare for this departure, parsed from the upstream answer.
	PriceLive PriceProvenance = "live"
	// PriceEstimated is a static table value, a day-best, a range low or a
	// calendar minimum: indicative, not a quote for this departure.
	PriceEstimated PriceProvenance = "estimated"
	// PriceUnavailable means no fare is known; Price is 0.
	PriceUnavailable PriceProvenance = "unavailable"
)

// FreshnessStatic classifies an estimated price: it comes from a table or an
// aggregate, so it has no retrieval age to judge.
const FreshnessStatic = "static"
