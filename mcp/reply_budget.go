package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
)

// replyBudget bounds the tools/call result of every list tool. The 2 KB under
// the 50 KB promise leaves room for the resource link and the travel router's
// envelope, both added after the handler returns. trvl sizes the reply
// itself: there is no limit knob.
const replyBudget = 48_000

// fitReply builds a list tool's reply from rows and, while the marshalled
// tools/call result (both the JSON text copy and structuredContent) is over
// replyBudget, applies one drop step and measures again. It returns the
// first reply that fits, so no step is skipped: reply size need not shrink
// with every step (a pinned row moving into a summary's top rows can add
// bytes). build is told when rows were dropped so its summary can say so.
// drop returns false at the tool's floor; if no step fits, the result is a
// short tool error, never an oversized reply. rows is cloned first, so drop
// may edit the slice and its values but must copy any nested slice before
// changing it.
func fitReply[T any](rows []T, drop func([]T) ([]T, bool), build func(rows []T, trimmed bool) (string, any)) ([]ContentBlock, any, error) {
	cur := slices.Clone(rows)
	trimmed := false
	for {
		summary, data := build(cur, trimmed)
		content, err := buildAnnotatedContentBlocks(summary, data)
		if err != nil {
			return nil, nil, err
		}
		raw, err := json.Marshal(ToolCallResult{Content: content, StructuredContent: data})
		if err != nil {
			return nil, nil, fmt.Errorf("marshal result: %w", err)
		}
		if len(raw) < replyBudget {
			return content, data, nil
		}
		next, ok := drop(cur)
		if !ok {
			return nil, nil, fmt.Errorf("the result is %d bytes even at its smallest, over the 50 KB reply budget; narrow the search", len(raw))
		}
		cur, trimmed = next, true
	}
}

// dropTail returns a drop step for a ranked list: it removes the last row
// that is not pinned. pinned may be nil.
func dropTail[T any](pinned func(T) bool) func([]T) ([]T, bool) {
	return func(rows []T) ([]T, bool) {
		if len(rows) <= 1 {
			return rows, false
		}
		for i := len(rows) - 1; i >= 0; i-- {
			if pinned == nil || !pinned(rows[i]) {
				return slices.Delete(rows, i, i+1), true
			}
		}
		return rows, false
	}
}

// budgetNote is the summary line for a reply the budget cut: how many rows
// are shown of how many were found, and how to narrow the search.
func budgetNote(shown, total int, noun, narrow string) string {
	note := fmt.Sprintf("\n\nShowing %d of %d %s", shown, total, noun)
	if narrow != "" {
		note += "; narrow with " + narrow
	}
	return note + ". Fewer rows fit the 50 KB result budget."
}

// budgetOmittedSchema documents the budget_omitted field of every list tool.
func budgetOmittedSchema() map[string]interface{} {
	return schemaIntDesc("Rows the 50 KB result budget left out of this response; absent when nothing was cut. The summary says how many were found and how to narrow the search.")
}
