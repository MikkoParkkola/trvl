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

// replyFirstCut is how many rows a reply over budget drops to in one step,
// before dropping one row at a time, so a 300-row list is not re-marshalled
// 300 times. It only applies once a reply is over budget.
const replyFirstCut = 40

// fitReply builds a list tool's reply from rows and, while the marshalled
// tools/call result (both the JSON text copy and structuredContent) is over
// replyBudget, drops rows and builds again. build is told once rows have been
// dropped so its summary can say so. drop returns false at the tool's floor;
// a floor that still does not fit is a short tool error, never an oversized
// reply.
func fitReply[T any](rows []T, drop func([]T) ([]T, bool), build func(rows []T, trimmed bool) (string, any)) ([]ContentBlock, any, error) {
	rows = slices.Clone(rows)
	trimmed := false
	for {
		summary, data := build(rows, trimmed)
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
		var ok bool
		if rows, ok = drop(rows); !ok {
			return nil, nil, fmt.Errorf("the result is %d bytes even at its smallest, over the 50 KB reply budget; narrow the search", len(raw))
		}
		trimmed = true
	}
}

// dropTail returns a drop step for a ranked list: it removes rows from the
// end, skipping pinned ones, down to replyFirstCut on the first step and one
// row at a time after that. pinned may be nil.
func dropTail[T any](pinned func(T) bool) func([]T) ([]T, bool) {
	return func(rows []T) ([]T, bool) {
		if len(rows) <= 1 {
			return rows, false
		}
		target := min(len(rows)-1, replyFirstCut)
		removed := false
		for i := len(rows) - 1; i >= 0 && len(rows) > target; i-- {
			if pinned != nil && pinned(rows[i]) {
				continue
			}
			rows = slices.Delete(rows, i, i+1)
			removed = true
		}
		return rows, removed
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
