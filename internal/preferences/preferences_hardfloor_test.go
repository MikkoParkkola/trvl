package preferences

import (
	"encoding/json"
	"strings"
	"testing"
)

// #473: FlightTimeHardFloor round-trips through JSON and is omitted when zero.
func TestFlightTimeHardFloor_JSONRoundTrip(t *testing.T) {
	p := Default()
	p.FlightTimeEarliest = "06:00"
	p.FlightTimeLatest = "22:00"
	p.FlightTimeHardFloor = 45

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"flight_time_hard_floor":45`) {
		t.Errorf("marshalled JSON missing flight_time_hard_floor:45\n%s", b)
	}

	var got Preferences
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.FlightTimeHardFloor != 45 {
		t.Errorf("round-trip FlightTimeHardFloor = %d, want 45", got.FlightTimeHardFloor)
	}
}

// #473: a zero hard floor is omitted from the serialised JSON (omitempty).
func TestFlightTimeHardFloor_OmitEmpty(t *testing.T) {
	p := Default()
	p.FlightTimeHardFloor = 0
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "flight_time_hard_floor") {
		t.Errorf("zero FlightTimeHardFloor should be omitted, got:\n%s", b)
	}
}
