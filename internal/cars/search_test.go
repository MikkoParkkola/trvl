package cars

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points the home directory this package's tests resolve at a
// throwaway location, so no test can mutate the developer's real ~/.trvl.
// Search calls providers.LogHealth, which appends to ~/.trvl/health.jsonl with
// no injection point other than os.UserHomeDir -- so the environment IS the
// seam.
//
// It has to be here rather than per test with t.Setenv for a reason specific to
// this path: LogHealth is asynchronous (internal/providers/health_log.go:136
// enqueues, a writer goroutine resolves the directory later). A t.Setenv would
// be restored when the test returns, so a queued entry could still land in the
// real home afterwards. A process-wide redirect has no such window. It also
// covers tests added later, which a per-test call cannot.
//
// os.UserHomeDir reads HOME on unix and USERPROFILE on Windows, and
// os.UserConfigDir reads XDG_CONFIG_HOME on unix, so all three move together.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "trvl-test-home-")
	if err != nil {
		panic("test home: " + err.Error())
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestSearch_RequiresPickupAndDates(t *testing.T) {
	t.Parallel()

	_, err := Search(context.Background(), SearchOptions{
		PickupLocation: "HEL",
		PickupDate:     "2026-07-01",
	})
	if err == nil {
		t.Fatal("expected missing dropoff_date error")
	}
}

func TestSearch_NoConfiguredProviderReturnsTypedStatus(t *testing.T) {
	t.Setenv(skyscannerAPIKeyEnv, "")

	result, err := Search(context.Background(), SearchOptions{
		PickupLocation: "HEL",
		PickupDate:     "2026-07-01",
		DropoffDate:    "2026-07-04",
		Currency:       "EUR",
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if result.Success {
		t.Fatal("expected no-provider result to be unsuccessful")
	}
	if result.Count != 0 || len(result.Offers) != 0 {
		t.Fatalf("offers = %d/%d, want none", result.Count, len(result.Offers))
	}
	if len(result.ProviderStatuses) != 1 {
		t.Fatalf("provider statuses = %d, want 1", len(result.ProviderStatuses))
	}
	status := result.ProviderStatuses[0]
	if status.ID != ProviderSkyscanner || status.Status != "skipped" {
		t.Fatalf("status = %#v, want skipped skyscanner", status)
	}
	if status.FixHintCode != "MISSING_CREDENTIAL" {
		t.Fatalf("fix hint code = %q, want MISSING_CREDENTIAL", status.FixHintCode)
	}
	if !strings.Contains(result.Error, "SKYSCANNER_API_KEY") {
		t.Fatalf("error %q should mention SKYSCANNER_API_KEY", result.Error)
	}
}

func TestSearch_SkyscannerFixtureNormalizesOffers(t *testing.T) {
	t.Setenv(skyscannerAPIKeyEnv, "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want test-key", got)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/create"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sessionToken": "session-123"})
		case strings.HasSuffix(r.URL.Path, "/search/poll/session-123"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "complete",
				"offers": []map[string]any{
					{
						"provider":       "Skyscanner",
						"supplier":       "Hertz",
						"vehicleClass":   "compact",
						"vehicleName":    "Volkswagen Golf or similar",
						"transmission":   "automatic",
						"fuelPolicy":     "full_to_full",
						"seats":          5,
						"bags":           2,
						"doors":          4,
						"bookingUrl":     "https://example.test/book",
						"freeCancel":     true,
						"unlimitedMiles": true,
						"price": map[string]any{
							"amount":   144.50,
							"currency": "EUR",
						},
					},
					{
						"supplier": "Too Expensive",
						"price":    map[string]any{"amount": 999.0, "currency": "EUR"},
					},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	restore := setSkyscannerBaseURLForTest(server.URL)
	defer restore()

	result, err := Search(context.Background(), SearchOptions{
		PickupLocation:  "Helsinki Airport",
		DropoffLocation: "Helsinki Airport",
		PickupDate:      "2026-07-01",
		DropoffDate:     "2026-07-04",
		PickupTime:      "09:00",
		DropoffTime:     "18:00",
		Currency:        "EUR",
		Passengers:      3,
		MaxPrice:        200,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if !result.Success || result.Count != 1 {
		t.Fatalf("success/count = %v/%d, want true/1: %#v", result.Success, result.Count, result)
	}
	offer := result.Offers[0]
	if offer.Supplier != "Hertz" || offer.VehicleClass != "compact" || offer.Price != 144.50 {
		t.Fatalf("normalized offer = %#v", offer)
	}
	if offer.Currency != "EUR" || offer.Passengers != 3 {
		t.Fatalf("currency/passengers = %s/%d, want EUR/3", offer.Currency, offer.Passengers)
	}
	if offer.Pickup.Location != "Helsinki Airport" || offer.Dropoff.Location != "Helsinki Airport" {
		t.Fatalf("locations not preserved: %#v %#v", offer.Pickup, offer.Dropoff)
	}
	if len(result.ProviderStatuses) != 1 || result.ProviderStatuses[0].Status != "ok" {
		t.Fatalf("provider statuses = %#v, want ok", result.ProviderStatuses)
	}
}

func TestPureProviderParsingHelpers(t *testing.T) {
	if MarketedProviderCount() != 1 {
		t.Fatalf("MarketedProviderCount = %d", MarketedProviderCount())
	}
	if got := MarketedProviderNames(); len(got) != 1 || got[0] != ProviderSkyscanner {
		t.Fatalf("MarketedProviderNames = %#v", got)
	}

	providers := normalizedProviders([]string{" skyscanner, custom ", "", "other"})
	if strings.Join(providers, "|") != "skyscanner|custom|other" {
		t.Fatalf("normalizedProviders = %#v", providers)
	}
	if got := normalizedProviders(nil); len(got) != 1 || got[0] != ProviderSkyscanner {
		t.Fatalf("default providers = %#v", got)
	}
	if got := normalizedProviders([]string{" , "}); len(got) != 1 || got[0] != ProviderSkyscanner {
		t.Fatalf("blank providers = %#v", got)
	}

	status := carProviderError("x", "Provider X", errors.New("bad shape"))
	if status.Status != "error" || status.FixHintCode != "RESPONSE_SHAPE_CHANGED" || !strings.Contains(status.Error, "bad shape") {
		t.Fatalf("carProviderError = %#v", status)
	}

	if got, ok := parsePriceString("EUR 1,234.50"); !ok || got != 1234.50 {
		t.Fatalf("parsePriceString = %.2f/%v", got, ok)
	}
	if got, ok := parsePriceString("only text"); ok || got != 0 {
		t.Fatalf("parsePriceString text = %.2f/%v", got, ok)
	}

	floatCases := []any{float64(3.5), float32(4.5), 5, int64(6), json.Number("7.5"), "EUR 8.50"}
	for _, tc := range floatCases {
		if got, ok := floatValue(tc); !ok || got <= 0 {
			t.Fatalf("floatValue(%T) = %.2f/%v", tc, got, ok)
		}
	}
	if _, ok := floatValue(struct{}{}); ok {
		t.Fatal("unsupported float value should return ok=false")
	}

	if got := firstString(map[string]any{"vehicle_name": " Golf "}, "vehicleName"); got != "Golf" {
		t.Fatalf("firstString normalized key = %q", got)
	}
	if got := firstFloat(map[string]any{"price": map[string]any{"amount": "42"}}, "price.amount"); got != 42 {
		t.Fatalf("firstFloat nested = %.2f", got)
	}
	if got := firstInt(map[string]any{"seats": json.Number("5")}, "seats"); got != 5 {
		t.Fatalf("firstInt = %d", got)
	}
	if got, ok := firstBool(map[string]any{"free_cancel": "included"}, "freeCancel"); !ok || !got {
		t.Fatalf("firstBool = %v/%v", got, ok)
	}
	if got, ok := boolValue("no"); !ok || got {
		t.Fatalf("boolValue no = %v/%v", got, ok)
	}
	if _, ok := boolValue("maybe"); ok {
		t.Fatal("boolValue maybe should be unknown")
	}
}
