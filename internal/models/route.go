package models

// RouteLeg is one segment of a multi-modal itinerary.
type RouteLeg struct {
	Mode       string  `json:"mode"`      // "flight", "train", "bus", "ferry"
	Provider   string  `json:"provider"`  // "google_flights", "flixbus", "db", "tallink", etc.
	From       string  `json:"from"`      // City name
	To         string  `json:"to"`        // City name
	FromCode   string  `json:"from_code"` // IATA code for flights, station code for others
	ToCode     string  `json:"to_code"`   // IATA code for flights, station code for others
	Departure  string  `json:"departure"` // ISO 8601 datetime
	Arrival    string  `json:"arrival"`   // ISO 8601 datetime
	Duration   int     `json:"duration"`  // minutes
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
	Transfers  int     `json:"transfers"` // internal transfers within this leg
	BookingURL string  `json:"booking_url,omitempty"`
}

// RouteItinerary is a complete multi-modal journey from origin to destination.
type RouteItinerary struct {
	Legs          []RouteLeg `json:"legs"`
	TotalPrice    float64    `json:"total_price"`
	Currency      string     `json:"currency"`
	TotalDuration int        `json:"total_duration"` // minutes including connection times
	Transfers     int        `json:"transfers"`      // number of mode changes
	DepartTime    string     `json:"depart_time"`    // first leg departure
	ArriveTime    string     `json:"arrive_time"`    // last leg arrival
}

// RouteSearchResult is the top-level response from a multi-modal route search.
type RouteSearchResult struct {
	Success     bool             `json:"success"`
	Origin      string           `json:"origin"`
	Destination string           `json:"destination"`
	Date        string           `json:"date"`
	Count       int              `json:"count"`
	Itineraries []RouteItinerary `json:"itineraries"`
	Error       string           `json:"error,omitempty"`
}
