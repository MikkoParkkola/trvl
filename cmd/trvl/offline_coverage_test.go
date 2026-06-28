package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/cabinarb"
	"github.com/MikkoParkkola/trvl/internal/dealquality"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/forecast"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/los"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/multidest"
	"github.com/MikkoParkkola/trvl/internal/multimodal"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
	"github.com/MikkoParkkola/trvl/internal/railpass"
	"github.com/MikkoParkkola/trvl/internal/serpapi"
	"github.com/MikkoParkkola/trvl/internal/travelgraph"
	"github.com/MikkoParkkola/trvl/internal/tripcoalesce"
	"github.com/spf13/cobra"
)

func TestOfflineHelperParsersAndFileLoaders(t *testing.T) {
	bookings, err := parseBookingArgs([]string{"alice:300:alice,bob", "bob:150:bob=2,alice=1"})
	if err != nil {
		t.Fatalf("parseBookingArgs: %v", err)
	}
	if len(bookings) != 2 || bookings[0].Payer != "alice" || bookings[1].Split[0].Weight != 2 {
		t.Fatalf("bookings = %#v", bookings)
	}

	split, err := parseSplit(" alice , bob=2 ")
	if err != nil {
		t.Fatalf("parseSplit: %v", err)
	}
	if len(split) != 2 || split[0].Traveller != "alice" || split[1].Weight != 2 {
		t.Fatalf("split = %#v", split)
	}

	path := filepath.Join(t.TempDir(), "bookings.json")
	if err := os.WriteFile(path, []byte(`[{"payer":"alice","amount":42,"split":[{"traveller":"alice","weight":1}]}]`), 0o600); err != nil {
		t.Fatalf("write bookings: %v", err)
	}
	fromFile, err := loadBookingsFromFile(path)
	if err != nil {
		t.Fatalf("loadBookingsFromFile: %v", err)
	}
	if len(fromFile) != 1 || fromFile[0].Amount != 42 {
		t.Fatalf("fromFile = %#v", fromFile)
	}

	if _, err := parseBookingArgs([]string{":10"}); err == nil {
		t.Fatal("expected empty payer error")
	}
	if _, err := parseSplit(" "); err == nil {
		t.Fatal("expected empty split error")
	}
}

func TestCabinAndLengthOfStayOfflineHelpers(t *testing.T) {
	fares, err := parseCabinFares([]string{"economy:500:AY", "premium_economy:550"})
	if err != nil {
		t.Fatalf("parseCabinFares: %v", err)
	}
	if len(fares) != 2 || fares[0].Carrier != "AY" || fares[1].Cabin != cabinarb.CabinPremiumEconomy {
		t.Fatalf("fares = %#v", fares)
	}

	quotes, err := parseLOSQuotes([]string{"5:400", "6:390:r"})
	if err != nil {
		t.Fatalf("parseLOSQuotes: %v", err)
	}
	if len(quotes) != 2 || !quotes[1].Refundable {
		t.Fatalf("quotes = %#v", quotes)
	}

	cabinOut := tempOutputFile(t)
	renderCabinArbTable(cabinOut, []cabinarb.UpgradeRecommendation{{
		Baseline:      cabinarb.CabinEconomy,
		Target:        cabinarb.CabinPremiumEconomy,
		BaselinePrice: 500,
		TargetPrice:   550,
		UpsellPercent: 10,
		Reason:        "small upsell",
	}}, 15)
	if !strings.Contains(readTempOutput(t, cabinOut), "Upgrade recommendations") {
		t.Fatal("expected cabin arbitrage table output")
	}

	losOut := tempOutputFile(t)
	renderLosTable(losOut, 5, []los.Flip{{
		Kind:              "stay_longer_pay_less",
		BaselineNights:    5,
		AlternativeNights: 6,
		BaselineTotal:     400,
		AlternativeTotal:  390,
		TotalDelta:        -10,
		Refundable:        true,
		Reason:            "sixth night lowers total",
	}})
	if !strings.Contains(readTempOutput(t, losOut), "Length-of-stay alternatives") {
		t.Fatal("expected LOS table output")
	}
}

func TestFlightSerpapiAndNudgeOfflineHelpers(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 0},
		{Price: 320},
		{Price: 180},
	}
	if got := cheapestFlightPrice(flights); got != 180 {
		t.Fatalf("cheapestFlightPrice = %.0f, want 180", got)
	}

	flight := models.FlightResult{Legs: []models.FlightLeg{
		{DepartureTime: "2026-07-01T06:55:00", AirlineCode: "AY"},
		{DepartureTime: "2026-07-01T08:40:00", AirlineCode: "AY"},
		{DepartureTime: "2026-07-01T09:50:00", AirlineCode: "KL"},
	}}
	if got := flightDepartHHMM(flight); got != "06:55" {
		t.Fatalf("flightDepartHHMM = %q", got)
	}
	if got := strings.Join(flightAirlineCodes(flight), ","); got != "AY,KL" {
		t.Fatalf("flightAirlineCodes = %q", got)
	}

	hotel := serpapi.Hotel{
		FeaturedPrices: []serpapi.PriceOption{{
			Source:    "ProviderA",
			TotalRate: serpapi.Rate{Extracted: 500},
		}},
		Prices: []serpapi.PriceOption{{
			Source:       "ProviderB",
			RatePerNight: serpapi.Rate{Extracted: 120},
		}},
	}
	if got := providerSummary(hotel, "EUR", 1); !strings.Contains(got, "ProviderA") || !strings.Contains(got, "...") {
		t.Fatalf("providerSummary = %q", got)
	}
	if got := serpapiPriceStatus(hotel); got != "provider_prices_present" {
		t.Fatalf("serpapiPriceStatus = %q", got)
	}
	if got := serpapiPluralSuffix(2); got != "s" {
		t.Fatalf("serpapiPluralSuffix = %q", got)
	}
	if got := serpapiTimeout(20, false); got != 2*time.Minute {
		t.Fatalf("serpapiTimeout = %s", got)
	}
	if got := parseSerpapiCSV("wifi, pool,, spa "); strings.Join(got, "|") != "wifi|pool|spa" {
		t.Fatalf("parseSerpapiCSV = %#v", got)
	}
	ints, err := parseSerpapiIntCSV("3,4,5", "hotel-class")
	if err != nil || len(ints) != 3 || ints[2] != 5 {
		t.Fatalf("parseSerpapiIntCSV = %#v, err=%v", ints, err)
	}
	if got, err := serpapiRatingParam(4.6); err != nil || got != "9" {
		t.Fatalf("serpapiRatingParam = %q, err=%v", got, err)
	}

	srcs := []travelgraph.SourceRef{{Kind: "watch", ID: "w1"}, {Kind: "route", ID: "HEL-BCN"}}
	if got := formatSources(srcs); got != "watch:w1, route:HEL-BCN" {
		t.Fatalf("formatSources = %q", got)
	}
}

func TestMiscOfflineHelpers(t *testing.T) {
	values := []string{"z", "a", "m"}
	sortStrings(values)
	if strings.Join(values, "") != "amz" {
		t.Fatalf("sortStrings = %#v", values)
	}
	if got := len([]rune(progressBar(0.5, 10))); got != 10 {
		t.Fatalf("progressBar length = %d", got)
	}

	if got, err := parseNonNegativeFloat("12.5"); err != nil || got != 12.5 {
		t.Fatalf("parseNonNegativeFloat = %.1f, err=%v", got, err)
	}
	if _, err := parseNonNegativeFloat("-1"); err == nil {
		t.Fatal("expected negative value error")
	}
	if got := colorizeHiddenCityRisk(10); !strings.Contains(got, "10/100") {
		t.Fatalf("colorizeHiddenCityRisk low = %q", got)
	}
	if got := colorizeHiddenCityRisk(80); !strings.Contains(got, "80/100") {
		t.Fatalf("colorizeHiddenCityRisk high = %q", got)
	}
}

func TestCommandConstructorsDoNotRequireNetwork(t *testing.T) {
	constructors := map[string]func() any{
		"air":               func() any { return airCmd() },
		"sun":               func() any { return sunCmd() },
		"bikes":             func() any { return bikesCmd() },
		"cabin-arb":         func() any { return cabinArbCmd() },
		"expenses":          func() any { return expensesCmd() },
		"forecast":          func() any { return forecastCmd() },
		"hidden-city":       func() any { return hiddenCityCmd() },
		"los":               func() any { return losCmd() },
		"multimodal-plan":   func() any { return multimodalPlanCmd() },
		"nested":            func() any { return nestedCmd() },
		"nudges":            func() any { return nudgesCmd() },
		"opportunities":     func() any { return opportunitiesCmd() },
		"opportunity-score": func() any { return opportunityScoreCmd() },
		"points-value":      func() any { return pointsValueCmd() },
		"price-trends":      func() any { return pricetrendsCmd() },
		"providers":         func() any { return providersCmd() },
		"rail-pass":         func() any { return railPassCmd() },
		"rate-status":       func() any { return rateStatusCmd() },
		"reviews":           func() any { return reviewsCmd },
		"rooms":             func() any { return roomsCmd() },
		"route":             func() any { return routeCmd() },
		"serpapi":           func() any { return serpapiCmd() },
	}
	for name, build := range constructors {
		t.Run(name, func(t *testing.T) {
			if got := build(); got == nil {
				t.Fatalf("%s constructor returned nil", name)
			}
		})
	}
}

func TestOfflineDeterministicCommandRunPaths(t *testing.T) {
	oppCmd := opportunityScoreCmd()
	addInheritedFormatFlag(t, oppCmd, "table")
	oppOut := executeCommandWithStdout(t, oppCmd, "PRG:2026-07-01:2026-07-05:90:80:95", "--min-score", "70")
	if !strings.Contains(oppOut, "PRG") || !strings.Contains(oppOut, "Overall") {
		t.Fatalf("opportunity-score output = %q", oppOut)
	}

	candidateFile := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(candidateFile, []byte(`[{"Destination":"ROM","DepartDate":"2026-08-01","ReturnDate":"2026-08-05","Signals":{"ProfileMatch":80,"RequestMatch":70,"DealQuality":90}}]`), 0o600); err != nil {
		t.Fatalf("write candidates: %v", err)
	}
	oppFileCmd := opportunityScoreCmd()
	addInheritedFormatFlag(t, oppFileCmd, "json")
	oppJSON := executeCommandWithStdout(t, oppFileCmd, "--file", candidateFile)
	if !strings.Contains(oppJSON, `"Destination":"ROM"`) {
		t.Fatalf("opportunity-score JSON output = %q", oppJSON)
	}

	railCmd := railPassCmd()
	addInheritedFormatFlag(t, railCmd, "table")
	railOut := executeCommandWithStdout(t, railCmd, "299", "DB:AMS:BRU:120:10", "SNCF:PAR:LYS:110")
	if !strings.Contains(railOut, "Rail pass evaluation") || !strings.Contains(railOut, "Segments scored") {
		t.Fatalf("rail-pass output = %q", railOut)
	}

	multidestFile := filepath.Join(t.TempDir(), "orderings.json")
	if err := os.WriteFile(multidestFile, []byte(`[{"cities":["AMS","ROM"],"legs":[{"origin":"AMS","destination":"ROM","date":"2026-06-01","price":120}],"hotels":[{"city":"ROM","check_in":"2026-06-01","check_out":"2026-06-05","total_price":400}]}]`), 0o600); err != nil {
		t.Fatalf("write multidest: %v", err)
	}
	multiCmd := multidestCmd()
	addInheritedFormatFlag(t, multiCmd, "table")
	multiOut := executeCommandWithStdout(t, multiCmd, "--file", multidestFile, "--top-k", "1")
	if !strings.Contains(multiOut, "Top 1 multi-destination bundles") || !strings.Contains(multiOut, "AMS -> ROM") {
		t.Fatalf("multidest output = %q", multiOut)
	}

	pointsList := executeCommandWithStdout(t, pointsValueCmd(), "--list", "--format", "json")
	if !strings.Contains(pointsList, `"Slug"`) {
		t.Fatalf("points-value list output = %q", pointsList)
	}
	pointsArb := executeCommandWithStdout(t, pointsValueCmd(), "--cash", "300", "--offer", "world-of-hyatt:12000", "--currency", "USD")
	if !strings.Contains(pointsArb, "Hotel Points Arbitrage") || !strings.Contains(pointsArb, "World of Hyatt") {
		t.Fatalf("points arbitrage output = %q", pointsArb)
	}

	var statusOut bytes.Buffer
	statusCmd := rateStatusCmd()
	statusCmd.SetOut(&statusOut)
	statusCmd.SilenceUsage = true
	statusCmd.SilenceErrors = true
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("rate-status Execute: %v", err)
	}
	if !strings.Contains(statusOut.String(), "Rate Limit Status") || !strings.Contains(statusOut.String(), "google:") {
		t.Fatalf("rate-status output = %q", statusOut.String())
	}
}

func TestOfflineRenderersForCompositePlans(t *testing.T) {
	coalesced := &tripcoalesce.TripPlan{
		Origin:      "HEL",
		Destination: "LHR",
		DepartDate:  "2026-07-01",
		ReturnDate:  "2026-07-08",
		Currency:    "EUR",
		CheapestFlight: &models.FlightResult{
			Price:    220,
			Currency: "EUR",
			Duration: 180,
			Stops:    0,
		},
		CheapestHotel: &models.HotelResult{Name: "Central", Price: 100, Currency: "EUR"},
		CheapestGround: &models.GroundRoute{
			Provider: "eurostar",
			Type:     "train",
			Price:    80,
			Currency: "EUR",
		},
		TotalCostEstimate: 400,
		CostBreakdown: []tripcoalesce.CostComponent{
			{Domain: "flights", Label: "cheapest flight", Amount: 220, Currency: "EUR"},
			{Domain: "hotels", Label: "Central", Amount: 100, Currency: "EUR"},
			{Domain: "ground", Label: "eurostar", Amount: 80, Currency: "EUR"},
		},
		Statuses: []tripcoalesce.DomainStatus{
			{Domain: "flights", OK: true, Count: 1},
			{Domain: "hotels", OK: true, Count: 1},
			{Domain: "ground", OK: true, Count: 1},
			{Domain: "rideshare", OK: false, Error: "not configured"},
		},
		Notes: []string{"floor estimate only"},
	}
	coalescedOut := captureStdout(t, func() { printCoalescedPlan(coalesced) })
	if !strings.Contains(coalescedOut, "Floor estimate") || !strings.Contains(coalescedOut, "floor estimate only") {
		t.Fatalf("coalesced output = %q", coalescedOut)
	}
	if got := domainNote(coalesced, "rideshare"); !strings.Contains(got, "not configured") {
		t.Fatalf("domainNote = %q", got)
	}
	if got := domainNote(coalesced, "missing"); got != "no results" {
		t.Fatalf("domainNote missing = %q", got)
	}

	plan := &multimodal.Plan{
		From:       "Helsinki",
		To:         "Tallinn",
		Date:       "2026-07-01",
		Discovered: 2,
		Notes:      []string{"priced one route"},
		Itineraries: []multimodal.Itinerary{{
			From:        "Helsinki",
			To:          "Tallinn",
			Date:        "2026-07-01",
			ModeChain:   "ferry",
			TotalPrice:  42,
			Currency:    "EUR",
			DurationMin: 120,
			BookingURL:  "https://example.test/ferry",
			Legs: []multimodal.PricedLeg{{
				Mode:        "ferry",
				From:        "Helsinki",
				To:          "Tallinn",
				Price:       42,
				Currency:    "EUR",
				DurationMin: 120,
				Provider:    "fixture",
				Detail:      "day ferry",
			}},
			Warnings: []string{"verify before booking"},
		}},
	}
	multimodalOut := captureStdout(t, func() { printMultimodalPlan(plan) })
	if !strings.Contains(multimodalOut, "Multimodal") || !strings.Contains(multimodalOut, "day ferry") {
		t.Fatalf("multimodal output = %q", multimodalOut)
	}

	emptyPlan := &multimodal.Plan{From: "A", To: "B", Date: "2026-07-01", Notes: []string{"nothing found"}}
	stderr := captureStderrOffline(t, func() { printMultimodalPlan(emptyPlan) })
	if !strings.Contains(stderr, "No multimodal itineraries") || !strings.Contains(stderr, "nothing found") {
		t.Fatalf("empty multimodal stderr = %q", stderr)
	}
}

func TestOfflinePureParsingVariants(t *testing.T) {
	segments, err := parseRailSegments([]string{"120", "DB:AMS:BRU:89", "SNCF:PAR:LYS:45:12"})
	if err != nil {
		t.Fatalf("parseRailSegments: %v", err)
	}
	if len(segments) != 3 || segments[2].ReservationFee != 12 {
		t.Fatalf("segments = %#v", segments)
	}
	if _, err := parseRailSegments([]string{"DB:AMS"}); err == nil {
		t.Fatal("expected malformed rail segment error")
	}

	offers, err := parsePointsOffers([]string{"world-of-hyatt:12000:15", "hilton-honors:80000"})
	if err != nil {
		t.Fatalf("parsePointsOffers: %v", err)
	}
	if len(offers) != 2 || offers[0].CashFees != 15 {
		t.Fatalf("offers = %#v", offers)
	}
	if _, err := parsePointsOffers([]string{"bad"}); err == nil {
		t.Fatal("expected malformed points offer error")
	}
	if _, err := parsePositiveInt("0"); err == nil {
		t.Fatal("expected positive int error")
	}

	rec := railpass.EvaluatePass(railpass.PassOption{Name: "Pass", Cost: 200}, []railpass.PointToPointSegment{{Price: 120}, {Price: 130}})
	if rec.Verdict != railpass.VerdictMarginal && rec.Verdict != railpass.VerdictBuyPass {
		t.Fatalf("rail recommendation = %#v", rec)
	}

	bundles := multidest.ScreenAndDrillDown([]multidest.Ordering{{
		Cities: []string{"AMS", "ROM"},
		Legs:   []multidest.Leg{{Origin: "AMS", Destination: "ROM", Price: 100}},
		Hotels: []multidest.HotelCost{{City: "ROM", TotalPrice: 200}},
	}}, multidest.ScreenOptions{})
	if len(bundles) != 1 || bundles[0].GrandTotal != 300 {
		t.Fatalf("bundles = %#v", bundles)
	}
}

func TestOfflineAdditionalCommandRenderers(t *testing.T) {
	expenseCmd := expensesCmd()
	addInheritedFormatFlag(t, expenseCmd, "table")
	expenseOut := executeCommandWithStdout(t, expenseCmd, "alice:300:alice,bob", "bob:100:bob")
	if !strings.Contains(expenseOut, "alice") || !strings.Contains(expenseOut, "bob") {
		t.Fatalf("expenses output = %q", expenseOut)
	}

	expenseFile := filepath.Join(t.TempDir(), "bookings.json")
	if err := os.WriteFile(expenseFile, []byte(`[{"Payer":"alice","Amount":120,"Split":[{"Traveller":"alice","Weight":1},{"Traveller":"bob","Weight":1}]}]`), 0o600); err != nil {
		t.Fatalf("write expense file: %v", err)
	}
	expenseJSONCmd := expensesCmd()
	addInheritedFormatFlag(t, expenseJSONCmd, "json")
	expenseJSON := executeCommandWithStdout(t, expenseJSONCmd, "--file", expenseFile)
	if !strings.Contains(expenseJSON, `"Transfers"`) {
		t.Fatalf("expenses JSON output = %q", expenseJSON)
	}

	carResult := &models.CarSearchResult{
		Success: true,
		Count:   1,
		Offers: []models.CarOffer{{
			Provider:     "skyscanner",
			Supplier:     "Hertz",
			VehicleName:  "Golf",
			VehicleClass: "compact",
			Price:        144,
			Currency:     "EUR",
			Seats:        5,
			Pickup:       models.CarEndpoint{Location: "HEL"},
		}},
	}
	carsOut := captureStdout(t, func() { printCarsTable(carResult) })
	if !strings.Contains(carsOut, "Rental Cars") || !strings.Contains(carsOut, "Hertz") || intString(5) != "5" {
		t.Fatalf("cars output = %q", carsOut)
	}
	carsErr := captureStderrOffline(t, func() { printCarsTable(&models.CarSearchResult{Error: "missing key"}) })
	if !strings.Contains(carsErr, "missing key") || intString(0) != "-" {
		t.Fatalf("cars error output = %q", carsErr)
	}

	reviewsOut := captureStdout(t, func() {
		if err := printReviewsTable(&models.HotelReviewResult{
			Name:    "Hotel Test",
			Summary: models.ReviewSummary{AverageRating: 4.5, TotalReviews: 12},
			Reviews: []models.HotelReview{{
				Rating: 4.5,
				Author: "Ada",
				Date:   "2026-07-01",
				Text:   strings.Repeat("good ", 30),
			}},
		}); err != nil {
			t.Fatalf("printReviewsTable: %v", err)
		}
	})
	if !strings.Contains(reviewsOut, "Hotel Test") || !strings.Contains(reviewsOut, "Ada") {
		t.Fatalf("reviews output = %q", reviewsOut)
	}

	freeCancel, breakfast := true, true
	refundable := false
	roomsOut := captureStdout(t, func() {
		if err := formatRoomsTable(&hotels.RoomAvailability{
			HotelID:  "hotel-1",
			Name:     "Room Test",
			CheckIn:  "2026-07-01",
			CheckOut: "2026-07-03",
			Notice:   "fixture data",
			Rooms: []hotels.RoomType{{
				Name:              "Standard",
				Price:             220,
				Currency:          "EUR",
				MaxGuests:         2,
				BedType:           "Queen",
				Board:             "room_only",
				Provider:          "fixture",
				Amenities:         []string{"wifi", "desk"},
				FreeCancellation:  &freeCancel,
				BreakfastIncluded: &breakfast,
			}, {
				Name:       "Budget",
				Price:      180,
				Currency:   "EUR",
				Refundable: &refundable,
			}},
		}); err != nil {
			t.Fatalf("formatRoomsTable: %v", err)
		}
	})
	if !strings.Contains(roomsOut, "Room Test") || !strings.Contains(roomsOut, "Cheapest") {
		t.Fatalf("rooms output = %q", roomsOut)
	}
	if got := prettyLabel("free_cancellation"); got != "Free cancellation" {
		t.Fatalf("prettyLabel = %q", got)
	}
}

func TestOfflinePricePositionRenderer(t *testing.T) {
	var sparse bytes.Buffer
	printPricePosition(&sparse, &pricesignal.Position{Observations: 3})
	if !strings.Contains(sparse.String(), "not enough history") {
		t.Fatalf("sparse price position = %q", sparse.String())
	}

	var confident bytes.Buffer
	printPricePosition(&confident, &pricesignal.Position{
		Confident:    true,
		Verdict:      pricesignal.VerdictWait,
		Band:         pricesignal.BandHigh,
		Low:          100,
		Median:       150,
		High:         220,
		VsMedianPct:  20,
		Observations: 12,
	})
	if !strings.Contains(confident.String(), "WAIT") || !strings.Contains(confident.String(), "+20%") {
		t.Fatalf("confident price position = %q", confident.String())
	}
	if verdictText("unknown") != "no verdict" || bandText("unknown") != "position unknown" || signedPct(-12) != "-12%" {
		t.Fatal("expected fallback price-position labels")
	}
}

func TestOfflineAwardScanAndRenderer(t *testing.T) {
	t.Setenv("AFKL_KLM_COOKIES", "")
	help := captureStderrOffline(t, func() {
		if err := runAwardScan(context.Background(), "HEL", "CDG", "2026-07", "", "table"); err != nil {
			t.Fatalf("runAwardScan no-cookie path: %v", err)
		}
	})
	if !strings.Contains(help, "Award search requires") || !strings.Contains(help, "trvl flights HEL CDG 2026-07 --award") {
		t.Fatalf("award no-cookie help = %q", help)
	}

	out := captureStdout(t, func() {
		if err := printAwardTable(&afklm.AwardScanResult{
			Origin:      "hel",
			Destination: "cdg",
			Offers: []afklm.AwardOffer{
				{Date: "2026-07-02", FlightNumber: "KL2", DepartureTime: "10:00", ArrivalTime: "12:30", Miles: 14000, TaxEUR: 31.5, Cabin: "ECONOMY", Stops: 1, Available: true},
				{Date: "2026-07-01", FlightNumber: "KL1", DepartureTime: "08:00", ArrivalTime: "10:30", Miles: 9000, TaxEUR: 28.0, Cabin: "BUSINESS", Available: true},
			},
			Errors: map[string]string{"2026-07-03": "temporary upstream miss"},
		}); err != nil {
			t.Fatalf("printAwardTable offers: %v", err)
		}
	})
	for _, want := range []string{"Flying Blue Award Scan", "KL1", "ideal", "1 stop", "Errors on 1 date", "Book at:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("award table missing %q in %q", want, out)
		}
	}

	empty := captureStdout(t, func() {
		if err := printAwardTable(&afklm.AwardScanResult{Errors: map[string]string{"2026-07-04": "blocked"}}); err != nil {
			t.Fatalf("printAwardTable empty: %v", err)
		}
	})
	if !strings.Contains(empty, "No award offers found") || !strings.Contains(empty, "blocked") {
		t.Fatalf("award empty table = %q", empty)
	}
	if !isMonthString("2026-07") || isMonthString("2026-07-01") {
		t.Fatal("isMonthString classification mismatch")
	}
}

func TestOfflineForecastRenderHelpers(t *testing.T) {
	samples := []dealquality.Sample{{Price: 100}, {Price: 125}, {Price: 150}}
	prices := samplesToFloat64(samples)
	if len(prices) != 3 || prices[1] != 125 {
		t.Fatalf("prices = %#v", prices)
	}
	if store, err := openHistoryStore(filepath.Join(t.TempDir(), "history.json")); err != nil || store == nil {
		t.Fatalf("openHistoryStore override = %v, %v", store, err)
	}

	if got := renderSparkline(forecast.Curve{}); got != "(no data)" {
		t.Fatalf("empty sparkline = %q", got)
	}
	if got := renderSparkline(forecast.Curve{Buckets: []int{0, 0, 0}}); len([]rune(got)) != 3 {
		t.Fatalf("zero sparkline = %q", got)
	}
	curve := forecast.Curve{Buckets: []int{1, 3, 2}, Min: 80, Max: 160, Threshold: 120}
	if got := renderSparkline(curve); !strings.ContainsRune(got, '\u258f') {
		t.Fatalf("threshold sparkline = %q", got)
	}

	insufficient := tempOutputFile(t)
	renderForecastTable(insufficient, "HEL-CDG", "flight", "summer", 200, forecast.Forecast{
		InsufficientData: true,
		Reason:           "need more samples",
	}, forecast.Curve{})
	if out := readTempOutput(t, insufficient); !strings.Contains(out, "recommendation suppressed") {
		t.Fatalf("insufficient forecast table = %q", out)
	}

	confident := tempOutputFile(t)
	renderForecastTable(confident, "HEL-CDG", "flight", "summer", 200, forecast.Forecast{
		CommitNowConfidence:   72,
		HorizonDays:           14,
		HorizonProbability:    0.25,
		DropProbability:       0.12,
		ExpectedSavingsIfWait: 18.5,
		Samples:               40,
		Reason:                "buy now",
	}, curve)
	if out := readTempOutput(t, confident); !strings.Contains(out, "Commit-now confidence: 72/100") || !strings.Contains(out, "Price distribution") {
		t.Fatalf("confident forecast table = %q", out)
	}
}

func TestOfflineOpportunityWatchCommands(t *testing.T) {
	setTestHome(t, t.TempDir())
	oldFormat := format
	format = "table"
	t.Cleanup(func() { format = oldFormat })

	listEmpty := executeCommandWithStdout(t, opportunitiesCmd())
	if !strings.Contains(listEmpty, "No opportunity watches") {
		t.Fatalf("empty opportunities output = %q", listEmpty)
	}

	created := executeCommandWithStdout(t, opportunitiesCmd(), "create", "--favourites", "prg, krk", "--min-score", "90", "--min-nights", "4", "--max-nights", "6")
	if !strings.Contains(created, "Created opportunity watch") || !strings.Contains(created, "PRG, KRK") {
		t.Fatalf("create opportunity output = %q", created)
	}

	listed := executeCommandWithStdout(t, opportunitiesCmd())
	if !strings.Contains(listed, "PRG,KRK") || !strings.Contains(listed, "90") || !strings.Contains(listed, "4-6") {
		t.Fatalf("listed opportunities output = %q", listed)
	}
}

func TestOfflineRouteAndExplainRenderers(t *testing.T) {
	breakdown := captureStdout(t, func() {
		printMatchBreakdown("Option A", 88, map[string]float64{
			"budget fit": 0.90,
			"duration":   0.75,
		})
	})
	if !strings.Contains(breakdown, "profile match breakdown") || !strings.Contains(breakdown, "budget fit") {
		t.Fatalf("match breakdown output = %q", breakdown)
	}
	if empty := captureStdout(t, func() { printMatchBreakdown("empty", 0, nil) }); empty != "" {
		t.Fatalf("empty match breakdown output = %q", empty)
	}

	noRoute := captureStderrOffline(t, func() {
		if err := printRouteTable(context.Background(), "EUR", &models.RouteSearchResult{Success: false, Error: "no path"}); err != nil {
			t.Fatalf("printRouteTable no-route: %v", err)
		}
	})
	if !strings.Contains(noRoute, "No routes found: no path") {
		t.Fatalf("no-route output = %q", noRoute)
	}

	routeOut := captureStdout(t, func() {
		err := printRouteTable(context.Background(), "EUR", &models.RouteSearchResult{
			Success:     true,
			Origin:      "Helsinki",
			Destination: "Amsterdam",
			Date:        "2026-07-01",
			Count:       1,
			Itineraries: []models.RouteItinerary{{
				TotalPrice:    123.4,
				Currency:      "EUR",
				TotalDuration: 365,
				Transfers:     2,
				Legs: []models.RouteLeg{
					{Mode: "flight", Provider: "google", FromCode: "HEL", ToCode: "CPH", Departure: "2026-07-01T08:00:00", Arrival: "2026-07-01T09:30:00", Duration: 90, Price: 80, Currency: "EUR"},
					{Mode: "train", Provider: "db", From: "Copenhagen", To: "Amsterdam", Departure: "2026-07-01T10:30:00", Arrival: "2026-07-01T15:35:00", Duration: 305, Price: 43, Currency: "EUR"},
				},
			}},
		})
		if err != nil {
			t.Fatalf("printRouteTable success: %v", err)
		}
	})
	for _, want := range []string{"Route", "Option 1", "EUR 123", "2 transfers", "flight", "train", "HEL"} {
		if !strings.Contains(routeOut, want) {
			t.Fatalf("route table missing %q in %q", want, routeOut)
		}
	}
	if modeIcon("walk") != "walk" || formatRoutePrice(0, "EUR") != "-" {
		t.Fatal("route helper fallback mismatch")
	}
}

func TestOfflineSelfUpdateShortCircuits(t *testing.T) {
	oldVersion := Version
	oldCheckOnly := selfUpdateCheckOnly
	oldTarget := selfUpdateTargetVersion
	oldForce := selfUpdateForceStandalone
	t.Cleanup(func() {
		Version = oldVersion
		selfUpdateCheckOnly = oldCheckOnly
		selfUpdateTargetVersion = oldTarget
		selfUpdateForceStandalone = oldForce
	})

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	Version = "dev"
	if err := runSelfUpdate(cmd, nil); err != nil {
		t.Fatalf("dev self-update: %v", err)
	}
	if !strings.Contains(out.String(), "Install method: dev") || !strings.Contains(out.String(), "no self-update available") {
		t.Fatalf("dev self-update output = %q", out.String())
	}

	out.Reset()
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	Version = "1.2.3"
	selfUpdateForceStandalone = true
	selfUpdateCheckOnly = true
	selfUpdateTargetVersion = "9.9.9"
	if err := runSelfUpdate(cmd, nil); err != nil {
		t.Fatalf("forced check-only self-update: %v", err)
	}
	if !strings.Contains(out.String(), "Target version: v9.9.9") || !strings.Contains(out.String(), "--check:") {
		t.Fatalf("check-only self-update output = %q stderr=%q", out.String(), stderr.String())
	}
}

func tempOutputFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func readTempOutput(t *testing.T, f *os.File) string {
	t.Helper()
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

func addInheritedFormatFlag(t *testing.T, cmd *cobra.Command, value string) {
	t.Helper()
	if cmd.Flags().Lookup("format") != nil {
		return
	}
	cmd.Flags().String("format", value, "output format")
}

func executeCommandWithStdout(t *testing.T, cmd *cobra.Command, args ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s Execute: %v", cmd.Name(), err)
		}
	})
}

func captureStderrOffline(t *testing.T, fn func()) string {
	t.Helper()
	f := tempOutputFile(t)
	defer func() { _ = f.Close() }()
	old := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = old }()
	fn()
	return readTempOutput(t, f)
}
