package models

// TransferOption is one transport mode for one leg (e.g. arrival airport to
// accommodation), enriched for the comparison card. It is derived from one or
// more GroundRoute values by internal/transfer.BuildOptions. The user chooses
// among options by their own priority; trvl does not impose a single "best".
type TransferOption struct {
	Mode            string   `json:"mode"`  // "airport_express","metro","taxi","ride_hail","private_transfer","bus","train","mixed"
	Label           string   `json:"label"` // human label, e.g. "Aerobus A1"
	TotalPrice      float64  `json:"total_price"`
	PriceIsEstimate bool     `json:"price_is_estimate"`
	Currency        string   `json:"currency"`
	DoorToDoorMin   int      `json:"door_to_door_minutes"`
	Changes         int      `json:"changes"`
	Pros            []string `json:"pros"`
	Cons            []string `json:"cons"`
	Steps           []Step   `json:"steps"`
	BookURL         string   `json:"book_url,omitempty"`
}

// Step is one instruction in a TransferOption. Grounded=false means the step
// was not derived from route data or the airport knowledge base and MUST be
// rendered as "(estimated)" — never presented as fact. This is the
// anti-hallucination contract for step-by-step instructions.
type Step struct {
	Order    int    `json:"order"`
	Text     string `json:"text"`
	Grounded bool   `json:"grounded"`
	DurMin   int    `json:"duration_minutes,omitempty"`
}

// TransferComparison is the full decision card for one leg: every mode as a
// choosable option, plus sort labels so cheapest/fastest/best-value/most
// luggage-friendly are visible at a glance. No forced ranking.
type TransferComparison struct {
	From        string           `json:"from"`
	To          string           `json:"to"`
	DistanceKm  float64          `json:"distance_km,omitempty"`
	Options     []TransferOption `json:"options"`
	Cheapest    string           `json:"cheapest_mode,omitempty"`
	Fastest     string           `json:"fastest_mode,omitempty"`
	BestValue   string           `json:"best_value_mode,omitempty"`
	LuggageBest string           `json:"most_luggage_friendly_mode,omitempty"`
}
