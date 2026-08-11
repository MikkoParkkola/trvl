package providers

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/browserutils/kooky"
)

func TestBrowserCookiesForURLContext_CancelledBeforeRead(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_COOKIES", "1")
	targetURL := "https://booking.example.test"

	warmCache.mu.Lock()
	delete(warmCache.entries, warmCacheKey(targetURL, ""))
	warmCache.mu.Unlock()

	originalRead := readCookies
	t.Cleanup(func() { readCookies = originalRead })
	var reads atomic.Int32
	readCookies = func(context.Context, ...kooky.Filter) (kooky.Cookies, error) {
		reads.Add(1)
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logs := captureLogs(t, func() {
		if got := BrowserCookiesForURLContext(ctx, targetURL); got != nil {
			t.Fatalf("cookies = %v, want nil for cancelled lookup", got)
		}
	})
	if got := reads.Load(); got != 0 {
		t.Fatalf("browser cookie reads = %d, want zero after cancellation", got)
	}
	if strings.Contains(logs, "browser cookie lookup timed out") {
		t.Fatalf("cancelled lookup emitted a misleading timeout warning: %s", logs)
	}
}
