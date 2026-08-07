package batchexec

import (
	"net/http"
	"testing"
)

// trvl#539 TRVL.HARDEN.2 -- stealthClient decides by an explicit flag, not by
// asking whether its transport is a test type.
//
// The production code used to type-assert on the test redirect transport, which
// is what forced that type to live in a production file and kept the exported
// test constructor reachable from production. Production has no business asking
// "am I in a test"; reuseTransportForStealth says what the branch actually
// decides.
//
// Asserted because the flag is the load-bearing half of that change. If it
// stopped being honoured, the test constructor could be moved back and nothing
// would notice until a stealth-engaged test started reaching the network.
func TestStealthClientReusesTheTransportWhenTold(t *testing.T) {
	own := &http.Client{}
	c := &Client{http: own, reuseTransportForStealth: true}

	if got := c.stealthClient(); got != own {
		t.Error("stealthClient built a new client despite reuseTransportForStealth; a " +
			"stealth-engaged test would leave the fixture and reach the real Chrome fingerprint " +
			"path, which is exactly what this flag exists to prevent")
	}
}

// The control: a production client, which never sets the flag, must still get
// the real stealth transport. Without this, hard-wiring reuse would pass the
// test above while silently disabling stealth for every user.
func TestStealthClientBuildsTheRealOneByDefault(t *testing.T) {
	own := &http.Client{}
	c := &Client{http: own}

	if got := c.stealthClient(); got == own {
		t.Error("stealthClient reused the plain transport for a client that never asked; " +
			"production would lose the Chrome fingerprint that clears bot detection")
	}
}

// NewClient must leave the flag false. It is the constructor every production
// caller uses, and a default of true would disable stealth everywhere.
func TestNewClientDoesNotReuseTransportForStealth(t *testing.T) {
	if NewClient().reuseTransportForStealth {
		t.Error("NewClient sets reuseTransportForStealth; production clients would skip the real " +
			"stealth transport entirely")
	}
}
