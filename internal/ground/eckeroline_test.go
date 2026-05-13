package ground

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLookupEckeroLinePort(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantOK   bool
	}{
		{"Helsinki", "HEL", true},
		{"hel", "HEL", true},
		{"Tallinn", "TLL", true},
		{"tll", "TLL", true},
		{"tln", "TLL", true},
		{"unknown", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		code, _, _, ok := LookupEckeroLinePort(tt.city)
		if ok != tt.wantOK {
			t.Errorf("LookupEckeroLinePort(%q): ok = %v, want %v", tt.city, ok, tt.wantOK)
			continue
		}
		if ok && code != tt.wantCode {
			t.Errorf("LookupEckeroLinePort(%q) code = %q, want %q", tt.city, code, tt.wantCode)
		}
	}
}

func TestHasEckeroLineRoute(t *testing.T) {
	if !HasEckeroLineRoute("Helsinki", "Tallinn") {
		t.Error("expected HEL-TLL route")
	}
	if !HasEckeroLineRoute("Tallinn", "Helsinki") {
		t.Error("expected TLL-HEL route")
	}
	if HasEckeroLineRoute("London", "Paris") {
		t.Error("London-Paris should not be an Eckerö Line route")
	}
	if HasEckeroLineRoute("Helsinki", "unknown") {
		t.Error("Helsinki-unknown should not be a route")
	}
}

func TestEckerolineDayMatch(t *testing.T) {
	// 2026-06-01 is a Monday
	tests := []struct {
		date string
		days string
		want bool
	}{
		{"2026-06-01", "daily", true},
		{"2026-06-01", "mon-sat", true},       // Monday
		{"2026-06-06", "mon-sat", true},       // Saturday
		{"2026-06-07", "mon-sat", false},      // Sunday
		{"2026-06-07", "sun-fri", true},       // Sunday
		{"2026-06-01", "sun-fri", true},       // Monday
		{"2026-06-06", "sun-fri", false},      // Saturday
		{"invalid", "daily", true},            // parse fail → assume yes
		{"2026-06-01", "unknown-range", true}, // unknown → assume yes
	}

	for _, tt := range tests {
		got := eckerolineDayMatch(tt.date, tt.days)
		if got != tt.want {
			t.Errorf("eckerolineDayMatch(%q, %q) = %v, want %v", tt.date, tt.days, got, tt.want)
		}
	}
}

func TestEckerolineSchedules_Bidirectional(t *testing.T) {
	if _, ok := eckerolineSchedules["HEL-TLL"]; !ok {
		t.Error("missing HEL-TLL schedule")
	}
	if _, ok := eckerolineSchedules["TLL-HEL"]; !ok {
		t.Error("missing TLL-HEL schedule")
	}
}

func TestEckerolineDepartureResponseParse(t *testing.T) {
	raw := `{"departures":[{"time":"09:00","price":19.00,"ship":"M/s Finlandia"},{"time":"15:15","price":25.00,"ship":"M/s Finlandia"}]}`
	var resp eckerolineDepartureResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Departures) != 2 {
		t.Fatalf("expected 2 departures, got %d", len(resp.Departures))
	}
	if resp.Departures[0].Price != 19.0 {
		t.Errorf("price = %.2f, want 19.00", resp.Departures[0].Price)
	}
}

func TestEckerolineDepartureArray(t *testing.T) {
	// The API can return departures as a plain array.
	raw := `[{"time":"09:00","price":19.00,"ship":"M/s Finlandia"}]`
	var deps []eckerolineDeparture
	if err := json.Unmarshal([]byte(raw), &deps); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 departure, got %d", len(deps))
	}
	if deps[0].Time != "09:00" {
		t.Errorf("time = %q, want 09:00", deps[0].Time)
	}
}

func TestEckerolineFormKeyRegex(t *testing.T) {
	html := `<input name="form_key" type="hidden" value="abc123def456" />`
	match := eckerolineFormKeyRegex.FindStringSubmatch(html)
	if len(match) < 2 {
		t.Fatal("form_key regex did not match")
	}
	if match[1] != "abc123def456" {
		t.Errorf("form_key = %q, want abc123def456", match[1])
	}
}

func TestEckerolineRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, eckerolineLimiter, 12*time.Second, 1)
}

func TestEckerolineAllPortsHaveRequiredFields(t *testing.T) {
	for alias, port := range eckerolinePorts {
		if port.Code == "" {
			t.Errorf("port alias %q has empty Code", alias)
		}
		if port.Name == "" {
			t.Errorf("port alias %q has empty Name", alias)
		}
		if port.City == "" {
			t.Errorf("port alias %q has empty City", alias)
		}
	}
}
