package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const smartToolModeEnv = "TRVL_MCP_TOOL_MODE"

// travelTool is the compact MCP entrypoint. Legacy tools remain callable by
// exact name via the intent field, but compact tools/list advertises this one
// router to keep client context small.
func travelTool() ToolDef {
	return ToolDef{
		Name:  "travel",
		Title: "Travel Smart Router",
		Description: "Primary trvl MCP tool. Route natural-language or structured travel requests " +
			"to the right capability while keeping the advertised tool list compact. Use query for " +
			"plain-language requests, intent for a family such as flights, hotels, ground, trip, " +
			"watches, preferences, or providers, and params for the target tool arguments. Exact " +
			"legacy tool names such as search_flights, search_hotels, search_ground, watch_price, " +
			"update_preferences, or configure_provider are accepted as intent values and remain " +
			"compatibility aliases.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"query": {
					Type:        "string",
					Description: "Natural-language travel request, e.g. 'find hotels in Tokyo' or 'train from Amsterdam to Paris'",
				},
				"intent": {
					Type:        "string",
					Description: "Optional target family or exact legacy tool name",
				},
				"action": {
					Type:        "string",
					Description: "Optional action for stateful families, e.g. list, create, update, check, configure, remove",
				},
				"params": {
					Type:        "object",
					Description: "Structured arguments forwarded to the resolved target tool",
				},
			},
		},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":         schemaString(),
				"intent":        schemaString(),
				"action":        schemaString(),
				"dispatched_to": schemaString(),
				"params":        schemaObject(),
				"result":        schemaObject(),
			},
		},
		Annotations: &ToolAnnotations{
			Title:           "Travel Smart Router",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  false,
			OpenWorldHint:   true,
		},
	}
}

func advertisedToolSurface(legacyTools []ToolDef) []ToolDef {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(smartToolModeEnv))) {
	case "legacy", "compat", "full":
		return legacyTools
	default:
		return []ToolDef{travelTool()}
	}
}

type travelSmartResult struct {
	Query        string         `json:"query,omitempty"`
	Intent       string         `json:"intent"`
	Action       string         `json:"action,omitempty"`
	DispatchedTo string         `json:"dispatched_to"`
	Params       map[string]any `json:"params,omitempty"`
	Result       interface{}    `json:"result,omitempty"`
}

func (s *Server) handleTravel(ctx context.Context, args map[string]any, elicit ElicitFunc, sampling SamplingFunc, progress ProgressFunc) ([]ContentBlock, interface{}, error) {
	query := strings.TrimSpace(argString(args, "query"))
	intent := strings.TrimSpace(argString(args, "intent"))
	params := smartToolParams(args)
	action := strings.TrimSpace(argString(args, "action"))
	if action == "" {
		action = strings.TrimSpace(argString(params, "action"))
	}

	target, resolvedIntent := s.resolveTravelTarget(intent, action, query)
	if target == "" {
		return nil, nil, fmt.Errorf("could not route travel request; provide intent or use a legacy tool name such as search_flights, search_hotels, search_ground, plan_trip, list_watches, get_preferences, or provider_health")
	}
	if target == "travel" {
		return nil, nil, fmt.Errorf("travel cannot dispatch to itself")
	}

	handler, ok := s.handlers[target]
	if !ok {
		return nil, nil, fmt.Errorf("resolved travel intent %q to unavailable tool %q", resolvedIntent, target)
	}

	content, structured, err := handler(ctx, params, elicit, sampling, progress)
	if err != nil {
		return content, travelSmartResult{
			Query:        query,
			Intent:       resolvedIntent,
			Action:       action,
			DispatchedTo: target,
			Params:       params,
			Result:       structured,
		}, err
	}

	return content, travelSmartResult{
		Query:        query,
		Intent:       resolvedIntent,
		Action:       action,
		DispatchedTo: target,
		Params:       params,
		Result:       structured,
	}, nil
}

func smartToolParams(args map[string]any) map[string]any {
	params := make(map[string]any)
	if raw, ok := args["params"].(map[string]any); ok {
		for k, v := range raw {
			params[k] = v
		}
	}
	for k, v := range args {
		if smartReservedArg(k) {
			continue
		}
		if _, exists := params[k]; !exists {
			params[k] = v
		}
	}
	return params
}

func smartReservedArg(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "query", "intent", "action", "params":
		return true
	default:
		return false
	}
}

func (s *Server) resolveTravelTarget(intent, action, query string) (string, string) {
	if target, ok := s.resolveExactLegacyTool(intent); ok {
		return target, normalizeSmartToken(intent)
	}

	intentText := strings.TrimSpace(intent)
	if intentText == "" {
		intentText = query
	}
	target := inferTravelTarget(intentText, action)
	if target == "" {
		if target, ok := resolveSmartIntentAlias(intent); ok {
			return target, normalizeSmartIntent(target)
		}
		return "", ""
	}
	return target, normalizeSmartIntent(target)
}

func (s *Server) resolveExactLegacyTool(intent string) (string, bool) {
	token := normalizeSmartToken(intent)
	if token == "" {
		return "", false
	}
	if token != "travel" {
		if _, ok := s.handlers[token]; ok {
			return token, true
		}
	}
	return "", false
}

func resolveSmartIntentAlias(intent string) (string, bool) {
	token := normalizeSmartToken(intent)
	if token == "" {
		return "", false
	}
	if target, ok := smartIntentAliases[token]; ok {
		return target, true
	}
	return "", false
}

var smartIntentAliases = map[string]string{
	"flight":           "search_flights",
	"flights":          "search_flights",
	"flight_search":    "search_flights",
	"hotel":            "search_hotels",
	"hotels":           "search_hotels",
	"hotel_search":     "search_hotels",
	"accommodation":    "search_hotels",
	"ground":           "search_ground",
	"ground_transport": "search_ground",
	"train":            "search_ground",
	"trains":           "search_ground",
	"bus":              "search_ground",
	"buses":            "search_ground",
	"ferry":            "search_ground",
	"ferries":          "search_ground",
	"route":            "search_route",
	"routes":           "search_route",
	"trip":             "plan_trip",
	"trip_planning":    "plan_trip",
	"plan":             "plan_trip",
	"planner":          "plan_trip",
	"watch":            "list_watches",
	"watches":          "list_watches",
	"price_watch":      "watch_price",
	"price_watches":    "list_watches",
	"alert":            "watch_price",
	"alerts":           "list_watches",
	"preference":       "get_preferences",
	"preferences":      "get_preferences",
	"prefs":            "get_preferences",
	"profile":          "get_preferences",
	"providers":        "provider_health",
	"provider":         "provider_health",
}

func inferTravelTarget(text, action string) string {
	token := normalizeSmartToken(text)
	if token == "" {
		return ""
	}
	actionToken := normalizeSmartToken(action)

	switch {
	case containsAny(token, "provider", "providers"):
		return providerActionTarget(token, actionToken)
	case containsAny(token, "watch", "watches", "alert", "alerts"):
		return watchActionTarget(token, actionToken)
	case containsAny(token, "preference", "preferences", "prefs", "profile", "onboard", "interview", "booking_history"):
		return profileActionTarget(token, actionToken)
	case containsAny(token, "hotel", "hotels", "accommodation", "lodging", "stay", "stays", "room", "rooms", "property"):
		return hotelTarget(token)
	case containsAny(token, "ground", "train", "trains", "bus", "buses", "ferry", "ferries", "night_train", "transfer", "transfers"):
		return groundTarget(token)
	case containsAny(token, "route", "multimodal"):
		return "search_route"
	case containsAny(token, "flight", "flights", "airfare", "airline", "airlines", "airport", "airports", "fare", "fares"):
		return flightTarget(token)
	case containsAny(token, "trip", "itinerary", "itineraries", "weekend", "destination", "destinations"):
		return tripTarget(token)
	case containsAny(token, "visa", "passport"):
		return "check_visa"
	case containsAny(token, "points", "miles", "award", "awards", "redemption"):
		if containsAny(token, "award", "awards", "seat", "seats") {
			return "search_awards"
		}
		return "calculate_points_value"
	case containsAny(token, "lounge", "lounges"):
		return "search_lounges"
	case containsAny(token, "weather", "forecast"):
		return "get_weather"
	case containsAny(token, "baggage", "bag", "bags", "luggage"):
		return "get_baggage_rules"
	case containsAny(token, "restaurant", "restaurants", "food", "dining"):
		return "search_restaurants"
	case containsAny(token, "event", "events", "concert", "concerts", "festival", "festivals"):
		return "local_events"
	case containsAny(token, "nearby", "poi", "attraction", "attractions", "place", "places"):
		return "nearby_places"
	case containsAny(token, "guide", "wikivoyage"):
		return "travel_guide"
	case containsAny(token, "deal", "deals"):
		return "search_deals"
	default:
		return ""
	}
}

func watchActionTarget(token, action string) string {
	switch {
	case containsAny(action, "create", "add", "set", "track") || containsAny(token, "create", "add", "set", "track"):
		return "watch_price"
	case containsAny(action, "check", "refresh", "run") || containsAny(token, "check", "refresh", "run"):
		return "check_watches"
	case containsAny(action, "opportunity", "opportunities") || containsAny(token, "opportunity", "opportunities"):
		return "watch_opportunities"
	default:
		return "list_watches"
	}
}

func profileActionTarget(token, action string) string {
	switch {
	case containsAny(action, "update", "save", "set", "change") || containsAny(token, "update", "save", "set", "change"):
		return "update_preferences"
	case containsAny(action, "onboard", "onboarding") || containsAny(token, "onboard", "onboarding"):
		return "onboard_profile"
	case containsAny(action, "build", "scan") || containsAny(token, "build", "scan"):
		return "build_profile"
	case containsAny(action, "add_booking", "booking") || containsAny(token, "add_booking", "booking"):
		return "add_booking"
	case containsAny(action, "interview") || containsAny(token, "interview"):
		return "interview_trip"
	default:
		return "get_preferences"
	}
}

func providerActionTarget(token, action string) string {
	switch {
	case containsAny(action, "configure", "config", "add", "create", "update", "set") || containsAny(token, "configure", "config", "add", "create", "update", "set"):
		return "configure_provider"
	case containsAny(action, "remove", "delete", "disable") || containsAny(token, "remove", "delete", "disable"):
		return "remove_provider"
	case containsAny(action, "suggest", "discover", "recommend") || containsAny(token, "suggest", "discover", "recommend"):
		return "suggest_providers"
	case containsAny(action, "test", "validate") || containsAny(token, "test", "validate"):
		return "test_provider"
	case containsAny(action, "list", "show") || containsAny(token, "list", "show"):
		return "list_providers"
	default:
		return "provider_health"
	}
}

func hotelTarget(token string) string {
	switch {
	case containsAny(token, "detail", "details", "amenities", "enrich"):
		return "search_hotels_with_details"
	case containsAny(token, "by_name", "named", "specific_property"):
		return "search_hotel_by_name"
	case containsAny(token, "price", "prices", "compare"):
		return "hotel_prices"
	case containsAny(token, "review", "reviews"):
		return "hotel_reviews"
	case containsAny(token, "room", "rooms", "availability"):
		if containsAny(token, "watch", "track", "alert") {
			return "watch_room_availability"
		}
		return "hotel_rooms"
	case containsAny(token, "hack", "hacks"):
		return "detect_accommodation_hacks"
	default:
		return "search_hotels"
	}
}

func groundTarget(token string) string {
	if containsAny(token, "airport_transfer", "transfer", "transfers", "taxi", "shuttle") {
		return "search_airport_transfers"
	}
	return "search_ground"
}

func flightTarget(token string) string {
	switch {
	case containsAny(token, "bundle", "interactive"):
		return "plan_flight_bundle"
	case containsAny(token, "hidden_city", "skiplag", "skiplagged"):
		return "search_hidden_city"
	case containsAny(token, "trip_dates", "optimize_dates"):
		return "optimize_trip_dates"
	case containsAny(token, "date", "dates", "calendar", "cheapest_day"):
		return "search_dates"
	case containsAny(token, "hack", "hacks"):
		return "detect_travel_hacks"
	case containsAny(token, "award", "awards", "seat", "seats"):
		return "search_awards"
	case containsAny(token, "lounge", "lounges"):
		return "search_lounges"
	case containsAny(token, "baggage", "bag", "bags", "luggage"):
		return "get_baggage_rules"
	default:
		return "search_flights"
	}
}

func tripTarget(token string) string {
	switch {
	case containsAny(token, "optimize_trip_dates", "trip_dates", "optimize_dates"):
		return "optimize_trip_dates"
	case containsAny(token, "optimize_booking", "booking_optimizer"):
		return "optimize_booking"
	case containsAny(token, "multi_city", "multicity"):
		return "optimize_multi_city"
	case containsAny(token, "cost", "budget", "estimate"):
		return "calculate_trip_cost"
	case containsAny(token, "weekend", "getaway"):
		return "weekend_getaway"
	case containsAny(token, "window", "windows"):
		return "find_trip_window"
	case containsAny(token, "assess", "viability", "go_no_go"):
		return "assess_trip"
	case containsAny(token, "list"):
		return "list_trips"
	case containsAny(token, "get", "show"):
		return "get_trip"
	case containsAny(token, "create", "new"):
		return "create_trip"
	case containsAny(token, "leg", "segment"):
		return "add_trip_leg"
	case containsAny(token, "booked", "booking"):
		return "mark_trip_booked"
	case containsAny(token, "ics", "calendar", "export"):
		return "export_ics"
	case containsAny(token, "destination", "destinations", "info"):
		return "destination_info"
	default:
		return "plan_trip"
	}
}

func normalizeSmartIntent(target string) string {
	return strings.TrimPrefix(strings.TrimPrefix(target, "search_"), "get_")
}

func normalizeSmartToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
