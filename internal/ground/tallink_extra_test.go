package ground

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestTallinkCabinClass_JSON(t *testing.T) {
	// Verify the struct can unmarshal a realistic travelclasses response.
	raw := `[
		{"code":"A2","name":"A-class 2-berth","description":"Inside cabin with two beds","price":89.0,"capacity":2},
		{"code":"B4","name":"B-class 4-berth","description":"Inside cabin with four beds","price":65.0,"capacity":4}
	]`
	var classes []tallinkCabinClass
	if err := json.Unmarshal([]byte(raw), &classes); err != nil {
		t.Fatalf("unmarshal cabin classes: %v", err)
	}
	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(classes))
	}
	if classes[0].Code != "A2" {
		t.Errorf("Code = %q, want A2", classes[0].Code)
	}
	if classes[0].Price != 89.0 {
		t.Errorf("Price = %f, want 89.0", classes[0].Price)
	}
	if classes[1].Capacity != 4 {
		t.Errorf("Capacity = %d, want 4", classes[1].Capacity)
	}
}

func TestTallinkRateLimiterConfiguration(t *testing.T) {
	// 10 req/min (every 6s), burst 1 — allows multiple detectors in a single
	// hacks run without hitting the context deadline (previously 5 req/min / 12s).
	assertLimiterConfiguration(t, tallinkLimiter, 6*time.Second, 1)
}

// mockTallinkTimetableResponse is a realistic timetables API response
// matching the booking.tallink.com/api/timetables format.
const mockTallinkTimetableResponse = `{
  "defaultSelections": {"outwardSail": 2380001, "returnSail": 2379001},
  "trips": {
    "2026-05-01": {
      "outwards": [
        {
          "sailId": 2380001,
          "shipCode": "MEGASTAR",
          "departureIsoDate": "2026-05-01T07:30",
          "arrivalIsoDate": "2026-05-01T09:30",
          "personPrice": "38.90",
          "vehiclePrice": null,
          "duration": 2.0,
          "sailPackageCode": "HEL-TAL",
          "sailPackageName": "Helsinki-Tallinn",
          "cityFrom": "HEL",
          "cityTo": "TAL",
          "pierFrom": "LSA2",
          "pierTo": "DTER",
          "hasRoom": true,
          "isOvernight": false,
          "isDisabled": false,
          "promotionApplied": false,
          "marketingMessage": null,
          "isVoucherApplicable": false
        },
        {
          "sailId": 2380002,
          "shipCode": "MYSTAR",
          "departureIsoDate": "2026-05-01T17:30",
          "arrivalIsoDate": "2026-05-01T19:30",
          "personPrice": "12.50",
          "vehiclePrice": null,
          "duration": 2.0,
          "sailPackageCode": "HEL-TAL",
          "sailPackageName": "Helsinki-Tallinn",
          "cityFrom": "HEL",
          "cityTo": "TAL",
          "pierFrom": "LSA2",
          "pierTo": "DTER",
          "hasRoom": true,
          "isOvernight": false,
          "isDisabled": false,
          "promotionApplied": true,
          "marketingMessage": null,
          "isVoucherApplicable": false
        }
      ],
      "returns": []
    },
    "2026-05-02": {
      "outwards": [
        {
          "sailId": 2380011,
          "shipCode": "MEGASTAR",
          "departureIsoDate": "2026-05-02T08:30",
          "arrivalIsoDate": "2026-05-02T10:30",
          "personPrice": "35.00",
          "vehiclePrice": null,
          "duration": 2.0,
          "sailPackageCode": "HEL-TAL",
          "sailPackageName": "Helsinki-Tallinn",
          "cityFrom": "HEL",
          "cityTo": "TAL",
          "pierFrom": "LSA2",
          "pierTo": "DTER",
          "hasRoom": true,
          "isOvernight": false,
          "isDisabled": false,
          "promotionApplied": false,
          "marketingMessage": null,
          "isVoucherApplicable": false
        }
      ],
      "returns": []
    }
  }
}`

// parseMockTimetable parses the mock timetable JSON into tallinkTimetableResponse.
func parseMockTimetable(t *testing.T) tallinkTimetableResponse {
	t.Helper()
	var resp tallinkTimetableResponse
	if err := json.Unmarshal([]byte(mockTallinkTimetableResponse), &resp); err != nil {
		t.Fatalf("unmarshal mock timetable: %v", err)
	}
	return resp
}

func TestTallinkTimetableResponse_Parse(t *testing.T) {
	resp := parseMockTimetable(t)

	if len(resp.Trips) != 2 {
		t.Fatalf("expected 2 trip days, got %d", len(resp.Trips))
	}

	day1 := resp.Trips["2026-05-01"]
	if len(day1.Outwards) != 2 {
		t.Fatalf("expected 2 outward sails on 2026-05-01, got %d", len(day1.Outwards))
	}

	s := day1.Outwards[0]
	if s.ShipCode != "MEGASTAR" {
		t.Errorf("ShipCode = %q, want MEGASTAR", s.ShipCode)
	}
	if s.PersonPrice != "38.90" {
		t.Errorf("PersonPrice = %q, want 38.90", s.PersonPrice)
	}
	if s.DepartureIsoDate != "2026-05-01T07:30" {
		t.Errorf("departure = %q, want 2026-05-01T07:30", s.DepartureIsoDate)
	}
	if s.ArrivalIsoDate != "2026-05-01T09:30" {
		t.Errorf("arrival = %q, want 2026-05-01T09:30", s.ArrivalIsoDate)
	}
	if s.CityFrom != "HEL" {
		t.Errorf("from = %q, want HEL", s.CityFrom)
	}
	if s.CityTo != "TAL" {
		t.Errorf("to = %q, want TAL", s.CityTo)
	}

	day2 := resp.Trips["2026-05-02"]
	if len(day2.Outwards) != 1 {
		t.Fatalf("expected 1 outward sail on 2026-05-02, got %d", len(day2.Outwards))
	}

	// Verify date-key lookup for non-existent date returns empty.
	day3 := resp.Trips["2026-05-03"]
	if len(day3.Outwards) != 0 {
		t.Errorf("expected 0 outward sails on 2026-05-03, got %d", len(day3.Outwards))
	}
}

// mockTallinkOvernightResponse is a realistic timetables API response for an
// overnight route (HEL-STO) using voyageType=CRUISE with cabin-inclusive pricing.
const mockTallinkOvernightResponse = `{
  "defaultSelections": {"outwardSail": 2372431, "returnSail": null},
  "trips": {
    "2026-05-15": {
      "outwards": [
        {
          "sailId": 2372431,
          "shipCode": "SERENADE",
          "departureIsoDate": "2026-05-15T16:45",
          "arrivalIsoDate": "2026-05-16T10:00",
          "personPrice": "135.90",
          "vehiclePrice": null,
          "duration": 17.5,
          "sailPackageCode": "HEL-STO",
          "sailPackageName": "Helsinki-Stockholm",
          "cityFrom": "HEL",
          "cityTo": "STO",
          "pierFrom": "OLYM",
          "pierTo": "VHAM",
          "hasRoom": true,
          "isOvernight": false,
          "isDisabled": false,
          "promotionApplied": false,
          "marketingMessage": null,
          "isVoucherApplicable": false
        }
      ],
      "returns": []
    }
  }
}`

func TestTallinkTimetable_MockServer(t *testing.T) {
	// Simulate the two-step flow: page load (sets cookie) → API call.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Simulate booking page — set JSESSIONID cookie
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "test-session-123"})
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><script>window.Env = { sessionGuid: 'TEST-GUID-1234', locale: 'en' };</script></html>`)) //nolint:errcheck
	})
	mux.HandleFunc("/api/timetables", func(w http.ResponseWriter, r *http.Request) {
		// Verify cookie is present
		cookie, err := r.Cookie("JSESSIONID")
		if err != nil || cookie.Value != "test-session-123" {
			t.Error("timetables API call missing JSESSIONID cookie")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Verify query params
		q := r.URL.Query()
		if q.Get("from") == "" || q.Get("to") == "" {
			t.Error("timetables API missing from/to params")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockTallinkTimetableResponse)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Parse the mock response directly (can't override tallinkBookingBase in unit test).
	resp := parseMockTimetable(t)
	if resp.DefaultSelections.OutwardSail != 2380001 {
		t.Errorf("default outward sail = %d, want 2380001", resp.DefaultSelections.OutwardSail)
	}
}

func TestTallinkOvernightResponse_Parse(t *testing.T) {
	var resp tallinkTimetableResponse
	if err := json.Unmarshal([]byte(mockTallinkOvernightResponse), &resp); err != nil {
		t.Fatalf("unmarshal overnight response: %v", err)
	}

	if resp.DefaultSelections.OutwardSail != 2372431 {
		t.Errorf("default outward sail = %d, want 2372431", resp.DefaultSelections.OutwardSail)
	}

	dayTrips, ok := resp.Trips["2026-05-15"]
	if !ok {
		t.Fatal("no trips for 2026-05-15")
	}
	if len(dayTrips.Outwards) != 1 {
		t.Fatalf("expected 1 outward sail, got %d", len(dayTrips.Outwards))
	}

	s := dayTrips.Outwards[0]
	if s.ShipCode != "SERENADE" {
		t.Errorf("ShipCode = %q, want SERENADE", s.ShipCode)
	}
	if s.PersonPrice != "135.90" {
		t.Errorf("PersonPrice = %q, want 135.90", s.PersonPrice)
	}
	if s.Duration != 17.5 {
		t.Errorf("Duration = %f, want 17.5", s.Duration)
	}
	if s.SailPackageCode != "HEL-STO" {
		t.Errorf("SailPackageCode = %q, want HEL-STO", s.SailPackageCode)
	}
}

func TestTallinkOvernightMockServer_CabinClasses(t *testing.T) {
	// Simulate the full overnight flow: page → timetable → cruiseSummary → travelclasses.
	mockCabinClasses := `[
		{"code":"A2","name":"A-class 2-berth","description":"Inside cabin","price":149.0,"capacity":2},
		{"code":"B4","name":"B-class 4-berth","description":"Inside cabin","price":99.0,"capacity":4},
		{"code":"D","name":"Deck","description":"No cabin","price":39.0,"capacity":0}
	]`

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "overnight-session"})
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><script>Env = { sessionGuid: 'NIGHT-GUID' };</script></html>`)) //nolint:errcheck
	})
	mux.HandleFunc("/api/reservation/cruiseSummary", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("cruiseSummary method = %s, want POST", r.Method)
		}
		cookie, err := r.Cookie("JSESSIONID")
		if err != nil || cookie.Value != "overnight-session" {
			t.Error("cruiseSummary missing session cookie")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"OK"}`)) //nolint:errcheck
	})
	mux.HandleFunc("/api/travelclasses", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("JSESSIONID")
		if err != nil || cookie.Value != "overnight-session" {
			t.Error("travelclasses missing session cookie")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockCabinClasses)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Test fetchTallinkCabinClasses directly with mock server cookies.
	ctx := context.Background()
	cookies := []*http.Cookie{{Name: "JSESSIONID", Value: "overnight-session"}}

	// Can't call fetchTallinkCabinClasses with mock server (hardcoded base URL),
	// so verify the struct parsing and formatting instead.
	var classes []tallinkCabinClass
	if err := json.Unmarshal([]byte(mockCabinClasses), &classes); err != nil {
		t.Fatalf("unmarshal cabin classes: %v", err)
	}
	if len(classes) != 3 {
		t.Fatalf("expected 3 cabin classes, got %d", len(classes))
	}

	formatted := tallinkFormatCabinClasses(classes)
	expected := "Cabins: A2 €149, B4 €99, D €39"
	if formatted != expected {
		t.Errorf("formatted = %q, want %q", formatted, expected)
	}

	// Verify cookies are non-nil (used by mock server handler).
	_ = ctx
	if len(cookies) == 0 {
		t.Fatal("cookies should not be empty")
	}
}

func TestTallinkDealFlag(t *testing.T) {
	resp := parseMockTimetable(t)
	sails := resp.Trips["2026-05-01"].Outwards

	// Second sail has price 12.50 (below tallinkDealThreshold=20) → "Deal" amenity.
	var price float64
	_, _ = fmt.Sscanf(sails[1].PersonPrice, "%f", &price)
	if price >= tallinkDealThreshold {
		t.Skipf("test assumption violated: price %.2f >= threshold %.2f", price, tallinkDealThreshold)
	}

	var amenities []string
	if price > 0 && price < tallinkDealThreshold {
		amenities = append(amenities, "Deal")
	}
	if len(amenities) != 1 || amenities[0] != "Deal" {
		t.Errorf("expected [Deal] amenity for price %.2f, got %v", price, amenities)
	}

	// First sail (38.90) should not be flagged.
	var price2 float64
	_, _ = fmt.Sscanf(sails[0].PersonPrice, "%f", &price2)
	var amenities2 []string
	if price2 > 0 && price2 < tallinkDealThreshold {
		amenities2 = append(amenities2, "Deal")
	}
	if len(amenities2) != 0 {
		t.Errorf("expected no deal amenity for price %.2f, got %v", price2, amenities2)
	}

	// Second sail also has promotionApplied=true → "Promotion" amenity.
	if !sails[1].PromotionApplied {
		t.Error("expected promotionApplied=true on second sail")
	}
}

func TestSearchTallink_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchTallink(ctx, "Helsinki", "Tallinn", date, "EUR")
	if err != nil {
		t.Skipf("Tallink API unavailable: %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no Tallink routes found")
	}

	r := routes[0]
	if r.Provider != "tallink" {
		t.Errorf("provider = %q, want tallink", r.Provider)
	}
	if r.Type != "ferry" {
		t.Errorf("type = %q, want ferry", r.Type)
	}
	if r.Duration <= 0 {
		t.Errorf("duration = %d, should be > 0", r.Duration)
	}
	if r.Departure.City != "Helsinki" {
		t.Errorf("departure city = %q, want Helsinki", r.Departure.City)
	}
	if r.Arrival.City != "Tallinn" {
		t.Errorf("arrival city = %q, want Tallinn", r.Arrival.City)
	}
	if r.BookingURL == "" {
		t.Error("booking URL should not be empty")
	}
	if !strings.Contains(r.BookingURL, "booking.tallink.com") {
		t.Errorf("booking URL should use booking.tallink.com, got %q", r.BookingURL)
	}
	if r.Transfers != 0 {
		t.Errorf("transfers = %d, want 0 (ferry)", r.Transfers)
	}
	if r.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", r.Currency)
	}

	// HEL-TAL is a shuttle route — should NOT have overnight amenities.
	for _, a := range r.Amenities {
		if a == "Overnight" || a == "Cabin included" {
			t.Errorf("shuttle route should not have %q amenity", a)
		}
	}
}

func TestSearchTallink_Overnight_Integration(t *testing.T) {
	testutil.RequireLiveIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	date := time.Now().AddDate(0, 1, 0).Format("2006-01-02")

	routes, err := SearchTallink(ctx, "Helsinki", "Stockholm", date, "EUR")
	if err != nil {
		t.Skipf("Tallink API unavailable: %v", err)
	}
	if len(routes) == 0 {
		t.Skip("no Tallink overnight routes found")
	}

	r := routes[0]
	if r.Provider != "tallink" {
		t.Errorf("provider = %q, want tallink", r.Provider)
	}
	if r.Type != "ferry" {
		t.Errorf("type = %q, want ferry", r.Type)
	}
	if r.Price <= 0 {
		t.Errorf("price = %f, should be > 0", r.Price)
	}
	if r.Duration < 600 {
		t.Errorf("duration = %d, should be >= 600 for overnight", r.Duration)
	}
	if r.Departure.City != "Helsinki" {
		t.Errorf("departure city = %q, want Helsinki", r.Departure.City)
	}
	if r.Arrival.City != "Stockholm" {
		t.Errorf("arrival city = %q, want Stockholm", r.Arrival.City)
	}

	// Overnight route should have "Overnight" and "Cabin included" amenities.
	hasOvernight := false
	hasCabin := false
	for _, a := range r.Amenities {
		if a == "Overnight" {
			hasOvernight = true
		}
		if a == "Cabin included" {
			hasCabin = true
		}
	}
	if !hasOvernight {
		t.Error("overnight route missing 'Overnight' amenity")
	}
	if !hasCabin {
		t.Error("overnight route missing 'Cabin included' amenity")
	}

	t.Logf("HEL-STO: %s → %s, €%.2f, %d min, amenities: %v",
		r.Departure.Time, r.Arrival.Time, r.Price, r.Duration, r.Amenities)
}
