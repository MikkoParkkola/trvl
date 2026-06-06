package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
