package trips

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ExportJSON(t Trip) ([]byte, error) {
	t = NormalizeWorkspace(t)
	return json.MarshalIndent(t, "", "  ")
}

func ImportJSON(data []byte) (Trip, error) {
	var t Trip
	if err := json.Unmarshal(data, &t); err != nil {
		return Trip{}, fmt.Errorf("decode trip workspace: %w", err)
	}
	if t.Name == "" {
		return Trip{}, fmt.Errorf("trip name is required")
	}
	return NormalizeWorkspace(t), nil
}

func ExportMarkdown(t Trip) string {
	t = NormalizeWorkspace(t)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", t.Name)
	fmt.Fprintf(&b, "- Status: %s\n", t.Status)
	if len(t.Tags) > 0 {
		fmt.Fprintf(&b, "- Tags: %s\n", strings.Join(t.Tags, ", "))
	}
	if t.Notes != "" {
		fmt.Fprintf(&b, "- Notes: %s\n", t.Notes)
	}
	if len(t.Legs) > 0 {
		b.WriteString("\n## Legs\n\n")
		for _, leg := range t.Legs {
			fmt.Fprintf(&b, "- %s: %s -> %s", printable(leg.Type), leg.From, leg.To)
			if leg.StartTime != "" {
				fmt.Fprintf(&b, " on %s", leg.StartTime)
			}
			if leg.Provider != "" {
				fmt.Fprintf(&b, " via %s", leg.Provider)
			}
			if leg.Price > 0 {
				fmt.Fprintf(&b, " (%.2f %s)", leg.Price, leg.Currency)
			}
			b.WriteByte('\n')
		}
	}
	if len(t.Workspace.Days) > 0 {
		b.WriteString("\n## Day Plan\n\n")
		for _, day := range t.Workspace.Days {
			label := day.Title
			if label == "" {
				label = day.Date
			}
			fmt.Fprintf(&b, "- %s: %d stops", label, len(day.PlaceIDs))
			if day.EstimatedRouteMinutes > 0 {
				fmt.Fprintf(&b, ", %d min route time", day.EstimatedRouteMinutes)
			}
			if len(day.Warnings) > 0 {
				fmt.Fprintf(&b, " (%s)", strings.Join(day.Warnings, "; "))
			}
			b.WriteByte('\n')
		}
	}
	if len(t.Workspace.Candidates) > 0 {
		b.WriteString("\n## Booking Candidates\n\n")
		for _, cand := range t.Workspace.Candidates {
			fmt.Fprintf(&b, "- %s: %s", printable(cand.Type), cand.Title)
			if cand.Provider != "" {
				fmt.Fprintf(&b, " via %s", cand.Provider)
			}
			if cand.Price > 0 {
				fmt.Fprintf(&b, " (%.2f %s)", cand.Price, cand.Currency)
			}
			if cand.URL != "" {
				fmt.Fprintf(&b, " <%s>", cand.URL)
			}
			b.WriteByte('\n')
		}
	}
	if len(t.Workspace.UnresolvedActions) > 0 {
		b.WriteString("\n## Open Actions\n\n")
		for _, action := range t.Workspace.UnresolvedActions {
			fmt.Fprintf(&b, "- [%s] %s\n", action.Status, action.Title)
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func printable(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "item"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
