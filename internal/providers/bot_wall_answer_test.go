package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// An empty or plain-HTML answer with HTTP 202 is Booking.com's anti-bot
// response (#837): AWS WAF's Challenge answers 202 and serves its HTML
// interstitial only to requests that accept text/html. Live search and
// TestProvider must report it in the bot-wall class that provider statuses and
// the live probe skip on, never as a JSON parse error. A response carrying
// x-amzn-waf-action is a challenge whatever its body, and the header's value
// lands in the error so a nightly failure records it. A 403 already reports
// "http 403"; it is pinned alongside.
func TestBotWallAnswerIsBlockedNotParseError(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	for name, tc := range map[string]struct {
		status    int
		wafAction string
		body      string
	}{
		"202 empty":                       {http.StatusAccepted, "", ""},
		"202 whitespace":                  {http.StatusAccepted, "", " \n\t"},
		"202 plain HTML":                  {http.StatusAccepted, "", "<html><body>Please wait...</body></html>"},
		"202 JSON with x-amzn-waf-action": {http.StatusAccepted, "challenge", `{"results":[{"name":"x"}]}`},
		"403 empty":                       {http.StatusForbidden, "", ""},
		"403 HTML":                        {http.StatusForbidden, "", "<html><body>Access denied</body></html>"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				if tc.wafAction != "" {
					w.Header().Set("x-amzn-waf-action", tc.wafAction)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			rt, cfg := newCanaryProvider(t, srv.URL)

			_, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
			if err == nil {
				t.Fatal("searchProvider succeeded on a bot-wall answer")
			}
			if got := models.ClassifyProviderError(err); got != models.StatusRateLimited || strings.Contains(err.Error(), "parse json") {
				t.Errorf("searchProvider: status %q, err %q; want %q and no parse error", got, err, models.StatusRateLimited)
			}
			if got := models.ClassifyProviderReason(err.Error()); got != models.ReasonBlocked {
				t.Errorf("searchProvider: reason %q for %q, want %q", got, err, models.ReasonBlocked)
			}

			result := TestProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2)
			if result.Success {
				t.Fatal("TestProvider succeeded on a bot-wall answer")
			}
			if got := models.ClassifyProviderError(errors.New(result.Error)); got != models.StatusRateLimited || result.Step == "response_parse" {
				t.Errorf("TestProvider: step %q, error %q classifies %q; want %q outside response_parse", result.Step, result.Error, got, models.StatusRateLimited)
			}

			if want := "x-amzn-waf-action: " + tc.wafAction; tc.wafAction != "" {
				if !strings.Contains(err.Error(), want) || !strings.Contains(result.Error, want) {
					t.Errorf("errors %q and %q must carry %q", err, result.Error, want)
				}
			}
		})
	}
}

// A JSON 202 without the WAF header is a real Accepted and still parses.
func TestJSONAcceptedStillParses(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"results":[{"name":"x"}]}`))
	}))
	defer srv.Close()
	rt, cfg := newCanaryProvider(t, srv.URL)

	hotels, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
	if err != nil || len(hotels) != 1 {
		t.Fatalf("searchProvider on a JSON 202 = %d hotels, %v; want 1 hotel", len(hotels), err)
	}
}

func TestWAFActionHeaderIsChallenge(t *testing.T) {
	h := http.Header{}
	h.Set("x-amzn-waf-action", "challenge")
	upper := http.Header{}
	upper.Set("X-Amzn-Waf-Action", "Challenge")
	captcha := http.Header{}
	captcha.Set("x-amzn-waf-action", "captcha")
	for name, tc := range map[string]struct {
		status int
		header http.Header
		body   string
		want   bool
	}{
		"202 JSON with header":        {http.StatusAccepted, h, `{"results":[]}`, true},
		"202 JSON without header":     {http.StatusAccepted, nil, `{"results":[]}`, false},
		"202 empty without header":    {http.StatusAccepted, nil, "", true},
		"200 JSON, header upper-case": {http.StatusOK, upper, `{"results":[]}`, true},
		"405 JSON, captcha header":    {http.StatusMethodNotAllowed, captcha, `{"results":[]}`, false},
	} {
		if got := isAkamaiChallenge(tc.status, tc.header, []byte(tc.body)); got != tc.want {
			t.Errorf("%s: isAkamaiChallenge = %v, want %v", name, got, tc.want)
		}
	}
}

// A 429 whose retry comes back as an empty 202 is the same WAF challenge one
// hop later (#841): the challenge check must see the answer the 429 retry
// loop ends with, not only the first one.
func TestEmpty202After429RetryIsBlocked(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	rt, cfg := newCanaryProvider(t, srv.URL)
	cfg.Cookies = CookieConfig{Browser: "chrome"}
	u, _ := url.Parse(srv.URL)
	reads := stubBrowserReader(t, u.Hostname())

	_, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
	if err == nil {
		t.Fatal("searchProvider succeeded on a 429 then an empty 202")
	}
	if calls.Load() < 2 {
		t.Fatalf("server saw %d requests, want the 429 retried", calls.Load())
	}
	if got := models.ClassifyProviderError(err); got != models.StatusRateLimited || strings.Contains(err.Error(), "parse json") {
		t.Errorf("status %q, err %q; want %q and no parse error", got, err, models.StatusRateLimited)
	}
	if got := models.ClassifyProviderReason(err.Error()); got != models.ReasonBlocked {
		t.Errorf("reason %q for %q, want %q", got, err, models.ReasonBlocked)
	}
	if reads.Load() == 0 {
		t.Error("cookie recovery never read the browser store for the 202 the 429 retry returned")
	}
}

// The provider host controls x-amzn-waf-action, and the error text is
// classified by substring, so only AWS WAF's documented values reach it.
func TestWAFActionNoteKeepsOnlyDocumentedValues(t *testing.T) {
	for value, want := range map[string]string{
		"challenge":   "; x-amzn-waf-action: challenge",
		"Captcha":     "; x-amzn-waf-action: captcha",
		"timeout 429": "",
		"":            "",
	} {
		h := http.Header{}
		if value != "" {
			h.Set("x-amzn-waf-action", value)
		}
		if got := wafActionNote(h); got != want {
			t.Errorf("wafActionNote(%q) = %q, want %q", value, got, want)
		}
	}
}

// A hostile header value on a real challenge must not relabel it.
func TestHostileWAFActionValueDoesNotRelabelChallenge(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("x-amzn-waf-action", "timeout 429")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	rt, cfg := newCanaryProvider(t, srv.URL)

	_, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
	if err == nil {
		t.Fatal("searchProvider succeeded on a challenge")
	}
	if got := models.ClassifyProviderError(err); got != models.StatusRateLimited || strings.Contains(err.Error(), "timeout") {
		t.Errorf("status %q, err %q; want %q with the header value dropped", got, err, models.StatusRateLimited)
	}
	if got := models.ClassifyProviderReason(err.Error()); got != models.ReasonBlocked {
		t.Errorf("reason %q for %q, want %q", got, err, models.ReasonBlocked)
	}
}
