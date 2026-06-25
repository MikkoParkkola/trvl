package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// readGoogleRoomDetailFixture loads the representative (SCHEMA NOT VERIFIED)
// room-detail payload used by the offline parse tests.
func readGoogleRoomDetailFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/google_room_detail.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func TestGoogleRoomDetailConfigured(t *testing.T) {
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", "")
	if googleRoomDetailConfigured() {
		t.Fatal("expected unconfigured when GOOGLE_HOTELS_DETAIL_API_BASE is empty")
	}
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", "https://relay.example/google-hotels")
	if !googleRoomDetailConfigured() {
		t.Fatal("expected configured when GOOGLE_HOTELS_DETAIL_API_BASE is set")
	}
}

func TestTryGoogleRoomDetailUnconfiguredIsNoOp(t *testing.T) {
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", "")
	// Enable for this test (TestMain disables it globally) so we prove the no-op
	// comes from the missing base URL, not the test gate.
	prev := googleRoomDetailEnabled
	googleRoomDetailEnabled = true
	defer func() { googleRoomDetailEnabled = prev }()

	rooms, name, notice := tryGoogleRoomDetail(context.Background(), RoomSearchOptions{
		HotelID: "0x123:0x456", CheckIn: "2026-07-25", CheckOut: "2026-07-26", Currency: "EUR",
	})
	if rooms != nil || name != "" || notice != "" {
		t.Fatalf("unconfigured fetch should be a silent no-op, got rooms=%d name=%q notice=%q", len(rooms), name, notice)
	}
}

func TestTryGoogleRoomDetailDisabledIsNoOp(t *testing.T) {
	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", "https://relay.example/google-hotels")
	// googleRoomDetailEnabled is false in the suite (TestMain); the gate must win.
	rooms, name, notice := tryGoogleRoomDetail(context.Background(), RoomSearchOptions{
		HotelID: "0x123:0x456", CheckIn: "2026-07-25", CheckOut: "2026-07-26", Currency: "EUR",
	})
	if rooms != nil || name != "" || notice != "" {
		t.Fatalf("disabled fetch should be a silent no-op, got rooms=%d name=%q notice=%q", len(rooms), name, notice)
	}
}

func TestParseGoogleRoomDetail_RoomLevelAndLeadIn(t *testing.T) {
	rooms, hotelName := parseGoogleRoomDetail(readGoogleRoomDetailFixture(t), "EUR")
	if hotelName != "Hôtel des Grands Boulevards" {
		t.Errorf("hotelName = %q", hotelName)
	}
	// Three price-bearing rooms; the sold-out stub (no price) is skipped.
	if len(rooms) != 3 {
		t.Fatalf("expected 3 priced rooms, got %d", len(rooms))
	}

	// Room 0: structured nightly rate + nameable identity -> exact room-level.
	r0 := rooms[0]
	if r0.Name != "Deluxe Double Room" {
		t.Errorf("r0 name = %q", r0.Name)
	}
	if r0.Price != 214.0 || r0.NightlyPrice != 214.0 || r0.TotalPrice != 428.0 {
		t.Errorf("r0 price/nightly/total = %v/%v/%v", r0.Price, r0.NightlyPrice, r0.TotalPrice)
	}
	if r0.Currency != "EUR" {
		t.Errorf("r0 currency = %q", r0.Currency)
	}
	if r0.Provider != "Google Hotels" {
		t.Errorf("r0 provider = %q, want Google Hotels", r0.Provider)
	}
	if r0.MatchConfidence != models.RoomInventoryMatchExact {
		t.Errorf("r0 match = %q, want exact", r0.MatchConfidence)
	}
	// Relative provider link is made absolute against the Google host.
	if want := googleHotelsDefaultHost + "/travel/clk/bkng?room=deluxe-double"; r0.ProviderURL != want {
		t.Errorf("r0 providerURL = %q, want %q", r0.ProviderURL, want)
	}
	// A structured nightly rate must carry room-level (or better) confidence.
	q0 := roomInventoryQuote(r0)
	if q0.PriceConfidence != models.PriceConfidenceRoomLevel && q0.PriceConfidence != models.PriceConfidenceVerified {
		t.Errorf("r0 quote confidence = %q, want room_level/verified", q0.PriceConfidence)
	}
	if q0.ProviderURL == "" {
		t.Error("r0 quote must carry a ProviderURL")
	}

	// Room 1: nightly rate, absolute provider link preserved.
	r1 := rooms[1]
	if r1.Name != "Classic Queen Room" || r1.Price != 178.0 {
		t.Errorf("r1 = %q %v", r1.Name, r1.Price)
	}
	if r1.ProviderURL != "https://www.expedia.com/clk/grands-boulevards-queen" {
		t.Errorf("r1 providerURL = %q", r1.ProviderURL)
	}
	if r1.MatchConfidence != models.RoomInventoryMatchExact {
		t.Errorf("r1 match = %q, want exact", r1.MatchConfidence)
	}

	// Room 2: lead-in only (no room name, no structured nightly) -> property level.
	r2 := rooms[2]
	if r2.Name != "Standard Room" {
		t.Errorf("r2 name = %q, want Standard Room", r2.Name)
	}
	if r2.Price != 165.0 {
		t.Errorf("r2 price = %v", r2.Price)
	}
	if r2.MatchConfidence != models.RoomInventoryMatchPropertyLevelOnly {
		t.Errorf("r2 match = %q, want property_level_only", r2.MatchConfidence)
	}
	// Protocol-relative provider link gets an https scheme.
	if r2.ProviderURL != "https://agoda.example/clk/grands-boulevards" {
		t.Errorf("r2 providerURL = %q", r2.ProviderURL)
	}
	// A lead-in must NOT be promoted to a verified/room-level quote.
	q2 := roomInventoryQuote(r2)
	if q2.PriceConfidence != models.PriceConfidenceUnverified {
		t.Errorf("r2 quote confidence = %q, want unverified", q2.PriceConfidence)
	}
}

// TestGoogleRoomDetailLeadsOverPartnerLeadIn proves the merged bookability sort
// surfaces a real room-level Google rate ABOVE an existing property-level
// lead-in (the headline price). This is the core behaviour the operator asked
// for: "lead with proper price".
func TestGoogleRoomDetailLeadsOverPartnerLeadIn(t *testing.T) {
	detailRooms, _ := parseGoogleRoomDetail(readGoogleRoomDetailFixture(t), "EUR")
	// A pre-existing Google partner lead-in (property level only).
	leadIn := []RoomType{{
		Name:            "Standard Room",
		Price:           199.0,
		NightlyPrice:    199.0,
		Currency:        "EUR",
		Provider:        "Google Hotels",
		MatchConfidence: models.RoomInventoryMatchPropertyLevelOnly,
	}}

	merged := mergeRoomTypes(leadIn, detailRooms)
	sortRoomsByBookability(merged)

	if len(merged) == 0 {
		t.Fatal("expected merged rooms")
	}
	top := merged[0]
	if top.MatchConfidence != models.RoomInventoryMatchExact {
		t.Fatalf("sort must lead with a room-level rate, got match=%q price=%v", top.MatchConfidence, top.Price)
	}
	// Cheapest exact room (Classic Queen, 178) leads, not the 199 lead-in.
	if top.Price != 178.0 {
		t.Errorf("expected cheapest room-level rate (178) to lead, got %v", top.Price)
	}
}

func TestTryGoogleRoomDetailConfiguredAgainstMock(t *testing.T) {
	fixture := readGoogleRoomDetailFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", srv.URL)
	prev := googleRoomDetailEnabled
	googleRoomDetailEnabled = true
	defer func() { googleRoomDetailEnabled = prev }()

	rooms, name, notice := tryGoogleRoomDetail(context.Background(), RoomSearchOptions{
		HotelID:  "0x123:0x456",
		CheckIn:  "2026-07-25",
		CheckOut: "2026-07-27",
		Guests:   2,
		Currency: "EUR",
	})
	if notice != "" {
		t.Fatalf("happy path should carry no notice, got %q", notice)
	}
	if name != "Hôtel des Grands Boulevards" {
		t.Errorf("name = %q", name)
	}
	if len(rooms) != 3 {
		t.Fatalf("expected 3 rooms from mock, got %d", len(rooms))
	}
}

func TestTryGoogleRoomDetailBotWallClassifiesRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<html>challenge</html>"))
	}))
	defer srv.Close()

	t.Setenv("GOOGLE_HOTELS_DETAIL_API_BASE", srv.URL)
	prev := googleRoomDetailEnabled
	googleRoomDetailEnabled = true
	defer func() { googleRoomDetailEnabled = prev }()

	rooms, _, notice := tryGoogleRoomDetail(context.Background(), RoomSearchOptions{
		HotelID: "0x123:0x456", CheckIn: "2026-07-25", CheckOut: "2026-07-26", Currency: "EUR",
	})
	if len(rooms) != 0 {
		t.Fatalf("bot-wall should yield no rooms, got %d", len(rooms))
	}
	// A 429 is retryable: surface an honest Notice, never a fabricated empty.
	if notice == "" {
		t.Fatal("expected a retryable rate-limit Notice on a 429 bot-wall")
	}
}

func TestGoogleRoomDetailBookingURLFallback(t *testing.T) {
	if got := googleRoomDetailBookingURL(googleRoomDetailRoom{}); got != googleHotelsDefaultHost+"/travel/hotels" {
		t.Errorf("empty link should fall back to host, got %q", got)
	}
	if got := googleRoomDetailBookingURL(googleRoomDetailRoom{ProviderURL: "//cdn.example/x"}); got != "https://cdn.example/x" {
		t.Errorf("protocol-relative = %q", got)
	}
}

// TestParseGoogleRoomDetailMalformedIsSafe proves a non-JSON payload degrades to
// an empty result, never a panic — the same no-op-safety contract the other
// providers honour.
func TestParseGoogleRoomDetailMalformedIsSafe(t *testing.T) {
	rooms, name := parseGoogleRoomDetail([]byte("<html>not json</html>"), "EUR")
	if rooms != nil || name != "" {
		t.Fatalf("malformed payload should yield empty result, got rooms=%d name=%q", len(rooms), name)
	}
}
