package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// MIK-7989: a provider that blocks trvl from a cloud IP must be reported as
// blocked, not as "no results" and not lumped with rate limits or outages.
func TestClassifyProviderReason(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"", ""},
		{"sncf: HTTP 403 Forbidden", ReasonBlocked},
		{"trainline: blocked by datadome challenge", ReasonBlocked},
		{"rome2rio: status 403", ReasonBlocked},
		{"kiwi: HTTP 429 Too Many Requests", ReasonRateLimited},
		{"google: rate limit exceeded (403 text in body)", ReasonRateLimited},
		{"wizzair: HTTP 503 Service Unavailable", ReasonUnavailable},
		{"easyjet: 502 Bad Gateway", ReasonUnavailable},
		{`italo: Get "https://italotreno.com/x": dial tcp: lookup italotreno.com: no such host`, ReasonDNS},
		{"lookup api.example.com on 10.0.0.1:53: server misbehaving", ReasonDNS},
		{"net/http: TLS handshake timeout", ReasonTimeout},
		{"read tcp 10.0.0.2:5000->1.2.3.4:443: i/o timeout", ReasonTimeout},
		{"context deadline exceeded", ReasonTimeout},
		{`Get "https://d111.cloudfront.net/api": read: connection reset by peer`, ReasonNetwork},
		{`Get "https://blocked.example.com/x": dial tcp 1.2.3.4:443: connect: connection refused`, ReasonNetwork},
		{"unexpected JSON shape", ReasonOther},
	}
	for _, tc := range cases {
		if got := ClassifyProviderReason(tc.msg); got != tc.want {
			t.Errorf("ClassifyProviderReason(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}

func TestProviderStatusJSONCarriesReason(t *testing.T) {
	blocked, err := json.Marshal(ProviderStatus{ID: "sncf", Status: StatusRateLimited, Error: "HTTP 403 Forbidden"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blocked), `"reason":"blocked"`) {
		t.Fatalf("failed provider JSON must carry its reason, got %s", blocked)
	}

	ok, _ := json.Marshal(ProviderStatus{ID: "db", Status: StatusOK, Results: 3})
	if strings.Contains(string(ok), "reason") {
		t.Fatalf("a successful provider must not carry a reason, got %s", ok)
	}

	explicit, _ := json.Marshal(ProviderStatus{ID: "x", Status: StatusFailed, Error: "HTTP 403", Reason: ReasonDNS})
	if !strings.Contains(string(explicit), `"reason":"dns"`) {
		t.Fatalf("an explicit reason must be preserved, got %s", explicit)
	}

	// The marshaler must not recurse or drop the existing fields.
	var back map[string]any
	if err := json.Unmarshal(blocked, &back); err != nil {
		t.Fatal(err)
	}
	if back["id"] != "sncf" || back["error"] != "HTTP 403 Forbidden" {
		t.Fatalf("existing fields lost: %s", blocked)
	}
}

func TestEffectiveReasonMatchesJSON(t *testing.T) {
	s := ProviderStatus{Status: StatusFailed, Error: "lookup x: no such host"}
	if s.EffectiveReason() != ReasonDNS {
		t.Fatalf("EffectiveReason = %q, want dns", s.EffectiveReason())
	}
}
