package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestCheckDestinationURLRefusesByDefault pins the parse-time half of the
// policy: what a caller can be refused for before anything is dialled.
func TestCheckDestinationURLRefusesByDefault(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	refused := []struct {
		name string
		url  string
	}{
		{"loopback v4", "http://127.0.0.1:8080/search"},
		{"loopback v4 alt", "https://127.1.2.3/search"},
		{"loopback v6", "http://[::1]:9000/search"},
		{"loopback name", "http://localhost:8080/search"},
		{"loopback subdomain", "http://mock.localhost/search"},
		{"private 10", "https://10.0.0.5/api"},
		{"private 172", "https://172.16.4.9/api"},
		{"private 192", "https://192.168.1.1/api"},
		{"link-local", "http://169.254.1.1/"},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/"},
		{"unspecified", "http://0.0.0.0:80/"},
		{"v4-mapped loopback", "http://[::ffff:127.0.0.1]/"},
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://example.com:70/"},
		{"no host", "http:///search"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDestinationURL(tc.url)
			if err == nil {
				t.Fatalf("CheckDestinationURL(%q) = nil, want refusal", tc.url)
			}
			if !errors.Is(err, ErrDestinationRefused) {
				t.Fatalf("CheckDestinationURL(%q) error %v does not wrap ErrDestinationRefused", tc.url, err)
			}
		})
	}

	allowed := []string{
		"https://www.booking.com/searchresults.json",
		"http://93.184.216.34/search",
		"", // absent config field: emptiness is someone else's validation
	}
	for _, raw := range allowed {
		if err := CheckDestinationURL(raw); err != nil {
			t.Fatalf("CheckDestinationURL(%q) = %v, want nil", raw, err)
		}
	}
}

// TestCheckDestinationURLLocalOptIn pins what the opt-in does and does not
// widen: local addresses become reachable, non-HTTP schemes do not.
func TestCheckDestinationURLLocalOptIn(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")

	for _, raw := range []string{"http://127.0.0.1:8080/search", "http://localhost:8080/search", "http://192.168.1.1/"} {
		if err := CheckDestinationURL(raw); err != nil {
			t.Fatalf("under %s=1, CheckDestinationURL(%q) = %v, want nil", AllowLocalEnv, raw, err)
		}
	}
	if err := CheckDestinationURL("file:///etc/passwd"); !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("under %s=1, file:// got %v, want refusal: the opt-in is about local addresses, not about schemes", AllowLocalEnv, err)
	}
}

// TestProviderRefusesLoopbackEndpoint is the behaviour test for the whole
// point of the policy: a caller-supplied provider config aimed at a service on
// the local machine must not reach it, and the caller must be told it was
// refused rather than handed an empty-looking result.
//
// It asserts on the server's own request counter, not on the returned body:
// counting hits is the only assertion that fails if the refusal is moved to a
// place that still lets the request go out.
func TestProviderRefusesLoopbackEndpoint(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secret":"internal service response"}`))
	}))
	defer srv.Close()

	cfg := &ProviderConfig{
		ID:       "ssrf-probe",
		Name:     "SSRF Probe",
		Category: "hotels",
		Endpoint: srv.URL + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}

	result := TestProvider(context.Background(), cfg, "Paris", 48.8566, 2.3522, "2026-05-01", "2026-05-02", "EUR", 2)

	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server received %d requests, want 0: the destination policy did not stop the request", got)
	}
	if result.Success {
		t.Fatalf("result.Success = true, want false for a refused destination")
	}
	if !strings.Contains(result.Error, "destination refused") {
		t.Fatalf("result.Error = %q, want a visible refusal: a refusal reported as a generic failure reads as an empty result", result.Error)
	}
	if result.BodySnippet != "" {
		t.Fatalf("result.BodySnippet = %q, want empty: no bytes from a refused destination may reach the caller", result.BodySnippet)
	}
}

// TestRefusalIsClassifiedAsARefusal pins the last step of the AC: a search
// that was refused has to reach the caller labelled as a refusal. The per-
// provider status carries this code, so classifying a refusal as a network
// failure is how it would come back looking like an ordinary empty result.
func TestRefusalIsClassifiedAsARefusal(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	client := &http.Client{Transport: guardedTransport()}
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("client.Get to loopback succeeded, want refusal")
	}
	code, hint := classifyProviderError(err)
	if code != FixHintDestinationRefused {
		t.Fatalf("classifyProviderError = %q, want %q: a refusal reported as a network failure sends the caller after the wrong problem", code, FixHintDestinationRefused)
	}
	if !strings.Contains(hint, AllowLocalEnv) {
		t.Fatalf("hint %q does not name the opt-in, so a developer running a local mock is not told how to proceed", hint)
	}
}

// The refusal comes back from a dialer, so it arrives wrapped in *url.Error
// and *net.OpError; a caller that asks errors.Is must still get true.
func TestDialControlRefusalUnwraps(t *testing.T) {
	t.Setenv(AllowLocalEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: guardedTransport()}
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("client.Get to loopback succeeded, want refusal")
	}
	if !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("error %v does not unwrap to ErrDestinationRefused through url.Error/net.OpError", err)
	}
}
