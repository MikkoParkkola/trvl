package models

// GroundSearchResult holds the results from searching bus/train providers.
type GroundSearchResult struct {
	Success bool          `json:"success"`
	Count   int           `json:"count"`
	Routes  []GroundRoute `json:"routes"`
	Error   string        `json:"error,omitempty"`
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
