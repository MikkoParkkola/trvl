package providers

import (
	"net/http"
	"testing"
)

// seedWarmCache installs a COMPLETED warm-cache entry for one URL and removes
// it again when the test ends.
//
// The entry is what the background pre-warm goroutine would have left behind:
// its done channel is already closed, so a lookup takes the cached branch
// immediately instead of waiting out the timeout.
func seedWarmCache(t *testing.T, targetURL string, cookies []*http.Cookie) {
	t.Helper()
	key := warmCacheKey(targetURL, "")
	done := make(chan struct{})
	close(done)

	warmCache.mu.Lock()
	warmCache.entries[key] = &warmCacheEntry{cookies: cookies, done: done}
	warmCache.mu.Unlock()

	t.Cleanup(func() {
		warmCache.mu.Lock()
		delete(warmCache.entries, key)
		warmCache.mu.Unlock()
	})
}

// A pre-warmed site the user is not logged in to must not be reported as
// "found". It is the same invariant TestFoundIsNeverReturnedWithNoCookies
// asserts, on the path that test cannot reach.
//
// readBrowserCookiesDirect builds its result with make([]*http.Cookie, 0, n),
// so a clean read matching nothing returns a slice that is EMPTY BUT NOT NIL,
// and the warm cache stores it verbatim. The old warm-cache branch tested
// `cached != nil`, which that slice satisfies, and stamped it "found" -- the
// exact "found beside zero cookies" ambiguity this branch exists to remove,
// reintroduced for every pre-warmed provider. Warm-up runs in production from
// NewRuntime whenever the cookie source is "browser", so this is the common
// path for a logged-out user, not a corner.
//
// Sabotage-verified: restoring `return cached, outcomeFound, nil` fails here.
func TestWarmCacheEmptySuccessIsNotFound(t *testing.T) {
	const target = "https://www.thetrainline.com/search"
	seedWarmCache(t, target, []*http.Cookie{})

	got, outcome, err := browserCookiesForURLWithOutcome(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d cookies from an empty warm entry, want 0", len(got))
	}
	if outcome == outcomeFound {
		t.Error("a pre-warmed site with zero cookies was reported as \"found\". " +
			"The caller is told cookies were found and handed none, which is the " +
			"misreading #529 exists to remove -- and it says it about a user who is " +
			"simply not logged in.")
	}
	if outcome != outcomeNoMatch {
		t.Errorf("outcome = %v, want %v: a completed read that matched nothing is "+
			"the one case where \"not logged in\" is a true statement", outcome, outcomeNoMatch)
	}
}

// The control: a warm entry that DOES hold cookies must still be served as
// found. Without this, classifying every warm hit as no_match would pass the
// test above while silently disabling pre-warmed browser cookies in production
// -- the warm cache's entire purpose.
func TestWarmCacheWithCookiesIsStillFound(t *testing.T) {
	const target = "https://www.thetrainline.com/search"
	seedWarmCache(t, target, []*http.Cookie{{Name: "sid", Value: "abc", Domain: "thetrainline.com"}})

	got, outcome, err := browserCookiesForURLWithOutcome(target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d cookies from a populated warm entry, want 1", len(got))
	}
	if outcome != outcomeFound {
		t.Errorf("outcome = %v, want %v: pre-warmed cookies were read and returned", outcome, outcomeFound)
	}
}
