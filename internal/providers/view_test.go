package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialFreeViewsOmitProviderSecrets(t *testing.T) {
	const canary = "credential-canary-must-not-escape"
	configs := []*ProviderConfig{{
		ID:           "example",
		Name:         "Example",
		Category:     "hotels",
		Endpoint:     "https://api.example.test/private/" + canary + "?token=" + canary,
		Headers:      map[string]string{"Authorization": "Bearer " + canary},
		BodyTemplate: `{"secret":"` + canary + `"}`,
		Auth: &AuthConfig{
			Type:             "preflight",
			PreflightURL:     "https://auth.example.test/" + canary,
			PreflightBody:    canary,
			PreflightHeaders: map[string]string{"Authorization": canary},
		},
		LastError: "request failed with " + canary,
	}}

	encoded, err := json.Marshal(CredentialFreeViews(configs))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), canary) {
		t.Fatalf("credential-free provider view leaked canary: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"domain":"api.example.test"`) {
		t.Fatalf("credential-free view omitted diagnostic domain: %s", encoded)
	}
}
