// Package transfer turns raw ground routes into the door-to-door comparison
// card: every transport mode as a choosable TransferOption with time, price,
// pros, cons, and grounded step-by-step instructions. trvl never imposes a
// single "best"; it labels cheapest/fastest/best-value/luggage so the traveller
// decides by their own priority.
package transfer

import (
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/transfer/airportkb"
)

// BuildOptions enriches raw ground routes into a TransferComparison for one
// leg. airportCode is the IATA code of the airport endpoint (used to ground
// step instructions from the curated KB); pass "" when neither endpoint is an
// airport. from/to are display labels for the leg. Ride-hail deep-links (Uber,
// Bolt) are appended as additional choosable options when from/to are known.
func BuildOptions(routes []models.GroundRoute, airportCode, from, to string) models.TransferComparison {
	profile, hasProfile := airportkb.Lookup(airportCode)
	var profPtr *airportkb.Profile
	if hasProfile {
		profPtr = &profile
	}

	opts := make([]models.TransferOption, 0, len(routes))
	for _, r := range routes {
		opt := models.TransferOption{
			Mode:          classifyMode(r),
			Label:         routeLabel(r),
			TotalPrice:    r.Price,
			Currency:      r.Currency,
			DoorToDoorMin: r.Duration,
			Changes:       r.Transfers,
			BookURL:       r.BookingURL,
		}
		// Taxi/private fares are estimates, not booked quotes.
		if opt.Mode == "taxi" || opt.Mode == "private_transfer" {
			opt.PriceIsEstimate = true
		}
		opt.Steps = AssembleSteps(r, opt.Mode, profPtr)
		opts = append(opts, opt)
	}

	// Provider breadth (E.1): ride-hail deep-links as additional options, but
	// only when there is at least one real route to compare against (avoid
	// fabricating a card from nothing).
	if len(opts) > 0 {
		opts = append(opts, RideHailOptions(from, to)...)
	}

	// Pros/cons need the full set (price ratios, fastest, etc.).
	annotateProsCons(opts)

	cmp := models.TransferComparison{From: from, To: to, Options: opts}
	labelComparison(&cmp)
	return cmp
}

// classifyMode maps a GroundRoute to a transfer mode label. Conservative: it
// only asserts specific modes (airport_express/metro) when the route signals
// them; otherwise it falls back to the route Type.
func classifyMode(r models.GroundRoute) string {
	p := strings.ToLower(r.Provider)
	t := strings.ToLower(r.Type)
	switch {
	case p == "taxi":
		return "taxi"
	case strings.Contains(p, "transfer") || strings.Contains(p, "pickup") || strings.Contains(p, "mozio"):
		return "private_transfer"
	case strings.Contains(p, "metro") || strings.Contains(p, "subway"):
		return "metro"
	case strings.Contains(p, "aerobus") || strings.Contains(p, "express") || strings.Contains(p, "shuttle"):
		return "airport_express"
	case t == "train" || t == "bus" || t == "mixed":
		return t
	default:
		return "transit"
	}
}

func routeLabel(r models.GroundRoute) string {
	if r.Provider != "" {
		return r.Provider
	}
	if r.Type != "" {
		return titleASCII(r.Type)
	}
	return "Transfer"
}

// titleASCII capitalizes the first letter of an ASCII label (provider/type
// labels are lowercase ASCII). Avoids the deprecated strings.Title.
func titleASCII(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}

// labelComparison fills the cheapest/fastest/best-value/luggage sort labels.
// No option is removed and none is marked "the answer"; these are affordances.
// Options with unknown price/duration (e.g. ride-hail deep-links) are skipped
// for cheapest/fastest/best-value so they never falsely win on a zero value.
func labelComparison(cmp *models.TransferComparison) {
	if len(cmp.Options) == 0 {
		return
	}
	var cheapest, fastest *models.TransferOption
	for i := range cmp.Options {
		o := &cmp.Options[i]
		if o.TotalPrice > 0 && (cheapest == nil || o.TotalPrice < cheapest.TotalPrice) {
			cheapest = o
		}
		if o.DoorToDoorMin > 0 && (fastest == nil || o.DoorToDoorMin < fastest.DoorToDoorMin) {
			fastest = o
		}
	}
	if cheapest != nil {
		cmp.Cheapest = cheapest.Mode
	}
	if fastest != nil {
		cmp.Fastest = fastest.Mode
	}
	if bv := bestValue(cmp.Options); bv != nil {
		cmp.BestValue = bv.Mode
	}
	cmp.LuggageBest = mostLuggageFriendly(cmp.Options).Mode
}

// bestValue ranks priced+timed options by a normalized price+time score
// (lower is better). Options with unknown price or duration are excluded.
// Returns nil when no option has both a price and a duration.
func bestValue(opts []models.TransferOption) *models.TransferOption {
	priced := make([]models.TransferOption, 0, len(opts))
	for _, o := range opts {
		if o.TotalPrice > 0 && o.DoorToDoorMin > 0 {
			priced = append(priced, o)
		}
	}
	if len(priced) == 0 {
		return nil
	}
	minP, maxP := priced[0].TotalPrice, priced[0].TotalPrice
	minT, maxT := priced[0].DoorToDoorMin, priced[0].DoorToDoorMin
	for _, o := range priced[1:] {
		minP, maxP = minf(minP, o.TotalPrice), maxf(maxP, o.TotalPrice)
		minT, maxT = mini(minT, o.DoorToDoorMin), maxi(maxT, o.DoorToDoorMin)
	}
	bestIdx := 0
	bestScore := score(priced[0], minP, maxP, minT, maxT)
	for i := 1; i < len(priced); i++ {
		if s := score(priced[i], minP, maxP, minT, maxT); s < bestScore {
			bestIdx, bestScore = i, s
		}
	}
	return &priced[bestIdx]
}

func score(o models.TransferOption, minP, maxP float64, minT, maxT int) float64 {
	np := norm(o.TotalPrice, minP, maxP)
	nt := norm(float64(o.DoorToDoorMin), float64(minT), float64(maxT))
	return np + nt // equal weight; tunable later
}

// mostLuggageFriendly prefers door-to-door modes (taxi/private), then fewest
// changes. Deterministic tie-break by mode name keeps output stable.
func mostLuggageFriendly(opts []models.TransferOption) models.TransferOption {
	sorted := append([]models.TransferOption(nil), opts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := doorToDoorRank(sorted[i].Mode), doorToDoorRank(sorted[j].Mode)
		if di != dj {
			return di < dj
		}
		if sorted[i].Changes != sorted[j].Changes {
			return sorted[i].Changes < sorted[j].Changes
		}
		return sorted[i].Mode < sorted[j].Mode
	})
	return sorted[0]
}

func doorToDoorRank(mode string) int {
	switch mode {
	case "taxi", "private_transfer":
		return 0
	case "airport_express":
		return 1
	default:
		return 2
	}
}

func norm(v, lo, hi float64) float64 {
	if hi == lo {
		return 0
	}
	return (v - lo) / (hi - lo)
}
func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
