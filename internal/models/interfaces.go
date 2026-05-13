package models

import "context"

// FlightSearchOptions mirrors flights.SearchOptions for the provider interface.
// This avoids a circular import between models and flights.
type FlightSearchOptions struct {
	ReturnDate string     // Return date for round-trip (YYYY-MM-DD); empty = one-way
	CabinClass CabinClass // Cabin class (default: Economy)
	MaxStops   MaxStops   // Maximum stops filter
	SortBy     SortBy     // Result sort order
	Airlines   []string   // Restrict to these airline IATA codes
	Adults     int        // Number of adult passengers (default: 1)
	Currency   string     // Target currency (ISO 4217, e.g. "USD"); empty = IP default
}

// HotelSearchOptions mirrors hotels.HotelSearchOptions for the provider interface.
type HotelSearchOptions struct {
	CheckIn         string // YYYY-MM-DD
	CheckOut        string // YYYY-MM-DD
	Guests          int
	Stars           int     // 0 = any, 2-5 filter
	Sort            string  // "cheapest", "rating", "distance", "stars"
	Currency        string  // default "USD"
	MinPrice        float64 // minimum price per night (0 = no filter)
	MaxPrice        float64 // maximum price per night (0 = no filter)
	MinRating       float64 // minimum guest rating (0 = no filter)
	MaxDistanceKm   float64 // max km from city center (0 = no filter)
	Amenities       []string
	CenterLat       float64
	CenterLon       float64
	EnrichAmenities bool
	EnrichLimit     int
}

// GroundSearchOptions mirrors ground.SearchOptions for the provider interface.
type GroundSearchOptions struct {
	Currency  string   // Default: EUR
	Providers []string // Filter to specific providers; empty = all
	MaxPrice  float64  // 0 = no limit
	Type      string   // "bus", "train", or empty for all
}

// FlightSearcher searches for flights between airports on a given date.
type FlightSearcher interface {
	SearchFlights(ctx context.Context, origin, dest, date string, opts FlightSearchOptions) (*FlightSearchResult, error)
}

// HotelSearcher searches for hotels in a location.
type HotelSearcher interface {
	SearchHotels(ctx context.Context, location string, opts HotelSearchOptions) (*HotelSearchResult, error)
}

// GroundSearcher searches for ground transport (bus/train) between cities.
type GroundSearcher interface {
	SearchGround(ctx context.Context, from, to, date string, opts GroundSearchOptions) (*GroundSearchResult, error)
}
