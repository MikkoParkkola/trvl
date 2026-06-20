package flights

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestSearchWizzair_MapsFlight(t *testing.T) {
	fixture := loadFixture(t, "wizzair_timetable.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		// Version segment must be present in the path (not empty).
		if got := r.URL.Path; got == "" || got == "/" {
			t.Errorf("path missing version segment: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "10.1.0"
	defer func() { wizzHost, wizzVersion = origHost, origVer }()

	out, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("SearchWizzair error: %v", err)
	}
	// Only the 2026-07-07 flight matches the requested date; the 07-08 one is dropped.
	if len(out) != 1 {
		t.Fatalf("want 1 result (date-scoped), got %d", len(out))
	}
	f := out[0]
	if f.Price != 24.99 || f.Currency != "EUR" || f.Provider != "wizzair" || f.Stops != 0 {
		t.Errorf("bad result: %+v", f)
	}
	if len(f.Legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(f.Legs))
	}
	leg := f.Legs[0]
	if leg.AirlineCode != "W6" || leg.Airline != "Wizz Air" || leg.FlightNumber != "W6 2401" {
		t.Errorf("bad leg airline/flight: %+v", leg)
	}
	if leg.DepartureAirport.Code != "BUD" || leg.ArrivalAirport.Code != "BCN" {
		t.Errorf("bad leg airports: %+v", leg)
	}
	if leg.DepartureTime != "2026-07-07T06:15" {
		t.Errorf("departure time = %q, want 2026-07-07T06:15", leg.DepartureTime)
	}
	if f.BookingURL == "" {
		t.Error("booking URL not set")
	}
}

func TestWizzResolvedVersion_EnvOverride(t *testing.T) {
	orig := wizzVersion
	wizzVersion = "10.1.0"
	defer func() { wizzVersion = orig }()

	t.Setenv("WIZZAIR_API_VERSION", "")
	if got := wizzResolvedVersion(); got != "10.1.0" {
		t.Errorf("default version = %q, want 10.1.0", got)
	}
	t.Setenv("WIZZAIR_API_VERSION", "27.5.0")
	if got := wizzResolvedVersion(); got != "27.5.0" {
		t.Errorf("env-override version = %q, want 27.5.0", got)
	}
}

func TestWizzTimetableURL_IncludesVersion(t *testing.T) {
	orig := wizzVersion
	wizzVersion = "10.1.0"
	defer func() { wizzVersion = orig }()
	t.Setenv("WIZZAIR_API_VERSION", "")
	got := wizzTimetableURL()
	want := wizzHost + "/10.1.0/Api/search/timetable"
	if got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
}

func TestWizzairEligibleOptions(t *testing.T) {
	if !wizzairEligibleOptions(SearchOptions{}) {
		t.Error("plain one-way economy should be eligible")
	}
	if wizzairEligibleOptions(SearchOptions{ReturnDate: "2026-07-10"}) {
		t.Error("round-trip should be ineligible")
	}
	if wizzairEligibleOptions(SearchOptions{Alliances: []string{"ONEWORLD"}}) {
		t.Error("alliance filter should be ineligible (Wizz non-aligned)")
	}
	if wizzairEligibleOptions(SearchOptions{Airlines: []string{"BA"}}) {
		t.Error("non-W6 airline filter should be ineligible")
	}
	if !wizzairEligibleOptions(SearchOptions{Airlines: []string{"W6"}}) {
		t.Error("W6 airline filter should be eligible")
	}
}

// TestSearchWizzair_404_VersionRotated proves the version-rotation 404 surfaces
// the typed sentinel (errors.Is) rather than crashing or returning a generic
// failure. The aggregate uses this to render an actionable status.
func TestSearchWizzair_404_VersionRotated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "10.1.0"
	defer func() { wizzHost, wizzVersion = origHost, origVer }()
	t.Setenv("WIZZAIR_API_VERSION", "")

	out, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrWizzVersionRotated) {
		t.Fatalf("error = %v, want errors.Is ErrWizzVersionRotated", err)
	}
	if out != nil {
		t.Errorf("want nil results on failure, got %d", len(out))
	}
	// The tried version must appear in the message (honest, no fabricated current).
	if !strings.Contains(err.Error(), "10.1.0") {
		t.Errorf("error %q should name the tried version 10.1.0", err)
	}
}

// TestWizzairFailureStatus_TypedActionable asserts the helper turns a rotation
// error into an actionable ProviderStatus with the typed FixHintCode and a hint
// naming the env override and last-known-good version. Pure / offline.
func TestWizzairFailureStatus_TypedActionable(t *testing.T) {
	orig := wizzVersion
	wizzVersion = "10.1.0"
	defer func() { wizzVersion = orig }()
	t.Setenv("WIZZAIR_API_VERSION", "")

	rotated := fmt.Errorf("wizzair: tried API version %q: %w", "10.1.0", ErrWizzVersionRotated)
	st := wizzairFailureStatus(rotated)

	if st.ID != "wizzair" || st.Name != "Wizz Air" {
		t.Errorf("bad identity: %+v", st)
	}
	if st.Status == "ok" || st.Status == "" {
		t.Errorf("status = %q, want a failure status", st.Status)
	}
	if st.FixHintCode != "WIZZ_VERSION_ROTATED" {
		t.Errorf("fix_hint_code = %q, want WIZZ_VERSION_ROTATED", st.FixHintCode)
	}
	if !strings.Contains(st.FixHint, "WIZZAIR_API_VERSION") {
		t.Errorf("fix_hint %q should name WIZZAIR_API_VERSION", st.FixHint)
	}
	if !strings.Contains(st.FixHint, "10.1.0") {
		t.Errorf("fix_hint %q should name last-known-good 10.1.0", st.FixHint)
	}
	if st.Error == "" {
		t.Error("error message should be populated")
	}
}

// TestWizzairFailureStatus_GenericError checks a non-rotation error gets no
// version-rotation fix hint (no false actionability).
func TestWizzairFailureStatus_GenericError(t *testing.T) {
	st := wizzairFailureStatus(errors.New("wizzair: decode: boom"))
	if st.FixHintCode != "" {
		t.Errorf("fix_hint_code = %q, want empty for generic error", st.FixHintCode)
	}
	if st.FixHint != "" {
		t.Errorf("fix_hint = %q, want empty for generic error", st.FixHint)
	}
}

// TestSearchWizzair_EnvOverrideRestores proves the WIZZAIR_API_VERSION override
// is the working manual fix: the server 404s every path EXCEPT the override
// version, and the search succeeds only because the override is honored on the
// request path. This is the deterministic proof that the override restores the
// provider after a rotation.
func TestSearchWizzair_EnvOverrideRestores(t *testing.T) {
	const goodVersion = "27.5.0"
	fixture := loadFixture(t, "wizzair_timetable.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/"+goodVersion+"/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "10.1.0" // stale default would 404
	defer func() { wizzHost, wizzVersion = origHost, origVer }()

	// Without the override: stale default 404s -> rotation sentinel.
	t.Setenv("WIZZAIR_API_VERSION", "")
	if _, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1}); !errors.Is(err, ErrWizzVersionRotated) {
		t.Fatalf("stale default: err = %v, want ErrWizzVersionRotated", err)
	}

	// With the override: request path carries the good version -> results.
	t.Setenv("WIZZAIR_API_VERSION", goodVersion)
	out, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("override should restore provider, got err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 result after override, got %d", len(out))
	}
}

// TestSearchWizzair_LiveFixture_Parses feeds a REAL captured Wizz timetable
// response (BUD -> BCN, captured live 2026-06-20 against be.wizzair.com/29.3.0)
// through SearchWizzair to prove the current upstream shape still maps cleanly.
// Note the live timetable omits a flightNumber and prices in the local market
// currency (HUF) — the parser must tolerate both honestly rather than fabricate.
func TestSearchWizzair_LiveFixture_Parses(t *testing.T) {
	fixture := loadFixture(t, "wizzair_timetable_live.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "29.3.0"
	defer func() { wizzHost, wizzVersion = origHost, origVer }()
	t.Setenv("WIZZAIR_API_VERSION", "")

	out, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-08-04", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("SearchWizzair error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 result, got %d", len(out))
	}
	f := out[0]
	if f.Price != 20890.0 {
		t.Errorf("price = %v, want 20890", f.Price)
	}
	if f.Currency != "HUF" { // upstream currency must be preserved, not coerced to the EUR fallback
		t.Errorf("currency = %q, want HUF (preserved from upstream)", f.Currency)
	}
	if f.Provider != "wizzair" || f.Stops != 0 {
		t.Errorf("bad result meta: %+v", f)
	}
	if len(f.Legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(f.Legs))
	}
	leg := f.Legs[0]
	if leg.DepartureAirport.Code != "BUD" || leg.ArrivalAirport.Code != "BCN" {
		t.Errorf("bad leg airports: %+v", leg)
	}
	if leg.AirlineCode != "W6" || leg.Airline != "Wizz Air" {
		t.Errorf("bad leg airline: %+v", leg)
	}
	// First departure on the requested day is selected from departureDates.
	if leg.DepartureTime != "2026-08-04T06:45" {
		t.Errorf("departure time = %q, want 2026-08-04T06:45", leg.DepartureTime)
	}
	if f.BookingURL == "" {
		t.Error("booking URL not set")
	}
}

// TestSearchWizzair_400_InvalidMarket proves a structured Wizz validation refusal
// (HTTP 400 {"validationCodes":["InvalidMarket"]}, captured live 2026-06-20 for a
// route Wizz does not fly) surfaces the typed ErrWizzRejected sentinel and an
// actionable WIZZ_MARKET_REJECTED status — not an opaque "unexpected status 400".
func TestSearchWizzair_400_InvalidMarket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"validationCodes":["InvalidMarket"]}`))
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "29.3.0"
	defer func() { wizzHost, wizzVersion = origHost, origVer }()
	t.Setenv("WIZZAIR_API_VERSION", "")

	out, err := SearchWizzair(context.Background(), "BUD", "JFK", "2026-08-04", "EUR", SearchOptions{Adults: 1})
	if err == nil {
		t.Fatal("want error on 400 validationCodes, got nil")
	}
	if !errors.Is(err, ErrWizzRejected) {
		t.Fatalf("error = %v, want errors.Is ErrWizzRejected", err)
	}
	if out != nil {
		t.Errorf("want nil results on rejection, got %d", len(out))
	}
	if !strings.Contains(err.Error(), "InvalidMarket") {
		t.Errorf("error %q should echo the validationCodes", err)
	}
	st := wizzairFailureStatus(err)
	if st.FixHintCode != "WIZZ_MARKET_REJECTED" {
		t.Errorf("fix_hint_code = %q, want WIZZ_MARKET_REJECTED", st.FixHintCode)
	}
}

// TestSearchWizzair_400_EdgeBlocked proves a non-JSON 4xx (a CloudFront / bot
// wall, the most likely cause of a datacenter/CI-only "status 400") surfaces the
// typed ErrWizzBlocked sentinel and a WIZZ_BLOCKED status, with the offending
// body echoed for diagnosis — instead of the old opaque hard error.
func TestSearchWizzair_400_EdgeBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<html><body>Bad Request (CloudFront)</body></html>"))
	}))
	defer srv.Close()

	origHost, origVer := wizzHost, wizzVersion
	wizzHost = srv.URL
	wizzVersion = "29.3.0"
	defer func() { wizzHost, wizzVersion = origHost, origVer }()
	t.Setenv("WIZZAIR_API_VERSION", "")

	out, err := SearchWizzair(context.Background(), "BUD", "BCN", "2026-08-04", "EUR", SearchOptions{Adults: 1})
	if err == nil {
		t.Fatal("want error on non-JSON 400, got nil")
	}
	if !errors.Is(err, ErrWizzBlocked) {
		t.Fatalf("error = %v, want errors.Is ErrWizzBlocked", err)
	}
	if out != nil {
		t.Errorf("want nil results on block, got %d", len(out))
	}
	if !strings.Contains(err.Error(), "CloudFront") {
		t.Errorf("error %q should echo the edge body snippet", err)
	}
	st := wizzairFailureStatus(err)
	if st.FixHintCode != "WIZZ_BLOCKED" {
		t.Errorf("fix_hint_code = %q, want WIZZ_BLOCKED", st.FixHintCode)
	}
}

// TestClassifyWizzStatus_Table covers the classifier's resolution order in
// isolation (offline, pure): 404 -> rotation, JSON validationCodes -> rejected,
// everything else -> blocked.
func TestClassifyWizzStatus_Table(t *testing.T) {
	t.Setenv("WIZZAIR_API_VERSION", "")
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"404 rotation", http.StatusNotFound, "", ErrWizzVersionRotated},
		{"400 validationCodes", http.StatusBadRequest, `{"validationCodes":["InvalidMarket"]}`, ErrWizzRejected},
		{"400 html edge", http.StatusBadRequest, "<html>Bad Request</html>", ErrWizzBlocked},
		{"403 empty edge", http.StatusForbidden, "", ErrWizzBlocked},
		{"400 json no codes", http.StatusBadRequest, `{"message":"nope"}`, ErrWizzBlocked},
		{"500 edge", http.StatusInternalServerError, "upstream error", ErrWizzBlocked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := classifyWizzStatus(c.status, []byte(c.body))
			if !errors.Is(err, c.want) {
				t.Fatalf("classifyWizzStatus(%d, %q) = %v, want errors.Is %v", c.status, c.body, err, c.want)
			}
		})
	}
}

// TestWizzBodySnippet_Bounds proves the echoed body is whitespace-collapsed and
// length-bounded so an HTML/edge response stays readable on one line.
func TestWizzBodySnippet_Bounds(t *testing.T) {
	if got := wizzBodySnippet([]byte("  a\n\tb   c  ")); got != "a b c" {
		t.Errorf("snippet = %q, want %q", got, "a b c")
	}
	long := strings.Repeat("x", 500)
	got := wizzBodySnippet([]byte(long))
	if len(got) != 203 { // 200 + "..."
		t.Errorf("snippet len = %d, want 203 (200 + ellipsis)", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("long snippet should be truncated with ellipsis, got %q", got[len(got)-5:])
	}
}
