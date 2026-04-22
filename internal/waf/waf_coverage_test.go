package waf

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/sobek"
)

// ==========================================================================
// driveGetToken — exercising the nil/undefined integration path and the
// getToken-not-a-function path.
// ==========================================================================

func TestDriveGetToken_NoIntegration(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, http.DefaultClient, "https://coverage.test", "test-ua")
	if err := host.install(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// window.AwsWafIntegration is not set — should return empty token, no error.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	token, err := driveGetToken(ctx, vm, loop)
	if err != nil {
		t.Fatalf("driveGetToken with no integration: %v", err)
	}
	if token != "" {
		t.Errorf("expected empty token, got %q", token)
	}
}

func TestDriveGetToken_IntegrationNotAFunction(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, http.DefaultClient, "https://coverage.test", "test-ua")
	if err := host.install(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Set AwsWafIntegration with getToken as a string, not a function.
	if _, err := vm.RunString(`window.AwsWafIntegration = { getToken: "not-a-function" };`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	token, err := driveGetToken(ctx, vm, loop)
	if err != nil {
		t.Fatalf("driveGetToken with non-function getToken: %v", err)
	}
	if token != "" {
		t.Errorf("expected empty token, got %q", token)
	}
}

func TestDriveGetToken_ReturnsPlainString(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, http.DefaultClient, "https://coverage.test", "test-ua")
	if err := host.install(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// getToken returns a plain string, not a Promise.
	if _, err := vm.RunString(`
		window.AwsWafIntegration = {
			getToken: function() { return "PLAIN_TOKEN"; }
		};
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	token, err := driveGetToken(ctx, vm, loop)
	if err != nil {
		t.Fatalf("driveGetToken plain string: %v", err)
	}
	if token != "PLAIN_TOKEN" {
		t.Errorf("expected PLAIN_TOKEN, got %q", token)
	}
}

func TestDriveGetToken_PromiseRejected(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, http.DefaultClient, "https://coverage.test", "test-ua")
	if err := host.install(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := vm.RunString(`
		window.AwsWafIntegration = {
			getToken: function() {
				return Promise.reject("challenge failed");
			}
		};
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := driveGetToken(ctx, vm, loop)
	if err == nil {
		t.Fatal("expected error from rejected promise")
	}
	if !errors.Is(err, ErrChallengeFail) {
		t.Errorf("expected ErrChallengeFail, got %v", err)
	}
}

func TestDriveGetToken_GetTokenThrows(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)
	host := newVMHost(vm, loop, http.DefaultClient, "https://coverage.test", "test-ua")
	if err := host.install(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := vm.RunString(`
		window.AwsWafIntegration = {
			getToken: function() { throw new Error("boom"); }
		};
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := driveGetToken(ctx, vm, loop)
	if err == nil {
		t.Fatal("expected error when getToken throws")
	}
	if !errors.Is(err, ErrChallengeFail) {
		t.Errorf("expected ErrChallengeFail, got %v", err)
	}
}

// ==========================================================================
// fetchChallengeScript — exercising non-2xx status and origin referer.
// ==========================================================================

func TestFetchChallengeScript_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := fetchChallengeScript(ctx, srv.Client(), srv.URL+"/challenge.js", "https://coverage.test", "test-ua")
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected 503 in error, got %v", err)
	}
}

func TestFetchChallengeScript_SetsReferer(t *testing.T) {
	var gotReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("// challenge script"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	script, err := fetchChallengeScript(ctx, srv.Client(), srv.URL+"/challenge.js", "https://coverage-origin.test", "test-ua")
	if err != nil {
		t.Fatalf("fetchChallengeScript: %v", err)
	}
	if script != "// challenge script" {
		t.Errorf("script = %q", script)
	}
	if gotReferer != "https://coverage-origin.test/" {
		t.Errorf("Referer = %q, want https://coverage-origin.test/", gotReferer)
	}
}

func TestFetchChallengeScript_EmptyOrigin(t *testing.T) {
	var gotReferer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("// script"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := fetchChallengeScript(ctx, srv.Client(), srv.URL+"/challenge.js", "", "test-ua")
	if err != nil {
		t.Fatalf("fetchChallengeScript: %v", err)
	}
	if gotReferer != "" {
		t.Errorf("Referer should be empty for empty origin, got %q", gotReferer)
	}
}

// ==========================================================================
// SolveAWSWAF — exercising gokuProps injection and various option paths.
// ==========================================================================

func TestSolveAWSWAF_GokuPropsInjected(t *testing.T) {
	// Serve a challenge.js that reads window.gokuProps and returns it as token.
	challenge := `
		window.AwsWafIntegration = {
			getToken: function() {
				var props = window.gokuProps || {};
				return props.key || "no-key";
			}
		};
	`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.Copy(w, strings.NewReader(challenge))
	}))
	defer srv.Close()

	page := `<html>
<script>window.gokuProps = { "key": "test-token-value" };</script>
<script src="https://fake.awswaf.com/challenge.js"></script>
</html>`

	client := &http.Client{
		Transport: &rewriteTransport{to: srv.URL + "/challenge.js"},
		Timeout:   3 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cookie, err := SolveAWSWAF(ctx, client, "https://coverage.test/page", page, &Options{
		Budget:    3 * time.Second,
		UserAgent: "custom-ua/1.0",
		Origin:    "https://coverage-origin.test",
	})
	if err != nil {
		t.Fatalf("SolveAWSWAF: %v", err)
	}
	if cookie.Value != "test-token-value" {
		t.Errorf("token = %q, want test-token-value", cookie.Value)
	}
}

func TestSolveAWSWAF_FallsBackToCookieJar(t *testing.T) {
	// Challenge.js does not install AwsWafIntegration but sets document.cookie.
	challenge := `
		document.cookie = "aws-waf-token=JAR_TOKEN; Path=/";
	`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.Copy(w, strings.NewReader(challenge))
	}))
	defer srv.Close()

	page := `<script src="https://fake.awswaf.com/challenge.js"></script>`

	client := &http.Client{
		Transport: &rewriteTransport{to: srv.URL + "/challenge.js"},
		Timeout:   3 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cookie, err := SolveAWSWAF(ctx, client, "https://coverage.test/page", page, nil)
	if err != nil {
		t.Fatalf("SolveAWSWAF: %v", err)
	}
	if cookie.Value != "JAR_TOKEN" {
		t.Errorf("token = %q, want JAR_TOKEN", cookie.Value)
	}
}

func TestSolveAWSWAF_NoTokenReturnsError(t *testing.T) {
	// Challenge.js installs nothing and sets no cookies.
	challenge := `// empty challenge`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.Copy(w, strings.NewReader(challenge))
	}))
	defer srv.Close()

	page := `<script src="https://fake.awswaf.com/challenge.js"></script>`
	client := &http.Client{
		Transport: &rewriteTransport{to: srv.URL + "/challenge.js"},
		Timeout:   3 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := SolveAWSWAF(ctx, client, "https://coverage.test/page", page, nil)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}
}

// ==========================================================================
// eventLoop.run — exercising the context-cancelled path while waiting.
// ==========================================================================

func TestEventLoop_RunContextCancelledWhileWaiting(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)

	// Keep pending > 0 so the loop blocks waiting for inbound.
	loop.trackPending(+1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := loop.run(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestEventLoop_RunDoneBeforeTimers(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)

	// done() returns true immediately — should exit without processing timers.
	cb, _ := vm.RunString(`(function() {})`)
	fn, _ := sobek.AssertFunction(cb)
	loop.scheduleTimer(fn, 0, false)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := loop.run(ctx, func() bool { return true })
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEventLoop_RunMicrotaskDoneCheck(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)

	if _, err := vm.RunString(`globalThis.__mtDone = false`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Schedule two microtasks; after the first, done() returns true.
	loop.scheduleMicrotask(func() error {
		_, _ = vm.RunString(`globalThis.__mtDone = true`)
		return nil
	})
	loop.scheduleMicrotask(func() error {
		// This should NOT run because done() returns true after the first.
		_, _ = vm.RunString(`globalThis.__mtDone = false`)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := loop.run(ctx, func() bool {
		v := vm.Get("__mtDone")
		return v != nil && v.ToBoolean()
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	v := vm.Get("__mtDone")
	if v == nil || !v.ToBoolean() {
		t.Error("expected __mtDone=true (loop should have stopped after first microtask)")
	}
}

// ==========================================================================
// jsFetch — exercising the concurrency cap and body type branches.
// ==========================================================================

func TestJsFetch_ConcurrencyCapExceeded(t *testing.T) {
	vm, loop, host := newTestHost(t, http.DefaultClient)
	// Set fetchCap to 0 so any fetch is immediately over the cap.
	host.fetchCap = 0

	_, err := vm.RunString(`
		globalThis.__fetchErr = null;
		fetch("https://coverage.test/api", {}).catch(function(e) {
			globalThis.__fetchErr = String(e);
		});
	`)
	if err != nil {
		t.Fatalf("fetch setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	done := func() bool {
		v := vm.Get("__fetchErr")
		return v != nil && !sobek.IsUndefined(v) && !sobek.IsNull(v)
	}
	_ = loop.run(ctx, done)

	v := vm.Get("__fetchErr")
	if v == nil || !strings.Contains(v.String(), "concurrency cap") {
		t.Errorf("expected concurrency cap error, got %v", v)
	}
}

func TestJsFetch_ByteSliceBody(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	vm, loop, _ := newTestHost(t, srv.Client())
	vm.Set("__TARGET", srv.URL)

	_, err := vm.RunString(`
		globalThis.__fetchDone = false;
		fetch(__TARGET + "/post", {method: "POST", body: "binary-payload"})
			.then(function() { globalThis.__fetchDone = true; });
	`)
	if err != nil {
		t.Fatalf("fetch setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := func() bool {
		v := vm.Get("__fetchDone")
		return v != nil && v.ToBoolean()
	}
	if err := loop.run(ctx, done); err != nil {
		t.Fatalf("loop.run: %v", err)
	}

	if gotBody != "binary-payload" {
		t.Errorf("body = %q, want binary-payload", gotBody)
	}
}

// ==========================================================================
// coerceString — exercising the "null" string and numeric value paths.
// ==========================================================================

func TestCoerceString_NullString(t *testing.T) {
	vm := sobek.New()
	// A value whose .String() returns "null" (not the sobek.Null() sentinel).
	v, _ := vm.RunString(`null`)
	got := coerceString(v)
	if got != "" {
		t.Errorf("expected empty for null value, got %q", got)
	}
}

func TestCoerceString_NumberValue(t *testing.T) {
	vm := sobek.New()
	v := vm.ToValue(42)
	got := coerceString(v)
	if got != "42" {
		t.Errorf("expected '42', got %q", got)
	}
}

func TestCoerceString_ObjectTokenFieldNonString(t *testing.T) {
	vm := sobek.New()
	// Object has "token" field but it is a number, not a string.
	obj, _ := vm.RunString(`({"token": 12345})`)
	got := coerceString(obj)
	// The map["token"].(string) assertion fails, falls through to v.String().
	// The object toString gives "[object Object]" which triggers empty return.
	if got != "" {
		t.Errorf("expected empty for token=number, got %q", got)
	}
}

// ==========================================================================
// digest — exercising the unsupported algorithm panic path.
// ==========================================================================

func TestDigest_UnsupportedAlgorithm(t *testing.T) {
	vm, loop, _ := newTestHost(t, http.DefaultClient)

	_, err := vm.RunString(`
		globalThis.__digestErr = null;
		var data = new TextEncoder().encode("test");
		crypto.subtle.digest("SHA-1", data)
			.then(function() { globalThis.__digestErr = "should-not-resolve"; })
			.catch(function(e) { globalThis.__digestErr = String(e); });
	`)
	if err != nil {
		t.Fatalf("digest setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := func() bool {
		v := vm.Get("__digestErr")
		return v != nil && !sobek.IsUndefined(v) && !sobek.IsNull(v)
	}
	_ = loop.run(ctx, done)

	v := vm.Get("__digestErr")
	if v == nil || !strings.Contains(v.String(), "unsupported") {
		t.Errorf("expected unsupported digest error, got %v", v)
	}
}

// ==========================================================================
// jsSetTimeout — exercising the setInterval (repeat=true) path.
// ==========================================================================

func TestJsSetInterval_FiresMultipleTimes(t *testing.T) {
	vm, loop, _ := newTestHost(t, http.DefaultClient)

	_, err := vm.RunString(`
		globalThis.__intervalCount = 0;
		var id = setInterval(function() {
			globalThis.__intervalCount++;
			if (globalThis.__intervalCount >= 3) clearInterval(id);
		}, 5);
	`)
	if err != nil {
		t.Fatalf("setInterval setup: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := func() bool {
		v := vm.Get("__intervalCount")
		return v != nil && v.ToInteger() >= 3
	}
	if err := loop.run(ctx, done); err != nil {
		t.Fatalf("loop.run: %v", err)
	}

	v := vm.Get("__intervalCount")
	if v == nil || v.ToInteger() < 3 {
		t.Errorf("expected >= 3 interval fires, got %v", v)
	}
}

// ==========================================================================
// cryptoRandom — exercising the no-argument panic path.
// ==========================================================================

func TestCryptoRandom_NoArgsPanics(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)

	_, err := vm.RunString(`
		try {
			crypto.getRandomValues();
		} catch(e) {
			globalThis.__cryptoErr = String(e);
		}
	`)
	if err != nil {
		t.Fatalf("cryptoRandom no args: %v", err)
	}
	v := vm.Get("__cryptoErr")
	// Accept both error strings: the custom "typed array" message (raised
	// when our stub receives a defined argument) AND the built-in TypeError
	// that current Sobek versions raise for missing arguments before our
	// stub runs. Either proves the no-args path panics.
	if v == nil {
		t.Fatal("expected an error message from getRandomValues(), got nil")
	}
	msg := v.String()
	if !strings.Contains(msg, "typed array") && !strings.Contains(msg, "TypeError") {
		t.Errorf("expected typed-array or TypeError, got %q", msg)
	}
}

func TestCryptoRandom_ObjectWithoutLength(t *testing.T) {
	vm, _, _ := newTestHost(t, http.DefaultClient)

	_, err := vm.RunString(`
		try {
			crypto.getRandomValues({});
		} catch(e) {
			globalThis.__cryptoErr2 = String(e);
		}
	`)
	if err != nil {
		t.Fatalf("cryptoRandom no length: %v", err)
	}
	v := vm.Get("__cryptoErr2")
	if v == nil || !strings.Contains(v.String(), "not an array") {
		t.Errorf("expected not-an-array error, got %v", v)
	}
}

// ==========================================================================
// parseChallengePage — custom host path for challenge.js detection.
// ==========================================================================

func TestParseChallengePage_CustomHostPath(t *testing.T) {
	// Tests the non-awswaf.com pattern: custom host paths like
	// w.booking.com/__challenge_*/challenge.js
	body := `<script src="https://w.booking.com/__challenge_abc/challenge.js"></script>`
	info, err := parseChallengePage("https://booking.com/hotel", body)
	if err != nil {
		t.Fatalf("parseChallengePage: %v", err)
	}
	if !strings.Contains(info.scriptURL, "challenge.js") {
		t.Errorf("scriptURL = %q, expected challenge.js reference", info.scriptURL)
	}
	if info.origin != "https://booking.com" {
		t.Errorf("origin = %q", info.origin)
	}
}

func TestParseChallengePage_InvalidPageURL(t *testing.T) {
	body := `<script src="https://cdn.awswaf.com/challenge.js"></script>`
	info, err := parseChallengePage("://invalid-url", body)
	if err != nil {
		t.Fatalf("parseChallengePage: %v", err)
	}
	// Origin should be empty for unparseable URL.
	if info.origin != "" {
		t.Errorf("origin = %q, want empty", info.origin)
	}
}

// ==========================================================================
// fireDueTimers — exercising interval re-arm after firing.
// ==========================================================================

func TestFireDueTimers_IntervalRearms(t *testing.T) {
	vm := sobek.New()
	loop := newEventLoop(vm)

	fired := 0
	cb, _ := vm.RunString(`(function() {})`)
	fn, _ := sobek.AssertFunction(cb)

	// Schedule a repeating timer with a tiny non-zero delay. A 0-ms interval
	// is a busy-loop and fireDueTimers intentionally does NOT re-arm it (see
	// eventloop.go: `top.interval > 0`), so we use 1ms to exercise the
	// genuine re-arm path.
	loop.scheduleTimer(fn, 1*time.Millisecond, true)

	// Wait past the deadline so fireDueTimers has something to pop.
	time.Sleep(2 * time.Millisecond)

	// Fire once — the timer should re-arm.
	loop.fireDueTimers(func(err error) {
		fired++
	})
	if fired != 1 {
		t.Errorf("fired = %d, want 1", fired)
	}

	// The timer should still be in the heap (re-armed for next tick).
	if len(loop.timers) != 1 {
		t.Errorf("timers in heap = %d, want 1 (re-armed)", len(loop.timers))
	}
}
