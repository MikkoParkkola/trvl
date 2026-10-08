package hotels

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// useTestDefaultClient points the DefaultClient singleton at a local server for
// the rest of the test, so code paths that build their own Google requests stay
// offline (MIK-8107). Package-global swap: callers must not use t.Parallel.
func useTestDefaultClient(t *testing.T, h http.Handler) {
	t.Helper()
	DefaultClient() // fire the sync.Once so it cannot overwrite the swap
	orig := defaultClient
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := newTestClient(srv.URL)
	c.SetNoCache(true)
	defaultClient = c
	t.Cleanup(func() { defaultClient = orig })
}

// requestLog records the requests a local server received.
type requestLog struct {
	mu     sync.Mutex
	bodies []string
	urls   []string
}

func (l *requestLog) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		l.mu.Lock()
		l.bodies = append(l.bodies, string(b))
		l.urls = append(l.urls, r.URL.String())
		l.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	})
}

func (l *requestLog) sawBody(want string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range l.bodies {
		if b == want {
			return true
		}
	}
	return false
}

func TestSearchHotelsWithClient_DefaultsGuestsAndCurrency(t *testing.T) {
	var log requestLog
	srv := httptest.NewServer(log.handler())
	defer srv.Close()
	client := newTestClient(srv.URL)
	client.SetNoCache(true)

	_, _ = SearchHotelsWithClient(context.Background(), client, "Helsinki", HotelSearchOptions{
		CheckIn: "2026-06-15", CheckOut: "2026-06-18", MaxPages: 1,
		CenterLat: 60.1699, CenterLon: 24.9384,
	})

	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.urls) == 0 {
		t.Fatal("no request reached the test server")
	}
	first, err := http.NewRequest(http.MethodGet, log.urls[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	q := first.URL.Query()
	if got := q.Get("adults"); got != "2" {
		t.Errorf("adults = %q, want 2 (default guests)", got)
	}
	if got := q.Get("currency"); got != "USD" {
		t.Errorf("currency = %q, want USD (default currency)", got)
	}
}

func TestGetHotelReviews_DefaultLimit(t *testing.T) {
	var log requestLog
	useTestDefaultClient(t, log.handler())

	_, _ = GetHotelReviews(context.Background(), "fake-id", ReviewOptions{})

	if want := "f.req=" + batchexec.BuildHotelReviewPayload("fake-id", 10); !log.sawBody(want) {
		t.Errorf("no review request with the default limit 10; got bodies %q", log.bodies)
	}
}

func TestGetHotelPrices_DefaultCurrency(t *testing.T) {
	var log requestLog
	useTestDefaultClient(t, log.handler())

	_, _ = GetHotelPrices(context.Background(), "/g/123", "2026-06-15", "2026-06-18", "")

	in, err := parseDateArray("2026-06-15")
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseDateArray("2026-06-18")
	if err != nil {
		t.Fatal(err)
	}
	var guests int
	var children []int
	if want := "f.req=" + batchexec.BuildHotelPricePayloadWithOccupancy("/g/123", in, out, "USD", guests, children); !log.sawBody(want) {
		t.Errorf("no price request in the default currency USD; got bodies %q", log.bodies)
	}
}
