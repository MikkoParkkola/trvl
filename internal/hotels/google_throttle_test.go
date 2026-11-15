package hotels

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// wrbStatusBody is a payload-less batchexecute answer carrying Google's
// internal status code, the shape of the captured price_empty_payload.txt.
func wrbStatusBody(code string) string {
	return ")]}'\n\n[[\"wrb.fr\",\"yY52ce\",null,null,null,[" + code + "],\"generic\"],[\"di\",33],[\"af.httprm\",33,\"-4505950447697788873\",31]]"
}

func noSerpAPIKey(t *testing.T) {
	t.Helper()
	orig := serpapiAPIKeyFunc
	serpapiAPIKeyFunc = func() string { return "" }
	t.Cleanup(func() { serpapiAPIKeyFunc = orig })
}

func priceOpts() HotelPriceOpts {
	return HotelPriceOpts{HotelID: "/g/11b6d4_v_4", CheckIn: "2026-06-15", CheckOut: "2026-06-18", Currency: "EUR"}
}

func requireRateLimited(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: err = nil, want an error wrapping models.ErrRateLimited", label)
	}
	if !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("%s: err = %q does not wrap models.ErrRateLimited (an MCP gateway would label it INVALID_PARAM)", label, err)
	}
	if !strings.Contains(err.Error(), "rate-limited") {
		t.Fatalf("%s: err = %q, want the text to say rate-limited", label, err)
	}
}

// TestGetHotelPrices_Status13IsRateLimited: Google's quota refusal (wrb.fr
// status 13) on the price RPC is a rate limit, not a successful lookup with
// no partners.
func TestGetHotelPrices_Status13IsRateLimited(t *testing.T) {
	noSerpAPIKey(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(wrbStatusBody("13")))
	}))

	res, err := GetHotelPricesWithOpts(context.Background(), priceOpts())
	if err == nil {
		t.Fatalf("status 13: got success %+v, want a rate-limit error", res)
	}
	requireRateLimited(t, "status 13", err)
}

// TestGetHotelPrices_RefusedDuringCooldownIsRateLimited: after a status 13
// the client refuses further Google calls without dialing
// (batchexec.ErrRefused). prices.go wrapped that bare; it is a rate limit.
func TestGetHotelPrices_RefusedDuringCooldownIsRateLimited(t *testing.T) {
	noSerpAPIKey(t)
	var calls atomic.Int64
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(wrbStatusBody("13")))
	}))

	_, _ = GetHotelPricesWithOpts(context.Background(), priceOpts()) // arms the cooldown
	_, err := GetHotelPricesWithOpts(context.Background(), priceOpts())
	if n := calls.Load(); n != 1 {
		t.Fatalf("precondition: server saw %d requests, want 1 (second call refused before dialing)", n)
	}
	requireRateLimited(t, "refused during cooldown", err)
}

// TestGetHotelPrices_Status429IsRateLimited covers the plain HTTP form of the
// same refusal on the price RPC.
func TestGetHotelPrices_Status429IsRateLimited(t *testing.T) {
	noSerpAPIKey(t)
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	_, err := GetHotelPricesWithOpts(context.Background(), priceOpts())
	requireRateLimited(t, "HTTP 429", err)
}

// serveStatus3 answers the price RPC with the captured payload-less status 3
// response and replaces the fallback chain with fallback, counting calls.
func serveStatus3(t *testing.T, fallback *models.HotelPriceResult) *atomic.Int64 {
	t.Helper()
	noSerpAPIKey(t)
	body, err := os.ReadFile("testdata/price_empty_payload.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	var calls atomic.Int64
	orig := priceFallback
	priceFallback = func(context.Context, HotelPriceOpts) *models.HotelPriceResult {
		calls.Add(1)
		return fallback
	}
	t.Cleanup(func() { priceFallback = orig })
	return &calls
}

// TestGetHotelPrices_Status3UsesFallbackPrices: a payload-less answer goes to
// the fallback chain first, and the fallback's prices are the result.
func TestGetHotelPrices_Status3UsesFallbackPrices(t *testing.T) {
	calls := serveStatus3(t, &models.HotelPriceResult{
		Success: true, HotelID: "/g/11b6d4_v_4",
		Providers: []models.ProviderPrice{{Provider: "SerpAPI", Price: 150, Currency: "EUR"}},
	})

	res, err := GetHotelPricesWithOpts(context.Background(), priceOpts())
	if n := calls.Load(); n != 1 {
		t.Fatalf("fallback called %d times, want 1", n)
	}
	if err != nil || res == nil || len(res.Providers) != 1 || res.Providers[0].Price != 150 {
		t.Fatalf("result = %+v, err %v; want the fallback's single 150 EUR price", res, err)
	}
}

// TestGetHotelPrices_Status3NoFallbackIsTypedFailure: Google's price call
// returning no payload (status 3, the captured fixture) still goes through
// the fallback chain; when that also yields nothing the lookup fails with
// ErrPriceNoData. It must not claim success, and it is not a rate limit:
// status 3 starts no cooldown, so labelling it rate-limited would tell the
// caller to wait for something that is not coming.
func TestGetHotelPrices_Status3NoFallbackIsTypedFailure(t *testing.T) {
	calls := serveStatus3(t, nil)

	res, err := GetHotelPricesWithOpts(context.Background(), priceOpts())
	if n := calls.Load(); n != 1 {
		t.Fatalf("fallback called %d times, want 1 before giving up", n)
	}
	if err == nil {
		t.Fatalf("status 3 with no fallback: got success %+v, want ErrPriceNoData", res)
	}
	if !errors.Is(err, ErrPriceNoData) {
		t.Fatalf("err = %q, want it to wrap ErrPriceNoData", err)
	}
	if !strings.Contains(err.Error(), "price service returned no data") {
		t.Fatalf("err = %q, want the text to say price service returned no data", err)
	}
	if errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %q wraps ErrRateLimited; status 3 is not a quota refusal", err)
	}
}

// TestFetchHotelPageFull_RefusedDuringCooldownIsRateLimited: the search page
// is a Google call too. Once a refusal has armed the cooldown, the next page
// fetch is refused without dialing; search_fetch.go wrapped that bare.
func TestFetchHotelPageFull_RefusedDuringCooldownIsRateLimited(t *testing.T) {
	var calls atomic.Int64
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	client := DefaultClient()

	_, _ = fetchHotelPageFull(context.Background(), client, "Vienna", defaultOpts(), 0, "") // arms the cooldown
	_, err := fetchHotelPageFull(context.Background(), client, "Vienna", defaultOpts(), 0, "")
	if n := calls.Load(); n != 1 {
		t.Fatalf("precondition: server saw %d requests, want 1 (second call refused before dialing)", n)
	}
	requireRateLimited(t, "refused during cooldown", err)
}

// TestFetchHotelPageFull_Status429IsRateLimited covers the plain HTTP form
// of the refusal on the search page.
func TestFetchHotelPageFull_Status429IsRateLimited(t *testing.T) {
	useTestDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	_, err := fetchHotelPageFull(context.Background(), DefaultClient(), "Vienna", defaultOpts(), 0, "")
	requireRateLimited(t, "HTTP 429", err)
}
