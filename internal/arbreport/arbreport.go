// Package arbreport aggregates trvl's independent arbitrage engines —
// hotel rate arbitrage (internal/hotelarb), cabin-class arbitrage
// (internal/cabinarb), and airline currency arbitrage (internal/hacks) —
// into a single ranked report for a trip context.
//
// It is a thin aggregator: it never reimplements engine logic. Each engine
// is invoked through its existing public API via a swappable seam (Engines),
// which keeps the real wiring in DefaultEngines and lets tests inject
// deterministic fakes. When an engine lacks the inputs it needs for a given
// trip, the aggregator skips it gracefully with an "N/A: <reason>" note and
// never fabricates an opportunity.
package arbreport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/cabinarb"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/hotelarb"
)

// Confidence levels surfaced on each opportunity.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// HotelRebook bundles an existing hold with a fresh quote so the hotel engine
// can evaluate a lower-price re-book opportunity.
type HotelRebook struct {
	Hold  hotelarb.Hold
	Quote hotelarb.PriceQuote
	Opts  hotelarb.RebookOptions
}

// Params is the trip context plus any engine-specific inputs the caller can
// supply. Engine-specific fields are optional: when absent, that engine is
// skipped gracefully rather than guessed.
type Params struct {
	Origin      string
	Destination string
	DepartDate  string // YYYY-MM-DD
	ReturnDate  string // optional
	Currency    string // defaults to EUR
	Travelers   int

	// Optional engine inputs.
	CabinFares   []cabinarb.CabinFare            // cabin-class fare ladder
	HotelRebooks []HotelRebook                   // active holds + fresh quotes
	HotelPoints  []hotelarb.PointsArbitrageInput // cash-vs-points comparisons
}

// Opportunity is one ranked arbitrage finding in the unified report.
type Opportunity struct {
	Engine          string  `json:"engine"`
	Type            string  `json:"type"`
	Description     string  `json:"description"`
	EstimatedSaving float64 `json:"estimated_saving"`
	Currency        string  `json:"currency"`
	Confidence      string  `json:"confidence"`
}

// SkippedEngine records an engine that produced no opportunity and why.
type SkippedEngine struct {
	Engine string `json:"engine"`
	Reason string `json:"reason"`
}

// ArbReport is the unified, ranked arbitrage report.
type ArbReport struct {
	Origin        string          `json:"origin"`
	Destination   string          `json:"destination"`
	DepartDate    string          `json:"depart_date"`
	ReturnDate    string          `json:"return_date,omitempty"`
	Currency      string          `json:"currency"`
	Travelers     int             `json:"travelers"`
	Opportunities []Opportunity   `json:"opportunities"`
	Skipped       []SkippedEngine `json:"skipped"`
	Count         int             `json:"count"`
}

// EngineFunc adapts one underlying engine to the aggregator. It returns the
// opportunities found, or a non-empty skip reason when the engine could not
// run (missing inputs, no findings). When skip is non-empty the opportunities
// slice is ignored.
type EngineFunc func(ctx context.Context, p Params) (opps []Opportunity, skip string)

// Engines is the swappable set of engine seams the aggregator drives. Tests
// inject fakes here; DefaultEngines wires the real packages.
type Engines struct {
	Currency EngineFunc
	Cabin    EngineFunc
	Hotel    EngineFunc
}

type namedEngine struct {
	name string
	fn   EngineFunc
}

// ordered returns the engines in a deterministic invocation order.
func (e Engines) ordered() []namedEngine {
	return []namedEngine{
		{"currency", e.Currency},
		{"cabin", e.Cabin},
		{"hotel", e.Hotel},
	}
}

// DefaultEngines returns the production seams wired to the real engines.
func DefaultEngines() Engines {
	return Engines{
		Currency: defaultCurrencyEngine,
		Cabin:    defaultCabinEngine,
		Hotel:    defaultHotelEngine,
	}
}

// Aggregate runs every default engine for the trip context and returns one
// ranked report.
func Aggregate(ctx context.Context, p Params) ArbReport {
	return AggregateWithEngines(ctx, p, DefaultEngines())
}

// AggregateWithEngines is Aggregate with caller-supplied engine seams. It is
// the testable core: it never reaches the network itself, it only drives the
// seams it is handed.
func AggregateWithEngines(ctx context.Context, p Params, eng Engines) ArbReport {
	report := ArbReport{
		Origin:        p.Origin,
		Destination:   p.Destination,
		DepartDate:    p.DepartDate,
		ReturnDate:    p.ReturnDate,
		Currency:      currencyOrDefault(p.Currency),
		Travelers:     p.Travelers,
		Opportunities: []Opportunity{},
		Skipped:       []SkippedEngine{},
	}

	for _, ne := range eng.ordered() {
		if ne.fn == nil {
			report.Skipped = append(report.Skipped, SkippedEngine{
				Engine: ne.name,
				Reason: "N/A: engine not configured",
			})
			continue
		}
		opps, skip := ne.fn(ctx, p)
		if strings.TrimSpace(skip) != "" {
			report.Skipped = append(report.Skipped, SkippedEngine{Engine: ne.name, Reason: skip})
			continue
		}
		if len(opps) == 0 {
			report.Skipped = append(report.Skipped, SkippedEngine{
				Engine: ne.name,
				Reason: "N/A: no opportunities found",
			})
			continue
		}
		for _, o := range opps {
			if o.Engine == "" {
				o.Engine = ne.name
			}
			if o.Currency == "" {
				o.Currency = report.Currency
			}
			report.Opportunities = append(report.Opportunities, o)
		}
	}

	// Rank by estimated saving, descending. Deterministic tie-break by engine
	// then type so the output is stable across runs.
	sort.SliceStable(report.Opportunities, func(i, j int) bool {
		a, b := report.Opportunities[i], report.Opportunities[j]
		if a.EstimatedSaving != b.EstimatedSaving {
			return a.EstimatedSaving > b.EstimatedSaving
		}
		if a.Engine != b.Engine {
			return a.Engine < b.Engine
		}
		return a.Type < b.Type
	})
	report.Count = len(report.Opportunities)
	return report
}

// --- default engine adapters (reuse existing public APIs only) ---

func defaultCurrencyEngine(ctx context.Context, p Params) ([]Opportunity, string) {
	if p.Origin == "" || p.Destination == "" {
		return nil, "N/A: origin and destination required for currency arbitrage"
	}
	if p.DepartDate == "" {
		return nil, "N/A: departure date required for currency arbitrage"
	}
	found := hacks.DetectCurrencyArbitrage(ctx, hacks.DetectorInput{
		Origin:      p.Origin,
		Destination: p.Destination,
		Date:        p.DepartDate,
		ReturnDate:  p.ReturnDate,
		Currency:    p.Currency,
		Passengers:  p.Travelers,
	})
	if len(found) == 0 {
		return nil, "N/A: no currency arbitrage on this route"
	}
	out := make([]Opportunity, 0, len(found))
	for _, h := range found {
		out = append(out, Opportunity{
			Engine:          "currency",
			Type:            h.Type,
			Description:     h.Title,
			EstimatedSaving: h.Savings,
			Currency:        h.Currency,
			Confidence:      ConfidenceMedium,
		})
	}
	return out, ""
}

func defaultCabinEngine(_ context.Context, p Params) ([]Opportunity, string) {
	if len(p.CabinFares) == 0 {
		return nil, "N/A: no cabin fare ladder supplied for this trip"
	}
	recs := cabinarb.Detect(p.CabinFares)
	if len(recs) == 0 {
		return nil, "N/A: no near-flat cabin upgrades within threshold"
	}
	cur := fareCurrency(p)
	out := make([]Opportunity, 0, len(recs))
	for _, r := range recs {
		// A cabin upgrade is a cash saving only when the higher cabin is
		// priced at or below the baseline; otherwise the upsell is a value
		// trade, not a saving, so we report it honestly with zero saving.
		saving := r.BaselinePrice - r.TargetPrice
		if saving < 0 {
			saving = 0
		}
		out = append(out, Opportunity{
			Engine:          "cabin",
			Type:            "cabin_upgrade",
			Description:     fmt.Sprintf("%s → %s: %s", r.Baseline, r.Target, r.Reason),
			EstimatedSaving: saving,
			Currency:        cur,
			Confidence:      ConfidenceHigh,
		})
	}
	return out, ""
}

func defaultHotelEngine(_ context.Context, p Params) ([]Opportunity, string) {
	if len(p.HotelRebooks) == 0 && len(p.HotelPoints) == 0 {
		return nil, "N/A: no active hotel holds or points offers supplied"
	}
	var out []Opportunity
	for _, rb := range p.HotelRebooks {
		d := hotelarb.EvaluateRebook(rb.Hold, rb.Quote, rb.Opts)
		if d.Action == hotelarb.ActionRebookLowerPrice && d.Savings > 0 {
			out = append(out, Opportunity{
				Engine:          "hotel",
				Type:            "hotel_rebook",
				Description:     fmt.Sprintf("%s: %s", d.HotelName, d.Reason),
				EstimatedSaving: d.Savings,
				Currency:        d.Currency,
				Confidence:      ConfidenceHigh,
			})
		}
	}
	for _, in := range p.HotelPoints {
		res, err := hotelarb.ComparePointsArbitrage(in)
		if err != nil {
			continue
		}
		if res.Recommendation == hotelarb.RecommendUsePoints && res.BestOffer.SavingsVsCash > 0 {
			out = append(out, Opportunity{
				Engine:          "hotel",
				Type:            "hotel_points",
				Description:     res.Reason,
				EstimatedSaving: res.BestOffer.SavingsVsCash,
				Currency:        res.Currency,
				Confidence:      ConfidenceMedium,
			})
		}
	}
	if len(out) == 0 {
		return nil, "N/A: hotel inputs supplied but no profitable rebook or points opportunity"
	}
	return out, ""
}

func currencyOrDefault(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	if c == "" {
		return "EUR"
	}
	return c
}

// fareCurrency picks a sensible currency label for cabin opportunities: the
// first non-empty fare currency, then the trip currency, then EUR.
func fareCurrency(p Params) string {
	for _, f := range p.CabinFares {
		if strings.TrimSpace(f.Currency) != "" {
			return strings.ToUpper(strings.TrimSpace(f.Currency))
		}
	}
	return currencyOrDefault(p.Currency)
}
