package hotels

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

// captureLogs installs a raw JSON handler at Debug level as the default logger.
// It scrubs nothing itself: a scrubbing handler in the record path could not
// detect the defect this file is named for.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func assertAbsentFromRecords(t *testing.T, buf *bytes.Buffer, sentinels ...string) {
	t.Helper()
	raw := buf.String()
	if raw == "" {
		t.Fatal("no log records were emitted; the test did not exercise the logging path")
	}
	for _, s := range sentinels {
		if s == "" {
			continue
		}
		if strings.Contains(raw, s) {
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
			for _, sentinel := range sentinels {
				if sentinel != "" && strings.Contains(s, sentinel) {
					t.Errorf("sentinel %q reached log attribute %q", sentinel, k)
				}
			}
			if strings.Contains(s, "://") {
				t.Errorf("full URL reached log attribute %q: %q", k, s)
			}
		}
	}
}

// TestBluegroundDetailHopFailureLogsNoURLOrPath drives the real two-hop search
// with the detail hop broken at the transport layer, so net/http returns a
// *url.Error whose text embeds the full request URL. That error, and the
// listing path that produced it, are both logged on the failure branch.
//
// The upstream path identifies the exact apartment a user is being shown, and
// the URL carries the query string, so neither may reach a record verbatim.
func TestBluegroundDetailHopFailureLogsNoURLOrPath(t *testing.T) {
	listBody, err := os.ReadFile("testdata/blueground_list_athens.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/p/") {
			// Break the connection so the detail hop fails with a *url.Error
			// rather than a status code: the URL only appears in the error
			// text on the transport-failure path.
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, hjErr := hj.Hijack(); hjErr == nil {
					_ = conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(listBody)
	}))
	defer srv.Close()

	prevEnabled, prevURL, prevClient := bluegroundEnabled, bluegroundBaseURL, bluegroundClient
	bluegroundEnabled, bluegroundBaseURL, bluegroundClient = true, srv.URL, srv.Client()
	defer func() {
		bluegroundEnabled, bluegroundBaseURL, bluegroundClient = prevEnabled, prevURL, prevClient
	}()

	// Confirm the fixture actually yields a property path; without one the
	// detail hop never runs and the test would pass vacuously.
	props, err := parseBluegroundList(listBody)
	if err != nil || len(props) == 0 {
		t.Fatalf("fixture yielded no properties: %v", err)
	}
	detailPath := strings.TrimSpace(props[0].Path)
	if detailPath == "" {
		t.Fatal("fixture property has no path")
	}

	buf := captureLogs(t)
	if _, err := SearchBlueground(context.Background(), "Athens, Greece", HotelSearchOptions{}); err != nil {
		t.Fatalf("search: %v", err)
	}
	assertAbsentFromRecords(t, buf, detailPath, srv.URL)
}

// assertNoAttrKeys fails if any emitted record carries one of the named
// attribute keys. Sentinel matching alone cannot police a numeric attribute
// such as a guest count, and an absent key is the property the fix establishes.
func assertNoAttrKeys(t *testing.T, buf *bytes.Buffer, keys ...string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		for _, k := range keys {
			if _, present := rec[k]; present {
				t.Errorf("attribute %q reached a log record: %v", k, rec[k])
			}
		}
	}
}

// trivagoStubTransport answers the three MCP hops SearchTrivago makes without
// leaving the process: initialize, tools/list and tools/call. It keys off the
// JSON-RPC method in the request body rather than the path, because all three
// hops post to the same endpoint.
type trivagoStubTransport struct{}

func (trivagoStubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &probe)

	res := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}
	switch probe.Method {
	case "initialize":
		res.Header.Set("Mcp-Session-Id", "stub-session")
		res.Body = io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	case "tools/list":
		res.Body = io.NopCloser(strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"` + trivagoDefaultSearchTool + `"}]}}`))
	default:
		res.Body = io.NopCloser(strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"accommodations":` +
				`[{"accommodation_name":"Stub Hotel","currency":"USD","price_per_night":"100"}]}}}`))
	}
	return res, nil
}

// TestTrivagoSearchLogsNoJourney drives the real SearchTrivago path against a
// stubbed transport so both of its progress lines are emitted. Location,
// check-in, check-out and guest count together are the journey itself, the
// personal data issue #531 names; only the resolved tool name and the result
// count may remain.
func TestTrivagoSearchLogsNoJourney(t *testing.T) {
	const (
		location = "SENTINELTRIVAGOCITY"
		checkIn  = "2031-05-11"
		checkOut = "2031-05-18"
	)

	prevClient := trivagoHTTPClient
	trivagoHTTPClient = &http.Client{Transport: trivagoStubTransport{}}
	prevLimiter := trivagoLimiter
	trivagoLimiter = rate.NewLimiter(rate.Inf, 1)
	prevEnabled := trivagoEnabled
	trivagoEnabled = true
	t.Cleanup(func() {
		trivagoHTTPClient = prevClient
		trivagoLimiter = prevLimiter
		trivagoEnabled = prevEnabled
	})

	buf := captureLogs(t)
	if _, err := SearchTrivago(context.Background(), location, HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   7,
		Currency: "EUR",
	}); err != nil {
		t.Fatalf("SearchTrivago: %v", err)
	}

	assertAbsentFromRecords(t, buf, location, checkIn, checkOut)
	assertNoAttrKeys(t, buf, "location", "arrival", "departure", "guests", "query")
}
