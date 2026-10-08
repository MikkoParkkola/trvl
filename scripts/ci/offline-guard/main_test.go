package main

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestReadExemptSkipsCommentsAndBlanks(t *testing.T) {
	in := "# header\n\ngithub.com/x/a  # MIK-1\n  github.com/x/b\n"
	got, err := readExempt(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["github.com/x/a"] || !got["github.com/x/b"] {
		t.Fatalf("readExempt = %v, want exactly github.com/x/a and github.com/x/b", got)
	}
}

// Lines as strace -yy writes them; one file per thread, so no interleaving.
func TestOutsideTargets(t *testing.T) {
	trace := strings.Join([]string{
		// Real outside TCP connects count, whatever the port and result.
		`connect(6<TCP:[640019132]>, {sa_family=AF_INET, sin_port=htons(443), sin_addr=inet_addr("142.251.156.119")}, 16) = -1 EINPROGRESS (Operation now in progress)`,
		`connect(7<TCPv6:[640019140]>, {sa_family=AF_INET6, sin6_port=htons(53), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "2001:4860:4802:32::78", &sin6_addr), sin6_scope_id=0}, 28) = -1 ECONNREFUSED (Connection refused)`,
		// A UDP connect sends nothing: Go's address-selection probe.
		`connect(4<UDP:[640032802]>, {sa_family=AF_INET, sin_port=htons(53), sin_addr=inet_addr("142.251.156.119")}, 16) = 0`,
		// A UDP send with an explicit outside destination counts.
		`sendto(6<UDPv6:[[::]:39832]>, "x", 1, 0, {sa_family=AF_INET6, sin6_port=htons(9), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "::ffff:192.0.2.2", &sin6_addr), sin6_scope_id=0}, 28) = 1`,
		// Loopback in every spelling stays local.
		`connect(7<TCP:[640032810]>, {sa_family=AF_INET, sin_port=htons(35165), sin_addr=inet_addr("127.0.0.1")}, 16) = -1 EINPROGRESS (Operation now in progress)`,
		`connect(7<TCP:[1]>, {sa_family=AF_INET, sin_port=htons(80), sin_addr=inet_addr("127.1.2.3")}, 16) = 0`,
		`connect(7<TCPv6:[2]>, {sa_family=AF_INET6, sin6_port=htons(80), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "::1", &sin6_addr), sin6_scope_id=0}, 28) = 0`,
		`connect(7<TCPv6:[3]>, {sa_family=AF_INET6, sin6_port=htons(80), sin6_flowinfo=htonl(0), inet_pton(AF_INET6, "::ffff:127.0.0.1", &sin6_addr), sin6_scope_id=0}, 28) = 0`,
		`sendto(5<UDP:[4]>, "q", 1, 0, {sa_family=AF_INET, sin_port=htons(53), sin_addr=inet_addr("127.0.0.53")}, 16) = 1`,
		// Unix sockets and exit markers are not network traffic.
		`connect(3<UNIX:[5]>, {sa_family=AF_UNIX, sun_path="/var/run/nscd/socket"}, 110) = -1 ENOENT (No such file or directory)`,
		`+++ exited with 0 +++`,
	}, "\n")
	got := outsideTargets(trace)
	want := []string{"142.251.156.119:443", "[2001:4860:4802:32::78]:53", "[::ffff:192.0.2.2]:9"}
	if !slices.Equal(got, want) {
		t.Fatalf("outsideTargets =\n  %v\nwant\n  %v", got, want)
	}
}

func requireStrace(t *testing.T) {
	t.Helper()
	if testing.Short() {
		// The guard itself runs go test -short under strace; a tracer cannot be
		// traced, and the leak fixture would flag the guard's own run.
		t.Skip("spawns strace; runs untraced in the offline-tests CI job")
	}
	if _, err := exec.LookPath("strace"); err != nil {
		t.Skip("strace not installed")
	}
}

func TestCheckPackageFlagsALeak(t *testing.T) {
	requireStrace(t)
	hits, err := checkPackage("./testdata/leaky", t.TempDir())
	if err != nil {
		t.Fatalf("checkPackage: %v (the fixture test itself passes)", err)
	}
	if !slices.Contains(hits, "192.0.2.1:443") {
		t.Fatalf("hits = %v, want 192.0.2.1:443", hits)
	}
}

func TestCheckPackagePassesLoopbackOnly(t *testing.T) {
	requireStrace(t)
	hits, err := checkPackage("./testdata/loopback", t.TempDir())
	if err != nil {
		t.Fatalf("checkPackage: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v, want none: httptest on loopback must not count", hits)
	}
}
