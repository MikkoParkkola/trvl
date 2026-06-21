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
//
// The search is constrained to the discovered mode (bus/train/ferry) so a ferry
// crossing is priced as a ferry rather than silently relabeled as the cheapest
// bus that happens to serve the same city pair. A concrete discovered mode is
// authoritative for the leg label; when the priced route itself decomposes into
// several modes (e.g. a coach that boards a ferry), that chain is disclosed in
// Detail so an embedded ferry is never hidden.
func priceGroundLeg(ctx context.Context, spec LegSpec, allowBrowser bool) (PricedLeg, bool) {
	if strings.TrimSpace(spec.From) == "" || strings.TrimSpace(spec.To) == "" {
		return PricedLeg{}, false
	}
	disc := normalizeMode(spec.Mode)
	opts := ground.SearchOptions{
		NoHacks:               true,
		AllowBrowserFallbacks: allowBrowser,
	}
	switch disc {
	case "bus", "train", "ferry":
		opts.Type = disc
	}
	res, err := ground.SearchByName(ctx, spec.From, spec.To, spec.Date, opts)
	if err != nil || res == nil || !res.Success {
		return PricedLeg{}, false
	}
	best, ok := cheapestRoute(res.Routes)
	if !ok {
		return PricedLeg{}, false
	}
	mode, detail := resolveLegMode(disc, best)
	return PricedLeg{
		Mode:        mode,
		From:        spec.From,
		To:          spec.To,
		Price:       best.Price,
		Currency:    best.Currency,
		DurationMin: best.Duration,
		Provider:    best.Provider,
		BookingURL:  best.BookingURL,
		Detail:      detail,
	}, true
}

// resolveLegMode decides a priced ground leg's display mode and any intermodal
// disclosure. A concrete discovered mode (bus/train/ferry) is authoritative —
// the ground search is constrained to it, so a ferry is never relabeled as a
// bus. When the priced route decomposes into multiple modes (e.g. a coach that
// boards a ferry), the full chain is returned as a "via ..." detail so the
// embedded ferry is disclosed rather than hidden. Nothing is fabricated: the
// disclosure comes only from the route's own leg breakdown.
func resolveLegMode(disc string, best models.GroundRoute) (mode, detail string) {
	mode = normalizeMode(disc)
	chain := distinctLegModes(best.Legs)
	if mode == "" || mode == "mixed" {
		switch {
		case len(chain) >= 1:
			mode = chain[0]
		case strings.TrimSpace(best.Type) != "":
			mode = normalizeMode(best.Type)
		}
	}
	if len(chain) > 1 {
		return mode, "via " + strings.Join(chain, " + ")
	}
	return mode, ""
}

// distinctLegModes returns the ordered, de-duplicated normalized modes of a
// route's leg breakdown (empty when the route carries no per-leg detail).
func distinctLegModes(legs []models.GroundLeg) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range legs {
		m := normalizeMode(l.Type)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
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
