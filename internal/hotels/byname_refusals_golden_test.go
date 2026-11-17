package hotels

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// byNameRefusalsGolden holds the two real by-name refusal sentences, with the
// retry clock normalised. cmd/trvl and mcp read the same file, so the CLI and
// MCP are held to the text SearchHotelByName actually returns (#813).
const byNameRefusalsGolden = "testdata/byname_refusals.golden"

var retryClock = regexp.MustCompile(`retry after [^)]*\)`)

// realByNameRefusals drives SearchHotelByName through a stubbed connection:
// Booking finds the hotel without a Google ID while Google refuses, once with
// a cooldown already armed and once with a plain 403.
func realByNameRefusals(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, tc := range map[string]struct {
		search http.HandlerFunc
		prime  bool
	}{
		"cooldown": {noSacherPage, true},
		"blocked":  {answer(http.StatusForbidden), false},
	} {
		t.Run(name, func(t *testing.T) {
			roomLookupWorld(t, googleRoutes{shortEntity, answer(404), tc.search}, tc.prime)
			withBookingResult(t, sacherBookingUSD)
			seedGeoAt(t, "Hotel Sacher Wien", sacherLat, sacherLon)
			_, err := SearchHotelByName(context.Background(), "Hotel Sacher Wien", lookupCheckIn, lookupCheckOut, "USD")
			if err == nil {
				t.Fatal("want a refusal error")
			}
			out[name] = retryClock.ReplaceAllString(err.Error(), "retry after <clock>)")
		})
	}
	return out
}

// TestByNameRefusals_Golden: the real sentences match the golden file the CLI
// and MCP tests read. A wording change fails here first.
func TestByNameRefusals_Golden(t *testing.T) {
	got := realByNameRefusals(t)
	raw, err := os.ReadFile(byNameRefusalsGolden)
	if err != nil {
		t.Fatalf("read golden: %v\nreal sentences:\ncooldown: %s\nblocked: %s", err, got["cooldown"], got["blocked"])
	}
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(raw), "\r", "")), "\n") {
		name, want, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("malformed golden line %q", line)
		}
		seen[name]++
		if got[name] != want {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], want)
		}
	}
	// The CLI and MCP tests check whatever lines the file holds, so a lost or
	// doubled line would silently change their coverage.
	if len(seen) != 2 || seen["cooldown"] != 1 || seen["blocked"] != 1 {
		t.Errorf("golden cases = %v, want exactly one cooldown and one blocked", seen)
	}
}
