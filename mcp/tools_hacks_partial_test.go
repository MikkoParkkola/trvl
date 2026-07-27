package mcp

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDetectTravelHacks_ReportsPartialSweep pins the honesty half of the
// DetectAll deadline fix at the boundary an agent actually reads.
//
// DetectAll returns what it gathered when the deadline passes. If that arrives
// here as a bare list, the tool answers "count: N" and 100% progress whether N
// was the answer or merely as far as it got, and an agent presents a truncated
// list as complete. The response has to say which.
func TestDetectTravelHacks_ReportsPartialSweep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, result, err := handleDetectTravelHacks(ctx, map[string]any{
		"origin":      "HEL",
		"destination": "BCN",
		"date":        "2026-09-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	var payload struct {
		Complete *bool  `json:"complete"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if payload.Complete == nil {
		t.Fatal("the response omits `complete`; an agent cannot tell a full sweep from a truncated one")
	}
	if *payload.Complete {
		t.Fatal("`complete` was true after a 1ms deadline; a truncated sweep must not be reported as the whole answer")
	}
	if !strings.Contains(payload.Note, "partial") {
		t.Fatalf("expected a note explaining the truncation, got %q", payload.Note)
	}
}

// TestDetectTravelHacks_FlagAndNoteAgree pins the contract that is actually
// environment-independent: whatever `complete` says, the note must agree with it.
//
// An earlier version asserted complete=true for an ordinary search. That premise
// was wrong once a detector cut short by its own allowance began counting against
// completeness — against live providers, one reliably does, so `false` is the
// honest answer and the test was asserting a fiction. What must always hold is
// that the two fields cannot contradict each other.
func TestDetectTravelHacks_FlagAndNoteAgree(t *testing.T) {
	_, result, err := handleDetectTravelHacks(context.Background(), map[string]any{
		"origin":      "HEL",
		"destination": "BCN",
		"date":        "2026-09-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, _ := json.Marshal(result)
	var payload struct {
		Complete *bool  `json:"complete"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if payload.Complete == nil {
		t.Fatal("the response omits `complete`; an agent cannot tell a full sweep from a truncated one")
	}
	if *payload.Complete && payload.Note != "" {
		t.Fatalf("a complete sweep carried a truncation note: %q", payload.Note)
	}
	if !*payload.Complete && !strings.Contains(payload.Note, "partial") {
		t.Fatalf("an incomplete sweep carried no explanation, note was %q", payload.Note)
	}
}

// TestBuildHacksSummary_EmptyPartialDoesNotClaimNoneExist pins that the prose
// agrees with the structured flag.
//
// An empty partial sweep used to read "No travel hacks detected", which states a
// finding the sweep never made: nothing was found because it ran out of time, not
// because the route has no savings. A reader acting on that text draws the wrong
// conclusion, and the structured complete=false beside it does not help anyone
// reading the sentence.
func TestBuildHacksSummary_EmptyPartialDoesNotClaimNoneExist(t *testing.T) {
	partial := buildHacksSummary("HEL", "BCN", "2026-09-01", nil, false)

	if strings.Contains(partial, "No travel hacks detected") {
		t.Fatalf("an unfinished sweep reported as a finding: %q", partial)
	}
	if !strings.Contains(strings.ToLower(partial), "not every detector was confirmed to finish") {
		t.Fatalf("expected the text to say the sweep was cut short, got %q", partial)
	}

	complete := buildHacksSummary("HEL", "BCN", "2026-09-01", nil, true)
	if !strings.Contains(complete, "No travel hacks detected") {
		t.Fatalf("a finished sweep with no results should say so plainly, got %q", complete)
	}
}

// TestBuildHacksSummary_PartialAdviceIsActionable pins a defect found in review of
// the partial-sweep wording. The summary told the caller to "retry with more time",
// which an agent can act on and which often cannot work: the sweep stops at its own
// bounds, 20s per detector and 25s overall, and no request parameter raises either.
// An agent following that advice extends its deadline and gets the same answer.
//
// Advice the reader cannot act on is the same class of defect as the "no hacks
// detected" claim this file already guards: text that reads like a finding and is
// not one.
func TestBuildHacksSummary_PartialAdviceIsActionable(t *testing.T) {
	partial := buildHacksSummary("HEL", "BCN", "2026-09-01", nil, false)

	for _, forbidden := range []string{"more time", "retry with more time"} {
		if strings.Contains(strings.ToLower(partial), forbidden) {
			t.Errorf("the summary promises something the caller cannot supply (%q):\n%s", forbidden, partial)
		}
	}
	// The first version of this test forbade only the unusable advice, and the
	// replacement wording sailed through it while blaming a slow provider, which
	// this code cannot know: a short caller deadline, or a detector doing local
	// work past its allowance, produces the same incompleteness with every provider
	// healthy. An agent reading a fabricated diagnosis would act on it.
	for _, forbidden := range []string{"unreachable", "provider is slow"} {
		if strings.Contains(strings.ToLower(partial), forbidden) {
			t.Errorf("the summary diagnoses a cause it cannot know (%q):\n%s", forbidden, partial)
		}
	}
	// Same reasoning as the CLI: a cancelled sweep has no deadline to blame, and
	// pinning one phrase would lock in wording that was itself slightly wrong.
	if strings.Contains(strings.ToLower(partial), "deadline") {
		t.Errorf("the summary blames a deadline, which need not exist when a sweep is cancelled:\n%s", partial)
	}
	// It still has to say the sweep was cut short, or removing the bad advice
	// would have been achieved by saying nothing.
	for _, want := range []string{"not every detector was confirmed to finish", "not a finding that none exist"} {
		if !strings.Contains(strings.ToLower(partial), want) {
			t.Errorf("the summary should still say the sweep was cut short (missing %q):\n%s", want, partial)
		}
	}
}

// forbiddenSweepCauseClaims lists phrases that assert a cause an incomplete sweep
// does not establish. A sweep ends early on a caller deadline, on a plain
// cancellation with no deadline, on its own overall bound, or on one detector
// overrunning its allowance, and the collector cannot tell which.
//
// Every entry was a claim actually shipped in this file or its CLI counterpart and
// caught in review, which is why the list looks repetitive: "deadline", then "in
// time" once the first was removed, then "timed out". They are the same assertion
// reworded, and each reworded version passed the assertions written for the last
// one. The CLI has its own copy of this list next to its own source, because a test
// should read the file it guards.
func forbiddenSweepCauseClaims() []string {
	return []string{
		"deadline", "timed out", "timeout", "in time", "ran out",
		"unreachable", "provider is slow", "too slow",
		"more time", "narrow the search",
		// These sound factual and are sometimes false. cutShort is read after a
		// detector returns, so one that finished just before its allowance expired
		// is recorded as truncated, and the sweep may in fact have ended after
		// every detector finished. Only "not every detector was confirmed to
		// finish" is supported by the flag.
		//
		// "ended early" is listed separately because it escaped twice, both times
		// in a PROGRESS message: those go out on a path the behavioural tests never
		// call, which is exactly what this literal walk exists to cover.
		"ended before every detector", "ended early",
	}
}

// TestHacksSurface_NoStringClaimsWhyASweepEndedEarly is an invariant over every
// string literal in tools_hacks.go, not a behavioural test, and the distinction is
// deliberate.
//
// Six revisions of this PR each removed one message that claimed a cause the code
// cannot know, and a seventh survived all of them: a PROGRESS message saying
// "Deadline reached", which the summary tests never see because it goes out on a
// different path. Three of those six were found by a reviewer rather than by a
// test, and the last one was missed by a case-sensitive search for "deadline".
//
// Asserting on the summary only guards the path the tests happen to call. This
// walks the file's literals instead, so a new message on any path, including one
// added years from now behind a flag nobody remembers, cannot reintroduce the
// claim. It parses the source rather than grepping it, so comments (which discuss
// these words on purpose) are excluded automatically.
func TestHacksSurface_NoStringClaimsWhyASweepEndedEarly(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "tools_hacks.go", nil, 0)
	if err != nil {
		t.Fatalf("parse tools_hacks.go: %v", err)
	}

	// Each phrase names a cause an incomplete sweep does not establish. A sweep
	// ends early on a caller deadline, on a plain cancellation with no deadline, on
	// its own overall bound, or on one detector overrunning its allowance, and the
	// collector cannot tell which. "timed out" is included because it implies a
	// clock ran out, which cancellation does not.
	forbidden := forbiddenSweepCauseClaims()

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text := strings.ToLower(lit.Value)
		for _, bad := range forbidden {
			if strings.Contains(text, bad) {
				t.Errorf("%s: string claims a cause the code cannot know (%q): %s",
					fset.Position(lit.Pos()), bad, lit.Value)
			}
		}
		return true
	})
}

// TestHacksOutputSchema_DeclaresEveryFieldThePayloadEmits guards the contract
// against the payload, rather than checking for two fields by name.
//
// The schema shipped declaring five properties while the payload carried seven, so
// the completeness signal this whole change exists to provide was missing from the
// document an agent reads to discover what it can rely on. Naming complete and note
// in a test would fix today and not tomorrow: the next field added to the struct
// would drift the same way. Walking the struct tags catches all of them.
//
// It also checks the required list, because for a boolean, absent and false are
// different claims and a caller cannot tell them apart. A completeness flag that
// might not be there is not a completeness flag.
func TestHacksOutputSchema_DeclaresEveryFieldThePayloadEmits(t *testing.T) {
	schema, ok := hacksOutputSchema().(map[string]interface{})
	if !ok {
		t.Fatalf("schema is not an object: %T", hacksOutputSchema())
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema declares no properties")
	}
	required := map[string]bool{}
	if list, ok := schema["required"].([]string); ok {
		for _, name := range list {
			required[name] = true
		}
	}

	_ = props
	_ = required
	assertSchemaCoversType(t, "", schema, reflect.TypeOf(hacksResponse{}))
}

// assertSchemaCoversType walks a Go type and its schema together, recursing into
// nested structs and slice elements.
//
// The first version of this stopped at the top level, and the entire rail-and-fly
// subtree stayed undeclared underneath it: six fields on Hack plus the whole bundle,
// its legs, and the flag that separates a modelled fare from a live quote. It passed
// while missing the most distinctive data the tool returns, which is the same shape
// of mistake as a test asserting on one output path while claims reappear on
// another. Depth is the point.
func assertSchemaCoversType(t *testing.T, path string, schema map[string]interface{}, typ reflect.Type) {
	t.Helper()

	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		items, ok := schema["items"].(map[string]interface{})
		if !ok {
			t.Errorf("%s: schema declares no items for an array", path)
			return
		}
		assertSchemaCoversType(t, path+"[]", items, typ.Elem())
		return
	case reflect.Struct:
		// Fall through to the field walk below.
	default:
		// A leaf. Its type keyword is not what this test is about; drift in field
		// PRESENCE is.
		return
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Errorf("%s: schema declares no properties for %s", path, typ)
		return
	}
	required := map[string]bool{}
	if list, ok := schema["required"].([]string); ok {
		for _, name := range list {
			required[name] = true
		}
	}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" || (tag == "" && !field.Anonymous) {
			continue
		}
		if field.Anonymous && tag == "" {
			// An embedded struct promotes its fields into this object.
			assertSchemaCoversType(t, path, schema, field.Type)
			continue
		}

		parts := strings.Split(tag, ",")
		name := parts[0]
		optional := false
		for _, opt := range parts[1:] {
			if opt == "omitempty" {
				optional = true
			}
		}

		where := path + "." + name
		sub, declared := props[name]
		if !declared {
			t.Errorf("%s: the payload emits this field but the schema does not declare it, so a schema-guided client cannot rely on it", where)
			continue
		}
		// A field always emitted must be required, or a client has to treat its
		// absence as meaningful when it never happens.
		if !optional && !required[name] {
			t.Errorf("%s: always emitted but absent from the schema's required list", where)
		}
		if optional && required[name] {
			t.Errorf("%s: omitted when empty but the schema marks it required", where)
		}

		if subSchema, ok := sub.(map[string]interface{}); ok {
			assertSchemaCoversType(t, where, subSchema, field.Type)
		}
	}
}
