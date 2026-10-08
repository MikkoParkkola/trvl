package main

import (
	"io"
	"net/http"
	"net/url"
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

func TestChildEnvRoutesEverythingThroughTheGuard(t *testing.T) {
	base := []string{"PATH=/bin", "https_proxy=http://corp:3128", "HTTP_PROXY=http://corp:3128", "NO_PROXY=.example.com", "no_proxy=*", "GOPROXY=https://proxy.golang.org"}
	env := childEnv(base, "http://127.0.0.1:9")
	for _, want := range []string{"PATH=/bin", "HTTPS_PROXY=http://127.0.0.1:9", "HTTP_PROXY=http://127.0.0.1:9", "GOPROXY=off"} {
		if !slices.Contains(env, want) {
			t.Errorf("childEnv missing %q: %v", want, env)
		}
	}
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(key) {
		case "NO_PROXY":
			t.Errorf("childEnv kept %q; an inherited bypass list would hide leaks", kv)
		case "HTTPS_PROXY", "HTTP_PROXY":
			if !strings.HasSuffix(kv, "=http://127.0.0.1:9") {
				t.Errorf("childEnv kept outside proxy %q", kv)
			}
		}
	}
}

func TestRefusingProxyRecordsTheTarget(t *testing.T) {
	p, err := startProxy()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyURL, _ := url.Parse(p.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	if resp, err := client.Get("https://offline-guard.invalid/"); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		t.Fatal("request through the guard succeeded; want refusal")
	}
	if got := p.Hits(); !slices.Equal(got, []string{"offline-guard.invalid:443"}) {
		t.Fatalf("Hits = %v, want [offline-guard.invalid:443]", got)
	}
}

func TestRunFlagsAPackageThatLeaks(t *testing.T) {
	hits, err := run([]string{"./testdata/leaky"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("run: %v (the fixture test itself passes)", err)
	}
	if !slices.Contains(hits, "offline-guard.invalid:443") {
		t.Fatalf("hits = %v, want offline-guard.invalid:443", hits)
	}
}

func TestRunPassesALoopbackOnlyPackage(t *testing.T) {
	hits, err := run([]string{"./testdata/loopback"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v, want none: httptest on loopback must not count", hits)
	}
}
