package hotels

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

type destinationScopeRoundTripper func(*http.Request) (*http.Response, error)

func (f destinationScopeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDestinationScopedProviderGettersRejectUnscopedResponses(t *testing.T) {
	originalHomeToGoClient, originalHomeToGoLimiter := hometogoHTTPClient, hometogoLimiter
	originalUniplacesClient, originalUniplacesLimiter := uniplacesHTTPClient, uniplacesLimiter
	originalWunderflatsClient, originalWunderflatsLimiter := wunderflatsHTTPClient, wunderflatsLimiter
	originalSpotahomeClient, originalSpotahomeLimiter := spotahomeClient, spotahomeLimiter
	originalBluegroundClient, originalBluegroundLimiter := bluegroundClient, bluegroundLimiter
	originalAnyplaceClient, originalAnyplaceLimiter := anyplaceHTTPClient, anyplaceLimiter
	t.Cleanup(func() {
		hometogoHTTPClient, hometogoLimiter = originalHomeToGoClient, originalHomeToGoLimiter
		uniplacesHTTPClient, uniplacesLimiter = originalUniplacesClient, originalUniplacesLimiter
		wunderflatsHTTPClient, wunderflatsLimiter = originalWunderflatsClient, originalWunderflatsLimiter
		spotahomeClient, spotahomeLimiter = originalSpotahomeClient, originalSpotahomeLimiter
		bluegroundClient, bluegroundLimiter = originalBluegroundClient, originalBluegroundLimiter
		anyplaceHTTPClient, anyplaceLimiter = originalAnyplaceClient, originalAnyplaceLimiter
	})

	providers := []struct {
		name string
		get  func(context.Context, string) ([]byte, error)
		set  func(*http.Client)
	}{
		{
			name: "hometogo",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return hometogoGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				hometogoHTTPClient = client
				hometogoLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "uniplaces",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return uniplacesGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				uniplacesHTTPClient = client
				uniplacesLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "wunderflats",
			get:  wunderflatsGet,
			set: func(client *http.Client) {
				wunderflatsHTTPClient = client
				wunderflatsLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "spotahome",
			get:  spotahomeGet,
			set: func(client *http.Client) {
				spotahomeClient = client
				spotahomeLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "blueground",
			get:  bluegroundGet,
			set: func(client *http.Client) {
				bluegroundClient = client
				bluegroundLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "anyplace",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return anyplaceGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				anyplaceHTTPClient = client
				anyplaceLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
	}

	scenarios := []struct {
		name          string
		effectivePath string
		wantError     bool
		missingURL    bool
	}{
		{name: "exact", effectivePath: "/s/lisbon"},
		{name: "trailing slash", effectivePath: "/s/lisbon/"},
		{name: "query only", effectivePath: "/s/lisbon?campaign=summer"},
		{name: "generic parent", effectivePath: "/s", wantError: true},
		{name: "sibling destination", effectivePath: "/s/porto", wantError: true},
		{name: "lookalike prefix", effectivePath: "/s/lisbon-cheap", wantError: true},
		{name: "missing effective URL", wantError: true, missingURL: true},
	}

	for _, provider := range providers {
		for _, scenario := range scenarios {
			t.Run(provider.name+"/"+scenario.name, func(t *testing.T) {
				client := &http.Client{Transport: destinationScopeRoundTripper(func(req *http.Request) (*http.Response, error) {
					effective := *req.URL
					parts := strings.SplitN(scenario.effectivePath, "?", 2)
					effective.Path = parts[0]
					effective.RawQuery = ""
					if len(parts) == 2 {
						effective.RawQuery = parts[1]
					}
					effectiveRequest := &http.Request{URL: &effective}
					if scenario.missingURL {
						effectiveRequest.URL = nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader("destination inventory")),
						Request:    effectiveRequest,
					}, nil
				})}
				provider.set(client)

				body, err := provider.get(context.Background(), "https://provider.example/s/lisbon")
				if scenario.wantError {
					if err == nil {
						t.Fatalf("accepted %q response body %q", scenario.effectivePath, body)
					}
					return
				}
				if err != nil {
					t.Fatalf("rejected safe effective path %q: %v", scenario.effectivePath, err)
				}
				if string(body) != "destination inventory" {
					t.Fatalf("body = %q", body)
				}
			})
		}
	}
}
