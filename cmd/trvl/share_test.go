package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/trips"
)

func TestShareCmd_NonNil(t *testing.T) {
	cmd := shareCmd()
	if cmd == nil {
		t.Fatal("shareCmd() returned nil")
	}
}

func TestShareCmd_Use(t *testing.T) {
	cmd := shareCmd()
	if cmd.Use != "share [trip_id]" {
		t.Errorf("shareCmd Use = %q, want %q", cmd.Use, "share [trip_id]")
	}
}

func TestShareCmd_Flags(t *testing.T) {
	cmd := shareCmd()
	flags := []string{"last", "format"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag --%s", name)
		}
	}
}

func TestShareCmd_RequiresArgOrLast(t *testing.T) {
	cmd := shareCmd()
	// Running with no args and no --last should error.
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Error("expected error with no args and no --last")
	}
}

func TestFormatTripMarkdown_Basic(t *testing.T) {
	trip := &trips.Trip{
		Name: "Krakow Trip",
		Legs: []trips.TripLeg{
			{
				Type:      "flight",
				From:      "Helsinki",
				To:        "Krakow",
				Provider:  "Finnair",
				StartTime: "2026-06-16",
				EndTime:   "2026-06-16",
				Price:     89,
				Currency:  "EUR",
			},
			{
				Type:      "hotel",
				From:      "Krakow",
				To:        "Krakow",
				Provider:  "Hotel Stary",
				StartTime: "2026-06-16",
				EndTime:   "2026-06-19",
				Price:     102,
				Currency:  "EUR",
			},
		},
	}

	md := formatTripMarkdown(trip)

	// Must contain route header.
	if !strings.Contains(md, "Helsinki -> Krakow") {
		t.Errorf("missing route header in:\n%s", md)
	}
	// Must contain price table.
	if !strings.Contains(md, "| Flight |") {
		t.Errorf("missing flight row in:\n%s", md)
	}
	if !strings.Contains(md, "| Hotel |") {
		t.Errorf("missing hotel row in:\n%s", md)
	}
	if !strings.Contains(md, "Finnair") {
		t.Errorf("missing airline in:\n%s", md)
	}
	if !strings.Contains(md, "Hotel Stary") {
		t.Errorf("missing hotel name in:\n%s", md)
	}
	// Must contain total.
	if !strings.Contains(md, "**Total**") {
		t.Errorf("missing total row in:\n%s", md)
	}
	if !strings.Contains(md, "EUR 191") {
		t.Errorf("missing correct total (EUR 191) in:\n%s", md)
	}
	// Must contain footer.
	if !strings.Contains(md, "trvl") {
		t.Errorf("missing trvl footer in:\n%s", md)
	}
	if !strings.Contains(md, "1 smart MCP tool + 62 compatibility aliases") {
		t.Errorf("missing tool count in footer:\n%s", md)
	}
}

func TestFormatTripMarkdown_NoLegs(t *testing.T) {
	trip := &trips.Trip{
		Name: "Empty Trip",
		Legs: nil,
	}

	md := formatTripMarkdown(trip)

	// Should fall back to trip name.
	if !strings.Contains(md, "Empty Trip") {
		t.Errorf("missing trip name in:\n%s", md)
	}
	// Should not contain price table headers.
	if strings.Contains(md, "|---|---|") {
		t.Errorf("unexpected price table in trip with no priced legs:\n%s", md)
	}
	// Footer should still be present.
	if !strings.Contains(md, "trvl") {
		t.Errorf("missing trvl footer in:\n%s", md)
	}
}

func TestFormatTripMarkdown_NightsCalculation(t *testing.T) {
	trip := &trips.Trip{
		Name: "3-night trip",
		Legs: []trips.TripLeg{
			{Type: "flight", From: "HEL", To: "BCN", StartTime: "2026-07-01", EndTime: "2026-07-01"},
			{Type: "flight", From: "BCN", To: "HEL", StartTime: "2026-07-04", EndTime: "2026-07-04"},
		},
	}

	md := formatTripMarkdown(trip)

	if !strings.Contains(md, "3 nights") {
		t.Errorf("expected '3 nights' in:\n%s", md)
	}
}

func TestFormatLastSearchMarkdown_Full(t *testing.T) {
	ls := &LastSearch{
		Command:        "trip",
		Origin:         "Helsinki",
		Destination:    "Krakow",
		DepartDate:     "2026-06-16",
		ReturnDate:     "2026-06-19",
		Nights:         3,
		FlightPrice:    89,
		FlightCurrency: "EUR",
		FlightAirline:  "Finnair",
		FlightStops:    0,
		HotelPrice:     102,
		HotelCurrency:  "EUR",
		HotelName:      "Hotel Stary",
		TotalPrice:     191,
		TotalCurrency:  "EUR",
	}

	md := formatLastSearchMarkdown(ls)

	checks := []string{
		"Helsinki -> Krakow",
		"Jun 16",
		"Jun 19",
		"3 nights",
		"Finnair",
		"nonstop",
		"Hotel Stary",
		"EUR 191",
		"trvl",
	}
	for _, want := range checks {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
}

func TestFormatLastSearchMarkdown_FlightOnly(t *testing.T) {
	ls := &LastSearch{
		Command:        "flights",
		Origin:         "AMS",
		Destination:    "NRT",
		DepartDate:     "2026-08-01",
		FlightPrice:    650,
		FlightCurrency: "EUR",
		FlightAirline:  "KLM",
		FlightStops:    1,
	}

	md := formatLastSearchMarkdown(ls)

	if !strings.Contains(md, "1 stop") {
		t.Errorf("expected '1 stop' in:\n%s", md)
	}
	// No hotel row.
	if strings.Contains(md, "| Hotel |") {
		t.Errorf("unexpected hotel row in flight-only search:\n%s", md)
	}
}

func TestFormatLastSearchMarkdown_NoRoute(t *testing.T) {
	ls := &LastSearch{
		Command: "discover",
	}

	md := formatLastSearchMarkdown(ls)

	if !strings.Contains(md, "discover search") {
		t.Errorf("expected 'discover search' fallback in:\n%s", md)
	}
}

func TestFormatDateCompact(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-06-16", "Jun 16"},
		{"2026-01-01", "Jan 1"},
		{"2026-12-25", "Dec 25"},
		{"bad-date", "bad-date"},
	}
	for _, tt := range tests {
		got := formatDateCompact(tt.input)
		if got != tt.want {
			t.Errorf("formatDateCompact(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractTripRoute(t *testing.T) {
	trip := &trips.Trip{
		Legs: []trips.TripLeg{
			{From: "HEL", To: "BCN", StartTime: "2026-07-01T10:00", EndTime: "2026-07-01T14:00"},
			{From: "BCN", To: "BCN", StartTime: "2026-07-01", EndTime: "2026-07-04"},
			{From: "BCN", To: "HEL", StartTime: "2026-07-04T16:00", EndTime: "2026-07-04T22:00"},
		},
	}

	origin, dest, depart, ret, nights := extractTripRoute(trip)

	if origin != "HEL" {
		t.Errorf("origin = %q, want HEL", origin)
	}
	if dest != "BCN" {
		t.Errorf("dest = %q, want BCN", dest)
	}
	if depart != "2026-07-01" {
		t.Errorf("depart = %q, want 2026-07-01", depart)
	}
	if ret != "2026-07-04" {
		t.Errorf("ret = %q, want 2026-07-04", ret)
	}
	if nights != 3 {
		t.Errorf("nights = %d, want 3", nights)
	}
}

func TestExtractTripRoute_Empty(t *testing.T) {
	trip := &trips.Trip{}
	origin, dest, depart, ret, nights := extractTripRoute(trip)
	if origin != "" || dest != "" || depart != "" || ret != "" || nights != 0 {
		t.Errorf("expected all empty for trip with no legs, got %q %q %q %q %d", origin, dest, depart, ret, nights)
	}
}

func TestSaveAndLoadLastSearch(t *testing.T) {
	// Use a temp dir to avoid polluting ~/.trvl.
	tmp := t.TempDir()
	setTestHome(t, tmp)

	// Create the .trvl dir.
	_ = os.MkdirAll(filepath.Join(tmp, ".trvl"), 0o700)

	ls := &LastSearch{
		Command:        "trip",
		Origin:         "HEL",
		Destination:    "BCN",
		DepartDate:     "2026-07-01",
		ReturnDate:     "2026-07-04",
		Nights:         3,
		FlightPrice:    150,
		FlightCurrency: "EUR",
		TotalPrice:     350,
		TotalCurrency:  "EUR",
	}

	saveLastSearch(ls)

	loaded, err := loadLastSearch()
	if err != nil {
		t.Fatalf("loadLastSearch() error: %v", err)
	}

	if loaded.Origin != "HEL" {
		t.Errorf("Origin = %q, want HEL", loaded.Origin)
	}
	if loaded.Destination != "BCN" {
		t.Errorf("Destination = %q, want BCN", loaded.Destination)
	}
	if loaded.Nights != 3 {
		t.Errorf("Nights = %d, want 3", loaded.Nights)
	}
	if loaded.TotalPrice != 350 {
		t.Errorf("TotalPrice = %f, want 350", loaded.TotalPrice)
	}
	if loaded.Timestamp.IsZero() {
		t.Error("Timestamp should be set after save")
	}
}

func TestLoadLastSearch_NotFound(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	_, err := loadLastSearch()
	if err == nil {
		t.Fatal("expected error when no last search exists")
	}
	if !strings.Contains(err.Error(), "no recent search") {
		t.Errorf("error = %q, want to contain 'no recent search'", err.Error())
	}
}

func TestTrvlFooter(t *testing.T) {
	f := trvlFooter()
	if !strings.Contains(f, "trvl") {
		t.Error("footer missing 'trvl'")
	}
	if !strings.Contains(f, "1 smart MCP tool + 62 compatibility aliases") {
		t.Error("footer missing tool count")
	}
	if !strings.Contains(f, "no API keys") {
		t.Error("footer missing 'no API keys'")
	}
}

func TestLastSearch_JSONRoundtrip(t *testing.T) {
	ls := &LastSearch{
		Command:     "trip",
		Timestamp:   time.Now().Truncate(time.Second),
		Origin:      "HEL",
		Destination: "KRK",
		Nights:      3,
		TotalPrice:  191,
	}

	// Verify it's valid JSON by marshaling and unmarshaling.
	var ls2 LastSearch
	data, err := json.Marshal(ls)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(data, &ls2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ls2.Origin != ls.Origin || ls2.TotalPrice != ls.TotalPrice {
		t.Errorf("roundtrip mismatch: got %+v", ls2)
	}
}

func TestFormatLastSearchMarkdown_MultiStop(t *testing.T) {
	ls := &LastSearch{
		Command:        "flights",
		Origin:         "HEL",
		Destination:    "SYD",
		FlightPrice:    890,
		FlightCurrency: "EUR",
		FlightAirline:  "Qatar",
		FlightStops:    2,
	}

	md := formatLastSearchMarkdown(ls)

	if !strings.Contains(md, "2 stops") {
		t.Errorf("expected '2 stops' in:\n%s", md)
	}
}
