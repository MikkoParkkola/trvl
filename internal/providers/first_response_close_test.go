package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// closeCounter is a response body that counts its Close calls.
type closeCounter struct {
	io.Reader
	closes atomic.Int32
}

func (c *closeCounter) Close() error { c.closes.Add(1); return nil }

type cannedStep struct {
	status int
	header map[string]string
	body   string
}

// cannedTransport answers each request with the next step (repeating the last)
// and keeps every body it hands out, so a test can count their closes.
type cannedTransport struct {
	mu     sync.Mutex
	steps  []cannedStep
	bodies []*closeCounter
}

func (c *cannedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	step := c.steps[min(len(c.bodies), len(c.steps)-1)]
	body := &closeCounter{Reader: strings.NewReader(step.body)}
	c.bodies = append(c.bodies, body)
	h := http.Header{}
	for k, v := range step.header {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: step.status, Header: h, Body: body, Request: req}, nil
}

// Live search must close every response body exactly once, the first one
// included, when a retry replaces it (#846): the 429 retry loop and the
// challenge-recovery retries both reassign resp.
func TestSearchClosesEveryResponseOnce(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	results := `{"results":[{"name":"x"}]}`
	for name, steps := range map[string][]cannedStep{
		"429 then 200": {
			{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "1"}},
			{status: http.StatusOK, body: results},
		},
		"challenge then recovered 200": {
			{status: http.StatusAccepted, header: map[string]string{"Content-Type": "text/html"}, body: `<html><script>window.aws = {}</script></html>`},
			{status: http.StatusOK, body: results},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rt, cfg := newCanaryProvider(t, "http://127.0.0.1:9")
			cfg.Cookies = CookieConfig{Browser: "chrome"}
			_ = stubBrowserReader(t, "127.0.0.1") // lets Tier 3a recover the challenge
			transport := &cannedTransport{steps: steps}
			rt.getOrCreateClient(cfg).client = &http.Client{Transport: transport, Jar: newCookieVault()}

			hotels, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
			if err != nil || len(hotels) != 1 {
				t.Fatalf("searchProvider = %d hotels, %v; want 1 hotel after the retry", len(hotels), err)
			}
			if len(transport.bodies) < 2 {
				t.Fatalf("transport served %d responses, want the first one replaced by a retry", len(transport.bodies))
			}
			for i, b := range transport.bodies {
				if got := b.closes.Load(); got != 1 {
					t.Errorf("response %d body closed %d times, want exactly 1", i, got)
				}
			}
		})
	}
}
