package afklm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// RailwayStations is the set of IATA codes that AF-KLM accepts with
// type=RAILWAY_STATION for Rail+Fly bundled offers (Thalys/Eurostar legs
// included in a single ticket from/to AMS or CDG). Live-probed against the
// Offers API 2026-04-21; other nominal rail codes (QYG, XER, XDB, RTM) return
// "origin forbidden" or invalid-value errors.
var RailwayStations = map[string]bool{
	"ZYR": true, // Brussels-Midi (Thalys from AMS ~2h, flight from BRU)
}

// TrainCityCodes is the set of IATA city codes that AF-KLM treats as rail-eligible
// origins when submitted with type=CITY. The API auto-synthesizes a train sub-leg
// to the nearest hub. Live-probed 2026-04-21.
var TrainCityCodes = map[string]bool{
	"ANR": true, // Antwerp city (AF-KLM auto-routes via train to AMS hub)
	"BRU": true, // Brussels city (train + flight from BRU airport)
}

// placeType returns the AF-KLM place type for a given IATA code. Rail+Fly
// stations take RAILWAY_STATION; train-eligible cities take CITY; everything
// else is an airport.
func placeType(code string) string {
	if RailwayStations[code] {
		return "RAILWAY_STATION"
	}
	if TrainCityCodes[code] {
		return "CITY"
	}
	return "AIRPORT"
}

// cabinCodes maps models.CabinClass to AF-KLM commercialCabins values.
func cabinCodes(cc models.CabinClass) []string {
	switch cc {
	case models.Economy:
		return []string{"ECONOMY"}
	case models.Business:
		return []string{"BUSINESS"}
	default:
		return []string{"ALL"}
	}
}

// AFKLMProvider implements models.FlightSearcher using the AF-KLM Offers API.
type AFKLMProvider struct {
	client *Client
}

// NewProvider creates an AFKLMProvider. Returns ErrNoCredential if the
// provider is not configured.
func NewProvider() (*AFKLMProvider, error) {
	client, err := NewClient(ClientOptions{})
	if err != nil {
		return nil, err
	}
	return &AFKLMProvider{client: client}, nil
}

// NewProviderWithClient creates an AFKLMProvider using the given pre-built
// client. Used in tests.
func NewProviderWithClient(client *Client) *AFKLMProvider {
	return &AFKLMProvider{client: client}
}

// SearchFlights implements models.FlightSearcher.
func (p *AFKLMProvider) SearchFlights(ctx context.Context, origin, dest, date string, opts models.FlightSearchOptions) (*models.FlightSearchResult, error) {
	adults := opts.Adults
	if adults < 1 {
		adults = 1
	}

	currency := opts.Currency
	if currency == "" {
		currency = "EUR"
	}

	// Build passengers list.
	passengers := make([]Passenger, adults)
	for i := range passengers {
		passengers[i] = Passenger{ID: i + 1, Type: "ADT"}
	}

	// Build connections.
	connections := []RequestedConnection{
		{
			DepartureDate: date,
			Origin:        Place{Type: placeType(origin), Code: origin},
			Destination:   Place{Type: placeType(dest), Code: dest},
		},
	}
	if opts.ReturnDate != "" {
		connections = append(connections, RequestedConnection{
			DepartureDate: opts.ReturnDate,
			Origin:        Place{Type: placeType(dest), Code: dest},
			Destination:   Place{Type: placeType(origin), Code: origin},
		})
	}

	tripType := "one-way"
	if opts.ReturnDate != "" {
		tripType = "round-trip"
	}

	req := AvailableOffersRequest{
		CommercialCabins:      cabinCodes(opts.CabinClass),
		BookingFlow:           "LEISURE",
		Passengers:            passengers,
		RequestedConnections:  connections,
		Currency:              currency,
		LowestFareCombination: true,
	}

	resp, _, err := p.client.AvailableOffers(ctx, req)
	if err != nil {
		// Graceful degradation for credential errors (quota is propagated so
		// roundtrip opportunistic path can treat exactly like ErrNoCredential).
		if errors.Is(err, ErrNoCredential) {
			return &models.FlightSearchResult{
				Success:  false,
				TripType: tripType,
				Error:    "afklm: no API key configured",
			}, nil
		}
		if errors.Is(err, ErrDailyQuota) {
			return nil, ErrDailyQuota
		}
		return nil, fmt.Errorf("afklm: search: %w", err)
	}

	flights := mapRecommendations(resp, origin, dest)
	return &models.FlightSearchResult{
		Success:  true,
		Count:    len(flights),
		TripType: tripType,
		Flights:  flights,
	}, nil
}

// mapRecommendations converts AF-KLM recommendations to FlightResult slices.
//
// AF-KLM uses RJF normalization: pricing metadata lives under
// recommendations[].flightProducts[].connections[] (keyed by connectionId),
// while actual flight details (airports, carriers, datetimes, duration) live
// in the top-level connections[bound_idx] array keyed by id. We build a lookup
// table first to join them in O(1) per entry.
func mapRecommendations(resp *AvailableOffersResponse, origin, dest string) []models.FlightResult {
	// Index top-level connections by (bound_idx, id) for O(1) lookup.
	lookup := make([]map[int]BoundConnection, len(resp.Connections))
	for i, bound := range resp.Connections {
		m := make(map[int]BoundConnection, len(bound))
		for _, c := range bound {
			m[c.ID] = c
		}
		lookup[i] = m
	}

	var results []models.FlightResult
	for _, rec := range resp.Recommendations {
		for _, fp := range rec.FlightProducts {
			if len(fp.Connections) == 0 {
				continue
			}

			var legs []models.FlightLeg
			totalDuration := 0
			outboundStops := 0
			bounds := 0

			for boundIdx, pc := range fp.Connections {
				if boundIdx >= len(lookup) {
					break
				}
				bc, ok := lookup[boundIdx][pc.ConnectionID]
				if !ok {
					continue
				}
				bounds++
				totalDuration += bc.Duration
				if boundIdx == 0 && len(bc.Segments) > 1 {
					outboundStops = len(bc.Segments) - 1
				}
				direction := boundDirection(boundIdx)
				for i, s := range bc.Segments {
					leg := mapSegment(s)
					leg.Direction = direction
					// Layover = gap between prev arrival and this departure within the same bound.
					if i > 0 {
						if prev, cur, ok := parseLayover(bc.Segments[i-1].ArrivalDateTime, s.DepartureDateTime); ok {
							leg.LayoverMinutes = int(cur.Sub(prev).Minutes())
						}
					}
					legs = append(legs, leg)
				}
			}

			// Pricing: prefer the outbound pricing connection's display price.
			price := fp.Connections[0].Price.DisplayPrice
			currency := fp.Connections[0].Price.Currency
			if price == 0 {
				price = fp.Price.DisplayPrice
				currency = fp.Price.Currency
			}

			// A real return offer (outbound + inbound bound) is a genuine
			// round-trip ticket — both legs are materialised above, so unlike
			// the Google/Kiwi native fares this needs no "return at booking"
			// caveat. Tag it FareRoundTrip so it ranks honestly against split
			// pairs; a single-bound (one-way) offer keeps the default type.
			var fareType models.FareType
			if bounds > 1 {
				fareType = models.FareRoundTrip
			}

			results = append(results, models.FlightResult{
				Price:    price,
				Currency: currency,
				Duration: totalDuration,
				Stops:    outboundStops,
				Provider: "afklm",
				Legs:     legs,
				FareType: fareType,
			})
		}
	}
	return results
}

// boundDirection maps an AF-KLM bound index to a leg direction tag. Bound 0 is
// always the outbound; bound 1 (present only on round-trip requests) is the
// inbound/return. Any further bounds (open-jaw multi-city) are left untagged.
func boundDirection(boundIdx int) string {
	switch boundIdx {
	case 0:
		return "outbound"
	case 1:
		return "inbound"
	default:
		return ""
	}
}

// mapSegment converts a top-level BoundConnection Segment to a FlightLeg.
func mapSegment(s Segment) models.FlightLeg {
	return models.FlightLeg{
		DepartureAirport: models.AirportInfo{Code: s.Origin.Code, Name: s.Origin.Name},
		ArrivalAirport:   models.AirportInfo{Code: s.Destination.Code, Name: s.Destination.Name},
		DepartureTime:    s.DepartureDateTime,
		ArrivalTime:      s.ArrivalDateTime,
		Duration:         s.Duration,
		AirlineCode:      s.MarketingFlight.Carrier.Code,
		Airline:          s.MarketingFlight.Carrier.Name,
		FlightNumber:     s.MarketingFlight.Carrier.Code + s.MarketingFlight.Number,
	}
}

// parseLayover parses two AF-KLM datetimes and returns their times for diff.
// Format is "2006-01-02T15:04:05". Returns ok=false when either is empty or
// when parsing fails.
func parseLayover(prevArrival, nextDeparture string) (prev, cur time.Time, ok bool) {
	const layout = "2006-01-02T15:04:05"
	if prevArrival == "" || nextDeparture == "" {
		return time.Time{}, time.Time{}, false
	}
	p, err1 := time.Parse(layout, prevArrival)
	n, err2 := time.Parse(layout, nextDeparture)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, false
	}
	return p, n, true
}
