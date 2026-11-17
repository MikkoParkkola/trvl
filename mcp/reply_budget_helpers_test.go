package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The 50 KB promise every list tool keeps on the whole tools/call result.
const replyBudgetPromise = 50_000

// replyBody returns the tool's own structured body, unwrapping the travel
// router's envelope when the call went through it.
func replyBody(t *testing.T, c groundCall) map[string]any {
	t.Helper()
	if inner, ok := c.structured["result"].(map[string]any); ok && c.structured["dispatched_to"] != nil {
		return inner
	}
	return c.structured
}

// rowNames lists the key field of each row in body[list].
func rowNames(body map[string]any, list, key string) []string {
	rows, _ := body[list].([]any)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		m, _ := r.(map[string]any)
		s, _ := m[key].(string)
		out = append(out, s)
	}
	return out
}

func bodyInt(body map[string]any, key string) int {
	f, _ := body[key].(float64)
	return int(f)
}

// assertUnderPromise fails when the whole result passes the 50 KB promise.
func assertUnderPromise(t *testing.T, c groundCall) {
	t.Helper()
	if c.size >= replyBudgetPromise {
		t.Errorf("tools/call result = %d bytes, want under %d", c.size, replyBudgetPromise)
	}
}

// assertTextCopyMatches checks that the assistant JSON copy is the tool's
// whole structured body, so a content-only client sees the same reply,
// through the travel router too.
func assertTextCopyMatches(t *testing.T, c groundCall) {
	t.Helper()
	var text map[string]any
	if err := json.Unmarshal([]byte(c.assistant), &text); err != nil {
		t.Fatalf("assistant copy is not JSON: %v", err)
	}
	if !reflect.DeepEqual(text, replyBody(t, c)) {
		t.Errorf("assistant JSON copy differs from the structured body")
	}
}

// assertBoundedFloorError checks the last resort: a reply that cannot fit even
// at its floor is a short tool error naming the budget, never an oversized
// success.
func assertBoundedFloorError(t *testing.T, c groundCall) {
	t.Helper()
	if !c.isError {
		t.Fatalf("isError = false with a floor over budget (%d bytes); want a bounded tool error", c.size)
	}
	assertUnderPromise(t, c)
	if !strings.Contains(c.errText, "50 KB") {
		t.Errorf("error should name the 50 KB budget: %q", c.errText)
	}
}
