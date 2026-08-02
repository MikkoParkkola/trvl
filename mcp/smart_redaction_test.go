package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The webhook URL is the credential for Slack/Discord-style endpoints: anyone
// holding it can post as you. The standing rule is that it must never reach MCP
// structured output, logs or error strings, while the storage file and the CLI
// keep it by design.
//
// The `travel` router echoes the caller's params back as structured output, so
// without redaction a single watch_price call through it published the
// credential into model context, transcripts and client logs. Found by GPT
// second-opinion review, 2026-08-02.

const smartSecretToken = "T4K9m2QpR7vN1sW6xL8b"

func smartSecretURL() string {
	return "https://hooks.slack.com/services/T00000000/B11111111/" + smartSecretToken
}

func TestRedactSecretParamsMasksWebhookValues(t *testing.T) {
	params := map[string]any{
		"type":    "flight",
		"webhook": smartSecretURL(),
		"nested": map[string]any{
			"webhook_url": smartSecretURL(),
			"keep":        "visible",
		},
		"list": []any{
			map[string]any{"webhook": smartSecretURL()},
		},
	}

	got := redactSecretParams(params)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), smartSecretToken) {
		t.Errorf("credential survived redaction:\n%s", raw)
	}
	if !strings.Contains(string(raw), "visible") {
		t.Errorf("redaction destroyed non-secret fields:\n%s", raw)
	}

	// The live argument map must be untouched: the dispatched handler reads it,
	// and blanking the webhook there would silently stop the watch notifying.
	if params["webhook"] != smartSecretURL() {
		t.Errorf("redaction mutated the caller's params: webhook = %v", params["webhook"])
	}
}

func TestRedactSecretsInTextMasksURLPathAndQuery(t *testing.T) {
	in := "watch AMS to VLC and post to " + smartSecretURL()
	got := redactSecretsInText(in)
	if strings.Contains(got, smartSecretToken) {
		t.Errorf("credential survived text redaction: %q", got)
	}
	if !strings.Contains(got, "hooks.slack.com") {
		t.Errorf("host was dropped as well as the secret, leaving the line undiagnosable: %q", got)
	}
}

// The scan must ADVANCE past each rewrite. The first implementation searched
// from index 0 every iteration, so it re-found the URL it had just masked and
// span forever -- the package suite hit the 10-minute timeout instead of
// failing an assertion, which is a far worse failure mode than a wrong answer.
//
// Two URLs and a bare host are what make that visible: one URL alone can loop
// undetected in a single-assertion test.
func TestRedactSecretsInTextTerminatesOnMultipleURLs(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		done <- redactSecretsInText(
			"first " + smartSecretURL() +
				" then http://example.com/hook/" + smartSecretToken +
				" and a bare https://example.com plus trailing text")
	}()

	var got string
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("redactSecretsInText did not terminate: the scan is not advancing past a rewrite")
	}

	if strings.Contains(got, smartSecretToken) {
		t.Errorf("a credential survived redaction: %q", got)
	}
	if !strings.Contains(got, "hooks.slack.com") || !strings.Contains(got, "example.com") {
		t.Errorf("hosts were dropped: %q", got)
	}
	if !strings.Contains(got, "trailing text") {
		t.Errorf("text after the last URL was lost: %q", got)
	}
	// A bare host carries nothing secret and must be left intact.
	if !strings.Contains(got, "https://example.com plus") {
		t.Errorf("a bare host with no path was masked anyway: %q", got)
	}
}

// End-to-end: the whole serialized router response must not carry the token,
// whether it arrived via params or was typed into the query.
func TestTravelRouterResponseOmitsWebhookCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := &Server{}
	_, structured, _ := s.handleTravel(context.Background(), map[string]any{
		"query": "watch this flight and notify " + smartSecretURL(),
		"params": map[string]any{
			"type": "flight", "origin": "AMS", "destination": "VLC",
			"depart_date": "2027-07-01", "target_price": 200.0, "currency": "EUR",
			"webhook": smartSecretURL(),
		},
	}, nil, nil, nil)

	raw, err := json.Marshal(structured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), smartSecretToken) {
		t.Errorf("webhook credential leaked into travel-router structured output:\n%s", raw)
	}
}
