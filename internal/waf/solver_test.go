package waf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/sobek"
)

// newTestHost spins up a bare vmHost with no HTTP client. Useful for stubs-only
// assertions.
func newTestHost(t *testing.T, client *http.Client) (*sobek.Runtime, *eventLoop, *vmHost) {
	t.Helper()
	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, client, "https://example.com", "test-ua/1.0")
	if err := host.install(); err != nil {
		t.Fatalf("install stubs: %v", err)
	}
	return vm, loop, host
}

func TestStubsLoadWithoutError(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)
	// Probe every promised global.
	globals := []string{
		"window", "self", "top", "parent", "document", "navigator", "screen",
		"location", "history", "performance", "localStorage", "sessionStorage",
		"crypto", "setTimeout", "setInterval", "clearTimeout", "clearInterval",
		"queueMicrotask", "requestAnimationFrame", "Event", "CustomEvent",
		"MessageEvent", "MutationObserver", "IntersectionObserver",
		"ResizeObserver", "PerformanceObserver", "fetch", "XMLHttpRequest",
		"atob", "btoa",
		"console", "TextEncoder", "TextDecoder", "AbortController",
		"URL", "URLSearchParams",
	}
	for _, name := range globals {
		v := vm.Get(name)
		if v == nil || sobek.IsUndefined(v) || sobek.IsNull(v) {
			t.Errorf("global %q not installed", name)
		}
	}
	// Navigator surface.
	uaVal, err := vm.RunString(`navigator.userAgent`)
	if err != nil || uaVal.String() != "test-ua/1.0" {
		t.Errorf("navigator.userAgent: got %q err=%v", uaVal, err)
	}
	// crypto.subtle is an object with digest.
	if _, err := vm.RunString(`typeof crypto.subtle.digest === "function" || (function(){throw new Error("no digest")})()`); err != nil {
		t.Errorf("crypto.subtle.digest missing: %v", err)
	}
	// document.cookie round-trip.
	if _, err := vm.RunString(`document.cookie = "foo=bar"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	got, err := vm.RunString(`document.cookie`)
	if err != nil || !strings.Contains(got.String(), "foo=bar") {
		t.Errorf("document.cookie round-trip failed: %q err=%v", got, err)
	}
}

func TestConsoleDoesNotCrash(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)
	// All console methods should be callable without throwing.
	_, err := vm.RunString(`
		console.log("hello", 42);
		console.warn("w");
		console.error("e");
		console.info("i");
		console.debug("d");
		console.trace();
		console.dir({});
		console.table([]);
		console.assert(true);
		console.time("t");
		console.timeEnd("t");
		console.group("g");
		console.groupEnd();
		console.groupCollapsed("gc");
	`)
	if err != nil {
		t.Fatalf("console methods threw: %v", err)
	}
}

func TestCanvasGetContext2D(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)
	v, err := vm.RunString(`
		var c = document.createElement("canvas");
		var ctx = c.getContext("2d");
		ctx.fillText("test", 0, 0);
		var m = ctx.measureText("hello");
		var url = c.toDataURL();
		JSON.stringify({width: c.width, height: c.height, tw: m.width, hasURL: url.indexOf("data:image/png") === 0});
	`)
	if err != nil {
		t.Fatalf("canvas stub threw: %v", err)
	}
	got := v.String()
	if !strings.Contains(got, `"width":300`) || !strings.Contains(got, `"hasURL":true`) {
		t.Errorf("unexpected canvas result: %s", got)
	}
}

func TestTextEncoderDecoder(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)
	v, err := vm.RunString(`
		var enc = new TextEncoder();
		var buf = enc.encode("ABC");
		var dec = new TextDecoder();
		var out = dec.decode(buf);
		out + "|" + buf.length;
	`)
	if err != nil {
		t.Fatalf("TextEncoder/Decoder threw: %v", err)
	}
	if v.String() != "ABC|3" {
		t.Errorf("TextEncoder/Decoder mismatch: %q", v.String())
	}
}

func TestAbortController(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)
	v, err := vm.RunString(`
		var ac = new AbortController();
		var before = ac.signal.aborted;
		ac.abort("cancelled");
		before + "|" + ac.signal.aborted + "|" + ac.signal.reason;
	`)
	if err != nil {
		t.Fatalf("AbortController threw: %v", err)
	}
	if v.String() != "false|true|cancelled" {
		t.Errorf("AbortController mismatch: %q", v.String())
	}
}

func TestURLAndURLSearchParams(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)
	v, err := vm.RunString(`
		var u = new URL("https://example.com:8080/path?foo=bar&baz=1#hash");
		var p = u.searchParams;
		u.hostname + "|" + u.port + "|" + u.pathname + "|" + p.get("foo") + "|" + p.has("baz") + "|" + u.hash;
	`)
	if err != nil {
		t.Fatalf("URL/URLSearchParams threw: %v", err)
	}
	want := "example.com|8080|/path|bar|true|#hash"
	if v.String() != want {
		t.Errorf("URL mismatch: got %q want %q", v.String(), want)
	}
}

func TestFetchBridge_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("X-Probe"); got != "yes" {
			t.Errorf("missing custom header: %q", got)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != "hello" {
			t.Errorf("body mismatch: %q", b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true,"echo":"hello"}`))
	}))
	defer srv.Close()

	vm, loop, _ := newTestHost(t, srv.Client())
	if err := vm.Set("__TARGET", srv.URL); err != nil {
		t.Fatalf("Set target: %v", err)
	}

	_, err := vm.RunString(`
		globalThis.__result = null;
		globalThis.__err = null;
		fetch(__TARGET, {method: "POST", headers: {"X-Probe": "yes"}, body: "hello"})
			.then(function (r) { return r.json(); })
			.then(function (j) { globalThis.__result = j; })
			.catch(function (e) { globalThis.__err = String(e); });
	`)
	if err != nil {
		t.Fatalf("fetch setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := func() bool {
		r := vm.Get("__result")
		e := vm.Get("__err")
		return (r != nil && !sobek.IsUndefined(r) && !sobek.IsNull(r)) ||
			(e != nil && !sobek.IsUndefined(e) && !sobek.IsNull(e))
	}
	if err := loop.run(ctx, done); err != nil {
		t.Fatalf("loop.run: %v", err)
	}

	if errV := vm.Get("__err"); errV != nil && !sobek.IsUndefined(errV) && !sobek.IsNull(errV) {
		t.Fatalf("fetch reported error: %s", errV.String())
	}
	okV, _ := vm.RunString(`__result.ok`)
	if okV == nil || !okV.ToBoolean() {
		t.Errorf("expected result.ok=true, got %v", okV)
	}
	echoV, _ := vm.RunString(`__result.echo`)
	if echoV == nil || echoV.String() != "hello" {
		t.Errorf("expected echo='hello', got %v", echoV)
	}
}

func TestCryptoDigest_SHA256(t *testing.T) {
	vm, loop, _ := newTestHost(t, http.DefaultClient)

	payload := []byte("the quick brown fox")
	want := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(want[:])

	if err := vm.Set("__payload", payload); err != nil {
		t.Fatalf("Set payload: %v", err)
	}
	_, err := vm.RunString(`
		globalThis.__hash = null;
		var data = new Uint8Array(__payload.length);
		for (var i = 0; i < __payload.length; i++) data[i] = __payload[i] & 0xff;
		crypto.subtle.digest("SHA-256", data).then(function (buf) {
			var u = new Uint8Array(buf);
			var hex = "";
			for (var i = 0; i < u.length; i++) {
				var h = u[i].toString(16);
				if (h.length < 2) h = "0" + h;
				hex += h;
			}
			globalThis.__hash = hex;
		});
	`)
	if err != nil {
		t.Fatalf("digest setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := func() bool {
		v := vm.Get("__hash")
		return v != nil && !sobek.IsUndefined(v) && !sobek.IsNull(v)
	}
	if err := loop.run(ctx, done); err != nil {
		t.Fatalf("loop.run: %v", err)
	}

	got := vm.Get("__hash")
	if got == nil || got.String() != wantHex {
		t.Errorf("digest mismatch: got %q want %q", got, wantHex)
	}
}

func TestSolver_MissingChallengeJS(t *testing.T) {
	// Any HTML without the awswaf.com hint should bail out cleanly.
	body := `<!doctype html><html><body><p>hello world</p></body></html>`
	_, err := SolveAWSWAF(context.Background(), http.DefaultClient, "https://example.com/", body, nil)
	if !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("expected ErrNoChallenge, got %v", err)
	}
}

func TestSolver_NilClientRejected(t *testing.T) {
	_, err := SolveAWSWAF(context.Background(), nil, "https://example.com/", "<html></html>", nil)
	if err == nil || !strings.Contains(err.Error(), "http.Client") {
		t.Fatalf("expected http.Client guard, got %v", err)
	}
}

func TestSolver_ChallengeScriptFetchError(t *testing.T) {
	// Feed a page that references awswaf.com but the fetch itself will 404.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	// Point the regex-detected URL at the test server by rewriting the host.
	body := `<script src="https://cdn.awswaf.com/challenge.js"></script>`
	// Replace challenge URL host with our httptest server so the fetch fails
	// with a 404, not a real DNS lookup.
	// strings.Replace result intentionally discarded; body is overridden below
	// with an unroutable host so the fetch itself fails fast.
	_ = srv.URL + body
	// The regex requires the *.awswaf.com pattern, so keep a fake reference
	// and do the host swap only for the real fetch via Options.Origin. The
	// cleanest approach: use an Origin override to anchor relative URL, but
	// the regex still runs on the untouched body. So we include BOTH the
	// awswaf.com marker and expect a real fetch failure from httptest.
	body = `<script>window.gokuProps={};</script>` +
		`<script src="https://broken.awswaf.com/challenge.js"></script>`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := SolveAWSWAF(ctx, &http.Client{Timeout: 1 * time.Second}, "https://example.com/", body, nil)
	if err == nil || errors.Is(err, ErrNoChallenge) {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestParseChallengePage_ExtractsURL(t *testing.T) {
	body := `<html><head>
<script>window.gokuProps = { "key": "abc", "iv": "xyz" };</script>
<script src="https://abc123.us-east-1.token.awswaf.com/abc123/challenge.js"></script>
</head></html>`
	info, err := parseChallengePage("https://booking.com/foo", body)
	if err != nil {
		t.Fatalf("parseChallengePage: %v", err)
	}
	if !strings.Contains(info.scriptURL, ".awswaf.com") {
		t.Errorf("scriptURL not captured: %q", info.scriptURL)
	}
	if !strings.Contains(info.gokuProps, `"key": "abc"`) {
		t.Errorf("gokuProps not captured: %q", info.gokuProps)
	}
	if info.origin != "https://booking.com" {
		t.Errorf("origin: got %q", info.origin)
	}
}

func TestBuildCookie_DomainScoping(t *testing.T) {
	c := buildCookie("https://www.booking.com/hotel/abc", "TOKEN123")
	if c.Name != "aws-waf-token" || c.Value != "TOKEN123" {
		t.Errorf("cookie fields: %+v", c)
	}
	if c.Domain != ".booking.com" {
		t.Errorf("expected Domain=.booking.com, got %q", c.Domain)
	}
	if !c.Secure || c.SameSite != http.SameSiteNoneMode {
		t.Errorf("cookie flags: %+v", c)
	}
	if time.Until(c.Expires) <= 0 {
		t.Errorf("cookie expiry not in future: %v", c.Expires)
	}
}

func TestEventLoop_MicrotasksBeforeMacrotasks(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	if _, err := vm.RunString(`globalThis.__order = []`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Schedule a 0-ms timer via the Go API, then Promise.resolve().then() —
	// the microtask must fire first.
	zeroCB, err := vm.RunString(`(function () { globalThis.__order.push("timer"); })`)
	if err != nil {
		t.Fatalf("compile timer cb: %v", err)
	}
	timerFn, ok := sobek.AssertFunction(zeroCB)
	if !ok {
		t.Fatal("timer cb not callable")
	}
	loop.scheduleTimer(timerFn, 0, false)
	loop.scheduleMicrotask(func() error {
		_, _ = vm.RunString(`__order.push("micro")`)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := loop.run(ctx, nil); err != nil {
		t.Fatalf("loop.run: %v", err)
	}

	order, _ := vm.RunString(`__order.join(",")`)
	if got := order.String(); got != "micro,timer" {
		t.Errorf("order mismatch: got %q want %q", got, "micro,timer")
	}
}

func TestEventLoop_TimeoutPropagates(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	// Keep the loop busy with an interval timer that never stops.
	cb, _ := vm.RunString(`(function () {})`)
	fn, _ := sobek.AssertFunction(cb)
	loop.scheduleTimer(fn, 10*time.Millisecond, true)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := loop.run(ctx, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("loop did not exit promptly: %v", elapsed)
	}
}

func TestSolver_AwsWafIntegrationShim(t *testing.T) {
	// Serve a "challenge.js" that just installs AwsWafIntegration.getToken().
	challenge := `
		window.AwsWafIntegration = {
			getToken: function () {
				return new Promise(function (resolve) {
					setTimeout(function () {
						document.cookie = "aws-waf-token=PRETEND_TOKEN; Path=/";
						resolve("PRETEND_TOKEN");
					}, 5);
				});
			}
		};
	`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.Copy(w, bytes.NewBufferString(challenge))
	}))
	defer srv.Close()

	// Build an interstitial HTML that points challenge.js at our test server.
	// We keep the *.awswaf.com marker so parseChallengePage accepts it, but
	// rewrite it to the srv URL via a DNS-style redirect. Simplest: host the
	// server on the right-looking URL by putting .awswaf.com into the source
	// and using a custom http.Transport.
	page := `<html><body>
<script>window.gokuProps={};</script>
<script src="https://fake.awswaf.com/challenge.js"></script>
</body></html>`

	client := &http.Client{
		Transport: &rewriteTransport{to: srv.URL + "/challenge.js"},
		Timeout:   3 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cookie, err := SolveAWSWAF(ctx, client, "https://example.com/", page, &Options{Budget: 3 * time.Second})
	if err != nil {
		t.Fatalf("SolveAWSWAF: %v", err)
	}
	if cookie.Value != "PRETEND_TOKEN" {
		t.Errorf("expected token=PRETEND_TOKEN, got %q", cookie.Value)
	}
	if cookie.Domain != ".example.com" {
		t.Errorf("expected Domain=.example.com, got %q", cookie.Domain)
	}
}

// rewriteTransport redirects any request to a single fixed URL. Used by
// TestSolver_AwsWafIntegrationShim so we don't need DNS for *.awswaf.com.
type rewriteTransport struct {
	to string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	proxied, err := http.NewRequest(req.Method, t.to, req.Body)
	if err != nil {
		return nil, err
	}
	proxied.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(proxied)
}
