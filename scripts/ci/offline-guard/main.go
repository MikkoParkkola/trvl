// Command offline-guard runs the default test suite under strace and fails if
// any test opened a connection to an outside host (MIK-8100).
//
// A test that leans on a short timeout, or ignores a provider error, still
// passes when the network is up, so a leak is invisible in an ordinary run.
// Detection is at the syscall because an HTTP proxy only sees clients that
// honour HTTPS_PROXY; the Google client (internal/batchexec) and the fhttp
// provider transport dial directly.
//
// Counted: a TCP connect to a non-loopback address, a datagram sent to an
// explicit non-loopback destination, and a write on a UDP socket connected to
// one. A UDP connect alone sends nothing (Go's resolver uses it to rank
// addresses) and is ignored.
//
// Linux only. Packages run one at a time so each hit names its package.
//
// Usage: go run ./scripts/ci/offline-guard [packages]
// With no packages it checks `go list ./...` minus scripts/ci/offline-exempt.txt.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	exemptPath := flag.String("exempt", "scripts/ci/offline-exempt.txt", "packages allowed to reach the network until fixed")
	flag.Parse()

	if err := exec.Command("strace", "-f", "--seccomp-bpf", "-e", "trace=connect", "-o", "/dev/null", "true").Run(); err != nil {
		fail(2, "strace with --seccomp-bpf is required: %v", err)
	}

	pkgs := flag.Args()
	if len(pkgs) == 0 {
		var err error
		if pkgs, err = defaultPackages(*exemptPath); err != nil {
			fail(2, "%v", err)
		}
	}

	dir, err := os.MkdirTemp("", "offline-guard-")
	if err != nil {
		fail(2, "%v", err)
	}

	leaks := 0
	var failed []string
	for i, pkg := range pkgs {
		hits, err := checkPackage(pkg, filepath.Join(dir, strconv.Itoa(i)))
		if err != nil {
			failed = append(failed, pkg)
			fmt.Fprintf(os.Stderr, "offline-guard: %s: %v\n", pkg, err)
		}
		if len(hits) > 0 {
			leaks++
			fmt.Fprintf(os.Stderr, "offline-guard: %s reached outside hosts:\n", pkg)
			for _, line := range summarize(hits) {
				fmt.Fprintln(os.Stderr, "  "+line)
			}
		}
	}
	_ = os.RemoveAll(dir) // os.Exit below skips deferred calls
	fmt.Printf("offline-guard: %d packages checked, %d reached outside hosts, %d failed\n", len(pkgs), leaks, len(failed))
	if leaks > 0 || len(failed) > 0 {
		os.Exit(1)
	}
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "offline-guard: "+format+"\n", args...)
	os.Exit(code)
}

// defaultPackages lists the module's packages minus the exempt ones. A go list
// that fails or finds nothing is an error, never a vacuous pass.
func defaultPackages(exemptPath string) ([]string, error) {
	// #nosec G304 -- the exempt list path comes from the -exempt flag set by
	// the CI workflow.
	f, err := os.Open(exemptPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	exempt, err := readExempt(f)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "list", "./...")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	all := strings.Fields(string(out))
	if len(all) == 0 {
		return nil, errors.New("go list found no packages")
	}
	var pkgs []string
	for _, p := range all {
		if !exempt[p] {
			pkgs = append(pkgs, p)
		}
	}
	return pkgs, nil
}

// readExempt parses one import path per line; "#" starts a comment.
func readExempt(r io.Reader) (map[string]bool, error) {
	exempt := make(map[string]bool)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if line = strings.TrimSpace(line); line != "" {
			exempt[line] = true
		}
	}
	return exempt, sc.Err()
}

// checkPackage runs one package's short tests under strace, writing one trace
// file per thread under dir, and returns the outside targets they reached.
func checkPackage(pkg, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// #nosec G204 -- fixed strace and go executables; pkg is an import path
	// from go list or the CI workflow.
	cmd := exec.Command("strace", "-f", "-ff", "-yy", "-v", "--seccomp-bpf",
		"-e", "trace=connect,sendto,sendmsg,sendmmsg,write,writev", "-o", filepath.Join(dir, "t"),
		"go", "test", "-short", "-count=1", pkg)
	// Dependencies are downloaded before the run; module fetches must not
	// count as test traffic.
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	runErr := cmd.Run()
	if runErr != nil {
		runErr = fmt.Errorf("go test under strace: %w", runErr)
	}

	files, err := filepath.Glob(filepath.Join(dir, "t.*"))
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- trace file this process just created
		if err != nil {
			return nil, err
		}
		hits = append(hits, outsideTargets(string(b))...)
	}
	return hits, runErr
}

var (
	callRe = regexp.MustCompile(`^(connect|sendto|sendmsg|sendmmsg)\(\d+<([A-Za-z0-9]+):`)
	in4Re  = regexp.MustCompile(`sin_port=htons\((\d+)\), sin_addr=inet_addr\("([^"]+)"\)`)
	in6Re  = regexp.MustCompile(`sin6_port=htons\((\d+)\).*?inet_pton\(AF_INET6, "([^"]+)"`)
	// -yy prints a connected socket as <UDP:[local->peer]>.
	udpWriteRe = regexp.MustCompile(`^writev?\(\d+<UDP(?:v6)?:\[.*?->(\[[^\]]+\]:\d+|[^\]>]+)\]>`)
)

// outsideTargets returns "addr:port" for each TCP connect, each datagram sent
// to an explicit destination (every message of a batch), and each write on a
// connected UDP socket, that targets a non-loopback address.
func outsideTargets(trace string) []string {
	var out []string
	add := func(addr, port string) {
		ip, err := netip.ParseAddr(addr)
		if err != nil {
			return
		}
		if u := ip.Unmap(); u.IsLoopback() || u.IsUnspecified() {
			return
		}
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	for _, line := range strings.Split(trace, "\n") {
		if w := udpWriteRe.FindStringSubmatch(line); w != nil {
			if ap, err := netip.ParseAddrPort(w[1]); err == nil {
				add(ap.Addr().String(), strconv.Itoa(int(ap.Port())))
			}
			continue
		}
		m := callRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[1] == "connect" && !strings.HasPrefix(m[2], "TCP") {
			continue
		}
		for _, a := range in4Re.FindAllStringSubmatch(line, -1) {
			add(a[2], a[1])
		}
		for _, a := range in6Re.FindAllStringSubmatch(line, -1) {
			add(a[2], a[1])
		}
	}
	return out
}

func summarize(hits []string) []string {
	counts := make(map[string]int)
	for _, h := range hits {
		counts[h]++
	}
	lines := make([]string, 0, len(counts))
	for h, n := range counts {
		lines = append(lines, fmt.Sprintf("%dx %s", n, h))
	}
	sort.Strings(lines)
	return lines
}
