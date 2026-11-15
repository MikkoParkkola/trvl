package ground

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestLookupOebbStation(t *testing.T) {
	tests := []struct {
		city      string
		wantExtID string
		wantOK    bool
	}{
		{"Vienna", "8103000", true},
		{"vienna", "8103000", true},
		{"Wien", "8103000", true},
		{"  Vienna  ", "8103000", true},
		{"Salzburg", "8100002", true},
		{"Innsbruck", "8100108", true},
		{"Graz", "8100173", true},
		{"Munich", "8000261", true},
		{"münchen", "8000261", true},
		{"Berlin", "8011160", true},
		{"Zurich", "8503000", true},
		{"Venice", "8300137", true},
		{"Budapest", "5500017", true},
		{"Prague", "5400014", true},
		{"Bratislava", "5600002", true},
		{"Ljubljana", "7900001", true},
		{"Zagreb", "7800001", true},
		{"", "", false},
		{"Nonexistent", "", false},
		{"London", "", false}, // not in ÖBB network
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupOebbStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupOebbStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.ExtID != tt.wantExtID {
				t.Errorf("ExtID = %q, want %q", station.ExtID, tt.wantExtID)
			}
		})
	}
}

func TestLookupOebbStation_Metadata(t *testing.T) {
	station, ok := LookupOebbStation("Vienna")
	if !ok {
		t.Fatal("expected Vienna to be found")
	}
	if station.Name != "Wien Hbf" {
		t.Errorf("Name = %q, want %q", station.Name, "Wien Hbf")
	}
	if station.City != "Vienna" {
		t.Errorf("City = %q, want %q", station.City, "Vienna")
	}
}

func TestHasOebbStation(t *testing.T) {
	if !HasOebbStation("Vienna") {
		t.Error("Vienna should have an ÖBB station")
	}
	if HasOebbStation("Atlantis") {
		t.Error("Atlantis should not have an ÖBB station")
	}
}

func TestHasOebbRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Vienna", "Salzburg", true},
		{"Vienna", "Munich", true},
		{"Vienna", "Budapest", true},
		{"Vienna", "Prague", true},
		{"Berlin", "Munich", true},
		{"Vienna", "London", false}, // London not in ÖBB network
		{"Atlantis", "Vienna", false},
		{"Vienna", "Atlantis", false},
		{"", "Vienna", false},
		{"Vienna", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.from+"->"+tt.to, func(t *testing.T) {
			got := HasOebbRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasOebbRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestOebbTripSearchRequest(t *testing.T) {
	req := oebbTripSearchRequest("1190100", "8000261", "20260715", "060000")

	// Verify auth block.
	auth, ok := req["auth"].(map[string]any)
	if !ok {
		t.Fatal("auth should be a map")
	}
	if auth["type"] != "AID" {
		t.Errorf("auth.type = %v, want AID", auth["type"])
	}

	// Verify svcReqL structure.
	svcReqL, ok := req["svcReqL"].([]map[string]any)
	if !ok || len(svcReqL) != 1 {
		t.Fatal("svcReqL should be a slice with one element")
	}
	svc := svcReqL[0]
	if svc["meth"] != "TripSearch" {
		t.Errorf("meth = %v, want TripSearch", svc["meth"])
	}

	tripReq, ok := svc["req"].(map[string]any)
	if !ok {
		t.Fatal("req should be a map")
	}

	// Check origin/destination.
	depLocL, ok := tripReq["depLocL"].([]map[string]any)
	if !ok || len(depLocL) == 0 {
		t.Fatal("depLocL should be non-empty")
	}
	if depLocL[0]["extId"] != "1190100" {
		t.Errorf("depLocL[0].extId = %v, want 1190100", depLocL[0]["extId"])
	}

	arrLocL, ok := tripReq["arrLocL"].([]map[string]any)
	if !ok || len(arrLocL) == 0 {
		t.Fatal("arrLocL should be non-empty")
	}
	if arrLocL[0]["extId"] != "8000261" {
		t.Errorf("arrLocL[0].extId = %v, want 8000261", arrLocL[0]["extId"])
	}

	if tripReq["outDate"] != "20260715" {
		t.Errorf("outDate = %v, want 20260715", tripReq["outDate"])
	}
	if tripReq["outTime"] != "060000" {
		t.Errorf("outTime = %v, want 060000", tripReq["outTime"])
	}
}

func TestOebbParseDateTime(t *testing.T) {
	tests := []struct {
		dateS string
		timeS string
		want  string
	}{
		{"20260715", "060000", "2026-07-15T06:00:00"},
		{"20260715", "143000", "2026-07-15T14:30:00"},
		// Time with leading zero omitted (HAFAS can return "60000" for 06:00:00)
		{"20260715", "60000", "2026-07-15T06:00:00"},
		// Day overflow: 250000 = 01:00 next day
		{"20260715", "250000", "2026-07-16T01:00:00"},
		// Missing fields
		{"", "060000", ""},
		{"20260715", "", ""},
	}

	for _, tt := range tests {
		got := oebbParseDateTime(tt.dateS, tt.timeS)
		if got != tt.want {
			t.Errorf("oebbParseDateTime(%q, %q) = %q, want %q", tt.dateS, tt.timeS, got, tt.want)
		}
	}
}

func TestOebbParseDuration(t *testing.T) {
	tests := []struct {
		dur  string
		want int
	}{
		// 6-char "HHMMSS" format (actual HAFAS response)
		{"041300", 253}, // 4h 13m = 253 min
		{"041700", 257}, // 4h 17m
		{"051100", 311}, // 5h 11m
		{"000000", 0},
		// 7-char "DHHMMSS" format (legacy / overnight)
		{"0035500", 235},  // 0d 3h 55m = 235 min
		{"0010000", 60},   // 0d 1h 0m
		{"0020000", 120},  // 0d 2h 0m
		{"1000000", 1440}, // 1d 0h 0m = 1440 min
		{"0000000", 0},
		// Edge cases
		{"", 0},
	}

	for _, tt := range tests {
		got := oebbParseDuration(tt.dur)
		if got != tt.want {
			t.Errorf("oebbParseDuration(%q) = %d, want %d", tt.dur, got, tt.want)
		}
	}
}

func TestBuildOebbBookingURL(t *testing.T) {
	from := oebbStation{ExtID: "1190100", Name: "Wien Hbf", City: "Vienna"}
	to := oebbStation{ExtID: "8000261", Name: "München Hbf", City: "Munich"}
	u := buildOebbBookingURL(from, to, "2026-07-15")

	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "oebb.at") {
		t.Error("should contain oebb.at")
	}
	if !strings.Contains(u, "1190100") {
		t.Error("should contain origin ExtID")
	}
	if !strings.Contains(u, "8000261") {
		t.Error("should contain destination ExtID")
	}
	if !strings.Contains(u, "2026-07-15") {
		t.Error("should contain date")
	}
}

func TestParseOebbConnections_Empty(t *testing.T) {
	routes := parseOebbConnections(oebbTripRes{}, oebbStation{}, oebbStation{}, "2026-07-15", "EUR")
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestParseOebbConnections_Basic(t *testing.T) {
	from := oebbStation{ExtID: "1190100", Name: "Wien Hbf", City: "Vienna"}
	to := oebbStation{ExtID: "8000261", Name: "München Hbf", City: "Munich"}

	res := oebbTripRes{
		Common: oebbCommon{
			LocL:  []oebbLoc{{Name: "Wien Hbf"}, {Name: "München Hbf"}},
			ProdL: []oebbProd{{Name: "RJX 163"}},
		},
		OutConL: []oebbCon{
			{
				Date: "20260715",
				Dep:  oebbConStop{DTimeS: "080000", LocX: 0},
				Arr:  oebbConStop{ATimeS: "123000", LocX: 1},
				Dur:  "043000", // 4h 30m = 270 min
				CHG:  0,
				SecL: []oebbSec{
					{
						Type: "JNY",
						Dep:  oebbConStop{DTimeS: "080000", LocX: 0},
						Arr:  oebbConStop{ATimeS: "123000", LocX: 1},
						JnyL: &oebbJny{ProdX: 0},
					},
				},
				TrfRes: &oebbTrfRes{
					FareSetL: []oebbFareSet{
						{
							FareL: []oebbFare{
								{Name: "Sparschiene", Price: 2990, Cur: "EUR"},
							},
						},
					},
				},
			},
		},
	}

	routes := parseOebbConnections(res, from, to, "2026-07-15", "EUR")
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "oebb" {
		t.Errorf("Provider = %q, want oebb", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("Type = %q, want train", r.Type)
	}
	if r.Price != 29.90 {
		t.Errorf("Price = %f, want 29.90", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", r.Currency)
	}
	if r.Duration != 270 {
		t.Errorf("Duration = %d, want 270", r.Duration)
	}
	if r.Departure.City != "Vienna" {
		t.Errorf("Departure.City = %q, want Vienna", r.Departure.City)
	}
	if r.Arrival.City != "Munich" {
		t.Errorf("Arrival.City = %q, want Munich", r.Arrival.City)
	}
	if r.Transfers != 0 {
		t.Errorf("Transfers = %d, want 0", r.Transfers)
	}
	if len(r.Legs) != 1 {
		t.Errorf("Legs = %d, want 1", len(r.Legs))
	}
	if r.Legs[0].Provider != "RJX 163" {
		t.Errorf("Leg provider = %q, want RJX 163", r.Legs[0].Provider)
	}
	if r.BookingURL == "" {
		t.Error("BookingURL should not be empty")
	}
}

func TestParseOebbConnections_MultiLeg(t *testing.T) {
	from := oebbStation{ExtID: "1190100", Name: "Wien Hbf", City: "Vienna"}
	to := oebbStation{ExtID: "5400014", Name: "Praha hl.n.", City: "Prague"}

	res := oebbTripRes{
		Common: oebbCommon{
			LocL:  []oebbLoc{{Name: "Wien Hbf"}, {Name: "Brno"}, {Name: "Praha hl.n."}},
			ProdL: []oebbProd{{Name: "RJX 77"}, {Name: "EC 101"}},
		},
		OutConL: []oebbCon{
			{
				Date: "20260715",
				Dep:  oebbConStop{DTimeS: "080000", LocX: 0},
				Arr:  oebbConStop{ATimeS: "130000", LocX: 2},
				Dur:  "050000", // 5h 0m = 300 min
				CHG:  1,
				SecL: []oebbSec{
					{
						Type: "JNY",
						Dep:  oebbConStop{DTimeS: "080000", LocX: 0},
						Arr:  oebbConStop{ATimeS: "103000", LocX: 1},
						JnyL: &oebbJny{ProdX: 0},
					},
					{
						Type: "JNY",
						Dep:  oebbConStop{DTimeS: "110000", LocX: 1},
						Arr:  oebbConStop{ATimeS: "130000", LocX: 2},
						JnyL: &oebbJny{ProdX: 1},
					},
				},
			},
		},
	}

	routes := parseOebbConnections(res, from, to, "2026-07-15", "EUR")
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Transfers != 1 {
		t.Errorf("Transfers = %d, want 1", r.Transfers)
	}
	if len(r.Legs) != 2 {
		t.Errorf("Legs = %d, want 2", len(r.Legs))
	}
	if r.Duration != 300 {
		t.Errorf("Duration = %d, want 300 (5h from dur field)", r.Duration)
	}
}

func TestOebbAllStationsHaveRequiredFields(t *testing.T) {
	for city, station := range oebbStations {
		if station.ExtID == "" {
			t.Errorf("station %q has empty ExtID", city)
		}
		if station.Name == "" {
			t.Errorf("station %q has empty Name", city)
		}
		if station.City == "" {
			t.Errorf("station %q has empty City", city)
		}
	}
}

func TestOebbRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, oebbLimiter, 12*time.Second, 1)
}

func TestSearchOebb_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchOebb(ctx, "Vienna", "Salzburg", date, "EUR")
	if err != nil {
		t.Skipf("ÖBB API unavailable: %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no ÖBB routes found")
	}

	r := routes[0]
	if r.Provider != "oebb" {
		t.Errorf("provider = %q, want oebb", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("type = %q, want train", r.Type)
	}
	if r.Duration <= 0 {
		t.Errorf("duration = %d, should be > 0", r.Duration)
	}
	if r.Departure.City != "Vienna" {
		t.Errorf("departure city = %q, want Vienna", r.Departure.City)
	}
	if r.Arrival.City != "Salzburg" {
		t.Errorf("arrival city = %q, want Salzburg", r.Arrival.City)
	}
	if r.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
}
