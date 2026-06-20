// Package tripcoalesce fans a single "plan A→B" request out across trvl's
// existing flight, hotel, and ground search engines concurrently, then
// assembles one combined TripPlan with a best-effort total-cost estimate.
//
// Innovation #5: today a user runs `trvl flights`, `trvl hotels`, and
// `trvl ground` as three separate commands and stitches the answers together
// by hand. The coalescer issues all three searches at once through a bounded
// errgroup (mirroring the cap-bounded fan-out pattern already used in
// internal/flights/concurrency.go), isolates per-domain failures so one slow
// or failing domain never aborts the others, and returns whatever succeeded.
//
// It REUSES the existing exported search entrypoints — it never reimplements a
// provider or edits a provider package. The real search functions are injected
// as seams (see New) so tests can fake them deterministically and offline.
package tripcoalesce

import (
	"context"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// FlightSearchFunc matches flights.SearchFlights — the flight search seam.
type FlightSearchFunc func(ctx context.Context, origin, destination, date string, opts flights.SearchOptions) (*models.FlightSearchResult, error)

// HotelSearchFunc matches hotels.SearchHotels — the hotel search seam.
type HotelSearchFunc func(ctx context.Context, location string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error)

// GroundSearchFunc matches ground.SearchByName — the ground search seam.
type GroundSearchFunc func(ctx context.Context, from, to, date string, opts ground.SearchOptions) (*models.GroundSearchResult, error)

// defaultDomainConcurrency mirrors the bounded cap used elsewhere (the flight
// fan-out caps at 6, multi.go at 5). Three domains never exceed it today, but
// the cap keeps the contract explicit and future-proof.
const defaultDomainConcurrency = 5

// defaultPerDomainTimeout bounds how long any single domain may run inside the
// fan-out so one stalled engine cannot hold the combined plan hostage. A domain
// that exceeds it is reported as a timeout, never silently dropped.
const defaultPerDomainTimeout = 45 * time.Second

// Params describes one trip to coalesce. Only Origin, Destination and
// DepartDate are required; the rest refine the per-domain searches.
type Params struct {
	Origin      string // flight origin (IATA, e.g. HEL) and ground "from" fallback
	Destination string // flight destination (IATA) and ground "to" / hotel-location fallback
	DepartDate  string // YYYY-MM-DD
	ReturnDate  string // optional round-trip return date (flights)

	// HotelLocation overrides the hotel search location. When empty, the hotel
	// search uses Destination.
	HotelLocation string
	GroundFrom    string // overrides ground "from"; empty uses Origin
	GroundTo      string // overrides ground "to"; empty uses Destination

	CheckIn  string // hotel check-in (YYYY-MM-DD); empty uses DepartDate
	CheckOut string // hotel check-out (YYYY-MM-DD); empty uses ReturnDate
	Nights   int    // nights for the hotel cost estimate; <=0 means per-night only

	Travelers int    // passengers / guests (default 1)
	Currency  string // shared currency hint (default EUR)

	// AllowBrowserFallbacks opts ground search into browser/cookie-assisted
	// providers (mirrors ground.SearchOptions.AllowBrowserFallbacks).
	AllowBrowserFallbacks bool
}

func (p Params) hotelLocation() string {
	if strings.TrimSpace(p.HotelLocation) != "" {
		return p.HotelLocation
	}
	return p.Destination
}

func (p Params) groundFrom() string {
	if strings.TrimSpace(p.GroundFrom) != "" {
		return p.GroundFrom
	}
	return p.Origin
}

func (p Params) groundTo() string {
	if strings.TrimSpace(p.GroundTo) != "" {
		return p.GroundTo
	}
	return p.Destination
}

func (p Params) travelers() int {
	if p.Travelers <= 0 {
		return 1
	}
	return p.Travelers
}

func (p Params) currency() string {
	if c := strings.TrimSpace(p.Currency); c != "" {
		return strings.ToUpper(c)
	}
	return "EUR"
}

// DomainStatus is the per-domain outcome surfaced so a caller can tell a clean
// "0 results" apart from a failure or timeout — never a fabricated empty.
type DomainStatus struct {
	Domain    string `json:"domain"` // "flights", "hotels", or "ground"
	OK        bool   `json:"ok"`     // true when the search returned without error
	Count     int    `json:"count"`  // number of results found
	Error     string `json:"error,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// CostComponent is one priced leg of the combined estimate.
type CostComponent struct {
	Domain   string  `json:"domain"`
	Label    string  `json:"label"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// TripPlan is the assembled result of one coalesced trip search.
type TripPlan struct {
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	DepartDate  string `json:"depart_date"`
	ReturnDate  string `json:"return_date,omitempty"`
	Currency    string `json:"currency"`

	Flights *models.FlightSearchResult `json:"flights,omitempty"`
	Hotels  *models.HotelSearchResult  `json:"hotels,omitempty"`
	Ground  *models.GroundSearchResult `json:"ground,omitempty"`

	// Cheapest picks per domain (nil when the domain found nothing priced).
	CheapestFlight *models.FlightResult `json:"cheapest_flight,omitempty"`
	CheapestHotel  *models.HotelResult  `json:"cheapest_hotel,omitempty"`
	CheapestGround *models.GroundRoute  `json:"cheapest_ground,omitempty"`

	// TotalCostEstimate sums the cheapest priced pick from each domain that
	// returned one. It is a floor estimate, not a quote; CostBreakdown lists
	// exactly which components contributed so nothing is implied silently.
	TotalCostEstimate float64         `json:"total_cost_estimate"`
	CostBreakdown     []CostComponent `json:"cost_breakdown,omitempty"`

	Statuses []DomainStatus `json:"statuses"`
	Notes    []string       `json:"notes,omitempty"`
}

// Coalescer holds the injectable search seams plus fan-out tuning. Use New for
// the production wiring; override the *Search fields in tests with fakes.
type Coalescer struct {
	FlightSearch FlightSearchFunc
	HotelSearch  HotelSearchFunc
	GroundSearch GroundSearchFunc

	Concurrency      int
	PerDomainTimeout time.Duration

	// now is an injectable clock for deterministic elapsed-time tests.
	now func() time.Time
}

// New returns a Coalescer wired to the real exported search entrypoints.
func New() *Coalescer {
	return &Coalescer{
		FlightSearch:     flights.SearchFlights,
		HotelSearch:      hotels.SearchHotels,
		GroundSearch:     ground.SearchByName,
		Concurrency:      defaultDomainConcurrency,
		PerDomainTimeout: defaultPerDomainTimeout,
		now:              time.Now,
	}
}

// Plan coalesces one trip using the default (real) search seams.
func Plan(ctx context.Context, p Params) *TripPlan {
	return New().Plan(ctx, p)
}
