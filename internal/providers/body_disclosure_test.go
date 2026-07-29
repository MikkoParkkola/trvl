package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bodyCanary is a string a provider response can carry that has no business
// appearing anywhere else. Asserting on a marker rather than on the shape of
// the error is deliberate: the question these tests ask is "did content from
// that host come back", and a marker answers it without pinning the wording.
const bodyCanary = "CANARY-9f3a-bd21-response-content"

// newCanaryProvider registers a provider whose endpoint is the given server
// and returns a runtime plus its config, so a test can drive searchProvider
// against a response it controls.
func newCanaryProvider(t *testing.T, endpoint string) (*Runtime, *ProviderConfig) {
	t.Helper()
	dir := t.TempDir()
	cfg := &ProviderConfig{
		ID:       "canary",
		Name:     "Canary",
		Category: "hotels",
		Endpoint: endpoint + "/search",
		Method:   "GET",
		ResponseMapping: ResponseMapping{
			ResultsPath: "results",
			Fields:      map[string]string{"name": "name"},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "canary.json"), data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	reg, err := NewRegistryAt(dir)
	if err != nil {
		t.Fatalf("NewRegistryAt: %v", err)
	}
	return NewRuntime(reg), reg.Get("canary")
}

func searchCanary(t *testing.T, rt *Runtime, cfg *ProviderConfig) string {
	t.Helper()
	_, err := rt.searchProvider(context.Background(), cfg, "Kyoto", 35.0, 135.7, "2026-06-01", "2026-06-03", "EUR", 2, nil)
	if err == nil {
		t.Fatal("searchProvider succeeded; this test needs the failing path that reports the body")
	}
	return err.Error()
}

// TRVL.SSRF.PUBLIC.1 -- an HTTP error from a provider endpoint must not carry
// that endpoint's response body back to the caller. The endpoint is named by
// the provider config, so a body echoed into a returned error is a read
// channel for whatever host the config chose.
func TestErrorFromHTTPStatusWithholdsBody(t *testing.T) {
	t.Setenv(BodySnippetEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(bodyCanary))
	}))
	defer srv.Close()

	rt, cfg := newCanaryProvider(t, srv.URL)
	msg := searchCanary(t, rt, cfg)

	if strings.Contains(msg, bodyCanary) {
		t.Fatalf("error returned response content to the caller: %s", msg)
	}
	// The shape has to survive, or this trades an exfiltration channel for an
	// undiagnosable failure.
	if !strings.Contains(msg, "text/plain") {
		t.Errorf("error dropped the content type, which is the diagnostic that replaces the body: %s", msg)
	}
	if !strings.Contains(msg, "500") {
		t.Errorf("error dropped the status code: %s", msg)
	}
}

// TRVL.SSRF.PUBLIC.2 -- the snippet is still reachable, behind an opt-in that
// only whoever starts the process can set.
func TestErrorFromHTTPStatusIncludesBodyUnderOptIn(t *testing.T) {
	t.Setenv(BodySnippetEnv, "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(bodyCanary))
	}))
	defer srv.Close()

	rt, cfg := newCanaryProvider(t, srv.URL)
	msg := searchCanary(t, rt, cfg)

	if !strings.Contains(msg, bodyCanary) {
		t.Fatalf("under %s=1 the body snippet did not come back: %s", BodySnippetEnv, msg)
	}
}

// TRVL.SSRF.PUBLIC.1 again, on the other reporting path: a 200 whose body does
// not match the configured results_path. This one also echoed the JSON key
// names it found, which are response content too.
func TestErrorFromResultsPathMissWithholdsBody(t *testing.T) {
	t.Setenv(BodySnippetEnv, "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"` + bodyCanary + `_key":"` + bodyCanary + `"}`))
	}))
	defer srv.Close()

	rt, cfg := newCanaryProvider(t, srv.URL)
	msg := searchCanary(t, rt, cfg)

	if strings.Contains(msg, bodyCanary) {
		t.Fatalf("results_path failure returned response content to the caller: %s", msg)
	}
	if !strings.Contains(msg, "application/json") {
		t.Errorf("results_path failure dropped the content type: %s", msg)
	}
	if !strings.Contains(msg, "results") {
		t.Errorf("results_path failure no longer names the path that missed: %s", msg)
	}
}

func TestErrorFromResultsPathMissIncludesBodyUnderOptIn(t *testing.T) {
	t.Setenv(BodySnippetEnv, "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"` + bodyCanary + `_key":"` + bodyCanary + `"}`))
	}))
	defer srv.Close()

	rt, cfg := newCanaryProvider(t, srv.URL)
	msg := searchCanary(t, rt, cfg)

	if !strings.Contains(msg, bodyCanary) {
		t.Fatalf("under %s=1 the results_path failure gave no body: %s", BodySnippetEnv, msg)
	}
}

// The opt-in is read per call. A cached value would mean a long-running server
// could not be talked out of the channel once it was in, and would also make
// the two tests above depend on which ran first.
func TestBodySnippetOptInIsReadPerCall(t *testing.T) {
	t.Setenv(BodySnippetEnv, "")
	if bodySnippetsAllowed() {
		t.Fatal("empty value should not enable snippets")
	}
	t.Setenv(BodySnippetEnv, "1")
	if !bodySnippetsAllowed() {
		t.Fatal("value set after the first read was not picked up")
	}
	t.Setenv(BodySnippetEnv, "false")
	if bodySnippetsAllowed() {
		t.Fatal(`"false" should not enable snippets`)
	}
}
