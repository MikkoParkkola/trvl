// Package models — resolve.go provides multi-source resolution: collapsing the
// same physical flight or ground connection returned by several providers into
// one canonical result carrying a []PriceSource, exactly as MergeHotelResults
// does for hotels. Hotels need fuzzy geo matching (no stable cross-source id);
// flights and ground have precise identity (carrier+number+time / operator+
// station+time), so this uses deterministic exact-key matching — not haversine.
//
// Tracking: MIK-4951 (parent MIK-4948).
package models

import (
	"sort"
	"strings"
	"time"
)

// ResolveSources collapses items sharing an identity key into one canonical
// item that accumulates sources via fold. The first occurrence of a key is
// canonical and is folded with itself to seed its source list. Items with an
// empty key are passed through unchanged (cannot be deduplicated).
func ResolveSources[T any](items []T, keyFn func(T) string, fold func(dst *T, src T)) []T {
	out := make([]T, 0, len(items))
	idx := make(map[string]int, len(items))
	for _, it := range items {
		k := keyFn(it)
		if k == "" {
			out = append(out, it)
			continue
		}
		if p, ok := idx[k]; ok {
			fold(&out[p], it)
			continue
		}
		seed := it
		fold(&seed, it)
		idx[k] = len(out)
		out = append(out, seed)
	}
	return out
}

// canonTimeKey normalizes a timestamp string for identity comparison.
func canonTimeKey(s string) string {
	if t, ok := ParseTemporal(s); ok {
		return t.UTC().Format("2006-01-02T15:04")
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func canonPlaceKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// FlightIdentityKey is the deterministic identity of an itinerary: the ordered
// sequence of (airline code + flight number + departure time) across its legs.
// Two provider results with the same key are the same physical flights.
func FlightIdentityKey(f FlightResult) string {
	if len(f.Legs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f.Legs))
	for _, leg := range f.Legs {
		code := strings.ToUpper(strings.TrimSpace(leg.AirlineCode))
		num := strings.TrimSpace(leg.FlightNumber)
		if code == "" && num == "" {
			// No carrier identity on this leg — fall back to route+dep-arr time so we
			// never collapse genuinely different itineraries (arrival time included
			// so distinct routings on the same dep-minute don't collapse).
			parts = append(parts, canonPlaceKey(leg.DepartureAirport.Code)+">"+canonPlaceKey(leg.ArrivalAirport.Code)+"@"+canonTimeKey(leg.DepartureTime)+"-"+canonTimeKey(leg.ArrivalTime))
			continue
		}
		parts = append(parts, code+num+"@"+canonTimeKey(leg.DepartureTime))
	}
	return strings.Join(parts, "|")
}

// GroundIdentityKey is the deterministic identity of a ground connection:
// type + endpoints + times + the operator sequence of its legs.
func GroundIdentityKey(r GroundRoute) string {
	dep := canonPlaceKey(r.Departure.Station)
	if dep == "" {
		dep = canonPlaceKey(r.Departure.City)
	}
	arr := canonPlaceKey(r.Arrival.Station)
	if arr == "" {
		arr = canonPlaceKey(r.Arrival.City)
	}
	if dep == "" || arr == "" {
		return ""
	}
	ops := make([]string, 0, len(r.Legs))
	for _, leg := range r.Legs {
		ops = append(ops, strings.ToLower(strings.TrimSpace(leg.Provider)))
	}
	return strings.ToLower(r.Type) + "|" + dep + ">" + arr +
		"@" + canonTimeKey(r.Departure.Time) + "-" + canonTimeKey(r.Arrival.Time) +
		"|" + strings.Join(ops, ",")
}

// dominantCurrency selects the currency for headline cheapest computation
// in an order-independent way: the currency with the highest number of
// Price>0 sources (most-represented). Ties are broken by the
// lexicographically smallest uppercased currency code (not first-seen order).
// Currency grouping is case-insensitive; the returned string is taken from
// a source's Currency value (as-is) to preserve existing casing style in results.
func dominantCurrency(sources []PriceSource) string {
	if len(sources) == 0 {
		return ""
	}
	count := map[string]int{}
	orig := map[string]string{} // upper -> a source's original Currency for that group
	for _, s := range sources {
		if s.Price <= 0 {
			continue
		}
		u := strings.ToUpper(strings.TrimSpace(s.Currency))
		if u == "" {
			continue
		}
		count[u]++
		if _, ok := orig[u]; !ok {
			orig[u] = s.Currency
		}
	}
	if len(count) == 0 {
		return ""
	}
	maxC := 0
	for _, c := range count {
		if c > maxC {
			maxC = c
		}
	}
	var best []string
	for u, c := range count {
		if c == maxC {
			best = append(best, u)
		}
	}
	sort.Strings(best)
	chosenU := best[0]
	if o, ok := orig[chosenU]; ok && o != "" {
		return o
	}
	return chosenU
}

// recomputeSourceEconomics sets the headline price to the cheapest source and
// fills Savings (dearest - cheapest) and the cheapest provider name. To avoid a
// false "cheapest" across currencies, comparison is restricted to sources that
// share the (now deterministically chosen) currency; differently-currency
// sources are kept in Sources but excluded from the min/savings math.
func recomputeSourceEconomics(sources []PriceSource) (cheapest float64, currency, cheapestProvider, bookingURL string, savings float64) {
	if len(sources) == 0 {
		return 0, "", "", "", 0
	}
	cmpCur := dominantCurrency(sources)
	// Use case-insensitive compare for the group (per spec); return values
	// come from the actual min source's fields (as-is).
	cmpU := strings.ToUpper(strings.TrimSpace(cmpCur))
	minIdx := -1
	var maxPrice float64
	for i, s := range sources {
		su := strings.ToUpper(strings.TrimSpace(s.Currency))
		if s.Price <= 0 || su != cmpU {
			continue
		}
		if minIdx == -1 || s.Price < sources[minIdx].Price {
			minIdx = i
		}
		if s.Price > maxPrice {
			maxPrice = s.Price
		}
	}
	if minIdx == -1 {
		return 0, sources[0].Currency, "", sources[0].BookingURL, 0
	}
	m := sources[minIdx]
	return m.Price, m.Currency, m.Provider, m.BookingURL, maxPrice - m.Price
}

func foldFlightSource(dst *FlightResult, src FlightResult) {
	now := time.Now()
	dst.Sources = append(dst.Sources, PriceSource{
		Provider:    src.Provider,
		Price:       src.Price,
		Currency:    src.Currency,
		BookingURL:  src.BookingURL,
		RetrievedAt: now,
		Freshness:   ClassifyFreshness(src.Provider, now, now),
	})
	price, cur, prov, url, savings := recomputeSourceEconomics(dst.Sources)
	if price > 0 {
		dst.Price = price
		dst.Currency = cur
		dst.Provider = prov
		dst.BookingURL = url
		// Fare-risk attributes must travel with the cheapest headline price so a
		// consumer never ranks the cheapest provider while reading another
		// provider's baggage / self-connect / fare terms (Frankenfare fix).
		if prov == src.Provider {
			dst.SelfConnect = src.SelfConnect
			dst.FareType = src.FareType
			dst.Warnings = src.Warnings
			dst.CarryOnIncluded = src.CarryOnIncluded
			dst.CheckedBagsIncluded = src.CheckedBagsIncluded
			dst.ComparablePrice = src.ComparablePrice
			dst.ComparableBreakdown = src.ComparableBreakdown
			dst.Confidence = src.Confidence
		}
	}
	dst.CheapestSource = prov
	dst.Savings = savings
}

// ResolveFlightSources collapses duplicate itineraries across providers into one
// FlightResult each, carrying every provider as a PriceSource, cheapest headline.
func ResolveFlightSources(flights []FlightResult) []FlightResult {
	return ResolveSources(flights, FlightIdentityKey, foldFlightSource)
}

// ResolveGroundSources collapses the same ground connection returned by
// several providers into one route carrying every provider as a PriceSource.
// The first occurrence keeps its place and its identity fields; every headline
// field comes from the single winning source (see groundHeadline). Routes with
// no identity key pass through unchanged.
func ResolveGroundSources(routes []GroundRoute) []GroundRoute {
	out := make([]GroundRoute, 0, len(routes))
	groups := map[string]int{}
	var members [][]GroundRoute
	for _, r := range routes {
		k := GroundIdentityKey(r)
		if k == "" {
			out = append(out, r)
			members = append(members, nil)
			continue
		}
		if p, ok := groups[k]; ok {
			members[p] = append(members[p], r)
			continue
		}
		groups[k] = len(out)
		out = append(out, r)
		members = append(members, []GroundRoute{r})
	}
	for i, m := range members {
		if m != nil {
			out[i] = resolveGroundGroup(m)
		}
	}
	return out
}

// resolveGroundGroup builds one route from every provider's copy of it.
func resolveGroundGroup(m []GroundRoute) GroundRoute {
	now := time.Now()
	dst := m[0]
	dst.Sources = make([]PriceSource, 0, len(m))
	for _, r := range m {
		dst.Sources = append(dst.Sources, PriceSource{
			Provider:        r.Provider,
			Price:           r.Price,
			MaxPrice:        r.PriceMax,
			Currency:        r.Currency,
			BookingURL:      r.BookingURL,
			RetrievedAt:     now,
			Freshness:       groundSourceFreshness(r, now),
			Provenance:      r.PriceProvenance,
			ComparablePrice: r.ComparablePrice,
		})
	}
	w, savings := groundHeadline(m)
	dst.Savings = savings
	if w < 0 {
		dst.CheapestSource = ""
		return dst
	}
	win := m[w]
	dst.Price, dst.Currency, dst.Provider, dst.BookingURL = win.Price, win.Currency, win.Provider, win.BookingURL
	dst.PriceMax, dst.Amenities, dst.SeatsLeft = win.PriceMax, win.Amenities, win.SeatsLeft
	dst.Confidence, dst.ComparablePrice, dst.PriceProvenance = win.Confidence, win.ComparablePrice, win.PriceProvenance
	dst.CheapestSource = win.Provider
	return dst
}

// groundSourceFreshness: only a live fare has a retrieval age worth judging;
// an estimate is static and an unavailable price has none.
func groundSourceFreshness(r GroundRoute, now time.Time) string {
	switch {
	case r.Price <= 0 || r.PriceProvenance == PriceUnavailable:
		return ""
	case r.PriceProvenance == PriceLive:
		return ClassifyFreshness(r.Provider, now, now)
	default:
		return FreshnessStatic
	}
}

// groundHeadline picks the winning copy of one ground connection and the
// savings inside its group. Priced routes compete in one unit: the comparable
// group (ComparablePrice set by the normalise pass) when it is non-empty,
// otherwise the dominant raw currency. Inside that unit a live fare beats an
// estimate, and price decides within a tier. Savings is measured in the same
// unit and tier, so an estimate never reads as a saving against a live fare.
// Returns -1 when no copy is priced.
func groundHeadline(m []GroundRoute) (winner int, savings float64) {
	comparable := false
	for _, r := range m {
		if r.Price > 0 && r.ComparablePrice > 0 {
			comparable = true
			break
		}
	}
	var domU string
	if !comparable {
		srcs := make([]PriceSource, len(m))
		for i, r := range m {
			srcs[i] = PriceSource{Price: r.Price, Currency: r.Currency}
		}
		domU = strings.ToUpper(strings.TrimSpace(dominantCurrency(srcs)))
	}
	amount := func(r GroundRoute) float64 {
		if comparable {
			return r.ComparablePrice
		}
		return r.Price
	}
	inUnit := func(r GroundRoute) bool {
		if r.Price <= 0 {
			return false
		}
		if comparable {
			return r.ComparablePrice > 0
		}
		return strings.ToUpper(strings.TrimSpace(r.Currency)) == domU
	}
	tier := func(r GroundRoute) int {
		if r.PriceProvenance == PriceLive {
			return 0
		}
		return 1
	}
	winner = -1
	for i, r := range m {
		if !inUnit(r) {
			continue
		}
		if winner < 0 || tier(r) < tier(m[winner]) || (tier(r) == tier(m[winner]) && amount(r) < amount(m[winner])) {
			winner = i
		}
	}
	if winner < 0 {
		return -1, 0
	}
	maxAmt := 0.0
	for _, r := range m {
		if inUnit(r) && tier(r) == tier(m[winner]) && amount(r) > maxAmt {
			maxAmt = amount(r)
		}
	}
	return winner, maxAmt - amount(m[winner])
}

// SortFlightsByPrice is a stable cheapest-first sort helper (price 0 sinks last).
func SortFlightsByPrice(flights []FlightResult) {
	sort.SliceStable(flights, func(i, j int) bool {
		a, b := flights[i].Price, flights[j].Price
		if a <= 0 {
			return false
		}
		if b <= 0 {
			return true
		}
		return a < b
	})
}
