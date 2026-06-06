package nlsearch

import (
	"strings"
	"testing"
	"time"
)

func FuzzHeuristicKeepsStructuredFieldsValid(f *testing.F) {
	for _, seed := range []string{
		"flight from HEL to BCN on 2026-07-01",
		"hotels in Tokyo 7 May 2026",
		"cheap route from Helsinki to Prague next weekend",
		"need a room under 120 EUR",
		"",
		strings.Repeat("A", 256),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, query string) {
		if len(query) > 4096 {
			t.Skip("keep normal test-mode fuzz corpus bounded")
		}
		p := Heuristic(query, "2026-05-13")

		switch p.Intent {
		case "route", "hotel", "flight", "deals":
		default:
			t.Fatalf("unexpected intent %q for query %q", p.Intent, query)
		}
		for name, date := range map[string]string{
			"date":        p.Date,
			"return_date": p.ReturnDate,
			"check_in":    p.CheckIn,
			"check_out":   p.CheckOut,
		} {
			if date == "" {
				continue
			}
			if _, err := time.Parse(time.DateOnly, date); err != nil {
				t.Fatalf("%s = %q is not an ISO date for query %q: %v", name, date, query, err)
			}
		}
		for name, code := range map[string]string{
			"origin":      p.Origin,
			"destination": p.Destination,
		} {
			if code == "" {
				continue
			}
			if len(code) != 3 || code != strings.ToUpper(code) {
				t.Fatalf("%s = %q, want uppercase IATA-like code for query %q", name, code, query)
			}
		}
		if p.MaxBudget < 0 {
			t.Fatalf("MaxBudget = %f, want non-negative for query %q", p.MaxBudget, query)
		}
	})
}
