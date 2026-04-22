package afklm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestNewAwardScanner_NoCookies verifies ErrNoAwardCookies is returned when
// no cookies are configured.
func TestNewAwardScanner_NoCookies(t *testing.T) {
	t.Setenv("AFKL_KLM_COOKIES", "")
	_, err := NewAwardScanner(AwardScannerOptions{})
	if err != ErrNoAwardCookies {
		t.Fatalf("expected ErrNoAwardCookies, got %v", err)
	}
}

// TestNewAwardScanner_EnvCookies verifies cookies resolved from env var.
func TestNewAwardScanner_EnvCookies(t *testing.T) {
	t.Setenv("AFKL_KLM_COOKIES", "session=abc")
	scanner, err := NewAwardScanner(AwardScannerOptions{BaseURL: "http://localhost"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scanner == nil {
		t.Fatal("expected non-nil scanner")
	}
}

// TestAwardScanner_Search_HTTPServer verifies the full request/response flow
// against a mock HTTP server returning a fixture-shaped response.
func TestAwardScanner_Search_HTTPServer(t *testing.T) {
	// Serve a representative award response.
	fixture, err := os.ReadFile("testdata/award_prg_ams.json")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method and headers.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		cookie := r.Header.Get("Cookie")
		if cookie == "" {
			t.Error("expected Cookie header, got empty")
		}

		// Verify request body contains REWARD bookingFlow.
		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if flow, ok := reqBody["bookingFlow"].(string); !ok || flow != "REWARD" {
			t.Errorf("expected bookingFlow=REWARD, got %v", reqBody["bookingFlow"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	scanner, err := NewAwardScanner(AwardScannerOptions{
		Cookies:    "session=test",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAwardScanner: %v", err)
	}

	offers, err := scanner.Search(context.Background(), "PRG", "AMS", "2026-06-15")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(offers) == 0 {
		t.Fatal("expected at least one offer, got none")
	}

	// Verify first offer.
	first := offers[0]
	if first.Origin != "PRG" {
		t.Errorf("Origin = %q, want PRG", first.Origin)
	}
	if first.Destination != "AMS" {
		t.Errorf("Destination = %q, want AMS", first.Destination)
	}
	if first.Miles <= 0 {
		t.Errorf("Miles = %d, want > 0", first.Miles)
	}
	if first.Date != "2026-06-15" {
		t.Errorf("Date = %q, want 2026-06-15", first.Date)
	}
	if !first.Available {
		t.Error("expected Available=true")
	}
}

// TestAwardScanner_Search_APIError verifies non-200 responses return an error.
func TestAwardScanner_Search_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":[{"code":2000,"description":"Reward not allowed"}]}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	scanner, err := NewAwardScanner(AwardScannerOptions{
		Cookies:    "session=test",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAwardScanner: %v", err)
	}

	_, err = scanner.Search(context.Background(), "PRG", "AMS", "2026-06-15")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
}

// TestAwardOffer_Suitable verifies miles ceiling logic.
func TestAwardOffer_Suitable(t *testing.T) {
	tests := []struct {
		miles     int
		available bool
		suitable  bool
		ideal     bool
	}{
		{7500, true, true, true},
		{10000, true, true, true},
		{12500, true, true, false},
		{15000, true, true, false},
		{15001, true, false, false},
		{0, true, false, false},
		{7500, false, false, false},
	}

	for _, tc := range tests {
		o := AwardOffer{Miles: tc.miles, Available: tc.available}
		if got := o.Suitable(); got != tc.suitable {
			t.Errorf("miles=%d available=%v: Suitable()=%v, want %v", tc.miles, tc.available, got, tc.suitable)
		}
		if got := o.Ideal(); got != tc.ideal {
			t.Errorf("miles=%d available=%v: Ideal()=%v, want %v", tc.miles, tc.available, got, tc.ideal)
		}
	}
}

// TestDateRange verifies date range generation.
func TestDateRange(t *testing.T) {
	dates := DateRange("2026-06-01", "2026-06-03")
	if len(dates) != 3 {
		t.Fatalf("len=%d, want 3", len(dates))
	}
	want := []string{"2026-06-01", "2026-06-02", "2026-06-03"}
	for i, d := range dates {
		if d != want[i] {
			t.Errorf("dates[%d]=%q, want %q", i, d, want[i])
		}
	}
}

// TestDateRange_Empty verifies that invalid inputs return nil.
func TestDateRange_Empty(t *testing.T) {
	if DateRange("bad", "2026-06-03") != nil {
		t.Error("expected nil for bad start date")
	}
	if DateRange("2026-06-03", "bad") != nil {
		t.Error("expected nil for bad end date")
	}
}

// TestMonthDateRange verifies full-month generation for June 2026.
func TestMonthDateRange(t *testing.T) {
	dates := MonthDateRange("2026-06")
	if len(dates) != 30 {
		t.Fatalf("len=%d, want 30", len(dates))
	}
	if dates[0] != "2026-06-01" {
		t.Errorf("first=%q, want 2026-06-01", dates[0])
	}
	if dates[29] != "2026-06-30" {
		t.Errorf("last=%q, want 2026-06-30", dates[29])
	}
}

// TestMonthDateRange_BadInput verifies nil is returned for invalid input.
func TestMonthDateRange_BadInput(t *testing.T) {
	if MonthDateRange("not-a-month") != nil {
		t.Error("expected nil for invalid month")
	}
}

// TestAwardScanner_ScanDateRange_RateLimit verifies sequential 1 QPS pacing.
func TestAwardScanner_ScanDateRange_RateLimit(t *testing.T) {
	const calls = 3
	callTimesCh := make(chan time.Time, calls)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimesCh <- time.Now()

		fixture, _ := os.ReadFile("testdata/award_prg_ams.json")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	scanner, err := NewAwardScanner(AwardScannerOptions{
		Cookies:    "session=test",
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAwardScanner: %v", err)
	}

	dates := []string{"2026-06-01", "2026-06-02", "2026-06-03"}
	start := time.Now()
	_, err = scanner.ScanDateRange(context.Background(), "PRG", "AMS", dates)
	if err != nil {
		t.Fatalf("ScanDateRange: %v", err)
	}
	elapsed := time.Since(start)

	// 3 calls at 1 QPS should take at least 2 seconds (gaps between calls 1-2 and 2-3).
	if elapsed < 1900*time.Millisecond {
		t.Errorf("elapsed=%v, want >= 1.9s (rate limiting 3 calls at 1 QPS)", elapsed)
	}

	if len(callTimesCh) != calls {
		t.Errorf("got %d server calls, want %d", len(callTimesCh), calls)
	}
}

// TestLoadAwardFixture verifies testdata loading.
func TestLoadAwardFixture(t *testing.T) {
	t.Setenv("TRVL_TEST_AWARD_FIXTURE", "1")

	scanner, err := NewAwardScanner(AwardScannerOptions{
		Cookies: "session=test",
		BaseURL: "http://localhost",
	})
	if err != nil {
		t.Fatalf("NewAwardScanner: %v", err)
	}

	offers, err := scanner.Search(context.Background(), "PRG", "AMS", "2026-06-15")
	if err != nil {
		t.Fatalf("Search (fixture): %v", err)
	}
	if len(offers) == 0 {
		t.Fatal("expected offers from fixture, got none")
	}
	for _, o := range offers {
		if o.Date != "2026-06-15" {
			t.Errorf("offer.Date=%q, want 2026-06-15", o.Date)
		}
		if o.Miles <= 0 {
			t.Errorf("offer.Miles=%d, want >0", o.Miles)
		}
	}
}

// TestMapAwardResponse_EmptyResponse verifies graceful handling of empty response.
func TestMapAwardResponse_EmptyResponse(t *testing.T) {
	resp := &klmAwardResponse{}
	offers := mapAwardResponse(resp, "PRG", "AMS", "2026-06-15")
	if offers != nil {
		t.Errorf("expected nil for empty response, got %v", offers)
	}
}

// TestTimeFromDateTime covers various datetime formats.
func TestTimeFromDateTime(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-06-15T08:40:00", "08:40"},
		{"2026-06-15T14:05:00", "14:05"},
		{"", ""},
		{"08:40", "08:40"},
	}
	for _, tc := range tests {
		got := timeFromDateTime(tc.input)
		if got != tc.want {
			t.Errorf("timeFromDateTime(%q)=%q, want %q", tc.input, got, tc.want)
		}
	}
}
