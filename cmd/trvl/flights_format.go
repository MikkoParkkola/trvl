package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/points"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

func flightProviderLabel(f models.FlightResult) string {
	switch strings.ToLower(strings.TrimSpace(f.Provider)) {
	case "":
		return ""
	case "google_flights":
		return "Google"
	case "kiwi":
		return "Kiwi"
	default:
		return f.Provider
	}
}

// printBookingLinks lists the direct booking URL for each flight, numbered to
// match the table's "#" column so every option in the grid is actionable.
// Flights without a URL are skipped; nothing is printed when none carry a link.
func printBookingLinks(w io.Writer, flights []models.FlightResult) {
	type linkRow struct {
		idx   int
		label string
		url   string
	}
	var links []linkRow
	for i, f := range flights {
		if strings.TrimSpace(f.BookingURL) == "" {
			continue
		}
		label := flightAirlinesDisplay(f)
		if p := flightProviderLabel(f); p != "" {
			if label != "" {
				label += " · " + p
			} else {
				label = p
			}
		}
		if label == "" {
			label = "-"
		}
		links = append(links, linkRow{idx: i + 1, label: label, url: f.BookingURL})
	}
	if len(links) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Booking links:")
	for _, l := range links {
		_, _ = fmt.Fprintf(w, "  [%d] %s — %s\n", l.idx, l.label, l.url)
	}
}

func flightWarnings(f models.FlightResult) string {
	if len(f.Warnings) > 0 {
		return strings.Join(f.Warnings, "; ")
	}
	if f.SelfConnect {
		return "Self-connect: protect your own connection"
	}
	return ""
}

// flightRoute builds a route string like "HEL -> FRA -> NRT", annotating
// connection airports with their layover duration, e.g.
// "HEL -> FRA (2h00) -> NRT". Nonstop flights render as "AMS -> HEL". A composed
// round-trip (legs tagged outbound/inbound) renders each direction separately
// joined by "  ||  " so the return is visible, e.g. "HEL -> BCN  ||  BCN -> HEL"
// — never a single chain that disguises the turnaround as a connection.
func flightRoute(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return ""
	}
	if out, in := splitLegsByDirection(f.Legs); in != nil {
		return legRoute(out) + "  ||  " + legRoute(in)
	}
	return legRoute(f.Legs)
}

// splitLegsByDirection partitions legs into outbound and inbound when the result
// is a composed round-trip (legs carry a "inbound" Direction tag). For a one-way
// result (no inbound legs) it returns (legs, nil) so callers render a single
// chain unchanged.
func splitLegsByDirection(legs []models.FlightLeg) (outbound, inbound []models.FlightLeg) {
	for _, leg := range legs {
		if leg.Direction == "inbound" {
			inbound = append(inbound, leg)
		} else {
			outbound = append(outbound, leg)
		}
	}
	if len(inbound) == 0 {
		return legs, nil
	}
	return outbound, inbound
}

// legRoute renders one contiguous set of legs as "A -> B (layover) -> C".
func legRoute(legs []models.FlightLeg) string {
	if len(legs) == 0 {
		return ""
	}
	parts := []string{legs[0].DepartureAirport.Code}
	for i, leg := range legs {
		// Annotate the connection airport (arrival of a non-final leg) with the
		// layover before the next leg, when the model carries it.
		if i < len(legs)-1 {
			lo := legs[i+1].LayoverMinutes
			if lo > 0 {
				parts = append(parts, fmt.Sprintf("%s (%s)", leg.ArrivalAirport.Code, formatDuration(lo)))
				continue
			}
		}
		parts = append(parts, leg.ArrivalAirport.Code)
	}
	return strings.Join(parts, " -> ")
}

// flightAirlinesDisplay returns the operating carrier(s). A single-carrier
// itinerary shows that airline; a mixed itinerary joins distinct carriers with
// " / " so connection flights reveal every operator (e.g. "Brussels / Lufthansa").
func flightAirlinesDisplay(f models.FlightResult) string {
	var names []string
	seen := map[string]bool{}
	for _, leg := range f.Legs {
		name := strings.TrimSpace(leg.Airline)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, " / ")
}

// flightNumbersDisplay joins every leg's flight number with " / " so a
// connection shows each segment (e.g. "SN2611 / LH882"). Empty leg numbers
// (some providers omit them) are skipped; an all-empty itinerary yields "-".
func flightNumbersDisplay(f models.FlightResult) string {
	var nums []string
	for _, leg := range f.Legs {
		n := strings.TrimSpace(leg.FlightNumber)
		if n == "" {
			continue
		}
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return "-"
	}
	return strings.Join(nums, " / ")
}

// flightAircraftDisplay joins every leg's aircraft type with " / " so a
// connection shows the plane on each segment (e.g. "A319 / A320"). Manufacturer
// prefixes are trimmed for table width. An all-empty itinerary yields "-".
func flightAircraftDisplay(f models.FlightResult) string {
	var craft []string
	for _, leg := range f.Legs {
		c := shortAircraft(leg.Aircraft)
		if c == "" {
			continue
		}
		craft = append(craft, c)
	}
	if len(craft) == 0 {
		return "-"
	}
	return strings.Join(craft, " / ")
}

// shortAircraft trims verbose manufacturer prefixes so the Aircraft column
// stays narrow: "Airbus A350" -> "A350", "Boeing 737-800" -> "737-800".
func shortAircraft(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, prefix := range []string{"Airbus ", "Boeing ", "Embraer ", "Bombardier ", "Embraer-", "Airbus-"} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(s, prefix))
		}
	}
	return s
}

// parseFlightTime parses the assorted timestamp formats trvl's providers emit:
// RFC3339 with offset (skiplagged), and the offset-less "2006-01-02T15:04" /
// "...T15:04:05" forms (Google Flights / Kiwi). Returns ok=false if unparseable.
func parseFlightTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// formatLegDeparture renders a departure timestamp with its date, e.g.
// "Thu 28 May 19:25". Falls back to the raw string if it cannot be parsed.
func formatLegDeparture(raw string) string {
	t, ok := parseFlightTime(raw)
	if !ok {
		return raw
	}
	return t.Format("Mon 2 Jan 15:04")
}

// formatLegArrival renders an arrival time, appending a "+N" day marker when the
// arrival lands on a later calendar date than departure (overnight flights),
// e.g. "00:25 +1". Falls back to the raw arrival string if unparseable.
func formatLegArrival(depRaw, arrRaw string) string {
	arr, ok := parseFlightTime(arrRaw)
	if !ok {
		return arrRaw
	}
	out := arr.Format("15:04")
	if dep, depOK := parseFlightTime(depRaw); depOK {
		d := dayDelta(dep, arr)
		if d > 0 {
			out += fmt.Sprintf(" +%d", d)
		}
	}
	return out
}

// dayDelta returns the number of calendar days arrival is after departure,
// using each timestamp's own location so an overnight flight reads "+1".
func dayDelta(dep, arr time.Time) int {
	dy, dm, dd := dep.Date()
	ay, am, ad := arr.Date()
	depDay := time.Date(dy, dm, dd, 0, 0, 0, 0, time.UTC)
	arrDay := time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)
	return int(arrDay.Sub(depDay).Hours() / 24)
}

// formatPrice formats a price with currency.
func formatPrice(amount float64, currency string) string {
	if amount == 0 {
		return "-"
	}
	return fmt.Sprintf("%s %.0f", currency, amount)
}

// formatDuration converts minutes to a human-readable duration string.
func formatDuration(minutes int) string {
	if minutes == 0 {
		return "-"
	}
	h := minutes / 60
	m := minutes % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// formatAllIn renders the all-in cost cell for the flight table.
// Shows the total cost and a parenthetical explaining the delta, e.g.:
//
//	"EUR 124 (+€35 bag)" — baggage fee added
//	"EUR 89 (bags incl)" — no extra charge
//	"EUR 89 (FF bags)"   — FF status waived the fee
func formatAllIn(basePrice float64, currency string, allInCost float64, breakdown string) string {
	if allInCost <= 0 || breakdown == "" {
		return formatPrice(basePrice, currency)
	}
	label := breakdown
	if allInCost == basePrice {
		// No price difference — shorten the label.
		switch {
		case strings.Contains(breakdown, "FF"):
			label = "FF bags"
		case strings.Contains(breakdown, "included"):
			label = "bags incl"
		default:
			label = breakdown
		}
	}
	return fmt.Sprintf("%s %.0f (%s)", currency, allInCost, label)
}

// formatStops returns a human-readable stops string.
func formatStops(stops int) string {
	switch stops {
	case 0:
		return "Direct"
	case 1:
		return "1 stop"
	default:
		return fmt.Sprintf("%d stops", stops)
	}
}

// printMilesEarning shows a brief miles-earning summary for the cheapest
// flight, based on the user's frequent flyer programmes.
func printMilesEarning(prefs *preferences.Preferences, origin, destination string, cheapest models.FlightResult) {
	if len(prefs.FrequentFlyerPrograms) == 0 {
		return
	}

	airlineCode := ""
	if len(cheapest.Legs) > 0 {
		airlineCode = cheapest.Legs[0].AirlineCode
	}
	if airlineCode == "" {
		return
	}

	// Determine cabin class from the flight (default to economy).
	cabinClass := "economy"

	// Use EUR as price basis for revenue-based earning.
	priceEUR := cheapest.Price
	if cheapest.Currency != "EUR" {
		// Rough conversion — earning estimates are approximate anyway.
		priceEUR = cheapest.Price // treat as-is; user sees "estimate" caveat
	}

	fmt.Println()
	for _, ff := range prefs.FrequentFlyerPrograms {
		est := points.EstimateMilesEarned(origin, destination, cabinClass, airlineCode, ff.Alliance, priceEUR)
		if est.Miles <= 0 {
			continue
		}

		programLabel := ff.ProgramName
		if programLabel == "" {
			programLabel = est.Program
		}

		line := fmt.Sprintf("  \u2708 %s: ~%s miles earned (%s)", programLabel, formatMiles(est.Miles), airlineCode)

		if ff.MilesBalance > 0 {
			newBalance := ff.MilesBalance + est.Miles
			line += fmt.Sprintf(" | Balance: %s \u2192 %s", formatMiles(ff.MilesBalance), formatMiles(newBalance))
		}

		fmt.Println(line)
	}
}

// formatMiles formats a miles number with comma separators.
func formatMiles(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	// Insert commas from the right.
	var b strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		b.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// railFlyHubs lists origin hub airports where Rail+Fly arbitrage is possible
// (departing from a rail-connected nearby station/airport can be cheaper).
// Keyed by ORIGIN — the detector substitutes rail-reachable origins. The
// --rail-fly flag forces the check even for origins outside this allowlist.
var railFlyHubs = map[string]bool{
	"AMS": true, "FRA": true, "CDG": true, "ZRH": true,
}

// maybeShowFlightHackTips runs applicable hack detectors after a flight search
// and prints up to 3 compact tips sorted by savings (highest first).
func maybeShowFlightHackTips(ctx context.Context, origins, dests []string, departDate, returnDate string, passengers int, result *models.FlightSearchResult, railFly bool) {
	if result == nil || !result.Success || len(result.Flights) == 0 {
		return
	}

	// Use first origin/dest for detector input (primary route).
	origin := origins[0]
	dest := dests[0]

	// Determine cheapest price and currency for NaivePrice.
	cheapest := result.Flights[0]
	for _, f := range result.Flights[1:] {
		if f.Price > 0 && f.Price < cheapest.Price {
			cheapest = f
		}
	}

	currency := cheapest.Currency
	if currency == "" {
		currency = "EUR"
	}

	// Collect airline codes from results for fuel surcharge detection.
	airlineCodeSet := make(map[string]bool)
	for _, f := range result.Flights {
		for _, leg := range f.Legs {
			if leg.AirlineCode != "" {
				airlineCodeSet[leg.AirlineCode] = true
			}
		}
	}
	var airlineCodes []string
	for code := range airlineCodeSet {
		airlineCodes = append(airlineCodes, code)
	}

	// --- Zero-API-call detectors (synchronous) ---

	input := hacks.DetectorInput{
		Origin:      origin,
		Destination: dest,
		Date:        departDate,
		ReturnDate:  returnDate,
		Currency:    currency,
		NaivePrice:  cheapest.Price * float64(passengers),
		Passengers:  passengers,
	}

	allHacks := hacks.DetectFlightTips(ctx, input)

	// Fuel surcharge — if flight results contain airline codes.
	if len(airlineCodes) > 0 {
		allHacks = append(allHacks, hacks.DetectFuelSurcharge(origin, dest, airlineCodes)...)
	}

	// --- API-call detector: Rail+Fly (goroutine with 15s timeout) ---
	var mu sync.Mutex
	var wg sync.WaitGroup
	if railFly || railFlyHubs[origin] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rfCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if h := hacks.DetectRailFlyArbitrage(rfCtx, origin, dest, departDate, returnDate); len(h) > 0 {
				mu.Lock()
				allHacks = append(allHacks, h...)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(allHacks) == 0 {
		return
	}

	// Sort by savings descending, then by type for deterministic ordering.
	sort.Slice(allHacks, func(i, j int) bool {
		if allHacks[i].Savings != allHacks[j].Savings {
			return allHacks[i].Savings > allHacks[j].Savings
		}
		return allHacks[i].Type < allHacks[j].Type
	})

	// Cap at 3 tips.
	if len(allHacks) > 3 {
		allHacks = allHacks[:3]
	}

	fmt.Println()
	for _, h := range allHacks {
		label := hackTypeLabel(h.Type)
		tip := h.Title
		if h.Savings > 0 {
			tip = fmt.Sprintf("%s — saves %s %.0f", h.Title, h.Currency, h.Savings)
		}
		fmt.Printf("  💡 %s: %s\n", label, tip)
	}
}

// hackTypeLabel returns a short display label for a hack type.
func hackTypeLabel(t string) string {
	switch t {
	case "rail_fly_arbitrage":
		return "Rail+Fly"
	case "advance_purchase":
		return "Timing"
	case "fare_breakpoint":
		return "Routing"
	case "destination_airport":
		return "Destination"
	case "fuel_surcharge":
		return "Surcharge"
	case "group_split":
		return "Group"
	default:
		return strings.ReplaceAll(t, "_", " ")
	}
}

// flightDepartHHMM extracts the "HH:MM" clock time from the first leg's
// DepartureTime, which may be "2006-01-02T15:04" or similar ISO-ish formats.
func flightDepartHHMM(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return ""
	}
	dt := f.Legs[0].DepartureTime
	// ISO datetime: "2026-06-15T06:55" or "2026-06-15T06:55:00"
	if len(dt) >= len("2006-01-02T15:04") {
		clock := dt[len("2006-01-02T"):]
		if len(clock) > 5 {
			clock = clock[:5]
		}
		return clock
	}
	// Already HH:MM.
	if len(dt) == 5 && dt[2] == ':' {
		return dt
	}
	return ""
}

// flightAirlineCodes returns the unique IATA airline codes across all legs.
func flightAirlineCodes(f models.FlightResult) []string {
	seen := make(map[string]bool, len(f.Legs))
	codes := make([]string, 0, len(f.Legs))
	for _, leg := range f.Legs {
		if leg.AirlineCode != "" && !seen[leg.AirlineCode] {
			seen[leg.AirlineCode] = true
			codes = append(codes, leg.AirlineCode)
		}
	}
	return codes
}
