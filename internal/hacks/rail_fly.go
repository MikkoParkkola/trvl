package hacks

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// railFlyStation describes a railway station that an airline sells as a flight origin.
type railFlyStation struct {
	IATA          string // Rail station IATA code (e.g. "ZWE" for Antwerp)
	City          string // City name (e.g. "Antwerp")
	HubIATA       string // Airline hub airport (e.g. "AMS")
	Airline       string // IATA carrier code (e.g. "KL")
	AirlineName   string // Display name (e.g. "KLM")
	TrainProvider string // Train operator (e.g. "Eurostar")
	TrainMinutes  int    // Approximate train journey time
	FareZone      string // Why it's cheaper: "Belgian market", "German regional", etc.
}

var railFlyStations = []railFlyStation{
	// KLM Air&Rail — trains to Amsterdam Schiphol
	{IATA: "ZWE", City: "Antwerp", HubIATA: "AMS", Airline: "KL", AirlineName: "KLM", TrainProvider: "Eurostar", TrainMinutes: 60, FareZone: "Belgian market"},
	{IATA: "ZYR", City: "Brussels-Midi", HubIATA: "AMS", Airline: "KL", AirlineName: "KLM", TrainProvider: "Eurostar", TrainMinutes: 105, FareZone: "Belgian market"},

	// Lufthansa AIRail — ICE trains to Frankfurt Airport
	{IATA: "QKL", City: "Cologne", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 62, FareZone: "Rhineland regional"},
	{IATA: "ZWS", City: "Stuttgart", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 78, FareZone: "Baden-Württemberg regional"},
	{IATA: "QDU", City: "Düsseldorf Hbf", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 82, FareZone: "NRW regional"},
	{IATA: "QMZ", City: "Mannheim", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 38, FareZone: "Rhein-Neckar regional"},
	{IATA: "QBO", City: "Bonn", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 55, FareZone: "Rhineland regional"},
	{IATA: "ZAQ", City: "Nuremberg", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 127, FareZone: "Bavaria regional"},
	{IATA: "QPP", City: "Kassel-Wilhelmshöhe", HubIATA: "FRA", Airline: "LH", AirlineName: "Lufthansa", TrainProvider: "DB ICE", TrainMinutes: 90, FareZone: "Hesse regional"},

	// Air France TGV Air — TGV trains to Paris CDG
	{IATA: "ZYR", City: "Brussels-Midi", HubIATA: "CDG", Airline: "AF", AirlineName: "Air France", TrainProvider: "Thalys/TGV", TrainMinutes: 80, FareZone: "Belgian market"},

	// Swiss Rail+Air — trains to Zurich Airport
	{IATA: "ZDH", City: "Basel", HubIATA: "ZRH", Airline: "LX", AirlineName: "Swiss", TrainProvider: "SBB", TrainMinutes: 80, FareZone: "Basel border zone"},
}

// railFlyFlightSearcher is the function used to search flight prices. It is a
// package-level seam so detector tests can cover the full path without live
// network calls.
var railFlyFlightSearcher = flights.SearchFlightsWithClient

// DetectRailFlyArbitrage checks if booking via a rail-connected origin
// (e.g., Antwerp instead of Amsterdam for KLM) triggers a cheaper fare zone.
// For a hub origin the Air&Rail train is bundled free in the ticket; for an
// alias origin (e.g. ANR/BRU) the rail leg is a real cost subtracted from the
// reported net saving.
func DetectRailFlyArbitrage(ctx context.Context, origin, destination, departDate, returnDate string) []Hack {
	if origin == "" || destination == "" || departDate == "" {
		return nil
	}
	origin = strings.ToUpper(origin)
	destination = strings.ToUpper(destination)

	// Find rail stations that connect to this origin airport as a hub
	stations := railFlyStationsForHub(origin)
	if len(stations) == 0 {
		return nil
	}

	// Search baseline price from the actual airport
	client := batchexec.NewClient()

	baseOpts := flights.SearchOptions{
		SortBy: models.SortCheapest,
	}
	if returnDate != "" {
		baseOpts.ReturnDate = returnDate
	}

	baseResult, baseErr := railFlyFlightSearcher(ctx, client, origin, destination, departDate, baseOpts)
	basePrice, baseCurrency, _, _ := cheapestFlightInfo(baseResult, baseErr)
	if basePrice <= 0 {
		return nil
	}

	// Search from each rail-connected origin in parallel
	type railResult struct {
		station  railFlyStation
		price    float64
		currency string
		flight   *models.FlightResult // cheapest concrete itinerary from this rail station search (for candidate injection)
	}

	results := make(chan railResult, len(stations))
	var wg sync.WaitGroup

	for _, st := range stations {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := flights.SearchOptions{
				SortBy: models.SortCheapest,
			}
			if returnDate != "" {
				opts.ReturnDate = returnDate
			}
			res, err := railFlyFlightSearcher(ctx, client, st.IATA, destination, departDate, opts)
			p, c, _, fl := cheapestFlightInfo(res, err)
			results <- railResult{station: st, price: p, currency: c, flight: fl}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Find the cheapest rail alternative
	var bestStation *railFlyStation
	bestPrice := basePrice
	bestCurrency := baseCurrency
	var bestFlight *models.FlightResult

	for r := range results {
		if r.price > 0 && r.price < bestPrice {
			bestPrice = r.price
			bestCurrency = r.currency
			st := r.station
			bestStation = &st
			bestFlight = r.flight
		}
	}

	if bestStation == nil {
		return nil
	}

	grossSavings := basePrice - bestPrice

	// Price the rail leg explicitly BEFORE thresholding so reported savings are
	// honest. For a hub-origin search the Air&Rail train is bundled into the
	// ticket (cost 0); for an alias origin (e.g. ANR/BRU) the substitute origin
	// is a different airport, so the rail leg is a real out-of-pocket cost that
	// must be subtracted from net savings. A ground-provider error degrades
	// gracefully to a conservative estimate; it never aborts the search.
	railLeg := resolveRailLegCost(ctx, origin, *bestStation, departDate)

	// Net savings subtract any non-bundled rail cost (railLeg.Cost is 0 when the
	// train is bundled in the airline ticket).
	netSavings := grossSavings - railLeg.Cost

	// Only report if net savings exceed 5% AND at least 15 absolute.
	if netSavings < 15 || netSavings/basePrice < 0.05 {
		return nil
	}

	hack := buildRailFlyHack(origin, destination, basePrice, baseCurrency, bestPrice, bestCurrency, netSavings, *bestStation, returnDate)
	attachRailLegCost(&hack, railLeg)

	// Attach the single-total rail+fly bundle (rail leg + flight leg + return)
	// so the hack carries an auditable, one-number itinerary with per-leg timing
	// and the connection-guarantee status.
	bundle := composeRailFlyBundle(ctx, origin, destination, departDate, returnDate, *bestStation, bestPrice, basePrice, bestCurrency)
	hack.Bundle = bundle.Bundle

	// Capture the real flight result from the rail-station search as a
	// bookable candidate. Annotate with [rail+fly] + tradeoff note. This is
	// the only path that injects real (non-fabricated) itineraries for
	// rail+fly. The policy layer will promote when not suppressed.
	if bestFlight != nil {
		cand := *bestFlight // copy value
		cand.Warnings = append([]string(nil), cand.Warnings...)
		cand.Warnings = append(cand.Warnings, "[rail+fly] throwaway train leg; board/exit at the hub airport")
		hack.ConcreteCandidates = append(hack.ConcreteCandidates, cand)
	}

	out := []Hack{hack}

	// Round-trip searches can also exit by rail from the destination: surface an
	// open-jaw rail-return variant (fly in, train out) when it still nets a
	// saving over the direct round-trip fare.
	if returnDate != "" {
		oj := composeOpenJawRailReturn(ctx, origin, destination, destination, bestPrice, basePrice, bestCurrency, departDate, returnDate)
		if oj.Savings > 0 {
			out = append(out, oj)
		}
	}
	return out
}

// detectRailFlyArbitrage adapts DetectRailFlyArbitrage to the detectFn signature
// used by DetectAll.
func detectRailFlyArbitrage(ctx context.Context, in DetectorInput) []Hack {
	return DetectRailFlyArbitrage(ctx, in.Origin, in.Destination, in.Date, in.ReturnDate)
}

// railFlyOriginAlias maps a real departure airport (the IATA the traveller
// actually searches from) to the rail-station IATAs that offer a rail-fly
// substitute origin nearby. When a user searches FROM one of these airports,
// the detector surfaces the rail-connected stations the same way it does when
// the origin is the airline hub itself.
//
// The departure airport is carried as the typed IATA field (not a bare map key)
// so the recognised virtual origins are auditable from a single struct table —
// ANR (Antwerp Airport) and BRU (Brussels Airport) are first-class entries.
type railFlyOriginAlias struct {
	// IATA is the departure airport the user searches from (e.g. "ANR", "BRU").
	IATA string
	// Stations are the rail-station IATAs this airport surfaces as virtual
	// origins (e.g. "ZWE" for Antwerp, "ZYR" for Brussels-Midi).
	Stations []string
}

// railFlyOriginAliases is the table of airport-style virtual origins.
//
// Example: a search from ANR (Antwerp Airport) surfaces ZWE (Antwerp rail
// station, KLM Air&Rail to AMS); a search from BRU (Brussels Airport) surfaces
// ZYR (Brussels-Midi) which has both a KLM (AMS) and an Air France (CDG) bundle.
var railFlyOriginAliases = []railFlyOriginAlias{
	{IATA: "ANR", Stations: []string{"ZWE"}}, // Antwerp Airport -> Antwerp rail station (KLM -> AMS)
	{IATA: "BRU", Stations: []string{"ZYR"}}, // Brussels Airport -> Brussels-Midi (KLM -> AMS, Air France -> CDG)
}

// aliasStationsFor returns the rail-station IATAs an airport-style virtual
// origin surfaces, or nil when the origin is not a recognised alias.
func aliasStationsFor(origin string) []string {
	for _, a := range railFlyOriginAliases {
		if a.IATA == origin {
			return a.Stations
		}
	}
	return nil
}

func railFlyStationsForHub(origin string) []railFlyStation {
	var result []railFlyStation

	// Primary match: origin is itself an airline hub (e.g. AMS, FRA, CDG, ZRH).
	for _, st := range railFlyStations {
		if st.HubIATA == origin {
			result = append(result, st)
		}
	}

	// Alias match: origin is a city airport (e.g. ANR, BRU) with a nearby
	// rail-fly station that routes through a different hub. Deduplicated against
	// the primary match by (station IATA, hub IATA).
	if aliasIATAs := aliasStationsFor(origin); len(aliasIATAs) > 0 {
		seen := map[string]bool{}
		for _, st := range result {
			seen[st.IATA+"|"+st.HubIATA] = true
		}
		for _, wantIATA := range aliasIATAs {
			for _, st := range railFlyStations {
				if st.IATA != wantIATA {
					continue
				}
				key := st.IATA + "|" + st.HubIATA
				if seen[key] {
					continue
				}
				seen[key] = true
				result = append(result, st)
			}
		}
	}

	return result
}

func buildRailFlyHack(origin, destination string, basePrice float64, baseCurrency string, railPrice float64, railCurrency string, savings float64, station railFlyStation, returnDate string) Hack {
	tripType := "one-way"
	if returnDate != "" {
		tripType = "round-trip"
	}

	// bundled is true when the search origin IS the airline hub, so the Air&Rail
	// train is included in the ticket. For an alias origin (e.g. ANR/BRU) the
	// substitute origin flies via a different hub, so the train is a real,
	// separately-paid leg and the hack text must say so honestly.
	bundled := origin == station.HubIATA

	// KLM Air&Rail has no enforcement — train boarding is not linked to
	// flight check-in at Schiphol.  Lufthansa AIRail may enforce outbound.
	isKLM := station.Airline == "KL"

	trainStep := fmt.Sprintf("Take %s from %s to %s (%d min, included in ticket)", station.TrainProvider, station.City, origin, station.TrainMinutes)
	if !bundled {
		trainStep = fmt.Sprintf("Take %s from %s to %s airport (%d min, booked as a separate rail ticket)", station.TrainProvider, station.City, station.HubIATA, station.TrainMinutes)
	}

	steps := []string{
		fmt.Sprintf("Direct from %s: %.0f %s (%s)", origin, basePrice, baseCurrency, tripType),
		fmt.Sprintf("Via %s (%s): %.0f %s (%s) — %.0f %s cheaper", station.City, station.IATA, railPrice, railCurrency, tripType, savings, baseCurrency),
		trainStep,
		"Train ticket appears as a flight segment in the booking — board with your airline booking reference",
		"On return: you can skip the train leg back to " + station.City + " — nobody checks if you board the return train",
	}

	var risks []string
	if isKLM {
		steps = append(steps, "KLM Air&Rail: train segments can be skipped in practice — go directly to/from Schiphol")
		steps = append(steps, "The fare zone savings apply regardless of whether you use the train")
		risks = []string{
			"LOW risk (KLM): Skipping is against T&C but no enforcement mechanism exists",
			"RETURN: Safe to skip the train from " + origin + " to " + station.City + " (last leg, no enforcement)",
			fmt.Sprintf("Allow %d+ minutes for the train journey plus airport transfer if you choose to ride", station.TrainMinutes+30),
		}
	} else {
		risks = []string{
			"OUTBOUND: You MUST board the train to " + origin + " — skipping cancels the entire booking",
			"MEDIUM risk (Lufthansa AIRail): outbound train boarding may be enforced — reports vary",
			"RETURN: Safe to skip the train from " + origin + " to " + station.City + " (last leg, no enforcement)",
			fmt.Sprintf("Allow %d+ minutes for the train journey plus airport transfer", station.TrainMinutes+30),
			"Train is flexible within the travel day (any departure, not fixed to one schedule)",
		}
	}
	if !bundled {
		risks = append(risks, fmt.Sprintf("Rail leg %s→%s is a separate ticket — its cost is already subtracted from the reported net saving", station.City, station.HubIATA))
	}

	title := fmt.Sprintf("Book via %s — train to %s is free, saves %.0f %s", station.City, station.HubIATA, savings, baseCurrency)
	description := fmt.Sprintf(
		"Book %s %s→%s instead of %s→%s. The %s train from %s to %s airport (%d min) is included free in the ticket. "+
			"Different fare zone (%s) triggers cheaper market pricing — airlines price by origin country/region. "+
			"Can be combined with hidden-city ticketing (book via rail station to hub, exit at hub, skip onward flight) for maximum savings.",
		station.AirlineName, station.IATA, destination, origin, destination,
		station.TrainProvider, station.City, origin, station.TrainMinutes, station.FareZone)
	if !bundled {
		title = fmt.Sprintf("Book via %s — net saving %.0f %s after the rail leg", station.City, savings, baseCurrency)
		description = fmt.Sprintf(
			"Book %s %s→%s instead of %s→%s. Take the %s train from %s to %s airport (%d min) as a separate rail ticket; "+
				"its cost is subtracted from the reported net saving. A different fare zone (%s) triggers cheaper market pricing — "+
				"airlines price by origin country/region.",
			station.AirlineName, station.IATA, destination, origin, destination,
			station.TrainProvider, station.City, station.HubIATA, station.TrainMinutes, station.FareZone)
	}

	return Hack{
		Type:        "rail_fly_arbitrage",
		Title:       title,
		Description: description,
		Savings:     roundSavings(savings),
		Currency:    baseCurrency,
		Steps:       steps,
		Risks:       risks,
		Citations: []string{
			fmt.Sprintf("https://www.google.com/travel/flights?q=%s%%20to%%20%s", station.IATA, destination),
			fmt.Sprintf("https://www.google.com/travel/flights?q=%s%%20to%%20%s", origin, destination),
		},
	}
}
