package multimodal

import (
	"context"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// NewPlanner returns a Planner wired to trvl's production seams:
//
//   - DISCOVERY via Rome2Rio (ground.SearchRome2Rio);
//   - per-leg PRICING via the existing flight and ground searches — never a
//     reimplemented provider;
//   - savings ANNOTATION via the travel-hacks engine (hacks.BestSaving).
//
// allowBrowser threads through to Rome2Rio (its live Cloudflare path is gated by
// --allow-browser-fallbacks) and to the ground leg pricer.
func NewPlanner(allowBrowser bool) *Planner {
	return &Planner{
		Discover:     ground.SearchRome2Rio,
		Price:        productionLegPricer(allowBrowser),
		Hacks:        productionHackAnnotator,
		AllowBrowser: allowBrowser,
	}
}

// productionLegPricer dispatches a leg to the right existing provider by mode:
// "fly" → flight search (with IATA resolution); everything else → ground search.
// It returns ok=false whenever a real fare cannot be obtained, so the composer
// falls back to an honest estimate rather than fabricating a price.
func productionLegPricer(allowBrowser bool) LegPricer {
	return func(ctx context.Context, spec LegSpec) (PricedLeg, bool) {
		switch normalizeMode(spec.Mode) {
		case "fly":
			return priceFlightLeg(ctx, spec)
		case "drive", "walk", "":
			// No bookable provider for self-driving / walking legs.
			return PricedLeg{}, false
		default:
			return priceGroundLeg(ctx, spec, allowBrowser)
		}
	}
}

func normalizeMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "fly", "flight", "plane", "air":
		return "fly"
	case "train", "rail":
		return "train"
	case "bus", "coach":
		return "bus"
	case "ferry", "boat", "car ferry":
		return "ferry"
	case "drive", "car":
		return "drive"
	case "walk":
		return "walk"
	default:
		return strings.ToLower(strings.TrimSpace(m))
	}
}

// priceFlightLeg resolves the leg endpoints to IATA codes and prices the
// cheapest flight via the existing flight search (hacks disabled — savings are
// annotated once at plan level).
func priceFlightLeg(ctx context.Context, spec LegSpec) (PricedLeg, bool) {
	origin, ok := resolveAirport(spec.From)
	if !ok {
		return PricedLeg{}, false
	}
	dest, ok := resolveAirport(spec.To)
	if !ok {
		return PricedLeg{}, false
	}
	res, err := flights.SearchFlights(ctx, origin, dest, spec.Date, flights.SearchOptions{NoHacks: true})
	if err != nil || res == nil || !res.Success {
		return PricedLeg{}, false
	}
	best, ok := cheapestFlight(res.Flights)
	if !ok {
		return PricedLeg{}, false
	}
	return PricedLeg{
		Mode:        "fly",
		From:        spec.From,
		To:          spec.To,
		Price:       best.PriceForRanking(),
		Currency:    best.Currency,
		DurationMin: best.Duration,
		Provider:    flightProvider(best),
		BookingURL:  best.BookingURL,
		Detail:      origin + "→" + dest,
	}, true
}

// priceGroundLeg prices the cheapest ground option for the leg via the existing
// ground search (hacks disabled to avoid a nested savings fan-out).
func priceGroundLeg(ctx context.Context, spec LegSpec, allowBrowser bool) (PricedLeg, bool) {
	if strings.TrimSpace(spec.From) == "" || strings.TrimSpace(spec.To) == "" {
		return PricedLeg{}, false
	}
	opts := ground.SearchOptions{
		NoHacks:               true,
		AllowBrowserFallbacks: allowBrowser,
	}
	switch normalizeMode(spec.Mode) {
	case "bus", "train":
		opts.Type = normalizeMode(spec.Mode)
	}
	res, err := ground.SearchByName(ctx, spec.From, spec.To, spec.Date, opts)
	if err != nil || res == nil || !res.Success {
		return PricedLeg{}, false
	}
	best, ok := cheapestRoute(res.Routes)
	if !ok {
		return PricedLeg{}, false
	}
	mode := best.Type
	if mode == "" {
		mode = normalizeMode(spec.Mode)
	}
	return PricedLeg{
		Mode:        mode,
		From:        spec.From,
		To:          spec.To,
		Price:       best.Price,
		Currency:    best.Currency,
		DurationMin: best.Duration,
		Provider:    best.Provider,
		BookingURL:  best.BookingURL,
	}, true
}

// resolveAirport turns a place name into a bookable IATA code: an explicit IATA
// passes through; a city name resolves to its primary airport. Returns ok=false
// when no airport is known, so the flight leg is estimated rather than guessed.
func resolveAirport(place string) (string, bool) {
	p := strings.ToUpper(strings.TrimSpace(place))
	if models.IsIATACode(p) {
		return p, true
	}
	if codes := models.ResolveCityToAirports(place); len(codes) > 0 {
		return codes[0], true
	}
	return "", false
}

func cheapestFlight(fs []models.FlightResult) (models.FlightResult, bool) {
	var best models.FlightResult
	found := false
	for _, f := range fs {
		if f.PriceForRanking() <= 0 || f.Currency == "" {
			continue
		}
		if !found || f.PriceForRanking() < best.PriceForRanking() {
			best, found = f, true
		}
	}
	return best, found
}

func cheapestRoute(rs []models.GroundRoute) (models.GroundRoute, bool) {
	var best models.GroundRoute
	found := false
	for _, r := range rs {
		if r.Price <= 0 || r.Currency == "" {
			continue
		}
		if !found || r.Price < best.Price {
			best, found = r, true
		}
	}
	return best, found
}

func flightProvider(f models.FlightResult) string {
	if f.Provider != "" {
		return f.Provider
	}
	return "flights"
}

// productionHackAnnotator runs the travel-hacks savings engine against the
// itinerary's true total as the naive baseline. It returns nil unless a strictly
// cheaper synthesized option exists (BestSaving enforces the honesty contract).
func productionHackAnnotator(ctx context.Context, from, to, date string, naivePrice float64, currency string) *models.HackSaving {
	return hacks.BestSaving(ctx, hacks.DetectorInput{
		Origin:      from,
		Destination: to,
		Date:        date,
		Currency:    currency,
		NaivePrice:  naivePrice,
	}, nil)
}
