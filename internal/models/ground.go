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
	Error      string      `json:"error,omitempty"`
}

// GroundRoute represents a single bus or train connection.
type GroundRoute struct {
	Provider   string      `json:"provider"` // "flixbus", "regiojet"
	Type       string      `json:"type"`     // "bus", "train", "mixed"
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
