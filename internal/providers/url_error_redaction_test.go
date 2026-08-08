package providers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	urlPathCanary  = "CANARY-secret-path-7076"
	urlQueryCanary = "CANARY-secret-query-7076"
)

type failingURLTransport struct {
	err error
}

func (t failingURLTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func sensitiveProviderURL() string {
	return "https://93.184.216.34/" + urlPathCanary + "/search?api_key=" + urlQueryCanary
}

func assertRequestURLRedacted(t *testing.T, value string) {
	t.Helper()
	for _, canary := range []string{urlPathCanary, urlQueryCanary} {
		if strings.Contains(value, canary) {
			t.Fatalf("credential-bearing request URL escaped in %q", value)
		}
	}
	if !strings.Contains(value, "url#") {
		t.Fatalf("redacted error %q has no URL correlation fingerprint", value)
	}
}

func TestRequestErrorsHideURLAtProviderSeams(t *testing.T) {
	sentinel := errors.New("transport sentinel")
	client := &http.Client{Transport: failingURLTransport{err: sentinel}}

	t.Run("search retry", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, sensitiveProviderURL(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = doSearchRequest(context.Background(), client, req)
		if err == nil {
			t.Fatal("doSearchRequest succeeded with a failing transport")
		}
		assertRequestURLRedacted(t, err.Error())
		if !errors.Is(err, sentinel) {
			t.Fatalf("redaction broke the error chain: %v", err)
		}
	})

	t.Run("preflight", func(t *testing.T) {
		_, _, err := doPreflightRequest(context.Background(), client, &AuthConfig{
			PreflightURL: sensitiveProviderURL(),
		})
		if err == nil {
			t.Fatal("doPreflightRequest succeeded with a failing transport")
		}
		assertRequestURLRedacted(t, err.Error())
		if !errors.Is(err, sentinel) {
			t.Fatalf("redaction broke the error chain: %v", err)
		}
	})

	t.Run("live search returned status persisted status and MCP source", func(t *testing.T) {
		rt, cfg := newCanaryProvider(t, "https://93.184.216.34/"+urlPathCanary+"?api_key="+urlQueryCanary)
		rt.getOrCreateClient(cfg).client = client

		_, statuses, err := rt.SearchHotels(context.Background(), "Kyoto", 35, 135, "2026-09-01", "2026-09-02", "EUR", 2, nil)
		if err == nil {
			t.Fatal("SearchHotels succeeded with a failing transport")
		}
		assertRequestURLRedacted(t, err.Error())
		if !errors.Is(err, sentinel) {
			t.Fatalf("redaction broke the returned error chain: %v", err)
		}
		if len(statuses) != 1 {
			t.Fatalf("provider statuses = %d, want 1", len(statuses))
		}
		assertRequestURLRedacted(t, statuses[0].Error)

		persisted := rt.registry.Get(cfg.ID)
		if persisted == nil {
			t.Fatal("provider disappeared from registry")
		}
		assertRequestURLRedacted(t, persisted.LastError)

		healthDir, err := HealthLogDir()
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if entry, ok := HealthSummary(healthDir)[cfg.ID]; ok && entry.LastError != "" {
				assertRequestURLRedacted(t, entry.LastError)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("provider failure was not available to the health/MCP source")
	})
}

func TestProviderDiagnosticHidesRequestURL(t *testing.T) {
	t.Setenv(AllowLocalEnv, "1")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	result := TestProvider(context.Background(), &ProviderConfig{
		ID:       "url-redaction-diagnostic",
		Name:     "URL redaction diagnostic",
		Category: "hotels",
		Endpoint: "http://" + address + "/" + urlPathCanary + "?api_key=" + urlQueryCanary,
		Method:   http.MethodGet,
	}, "Kyoto", 35, 135, "2026-09-01", "2026-09-02", "EUR", 2)
	if result.Error == "" {
		t.Fatal("TestProvider unexpectedly reached a closed listener")
	}
	assertRequestURLRedacted(t, result.Error)
}
