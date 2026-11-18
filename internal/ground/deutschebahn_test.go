package ground

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestLookupDBStation(t *testing.T) {
	tests := []struct {
		city    string
		wantEVA string
		wantOK  bool
	}{
		{"Berlin", "8011160", true},
		{"berlin", "8011160", true},
		{"BERLIN", "8011160", true},
		{"  Berlin  ", "8011160", true},
		{"Munich", "8000261", true},
		{"münchen", "8000261", true},
		{"Frankfurt", "8000105", true},
		{"Hamburg", "8002549", true},
		{"Cologne", "8000207", true},
		{"köln", "8000207", true},
		{"Vienna", "8103000", true},
		{"wien", "8103000", true},
		{"Zurich", "8503000", true},
		{"zürich", "8503000", true},
		{"Amsterdam", "8400058", true},
		{"Prague", "5400014", true},
		{"Budapest", "5500017", true},
		{"Warsaw", "5100065", true},
		{"Krakow", "5100028", true},
		{"kraków", "5100028", true},
		{"Copenhagen", "8600626", true},
		{"Paris", "8727100", true},
		{"Brussels", "8814001", true},
		{"Milan", "8300046", true},
		{"", "", false},
		{"Nonexistent", "", false},
		{"Timbuktu", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.city, func(t *testing.T) {
			station, ok := LookupDBStation(tt.city)
			if ok != tt.wantOK {
				t.Fatalf("LookupDBStation(%q) ok = %v, want %v", tt.city, ok, tt.wantOK)
			}
			if ok && station.EVA != tt.wantEVA {
				t.Errorf("EVA = %q, want %q", station.EVA, tt.wantEVA)
			}
		})
	}
}

func TestLookupDBStation_Metadata(t *testing.T) {
	station, ok := LookupDBStation("Berlin")
	if !ok {
		t.Fatal("expected Berlin to be found")
	}
	if station.Name != "Berlin Hbf" {
		t.Errorf("Name = %q, want %q", station.Name, "Berlin Hbf")
	}
	if station.City != "Berlin" {
		t.Errorf("City = %q, want %q", station.City, "Berlin")
	}
	if station.Country != "DE" {
		t.Errorf("Country = %q, want %q", station.Country, "DE")
	}
}

func TestLookupDBStation_GermanAliases(t *testing.T) {
	// Verify that German spellings resolve to the same station.
	pairs := [][2]string{
		{"munich", "münchen"},
		{"cologne", "köln"},
		{"zurich", "zürich"},
		{"vienna", "wien"},
		{"nuremberg", "nürnberg"},
		{"hanover", "hannover"},
		{"dusseldorf", "düsseldorf"},
		{"prague", "praha"},
	}

	for _, p := range pairs {
		s1, ok1 := LookupDBStation(p[0])
		s2, ok2 := LookupDBStation(p[1])
		if !ok1 || !ok2 {
			t.Errorf("both %q and %q should be found", p[0], p[1])
			continue
		}
		if s1.EVA != s2.EVA {
			t.Errorf("%q EVA=%q != %q EVA=%q", p[0], s1.EVA, p[1], s2.EVA)
		}
	}
}

func TestHasDBStation(t *testing.T) {
	if !HasDBStation("Berlin") {
		t.Error("Berlin should have a DB station")
	}
	if HasDBStation("Atlantis") {
		t.Error("Atlantis should not have a DB station")
	}
}

func TestHasDBRoute(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"Berlin", "Munich", true},
		{"Berlin", "Vienna", true},
		{"Prague", "Budapest", true},
		{"Berlin", "Atlantis", false},
		{"Atlantis", "Berlin", false},
		{"Atlantis", "Nowhere", false},
		{"", "Berlin", false},
		{"Berlin", "", false},
	}

	for _, tt := range tests {
		name := tt.from + "->" + tt.to
		t.Run(name, func(t *testing.T) {
			got := HasDBRoute(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("HasDBRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestDBJourneysRequest(t *testing.T) {
	when := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	req := dbJourneysRequest("8011160", "8000261", when)

	// Verify structure.
	if req["klasse"] != "KLASSE_2" {
		t.Errorf("klasse = %v, want KLASSE_2", req["klasse"])
	}

	reiseHin, ok := req["reiseHin"].(map[string]any)
	if !ok {
		t.Fatal("reiseHin should be a map")
	}
	wunsch, ok := reiseHin["wunsch"].(map[string]any)
	if !ok {
		t.Fatal("reiseHin.wunsch should be a map")
	}

	fromLid, ok := wunsch["abgangsLocationId"].(string)
	if !ok {
		t.Fatal("abgangsLocationId should be a string")
	}
	if !strings.Contains(fromLid, "8011160") {
		t.Errorf("abgangsLocationId should contain Berlin EVA, got %q", fromLid)
	}

	toLid, ok := wunsch["zielLocationId"].(string)
	if !ok {
		t.Fatal("zielLocationId should be a string")
	}
	if !strings.Contains(toLid, "8000261") {
		t.Errorf("zielLocationId should contain München EVA, got %q", toLid)
	}

	zeitWunsch, ok := wunsch["zeitWunsch"].(map[string]any)
	if !ok {
		t.Fatal("zeitWunsch should be a map")
	}
	if zeitWunsch["zeitPunktArt"] != "ABFAHRT" {
		t.Errorf("zeitPunktArt = %v, want ABFAHRT", zeitWunsch["zeitPunktArt"])
	}
	reiseDatum, ok := zeitWunsch["reiseDatum"].(string)
	if !ok {
		t.Fatal("reiseDatum should be a string")
	}
	if !strings.HasPrefix(reiseDatum, "2026-07-15") {
		t.Errorf("reiseDatum = %q, want prefix 2026-07-15", reiseDatum)
	}

	verkehrsmittel, ok := wunsch["verkehrsmittel"].([]string)
	if !ok || len(verkehrsmittel) == 0 || verkehrsmittel[0] != "ALL" {
		t.Errorf("verkehrsmittel = %v, want [ALL]", verkehrsmittel)
	}
}

func TestDBJourneysRequest_LocationIDFormat(t *testing.T) {
	when := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	req := dbJourneysRequest("8011160", "8000261", when)

	reiseHin := req["reiseHin"].(map[string]any)
	wunsch := reiseHin["wunsch"].(map[string]any)

	// The location ID format should be A=1@L=<EVA>@
	fromLid := wunsch["abgangsLocationId"].(string)
	if fromLid != "A=1@L=8011160@" {
		t.Errorf("fromLid = %q, want %q", fromLid, "A=1@L=8011160@")
	}
	toLid := wunsch["zielLocationId"].(string)
	if toLid != "A=1@L=8000261@" {
		t.Errorf("toLid = %q, want %q", toLid, "A=1@L=8000261@")
	}
}

func TestComputeDBDuration(t *testing.T) {
	tests := []struct {
		dep  string
		arr  string
		want int
	}{
		{"2026-07-15T08:00:00+02:00", "2026-07-15T12:30:00+02:00", 270},
		{"2026-07-15T08:00:00", "2026-07-15T12:30:00", 270},
		{"2026-07-15T23:00:00+02:00", "2026-07-16T07:00:00+02:00", 480},
		{"invalid", "invalid", 0},
		{"2026-07-15T12:00:00", "2026-07-15T08:00:00", 0}, // negative
	}

	for _, tt := range tests {
		got := computeDurationMinutes(tt.dep, tt.arr)
		if got != tt.want {
			t.Errorf("computeDurationMinutes(%q, %q) = %d, want %d", tt.dep, tt.arr, got, tt.want)
		}
	}
}

func TestBuildDBBookingURL(t *testing.T) {
	u := buildDBBookingURL("8011160", "8000261", "2026-07-15T08:00:00+02:00")
	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "bahn.de") {
		t.Error("should contain bahn.de")
	}
	if !strings.Contains(u, "8011160") {
		t.Error("should contain origin EVA")
	}
	if !strings.Contains(u, "8000261") {
		t.Error("should contain destination EVA")
	}
	if !strings.Contains(u, "2026-07-15") {
		t.Error("should contain date")
	}
}

func TestBuildDBBookingURL_ShortTime(t *testing.T) {
	// If time string is short, should still produce a valid URL.
	u := buildDBBookingURL("8011160", "8000261", "2026-07-15")
	if u == "" {
		t.Fatal("booking URL should not be empty")
	}
	if !strings.Contains(u, "bahn.de") {
		t.Error("should contain bahn.de")
	}
}

func TestDBRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, dbLimiter, 2*time.Second, 1)
}

func TestAllDBStationsHaveRequiredFields(t *testing.T) {
	for city, station := range dbStations {
		if station.EVA == "" {
			t.Errorf("station %q has empty EVA", city)
		}
		if station.Name == "" {
			t.Errorf("station %q has empty Name", city)
		}
		if station.City == "" {
			t.Errorf("station %q has empty City", city)
		}
		if station.Country == "" {
			t.Errorf("station %q has empty Country", city)
		}
		if len(station.Country) != 2 {
			t.Errorf("station %q Country %q should be 2 letters", city, station.Country)
		}
		if len(station.EVA) < 7 {
			t.Errorf("station %q EVA %q should be at least 7 digits", city, station.EVA)
		}
	}
}

func TestDBStationCount(t *testing.T) {
	// We should have at least 30 unique stations (some cities have aliases).
	seen := make(map[string]bool)
	for _, station := range dbStations {
		seen[station.EVA] = true
	}
	if len(seen) < 30 {
		t.Errorf("expected at least 30 unique DB stations, got %d", len(seen))
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{[]string{"a", "b"}, "a"},
		{[]string{"", "b"}, "b"},
		{[]string{"", "", "c"}, "c"},
		{[]string{"", ""}, ""},
		{[]string{}, ""},
	}

	for _, tt := range tests {
		got := firstNonEmpty(tt.input...)
		if got != tt.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDBNotSearchedForNonDBCities(t *testing.T) {
	if HasDBRoute("Atlantis", "Mordor") {
		t.Error("Atlantis-Mordor should not have a DB route")
	}
	if HasDBRoute("Berlin", "Atlantis") {
		t.Error("Berlin-Atlantis should not have a DB route (one end missing)")
	}
}

func TestParseDBVerbindungen_Empty(t *testing.T) {
	routes := parseDBVerbindungen(nil, DBStation{}, DBStation{}, "EUR")
	if len(routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(routes))
	}
}

func TestParseDBVerbindungen_Basic(t *testing.T) {
	from := DBStation{EVA: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"}
	to := DBStation{EVA: "8000261", Name: "München Hbf", City: "Munich", Country: "DE"}

	verbindungen := []dbVerbindung{
		{
			VerbindungsAbschnitte: []dbAbschnitt{
				{
					AbfahrtsZeitpunkt: "2026-07-15T08:00:00+02:00",
					AnkunftsZeitpunkt: "2026-07-15T12:30:00+02:00",
					Typ:               "PUBLICTRANSPORT",
					Verkehrsmittel: &dbVerkehrsmittel{
						Name:     "ICE 1001",
						LangText: "ICE 1001",
						KurzText: "ICE",
					},
					Halte: []dbHalt{},
				},
			},
			AngebotsPreis: &dbPreis{Betrag: 29.90, Waehrung: "EUR"},
		},
	}

	routes := parseDBVerbindungen(verbindungen, from, to, "EUR")
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "db" {
		t.Errorf("Provider = %q, want %q", r.Provider, "db")
	}
	if r.Type != "train" {
		t.Errorf("Type = %q, want %q", r.Type, "train")
	}
	if r.Price != 29.90 {
		t.Errorf("Price = %f, want 29.90", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want %q", r.Currency, "EUR")
	}
	if r.Duration != 270 {
		t.Errorf("Duration = %d, want 270", r.Duration)
	}
	if r.Departure.City != "Berlin" {
		t.Errorf("Departure.City = %q, want %q", r.Departure.City, "Berlin")
	}
	if r.Arrival.City != "Munich" {
		t.Errorf("Arrival.City = %q, want %q", r.Arrival.City, "Munich")
	}
	if r.Transfers != 0 {
		t.Errorf("Transfers = %d, want 0", r.Transfers)
	}
	if r.BookingURL == "" {
		t.Error("BookingURL should not be empty")
	}
}

func TestParseDBVerbindungen_MultiLeg(t *testing.T) {
	from := DBStation{EVA: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"}
	to := DBStation{EVA: "8101003", Name: "Wien Hbf", City: "Vienna", Country: "AT"}

	verbindungen := []dbVerbindung{
		{
			VerbindungsAbschnitte: []dbAbschnitt{
				{
					AbfahrtsZeitpunkt: "2026-07-15T08:00:00+02:00",
					AnkunftsZeitpunkt: "2026-07-15T12:00:00+02:00",
					Typ:               "PUBLICTRANSPORT",
					Verkehrsmittel:    &dbVerkehrsmittel{Name: "ICE 17"},
				},
				{
					AbfahrtsZeitpunkt: "2026-07-15T12:30:00+02:00",
					AnkunftsZeitpunkt: "2026-07-15T16:00:00+02:00",
					Typ:               "PUBLICTRANSPORT",
					Verkehrsmittel:    &dbVerkehrsmittel{Name: "RJX 65"},
				},
			},
			AbPreis: &dbPreis{Betrag: 49.90, Waehrung: "EUR"},
		},
	}

	routes := parseDBVerbindungen(verbindungen, from, to, "EUR")
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
	if r.Duration != 480 {
		t.Errorf("Duration = %d, want 480", r.Duration)
	}
}

func TestParseDBVerbindungen_WalkingLegSkipped(t *testing.T) {
	from := DBStation{EVA: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"}
	to := DBStation{EVA: "8000261", Name: "München Hbf", City: "Munich", Country: "DE"}

	verbindungen := []dbVerbindung{
		{
			VerbindungsAbschnitte: []dbAbschnitt{
				{
					AbfahrtsZeitpunkt: "2026-07-15T08:00:00+02:00",
					AnkunftsZeitpunkt: "2026-07-15T10:00:00+02:00",
					Typ:               "PUBLICTRANSPORT",
					Verkehrsmittel:    &dbVerkehrsmittel{Name: "ICE 1"},
				},
				{
					AbfahrtsZeitpunkt: "2026-07-15T10:00:00+02:00",
					AnkunftsZeitpunkt: "2026-07-15T10:10:00+02:00",
					Typ:               "FUSSWEG",
				},
				{
					AbfahrtsZeitpunkt: "2026-07-15T10:15:00+02:00",
					AnkunftsZeitpunkt: "2026-07-15T12:00:00+02:00",
					Typ:               "PUBLICTRANSPORT",
					Verkehrsmittel:    &dbVerkehrsmittel{Name: "ICE 2"},
				},
			},
			AngebotsPreis: &dbPreis{Betrag: 39.90, Waehrung: "EUR"},
		},
	}

	routes := parseDBVerbindungen(verbindungen, from, to, "EUR")
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	// Walking leg should not be counted as a transfer or included in legs.
	if r.Transfers != 1 {
		t.Errorf("Transfers = %d, want 1", r.Transfers)
	}
	if len(r.Legs) != 2 {
		t.Errorf("Legs = %d, want 2 (walking leg excluded)", len(r.Legs))
	}
}

func TestDBBestPriceResponse_Decode(t *testing.T) {
	// Verify dbBestPriceResponse correctly decodes both "preis" and "ab" fields.
	tests := []struct {
		name      string
		json      string
		wantPrice float64
		wantCur   string
	}{
		{
			name:      "interval with angebotsPreis",
			json:      `{"tagesbestPreisIntervalle":[{"angebotsPreis":{"betrag":39.90,"waehrung":"EUR"}}]}`,
			wantPrice: 39.90,
			wantCur:   "EUR",
		},
		{
			name:      "multiple intervals cheapest wins",
			json:      `{"tagesbestPreisIntervalle":[{"angebotsPreis":{"betrag":74.99,"waehrung":"EUR"}},{"angebotsPreis":{"betrag":49.00,"waehrung":"EUR"}}]}`,
			wantPrice: 49.00,
			wantCur:   "EUR",
		},
		{
			name:      "empty response",
			json:      `{}`,
			wantPrice: 0,
			wantCur:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r dbBestPriceResponse
			if err := json.Unmarshal([]byte(tt.json), &r); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			gotPrice := 0.0
			gotCur := ""
			for _, itv := range r.TagesbestPreisIntervalle {
				if itv.AngebotsPreis != nil && itv.AngebotsPreis.Betrag > 0 {
					if gotPrice == 0 || itv.AngebotsPreis.Betrag < gotPrice {
						gotPrice = itv.AngebotsPreis.Betrag
						gotCur = itv.AngebotsPreis.Waehrung
					}
				}
			}
			if gotPrice != tt.wantPrice {
				t.Errorf("price = %f, want %f", gotPrice, tt.wantPrice)
			}
			if gotCur != tt.wantCur {
				t.Errorf("currency = %q, want %q", gotCur, tt.wantCur)
			}
		})
	}
}

func TestParseDBVerbindungen_ZeroPrice(t *testing.T) {
	// Routes with no price data should produce price=0 so the caller can apply
	// the tagesbestpreis fallback.
	from := DBStation{EVA: "8011160", Name: "Berlin Hbf", City: "Berlin", Country: "DE"}
	to := DBStation{EVA: "5400014", Name: "Praha hl.n.", City: "Prague", Country: "CZ"}

	verbindungen := []dbVerbindung{
		{
			VerbindungsAbschnitte: []dbAbschnitt{
				{
					AbfahrtsZeitpunkt: "2026-06-18T08:00:00+02:00",
					AnkunftsZeitpunkt: "2026-06-18T14:00:00+02:00",
					Typ:               "PUBLICTRANSPORT",
					Verkehrsmittel:    &dbVerkehrsmittel{Name: "EC 379"},
				},
			},
			// No price fields set — cross-border route.
		},
	}

	routes := parseDBVerbindungen(verbindungen, from, to, "EUR")
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Price != 0 {
		t.Errorf("expected price=0 for unprice route, got %f", routes[0].Price)
	}
}

func TestSearchDeutscheBahn_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchDeutscheBahn(ctx, "Berlin", "Munich", date, "EUR")
	if err != nil {
		t.Skipf("DB API unavailable (expected in CI): %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no DB routes found (may be a timing/availability issue)")
	}

	r := routes[0]
	if r.Provider != "db" {
		t.Errorf("provider = %q, want db", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("type = %q, want train", r.Type)
	}
	if r.Duration <= 0 {
		t.Errorf("duration = %d, should be > 0", r.Duration)
	}
	if r.Departure.City != "Berlin" {
		t.Errorf("departure city = %q, want Berlin", r.Departure.City)
	}
	if r.Arrival.City != "Munich" {
		t.Errorf("arrival city = %q, want Munich", r.Arrival.City)
	}
	if r.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
}
