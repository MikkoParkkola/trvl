package afklm

// AvailableOffersRequest is the request body for POST /opendata/offers/v3/available-offers.
type AvailableOffersRequest struct {
	CommercialCabins      []string              `json:"commercialCabins,omitempty"`
	BookingFlow           string                `json:"bookingFlow"`
	Passengers            []Passenger           `json:"passengers"`
	RequestedConnections  []RequestedConnection `json:"requestedConnections"`
	Currency              string                `json:"currency,omitempty"`
	FareOption            string                `json:"fareOption,omitempty"`
	WithUpsellCabins      bool                  `json:"withUpsellCabins,omitempty"`
	LowestFareCombination bool                  `json:"lowestFareCombination,omitempty"`
}

// Passenger represents a traveller in the request.
type Passenger struct {
	ID   int    `json:"id"`
	Type string `json:"type"` // ADT = adult
}

// RequestedConnection represents one leg of the journey.
type RequestedConnection struct {
	DepartureDate string `json:"departureDate"` // ISO calendar date "2006-01-02"
	Origin        Place  `json:"origin"`
	Destination   Place  `json:"destination"`
}

// Place identifies an airport, railway station, city, etc.
type Place struct {
	Type string `json:"type"` // AIRPORT, RAILWAY_STATION, CITY, STOPOVER, etc.
	Code string `json:"code"` // IATA code
}

// AvailableOffersResponse is the top-level response from available-offers.
// The AF-KLM API uses Rich JSON Format (RJF) normalization: pricing metadata
// lives under recommendations[].flightProducts[].connections[], while actual
// flight details (airports, carriers, datetimes) live in the top-level
// Connections array indexed by bound (0=outbound, 1=return).
type AvailableOffersResponse struct {
	Recommendations []Recommendation `json:"recommendations"`
	// Connections is [bound_index][connection_options] — top-level flight detail.
	// bound_index 0 is outbound; 1 is the return bound when present.
	Connections [][]BoundConnection `json:"connections"`
}

// Recommendation is a priced bookable bundle.
type Recommendation struct {
	FlightProducts []FlightProduct `json:"flightProducts"`
}

// FlightProduct is a priced itinerary option within a recommendation.
type FlightProduct struct {
	ID          string              `json:"id"`
	Connections []PricingConnection `json:"connections"`
	Price       Price               `json:"price"`
}

// PricingConnection sits under flightProducts and carries the price, cabin,
// fare family and a connectionId linking to the top-level BoundConnection.
// The segments[] inside pricing contains only fare-class metadata (cabinClassCode,
// sellingClassCode, fareBasisCode) — no flight details.
type PricingConnection struct {
	ConnectionID           int        `json:"connectionId"`
	NumberOfSeatsAvailable int        `json:"numberOfSeatsAvailable"`
	FareFamily             FareFamily `json:"fareFamily"`
	CommercialCabin        string     `json:"commercialCabin"`
	CommercialCabinLabel   string     `json:"commercialCabinLabel"`
	Price                  Price      `json:"price"`
}

// FareFamily identifies the fare product family.
type FareFamily struct {
	Code      string `json:"code"`
	Hierarchy int    `json:"hierarchy,omitempty"`
}

// BoundConnection is a top-level routing option (an ordered chain of segments).
type BoundConnection struct {
	ID            int       `json:"id"`
	DateVariation int       `json:"dateVariation"`
	Duration      int       `json:"duration"` // minutes
	Segments      []Segment `json:"segments"`
}

// Segment is a single flight segment within a BoundConnection.
type Segment struct {
	Origin            SegmentPlace    `json:"origin"`
	Destination       SegmentPlace    `json:"destination"`
	MarketingFlight   MarketingFlight `json:"marketingFlight"`
	DepartureDateTime string          `json:"departureDateTime"` // "2006-01-02T15:04:05"
	ArrivalDateTime   string          `json:"arrivalDateTime"`
	Duration          int             `json:"duration"` // minutes
}

// SegmentPlace is a departure or arrival point in a segment.
type SegmentPlace struct {
	Code string      `json:"code"`
	Name string      `json:"name"`
	City SegmentCity `json:"city"`
}

// SegmentCity is the city containing the airport.
type SegmentCity struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// MarketingFlight holds the flight number and marketing carrier.
type MarketingFlight struct {
	Number  string           `json:"number"`
	Carrier MarketingCarrier `json:"carrier"`
}

// MarketingCarrier holds carrier code and name.
type MarketingCarrier struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Price contains fare amounts in the requested currency.
type Price struct {
	DisplayPrice float64 `json:"displayPrice"`
	TotalPrice   float64 `json:"totalPrice"`
	Currency     string  `json:"currency"`
}

// LowestFaresRequest is the request body for POST /opendata/offers/v3/lowest-fares-by-destination.
type LowestFaresRequest struct {
	BookingFlow       string `json:"bookingFlow"`
	DestinationCities string `json:"destinationCities"` // comma-separated, embedded quotes per API quirk
	Type              string `json:"type"`              // "DAY"
	FromDate          string `json:"fromDate"`          // RFC3339
	UntilDate         string `json:"untilDate"`         // RFC3339
	Origin            Place  `json:"origin"`
}

// LowestFaresResponse is the response from lowest-fares-by-destination.
type LowestFaresResponse struct {
	DestinationFares []DestinationFare `json:"destinationFares"`
}

// DestinationFare is the cheapest fare found for a single destination.
type DestinationFare struct {
	Destination Place  `json:"destination"`
	Price       Price  `json:"price"`
	DepartDate  string `json:"departDate,omitempty"`
}
