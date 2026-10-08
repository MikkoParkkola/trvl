// Command offline-guard runs the default test suite behind a local HTTP proxy
// that refuses every request, and fails if any test tried to reach an outside
// host (MIK-8100).
//
// A test that leans on a short timeout, or ignores a provider error, still
// passes when the network is up, so a leak is invisible in an ordinary run.
// Routing the suite through a refusing proxy turns each attempt into a recorded
// target. Go's ProxyFromEnvironment never proxies loopback, so httptest servers
// are unaffected.
//
// Limit: a client that ignores HTTPS_PROXY (a custom transport with no Proxy
// func, or a raw dialer) connects directly and is not seen.
//
// Usage: go run ./scripts/ci/offline-guard [packages]
// With no packages it tests `go list ./...` minus scripts/ci/offline-exempt.txt.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	exemptPath := flag.String("exempt", "scripts/ci/offline-exempt.txt", "packages allowed to reach the network until fixed")
	flag.Parse()

	pkgs := flag.Args()
	if len(pkgs) == 0 {
		var err error
		if pkgs, err = defaultPackages(*exemptPath); err != nil {
			fmt.Fprintln(os.Stderr, "offline-guard:", err)
			os.Exit(2)
		}
	}

	hits, err := run(pkgs, os.Stdout, os.Stderr)
	if len(hits) > 0 {
		fmt.Fprintf(os.Stderr, "offline-guard: tests tried to reach %d outside target(s); the default suite must stay offline:\n", len(hits))
		for _, line := range summarize(hits) {
			fmt.Fprintln(os.Stderr, "  "+line)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "offline-guard:", err)
	}
	if err != nil || len(hits) > 0 {
		os.Exit(1)
	}
}

func defaultPackages(exemptPath string) ([]string, error) {
	f, err := os.Open(exemptPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	exempt, err := readExempt(f)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("go", "list", "./...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var pkgs []string
	for _, p := range strings.Fields(string(out)) {
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

// run tests pkgs behind a refusing proxy and returns every outside target the
// tests asked for, in arrival order.
func run(pkgs []string, stdout, stderr io.Writer) ([]string, error) {
	p, err := startProxy()
	if err != nil {
		return nil, err
	}
	args := append([]string{"test", "-short", "-count=1"}, pkgs...)
	// #nosec G204 -- fixed go executable; arguments are package patterns from
	// the CI workflow or go list.
	cmd := exec.Command("go", args...)
	cmd.Env = childEnv(os.Environ(), p.URL())
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	p.Close()
	if runErr != nil {
		runErr = fmt.Errorf("go test: %w", runErr)
	}
	return p.Hits(), runErr
}

// childEnv points every proxy setting at the guard. An inherited NO_PROXY
// would let listed hosts bypass it, and module downloads must not count as
// test traffic, so GOPROXY is off: dependencies are fetched before the run.
func childEnv(base []string, proxyURL string) []string {
	env := make([]string, 0, len(base)+3)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "GOPROXY":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HTTPS_PROXY="+proxyURL, "HTTP_PROXY="+proxyURL, "GOPROXY=off")
}

type refusingProxy struct {
	ln   net.Listener
	wg   sync.WaitGroup
	mu   sync.Mutex
	hits []string
}

func startProxy() (*refusingProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &refusingProxy{ln: ln}
	p.wg.Add(1)
	go p.serve()
	return p, nil
}

func (p *refusingProxy) URL() string { return "http://" + p.ln.Addr().String() }

func (p *refusingProxy) serve() {
	defer p.wg.Done()
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		p.wg.Add(1)
		go p.refuse(conn)
	}
}

// refuse records the request target and answers 403. CONNECT carries
// host:port; a plain proxied request carries an absolute URL.
func (p *refusingProxy) refuse(conn net.Conn) {
	defer p.wg.Done()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	target := fields[1]
	if fields[0] != "CONNECT" {
		if u, err := url.Parse(target); err == nil && u.Host != "" {
			target = u.Host
		}
	}
	p.mu.Lock()
	p.hits = append(p.hits, target)
	p.mu.Unlock()
	_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
}

// Close stops accepting and waits for in-flight requests, so a request a test
// sent just before exiting is still counted.
func (p *refusingProxy) Close() {
	_ = p.ln.Close()
	p.wg.Wait()
}

func (p *refusingProxy) Hits() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hits...)
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
