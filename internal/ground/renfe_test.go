package ground

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func TestLookupRenfeStation(t *testing.T) {
	tests := []struct {
		city     string
		wantCode string
		wantCity string
		wantOK   bool
	}{
		{"Madrid", "MAD", "Madrid", true},
		{"madrid", "MAD", "Madrid", true},
		{"  Madrid  ", "MAD", "Madrid", true},
		{"Barcelona", "BCN", "Barcelona", true},
		{"Seville", "SVQ", "Seville", true},
		{"Sevilla", "SVQ", "Seville", true},
		{"Valencia", "VLC", "Valencia", true},
		{"Malaga", "AGP", "Malaga", true},
		{"San Sebastian", "EAS", "San Sebastian", true},
		{"Donostia", "EAS", "San Sebastian", true},
		{"Paris", "PAR", "Paris", true},
		{"Marseille", "MRS", "Marseille", true},
		{"Lyon", "LYS", "Lyon", true},
		{"unknown", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		s, ok := LookupRenfeStation(tt.city)
		if ok != tt.wantOK {
			t.Errorf("LookupRenfeStation(%q): ok = %v, want %v", tt.city, ok, tt.wantOK)
			continue
		}
		if ok && s.Code != tt.wantCode {
			t.Errorf("LookupRenfeStation(%q).Code = %q, want %q", tt.city, s.Code, tt.wantCode)
		}
		if ok && s.City != tt.wantCity {
			t.Errorf("LookupRenfeStation(%q).City = %q, want %q", tt.city, s.City, tt.wantCity)
		}
	}
}

func TestHasRenfeRoute(t *testing.T) {
	tests := []struct {
		from, to string
		want     bool
	}{
		{"Madrid", "Barcelona", true},  // both Spanish
		{"Madrid", "Paris", true},      // one Spanish, one French
		{"Paris", "Madrid", true},      // reversed
		{"Paris", "Lyon", false},       // both French, no Spanish station
		{"London", "Paris", false},     // London not in Renfe
		{"unknown", "Madrid", false},   // unknown origin
		{"Madrid", "unknown", false},   // unknown destination
		{"unknown", "unknown2", false}, // both unknown
	}

	for _, tt := range tests {
		got := HasRenfeRoute(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("HasRenfeRoute(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestBuildRenfeBookingURL(t *testing.T) {
	from := RenfeStation{Numeric: "60000"}
	to := RenfeStation{Numeric: "71801"}
	url := buildRenfeBookingURL(from, to, "2026-06-15")

	if !strings.Contains(url, "venta.renfe.com") {
		t.Errorf("URL should contain venta.renfe.com, got %q", url)
	}
	if !strings.Contains(url, "60000") {
		t.Errorf("URL should contain origin numeric ID, got %q", url)
	}
	if !strings.Contains(url, "71801") {
		t.Errorf("URL should contain destination numeric ID, got %q", url)
	}
	if !strings.Contains(url, "2026-06-15") {
		t.Errorf("URL should contain date, got %q", url)
	}
	if !strings.Contains(url, "tipoBusqueda=ida") {
		t.Errorf("URL should contain tipoBusqueda=ida, got %q", url)
	}
}

func TestRenfeAllStationsHaveRequiredFields(t *testing.T) {
	for alias, station := range renfeStations {
		if station.Code == "" {
			t.Errorf("station alias %q has empty Code", alias)
		}
		if station.Name == "" {
			t.Errorf("station alias %q has empty Name", alias)
		}
		if station.City == "" {
			t.Errorf("station alias %q has empty City", alias)
		}
		if station.Country == "" {
			t.Errorf("station alias %q has empty Country", alias)
		}
	}
}

func TestRenfeSpanishStationsHaveNumericID(t *testing.T) {
	for alias, station := range renfeStations {
		if station.Country == "ES" && station.Numeric == "" {
			t.Errorf("Spanish station %q has empty Numeric ID", alias)
		}
	}
}

func TestRenfePriceCalendarRequestMarshal(t *testing.T) {
	req := renfePriceCalendarRequest{
		OriginID:     "60000",
		DestinyID:    "71801",
		InitDate:     "2026-06-15",
		EndDate:      "2026-06-15",
		SalesChannel: renfeSalesChannel{CodApp: "VLP"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["originId"] != "60000" {
		t.Errorf("originId = %v, want 60000", m["originId"])
	}
	if m["destinyId"] != "71801" {
		t.Errorf("destinyId = %v, want 71801", m["destinyId"])
	}
	if m["initDate"] != "2026-06-15" {
		t.Errorf("initDate = %v, want 2026-06-15", m["initDate"])
	}
	sc, ok := m["salesChannel"].(map[string]any)
	if !ok {
		t.Fatal("salesChannel not a map")
	}
	if sc["codApp"] != "VLP" {
		t.Errorf("codApp = %v, want VLP", sc["codApp"])
	}
}

const mockRenfePriceCalendarResponse = `{
  "origin": {"name": "Madrid Puerta de Atocha", "extId": "60000"},
  "destination": {"name": "Barcelona Sants", "extId": "71801"},
  "journeysPriceCalendar": [
    {"date": "2026-06-15", "minPriceAvailable": true, "minPrice": 25.50},
    {"date": "2026-06-16", "minPriceAvailable": true, "minPrice": 32.00},
    {"date": "2026-06-17", "minPriceAvailable": false, "minPrice": 0}
  ]
}`

func TestRenfePriceCalendarResponseParse(t *testing.T) {
	var resp renfePriceCalendarResponse
	if err := json.Unmarshal([]byte(mockRenfePriceCalendarResponse), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Origin.Name != "Madrid Puerta de Atocha" {
		t.Errorf("origin name = %q", resp.Origin.Name)
	}
	if resp.Origin.ExtID != "60000" {
		t.Errorf("origin extId = %q", resp.Origin.ExtID)
	}
	if resp.Destination.Name != "Barcelona Sants" {
		t.Errorf("destination name = %q", resp.Destination.Name)
	}

	if len(resp.Journeys) != 3 {
		t.Fatalf("expected 3 journeys, got %d", len(resp.Journeys))
	}

	j0 := resp.Journeys[0]
	if j0.Date != "2026-06-15" {
		t.Errorf("journey[0].Date = %q", j0.Date)
	}
	if !j0.MinPriceAvailable {
		t.Error("journey[0] should have minPriceAvailable=true")
	}
	if j0.MinPrice != 25.50 {
		t.Errorf("journey[0].MinPrice = %v, want 25.50", j0.MinPrice)
	}

	j2 := resp.Journeys[2]
	if j2.MinPriceAvailable {
		t.Error("journey[2] should have minPriceAvailable=false")
	}
}

func TestRenfeRateLimiterConfiguration(t *testing.T) {
	assertLimiterConfiguration(t, renfeLimiter, 12*time.Second, 1)
}

func TestRenfeDuplicateAliasesResolveSameStation(t *testing.T) {
	s1, ok1 := LookupRenfeStation("Seville")
	s2, ok2 := LookupRenfeStation("Sevilla")
	if !ok1 || !ok2 {
		t.Fatal("both Seville and Sevilla should resolve")
	}
	if s1.Code != s2.Code {
		t.Errorf("Seville.Code=%q != Sevilla.Code=%q", s1.Code, s2.Code)
	}

	s3, ok3 := LookupRenfeStation("San Sebastian")
	s4, ok4 := LookupRenfeStation("Donostia")
	if !ok3 || !ok4 {
		t.Fatal("both San Sebastian and Donostia should resolve")
	}
	if s3.Code != s4.Code {
		t.Errorf("San Sebastian.Code=%q != Donostia.Code=%q", s3.Code, s4.Code)
	}
}

// TestSearchRenfe_CalendarMinimumIsEstimated replays the live price-calendar
// answer captured 2026-11-15. It holds one day minimum and no train times, so
// the route must not invent a departure, must be estimated, and keeps Renfe's
// own currency (EUR) whatever currency the caller asked for.
func TestSearchRenfe_CalendarMinimumIsEstimated(t *testing.T) {
	origClient, origLimiter := renfeClient, renfeLimiter
	t.Cleanup(func() { renfeClient, renfeLimiter = origClient, origLimiter })
	renfeLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	fixture := loadGroundFixture(t, "renfe_madrid_barcelona_calendar.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()
	renfeClient = &http.Client{Transport: &redirectTransport{target: srv.URL}, Timeout: 5 * time.Second}

	routes, err := SearchRenfe(context.Background(), "Madrid", "Barcelona", "2026-11-20", "SEK")
	if err != nil {
		t.Fatalf("SearchRenfe: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1 day-minimum route", len(routes))
	}
	r := routes[0]
	if r.Price != 32 {
		t.Errorf("price = %v, want 32", r.Price)
	}
	if r.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR (Renfe's own, not the requested SEK)", r.Currency)
	}
	if r.PriceProvenance != models.PriceEstimated {
		t.Errorf("provenance = %q, want %q for a calendar day minimum", r.PriceProvenance, models.PriceEstimated)
	}
	if r.Departure.Time != "" {
		t.Errorf("departure time = %q, want empty: the calendar carries no train times", r.Departure.Time)
	}
	if r.Arrival.Time != "" {
		t.Errorf("arrival time = %q, want empty: the calendar carries no train times", r.Arrival.Time)
	}
}

func TestSearchRenfe_NotApplicableWrapsSentinel(t *testing.T) {
	for _, pair := range [][2]string{
		{"Narnia", "Barcelona"},
		{"Madrid", "Narnia"},
		{"Madrid", "Paris"}, // Paris has no numeric ID the calendar API accepts
	} {
		_, err := SearchRenfe(context.Background(), pair[0], pair[1], "2026-11-20", "EUR")
		if !errors.Is(err, models.ErrNotApplicable) {
			t.Errorf("%s -> %s: error %v does not wrap models.ErrNotApplicable", pair[0], pair[1], err)
		}
	}
}
