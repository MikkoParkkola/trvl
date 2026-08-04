package route

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// captureLogs installs a raw JSON handler at Debug level as the default logger.
//
// It deliberately performs no scrubbing of its own: the assertion must fail
// when a call site logs a secret, not pass because the handler cleaned up after
// it. A scrubbing handler in the record path could not detect this defect.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// assertAbsentFromRecords fails if any sentinel appears in any emitted record,
// checking decoded attribute values as well as the raw stream so a JSON-escaped
// leak still trips.
func assertAbsentFromRecords(t *testing.T, buf *bytes.Buffer, sentinels ...string) {
	t.Helper()
	raw := buf.String()
	if raw == "" {
		t.Fatal("no log records were emitted; the test did not exercise the logging path")
	}
	// Matching is case-insensitive on purpose. City names are normalised to
	// title case before they are used, so a case-sensitive comparison would
	// silently pass on exactly the attributes this test exists to police.
	lowerRaw := strings.ToLower(raw)
	for _, s := range sentinels {
		if strings.Contains(lowerRaw, strings.ToLower(s)) {
			t.Errorf("sentinel %q reached a log record", s)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		for k, v := range rec {
			s, ok := v.(string)
			if !ok {
				continue
			}
			lowerVal := strings.ToLower(s)
			for _, sentinel := range sentinels {
				if strings.Contains(lowerVal, strings.ToLower(sentinel)) {
					t.Errorf("sentinel %q reached log attribute %q", sentinel, k)
				}
			}
			if strings.Contains(s, "://") {
				t.Errorf("full URL reached log attribute %q: %q", k, s)
			}
		}
	}
}

// TestRouteSearchLogsNoJourneyOrURL drives the real SearchRoute path with both
// upstream searches failing, so every logging branch under test runs:
//
//   - the entry line, which used to carry origin, destination and travel date;
//   - the two failure lines, which used to carry the airport/city pair and an
//     error whose text embeds the upstream request URL.
//
// The journey tuple is what issue #531 names as personal data, and the URL is
// what net/http embeds in *url.Error. Neither may reach a record.
func TestRouteSearchLogsNoJourneyOrURL(t *testing.T) {
	const (
		origin   = "SENTINELORIGINCITY"
		dest     = "SENTINELDESTCITY"
		date     = "2031-04-17"
		urlToken = "SENTINEL-ROUTE-TOKEN"
	)
	failURL := errors.New(`Get "https://upstream.example/search?token=` + urlToken + `": dial tcp: refused`)

	withSearchMocks(t,
		func(context.Context, string, string, string, flights.SearchOptions) (*models.FlightSearchResult, error) {
			return nil, failURL
		},
		func(context.Context, string, string, string, ground.SearchOptions) (*models.GroundSearchResult, error) {
			return nil, failURL
		},
		nil,
	)

	buf := captureLogs(t)
	_, _ = SearchRoute(context.Background(), origin, dest, date, Options{})
	assertAbsentFromRecords(t, buf, origin, dest, date, urlToken)
}
