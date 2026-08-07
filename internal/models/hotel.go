package models

import (
	"strings"
	"time"
)

// adultsOnlyMarkers are case-insensitive substrings that, when present in a
// hotel's name or description, indicate the property does not accept children.
// Kept here (in the shared type package) so every provider path can reuse the
// same detection logic via IsAdultsOnly.
var adultsOnlyMarkers = []string{
	"adults only",
	"adults-only",
	"adult only",
	"adults recommended",
}

// IsAdultsOnly reports whether the given hotel name or description marks the
// property as adults-only (i.e. not suitable for parties with children).
// Matching is case-insensitive across a fixed set of common marker phrases.
func IsAdultsOnly(name, description string) bool {
	hay := strings.ToLower(name + " " + description)
	for _, m := range adultsOnlyMarkers {
		if strings.Contains(hay, m) {
			return true
		}
	}
	return false
}

// PriceSource tracks which provider found a result at what price.
// When multiple providers return the same hotel, all sources are preserved
// and the lowest price becomes the primary HotelResult.Price.
type PriceSource struct {
	Provider        string  `json:"provider"` // "google_hotels", "trivago", "airbnb", "booking"
	Price           float64 `json:"price"`
	MaxPrice        float64 `json:"max_price,omitempty"` // highest room/block price (when available)
	Currency        string  `json:"currency"`
	RoomCount       int     `json:"room_count,omitempty"` // distinct room types (when available)
	BookingURL      string  `json:"booking_url,omitempty"`
	PriceBasis      string  `json:"price_basis,omitempty"`      // lead_in|room_nightly|room_total|tax_inclusive_total
	PriceConfidence string  `json:"price_confidence,omitempty"` // unverified|room_level|verified
	// RetrievedAt is when trvl obtained this price; Freshness classifies it
	// (live|recent|stale) so renderers can avoid superlatives on stale prices.
	RetrievedAt time.Time `json:"retrieved_at,omitempty"`
	Freshness   string    `json:"freshness,omitempty"`
}

// Room represents a single bookable room type at a property.
// Prices depend on guest count — the search guests parameter determines
// which rates are shown. Rich room data enables LLM reasoning about
// room selection ("which room has a balcony?", "cheapest with breakfast?").
type Room struct {
	Name               string   `json:"name"`            // e.g. "Standard Double Room", "Superior Suite"
	Price              float64  `json:"price,omitempty"` // price for this room type (for the searched guest count)
	NightlyPrice       float64  `json:"nightly_price,omitempty"`
	TotalPrice         float64  `json:"total_price,omitempty"`
	TaxesAndFees       float64  `json:"taxes_and_fees,omitempty"`
	TaxesFeesIncluded  *bool    `json:"taxes_fees_included,omitempty"`
	Currency           string   `json:"currency,omitempty"`
	Provider           string   `json:"provider,omitempty"`
	ProviderURL        string   `json:"provider_url,omitempty"`
	RateID             string   `json:"rate_id,omitempty"`
	RatePlanName       string   `json:"rate_plan_name,omitempty"`
	MatchConfidence    string   `json:"match_confidence,omitempty"`
	PriceBasis         string   `json:"price_basis,omitempty"`
	PriceConfidence    string   `json:"price_confidence,omitempty"`
	SizeM2             float64  `json:"size_m2,omitempty"`           // room size in square meters
	MaxGuests          int      `json:"max_guests,omitempty"`        // maximum occupancy
	BedType            string   `json:"bed_type,omitempty"`          // e.g. "1 double bed", "2 single beds"
	Amenities          []string `json:"amenities,omitempty"`         // room-level amenities (balcony, minibar, bathtub, etc.)
	FreeCancellation   bool     `json:"free_cancellation,omitempty"` // free cancellation available
	Refundable         *bool    `json:"refundable,omitempty"`
	CancellationPolicy string   `json:"cancellation_policy,omitempty"`
	BreakfastIncluded  bool     `json:"breakfast_included,omitempty"` // breakfast included in price
	Board              string   `json:"board,omitempty"`
	Description        string   `json:"description,omitempty"` // room description text
}

// HotelResult represents a single hotel from a search.
type HotelResult struct {
	Name        string  `json:"name"`
	HotelID     string  `json:"hotel_id"`
	Rating      float64 `json:"rating"`
	ReviewCount int     `json:"review_count"`
	Stars       int     `json:"stars"`
	Price       float64 `json:"price"` // Lowest price across all sources
	Currency    string  `json:"currency"`
	// ComparablePrice is the headline price converted to a common target currency
	// for cross-currency ranking and Min/MaxPrice filtering (and PriceForRanking).
	// 0 means the price could not be converted (incomparable across currencies).
	// NOTE: this is a CURRENCY axis (matches GroundRoute.ComparablePrice). It is
	// NOT the same as FlightResult.ComparablePrice, which is a FEES axis (all-in
	// cost, same currency). Do not read comparable_price generically across the
	// three result types — the semantics differ by type.
	ComparablePrice float64       `json:"comparable_price,omitempty"`
	Address         string        `json:"address"`
	Description     string        `json:"description,omitempty"` // property tagline or summary
	ImageURL        string        `json:"image_url,omitempty"`   // main property image
	RoomTypes       []Room        `json:"room_types,omitempty"`  // available rooms with names and prices
	Lat             float64       `json:"lat"`
	Lon             float64       `json:"lon"`
	Neighborhood    string        `json:"neighborhood,omitempty"`  // e.g. "Montmartre", "Le Marais"
	DistanceKm      float64       `json:"distance_km,omitempty"`   // km from city center
	PropertyType    string        `json:"property_type,omitempty"` // hotel|hostel|apartment|vacation_rental|...
	Amenities       []string      `json:"amenities,omitempty"`
	BookingURL      string        `json:"booking_url,omitempty"`
	EcoCertified    bool          `json:"eco_certified,omitempty"`
	AdultsOnly      bool          `json:"adults_only,omitempty"`      // property does not accept children (detected from name/description)
	Sources         []PriceSource `json:"sources,omitempty"`          // All providers that found this hotel
	Savings         float64       `json:"savings,omitempty"`          // price difference: most expensive source - cheapest source
	CheapestSource  string        `json:"cheapest_source,omitempty"`  // provider name of cheapest source
	PriceBasis      string        `json:"price_basis,omitempty"`      // basis for primary Price
	PriceConfidence string        `json:"price_confidence,omitempty"` // confidence for primary Price
	RetrievedAt     time.Time     `json:"retrieved_at,omitempty"`     // checked time for primary Price
	Freshness       string        `json:"freshness,omitempty"`        // freshness for primary Price
	PriceWarnings   []string      `json:"price_warnings,omitempty"`   // machine-readable caveats, e.g. mixed_source_currencies
	// Confidence is an honest bookability assessment of the headline Price
	// (innovation #3). Nil when not scored; an unrated Confidence means trvl
	// lacked the signal to judge — it is never a fabricated number.
	Confidence *Confidence `json:"confidence,omitempty"`
}

// ProviderStatus reports the outcome of a single external provider query.
// Included in search responses so the orchestrating LLM can autonomously
// diagnose and fix broken providers.
type ProviderStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`                  // "ok", "error", "disabled"
	Results     int    `json:"results,omitempty"`       // number of results returned
	Error       string `json:"error,omitempty"`         // error message if status != "ok"
	FixHint     string `json:"fix_hint,omitempty"`      // actionable hint for the LLM
	FixHintCode string `json:"fix_hint_code,omitempty"` // typed root-cause code (e.g. "AKAMAI_BLOCK")
}

// HotelSearchResult is the top-level response for a hotel search.
type HotelSearchResult struct {
	Success          bool             `json:"success"`
	Count            int              `json:"count"`
	TotalAvailable   int              `json:"total_available,omitempty"`
	Hotels           []HotelResult    `json:"hotels"`
	ProviderStatuses []ProviderStatus `json:"provider_statuses,omitempty"`
	Completeness     Completeness     `json:"completeness,omitempty"`
	// Destination carries best-effort destination intelligence (weather, safety,
	// holidays, currency, country facts) for the searched location. Additive and
	// silently degrading — absent when the lookup fails; never blocks the search.
	Destination *DestinationInfo `json:"destination,omitempty"`
	Error       string           `json:"error,omitempty"`
}

// ProviderPrice represents a single booking provider's price for a hotel.
type ProviderPrice struct {
	Provider        string  `json:"provider"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
	NightlyPrice    float64 `json:"nightly_price,omitempty"`
	TotalPrice      float64 `json:"total_price,omitempty"`
	ProviderURL     string  `json:"provider_url,omitempty"`
	PriceBasis      string  `json:"price_basis,omitempty"`
	PriceConfidence string  `json:"price_confidence,omitempty"`
	// Official is a positive-only upstream fact: the source explicitly marked
	// this seller as the property's official site. False means only that no such
	// signal was supplied; trvl never renders it as "not official" and never
	// infers it from a hostname or provider-name list (trvl#535).
	Official bool `json:"official,omitempty"`
	// LinkDurability classifies ProviderURL so an agent knows whether the link
	// is safe to hand to a user or likely to expire: "stable" (direct OTA),
	// "expiring" (a google.com/aclk ad-click redirect, good for a day or two),
	// or "" when no link is present. Dead vacation-rental redirects
	// (google.com/travel/clk) are stripped before this is set. See #168.
	LinkDurability string `json:"link_durability,omitempty"`
	// TaxAddedAtCheckout is true when the provider's shown total equals its
	// pre-tax figure, meaning taxes/fees will be added at checkout and the
	// quoted price will grow. Set only when both figures are known. See #171.
	TaxAddedAtCheckout bool `json:"tax_added_at_checkout,omitempty"`
	// FreeCancellation reports whether this seller's rate is refundable, and is
	// nil when the seller said nothing. Three states, not two: refundable,
	// stated as non-refundable, and UNKNOWN (trvl#535, TRVL.TRUST.4).
	//
	// Nil rather than false for unknown, because the upstream flag is a positive
	// badge that is simply absent when there is no free-cancellation offer. A
	// plain bool would render that absence as "not refundable", which is a claim
	// the source never made -- and the whole of this ticket's lineage is about
	// not presenting a guess as a fact.
	//
	// So this is only ever set to true from an explicit upstream signal. It is
	// never inferred from price, brand or rate name.
	FreeCancellation *bool `json:"free_cancellation,omitempty"`
	// FreeCancellationUntil is the deadline the seller stated, when it gave one.
	// Free-form as received; trvl does not parse or re-render it, because a
	// misparsed cancellation deadline is worse than an unparsed one.
	FreeCancellationUntil string `json:"free_cancellation_until,omitempty"`
}

// HotelPriceResult is the top-level response for a hotel price lookup.
type HotelPriceResult struct {
	Success   bool            `json:"success"`
	HotelID   string          `json:"hotel_id"`
	Name      string          `json:"name"`
	CheckIn   string          `json:"check_in"`
	CheckOut  string          `json:"check_out"`
	Providers []ProviderPrice `json:"providers"`
	// Notice carries a human-readable, non-error explanation when the
	// upstream returned a structurally valid response that simply contains
	// no booking partner prices. Distinct from Error, which signals a hard
	// failure (HTTP/decode/parse). When Notice is set Success is still true.
	Notice string `json:"notice,omitempty"`
	Error  string `json:"error,omitempty"`
	// BookingFallbackURL is a durable Booking.com property+date deep-link that
	// never 404s, attached alongside provider links that may expire. See #168.
	BookingFallbackURL string `json:"booking_fallback_url,omitempty"`
	// TouristTaxNote flags that a local tourist/city tax may be payable in cash
	// at the property and is not included in any online total. It is descriptive
	// only — never a numeric estimate, and never folded into ranking, since it
	// is roughly equal across candidates at a destination. See #169.
	TouristTaxNote string `json:"tourist_tax_note,omitempty"`
}

// PriceForRanking returns the normalized comparable price when available,
// falling back to raw Price. Cross-currency-safe ranking uses this.
func (h HotelResult) PriceForRanking() float64 {
	if h.ComparablePrice > 0 {
		return h.ComparablePrice
	}
	return h.Price
}
