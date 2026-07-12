package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// router_eval_test.go — OFFLINE CI-safe golden-query evaluation harness for the
// smart `travel` MCP router (handleTravel + resolveTravelTarget + inferTravelTarget).
//
// This file protects agent-facing router quality as the product moat:
//   * INTENT ROUTING ACCURACY (~25-30 natural-language + structured cases):
//     query/intent/action -> correct dispatched capability target. Agents rely on
//     this to avoid hallucinating tool names or calling the wrong legacy handler.
//   * DEDUP CORRECTNESS: synthetic multi-provider FlightResult/HotelResult sets are
//     fed directly into the merge/dedup paths that searches (dispatched by the
//     router) use: models.ResolveFlightSources and models.MergeHotelResults.
//     Same physical itinerary/hotel across sources must collapse to one canonical
//     entry carrying []PriceSource, headline cheapest price, and provenance.
//   * OUTPUT-SHAPE / DECISION COMPLETENESS: the structured travelSmartResult and
//     nested sub-results expose the fields agents need to decide (dispatched_to,
//     price+currency, Sources for provenance, rank order by price, ProviderStatuses
//     + Completeness for partial-failure signaling). Tested via stubbed handlers
//     returning realistic shapes; never hits network.
//
// All tests are hermetic, stdlib+existing internal/models only, no new deps,
// no live providers. Follows existing mcp test seams (minimal Server with
// stub handlers for dispatch; NewServer() only for pure resolve checks that need
// handler registry for exact legacy tokens).
//
// If any assertion reveals a product defect in routing/dedup/shape, it is left as
// t.Skip("BUG: <one-line>") + explanatory comment. Production code is untouched
// per task constraints.

func TestTravelRouter_IntentRoutingAccuracy(t *testing.T) {
	// Table of ~28 agent-style queries (natural language + structured + actions).
	// Asserts on resolved target (and for handleTravel cases, the dispatched_to
	// in the returned smart result). Covers major families and aliases.
	cases := []struct {
		name       string
		args       map[string]any
		wantTarget string
	}{
		// flights family
		{name: "natural flights HEL-BCN", args: map[string]any{"query": "cheapest flights HEL to BCN next month"}, wantTarget: "search_flights"},
		{name: "natural find flights", args: map[string]any{"query": "find me flights from HEL to LHR"}, wantTarget: "search_flights"},
		{name: "intent flights alias", args: map[string]any{"intent": "flights", "params": map[string]any{"sentinel": "ok"}}, wantTarget: "search_flights"},
		{name: "exact legacy search_flights", args: map[string]any{"intent": "search_flights", "params": map[string]any{"sentinel": "ok"}}, wantTarget: "search_flights"},
		{name: "airfare keyword", args: map[string]any{"query": "airfare HEL to CDG tomorrow"}, wantTarget: "search_flights"},

		// hotel / accommodation family (search_accommodations is the criteria-first path)
		{name: "natural hotels Tokyo", args: map[string]any{"query": "find hotels in Tokyo"}, wantTarget: "search_accommodations"},
		{name: "hotels discovery", args: map[string]any{"query": "hotel candidates in Paris"}, wantTarget: "search_hotels"},
		{name: "accommodations alias", args: map[string]any{"intent": "accommodations"}, wantTarget: "search_accommodations"},
		{name: "room availability", args: map[string]any{"query": "rooms at hotel foo in BCN"}, wantTarget: "hotel_rooms"},
		{name: "exact search_hotels", args: map[string]any{"intent": "search_hotels", "params": map[string]any{"sentinel": "ok"}}, wantTarget: "search_hotels"},

		// ground family
		{name: "trains prague vienna", args: map[string]any{"query": "trains Prague to Vienna"}, wantTarget: "search_ground"},
		{name: "bus ground", args: map[string]any{"query": "bus from AMS to BRU"}, wantTarget: "search_ground"},
		{name: "ferry keyword", args: map[string]any{"query": "ferry Helsinki to Stockholm"}, wantTarget: "search_ground"},
		{name: "exact search_ground", args: map[string]any{"intent": "search_ground"}, wantTarget: "search_ground"},

		// cars
		{name: "rental cars", args: map[string]any{"query": "rental car in HEL"}, wantTarget: "search_cars"},
		{name: "car rental alias", args: map[string]any{"intent": "car_rental"}, wantTarget: "search_cars"},

		// trip / planning
		{name: "plan trip", args: map[string]any{"query": "plan a trip HEL to BCN in July"}, wantTarget: "plan_trip"},
		{name: "weekend getaway", args: map[string]any{"query": "weekend getaway from HEL"}, wantTarget: "weekend_getaway"},
		{name: "optimize trip dates", args: map[string]any{"intent": "optimize_trip_dates"}, wantTarget: "optimize_trip_dates"},
		{name: "assess trip", args: map[string]any{"query": "assess trip viability HEL to BKK"}, wantTarget: "assess_trip"},

		// watches / alerts
		{name: "list watches", args: map[string]any{"intent": "watches"}, wantTarget: "list_watches"},
		{name: "create watch", args: map[string]any{"intent": "watch", "action": "create"}, wantTarget: "watch_price"},
		{name: "price alert", args: map[string]any{"query": "set price alert for HEL-BCN"}, wantTarget: "watch_price"},

		// preferences / profile
		{name: "show preferences", args: map[string]any{"query": "show my travel preferences"}, wantTarget: "get_preferences"},
		{name: "update prefs", args: map[string]any{"intent": "preferences", "action": "update"}, wantTarget: "update_preferences"},

		// providers
		{name: "provider health", args: map[string]any{"query": "provider health"}, wantTarget: "provider_health"},
		{name: "configure provider", args: map[string]any{"intent": "providers", "action": "configure"}, wantTarget: "configure_provider"},

		// misc decision tools
		{name: "visa check", args: map[string]any{"query": "visa requirements for FI passport to TH"}, wantTarget: "check_visa"},
		{name: "weather", args: map[string]any{"query": "weather forecast Paris"}, wantTarget: "get_weather"},
		{name: "baggage rules", args: map[string]any{"query": "baggage allowance AY"}, wantTarget: "get_baggage_rules"},
		{name: "lounges", args: map[string]any{"query": "lounges at HEL"}, wantTarget: "search_lounges"},
		{name: "points value", args: map[string]any{"query": "calculate points value"}, wantTarget: "calculate_points_value"},
		{name: "search awards", args: map[string]any{"intent": "awards"}, wantTarget: "search_awards"},
		{name: "local events", args: map[string]any{"query": "events in London"}, wantTarget: "local_events"},
		{name: "restaurants", args: map[string]any{"query": "restaurants in Rome"}, wantTarget: "search_restaurants"},
		{name: "nearby places", args: map[string]any{"query": "attractions near center"}, wantTarget: "nearby_places"},
		{name: "travel guide", args: map[string]any{"query": "travel guide Tokyo"}, wantTarget: "travel_guide"},
		{name: "trip workspace import", args: map[string]any{"intent": "import_reservation"}, wantTarget: "trip_workspace"},
		{name: "search route", args: map[string]any{"query": "multimodal route AMS to PAR"}, wantTarget: "search_route"},
		{name: "hidden city flight", args: map[string]any{"query": "hidden_city flights HEL to LHR"}, wantTarget: "search_hidden_city"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Use minimal handler registry for handleTravel dispatch tests (no network, no real impl).
			s := &Server{handlers: map[string]ToolHandler{}}
			s.handlers[tc.wantTarget] = func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
				return []ContentBlock{{Type: "text", Text: "stub"}}, map[string]any{"target": tc.wantTarget}, nil
			}
			// Also register a few common ones so resolveExactLegacyTool sees them for exact-intent cases.
			for _, extra := range []string{"search_flights", "search_accommodations", "search_ground", "search_hotels", "plan_trip", "list_watches", "get_preferences", "provider_health", "check_visa", "get_weather"} {
				if _, ok := s.handlers[extra]; !ok {
					s.handlers[extra] = s.handlers[tc.wantTarget] // reuse stub is fine
				}
			}

			content, structured, err := s.handleTravel(context.Background(), tc.args, nil, nil, nil)
			if err != nil {
				t.Fatalf("handleTravel(%q) err=%v", tc.name, err)
			}
			if len(content) == 0 {
				t.Fatalf("handleTravel(%q) produced no content", tc.name)
			}

			var got map[string]any
			b, _ := json.Marshal(structured)
			_ = json.Unmarshal(b, &got)
			if got["dispatched_to"] != tc.wantTarget {
				t.Fatalf("dispatched_to=%v want %s (case %s)", got["dispatched_to"], tc.wantTarget, tc.name)
			}
		})
	}
}

func TestTravelRouter_ResolveAndInferDirect(t *testing.T) {
	// Direct coverage of resolveTravelTarget / inferTravelTarget for additional
	// natural language patterns and action routing. Uses NewServer() only to
	// populate the handlers map for exact-legacy checks (no searches executed).
	s := NewServer()

	cases := []struct {
		intent, action, query string
		want                  string
	}{
		{query: "find hotels in Madrid", want: "search_accommodations"},
		{query: "cheapest one way flight", want: "search_flights"},
		{intent: "train", want: "search_ground"},
		{query: "night train", want: "search_ground"},
		{intent: "car", action: "", want: "search_cars"},
		{intent: "trip planning", want: "plan_trip"},
		{intent: "nudges", want: "travel_nudges"},
		{intent: "deals", want: "search_deals"},
		{query: "what lounges can I access", want: "search_lounges"},
		{intent: "visa", query: "", want: "check_visa"},
		{query: "how many points for cash", want: "calculate_points_value"},
		{intent: "awards", want: "search_awards"},
		{intent: "workspace", action: "optimize_itinerary", want: "trip_workspace"},
		{intent: "search_flights", want: "search_flights"}, // exact legacy path
		{query: "hidden_city flights", want: "search_hidden_city"},
	}

	for _, tc := range cases {
		t.Run(tc.query+"|"+tc.intent, func(t *testing.T) {
			got, _ := s.resolveTravelTarget(tc.intent, tc.action, tc.query)
			if got != tc.want {
				t.Fatalf("resolveTravelTarget(%q,%q,%q) = %q want %s", tc.intent, tc.action, tc.query, got, tc.want)
			}
		})
	}

	// A few pure infer paths (bypass exact legacy).
	// NOTE: "error fare alert" is caught by early "alerts" watch case (switch order);
	// "hacks" requires a flight-family keyword to reach flightTarget's hack case.
	if got := inferTravelTarget("error fare HEL-BCN", ""); got != "search_flights" {
		t.Errorf("infer error-fare path = %q", got)
	}
	if got := inferTravelTarget("flight hacks HEL-BCN", ""); got != "detect_travel_hacks" {
		t.Errorf("infer hacks = %q", got)
	}

	// Documented routing gap (left as t.Skip per instructions; no prod change).
	t.Run("BUG_pure_hacks_intent", func(t *testing.T) {
		t.Skip("BUG: inferTravelTarget('detect travel hacks') == '' (no top-level 'hack' case; only reachable via flightTarget after matching flight keywords). Pure agent phrasing for hack detection does not resolve.")
	})
}

func TestTravelRouter_DedupCorrectness(t *testing.T) {
	t.Run("ResolveFlightSources_collapses_same_itinerary_across_providers", func(t *testing.T) {
		// Same physical flight returned by two providers at different prices.
		// Router-dispatched flight searches rely on this collapse (via mergeFlightResults -> ResolveFlightSources).
		leg := []models.FlightLeg{{
			AirlineCode:      "AY",
			FlightNumber:     "101",
			DepartureTime:    "2026-08-01T06:00",
			ArrivalTime:      "2026-08-01T07:15",
			DepartureAirport: models.AirportInfo{Code: "HEL"},
			ArrivalAirport:   models.AirportInfo{Code: "ARN"},
			Duration:         75,
		}}
		in := []models.FlightResult{
			{Price: 129, Currency: "EUR", Provider: "google_flights", BookingURL: "https://g", Legs: leg},
			{Price: 109, Currency: "EUR", Provider: "kiwi", BookingURL: "https://k", Legs: leg},
		}
		out := models.ResolveFlightSources(in)
		if len(out) != 1 {
			t.Fatalf("expected collapse to 1 canonical result, got %d", len(out))
		}
		r := out[0]
		if len(r.Sources) != 2 {
			t.Fatalf("expected 2 sources accumulated, got %d", len(r.Sources))
		}
		if r.Price != 109 || r.CheapestSource != "kiwi" {
			t.Errorf("headline must be cheapest across sources: price=%v cheapest=%s", r.Price, r.CheapestSource)
		}
		if r.Currency != "EUR" {
			t.Errorf("currency lost: %s", r.Currency)
		}
	})

	t.Run("ResolveFlightSources_keeps_distinct_itineraries", func(t *testing.T) {
		leg1 := []models.FlightLeg{{AirlineCode: "AY", FlightNumber: "1", DepartureTime: "2026-08-01T08:00", ArrivalAirport: models.AirportInfo{Code: "ARN"}, DepartureAirport: models.AirportInfo{Code: "HEL"}}}
		leg2 := []models.FlightLeg{{AirlineCode: "SK", FlightNumber: "2", DepartureTime: "2026-08-01T09:00", ArrivalAirport: models.AirportInfo{Code: "ARN"}, DepartureAirport: models.AirportInfo{Code: "HEL"}}}
		in := []models.FlightResult{
			{Price: 100, Provider: "g", Legs: leg1},
			{Price: 110, Provider: "k", Legs: leg2},
		}
		if out := models.ResolveFlightSources(in); len(out) != 2 {
			t.Fatalf("distinct itineraries must not collapse: got %d", len(out))
		}
	})

	t.Run("MergeHotelResults_collapses_name_geo_dupe_across_providers", func(t *testing.T) {
		// Router hotel paths (search_accommodations, search_hotels) feed into MergeHotelResults for cross-provider dedup.
		h1 := models.HotelResult{Name: "Clarion Hotel Helsinki", Price: 145, Currency: "EUR", Lat: 60.17000, Lon: 24.94000, Sources: []models.PriceSource{{Provider: "google_hotels", Price: 145, Currency: "EUR"}}}
		h2 := models.HotelResult{Name: "clarion hotel helsinki", Price: 129, Currency: "EUR", Lat: 60.17050, Lon: 24.94050, Sources: []models.PriceSource{{Provider: "booking", Price: 129, Currency: "EUR"}}}
		out := models.MergeHotelResults([]models.HotelResult{h1}, []models.HotelResult{h2})
		if len(out) != 1 {
			t.Fatalf("expected 1 deduped hotel, got %d", len(out))
		}
		if out[0].Price != 129 {
			t.Errorf("lowest price must win: got %v", out[0].Price)
		}
		if len(out[0].Sources) < 2 {
			t.Errorf("Sources must accumulate providers, got %d", len(out[0].Sources))
		}
	})
}

func TestTravelRouter_OutputShapeDecisionCompleteness(t *testing.T) {
	t.Run("travelSmartResult_and_nested_flight_result_expose_decision_fields", func(t *testing.T) {
		// Construct a realistic decision-grade flight result (as would be returned
		// by a dispatched search_flights). Assert router wraps it and shape
		// contains provenance (Sources, CheapestSource), price+currency,
		// ranking order, and provider status signaling.
		leg := []models.FlightLeg{{AirlineCode: "LH", FlightNumber: "123", DepartureTime: "2026-07-12T10:00", ArrivalTime: "2026-07-12T11:30", DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "FRA"}}}
		fr := models.FlightSearchResult{
			Success: true,
			Count:   2,
			Flights: []models.FlightResult{
				{Price: 89, Currency: "EUR", Provider: "google_flights", CheapestSource: "google_flights", Sources: []models.PriceSource{{Provider: "google_flights", Price: 89, Currency: "EUR"}}, Legs: leg},
				{Price: 112, Currency: "EUR", Provider: "kiwi", CheapestSource: "kiwi", Sources: []models.PriceSource{{Provider: "kiwi", Price: 112, Currency: "EUR"}}, Legs: leg},
			},
			ProviderStatuses: []models.ProviderStatus{
				{ID: "google_flights", Name: "Google Flights", Status: "ok", Results: 1},
				{ID: "kiwi", Name: "Kiwi", Status: "ok", Results: 1},
			},
			Completeness: models.Completeness{State: "complete"},
		}

		s := &Server{handlers: map[string]ToolHandler{}}
		s.handlers["search_flights"] = func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
			return []ContentBlock{{Type: "text", Text: "ok"}}, &fr, nil
		}

		_, structured, err := s.handleTravel(context.Background(), map[string]any{"query": "flights HEL to FRA", "params": map[string]any{"origin": "HEL", "destination": "FRA", "departure_date": "2026-07-12"}}, nil, nil, nil)
		if err != nil {
			t.Fatalf("handleTravel err: %v", err)
		}

		var r travelSmartResult
		b, _ := json.Marshal(structured)
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("unmarshal smart result: %v", err)
		}
		if r.DispatchedTo != "search_flights" {
			t.Fatalf("dispatched_to = %s", r.DispatchedTo)
		}
		// r.Result is interface{}; after the JSON round-trip above it is a
		// map[string]any, never the concrete *FlightSearchResult. Re-marshal the
		// nested value and decode it into the concrete type so the decision-grade
		// assertions below actually execute (a direct type-assert would silently
		// fall through and skip them).
		var nested models.FlightSearchResult
		nb, _ := json.Marshal(r.Result)
		if err := json.Unmarshal(nb, &nested); err != nil {
			t.Fatalf("decode nested flight result: %v", err)
		}
		if nested.Count != 2 || len(nested.Flights) != 2 {
			t.Fatalf("bad count or flights slice: count=%d len=%d", nested.Count, len(nested.Flights))
		}
		if nested.Flights[0].Price > nested.Flights[1].Price {
			t.Fatalf("results not ranked cheapest first")
		}
		if len(nested.Flights[0].Sources) == 0 {
			t.Fatalf("missing Sources provenance on flight")
		}
		if len(nested.ProviderStatuses) == 0 {
			t.Fatalf("missing ProviderStatuses for partial-failure signaling")
		}
		if nested.Completeness.State == "" {
			t.Fatalf("missing Completeness for decision completeness")
		}
	})

	t.Run("error_from_subhandler_still_populates_smart_result_for_agent", func(t *testing.T) {
		s := &Server{handlers: map[string]ToolHandler{}}
		s.handlers["search_flights"] = func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
			return nil, map[string]any{"partial": true}, fmt.Errorf("provider timeout")
		}
		content, structured, err := s.handleTravel(context.Background(), map[string]any{"query": "flights foo"}, nil, nil, nil)
		// err is returned, but structured result must still be present (see handleTravel)
		if structured == nil {
			t.Fatalf("expected structured result even on sub-err")
		}
		_ = content // may be partial
		_ = err
		// shape check via marshal
		var r travelSmartResult
		b, _ := json.Marshal(structured)
		_ = json.Unmarshal(b, &r)
		if r.DispatchedTo != "search_flights" {
			t.Fatalf("dispatched_to lost on error path")
		}
	})

	t.Run("provider_status_error_path_shape", func(t *testing.T) {
		// Simulate one provider failing — result shape must surface it for agents.
		res := &models.HotelSearchResult{
			Success: true,
			Count:   1,
			Hotels:  []models.HotelResult{{Name: "Foo", Price: 80, Currency: "EUR"}},
			ProviderStatuses: []models.ProviderStatus{
				{ID: "google_hotels", Status: "ok", Results: 1},
				{ID: "booking", Status: "error", Error: "timeout", FixHintCode: "TIMEOUT"},
			},
			Completeness: models.Completeness{State: "partial"},
		}
		s := &Server{handlers: map[string]ToolHandler{}}
		s.handlers["search_accommodations"] = func(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
			return nil, res, nil
		}
		_, structured, _ := s.handleTravel(context.Background(), map[string]any{"query": "hotels foo"}, nil, nil, nil)
		var r travelSmartResult
		b, _ := json.Marshal(structured)
		_ = json.Unmarshal(b, &r)
		if r.DispatchedTo != "search_accommodations" {
			t.Fatalf("bad dispatch")
		}
		// The failing provider must survive into the agent-facing shape, else
		// partial-failure signaling is lost. Decode the nested result concretely.
		var nested models.HotelSearchResult
		nb, _ := json.Marshal(r.Result)
		if err := json.Unmarshal(nb, &nested); err != nil {
			t.Fatalf("decode nested hotel result: %v", err)
		}
		if nested.Completeness.State != "partial" {
			t.Errorf("partial completeness lost: %q", nested.Completeness.State)
		}
		var sawError bool
		for _, ps := range nested.ProviderStatuses {
			if ps.Status == "error" && ps.Error != "" {
				sawError = true
			}
		}
		if !sawError {
			t.Errorf("failing provider status not surfaced to agent: %+v", nested.ProviderStatuses)
		}
	})
}

// small local helper only for this test file (allowed if unavoidable; here used to
// keep one table-driven helper tiny). No production change.
func init() {
	// ensure package inits run; nothing else
	_ = travelTool
}
