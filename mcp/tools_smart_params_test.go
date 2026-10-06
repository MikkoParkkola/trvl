package mcp

import (
	"context"
	"encoding/json"
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
