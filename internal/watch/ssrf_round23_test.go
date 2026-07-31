package watch

import (
	"net"
	"testing"
)

// TestIsPublicWebhookIP_SpecialUseBlocks proves round 23's fix: the round-22
// SSRF guard only checked IsLoopback/IsPrivate/IsLinkLocalUnicast/
// IsLinkLocalMulticast/IsUnspecified/IsMulticast, which let RFC 6598
// carrier-grade-NAT/shared address space and several IETF reserved/
// documentation blocks (any of which can be assigned to real internal
// infrastructure behind a NAT or shared translator) resolve as "public".
// Found by GPT second-opinion review, 2026-07-30 (round 23).
func TestIsPublicWebhookIP_SpecialUseBlocks(t *testing.T) {
	blocked := []string{
		"0.1.2.3",         // 0.0.0.0/8
		"100.64.0.1",      // RFC 6598 CGNAT
		"100.100.100.100", // RFC 6598 CGNAT (used by real cloud metadata proxies)
		"100.127.255.255", // RFC 6598 CGNAT, top of range
		"192.0.0.8",       // IETF protocol assignments
		"192.0.2.55",      // TEST-NET-1
		"198.18.0.1",      // benchmarking
		"198.51.100.7",    // TEST-NET-2
		"203.0.113.9",     // TEST-NET-3
		"240.0.0.1",       // reserved
		"2001:db8::1",     // IPv6 documentation
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("net.ParseIP(%q) = nil, bad test input", s)
		}
		if isPublicWebhookIP(ip) {
			t.Errorf("isPublicWebhookIP(%q) = true, want false (special-use/non-public range)", s)
		}
	}
}

// TestIsPublicWebhookIP_AllowsGenuinelyPublicAddresses is the paired negative
// case: real public internet addresses must still be allowed through, so the
// new CIDR list doesn't overreach into legitimate webhook targets.
func TestIsPublicWebhookIP_AllowsGenuinelyPublicAddresses(t *testing.T) {
	allowed := []string{
		"8.8.8.8",              // public DNS
		"1.1.1.1",              // public DNS
		"93.184.216.34",        // example.com-class public address
		"2606:4700:4700::1111", // Cloudflare public IPv6 DNS
	}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("net.ParseIP(%q) = nil, bad test input", s)
		}
		if !isPublicWebhookIP(ip) {
			t.Errorf("isPublicWebhookIP(%q) = false, want true (genuinely public address)", s)
		}
	}
}

// TestIsPublicWebhookIP_StillRejectsRound22Cases guards against a regression
// in the round-22 checks while adding the round-23 CIDR list.
func TestIsPublicWebhookIP_StillRejectsRound22Cases(t *testing.T) {
	blocked := []string{
		"127.0.0.1",       // loopback
		"10.0.0.1",        // RFC1918 private
		"172.16.0.1",      // RFC1918 private
		"192.168.1.1",     // RFC1918 private
		"169.254.169.254", // link-local / cloud metadata
		"0.0.0.0",         // unspecified
		"224.0.0.1",       // multicast
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("net.ParseIP(%q) = nil, bad test input", s)
		}
		if isPublicWebhookIP(ip) {
			t.Errorf("isPublicWebhookIP(%q) = true, want false (round-22 case)", s)
		}
	}
}
