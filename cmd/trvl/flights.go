package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/baggage"
	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/tripsearch"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/points"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/spf13/cobra"
)

func flightsCmd() *cobra.Command {
	var (
		returnDate     string
		cabin          string
		maxStops       string
		sortBy         string
		airlines       []string
		adults         int
		format         string
		targetCurrency string
		compareCabins  bool
		provider       string
		award          bool
		awardCookies   string
		homeFan         bool
		railFly         bool
		minLayoverStr   string
		layoverAirports []string
		noEarlyConn     bool
		loungeRequired  bool
	)

	cmd := &cobra.Command{
		Use:   "flights ORIGIN DESTINATION DATE",
		Short: "Search flights between airports or cities (supports multi-airport)",
		Long: `Search flights between airports or cities on a specific date.

ORIGIN and DESTINATION are IATA codes or city names, comma-separated for multi-airport.
DATE is the departure date in YYYY-MM-DD format.

Examples:
  trvl flights HEL NRT 2026-06-15
  trvl flights AMS,EIN,ANR HEL,TKU,TLL 2026-06-15
  trvl flights Paris Tokyo 2026-06-15
  trvl flights "New York" London 2026-06-15
  trvl flights HEL NRT 2026-06-15 --return 2026-06-22
  trvl flights HEL NRT 2026-06-15 --cabin business --stops nonstop`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			originArg := args[0]

			// If the user passes "home" as origin, resolve from preferences.
			if strings.EqualFold(strings.TrimSpace(originArg), "home") {
				if prefs, err := preferences.Load(); err == nil && prefs.HomeAirport() != "" {
					originArg = prefs.HomeAirport()
				}
			}

			origins := flights.ParseFlightLocations(originArg)
			destinations := flights.ParseFlightLocations(args[1])
			date := args[2]

			// --home-fan: delegate to internal/hunt so CLI and MCP share the
			// single implementation of the home-airport fan-out rule.
			if homeFan {
				if prefs, err := preferences.Load(); err == nil {
					if expanded, eerr := tripsearch.ExpandOrigins(strings.Join(origins, ","), prefs); eerr == nil {
						origins = expanded
					}
				}
			}

			// --rail-fly: delegate to internal/hunt so rail+fly origin logic
			// (ZYR/ANR/BRU when AMS present) lives in one place.
			if railFly {
				origins = tripsearch.AddRailFlyOrigins(origins)
			}

			// --award: Flying Blue miles price scanner across a date or month range.
			if award {
				return runAwardScan(cmd.Context(), origins[0], destinations[0], date, awardCookies, format)
			}

			cabinClass, err := models.ParseCabinClass(cabin)
			if err != nil {
				return fmt.Errorf("invalid cabin class: %w", err)
			}

			stops, err := models.ParseMaxStops(maxStops)
			if err != nil {
				return fmt.Errorf("invalid max stops: %w", err)
			}

			sort, err := models.ParseSortBy(sortBy)
			if err != nil {
				return fmt.Errorf("invalid sort order: %w", err)
			}

			opts := flights.SearchOptions{
				ReturnDate: returnDate,
				CabinClass: cabinClass,
				MaxStops:   stops,
				SortBy:     sort,
				Airlines:   airlines,
				Adults:     adults,
				Currency:   targetCurrency,
			}

			var result *models.FlightSearchResult

			// --provider afklm: route to AF-KLM Offers API provider.
			if strings.EqualFold(provider, "afklm") {
				p, provErr := afklm.NewProvider()
				if provErr == afklm.ErrNoCredential {
					fmt.Println("AF-KLM provider not enabled. Sign up for a free personal API key at https://developer.airfranceklm.com then store it via: security add-generic-password -a $USER -s afklm-api-key -w <your_key>")
					return nil
				}
				if provErr != nil {
					return fmt.Errorf("afklm provider: %w", provErr)
				}
				mopts := models.FlightSearchOptions{
					ReturnDate: opts.ReturnDate,
					CabinClass: opts.CabinClass,
					MaxStops:   opts.MaxStops,
					SortBy:     opts.SortBy,
					Airlines:   opts.Airlines,
					Adults:     opts.Adults,
					Currency:   opts.Currency,
				}
				// Fan out across all origins (rail-fly adds ZYR/ANR/BRU) and all destinations.
				// Merge results into a single FlightSearchResult ranked by price.
				result = &models.FlightSearchResult{Success: true, TripType: "one_way"}
				if opts.ReturnDate != "" {
					result.TripType = "round_trip"
				}
				for _, o := range origins {
					for _, d := range destinations {
						r, rerr := p.SearchFlights(cmd.Context(), o, d, date, mopts)
						if rerr != nil || r == nil || !r.Success {
							continue
						}
						result.Flights = append(result.Flights, r.Flights...)
					}
				}
				result.Count = len(result.Flights)
				if result.Count == 0 {
					result.Success = false
					result.Error = "no flights returned from afklm for any origin/destination combination"
				}
				if format == "json" {
					return models.FormatJSON(os.Stdout, result)
				}
				return printFlightsTable(cmd.Context(), strings.Join(origins, ","), strings.Join(destinations, ","), targetCurrency, result)
			}

			// --compare-cabins: search all cabin classes in parallel.
			if compareCabins {
				return runCabinComparison(cmd.Context(), origins, destinations, date, opts, format)
			}

			if len(origins) > 1 || len(destinations) > 1 {
				result, err = flights.SearchMultiAirport(cmd.Context(), origins, destinations, date, opts)
			} else {
				result, err = flights.SearchFlights(cmd.Context(), origins[0], destinations[0], date, opts)
			}
			if err != nil {
				return err
			}

			// Apply Mikko-mental-model post-search filters via internal/hunt so
			// CLI and MCP share the same filter stack. Pre-validate duration
			// string here to preserve the specific --min-layover error message.
			if result != nil && result.Success && len(result.Flights) > 0 {
				mins := 0
				if minLayoverStr != "" {
					d, perr := time.ParseDuration(minLayoverStr)
					if perr != nil {
						return fmt.Errorf("invalid --min-layover %q: %w", minLayoverStr, perr)
					}
					mins = int(d.Minutes())
				}
				prefs, _ := preferences.Load()
				req := tripsearch.Request{
					MinLayoverMinutes: mins,
					LayoverAirports:   layoverAirports,
					LoungeRequired:    loungeRequired,
					NoEarlyConnection: noEarlyConn,
				}
				flts, _ := tripsearch.ApplyFilters(result.Flights, req, prefs)
				result.Flights = flts
				result.Count = len(flts)
			}

			// Cache best result for `trvl share --last`.
			if result != nil && result.Success && len(result.Flights) > 0 {
				f := result.Flights[0]
				airline := ""
				if len(f.Legs) > 0 {
					airline = f.Legs[0].Airline
				}
				if airline == "" {
					airline = flightProviderLabel(f)
				}
				saveLastSearch(&LastSearch{
					Command:        "flights",
					Origin:         strings.Join(origins, ","),
					Destination:    strings.Join(destinations, ","),
					DepartDate:     date,
					FlightPrice:    f.Price,
					FlightCurrency: f.Currency,
					FlightAirline:  airline,
					FlightStops:    f.Stops,
				})
			}

			if format == "json" {
				return models.FormatJSON(os.Stdout, result)
			}

			if err := printFlightsTable(cmd.Context(), strings.Join(origins, ","), strings.Join(destinations, ","), targetCurrency, result); err != nil {
				return err
			}

			// Auto-trigger: run applicable hack detectors and print tips
			// below the flight results.
			maybeShowFlightHackTips(cmd.Context(), origins, destinations, date, returnDate, adults, result)

			if openFlag && result.Success && len(result.Flights) > 0 && result.Flights[0].BookingURL != "" {
				_ = openBrowser(result.Flights[0].BookingURL)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&returnDate, "return", "", "Return date for round-trip (YYYY-MM-DD)")
	cmd.Flags().StringVar(&cabin, "cabin", "economy", "Cabin class: economy, premium_economy, business, first")
	cmd.Flags().StringVar(&maxStops, "stops", "any", "Max stops: any, nonstop, one_stop, two_plus")
	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort by: cheapest, duration, departure, arrival")
	cmd.Flags().StringSliceVar(&airlines, "airline", nil, "Filter by airline IATA code (repeatable)")
	cmd.Flags().IntVar(&adults, "adults", 1, "Number of adult passengers")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table, json")
	cmd.Flags().StringVar(&targetCurrency, "currency", "", "Convert prices to this currency (e.g. EUR, USD). Empty = show API default")
	cmd.Flags().BoolVar(&compareCabins, "compare-cabins", false, "Compare prices across all cabin classes (economy, premium, business, first)")
	cmd.Flags().StringVar(&provider, "provider", "", "Flight data provider (e.g. afklm). Default: Google Flights")
	cmd.Flags().BoolVar(&homeFan, "home-fan", false, "Expand origin to all home + nearby airports from preferences (e.g. AMS+EIN, HEL+TKU+TMP+TLL+ARN)")
	cmd.Flags().BoolVar(&railFly, "rail-fly", false, "KL/AF rail+fly: also search ZYR (Brussels-Midi station), ANR, BRU as origins via AFKL provider. Requires origin to include AMS.")
	cmd.Flags().StringVar(&minLayoverStr, "min-layover", "", "Only show flights with at least this layover duration (e.g. 12h, 90m)")
	cmd.Flags().StringSliceVar(&layoverAirports, "layover-at", nil, "Restrict qualifying layovers to these airports (IATA codes, comma list)")
	cmd.Flags().BoolVar(&noEarlyConn, "no-early-connection", false, "After overnight layover (≥8h), require next departure at or after preferences.early_connection_floor (default 10:00) — the 'unhurried wake + breakfast' rule")
	cmd.Flags().BoolVar(&noEarlyConn, "no-aamuyo", false, "Deprecated alias for --no-early-connection")
	_ = cmd.Flags().MarkHidden("no-aamuyo")
	cmd.Flags().BoolVar(&loungeRequired, "lounge-required", false, "Drop flights where any layover airport lacks lounge coverage for your cards")
	cmd.Flags().BoolVar(&award, "award", false, "Scan Flying Blue miles prices. DATE is a month (2026-06) or a day (2026-06-15). Requires KLM session cookies via AFKL_KLM_COOKIES or --award-cookies.")
	cmd.Flags().StringVar(&awardCookies, "award-cookies", "", "Raw KLM session Cookie header for award search (alternative to AFKL_KLM_COOKIES env var)")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

// printFlightsTable renders flight results as an ASCII table.
// If targetCurrency is set and differs from API currency, converts prices.
func printFlightsTable(ctx context.Context, origin, destination, targetCurrency string, result *models.FlightSearchResult) error {
	if !result.Success {
		fmt.Fprintf(os.Stderr, "Search failed: %s\n", result.Error)
		return nil
	}

	if result.Count == 0 {
		fmt.Println("No flights found.")
		return nil
	}

	// Check for matching deals from RSS feeds (cached, non-blocking).
	bannerLines := []string{fmt.Sprintf("Found %d flights", result.Count)}
	matchedDeals := deals.MatchDeals(ctx, origin, destination)
	for _, d := range matchedDeals {
		dealLine := fmt.Sprintf("🔥 %s: %s", deals.SourceNames[d.Source], d.Title)
		if len(dealLine) > 70 {
			dealLine = dealLine[:67] + "..."
		}
		bannerLines = append(bannerLines, dealLine)
	}

	models.Banner(os.Stdout, "✈️", fmt.Sprintf("Flights · %s", result.TripType), bannerLines...)
	fmt.Println()

	// Convert prices if --currency specified and differs from API currency.
	if targetCurrency != "" && len(result.Flights) > 0 && result.Flights[0].Currency != targetCurrency {
		for i := range result.Flights {
			if result.Flights[i].Price > 0 && result.Flights[i].Currency != targetCurrency {
				converted, cur := destinations.ConvertCurrency(ctx, result.Flights[i].Price, result.Flights[i].Currency, targetCurrency)
				result.Flights[i].Price = math.Round(converted)
				result.Flights[i].Currency = cur
			}
		}
	}

	showProvider := false
	showNotes := false
	for _, f := range result.Flights {
		if f.Provider != "" && !strings.EqualFold(f.Provider, "google_flights") {
			showProvider = true
		}
		if flightWarnings(f) != "" {
			showNotes = true
		}
	}

	// Compute all-in costs (base fare + baggage fees - FF benefits).
	// Only shown when at least one flight's all-in cost differs from base.
	type allInInfo struct {
		cost      float64
		breakdown string
	}
	allInData := make([]allInInfo, len(result.Flights))
	showAllIn := false
	prefs, _ := preferences.Load() //nolint:errcheck // default prefs on error
	if prefs != nil { // all-in is self-gating: column only appears when allIn != basePrice for any flight
		needCheckedBag := !prefs.CarryOnOnly
		needCarryOn := true
		var ffStatuses []baggage.FFStatus
		for _, fp := range prefs.FrequentFlyerPrograms {
			ffStatuses = append(ffStatuses, baggage.FFStatus{
				Alliance: fp.Alliance,
				Tier:     fp.Tier,
			})
		}
		for i, f := range result.Flights {
			airlineCode := ""
			if len(f.Legs) > 0 {
				airlineCode = f.Legs[0].AirlineCode
			}
			if airlineCode == "" {
				continue
			}
			allIn, breakdown := baggage.AllInCost(f.Price, airlineCode, needCheckedBag, needCarryOn, ffStatuses)
			allInData[i] = allInInfo{cost: allIn, breakdown: breakdown}
			if allIn != f.Price {
				showAllIn = true
			}
		}
	}

	headers := []string{"Price"}
	if showAllIn {
		headers = append(headers, "All-in")
	}
	headers = append(headers, "Duration", "Stops", "Route")
	if showProvider {
		headers = append(headers, "Provider")
	}
	headers = append(headers, "Airline", "Flight", "Departs", "Arrives")
	if showNotes {
		headers = append(headers, "Notes")
	}
	var rows [][]string
	var prices priceScale

	for _, f := range result.Flights {
		prices = prices.With(f.Price)
	}

	for i, f := range result.Flights {
		route := flightRoute(f)
		airline := ""
		flightNum := ""
		departs := ""
		arrives := ""

		if len(f.Legs) > 0 {
			airline = f.Legs[0].Airline
			flightNum = f.Legs[0].FlightNumber
			departs = f.Legs[0].DepartureTime
			arrives = f.Legs[len(f.Legs)-1].ArrivalTime
		}

		row := []string{
			prices.Apply(f.Price, formatPrice(f.Price, f.Currency)),
		}
		if showAllIn {
			row = append(row, formatAllIn(f.Price, f.Currency, allInData[i].cost, allInData[i].breakdown))
		}
		row = append(row,
			formatDuration(f.Duration),
			colorizeStops(f.Stops),
			route,
		)
		if showProvider {
			row = append(row, flightProviderLabel(f))
		}
		row = append(row, airline, flightNum, departs, arrives)
		if showNotes {
			row = append(row, flightWarnings(f))
		}
		rows = append(rows, row)
	}

	models.FormatTable(os.Stdout, headers, rows)

	// Summary: cheapest flight
	if len(result.Flights) > 0 {
		cheapest := result.Flights[0]
		for _, f := range result.Flights[1:] {
			if f.Price > 0 && f.Price < cheapest.Price {
				cheapest = f
			}
		}
		airline := ""
		if len(cheapest.Legs) > 0 {
			airline = cheapest.Legs[0].Airline
		}
		descriptorParts := []string{}
		if provider := flightProviderLabel(cheapest); provider != "" && (!strings.EqualFold(cheapest.Provider, "google_flights") || airline == "") {
			descriptorParts = append(descriptorParts, provider)
		}
		if airline != "" {
			descriptorParts = append(descriptorParts, airline)
		}
		if cheapest.SelfConnect {
			descriptorParts = append(descriptorParts, "self-connect")
		}
		descriptor := strings.Join(descriptorParts, ", ")
		if descriptor == "" {
			descriptor = "-"
		}
		models.Summary(os.Stdout, fmt.Sprintf("Cheapest: %s %.0f (%s, %s)",
			cheapest.Currency, cheapest.Price, descriptor, formatStops(cheapest.Stops)))
		models.BookingHint(os.Stdout)

		// Miles earning estimate for users with FF programmes.
		if prefs != nil {
			printMilesEarning(prefs, origin, destination, cheapest)
		}
	}
	return nil
}

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

func flightWarnings(f models.FlightResult) string {
	if len(f.Warnings) > 0 {
		return strings.Join(f.Warnings, "; ")
	}
	if f.SelfConnect {
		return "Self-connect: protect your own connection"
	}
	return ""
}

// flightRoute builds a route string like "HEL -> FRA -> NRT".
func flightRoute(f models.FlightResult) string {
	if len(f.Legs) == 0 {
		return ""
	}

	parts := []string{f.Legs[0].DepartureAirport.Code}
	for _, leg := range f.Legs {
		parts = append(parts, leg.ArrivalAirport.Code)
	}
	return strings.Join(parts, " -> ")
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

// railFlyHubs lists destination airports where Rail+Fly arbitrage is possible.
var railFlyHubs = map[string]bool{
	"AMS": true, "FRA": true, "CDG": true, "ZRH": true,
}

// maybeShowFlightHackTips runs applicable hack detectors after a flight search
// and prints up to 3 compact tips sorted by savings (highest first).
func maybeShowFlightHackTips(ctx context.Context, origins, dests []string, departDate, returnDate string, passengers int, result *models.FlightSearchResult) {
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
	if railFlyHubs[dest] {
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
