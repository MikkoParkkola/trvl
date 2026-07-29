package mcp

import (
	"context"
	"strings"
	"testing"
)

// captureConsent runs handleConfigureProvider far enough to build the consent
// prompt, returns that prompt, and declines, so nothing is saved.
func captureConsent(t *testing.T, args map[string]any) string {
	t.Helper()
	reg := testRegistry(t)
	var prompt string
	elicit := func(message string, _ map[string]interface{}) (map[string]interface{}, error) {
		prompt = message
		return nil, nil
	}
	if _, _, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil); err != nil {
		t.Fatalf("handleConfigureProvider: %v", err)
	}
	if prompt == "" {
		t.Fatal("no consent prompt was produced")
	}
	return prompt
}

func baseConsentArgs() map[string]any {
	return map[string]any{
		"id":            "consent-preflight",
		"name":          "Consent Preflight",
		"category":      "hotels",
		"endpoint":      "https://api.example.com/hotels",
		"results_path":  "$.data",
		"field_mapping": map[string]any{"name": "$.name"},
	}
}

// TRVL.SSRF.PUBLIC.3 -- auth_preflight_url is a second host that trvl contacts
// on the user's behalf, and on the browser escape hatch opens in their own
// browser. A prompt that names only the endpoint gets consent for one address
// while a second travels with it unnamed.
func TestConsentPromptNamesPreflightHost(t *testing.T) {
	t.Parallel()
	args := baseConsentArgs()
	args["auth_type"] = "cookie"
	args["auth_preflight_url"] = "https://auth.elsewhere.example/session"

	prompt := captureConsent(t, args)

	if !strings.Contains(prompt, "auth.elsewhere.example") {
		t.Fatalf("consent prompt does not name the preflight host:\n%s", prompt)
	}
	if !strings.Contains(prompt, "api.example.com") {
		t.Errorf("consent prompt no longer names the endpoint host:\n%s", prompt)
	}
}

// The line is conditional, so the no-preflight case has to be checked too: a
// prompt with a dangling bullet naming nothing is its own kind of dishonest.
func TestConsentPromptOmitsPreflightLineWhenAbsent(t *testing.T) {
	t.Parallel()
	prompt := captureConsent(t, baseConsentArgs())

	if strings.Contains(prompt, "Contact `` first") {
		t.Fatalf("consent prompt has an empty preflight bullet:\n%s", prompt)
	}
	if strings.Contains(prompt, "first to obtain a session") {
		t.Fatalf("consent prompt names a preflight step for a provider that has none:\n%s", prompt)
	}
}

// auth_preflight_url can be set without auth_type, in which case parsing drops
// it. Whatever the parser does, the prompt must not claim a preflight the
// runtime will not make -- so this pins prompt and behaviour together.
func TestConsentPromptPreflightLineMatchesParsedConfig(t *testing.T) {
	t.Parallel()
	args := baseConsentArgs()
	args["auth_preflight_url"] = "https://orphan.example/session"

	prompt := captureConsent(t, args)

	if strings.Contains(prompt, "orphan.example") {
		t.Fatalf("consent prompt names a preflight host that auth_type did not enable:\n%s", prompt)
	}
}
