package mcp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/models"
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
			"plain-language requests, intent for a family such as flights, hotels, cars, ground, trip, " +
			"watches, preferences, or providers, and params for the target tool arguments. Exact " +
			"legacy tool names such as search_flights, search_accommodations, search_hotels, search_ground, watch_price, " +
			"update_preferences, or configure_provider are accepted as intent values and remain " +
			"legacy-compatible capabilities. Params for the two main searches: search_flights needs " +
			"destination and departure_date (YYYY-MM-DD), origin optional (IATA code or known city); " +
			"search_ground needs from, to and date (YYYY-MM-DD). For any other tool's exact params, " +
			"call with that intent and action describe.",
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
				"renamed": map[string]interface{}{
					"type": "array", "items": schemaString(),
				},
				"unrecognized_params": map[string]interface{}{
					"type": "array", "items": schemaString(),
				},
				"result": schemaObject(),
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

// secretParamKeys are request parameters whose VALUE is itself a credential.
//
// A webhook URL is the bearer token for Slack/Discord-style endpoints: anyone
// holding the URL can post as you. The standing rule is that it must never
// reach MCP structured output, logs or error strings, while the storage file
// and the CLI keep it by design. This router echoes the caller's params back as
// structured output, so without redaction a single watch_price call through the
// `travel` tool published the credential into model context, transcripts and
// client logs. Found by GPT second-opinion review, 2026-08-02.
var secretParamKeys = map[string]bool{
	"webhook":     true,
	"webhook_url": true,
}

// redactSecretParams returns a copy of params with credential-valued entries
// replaced by a non-reversible marker, and the same treatment applied to nested
// maps and slices.
//
// A copy, not an in-place edit: params is the live argument map the dispatched
// handler also reads, and blanking a webhook there would silently stop the
// watch from ever notifying.
func redactSecretParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if secretParamKeys[strings.ToLower(k)] {
			if s, ok := v.(string); ok && s != "" {
				out[k] = "[redacted]"
				continue
			}
		}
		out[k] = redactSecretValue(v)
	}
	return out
}

func redactSecretValue(v any) any {
	switch tv := v.(type) {
	case map[string]any:
		return redactSecretParams(tv)
	case []any:
		out := make([]any, len(tv))
		for i, e := range tv {
			out[i] = redactSecretValue(e)
		}
		return out
	default:
		return v
	}
}

// redactSecretsInText masks webhook-shaped URLs inside free text, so a query a
// user typed the URL into does not leak it either.
//
// The host is kept deliberately: it is what makes a line diagnosable, and it is
// not the secret -- the path and query are, which is where Slack/Discord-style
// tokens live.
//
// Scanning advances past each rewrite. An earlier version searched from index 0
// every iteration, so it re-found the URL it had just masked and span forever;
// the package test suite hit the 10-minute timeout rather than failing an
// assertion.
func redactSecretsInText(s string) string {
	const mask = "/[redacted]"
	var b strings.Builder
	for pos := 0; ; {
		rel, scheme := -1, ""
		for _, cand := range []string{"https://", "http://"} {
			if i := strings.Index(s[pos:], cand); i >= 0 && (rel < 0 || i < rel) {
				rel, scheme = i, cand
			}
		}
		if rel < 0 {
			b.WriteString(s[pos:])
			return b.String()
		}
		urlStart := pos + rel + len(scheme)
		b.WriteString(s[pos : pos+rel])
		b.WriteString(scheme)

		rest := s[urlStart:]
		end := strings.IndexAny(rest, " \t\n\r\"'")
		if end < 0 {
			end = len(rest)
		}
		raw := rest[:end]
		if slash := strings.Index(raw, "/"); slash >= 0 {
			b.WriteString(raw[:slash])
			b.WriteString(mask)
		} else {
			// Bare host, no path or query: nothing secret to strip.
			b.WriteString(raw)
		}
		pos = urlStart + end
	}
}

type travelSmartResult struct {
	Query              string         `json:"query,omitempty"`
	Intent             string         `json:"intent"`
	Action             string         `json:"action,omitempty"`
	DispatchedTo       string         `json:"dispatched_to"`
	Params             map[string]any `json:"params,omitempty"`
	Renamed            []string       `json:"renamed,omitempty"`
	UnrecognizedParams []string       `json:"unrecognized_params,omitempty"`
	Result             interface{}    `json:"result,omitempty"`
}

// routerParamAliases maps a target tool's foreign spelling to its canonical
// argument. search_flights and search_ground name the same concepts
// differently (origin/destination/departure_date vs from/to/date), and agents
// carry one tool's names to the other (MIK-7987). An alias applies only when
// the canonical key is absent, so an explicit canonical value always wins.
var routerParamAliases = map[string]map[string]string{
	"search_flights": {"from": "origin", "to": "destination", "date": "departure_date"},
	"search_ground":  {"origin": "from", "destination": "to", "departure_date": "date"},
}

// routerRequiredChecked lists targets whose declared Required list matches what
// the handler enforces. Others are skipped: onboard_profile, for one, declares
// phase required but defaults it to 1, so a router-wide check would break it.
var routerRequiredChecked = map[string]bool{"search_flights": true, "search_ground": true}

// applyParamAliases renames alias keys in place and returns "alias -> canonical"
// notes for each rename. An alias the target declares itself is a real
// argument there and is never renamed.
func applyParamAliases(target string, def ToolDef, params map[string]any) []string {
	var renamed []string
	aliases := routerParamAliases[target]
	keys := make([]string, 0, len(aliases))
	for alias := range aliases {
		keys = append(keys, alias)
	}
	sort.Strings(keys)
	for _, alias := range keys {
		canonical := aliases[alias]
		v, ok := params[alias]
		if !ok {
			continue
		}
		if _, declared := def.InputSchema.Properties[alias]; declared {
			continue
		}
		if _, taken := params[canonical]; taken {
			continue
		}
		params[canonical] = v
		delete(params, alias)
		renamed = append(renamed, alias+" -> "+canonical)
	}
	return renamed
}

// unrecognizedParams returns params the target does not declare. They are
// still forwarded: some handlers read undeclared arguments (watch_price reads
// depart_date), so "not declared" must never be reported as "ignored".
func unrecognizedParams(def ToolDef, params map[string]any) []string {
	var out []string
	for k := range params {
		if _, ok := def.InputSchema.Properties[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func missingRequired(def ToolDef, params map[string]any) []string {
	var out []string
	for _, k := range def.InputSchema.Required {
		if v, ok := params[k]; !ok || v == nil || v == "" {
			out = append(out, k)
		}
	}
	return out
}

// describeContract renders a target's full argument contract: every declared
// argument with type and complete description, required ones first.
func describeContract(def ToolDef) string {
	required := make(map[string]bool, len(def.InputSchema.Required))
	for _, k := range def.InputSchema.Required {
		required[k] = true
	}
	names := make([]string, 0, len(def.InputSchema.Properties))
	for k := range def.InputSchema.Properties {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if required[names[i]] != required[names[j]] {
			return required[names[i]]
		}
		return names[i] < names[j]
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%s arguments (pass inside params; required: %s):\n", def.Name, strings.Join(def.InputSchema.Required, ", "))
	for _, k := range names {
		p := def.InputSchema.Properties[k]
		typ := p.Type
		if p.Items != nil {
			typ += " of " + p.Items.Type
		}
		mark := ""
		if required[k] {
			mark = ", required"
		}
		fmt.Fprintf(&b, "- %s (%s%s): %s\n", k, typ, mark, p.Description)
	}
	return b.String()
}

func routerNotes(def ToolDef, renamed, unrecognized []string) string {
	var notes []string
	if len(renamed) > 0 {
		notes = append(notes, "renamed: "+strings.Join(renamed, ", "))
	}
	if len(unrecognized) > 0 {
		described := make([]string, len(unrecognized))
		for i, k := range unrecognized {
			described[i] = k
			if near := closestParam(def, k); near != "" {
				described[i] += " (did you mean " + near + "?)"
			}
		}
		notes = append(notes, "not in the declared schema, forwarded anyway (the tool may still use them): "+strings.Join(described, ", "))
	}
	return strings.Join(notes, "\n")
}

// closestParam returns the declared argument nearest to key, if within edit
// distance 2, so a typo such as departure_dat points at the real name.
func closestParam(def ToolDef, key string) string {
	best, bestDist := "", 3
	for name := range def.InputSchema.Properties {
		if d := models.EditDistance(strings.ToLower(key), name); d < bestDist || (d == bestDist && name < best) {
			best, bestDist = name, d
		}
	}
	if bestDist > 2 {
		return ""
	}
	return best
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
	if target == "trip_workspace" && action != "" {
		if _, ok := params["action"]; !ok {
			params["action"] = action
		}
	}

	handler, ok := s.handlers[target]
	if !ok {
		return nil, nil, fmt.Errorf("resolved travel intent %q to unavailable tool %q", resolvedIntent, target)
	}

	// The router checks params against the target's declared schema when it is
	// known; bare test servers without toolDefs keep the old pass-through.
	def, hasDef := s.toolDefs[target]
	if strings.EqualFold(action, "describe") {
		// describe is discovery only: it must never run the capability, even
		// one registered without a published schema.
		if !hasDef {
			return nil, nil, fmt.Errorf("%s has no published argument list; call it with params, or use query for a natural-language request", target)
		}
		contract := describeContract(def)
		return []ContentBlock{{Type: "text", Text: contract}}, travelSmartResult{
			Intent:       resolvedIntent,
			Action:       action,
			DispatchedTo: target,
			Result:       map[string]any{"input_schema": def.InputSchema},
		}, nil
	}
	renamed := applyParamAliases(target, def, params)
	var unrecognized []string
	if hasDef {
		unrecognized = unrecognizedParams(def, params)
		if routerRequiredChecked[target] {
			if missing := missingRequired(def, params); len(missing) > 0 {
				msg := fmt.Sprintf("%s is missing required argument(s): %s", target, strings.Join(missing, ", "))
				if notes := routerNotes(def, renamed, unrecognized); notes != "" {
					msg += "\n" + notes
				}
				return nil, nil, fmt.Errorf("%s\n%s", msg, describeContract(def))
			}
		}
	}
	notes := routerNotes(def, renamed, unrecognized)

	content, structured, err := handler(ctx, params, elicit, sampling, progress)
	result := travelSmartResult{
		Query:              redactSecretsInText(query),
		Intent:             resolvedIntent,
		Action:             action,
		DispatchedTo:       target,
		Params:             redactSecretParams(params),
		Renamed:            renamed,
		UnrecognizedParams: unrecognized,
		Result:             structured,
	}
	if err != nil {
		// tools/call keeps only the error text on failure, so the notes ride in
		// the error itself or the agent never sees them.
		if notes != "" {
			err = fmt.Errorf("%w\n%s", err, notes)
		}
		return content, result, err
	}
	if notes != "" {
		content = append(content, ContentBlock{Type: "text", Text: notes})
	}
	return content, result, nil
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
	"flight":             "search_flights",
	"flights":            "search_flights",
	"flight_search":      "search_flights",
	"hotel":              "search_accommodations",
	"hotels":             "search_accommodations",
	"hotel_search":       "search_accommodations",
	"accommodation":      "search_accommodations",
	"accommodations":     "search_accommodations",
	"lodging":            "search_accommodations",
	"stay":               "search_accommodations",
	"stays":              "search_accommodations",
	"car":                "search_cars",
	"cars":               "search_cars",
	"rental_car":         "search_cars",
	"rental_cars":        "search_cars",
	"car_rental":         "search_cars",
	"car_rentals":        "search_cars",
	"ground":             "search_ground",
	"ground_transport":   "search_ground",
	"train":              "search_ground",
	"trains":             "search_ground",
	"bus":                "search_ground",
	"buses":              "search_ground",
	"ferry":              "search_ground",
	"ferries":            "search_ground",
	"route":              "search_route",
	"routes":             "search_route",
	"trip":               "plan_trip",
	"trip_workspace":     "trip_workspace",
	"workspace":          "trip_workspace",
	"import_trip":        "trip_workspace",
	"import_reservation": "trip_workspace",
	"optimize_itinerary": "trip_workspace",
	"fare_intelligence":  "trip_workspace",
	"booking_ready":      "trip_workspace",
	"trip_planning":      "plan_trip",
	"plan":               "plan_trip",
	"planner":            "plan_trip",
	"watch":              "list_watches",
	"watches":            "list_watches",
	"price_watch":        "watch_price",
	"price_watches":      "list_watches",
	"alert":              "watch_price",
	"alerts":             "list_watches",
	"preference":         "get_preferences",
	"preferences":        "get_preferences",
	"prefs":              "get_preferences",
	"profile":            "get_preferences",
	"providers":          "provider_health",
	"provider":           "provider_health",
	"nudges":             "travel_nudges",
	"price_drops":        "travel_nudges",
	"deals_for_me":       "travel_nudges",
	"what_should_i_book": "travel_nudges",
	"proactive":          "travel_nudges",
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
	case containsAny(token, "workspace", "import_reservation", "optimize_itinerary", "fare_intelligence", "booking_ready"):
		return "trip_workspace"
	case containsAny(token, "hotel", "hotels", "accommodation", "lodging", "stay", "stays", "room", "rooms", "property"):
		return hotelTarget(token)
	case containsAny(token, "rental_car", "rental_cars", "car_rental", "car_rentals", "hire_car", "hire_cars", "vehicle_rental"):
		return "search_cars"
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
	case containsAny(token, "discovery", "candidate", "candidates", "lead_in"):
		return "search_hotels"
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
		return "search_accommodations"
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
	case containsAny(token, "workspace", "import_reservation", "import_trip", "fare_intelligence", "booking_ready", "save_candidate"):
		return "trip_workspace"
	case containsAny(token, "optimize_itinerary", "map_check"):
		return "trip_workspace"
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
