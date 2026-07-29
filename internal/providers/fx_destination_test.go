package providers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFXCacheClientCarriesDestinationPolicy pins the transport rather than the
// outcome of a rate lookup. getRate falls back to a built-in table whenever a
// fetch fails, so a policy refusal and a network error and a malformed
// response all look identical from the outside -- which is exactly how this
// client stayed unguarded while every test around it passed. The observable
// that distinguishes them is whether the dial happens at all.
func TestFXCacheClientCarriesDestinationPolicy(t *testing.T) {
	// main_test.go sets AllowLocalEnv=1 for the whole package so the many
	// httptest-backed tests can reach a loopback server. Clear it here: this
	// test is about the policy, so it has to run with the policy on.
	t.Setenv(AllowLocalEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fc := newFXCache()
	_, err := fc.client.Get(srv.URL)
	if err == nil {
		t.Fatalf("fx client reached %s; its transport does not carry the destination policy", srv.URL)
	}
	if !errors.Is(err, ErrDestinationRefused) {
		t.Fatalf("fx client failed with %v, want a %v refusal", err, ErrDestinationRefused)
	}
}

// The opt-in an operator uses to point trvl at a local mock has to reach this
// client too, or installing the policy here breaks local development instead
// of the thing it was meant to break.
func TestFXCacheClientHonoursLocalOptIn(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fc := newFXCache()
	resp, err := fc.client.Get(srv.URL)
	if err != nil {
		t.Fatalf("fx client refused %s under %s=1: %v", srv.URL, AllowLocalEnv, err)
	}
	_ = resp.Body.Close()
}
