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
			// No carrier identity on this leg — fall back to route+time so we
			// never collapse genuinely different itineraries.
			parts = append(parts, canonPlaceKey(leg.DepartureAirport.Code)+">"+canonPlaceKey(leg.ArrivalAirport.Code)+"@"+canonTimeKey(leg.DepartureTime))
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

// recomputeSourceEconomics sets the headline price to the cheapest source and
// fills Savings (dearest - cheapest) and the cheapest provider name. To avoid a
// false "cheapest" across currencies, comparison is restricted to sources that
// share the currency of the first priced source; differently-priced-currency
// sources are kept in Sources but excluded from the min/savings math.
func recomputeSourceEconomics(sources []PriceSource) (cheapest float64, currency, cheapestProvider, bookingURL string, savings float64) {
	if len(sources) == 0 {
		return 0, "", "", "", 0
	}
	// Pick the comparison currency: the first source with a positive price.
	cmpCur := ""
	for _, s := range sources {
		if s.Price > 0 {
			cmpCur = s.Currency
			break
		}
	}
	minIdx := -1
	var maxPrice float64
	for i, s := range sources {
		if s.Price <= 0 || s.Currency != cmpCur {
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
	}
	dst.CheapestSource = prov
	dst.Savings = savings
}

func foldGroundSource(dst *GroundRoute, src GroundRoute) {
	now := time.Now()
	dst.Sources = append(dst.Sources, PriceSource{
		Provider:    src.Provider,
		Price:       src.Price,
		MaxPrice:    src.PriceMax,
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
	}
	dst.CheapestSource = prov
	dst.Savings = savings
}

// ResolveFlightSources collapses duplicate itineraries across providers into one
// FlightResult each, carrying every provider as a PriceSource, cheapest headline.
func ResolveFlightSources(flights []FlightResult) []FlightResult {
	return ResolveSources(flights, FlightIdentityKey, foldFlightSource)
}

// ResolveGroundSources does the same for ground connections.
func ResolveGroundSources(routes []GroundRoute) []GroundRoute {
	return ResolveSources(routes, GroundIdentityKey, foldGroundSource)
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
