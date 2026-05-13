// Package profile manages a traveller profile derived from booking history.
// Unlike preferences (what users want), this tracks patterns from what they
// actually booked: airlines, routes, hotels, timing, and budget.
//
// Profile data is stored at ~/.trvl/profile.json alongside preferences.
package profile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TravelProfile aggregates patterns from a user's booking history.
type TravelProfile struct {
	// Counts
	TotalTrips       int `json:"total_trips"`
	TotalFlights     int `json:"total_flights"`
	TotalHotelNights int `json:"total_hotel_nights"`

	// Airline patterns
	TopAirlines       []AirlineStats `json:"top_airlines"`
	PreferredAlliance string         `json:"preferred_alliance"`
	AvgFlightPrice    float64        `json:"avg_flight_price"`
	AvgBookingLead    int            `json:"avg_booking_lead_days"`

	// Route patterns
	TopRoutes       []RouteStats `json:"top_routes"`
	TopDestinations []string     `json:"top_destinations"`
	HomeDetected    []string     `json:"home_detected"`

	// Hotel patterns
	TopHotelChains []HotelChainStats `json:"top_hotel_chains"`
	AvgStarRating  float64           `json:"avg_star_rating"`
	AvgNightlyRate float64           `json:"avg_nightly_rate"`
	PreferredType  string            `json:"preferred_type"`

	// Ground transport
	TopGroundModes []ModeStats `json:"top_ground_modes"`

	// Timing patterns
	AvgTripLength   float64        `json:"avg_trip_length_days"`
	PreferredDays   []string       `json:"preferred_departure_days"`
	SeasonalPattern map[string]int `json:"seasonal_pattern"`

	// Budget patterns
	AvgTripCost float64 `json:"avg_trip_cost"`
	BudgetTier  string  `json:"budget_tier"`

	// Raw bookings
	Bookings []Booking `json:"bookings"`

	// Metadata
	GeneratedAt string   `json:"generated_at"`
	Sources     []string `json:"sources"`

	// Reasoning layer — WHY the user makes decisions (optional, populated via onboarding phase 5).
	TravelModes       []TravelMode              `json:"travel_modes,omitempty"`
	CityIntelligence  []CityIntelligence        `json:"city_intelligence,omitempty"`
	BookingStrategies []BookingStrategy         `json:"booking_strategies,omitempty"`
	PriceElasticity   []PreferenceElasticity    `json:"price_elasticity,omitempty"`
	DestinationGraph  []DestinationRelationship `json:"destination_graph,omitempty"`
	TravelIdentity    string                    `json:"travel_identity,omitempty"`    // one-sentence: "Multimodal optimizer who works remotely from favourite cities"
	DecisionFramework string                    `json:"decision_framework,omitempty"` // "Price first, then filter by quality. Never overpays but will stretch for the right experience."
}

// TravelMode captures a distinct travel persona with different search parameters.
type TravelMode struct {
	Name       string   `json:"name"`        // "solo_remote", "with_partner", "with_kids", "weekend_break"
	Companions int      `json:"companions"`  // 0=solo, 1=partner, 2+=family
	Accom      string   `json:"accom"`       // "apartment", "boutique_hotel", "any"
	AccomNeeds []string `json:"accom_needs"` // ["wifi_fast","laundry","kitchen","2br"]
	BudgetFlex float64  `json:"budget_flex"` // 1.0=strict, 1.3=will pay 30% more for right property
	Dining     string   `json:"dining"`      // "eat_out", "cook", "partner_cooks", "mix"
	Transport  string   `json:"transport"`   // "multimodal", "flights_only", "ground_preferred"
	Priority   string   `json:"priority"`    // "price", "experience", "convenience", "speed"
}

// CityIntelligence captures per-city knowledge depth and preferences.
type CityIntelligence struct {
	City           string   `json:"city"`
	KnowledgeLevel string   `json:"knowledge_level"` // "local", "regular", "occasional", "aspirational"
	YearsLived     int      `json:"years_lived,omitempty"`
	Neighbourhoods []string `json:"neighbourhoods,omitempty"` // preferred areas
	Restaurants    []string `json:"restaurants,omitempty"`
	WhyVisit       string   `json:"why_visit"`    // "friends", "family", "partner", "work", "events", "tourism"
	TypicalStay    int      `json:"typical_stay"` // days
	Notes          string   `json:"notes,omitempty"`
}

// BookingStrategy captures how the user makes booking decisions.
type BookingStrategy struct {
	Name        string `json:"name"`        // human-readable
	Pattern     string `json:"pattern"`     // machine key: "cheapest_fare_plus_status", "multi_book_cancel", "overnight_layover_home", "snap_watching"
	Description string `json:"description"` // explanation of the strategy
	Frequency   string `json:"frequency"`   // "always", "often", "sometimes", "when_available"
}

// PreferenceElasticity captures what moves the user's price tolerance.
type PreferenceElasticity struct {
	Factor     string  `json:"factor"`                // "modern_interior", "sauna", "location", "breakfast_quality", "laundry"
	Impact     string  `json:"impact"`                // "will_pay_more", "dealbreaker", "nice_to_have"
	PriceDelta float64 `json:"price_delta,omitempty"` // how much more they'd pay, as multiplier (1.3 = 30% more)
}

// DestinationRelationship captures WHY a destination matters.
type DestinationRelationship struct {
	City      string `json:"city"`
	Reason    string `json:"reason"`           // "home", "family", "friends", "partner", "history", "events", "aspirational", "transit_hub"
	Person    string `json:"person,omitempty"` // who connects them to this city
	Frequency string `json:"frequency"`        // "weekly", "monthly", "quarterly", "yearly", "once", "never_been"
	Sentiment string `json:"sentiment"`        // "love", "practical", "nostalgic", "curious", "avoid"
}

// AirlineStats tracks usage frequency for a single airline.
type AirlineStats struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Flights int    `json:"flights"`
}

// RouteStats tracks a specific origin-destination pair.
type RouteStats struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Count    int     `json:"count"`
	AvgPrice float64 `json:"avg_price"`
}

// HotelChainStats tracks hotel chain/brand usage.
type HotelChainStats struct {
	Name   string `json:"name"`
	Nights int    `json:"nights"`
}

// ModeStats tracks ground transport mode usage.
type ModeStats struct {
	Mode  string `json:"mode"`
	Count int    `json:"count"`
}

// Booking is a single booking record from any source.
type Booking struct {
	Type       string  `json:"type"`
	Date       string  `json:"date"`
	TravelDate string  `json:"travel_date"`
	From       string  `json:"from,omitempty"`
	To         string  `json:"to,omitempty"`
	Provider   string  `json:"provider"`
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
	Nights     int     `json:"nights,omitempty"`
	Stars      int     `json:"stars,omitempty"`
	Source     string  `json:"source"`
	Reference  string  `json:"reference,omitempty"`
	Notes      string  `json:"notes,omitempty"`
}

// defaultPath returns ~/.trvl/profile.json.
func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".trvl", "profile.json"), nil
}

// Load reads the profile from ~/.trvl/profile.json.
// Returns an empty profile (not nil) if the file does not exist.
func Load() (*TravelProfile, error) {
	path, err := defaultPath()
	if err != nil {
		return &TravelProfile{}, nil
	}
	return LoadFrom(path)
}

// LoadFrom reads the profile from an explicit path.
func LoadFrom(path string) (*TravelProfile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &TravelProfile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	if len(data) == 0 {
		return &TravelProfile{}, nil
	}

	var p TravelProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return &p, nil
}

// Save writes the profile to ~/.trvl/profile.json atomically.
func Save(p *TravelProfile) error {
	path, err := defaultPath()
	if err != nil {
		return err
	}
	return SaveTo(path, p)
}

// SaveTo writes the profile to an explicit path atomically.
func SaveTo(path string, p *TravelProfile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile: %w", err)
	}

	// Atomic write via temp file + rename.
	dir := filepath.Dir(path)
	rndBytes := make([]byte, 8)
	if _, err := rand.Read(rndBytes); err != nil {
		return fmt.Errorf("generate temp name: %w", err)
	}
	tmpPath := filepath.Join(dir, filepath.Base(path)+".tmp-"+hex.EncodeToString(rndBytes))
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// AddBooking appends a booking and rebuilds the profile stats.
// Uses the default path (~/.trvl/profile.json).
func AddBooking(b Booking) error {
	p, err := Load()
	if err != nil {
		return err
	}
	p.Bookings = append(p.Bookings, b)
	rebuilt := BuildProfile(p.Bookings)
	rebuilt.Sources = p.Sources
	return Save(rebuilt)
}

// AddBookingTo appends a booking to a profile at the given path.
func AddBookingTo(path string, b Booking) error {
	p, err := LoadFrom(path)
	if err != nil {
		return err
	}
	p.Bookings = append(p.Bookings, b)
	rebuilt := BuildProfile(p.Bookings)
	rebuilt.Sources = p.Sources
	return SaveTo(path, rebuilt)
}
