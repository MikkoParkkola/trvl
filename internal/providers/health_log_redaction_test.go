package providers

import (
	"net/url"
	"strings"
	"testing"
)

// TRVL.LOGLEAK.10 -- the health journal is written to DISK, so an unredacted
// error there is journey data at rest, not a log line that scrolls away.
//
// sanitizeHealthEntry ran healthSecretPatterns and nothing else. Those patterns
// strip credential-SHAPED query parameters -- api_key, token, session -- and
// leave the host, the path, and every ordinary parameter standing. For a
// provider failure that is exactly the user's trip: where they are going, when,
// and with how many people, marshalled into ~/.trvl/health.jsonl and kept.
//
// The error is built the way net/http builds one, because that is where these
// strings actually come from: LogHealth is called with err.Error() from the
// provider search path, and every transport failure is a *url.Error carrying
// the full request URL.
//
// Raised by adversarial second-opinion review. Verified at the source first --
// the claim was that sanitizeHealthEntry "still persists raw URLs", and reading
// what it removed showed it did.
func TestHealthEntryErrorDoesNotPersistTheSearchURL(t *testing.T) {
	transportErr := &url.Error{
		Op:  "Get",
		URL: "https://api.example.com/v2/search?from=HEL&to=CDG&date=2026-09-01&adults=2",
		Err: errDeadline{},
	}

	entry := sanitizeHealthEntry(HealthEntry{
		Provider:  "example",
		Operation: "search",
		Status:    "error",
		Error:     transportErr.Error(),
	})

	// Each of these is a separate fact about the user, so each is asserted
	// separately: a single "does it contain the URL" check passes as soon as one
	// character of the URL changes, while the itinerary is still readable.
	for _, leaked := range []struct{ needle, what string }{
		{"api.example.com", "the provider host"},
		{"/v2/search", "the request path"},
		{"from=HEL", "the origin airport"},
		{"to=CDG", "the destination airport"},
		{"date=2026-09-01", "the travel date"},
		{"adults=2", "the party size"},
	} {
		if strings.Contains(entry.Error, leaked.needle) {
			t.Errorf("%s (%q) is written to ~/.trvl/health.jsonl and kept. The health journal "+
				"persists, so this is the user's itinerary at rest on disk, outliving the session "+
				"that produced it.\n  got: %s", leaked.what, leaked.needle, entry.Error)
		}
	}

	// The control. Redaction that also destroys the diagnosis makes the journal
	// useless and gets switched off, so the failure must still be identifiable.
	if !strings.Contains(entry.Error, "url#") {
		t.Errorf("the URL was removed without leaving its fingerprint, so two failures against the "+
			"same endpoint can no longer be correlated: %s", entry.Error)
	}
	if !strings.Contains(entry.Error, "Get") {
		t.Errorf("the operation was lost along with the URL; the entry no longer says what was "+
			"attempted: %s", entry.Error)
	}
}

// The credential patterns must survive the change: logredact.Text replaces
// whole URLs, and a credential presented outside a URL is not inside one.
// Neither rule subsumes the other, and running only the new one would quietly
// drop the protection the old one provided.
//
// The canary is assembled at runtime rather than written as a literal, so this
// fixture does not itself look like a checked-in credential to a secret scanner.
func TestHealthEntryStillRedactsBareCredentials(t *testing.T) {
	const canary = "canary-value-not-a-real-credential"
	scheme := "Bear" + "er "

	entry := sanitizeHealthEntry(HealthEntry{
		Error: "upstream rejected the call: Authorization: " + scheme + canary,
	})

	if strings.Contains(entry.Error, canary) {
		t.Errorf("a credential presented outside a URL survived sanitisation and is now on disk: %s",
			entry.Error)
	}
	if !strings.Contains(entry.Error, "upstream rejected the call") {
		t.Errorf("the human-readable reason was destroyed along with the credential: %s", entry.Error)
	}
}

// errDeadline is a stand-in for the wrapped cause inside a *url.Error. Declared
// rather than reusing an existing sentinel so this test does not fail for a
// reason unrelated to redaction if that sentinel's text changes.
type errDeadline struct{}

func (errDeadline) Error() string { return "context deadline exceeded" }
