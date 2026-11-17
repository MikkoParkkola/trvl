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
// replyBudget, applies drop steps until it fits. It finds the fewest steps
// that fit with a galloping then binary search, so a long list costs a
// handful of marshals, and every reply it returns was measured. build is told
// when rows were dropped so its summary can say so. drop returns false at the
// tool's floor; a floor that still does not fit is a short tool error, never
// an oversized reply. Each search probe starts again from a clone of rows, so
// drop may edit the slice it is given and the values in it, but must copy any
// nested slice before changing it.
func fitReply[T any](rows []T, drop func([]T) ([]T, bool), build func(rows []T, trimmed bool) (string, any)) ([]ContentBlock, any, error) {
	type attempt struct {
		content []ContentBlock
		data    any
		size    int
	}
	// try builds the reply after k drop steps and reports how many steps it
	// could take before the floor.
	try := func(k int) (attempt, int, error) {
		cur := slices.Clone(rows)
		for i := 0; i < k; i++ {
			next, ok := drop(cur)
			if !ok {
				k = i
				break
			}
			cur = next
		}
		summary, data := build(cur, k > 0)
		content, err := buildAnnotatedContentBlocks(summary, data)
		if err != nil {
			return attempt{}, k, err
		}
		raw, err := json.Marshal(ToolCallResult{Content: content, StructuredContent: data})
		if err != nil {
			return attempt{}, k, fmt.Errorf("marshal result: %w", err)
		}
		return attempt{content, data, len(raw)}, k, nil
	}

	best, _, err := try(0)
	if err != nil || best.size < replyBudget {
		return best.content, best.data, err
	}
	// lo is a step count known to be over budget, hi the next one to probe.
	lo, hi := 0, 1
	for {
		a, took, err := try(hi)
		if err != nil {
			return nil, nil, err
		}
		if a.size < replyBudget {
			best = a
			break
		}
		if took < hi {
			return nil, nil, fmt.Errorf("the result is %d bytes even at its smallest, over the 50 KB reply budget; narrow the search", a.size)
		}
		lo, hi = hi, hi*2
	}
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		a, _, err := try(mid)
		if err != nil {
			return nil, nil, err
		}
		if a.size < replyBudget {
			best, hi = a, mid
		} else {
			lo = mid
		}
	}
	return best.content, best.data, nil
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
