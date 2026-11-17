package hotels

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestSearchHotelByName_CancelledLastSearchKeepsBoth: the city search was
// refused, and the lookup's context ended during its last candidate's search.
// The error carries the cancellation and the refusal (grok, review on 706aff4d).
//
// The last candidate's search is a run this test holds open and waits for:
// the lookup joins it and its deadline ends while it is held. A real slow
// search would outlive the test, because shared runs are detached from their
// callers, and race the next test over the package globals.
func TestSearchHotelByName_CancelledLastSearchKeepsBoth(t *testing.T) {
	const query = "Hotel Sacher, Vienna"
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), answer(http.StatusForbidden)}, false)

	cands := locationCandidates(query, LocationHotelQuery)
	last := cands[len(cands)-1]
	key := hotelSearchKey(englishHotelCity(last.query), HotelSearchOptions{
		CheckIn: lookupCheckIn, CheckOut: lookupCheckOut, Guests: 2, Currency: "USD", Scope: last.scope,
	})
	var registered atomic.Int32
	t.Cleanup(func() { hotelSearchRegistered = nil })
	hotelSearchRegistered = func() { registered.Add(1) }

	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = doHotelSearchRun(context.Background(), key, func(context.Context) (hotelSearchRun, error) {
			close(started)
			<-release
			return hotelSearchRun{result: &models.HotelSearchResult{}}, nil
		})
	}()
	<-started
	stop := sync.OnceFunc(func() { close(release); <-done })
	t.Cleanup(stop)

	// Long enough to wait out every pause between candidates.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(cands)-1)*2*time.Second+time.Second)
	defer cancel()
	_, err := SearchHotelByName(ctx, query, lookupCheckIn, lookupCheckOut, "USD")
	stop()

	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want the deadline joined with the recorded refusal", err)
	}
	// The held run, then one search per candidate: the deadline ended inside
	// the last one, not in a pause between candidates.
	if got, want := registered.Load(), int32(len(cands)+1); got != want {
		t.Fatalf("searches registered = %d, want %d", got, want)
	}
}

// TestSearchHotelByName_NoResultsReportsTheStrongestRefusal: candidate 1 met
// a cooldown refusal, candidate 2 a plain 403, and nothing answered. The
// error reports the outcome's strongest refusal, the cooldown, not the later
// block (grok, #826 rev 6 [6]). The Google fetch is stubbed because the real
// client's cooldown would skip candidate 2. The stub hands back a cooldown
// refusal directly: googleCooldownErr would probe an unarmed client.
func TestSearchHotelByName_NoResultsReportsTheStrongestRefusal(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), noSacherPage}, false)
	orig := fetchHotelPageFullFn
	t.Cleanup(func() { fetchHotelPageFullFn = orig })
	fetchHotelPageFullFn = func(_ context.Context, _ *batchexec.Client, loc string, _ HotelSearchOptions, _ int, _ string) (parseResult, error) {
		if strings.Contains(loc, "Sacher") {
			return parseResult{}, batchexec.ErrBlocked
		}
		return parseResult{}, fmt.Errorf("google hotel search: %w: %w", models.ErrRateLimited, batchexec.ErrRefused)
	}

	_, err := SearchHotelByName(context.Background(), "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, batchexec.ErrRefused) || errors.Is(err, batchexec.ErrBlocked) {
		t.Fatalf("err = %v, want the cooldown alone, not the later plain block", err)
	}
}

// TestHasLocality: the place a property answer must carry is matched trimmed,
// case-folded and on whole words (#826 rev 6 final [5]).
func TestHasLocality(t *testing.T) {
	cases := []struct {
		query, locality string
		want            bool
	}{
		{"Hotel Sacher Vienna", "Vienna", true},
		{"hotel sacher, VIENNA ", " vienna", true},
		{"Hotel Sacher", "Vienna", false},
		{"Hotel Viennaland", "Vienna", false},
		{"Summer Shades hotel, Naoussa, Paros", "Naoussa, Paros", true},
		// buildLocationCandidates drops commas: the words still match.
		{"Summer Shades hotel, Naoussa Paros", "Naoussa, Paros", true},
		{"Summer Shades hotel Paros", "Naoussa, Paros", false},
	}
	for _, tc := range cases {
		if got := hasLocality(tc.query, tc.locality); got != tc.want {
			t.Errorf("hasLocality(%q, %q) = %v, want %v", tc.query, tc.locality, got, tc.want)
		}
	}
	if got := queryLocality("Hotel Sacher, Vienna"); got != "Vienna" {
		t.Errorf("queryLocality = %q, want Vienna", got)
	}
	if got := queryLocality("Hotel Sacher Wien"); got != "Hotel Sacher Wien" {
		t.Errorf("queryLocality = %q, want the whole comma-free query", got)
	}
}

// TestSearchHotelByName_RefusedPageBlocksSettling: the property searches
// answered on page 1 without the hotel, but their page 2 was refused. That
// page could hold the hotel, so the answer settles nothing and the miss
// carries the refusal (#826 rev 6 [3], final [4]).
func TestSearchHotelByName_RefusedPageBlocksSettling(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
	}}, false)

	_, err := SearchHotelByName(context.Background(), "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, ErrNoNameMatch) || !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want the miss joined with the refused page's rate limit", err)
	}
}

// TestSearchHotelByName_CancellationKeepsTheRefusal: the city search was
// refused, then the lookup's context ended while waiting for the next
// candidate. The error carries both (#826 rev 6, cancellation).
func TestSearchHotelByName_CancellationKeepsTheRefusal(t *testing.T) {
	roomLookupWorld(t, googleRoutes{shortEntity, answer(404), func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("q"), "Sacher") {
			_, _ = w.Write(fakeHotelPageWithPrices(hotelWithPrice{"Hotel Stefanie", 150}))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}}, false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second) // ends inside the 2 s gap
	defer cancel()

	_, err := SearchHotelByName(ctx, "Hotel Sacher, Vienna", lookupCheckIn, lookupCheckOut, "USD")
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, models.ErrRateLimited) {
		t.Fatalf("err = %v, want the deadline joined with the recorded refusal", err)
	}
}
