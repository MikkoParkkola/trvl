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
	originalLandingClient, originalLandingLimiter := landingHTTPClient, landingLimiter
	t.Cleanup(func() {
		hometogoHTTPClient, hometogoLimiter = originalHomeToGoClient, originalHomeToGoLimiter
		uniplacesHTTPClient, uniplacesLimiter = originalUniplacesClient, originalUniplacesLimiter
		wunderflatsHTTPClient, wunderflatsLimiter = originalWunderflatsClient, originalWunderflatsLimiter
		spotahomeClient, spotahomeLimiter = originalSpotahomeClient, originalSpotahomeLimiter
		bluegroundClient, bluegroundLimiter = originalBluegroundClient, originalBluegroundLimiter
		anyplaceHTTPClient, anyplaceLimiter = originalAnyplaceClient, originalAnyplaceLimiter
		landingHTTPClient, landingLimiter = originalLandingClient, originalLandingLimiter
	})

	providers := []struct {
		name      string
		path      string
		generic   string
		sibling   string
		lookalike string
		get       func(context.Context, string) ([]byte, error)
		set       func(*http.Client)
	}{
		{
			name: "hometogo", path: "/lisbon/", generic: "/", sibling: "/porto/", lookalike: "/lisbon-cheap/",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return hometogoGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				hometogoHTTPClient = client
				hometogoLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "uniplaces", path: "/accommodation/lisbon", generic: "/accommodation", sibling: "/accommodation/porto", lookalike: "/accommodation/lisbon-cheap",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return uniplacesGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				uniplacesHTTPClient = client
				uniplacesLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "wunderflats", path: "/en/furnished-apartments/lisbon", generic: "/en/furnished-apartments", sibling: "/en/furnished-apartments/porto", lookalike: "/en/furnished-apartments/lisbon-cheap",
			get: wunderflatsGet,
			set: func(client *http.Client) {
				wunderflatsHTTPClient = client
				wunderflatsLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "spotahome", path: "/s/lisbon/for-rent:apartments.data", generic: "/s", sibling: "/s/porto/for-rent:apartments.data", lookalike: "/s/lisbon-cheap/for-rent:apartments.data",
			get: spotahomeGet,
			set: func(client *http.Client) {
				spotahomeClient = client
				spotahomeLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "blueground", path: "/furnished-apartments-athens-gr", generic: "/", sibling: "/furnished-apartments-paris-fr", lookalike: "/furnished-apartments-athens-gr-cheap",
			get: bluegroundGet,
			set: func(client *http.Client) {
				bluegroundClient = client
				bluegroundLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "anyplace", path: "/listings/lisbon", generic: "/listings", sibling: "/listings/porto", lookalike: "/listings/lisbon-cheap",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return anyplaceGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				anyplaceHTTPClient = client
				anyplaceLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
		{
			name: "landing", path: "/s/austin/apartments/furnished", generic: "/s", sibling: "/s/dallas/apartments/furnished", lookalike: "/s/austinite/apartments/furnished",
			get: func(ctx context.Context, rawURL string) ([]byte, error) {
				return landingGet(ctx, rawURL, "text/html")
			},
			set: func(client *http.Client) {
				landingHTTPClient = client
				landingLimiter = rate.NewLimiter(rate.Inf, 1)
			},
		},
	}

	scenarios := []struct {
		name           string
		path           func(providerPath, generic, sibling, lookalike string) string
		wantError      bool
		wantErrorText  string
		statusCode     int
		missingRequest bool
		missingURL     bool
	}{
		{name: "exact", path: func(p, _, _, _ string) string { return p }},
		{name: "trailing slash", path: func(p, _, _, _ string) string {
			if strings.HasSuffix(p, "/") {
				return strings.TrimSuffix(p, "/")
			}
			return p + "/"
		}},
		{name: "query only", path: func(p, _, _, _ string) string { return p + "?campaign=summer" }},
		{name: "generic parent", path: func(_, generic, _, _ string) string { return generic }, wantError: true, wantErrorText: "destination scope:"},
		{name: "sibling destination", path: func(_, _, sibling, _ string) string { return sibling }, wantError: true, wantErrorText: "destination scope:"},
		{name: "lookalike prefix", path: func(_, _, _, lookalike string) string { return lookalike }, wantError: true, wantErrorText: "destination scope:"},
		{name: "missing response request", path: func(p, _, _, _ string) string { return p }, wantError: true, wantErrorText: "destination scope:", missingRequest: true},
		{name: "missing effective URL", path: func(p, _, _, _ string) string { return p }, wantError: true, wantErrorText: "destination scope:", missingURL: true},
		{name: "non-2xx status takes precedence", path: func(_, _, sibling, _ string) string { return sibling }, wantError: true, wantErrorText: "unexpected status 502", statusCode: http.StatusBadGateway},
	}

	for _, provider := range providers {
		for _, scenario := range scenarios {
			t.Run(provider.name+"/"+scenario.name, func(t *testing.T) {
				effectivePath := scenario.path(provider.path, provider.generic, provider.sibling, provider.lookalike)
				client := &http.Client{Transport: destinationScopeRoundTripper(func(req *http.Request) (*http.Response, error) {
					effective := *req.URL
					parts := strings.SplitN(effectivePath, "?", 2)
					effective.Path = parts[0]
					effective.RawQuery = ""
					if len(parts) == 2 {
						effective.RawQuery = parts[1]
					}
					effectiveRequest := &http.Request{URL: &effective}
					if scenario.missingRequest {
						effectiveRequest = nil
					}
					if scenario.missingURL {
						effectiveRequest.URL = nil
					}
					statusCode := scenario.statusCode
					if statusCode == 0 {
						statusCode = http.StatusOK
					}
					return &http.Response{
						StatusCode: statusCode,
						Body:       io.NopCloser(strings.NewReader("destination inventory")),
						Request:    effectiveRequest,
					}, nil
				})}
				provider.set(client)

				body, err := provider.get(context.Background(), "https://provider.example"+provider.path)
				if scenario.wantError {
					if err == nil {
						t.Fatalf("accepted %q response body %q", effectivePath, body)
					}
					if !strings.Contains(err.Error(), scenario.wantErrorText) {
						t.Fatalf("error = %q, want text %q", err, scenario.wantErrorText)
					}
					if strings.Count(err.Error(), "destination scope:") > 1 {
						t.Fatalf("destination scope prefix duplicated in %q", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("rejected safe effective path %q: %v", effectivePath, err)
				}
				if string(body) != "destination inventory" {
					t.Fatalf("body = %q", body)
				}
			})
		}
	}
}
