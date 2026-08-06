package providers

import (
	"strings"
	"testing"
)

// TRVL.LOGLEAK.6 -- if a log line keeps a hostname, the IPv6 zone identifier
// goes with the rest of the free text.
//
// url.Hostname() strips the port and the brackets and KEEPS the zone:
// "[fe80::1%25eth0]:443" yields "fe80::1%eth0". A zone identifier is free-form
// text carried inside an address. On a provider URL -- supplied by a
// user-defined provider, and #538 tracks how far that trust reaches -- it is an
// attacker-influenceable string arriving in a log line under a field named
// "host", which is the one field a reader will assume is a hostname.
//
// Round 6 of #530 found this on url.URL.Host. The same reduction reappeared
// here under a different name, in a sibling branch, which is precisely what
// #531's TRVL.LOGLEAK.4 warned would happen.
func TestHostForLogDropsTheIPv6ZoneIdentifier(t *testing.T) {
	// %25 is a literal "%" in a URL. A zone identifier must be encoded that way
	// to survive parsing, which is why a naive reader never sees it coming.
	got := hostForLog("https://[fe80::1%25eth0lets-see-what-else-fits-here]:443/search?from=HEL")

	if strings.Contains(got, "%") {
		t.Errorf("hostForLog kept an IPv6 zone identifier: %q. The zone is free-form text inside "+
			"the address, so a user-defined provider can put arbitrary content into a log field "+
			"named \"host\".", got)
	}
	if strings.Contains(got, "lets-see-what-else-fits-here") {
		t.Errorf("attacker-influenceable text survived into the host field: %q", got)
	}
	if got != "fe80::1" {
		t.Errorf("hostForLog = %q, want %q: the address itself must survive, or the field stops "+
			"telling an operator which provider failed and the whole justification for keeping a "+
			"host collapses", got, "fe80::1")
	}
}

// The control: ordinary hostnames must come through untouched. Cutting too
// eagerly would empty the one field this helper exists to provide, which is how
// it ends up deleted again.
func TestHostForLogKeepsOrdinaryHostnames(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://www.thetrainline.com/search?from=HEL&to=CDG", "www.thetrainline.com"},
		{"https://www.sncf-connect.com:8443/x", "www.sncf-connect.com"},
		{"https://[2001:db8::1]:443/x", "2001:db8::1"},
		{"://bad?from=HEL", "?"},
		{"/relative/path", "?"},
	} {
		if got := hostForLog(c.in); got != c.want {
			t.Errorf("hostForLog(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// And the query string never survives, which is the reason this helper exists
// at all. Asserted separately from the zone rule so a regression names which
// property it lost.
func TestHostForLogNeverKeepsTheJourney(t *testing.T) {
	got := hostForLog("https://www.thetrainline.com/search?from=HEL&to=CDG&date=2026-09-01&adults=2")
	for _, journey := range []string{"from=HEL", "to=CDG", "date=2026-09-01", "adults=2", "/search"} {
		if strings.Contains(got, journey) {
			t.Errorf("hostForLog leaked %q into a cookie log line: %q", journey, got)
		}
	}
}
