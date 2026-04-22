package afklm

// NOTE: The klmAward* types below (klmAwardSearchRequest, klmAwardResponse, and
// all nested types) are HYPOTHESIZED based on the public AFKL Offers API schema.
// They were NOT verified against live klm.com traffic. The actual KLM internal
// award API may use a completely different JSON structure.
//
// To verify: capture a real "Book with miles" XHR from klm.com DevTools and
// compare the actual request/response JSON against these types. Update as needed.

// AwardOffer represents a single award flight offer (miles + tax).
type AwardOffer struct {
	// Date is the departure date (ISO calendar date "2006-01-02").
	Date string
	// FlightNumber is the marketing carrier + flight number, e.g. "KL1234".
	FlightNumber string
	// Origin is the IATA airport code of the departure airport.
	Origin string
	// Destination is the IATA airport code of the arrival airport.
	Destination string
	// DepartureTime is the local departure time, e.g. "08:40".
	DepartureTime string
	// ArrivalTime is the local arrival time, e.g. "10:00".
	ArrivalTime string
	// Miles is the miles price for one adult passenger.
	Miles int
	// TaxEUR is the tax/surcharge in EUR for one adult passenger.
	TaxEUR float64
	// Cabin is the commercial cabin class, e.g. "ECONOMY".
	Cabin string
	// Stops is the number of intermediate stops.
	Stops int
	// Available indicates seats are bookable (not sold out).
	Available bool
}

// AwardScanResult is the aggregated result from AwardScanner.ScanMonth.
type AwardScanResult struct {
	Origin      string
	Destination string
	Offers      []AwardOffer
	// Errors contains per-date errors (date -> error message) for partial failures.
	Errors map[string]string
}

// Suitable reports whether the offer meets the ideal miles ceiling (≤15,000 mi).
func (o AwardOffer) Suitable() bool { return o.Available && o.Miles > 0 && o.Miles <= 15_000 }

// Ideal reports whether the offer meets the ideal miles ceiling (≤10,000 mi).
func (o AwardOffer) Ideal() bool { return o.Available && o.Miles > 0 && o.Miles <= 10_000 }

// klmAwardSearchRequest is the HYPOTHESIZED JSON body for the KLM internal award
// API. The actual request shape used by klm.com's SPA has not been verified.
// Endpoint (unverified): POST https://www.klm.com/api/flights/award
type klmAwardSearchRequest struct {
	BookingFlow  string              `json:"bookingFlow"` // "REWARD"
	Passengers   []klmAwardPassenger `json:"passengers"`
	Connections  []klmAwardLeg       `json:"requestedConnections"`
	Currency     string              `json:"currency"` // "EUR"
	CommerCabins []string            `json:"commercialCabins,omitempty"`
}

type klmAwardPassenger struct {
	ID           int                `json:"id"`
	Type         string             `json:"type"` // "ADT"
	LoyaltyCards []klmLoyaltyCard   `json:"loyaltyCards,omitempty"`
}

type klmLoyaltyCard struct {
	ProgramCode string `json:"programCode"` // "FB"
	Number      string `json:"number"`
}

type klmAwardLeg struct {
	DepartureDate string   `json:"departureDate"` // "2026-06-15"
	Origin        klmPlace `json:"origin"`
	Destination   klmPlace `json:"destination"`
}

type klmPlace struct {
	Type string `json:"type"` // "AIRPORT"
	Code string `json:"code"` // IATA code
}

// klmAwardResponse mirrors the shape of the KLM internal API award response.
// The structure is similar to the public AFKL AvailableOffersResponse but
// recommendations include milesPrice instead of a cash price.
type klmAwardResponse struct {
	Recommendations []klmAwardRecommendation `json:"recommendations"`
	Connections     [][]klmAwardBoundConn    `json:"connections"`
}

type klmAwardRecommendation struct {
	FlightProducts []klmAwardProduct `json:"flightProducts"`
}

type klmAwardProduct struct {
	ID          string               `json:"id"`
	Connections []klmAwardPricedConn `json:"connections"`
	MilesPrice  klmMilesPrice        `json:"milesPrice"`
	CashPrice   Price                `json:"price"`
}

type klmAwardPricedConn struct {
	ConnectionID int           `json:"connectionId"`
	MilesPrice   klmMilesPrice `json:"milesPrice"`
	CashPrice    Price         `json:"price"`
	Cabin        string        `json:"commercialCabin"`
}

type klmMilesPrice struct {
	Miles    int     `json:"miles"`
	TaxEUR   float64 `json:"taxAmount"`
	Currency string  `json:"currency"` // "EUR" for tax
}

type klmAwardBoundConn struct {
	ID       int            `json:"id"`
	Duration int            `json:"duration"` // minutes
	Segments []klmAwardSeg  `json:"segments"`
}

type klmAwardSeg struct {
	Origin            SegmentPlace    `json:"origin"`
	Destination       SegmentPlace    `json:"destination"`
	MarketingFlight   MarketingFlight `json:"marketingFlight"`
	DepartureDateTime string          `json:"departureDateTime"`
	ArrivalDateTime   string          `json:"arrivalDateTime"`
	Duration          int             `json:"duration"`
}
