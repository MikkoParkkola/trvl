// Package multimodal composes end-to-end travel itineraries by combining
// Rome2Rio multimodal route DISCOVERY with trvl's existing per-leg PRICING
// providers (flights, trains, buses, ferries) and the travel-hacks savings
// engine.
//
// The synthesis (innovation #2 — the multimodal composer):
//
//  1. DISCOVER — Rome2Rio surfaces candidate mode-chains between two places
//     (e.g. "ferry to a hub, then fly"), combinations a single-mode provider
//     would never reveal. Rome2Rio prices are only indicative RANGES.
//  2. PRICE — each leg of a discovered chain is priced by the appropriate
//     existing provider (ground search for train/bus/ferry/drive, flight
//     search for fly). trvl never reimplements a provider here; it reuses them.
//  3. ASSEMBLE — legs are summed into an end-to-end itinerary with a single
//     true total. When a leg cannot be priced, the composer falls back to
//     Rome2Rio's indicative range for that leg and clearly labels the
//     itinerary (and the leg) as an ESTIMATE — never a fabricated fare.
//  4. RANK — itineraries are sorted by true total cost (fully-priced beats
//     estimated on ties), so the cheapest real way to travel leads.
//  5. ANNOTATE — the travel-hacks savings engine is run against the cheapest
//     itinerary so any cheaper synthesized option surfaces alongside.
//
// Honesty contract (mirrors the rest of trvl): a priced leg is only ever a
// real provider fare; estimates are always labelled; risk caveats from
// discovery and from the hacks engine are preserved verbatim. One leg's
// pricing failure degrades only that itinerary, never the whole plan.
package multimodal

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

const (
	// defaultMaxItineraries bounds how many ranked itineraries a plan returns.
	defaultMaxItineraries = 8
	// defaultLegTimeout bounds a single leg's pricing call so one slow provider
	// never blocks the whole plan.
	defaultLegTimeout = 20 * time.Second
	// defaultPlanTimeout bounds the overall discovery+pricing fan-out.
	defaultPlanTimeout = 45 * time.Second
	// maxParallelLegs bounds concurrent per-leg pricing calls across the plan.
	maxParallelLegs = 6
	// maxRoutesPriced caps how many discovered mode-chains are priced, bounding
	// the provider fan-out. Excess (cheaper-indicative-first) is reported, not
	// silently dropped.
	maxRoutesPriced = 10
)

// LegSpec identifies one leg to price: a transport mode between two places on a
// date. From/To are human place names (city or airport) as discovered; the
// production pricer resolves them to provider-specific identifiers.
type LegSpec struct {
	Mode string // "train" | "bus" | "ferry" | "fly" | "drive" | ...
	From string
	To   string
	Date string
	// Currency is the single target currency every leg of the plan is priced in,
	// so flight and ground legs are comparable and itinerary totals can be ranked
	// honestly. Empty falls back to each provider's own default.
	Currency string
}

// PricedLeg is one leg of an assembled itinerary after pricing (or estimation).
type PricedLeg struct {
	Mode        string  `json:"mode"`
	From        string  `json:"from"`
	To          string  `json:"to"`
	Price       float64 `json:"price"`
	Currency    string  `json:"currency"`
	DurationMin int     `json:"duration_minutes,omitempty"`
	// Estimated is true when Price is an indicative Rome2Rio figure rather than
	// a real provider fare. Such a leg is never presented as bookable.
	Estimated  bool   `json:"estimated"`
	Provider   string `json:"provider"`
	BookingURL string `json:"booking_url,omitempty"`
	Detail     string `json:"detail,omitempty"`
	// OriginalPrice and OriginalCurrency preserve a foreign leg's own fare when
	// the assembler converts it into the itinerary's headline currency at a
	// reference rate. They are zero/empty unless a conversion happened, so the
	// original quote is never lost and a caller can recompute the applied rate
	// (Price / OriginalPrice). Disclosure of the conversion also lands in Detail.
	OriginalPrice    float64 `json:"original_price,omitempty"`
	OriginalCurrency string  `json:"original_currency,omitempty"`
}

// Itinerary is an end-to-end multimodal journey with a single true total.
type Itinerary struct {
	From        string             `json:"from"`
	To          string             `json:"to"`
	Date        string             `json:"date"`
	Legs        []PricedLeg        `json:"legs"`
	TotalPrice  float64            `json:"total_price"`
	Currency    string             `json:"currency"`
	DurationMin int                `json:"duration_minutes,omitempty"`
	Transfers   int                `json:"transfers"`
	ModeChain   string             `json:"mode_chain"` // e.g. "ferry → fly"
	Estimated   bool               `json:"estimated"`  // any leg estimated
	Source      string             `json:"source"`     // discovery provider
	BookingURL  string             `json:"booking_url,omitempty"`
	HackSaving  *models.HackSaving `json:"hack_saving,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
	Risks       []string           `json:"risks,omitempty"`
}

// Plan is the full result of composing a from→to→date query.
type Plan struct {
	From        string      `json:"from"`
	To          string      `json:"to"`
	Date        string      `json:"date"`
	Itineraries []Itinerary `json:"itineraries"`
	Discovered  int         `json:"discovered"` // mode-chains discovered
	Priced      int         `json:"priced"`     // chains assembled into itineraries
	Notes       []string    `json:"notes,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// Discoverer surfaces candidate mode-chains between two places. Production wraps
// ground.SearchRome2Rio; tests inject deterministic routes.
type Discoverer func(ctx context.Context, from, to string, allowBrowser bool) ([]models.GroundRoute, error)

// LegPricer prices a single leg using an existing per-leg provider. It returns
// ok=false (never an error) when the leg cannot be priced, so the composer can
// fall back to an honest estimate without failing the whole plan.
type LegPricer func(ctx context.Context, spec LegSpec) (PricedLeg, bool)

// HackAnnotator runs the travel-hacks savings engine against a naive baseline.
// Returns nil when no genuinely cheaper option exists. Optional (may be nil).
type HackAnnotator func(ctx context.Context, from, to, date string, naivePrice float64, currency string) *models.HackSaving

// CurrencyConverter converts amount from one currency to another, returning the
// converted amount and the resulting currency. It mirrors
// destinations.ConvertCurrency: on an unknown rate it returns the amount in the
// ORIGINAL currency (result currency != target), which the assembler reads as
// "conversion unavailable" and keeps the conservative exclusion. Optional (may
// be nil): a nil converter leaves foreign legs excluded from the sum rather than
// normalised.
type CurrencyConverter func(ctx context.Context, amount float64, from, to string) (float64, string)

// Planner composes multimodal itineraries from injected seams. The zero value is
// not usable; construct via NewPlanner (production) or set the seams directly
// (tests).
type Planner struct {
	Discover       Discoverer
	Price          LegPricer
	Hacks          HackAnnotator     // optional
	Convert        CurrencyConverter // optional; nil = foreign legs excluded from the sum, not normalised
	AllowBrowser   bool
	MaxItineraries int
	LegTimeout     time.Duration
	PlanTimeout    time.Duration
}

func (p *Planner) maxItineraries() int {
	if p.MaxItineraries > 0 {
		return p.MaxItineraries
	}
	return defaultMaxItineraries
}

func (p *Planner) legTimeout() time.Duration {
	if p.LegTimeout > 0 {
		return p.LegTimeout
	}
	return defaultLegTimeout
}

func (p *Planner) planTimeout() time.Duration {
	if p.PlanTimeout > 0 {
		return p.PlanTimeout
	}
	return defaultPlanTimeout
}

// Plan discovers, prices, assembles, ranks and annotates multimodal itineraries
// for from→to on date. It never returns a nil Plan on a non-empty query; a
// discovery failure or an empty discovery degrades to an empty (but described)
// plan rather than an error so callers can always render something honest.
func (p *Planner) Plan(ctx context.Context, from, to, date, currency string) (*Plan, error) {
	plan := &Plan{From: from, To: to, Date: date}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" || strings.TrimSpace(date) == "" {
		plan.Error = "from, to, and date are required"
		return plan, nil
	}
	if p.Discover == nil || p.Price == nil {
		plan.Error = "multimodal planner is not configured"
		return plan, nil
	}
	// One target currency for every leg so flight and ground prices are comparable
	// and itinerary totals rank honestly. Default matches the ground search default.
	if strings.TrimSpace(currency) == "" {
		currency = "EUR"
	}

	ctx, cancel := context.WithTimeout(ctx, p.planTimeout())
	defer cancel()

	routes, err := p.Discover(ctx, from, to, p.AllowBrowser)
	if err != nil {
		// Discovery failure is honest, not fatal: report it and return an empty
		// plan. The caller surfaces the typed reason (Cloudflare wall, etc.).
		plan.Error = "route discovery: " + err.Error()
		return plan, nil
	}
	plan.Discovered = len(routes)
	if len(routes) == 0 {
		plan.Notes = append(plan.Notes, "no multimodal routes discovered for this pair")
		return plan, nil
	}

	// Bound the number of chains priced (cheapest-indicative first), reporting
	// any truncation rather than silently dropping options.
	routes = boundRoutes(routes)
	if plan.Discovered > len(routes) {
		plan.Notes = append(plan.Notes,
			"priced the "+itoa(len(routes))+" most promising of "+itoa(plan.Discovered)+" discovered chains")
	}

	itineraries := p.priceRoutes(ctx, from, to, date, currency, routes)
	plan.Priced = len(itineraries)

	rankItineraries(itineraries)
	itineraries = dedupeItineraries(itineraries)
	if len(itineraries) > p.maxItineraries() {
		itineraries = itineraries[:p.maxItineraries()]
	}

	// Annotate the cheapest itinerary with any travel-hack saving. The baseline
	// is its true total; the engine only surfaces a strictly cheaper option.
	if p.Hacks != nil && len(itineraries) > 0 {
		best := itineraries[0]
		if best.TotalPrice > 0 {
			if s := p.Hacks(ctx, from, to, date, best.TotalPrice, best.Currency); s != nil {
				itineraries[0].HackSaving = s
			}
		}
	}

	plan.Itineraries = itineraries
	if len(itineraries) == 0 {
		plan.Notes = append(plan.Notes, "routes discovered but none could be priced or estimated")
	}
	return plan, nil
}

// priceRoutes prices every leg of every route with bounded parallelism, then
// assembles each route into an itinerary. A route whose legs all fail to price
// (and which has no indicative range to estimate from) is dropped with no panic.
func (p *Planner) priceRoutes(ctx context.Context, from, to, date, currency string, routes []models.GroundRoute) []Itinerary {
	sem := make(chan struct{}, maxParallelLegs)
	var mu sync.Mutex
	out := make([]Itinerary, 0, len(routes))

	var wg sync.WaitGroup
	for i := range routes {
		route := routes[i]
		specs := legSpecsForRoute(route, from, to, date)
		for j := range specs {
			specs[j].Currency = currency // one target currency for every leg
		}
		priced := make([]PricedLeg, len(specs))
		var legWG sync.WaitGroup
		for j := range specs {
			legWG.Add(1)
			go func(idx int, spec LegSpec) {
				defer legWG.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					priced[idx] = estimatePlaceholder(spec)
					return
				}
				legCtx, cancel := context.WithTimeout(ctx, p.legTimeout())
				defer cancel()
				if pl, ok := p.Price(legCtx, spec); ok {
					priced[idx] = pl
					return
				}
				priced[idx] = estimatePlaceholder(spec)
			}(j, specs[j])
		}
		wg.Add(1)
		go func(r models.GroundRoute, legs *[]PricedLeg, wgLeg *sync.WaitGroup) {
			defer wg.Done()
			wgLeg.Wait()
			if it, ok := assembleItinerary(ctx, p.Convert, r, from, to, date, *legs); ok {
				mu.Lock()
				out = append(out, it)
				mu.Unlock()
			}
		}(route, &priced, &legWG)
	}
	wg.Wait()
	return out
}

// boundRoutes returns at most maxRoutesPriced routes, preferring those with the
// lowest indicative price (0-priced routes sort last so priced discovery leads).
func boundRoutes(routes []models.GroundRoute) []models.GroundRoute {
	if len(routes) <= maxRoutesPriced {
		return routes
	}
	cp := make([]models.GroundRoute, len(routes))
	copy(cp, routes)
	sort.SliceStable(cp, func(i, j int) bool {
		return indicativeSortKey(cp[i]) < indicativeSortKey(cp[j])
	})
	return cp[:maxRoutesPriced]
}

func indicativeSortKey(r models.GroundRoute) float64 {
	if r.Price > 0 {
		return r.Price
	}
	return 1e18 // unpriced discovery sorts last
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
