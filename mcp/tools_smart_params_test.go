package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// MIK-7987: a real agent guessed travel params eight times in a row because the
// router forwarded them unchecked. These tests pin the router's argument
// contract: cross-tool aliases, required-field errors that teach the whole
// schema, and notes that survive the tools/call error path.

// stubTarget replaces one handler on a fully built server and records the
// params it receives.
func stubTarget(t *testing.T, s *Server, name string) *map[string]any {
	t.Helper()
	var got map[string]any
	s.handlers[name] = func(_ context.Context, args map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
		got = args
		return []ContentBlock{{Type: "text", Text: "stub result"}}, map[string]any{"ok": true}, nil
	}
	return &got
}

// callTravel sends a real tools/call for the travel tool and returns the
// serialized result, so assertions see exactly what an MCP client sees.
func callTravel(t *testing.T, s *Server, args map[string]any) (text string, structured map[string]any, isError bool) {
	t.Helper()
	resp := s.HandleRequest(jsonRequest(t, "tools/call", 1, map[string]any{"name": "travel", "arguments": args}))
	if resp == nil {
		t.Fatal("nil response")
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Content           []ContentBlock `json:"content"`
		StructuredContent map[string]any `json:"structuredContent"`
		IsError           bool           `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
		b.WriteString("\n")
	}
	return b.String(), res.StructuredContent, res.IsError
}

func TestTravelRenamesCrossToolFlightAliases(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "search_flights")

	text, _, isErr := callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"from": "HEL", "to": "CDG", "date": "2026-11-01"},
	})
	if isErr {
		t.Fatalf("aliased flight call failed: %s", text)
	}
	if (*got)["departure_date"] != "2026-11-01" || (*got)["origin"] != "HEL" || (*got)["destination"] != "CDG" {
		t.Fatalf("handler params = %#v, want origin/destination/departure_date", *got)
	}
	if _, left := (*got)["date"]; left {
		t.Fatalf("alias key date should be moved, not duplicated: %#v", *got)
	}
	if !strings.Contains(text, "date -> departure_date") {
		t.Fatalf("rename must be reported in text content, got %q", text)
	}
}

func TestTravelRenamesCrossToolGroundAliases(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "search_ground")

	text, _, isErr := callTravel(t, s, map[string]any{
		"intent": "search_ground",
		"params": map[string]any{"origin": "Torino", "destination": "Lyon", "departure_date": "2026-10-23"},
	})
	if isErr {
		t.Fatalf("aliased ground call failed: %s", text)
	}
	if (*got)["from"] != "Torino" || (*got)["to"] != "Lyon" || (*got)["date"] != "2026-10-23" {
		t.Fatalf("handler params = %#v, want from/to/date", *got)
	}
}

func TestTravelCanonicalKeyWinsOverAlias(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "search_flights")

	callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"destination": "CDG", "departure_date": "2026-11-01", "date": "2026-12-24"},
	})
	if (*got)["departure_date"] != "2026-11-01" {
		t.Fatalf("canonical departure_date must win, got %#v", *got)
	}
}

func TestTravelMissingRequiredTeachesFullContract(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "search_flights")

	text, _, isErr := callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"destination": "CDG", "when": "2026-11-01"},
	})
	if !isErr {
		t.Fatalf("missing departure_date must be an error, got %q", text)
	}
	if *got != nil {
		t.Fatalf("handler must not run when a required field is missing")
	}
	for _, want := range []string{"departure_date", "when", "YYYY-MM-DD", "cabin_class", "required"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error text must contain %q so one failure teaches the contract; got %q", want, text)
		}
	}
}

func TestTravelUnrecognizedParamIsForwardedAndReported(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "search_flights")

	text, structured, isErr := callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"destination": "CDG", "departure_date": "2026-11-01", "nonstop_only": true},
	})
	if isErr {
		t.Fatalf("an unrecognized param must not fail a valid call: %s", text)
	}
	if (*got)["nonstop_only"] != true {
		t.Fatalf("unrecognized params stay forwarded; handler got %#v", *got)
	}
	if !strings.Contains(text, "nonstop_only") {
		t.Fatalf("text content must name the unrecognized param, got %q", text)
	}
	if _, ok := structured["unrecognized_params"]; !ok {
		t.Fatalf("structured output must carry unrecognized_params, got %#v", structured)
	}
}

func TestTravelDescribeReturnsSchemaWithoutDispatch(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "search_ground")

	text, _, isErr := callTravel(t, s, map[string]any{"intent": "search_ground", "action": "describe"})
	if isErr {
		t.Fatalf("describe failed: %s", text)
	}
	if *got != nil {
		t.Fatalf("describe must not run the search")
	}
	for _, want := range []string{"from", "to", "date", "required"} {
		if !strings.Contains(text, want) {
			t.Fatalf("describe output must contain %q, got %q", want, text)
		}
	}
}

func TestTravelDescribeNeverDispatches(t *testing.T) {
	// plan_journey has a handler but no published ToolDef; describe must
	// report that rather than fall through and run the capability.
	s := NewServer()
	got := stubTarget(t, s, "plan_journey")
	text, _, isErr := callTravel(t, s, map[string]any{"intent": "plan_journey", "action": "describe"})
	if *got != nil {
		t.Fatalf("describe must never run the capability")
	}
	if !isErr || !strings.Contains(text, "plan_journey") {
		t.Fatalf("describe without a published schema must say so, got %q", text)
	}
}

func TestTravelRequiredCheckLeavesOtherToolsAlone(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "onboard_profile")

	// onboard_profile declares phase required but defaults it to 1; the
	// router check is scoped to the two search tools so this keeps working.
	text, _, isErr := callTravel(t, s, map[string]any{"intent": "onboard_profile"})
	if isErr || *got == nil {
		t.Fatalf("onboard_profile without phase must still dispatch: %s", text)
	}
}

func TestTravelForwardsUndeclaredWatchDate(t *testing.T) {
	s := NewServer()
	got := stubTarget(t, s, "watch_price")

	callTravel(t, s, map[string]any{
		"intent": "watch_price",
		"params": map[string]any{"type": "flight", "origin": "HEL", "destination": "CDG", "depart_date": "2026-11-01"},
	})
	if *got == nil || (*got)["depart_date"] != "2026-11-01" {
		t.Fatalf("watch_price depart_date must be forwarded unchanged, got %#v", *got)
	}
}

func TestAirportListUnknownCityGivesHonestError(t *testing.T) {
	_, err := validateAirportList("Torino")
	if err == nil {
		t.Fatal("Torino is not in the airport table; expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Torino") || !strings.Contains(msg, "airport list") || !strings.Contains(msg, "IATA") {
		t.Fatalf("error must name the city, say it is not in trvl's airport list, and ask for an IATA code; got %q", msg)
	}
	if strings.Contains(msg, "TORINO") {
		t.Fatalf("error must not echo an upper-cased fake code; got %q", msg)
	}
}

func TestAirportListKeepsListsAndKnownCities(t *testing.T) {
	for in, want := range map[string]string{
		"ORY,BVA,CDG": "ORY,BVA,CDG",
		"HEL":         "HEL",
		"Helsinki":    "HEL",
	} {
		got, err := validateAirportList(in)
		if err != nil || got != want {
			t.Fatalf("validateAirportList(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := validateAirportList("HEL,Torino"); err == nil || !strings.Contains(err.Error(), "Torino") {
		t.Fatalf("an unknown city inside a list must be named in the error, got %v", err)
	}
}

func TestTravelDescriptionCarriesSearchSignatures(t *testing.T) {
	desc := travelTool().Description
	for _, want := range []string{"departure_date", "destination", "search_ground", "from", "describe"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("travel description must carry %q so a cold agent needs no discovery call", want)
		}
	}
}

// travelToolBaselineBytes is the serialized size of the travel tool before
// MIK-7987. The router is advertised alone to keep tools/list small; the
// signatures may add at most 600 bytes (about 150 tokens).
const travelToolBaselineBytes = 1486

func TestTravelToolStaysCompact(t *testing.T) {
	raw, err := json.Marshal(travelTool())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("travel tool serializes to %d bytes", len(raw))
	if len(raw) > travelToolBaselineBytes+600 {
		t.Fatalf("travel tool serializes to %d bytes; budget is %d", len(raw), travelToolBaselineBytes+600)
	}
}

// assertFullContract checks text carries every declared argument of def with
// its type and complete description, and every required name.
func assertFullContract(t *testing.T, text string, def ToolDef) {
	t.Helper()
	for name, p := range def.InputSchema.Properties {
		if !strings.Contains(text, "- "+name+" ("+p.Type) || !strings.Contains(text, p.Description) {
			t.Fatalf("contract for %s lacks argument %q with type and full description", def.Name, name)
		}
	}
	for _, req := range def.InputSchema.Required {
		if !strings.Contains(text, req+" ("+def.InputSchema.Properties[req].Type) || !strings.Contains(text, ", required)") {
			t.Fatalf("contract for %s does not mark %q as required", def.Name, req)
		}
	}
}

func TestTravelContractIsCompleteForBothSearches(t *testing.T) {
	for _, target := range []string{"search_flights", "search_ground"} {
		t.Run(target, func(t *testing.T) {
			s := NewServer()
			got := stubTarget(t, s, target)
			def := s.toolDefs[target]

			text, _, isErr := callTravel(t, s, map[string]any{"intent": target, "action": "describe"})
			if isErr || *got != nil {
				t.Fatalf("describe must succeed without dispatch: %s", text)
			}
			assertFullContract(t, text, def)

			// Every required field missing at once: all are named, none dispatched.
			text, _, isErr = callTravel(t, s, map[string]any{"intent": target, "params": map[string]any{"currency": "EUR"}})
			if !isErr || *got != nil {
				t.Fatalf("missing required fields must fail without dispatch: %s", text)
			}
			missingLine := strings.SplitN(text, "\n", 2)[0]
			for _, req := range def.InputSchema.Required {
				if !strings.Contains(missingLine, req) {
					t.Fatalf("missing-field line must name %q, got %q", req, missingLine)
				}
			}
			assertFullContract(t, text, def)
		})
	}
}

func TestTravelAliasesTable(t *testing.T) {
	for target, aliases := range routerParamAliases {
		for alias, canonical := range aliases {
			t.Run(target+"/"+alias, func(t *testing.T) {
				s := NewServer()
				got := stubTarget(t, s, target)
				params := map[string]any{alias: "X"}
				for _, req := range s.toolDefs[target].InputSchema.Required {
					if req != canonical {
						params[req] = "2026-11-01"
					}
				}
				text, structured, isErr := callTravel(t, s, map[string]any{"intent": target, "params": params})
				if isErr {
					t.Fatalf("aliased call failed: %s", text)
				}
				if (*got)[canonical] != "X" {
					t.Fatalf("%s must reach the handler as %s; got %#v", alias, canonical, *got)
				}
				note := alias + " -> " + canonical
				if !strings.Contains(text, note) {
					t.Fatalf("text must report %q, got %q", note, text)
				}
				renamed, _ := structured["renamed"].([]any)
				if len(renamed) != 1 || renamed[0] != note {
					t.Fatalf("structured renamed = %#v, want [%q]", structured["renamed"], note)
				}
			})
		}
	}
}

func TestTravelAliasNeverShadowsADeclaredArgument(t *testing.T) {
	// If a target ever declares an alias name itself, the router must leave
	// that argument alone rather than rename it away.
	s := NewServer()
	got := stubTarget(t, s, "search_flights")
	def := s.toolDefs["search_flights"]
	props := make(map[string]Property, len(def.InputSchema.Properties)+1)
	for k, v := range def.InputSchema.Properties {
		props[k] = v
	}
	props["from"] = Property{Type: "string", Description: "declared for this test"}
	def.InputSchema.Properties = props
	s.toolDefs["search_flights"] = def

	callTravel(t, s, map[string]any{"intent": "search_flights", "params": map[string]any{"from": "A", "destination": "CDG", "departure_date": "2026-11-01"}})
	if (*got)["from"] != "A" {
		t.Fatalf("declared from must be forwarded untouched, got %#v", *got)
	}
	if _, renamed := (*got)["origin"]; renamed {
		t.Fatalf("declared from must not be renamed to origin, got %#v", *got)
	}
}

func TestTravelUnrecognizedParamSuggestsAndExplains(t *testing.T) {
	s := NewServer()
	stubTarget(t, s, "search_flights")

	text, structured, isErr := callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"destination": "CDG", "departure_date": "2026-11-01", "cabin_clas": "business"},
	})
	if isErr {
		t.Fatalf("typo in an optional arg must not fail the call: %s", text)
	}
	if !strings.Contains(text, "cabin_clas (did you mean cabin_class?)") || !strings.Contains(text, "forwarded") {
		t.Fatalf("text must suggest cabin_class and say the key was forwarded, got %q", text)
	}
	un, _ := structured["unrecognized_params"].([]any)
	if len(un) != 1 || un[0] != "cabin_clas" {
		t.Fatalf("structured unrecognized_params = %#v, want [cabin_clas]", structured["unrecognized_params"])
	}
}

func TestTravelNotesSurviveAFailingHandler(t *testing.T) {
	s := NewServer()
	s.handlers["search_flights"] = func(_ context.Context, _ map[string]any, _ ElicitFunc, _ SamplingFunc, _ ProgressFunc) ([]ContentBlock, interface{}, error) {
		return nil, nil, errors.New("upstream exploded")
	}
	text, _, isErr := callTravel(t, s, map[string]any{
		"intent": "search_flights",
		"params": map[string]any{"destination": "CDG", "date": "2026-11-01", "cabin_clas": "business"},
	})
	if !isErr {
		t.Fatal("handler error must surface as a tool error")
	}
	for _, want := range []string{"upstream exploded", "date -> departure_date", "cabin_clas (did you mean cabin_class?)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("serialized error must contain %q, got %q", want, text)
		}
	}
}

func TestTravelDescriptionSignaturesAreAssociated(t *testing.T) {
	desc := travelTool().Description
	for _, want := range []string{
		"search_flights needs destination and departure_date",
		"search_ground needs from, to and date",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("travel description must say %q", want)
		}
	}
}

func TestCityDiagnosticsSuggestAndStayTruthful(t *testing.T) {
	_, err := validateAirportList("Helsinkii")
	if err == nil || !strings.Contains(err.Error(), "helsinki") {
		t.Fatalf("a misspelled known city must suggest it, got %v", err)
	}
	_, err = validateAirportList("CDG,Torino")
	if err == nil {
		t.Fatal("an unresolvable city inside a list must fail")
	}
	if msg := err.Error(); !strings.Contains(msg, `"Torino"`) || !strings.Contains(msg, "airport list") || !strings.Contains(msg, "IATA") {
		t.Fatalf("list token must get the full diagnostic, got %q", msg)
	}
	for _, name := range []string{"origin", "destination"} {
		d := searchFlightsTool().InputSchema.Properties[name].Description
		if !strings.Contains(d, "airport list") || strings.Contains(d, "City names resolve") {
			t.Fatalf("search_flights %s description must state the known-city limit, got %q", name, d)
		}
	}
}
