package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/providers"
)

// TestParseProviderConfigRefusesLocalDestinations pins the refusal at the seam
// where a caller hands trvl a provider: a config aimed at the local machine,
// the private network, or the cloud metadata address is rejected outright, so
// it is never stored and never searched.
//
// The request-time policy in internal/providers is what actually stops the
// packet; this asserts the caller is told no at the point they ask, with an
// error rather than a config that quietly fails later.
func TestParseProviderConfigRefusesLocalDestinations(t *testing.T) {
	t.Setenv(providers.AllowLocalEnv, "")

	base := func(endpoint string) map[string]any {
		return map[string]any{
			"id":           "probe",
			"name":         "Probe",
			"category":     "hotels",
			"endpoint":     endpoint,
			"results_path": "$.results",
		}
	}

	for _, endpoint := range []string{
		"http://127.0.0.1:8080/search",
		"http://localhost:8080/search",
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://10.0.0.7/internal",
		"file:///etc/passwd",
	} {
		_, err := parseProviderConfig(base(endpoint))
		if err == nil {
			t.Fatalf("parseProviderConfig(endpoint=%q) = nil error, want refusal", endpoint)
		}
		if !errors.Is(err, providers.ErrDestinationRefused) {
			t.Fatalf("parseProviderConfig(endpoint=%q) error %v does not wrap ErrDestinationRefused", endpoint, err)
		}
		if !strings.Contains(err.Error(), "endpoint") {
			t.Fatalf("error %q does not name the offending field", err)
		}
	}

	// The preflight URL is a second destination the caller controls, so it is
	// checked too -- refusing only the endpoint would leave the request that
	// runs first unguarded.
	args := base("https://api.example.com/search")
	args["auth_type"] = "preflight"
	args["auth_preflight_url"] = "http://169.254.169.254/latest/api/token"
	_, err := parseProviderConfig(args)
	if !errors.Is(err, providers.ErrDestinationRefused) {
		t.Fatalf("preflight url to the metadata address: got %v, want refusal", err)
	}

	// A public destination is unaffected.
	if _, err := parseProviderConfig(base("https://api.example.com/search")); err != nil {
		t.Fatalf("public endpoint refused: %v", err)
	}
}
