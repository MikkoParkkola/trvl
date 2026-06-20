package nlsearch

// plan.go strengthens the keyword heuristic parser into a natural-language
// "supertool": it parses a free-form travel query into a normalized, structured
// Plan that names the travel mode, endpoints, dates, constraints, and — crucially
// — which search tool(s) the caller should dispatch with which parameters.
//
// It is intentionally deterministic and rule-based (no LLM dependency at
// runtime) so the CLI, the MCP surface, and tests all produce identical plans
// for identical input. BuildPlan reuses Heuristic for the base intent / endpoint
// / date extraction and layers constraint detection, relative-date resolution,
// destination-only ("to <City>") extraction, round-trip inference, and mode
// inference on top.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// Travel modes a Plan can route to.
const (
	ModeFlight     = "flight"
	ModeGround     = "ground"
	ModeHotel      = "hotel"
	ModeMultimodal = "multimodal"
	ModeHacks      = "hacks"
)

// Canonical constraint tokens. budget<=X is emitted dynamically with the
// numeric ceiling appended (e.g. "budget<=500").
const (
	ConstraintCheapest = "cheapest"
	ConstraintFastest  = "fastest"
	ConstraintNonstop  = "nonstop"
	ConstraintNoRedeye = "no-redeye"
)

// PlannedSearch names one concrete search the caller should dispatch. Tool is
// the MCP tool / CLI command name; Params are its arguments. The nlsearch
// package deliberately does NOT call the search packages itself (that would
// invert the dependency direction); it only returns the routing decision.
type PlannedSearch struct {
	Tool   string         `json:"tool"`
	Params map[string]any `json:"params"`
}

// Plan is the normalized routing decision for a natural-language travel query.
type Plan struct {
	Query       string          `json:"query"`
	Mode        string          `json:"mode"` // flight | ground | hotel | multimodal | hacks
	Origin      string          `json:"origin,omitempty"`
	Destination string          `json:"destination,omitempty"`
	Date        string          `json:"date,omitempty"`
	ReturnDate  string          `json:"return_date,omitempty"`
	RoundTrip   bool            `json:"round_trip"`
	MaxBudget   float64         `json:"max_budget,omitempty"`
	Constraints []string        `json:"constraints"`
	Searches    []PlannedSearch `json:"searches"`
	Notes       []string        `json:"notes,omitempty"`
}

// destPattern variants capture a destination phrase. We try "to <X>" first
// (directional), then "in <X>" (common for hotel queries). Candidates are only
// accepted when they resolve to a real airport, so time words like "in May" or
// "next month" are rejected by construction.
var (
	toDestPattern = regexp.MustCompile(`(?i)\bto\s+([A-Za-z][A-Za-z.\-]*(?:\s+[A-Za-z][A-Za-z.\-]*){0,2}?)(?:\s+(?:next|this|on|in|for|by|under|below|over|via|and|avoiding|without|with|before|after|that|so)\b|,|\.|$)`)
	inDestPattern = regexp.MustCompile(`(?i)\bin\s+([A-Za-z][A-Za-z.\-]*(?:\s+[A-Za-z][A-Za-z.\-]*){0,2}?)(?:\s+(?:next|this|on|for|by|under|below|over|and|with|before|after|that|so)\b|,|\.|$)`)
)

// extractDestination resolves a destination-only phrase to an IATA code, or "".
func extractDestination(query string) string {
	for _, re := range []*regexp.Regexp{toDestPattern, inDestPattern} {
		if m := re.FindStringSubmatch(query); len(m) == 2 {
			cand := strings.TrimSpace(m[1])
			if code := cityToIATA(cand); code != "" {
				return code
			}
			if up := strings.ToUpper(cand); len(up) == 3 && iataPattern.MatchString(up) && !commonEnglishUppercase[up] {
				return up
			}
		}
	}
	return ""
}

// budgetPattern captures a budget ceiling expressed after a trigger word, with
// an optional currency symbol/code. e.g. "under EUR 500", "budget of 500",
// "max 1200", "below $300".
var budgetTriggerPattern = regexp.MustCompile(`(?i)(?:under|below|less than|at most|max(?:imum)?|budget(?:\s+of)?|up to|no more than|<=?)\s*(?:eur|usd|gbp|€|\$|£)?\s*([0-9][0-9.,]*)`)

// currencyAmountPattern captures a currency-prefixed amount anywhere, e.g.
// "EUR 500", "€500", "$1,200". Used as a fallback when no trigger word matches.
var currencyAmountPattern = regexp.MustCompile(`(?i)(?:eur|usd|gbp|€|\$|£)\s*([0-9][0-9.,]*)`)

// BuildPlan parses a free-form travel query into a normalized Plan. `today`
// must be in ISO 8601 calendar form (YYYY-MM-DD). It never errors: an
// unrecognized query yields a best-effort Plan with empty endpoints and a note.
func BuildPlan(query, today string) Plan {
	base := Heuristic(query, today)
	lower := strings.ToLower(query)

	plan := Plan{
		Query:       strings.TrimSpace(query),
		Origin:      base.Origin,
		Destination: base.Destination,
		Date:        base.Date,
		ReturnDate:  base.ReturnDate,
		MaxBudget:   base.MaxBudget,
		Constraints: []string{},
		Searches:    []PlannedSearch{},
	}

	// Destination-only extraction when Heuristic found no destination (queries
	// without an explicit "from ... to ..." pair, e.g. "way to Tromso" or
	// "hotel in Prague").
	if plan.Destination == "" {
		plan.Destination = extractDestination(query)
	}

	// Budget extraction → MaxBudget + dynamic "budget<=X" constraint.
	if plan.MaxBudget == 0 {
		plan.MaxBudget = extractBudget(query)
	}

	// Relative-date resolution for phrasings Heuristic does not cover
	// ("next week", "next month"). Heuristic already handles weekends + explicit
	// and natural dates.
	if plan.Date == "" {
		if d := resolveRelativeDate(lower, today); d != "" {
			plan.Date = d
			plan.Notes = append(plan.Notes, "departure date approximated from relative phrase")
		}
	}

	// Round-trip inference.
	plan.RoundTrip = base.ReturnDate != "" || isRoundTrip(lower)
	if plan.RoundTrip && plan.ReturnDate == "" && plan.Date != "" {
		if t, err := models.ParseDate(plan.Date); err == nil {
			plan.ReturnDate = t.AddDate(0, 0, 7).Format(time.DateOnly)
			plan.Notes = append(plan.Notes, "return date approximated as one week after departure")
		}
	}

	// Constraint detection (deterministic, fixed order).
	plan.Constraints = detectConstraints(lower, plan.MaxBudget)

	// Mode inference.
	plan.Mode = inferMode(lower, base.Intent, plan.Origin, plan.Destination)

	// Searches assembly — name which search(es) to dispatch.
	plan.Searches = buildSearches(plan, base)

	return plan
}

// extractBudget returns the first budget ceiling found in the query, or 0.
func extractBudget(query string) float64 {
	if m := budgetTriggerPattern.FindStringSubmatch(query); len(m) == 2 {
		if v := parseAmount(m[1]); v > 0 {
			return v
		}
	}
	if m := currencyAmountPattern.FindStringSubmatch(query); len(m) == 2 {
		if v := parseAmount(m[1]); v > 0 {
			return v
		}
	}
	return 0
}

// parseAmount converts a captured number string ("1,200", "500.00") to a float.
func parseAmount(s string) float64 {
	clean := strings.ReplaceAll(s, ",", "")
	clean = strings.TrimRight(clean, ".")
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0
	}
	return v
}

// resolveRelativeDate maps "next week" / "next month" to a concrete ISO date.
// Returns "" when no relative phrase is present.
func resolveRelativeDate(lower, today string) string {
	t, err := models.ParseDate(today)
	if err != nil {
		return ""
	}
	switch {
	case strings.Contains(lower, "next month"):
		return t.AddDate(0, 1, 0).Format(time.DateOnly)
	case strings.Contains(lower, "next week"):
		return t.AddDate(0, 0, 7).Format(time.DateOnly)
	}
	return ""
}

// isRoundTrip reports whether the query phrasing implies a return leg.
func isRoundTrip(lower string) bool {
	for _, kw := range []string{"and back", "round trip", "round-trip", "roundtrip", "return trip", "there and back", "both ways", "and return"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// detectConstraints extracts the ordered constraint list. budget<=X is appended
// last when a budget ceiling was found.
func detectConstraints(lower string, budget float64) []string {
	out := []string{}
	if containsAny(lower, "cheapest", "cheap", "lowest price", "lowest fare", "affordable", "budget-friendly", "least expensive", "save money") {
		out = append(out, ConstraintCheapest)
	}
	if containsAny(lower, "fastest", "quickest", "shortest", "asap", "as fast", "quickly", "minimal travel time") {
		out = append(out, ConstraintFastest)
	}
	if containsAny(lower, "nonstop", "non-stop", "non stop", "direct", "no layover", "no layovers", "no stops", "no connection", "no connections") {
		out = append(out, ConstraintNonstop)
	}
	if detectNoRedeye(lower) {
		out = append(out, ConstraintNoRedeye)
	}
	if budget > 0 {
		out = append(out, fmt.Sprintf("budget<=%s", strconv.FormatFloat(budget, 'f', -1, 64)))
	}
	return out
}

// detectNoRedeye reports whether the query asks to avoid red-eye / overnight
// departures. Bare "red-eye" is treated as avoidance (the term carries negative
// connotation); "overnight" only counts when paired with an avoidance verb so
// "overnight train" (often desired) is not misclassified.
func detectNoRedeye(lower string) bool {
	if containsAny(lower, "red-eye", "redeye", "red eye") {
		return true
	}
	if strings.Contains(lower, "overnight") && containsAny(lower, "no ", "avoid", "without", "skip", "not ", "rather not", "don't want") {
		return true
	}
	return false
}

// inferMode determines the travel mode with a fixed precedence. base is the
// keyword intent from Heuristic ("hotel"/"flight"/"deals"/"route").
func inferMode(lower, baseIntent, origin, destination string) string {
	switch {
	case baseIntent == "hotel":
		return ModeHotel
	case containsAny(lower, "hack", "hacks", "hidden city", "hidden-city", "error fare", "throwaway", "skiplag", "trick", "loophole"):
		return ModeHacks
	case containsAny(lower, "train", "rail", "by rail", "bus", "coach", "ferry", "boat", "by sea", "overland"):
		return ModeGround
	case containsAny(lower, "multimodal", "multi-modal", "any way", "cheapest way", "best way", "fastest way", "how to get", "way to get", "get to", "get from", "how do i get", "any mode"):
		return ModeMultimodal
	case baseIntent == "flight" || containsAny(lower, "fly ", "flying", "flight", "by air"):
		return ModeFlight
	default:
		// Endpoint shape tie-break: two IATA airports with no ground/multimodal
		// signal reads as air travel; otherwise default to multimodal so the
		// caller compares options.
		if isIATA(origin) && isIATA(destination) {
			return ModeFlight
		}
		return ModeMultimodal
	}
}

// buildSearches names the concrete search(es) to dispatch for the plan's mode.
func buildSearches(p Plan, base Params) []PlannedSearch {
	switch p.Mode {
	case ModeHotel:
		location := base.Location
		if location == "" {
			location = p.Destination
		}
		params := map[string]any{"location": location}
		if base.CheckIn != "" {
			params["check_in"] = base.CheckIn
		} else if p.Date != "" {
			params["check_in"] = p.Date
		}
		if base.CheckOut != "" {
			params["check_out"] = base.CheckOut
		} else if p.ReturnDate != "" {
			params["check_out"] = p.ReturnDate
		}
		addBudget(params, "max_price", p.MaxBudget)
		return []PlannedSearch{{Tool: "search_hotels", Params: params}}

	case ModeFlight:
		params := map[string]any{"origin": p.Origin, "destination": p.Destination}
		if p.Date != "" {
			params["departure_date"] = p.Date
		}
		if p.ReturnDate != "" {
			params["return_date"] = p.ReturnDate
		}
		addBudget(params, "max_price", p.MaxBudget)
		return []PlannedSearch{{Tool: "search_flights", Params: params}}

	case ModeGround:
		params := map[string]any{"origin": p.Origin, "destination": p.Destination, "avoid": "flight"}
		if p.Date != "" {
			params["date"] = p.Date
		}
		addBudget(params, "max_price", p.MaxBudget)
		return []PlannedSearch{{Tool: "search_route", Params: params}}

	case ModeHacks:
		params := map[string]any{"origin": p.Origin, "destination": p.Destination}
		if p.Date != "" {
			params["date"] = p.Date
		}
		if p.ReturnDate != "" {
			params["return_date"] = p.ReturnDate
		}
		return []PlannedSearch{{Tool: "detect_travel_hacks", Params: params}}

	default: // ModeMultimodal — compare a multimodal route and direct flights.
		routeParams := map[string]any{"origin": p.Origin, "destination": p.Destination}
		if p.Date != "" {
			routeParams["date"] = p.Date
		}
		addBudget(routeParams, "max_price", p.MaxBudget)
		flightParams := map[string]any{"origin": p.Origin, "destination": p.Destination}
		if p.Date != "" {
			flightParams["departure_date"] = p.Date
		}
		if p.ReturnDate != "" {
			flightParams["return_date"] = p.ReturnDate
		}
		addBudget(flightParams, "max_price", p.MaxBudget)
		return []PlannedSearch{
			{Tool: "search_route", Params: routeParams},
			{Tool: "search_flights", Params: flightParams},
		}
	}
}

func addBudget(params map[string]any, key string, budget float64) {
	if budget > 0 {
		params[key] = budget
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func isIATA(s string) bool {
	return len(s) == 3 && iataPattern.MatchString(strings.ToUpper(s))
}
