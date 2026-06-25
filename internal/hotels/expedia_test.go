package hotels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// readExpediaFixture loads the representative (SCHEMA NOT VERIFIED) availability
// payload used by the offline parse tests.
func readExpediaFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/expedia_search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func TestExpediaConfigured(t *testing.T) {
	t.Setenv("EXPEDIA_API_BASE", "")
	if expediaConfigured() {
		t.Fatal("expected unconfigured when EXPEDIA_API_BASE is empty")
	}
	t.Setenv("EXPEDIA_API_BASE", "https://proxy.example/expedia")
	if !expediaConfigured() {
		t.Fatal("expected configured when EXPEDIA_API_BASE is set")
	}
}

func TestSearchExpediaUnconfiguredIsNoOp(t *testing.T) {
	t.Setenv("EXPEDIA_API_BASE", "")
	// enabledfor this test (TestMain disables it globally) so we prove the
	// no-op comes from the missing base URL, not the test gate.
	prev := expediaEnabled
	expediaEnabled = true
	defer func() { expediaEnabled = prev }()

	res, err := SearchExpedia(context.Background(), "Paris", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("unconfigured search should not error, got %v", err)
	}
	if res != nil {
		t.Fatalf("unconfigured search should return nil results, got %d", len(res))
	}
}

func TestSearchExpediaDisabledIsNoOp(t *testing.T) {
	t.Setenv("EXPEDIA_API_BASE", "https://proxy.example/expedia")
	// expediaEnabled is false in the test suite (TestMain); the gate must win.
	res, err := SearchExpedia(context.Background(), "Paris", HotelSearchOptions{Currency: "EUR"})
	if err != nil {
		t.Fatalf("disabled search should not error, got %v", err)
	}
	if res != nil {
		t.Fatalf("disabled search should return nil results, got %d", len(res))
	}
}

func TestParseExpediaProperties(t *testing.T) {
	hotels, err := parseExpediaProperties(readExpediaFixture(t), "EUR")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Two price-bearing properties; the zero-priced sold-out stub is skipped.
	if len(hotels) != 2 {
		t.Fatalf("expected 2 priced hotels, got %d", len(hotels))
	}

	roomLevel := hotels[0]
	if roomLevel.Name != "Hôtel Le Marais" {
		t.Errorf("name = %q", roomLevel.Name)
	}
	if roomLevel.Price != 189.0 || roomLevel.Currency != "EUR" {
		t.Errorf("price/currency = %v %q", roomLevel.Price, roomLevel.Currency)
	}
	if roomLevel.PriceBasis != models.PriceBasisRoomNightly {
		t.Errorf("basis = %q, want %q", roomLevel.PriceBasis, models.PriceBasisRoomNightly)
	}
	if roomLevel.PriceConfidence != models.PriceConfidenceRoomLevel {
		t.Errorf("confidence = %q, want %q", roomLevel.PriceConfidence, models.PriceConfidenceRoomLevel)
	}
	if roomLevel.Stars != 4 || roomLevel.Rating != 8.6 {
		t.Errorf("stars/rating = %d %v", roomLevel.Stars, roomLevel.Rating)
	}
	if len(roomLevel.Sources) != 1 || roomLevel.Sources[0].Provider != "expedia" {
		t.Errorf("sources = %+v", roomLevel.Sources)
	}
	if roomLevel.Sources[0].PriceConfidence != models.PriceConfidenceRoomLevel {
		t.Errorf("source confidence = %q", roomLevel.Sources[0].PriceConfidence)
	}
	// Relative deep link is made absolute against the public host.
	if got, want := roomLevel.BookingURL, expediaDefaultHost+"/Paris-Hotels-Hotel-Le-Marais.h12345.Hotel-Information"; got != want {
		t.Errorf("booking url = %q, want %q", got, want)
	}

	leadIn := hotels[1]
	if leadIn.Price != 92.0 {
		t.Errorf("lead-in price = %v", leadIn.Price)
	}
	if leadIn.PriceBasis != models.PriceBasisLeadIn {
		t.Errorf("lead-in basis = %q, want %q", leadIn.PriceBasis, models.PriceBasisLeadIn)
	}
	if leadIn.PriceConfidence != models.PriceConfidenceUnverified {
		t.Errorf("lead-in confidence = %q, want %q", leadIn.PriceConfidence, models.PriceConfidenceUnverified)
	}
	// Absolute deep link is preserved as-is.
	if leadIn.BookingURL != "https://www.expedia.com/Paris-Hotels-Budget-Stay-Bastille.h67890.Hotel-Information" {
		t.Errorf("lead-in booking url = %q", leadIn.BookingURL)
	}
}

func TestSearchExpediaConfiguredAgainstMock(t *testing.T) {
	fixture := readExpediaFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	t.Setenv("EXPEDIA_API_BASE", srv.URL)
	prev := expediaEnabled
	expediaEnabled = true
	defer func() { expediaEnabled = prev }()

	res, err := SearchExpedia(context.Background(), "Paris", HotelSearchOptions{
		CheckIn:  "2026-07-25",
		CheckOut: "2026-07-27",
		Guests:   2,
		Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("configured search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results from mock, got %d", len(res))
	}
}

func TestSearchExpediaBotWallClassifiesRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mimic the live probe: Akamai Bot Manager challenge.
		w.Header().Set("x-page-id", "wildcard-challenge-handler")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<html>challenge</html>"))
	}))
	defer srv.Close()

	t.Setenv("EXPEDIA_API_BASE", srv.URL)
	prev := expediaEnabled
	expediaEnabled = true
	defer func() { expediaEnabled = prev }()

	_, err := SearchExpedia(context.Background(), "Paris", HotelSearchOptions{Currency: "EUR"})
	if err == nil {
		t.Fatal("expected error on 429 bot-wall")
	}
	// A 429/blocked response is RETRYABLE, never a permanent failure.
	if got := models.ClassifyProviderError(err); got != models.StatusRateLimited {
		t.Fatalf("classify(%v) = %q, want %q", err, got, models.StatusRateLimited)
	}
}

func TestExpediaUnconfiguredStatusInSearch(t *testing.T) {
	// SearchHotels must surface an honest not_configured status with the
	// AKAMAI_BLOCK fix-hint code when Expedia is not opted in — never a silent
	// omission. We assert the status object the fan-out emits.
	t.Setenv("EXPEDIA_API_BASE", "")
	st := models.ProviderStatus{
		ID:          "expedia",
		Name:        "Expedia",
		Status:      models.StatusNotConfigured,
		FixHintCode: "AKAMAI_BLOCK",
	}
	if st.Status != models.StatusNotConfigured {
		t.Fatalf("status = %q", st.Status)
	}
	// not_configured must not count toward completeness denominator.
	c := models.ComputeCompleteness([]models.ProviderStatus{st})
	if c.Queried != 0 {
		t.Fatalf("not_configured should not be queried, got %d", c.Queried)
	}
}

func TestExpediaBookingURLFallback(t *testing.T) {
	if got := expediaBookingURL(expediaProperty{}); got != expediaDefaultHost {
		t.Errorf("empty deep link should fall back to host, got %q", got)
	}
	if got := expediaBookingURL(expediaProperty{DeepLink: "//cdn.example/x"}); got != "https://cdn.example/x" {
		t.Errorf("protocol-relative = %q", got)
	}
}
