package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/MikkoParkkola/trvl/internal/baggage"
	"github.com/MikkoParkkola/trvl/internal/cfprobe"
	"github.com/MikkoParkkola/trvl/internal/counterfactual"
	"github.com/MikkoParkkola/trvl/internal/deals"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hacks"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/pricefeed"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
	"github.com/MikkoParkkola/trvl/internal/scoring"
	"github.com/MikkoParkkola/trvl/internal/travelctx"
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
		explain        bool
		award          bool
		noGeo          bool
		awardCookies   string
		provider       string
		flightRailFly  bool
		deep           bool
		stealth        bool
	)

	cmd := &cobra.Command{
		Use:   "flights [ORIGIN] DESTINATION DATE",
		Short: "Search flights between airports (supports multi-airport)",
		Long: `Search flights between airports on a specific date.

ORIGIN and DESTINATION are IATA codes, comma-separated for multi-airport.
DATE is the departure date in YYYY-MM-DD format.

Examples:
  trvl flights HEL NRT 2026-06-15
  trvl flights AMS,EIN,ANR HEL,TKU,TLL 2026-06-15
  trvl flights HEL NRT 2026-06-15 --return 2026-06-22
  trvl flights HEL NRT 2026-06-15 --cabin business --stops nonstop`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Accept both "ORIGIN DEST DATE" (explicit) and "DEST DATE"
			// (origin auto-resolved from location/time context).
			var originArg, destArg, date string
			switch len(args) {
			case 3:
				originArg, destArg, date = args[0], args[1], args[2]
			default: // 2 args
				destArg, date = args[0], args[1]
			}

			// Resolve the ambient search context (current time + best-available
			// origin). Precedence: explicit ORIGIN > "home" keyword / saved home
			// airport > geo-IP (best-effort, opt-out via --no-geo / TRVL_NO_GEO).
			prefs, _ := preferences.Load() //nolint:errcheck // default prefs on error
			explicitForCtx := originArg
			if strings.EqualFold(strings.TrimSpace(originArg), "home") {
				explicitForCtx = "" // fall through to prefs/home resolution
			}
			tctx := travelctx.Resolve(cmd.Context(), prefs, travelctx.Options{
				ExplicitOrigin: explicitForCtx,
				AllowGeoIP:     !noGeo,
			})

			// Auto-fill the origin when the user didn't pass an explicit code
			// (2-arg form or the "home" keyword).
			autoResolved := false
			if explicitForCtx == "" && tctx.Origin.HasAirport() {
				originArg = tctx.Origin.Airport
				autoResolved = true
			}
			if strings.TrimSpace(originArg) == "" {
				return fmt.Errorf("no origin given and none could be resolved from your preferences or location; pass ORIGIN explicitly, e.g. `trvl flights HEL %s %s`", destArg, date)
			}
			if autoResolved && format != "json" {
				origName := tctx.Origin.City
				if origName == "" {
					origName = tctx.Origin.Airport
				}
				switch tctx.Origin.Source {
				case travelctx.SourcePrefs:
					_, _ = fmt.Fprintf(os.Stderr, "Origin %s (%s) — from your saved home airport.\n", tctx.Origin.Airport, origName)
				case travelctx.SourceGeoIP:
					_, _ = fmt.Fprintf(os.Stderr, "Origin %s (%s) — detected from your current location. Override with an explicit code or --no-geo.\n", tctx.Origin.Airport, origName)
				}
			}

			origins := flights.ParseAirports(originArg)
			destinations := flights.ParseAirports(destArg)

			// Surface the booking window: lead time is one of the strongest fare
			// levers, and trvl knows "now", so it can flag last-minute / too-early
			// searches without being asked.
			if format != "json" {
				if dep, derr := time.Parse("2006-01-02", date); derr == nil {
					if adv := travelctx.ClassifyWindow(tctx.LeadTimeDays(dep)).Advisory(); adv != "" {
						_, _ = fmt.Fprintf(os.Stderr, "%s\n", adv)
					}
				}
			}

			// Validate IATA codes up-front so invalid input fails fast with a
			// deterministic error, not via downstream provider HTTP calls. This
			// matches the pattern in when/grid/multicity/discover/weekend/explore
			// and keeps the default test suite network-free for negative paths.
			if len(origins) == 0 {
				return fmt.Errorf("invalid origin: %q: at least one IATA code required", originArg)
			}
			for _, code := range origins {
				if err := models.ValidateIATA(code); err != nil {
					return fmt.Errorf("invalid origin: %w", err)
				}
			}
			if len(destinations) == 0 {
				return fmt.Errorf("invalid destination: %q: at least one IATA code required", destArg)
			}
			for _, code := range destinations {
				if err := models.ValidateIATA(code); err != nil {
					return fmt.Errorf("invalid destination: %w", err)
				}
			}

			if award {
				if len(origins) != 1 || len(destinations) != 1 {
					return fmt.Errorf("--award supports exactly one origin and one destination")
				}
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
				Stealth:    stealth,
			}

			// --compare-cabins: search all cabin classes in parallel.
			if compareCabins {
				return runCabinComparison(cmd.Context(), origins, destinations, date, opts, format)
			}

			var result *models.FlightSearchResult
			switch strings.ToLower(strings.TrimSpace(provider)) {
			case "skiplagged":
				if len(origins) != 1 || len(destinations) != 1 {
					return fmt.Errorf("--provider skiplagged supports exactly one origin and one destination")
				}
				result, err = flights.SearchSkiplagged(cmd.Context(), origins[0], destinations[0], date, opts)
			case "afklm", "af-klm", "airfranceklm":
				if len(origins) != 1 || len(destinations) != 1 {
					return fmt.Errorf("--provider afklm supports exactly one origin and one destination")
				}
				result, err = flights.SearchAFKLM(cmd.Context(), origins[0], destinations[0], date, opts)
			case "ryanair", "wizzair", "wizz", "transavia", "easyjet":
				if len(origins) != 1 || len(destinations) != 1 {
					return fmt.Errorf("--provider %s supports exactly one origin and one destination", provider)
				}
				result, err = flights.SearchLowCostCarrier(cmd.Context(), strings.ToLower(strings.TrimSpace(provider)), origins[0], destinations[0], date, opts)
			case "", "default", "google", "google_flights", "kiwi":
				if len(origins) > 1 || len(destinations) > 1 {
					result, err = flights.SearchMultiAirport(cmd.Context(), origins, destinations, date, opts)
				} else {
					result, err = flights.SearchFlights(cmd.Context(), origins[0], destinations[0], date, opts)
				}
			default:
				return fmt.Errorf("unsupported --provider %q (valid: skiplagged, afklm, ryanair, wizzair, transavia, easyjet, or empty for default Google+Kiwi+Skiplagged merge)", provider)
			}
			if err != nil {
				return err
			}

			// MIK-6229/6234: log the search and compute price-position + all
			// call-free savings via the shared pricefeed (single source shared
			// with the MCP path). Single-O/D only — a multi-airport search has no
			// canonical route key. Never breaks a search.
			var pricePos *pricesignal.Position
			var savings []counterfactual.Saving
			if len(origins) == 1 && len(destinations) == 1 && result != nil && len(result.Flights) > 0 {
				now := time.Now()
				fr := pricefeed.Flight(origins[0], destinations[0], date, result, now)
				pricePos = fr.Position
				savings = fr.Savings

				// MIK-6234 Tier 2: opt-in, budget-gated cold-route fan-out. The
				// probe lane is separate from interactive traffic; if exhausted
				// it refuses rather than issuing a silent fan-out.
				if deep {
					cheapest := cheapestFlightPrice(result.Flights)
					in := hacks.DetectorInput{
						Origin:      origins[0],
						Destination: destinations[0],
						Date:        date,
						ReturnDate:  returnDate,
						Currency:    result.Flights[0].Currency,
						NaivePrice:  cheapest * float64(adults),
						Passengers:  adults,
					}
					probed, st := cfprobe.Default().Probe(now, func() []hacks.Hack {
						return hacks.DetectAll(cmd.Context(), in)
					})
					savings = append(savings, probed...)
					if st == cfprobe.StatusBudgetExhausted {
						fmt.Fprintln(os.Stderr, "Note: deep counterfactual budget exhausted; showing call-free results only.")
					}
				}
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
				// JSON parity with the text path: attach best-effort
				// destination intelligence. The wrapper embeds *result, so
				// setting it once covers both the wrapped and bare branches.
				if result != nil {
					result.Destination = enrichArrival(cmd.Context(), destArg, models.DateRange{CheckIn: date, CheckOut: returnDate})
				}
				if pricePos != nil || len(savings) > 0 {
					return models.FormatJSON(os.Stdout, struct {
						*models.FlightSearchResult
						PricePosition *pricesignal.Position   `json:"price_position,omitempty"`
						Savings       []counterfactual.Saving `json:"savings,omitempty"`
					}{result, pricePos, savings})
				}
				return models.FormatJSON(os.Stdout, result)
			}

			if err := printFlightsTable(cmd.Context(), strings.Join(origins, ","), strings.Join(destinations, ","), targetCurrency, result, explain); err != nil {
				return err
			}
			printPricePosition(os.Stdout, pricePos)
			printSavings(os.Stdout, savings, time.Now())

			// Additive destination intelligence for the arrival city. destArg is
			// the user's typed destination (city or airport code); Nominatim
			// resolves either. Best-effort: nil prints nothing.
			showDestinationFooter(cmd.Context(), destArg, models.DateRange{CheckIn: date, CheckOut: returnDate})

			// Auto-trigger: run applicable hack detectors and print tips
			// below the flight results.
			maybeShowFlightHackTips(cmd.Context(), origins, destinations, date, returnDate, adults, result, flightRailFly)

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
	cmd.Flags().BoolVar(&explain, "explain", false, "Show per-factor profile match breakdown for each result")
	cmd.Flags().BoolVar(&award, "award", false, "Search Flying Blue award availability instead of cash fares")
	cmd.Flags().StringVar(&awardCookies, "award-cookies", "", "KLM/Flying Blue Cookie header for --award (or set AFKL_KLM_COOKIES)")
	cmd.Flags().StringVar(&provider, "provider", "", "Flight provider: empty = default (Google Flights + Kiwi + Skiplagged merge), skiplagged = Skiplagged MCP only (hidden-city + virtual-interlining defaults), afklm = Air France-KLM Offers API only (opt-in, native round-trip fares; requires credential), ryanair|wizzair|transavia|easyjet = a single low-cost carrier only (round-trips composed as two one-way tickets; transavia/easyjet are opt-in and require a key)")
	cmd.Flags().BoolVar(&flightRailFly, "rail-fly", false, "Expand the search to rail-connected origins (KL/AF Air&Rail), surfacing cheaper rail+fly bundles even when the origin is outside the default hub list")
	cmd.Flags().BoolVar(&deep, "deep", false, "Run a budget-gated counterfactual fan-out (nearby airports, split tickets, hidden city). Issues extra provider calls, capped by a best-effort budget that never delays the primary search")
	cmd.Flags().BoolVar(&noGeo, "no-geo", false, "Disable geo-IP origin detection (also honored via TRVL_NO_GEO=1). Origin then resolves only from an explicit code or your saved home airport.")
	cmd.Flags().BoolVar(&stealth, "stealth", false, "Opt in to authorized first-party stealth access (Chrome HTTP/2 fingerprint) for the flight fetch. Default off. Scope-fenced: activates ONLY for hosts on the operator allowlist TRVL_STEALTH_ALLOWLIST (comma-separated; empty = never). Fail-safe: a non-allowlisted host runs the normal path. Using stealth against sites that prohibit automated access is the operator's responsibility.")

	cmd.ValidArgsFunction = airportCompletion

	return cmd
}

// printFlightsTable renders flight results as an ASCII table.
// If targetCurrency is set and differs from API currency, converts prices.
func printFlightsTable(ctx context.Context, origin, destination, targetCurrency string, result *models.FlightSearchResult, explain bool) error {
	if !result.Success {
		_, _ = fmt.Fprintf(os.Stderr, "Search failed: %s\n", result.Error)
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
	if prefs != nil {              // all-in is self-gating: column only appears when allIn != basePrice for any flight
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

	headers := []string{"#", "Price"}
	if showAllIn {
		headers = append(headers, "All-in")
	}
	headers = append(headers, "Duration", "Stops", "Route")
	if showProvider {
		headers = append(headers, "Provider")
	}
	headers = append(headers, "Airline", "Flight", "Aircraft", "Departs", "Arrives")
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
		airline := flightAirlinesDisplay(f)
		flightNum := flightNumbersDisplay(f)
		aircraft := flightAircraftDisplay(f)
		departs := ""
		arrives := ""

		if len(f.Legs) > 0 {
			departs = formatLegDeparture(f.Legs[0].DepartureTime)
			arrives = formatLegArrival(f.Legs[0].DepartureTime, f.Legs[len(f.Legs)-1].ArrivalTime)
		}

		row := []string{
			fmt.Sprintf("%d", i+1),
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
		row = append(row, airline, flightNum, aircraft, departs, arrives)
		if showNotes {
			row = append(row, flightWarnings(f))
		}
		rows = append(rows, row)
	}

	models.FormatTable(os.Stdout, headers, rows)

	// Direct booking links, numbered to match the table's "#" column, so every
	// option shown is directly actionable. Full URLs would shatter table
	// alignment, so they live in a list beneath the grid.
	printBookingLinks(os.Stdout, result.Flights)

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

	// --explain: per-flight profile match breakdown.
	if explain {
		fmt.Println()
		// Determine primary destination IATA for scoring (first element if multi-airport).
		destCode := destination
		if idx := strings.Index(destination, ","); idx >= 0 {
			destCode = destination[:idx]
		}
		for i, f := range result.Flights {
			matchScore, breakdown := scoring.ComputeProfileMatch(prefs, scoring.DiscoverInput{
				AirportCode:  destCode,
				FlightPrice:  f.Price,
				Total:        f.Price,
				Stops:        f.Stops,
				DepartTime:   flightDepartHHMM(f),
				AirlineCodes: flightAirlineCodes(f),
			})
			label := fmt.Sprintf("#%d", i+1)
			if len(f.Legs) > 0 && f.Legs[0].Airline != "" {
				label = fmt.Sprintf("#%d %s", i+1, f.Legs[0].Airline)
			}
			printMatchBreakdown(label, matchScore, breakdown)
		}
	}

	return nil
}
