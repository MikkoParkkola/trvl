package models

// GroundSearchResult holds the results from searching bus/train providers.
type GroundSearchResult struct {
	Success bool          `json:"success"`
	Count   int           `json:"count"`
	Routes  []GroundRoute `json:"routes"`
	// HackSaving, when non-nil, is the single best money-saving option the
	// travel-hacks savings engine auto-composed from this naive search (e.g. a
	// multimodal or cross-border-rail option cheaper than the naive cheapest
	// route). Additive — the naive Routes are never replaced — and only
	// populated when a real, lower-priced option exists.
	HackSaving *HackSaving `json:"hack_saving,omitempty"`
	// Destination, when non-nil, carries best-effort destination intelligence
	// (weather, safety, holidays, currency, country facts) for the arrival
	// location. Additive and silently degrading — absent when the lookup fails
	// or no destination is known; never blocks the core route search.
	Destination *DestinationInfo `json:"destination,omitempty"`
	// ProviderStatuses reports the outcome of each ground provider (bus / train
	// / ferry) that was attempted, mirroring flight and hotel search. It makes
	// PARTIAL failures honest: when one provider rate-limits or times out but
	// others return routes, the failure is recorded here instead of being
	// silently dropped (the legacy Error field is only populated on a total
	// wipeout). When a provider returns 0 routes without an error its status is
	// "ok" with Results=0 — distinct from "failed"/"timeout"/"skipped".
	ProviderStatuses []ProviderStatus `json:"provider_statuses,omitempty"`
	// Completeness is the composite evidence summary derived from
	// ProviderStatuses. When State != "complete", callers MUST NOT claim
	// "no routes found" — some providers timed out or failed.
	Completeness Completeness `json:"completeness,omitempty"`
	Error        string       `json:"error,omitempty"`
}

// GroundRoute represents a single bus or train connection.
type GroundRoute struct {
	Provider string `json:"provider"` // "flixbus", "regiojet"
	Type     string `json:"type"`     // "bus", "train", "mixed"
	// Direction marks which half of a round-trip this route belongs to:
	// "outbound" (origin->destination on the departure date) or "inbound"
	// (destination->origin on the return date). Empty for one-way searches.
	// Mirrors models.FlightLeg.Direction so round-trip ground results are
	// honestly labelled rather than silently flattening the return leg away.
	Direction  string      `json:"direction,omitempty"`
	Price      float64     `json:"price"`
	PriceMax   float64     `json:"price_max,omitempty"` // RegioJet gives price ranges
	Currency   string      `json:"currency"`
	Duration   int         `json:"duration_minutes"`
	Departure  GroundStop  `json:"departure"`
	Arrival    GroundStop  `json:"arrival"`
	Transfers  int         `json:"transfers"`
	Legs       []GroundLeg `json:"legs"`
	Amenities  []string    `json:"amenities,omitempty"`
	SeatsLeft  *int        `json:"seats_left,omitempty"`
	BookingURL string      `json:"booking_url"`
	// Sources lists every provider that returned this same physical connection
	// (mirrors HotelResult). Populated by ResolveGroundSources. Headline Price
	// is the cheapest across sources.
	Sources        []PriceSource `json:"sources,omitempty"`
	Savings        float64       `json:"savings,omitempty"`
	CheapestSource string        `json:"cheapest_source,omitempty"`
	// Confidence is an honest bookability assessment of the headline Price
	// (innovation #3). Nil when not scored; an unrated Confidence means trvl
	// lacked the signal to judge — it is never a fabricated number.
	Confidence *Confidence `json:"confidence,omitempty"`
	// SeaState is an optional, free Open-Meteo Marine sea-state enrichment for
	// ferry legs (wave height + a coarse calm/moderate/rough label). Nil unless
	// the route is a ferry and the lookup succeeded — it is never blocking and
	// never fabricated.
	SeaState *SeaState `json:"sea_state,omitempty"`
}

// SeaState is a coarse sea-state forecast for a ferry leg, sourced from the
// keyless Open-Meteo Marine API. It is a "nice to have" enrichment: absent when
// coordinates are unavailable or the lookup fails.
type SeaState struct {
	// WaveHeight is the maximum significant wave height in metres.
	WaveHeight float64 `json:"wave_height_m"`
	// SwellHeight is the maximum swell wave height in metres (0 if unavailable).
	SwellHeight float64 `json:"swell_height_m,omitempty"`
	// Label is a coarse human-readable state: "calm", "moderate", or "rough".
	Label string `json:"label"`
}

// GroundStop represents a departure or arrival point.
type GroundStop struct {
	City    string `json:"city"`
	Station string `json:"station,omitempty"`
	Time    string `json:"time"` // ISO 8601
}

// GroundLeg represents one segment of a multi-leg ground journey.
type GroundLeg struct {
	Type      string     `json:"type"` // "bus", "train"
	Provider  string     `json:"provider"`
	Departure GroundStop `json:"departure"`
	Arrival   GroundStop `json:"arrival"`
	Duration  int        `json:"duration_minutes"`
	Amenities []string   `json:"amenities,omitempty"`
}
