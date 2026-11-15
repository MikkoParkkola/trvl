package ground

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func eckeroStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func eckeroBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// TestTryEckeroLineLive_ErrorsAreErrors pins D2 for the Eckerö fetched path:
// every failure returns an error, typed ErrRateLimited (429/5xx/WAF) or
// ErrParseFailed (undecodable or missing shape), never nil,nil.
func TestTryEckeroLineLive_ErrorsAreErrors(t *testing.T) {
	cases := []struct {
		name string
		home http.HandlerFunc
		dep  http.HandlerFunc
		want error
	}{
		{"homepage 503", eckeroStatus(http.StatusServiceUnavailable), eckeroBody(`[]`), models.ErrRateLimited},
		{"homepage 429", eckeroStatus(http.StatusTooManyRequests), eckeroBody(`[]`), models.ErrRateLimited},
		{"homepage 403 WAF", eckeroStatus(http.StatusForbidden), eckeroBody(`[]`), models.ErrRateLimited},
		{"homepage without form_key", func(w http.ResponseWriter, _ *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: "s"})
			_, _ = w.Write([]byte(`<html>no key here</html>`))
		}, eckeroBody(`[]`), models.ErrParseFailed},
		{"departures 500", eckeroDefaultHome, eckeroStatus(http.StatusInternalServerError), models.ErrRateLimited},
		{"departures 429", eckeroDefaultHome, eckeroStatus(http.StatusTooManyRequests), models.ErrRateLimited},
		{"departures undecodable", eckeroDefaultHome, eckeroBody(`<html>maintenance</html>`), models.ErrParseFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withEckeroServer(t, c.home, c.dep)
			deps, err := tryEckeroLineLive(context.Background(), "HEL", "TLL", "2026-07-15")
			if err == nil {
				t.Fatalf("got %d departures and nil error, want an error wrapping %v", len(deps), c.want)
			}
			if !errors.Is(err, c.want) {
				t.Errorf("error %q does not wrap %v", err, c.want)
			}
		})
	}
}

// TestTryEckeroLineLive_TransportFailureIsError: a connection failure is an
// error, not "no departures".
func TestTryEckeroLineLive_TransportFailureIsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	origClient, origLimiter := eckerolineClient, eckerolineLimiter
	eckerolineClient = &http.Client{Transport: &urlRewriter{base: dead}, Timeout: 2 * time.Second}
	eckerolineLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { eckerolineClient, eckerolineLimiter = origClient, origLimiter })
	if _, err := tryEckeroLineLive(context.Background(), "HEL", "TLL", "2026-07-15"); err == nil {
		t.Fatal("connection refused returned nil error")
	}
}

// TestTryEckeroLineLive_ValidEmptyIsNotError: a valid empty departure list is
// an answer, not a failure.
func TestTryEckeroLineLive_ValidEmptyIsNotError(t *testing.T) {
	withEckeroServer(t, eckeroDefaultHome, eckeroBody(`[]`))
	deps, err := tryEckeroLineLive(context.Background(), "HEL", "TLL", "2026-07-15")
	if err != nil || len(deps) != 0 {
		t.Fatalf("got %d departures, err %v; want 0 and nil", len(deps), err)
	}
}

// TestSearchDFDS_AvailabilityFailureIsError pins D2 for DFDS: a failed
// availability check returns an error and no fabricated route.
func TestSearchDFDS_AvailabilityFailureIsError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"503", http.StatusServiceUnavailable, `busy`, models.ErrRateLimited},
		{"429", http.StatusTooManyRequests, `slow down`, models.ErrRateLimited},
		{"403 WAF", http.StatusForbidden, `<html>blocked</html>`, models.ErrRateLimited},
		{"undecodable 200", http.StatusOK, `<html>not json</html>`, models.ErrParseFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withDFDSAvailability(t, c.status, c.body)
			routes, err := SearchDFDS(context.Background(), "Amsterdam", "Newcastle", "2026-07-15", "EUR")
			if len(routes) != 0 {
				t.Errorf("fabricated %d route(s) after a failed availability check", len(routes))
			}
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want it to wrap %v", err, c.want)
			}
		})
	}
}

// TestSearchDFDS_NetworkFailureIsError: no connection, no route, an error.
func TestSearchDFDS_NetworkFailureIsError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	origClient, origLimiter := dfdsClient, dfdsLimiter
	dfdsClient = &http.Client{Transport: &redirectTransport{target: dead}, Timeout: 2 * time.Second}
	dfdsLimiter = rate.NewLimiter(rate.Limit(1000), 1)
	t.Cleanup(func() { dfdsClient, dfdsLimiter = origClient, origLimiter })
	routes, err := SearchDFDS(context.Background(), "Amsterdam", "Newcastle", "2026-07-15", "EUR")
	if err == nil || len(routes) != 0 {
		t.Fatalf("got %d routes, err %v; want no routes and an error", len(routes), err)
	}
}

// TestSearchDFDS_InactiveRouteIsValidEmpty guards the valid "no service"
// answer: an inactive route returns no routes and no error.
func TestSearchDFDS_InactiveRouteIsValidEmpty(t *testing.T) {
	withDFDSAvailability(t, http.StatusOK, `{"dates":{"fromDate":"","toDate":""},"disabledDates":[],"offerDates":[]}`)
	routes, err := SearchDFDS(context.Background(), "Amsterdam", "Newcastle", "2026-07-15", "EUR")
	if err != nil || len(routes) != 0 {
		t.Fatalf("got %d routes, err %v; want 0 and nil", len(routes), err)
	}
}
