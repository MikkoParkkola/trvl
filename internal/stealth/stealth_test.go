package stealth

import "testing"

// TestAuthorized_EmptyAllowlistRefusesByDefault proves the fail-safe default:
// an unset/empty allowlist never authorizes stealth for any host.
func TestAuthorized_EmptyAllowlistRefusesByDefault(t *testing.T) {
	// GIVEN: no allowlist configured.
	t.Setenv(AllowlistEnv, "")

	// WHEN/THEN: every host is refused.
	for _, host := range []string{"www.google.com", "google.com", "example.com", ""} {
		if Authorized(host) {
			t.Errorf("Authorized(%q) = true with empty allowlist; want false (fail-safe)", host)
		}
	}
}

// TestAuthorized_ReadsEnv proves Authorized consults TRVL_STEALTH_ALLOWLIST at
// call time (end-to-end through the public API, not just the pure core).
func TestAuthorized_ReadsEnv(t *testing.T) {
	// GIVEN: an allowlist with one exact host.
	t.Setenv(AllowlistEnv, "www.google.com")

	// WHEN/THEN: the listed host is authorized, an unlisted one is not.
	if !Authorized("www.google.com") {
		t.Error("Authorized(www.google.com) = false; want true (exact match in env)")
	}
	if Authorized("evil.example.com") {
		t.Error("Authorized(evil.example.com) = true; want false (not in env)")
	}
}

func TestAuthorizedIn(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		allowlist string
		want      bool
	}{
		{"empty allowlist refuses", "www.google.com", "", false},
		{"empty host refused", "", "www.google.com", false},
		{"whitespace-only allowlist refuses", "www.google.com", "   ,  ", false},
		{"exact match", "www.google.com", "www.google.com", true},
		{"exact non-match", "maps.google.com", "www.google.com", false},
		{"suffix match", "www.google.com", ".google.com", true},
		{"suffix match deep subdomain", "a.b.google.com", ".google.com", true},
		{"suffix match apex via dotted entry", "google.com", ".google.com", true},
		{"suffix non-match different domain", "www.google.com.evil.com", ".google.com", false},
		{"case-insensitive host", "WWW.GOOGLE.COM", "www.google.com", true},
		{"case-insensitive entry", "www.google.com", "WWW.GOOGLE.COM", true},
		{"case-insensitive suffix", "WWW.Google.CoM", ".GOOGLE.com", true},
		{"host with port", "www.google.com:443", "www.google.com", true},
		{"multi-entry first matches", "www.google.com", "www.google.com,example.com", true},
		{"multi-entry last matches", "example.com", "www.google.com,example.com", true},
		{"multi-entry none match", "other.com", "www.google.com,example.com", false},
		{"entry with surrounding spaces", "example.com", " example.com , foo.com ", true},
		{"suffix entry must not match unrelated", "notgoogle.com", ".google.com", false},
		{"bare suffix entry does not authorize substring", "evilgoogle.com", ".google.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authorizedIn(tt.host, tt.allowlist); got != tt.want {
				t.Errorf("authorizedIn(%q, %q) = %v; want %v", tt.host, tt.allowlist, got, tt.want)
			}
		})
	}
}

func TestCanonicalHost(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"www.google.com", "www.google.com"},
		{"WWW.GOOGLE.COM", "www.google.com"},
		{"  www.google.com  ", "www.google.com"},
		{"www.google.com:443", "www.google.com"},
		{"", ""},
		{"host:8080", "host"},
	}
	for _, tt := range tests {
		if got := canonicalHost(tt.in); got != tt.want {
			t.Errorf("canonicalHost(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}
