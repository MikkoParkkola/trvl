//go:build unix

package afklm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeBin installs an executable named `name` on PATH whose body is `body`. It
// returns the marker path the script appends to, so a test can assert whether
// the helper was invoked at all, and how many times.
func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	marker := filepath.Join(dir, name+".invoked")
	script := "#!/bin/sh\necho invoked >> \"" + marker + "\"\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	return marker
}

// invocations counts how many times a fake helper ran.
func invocations(t *testing.T, marker string) int {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(data)))
}

// testRef gives each test its own 1Password reference.
//
// The reference is the cache and singleflight key, and a lookup detached from
// its caller (see resolveExternal) keeps running after the test that started it
// returns. A hung helper from one test would otherwise publish its failure into
// a later test's cache under a shared key. Distinct refs keep them apart, and
// the need for them is itself a property worth knowing about.
func testRef(t *testing.T) string {
	t.Helper()
	return "op://Private/" + t.Name() + "/credential"
}

// stubExternalHelpers puts fake `op` and `security` binaries first on PATH so
// any external credential lookup is observable and cannot reach the real ones.
func stubExternalHelpers(t *testing.T, opBody, securityBody string) (opMarker, secMarker string) {
	t.Helper()
	dir := t.TempDir()
	opMarker = fakeBin(t, dir, "op", opBody)
	secMarker = fakeBin(t, dir, "security", securityBody)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetExternalCache()
	return opMarker, secMarker
}

func TestResolveCredential_EnvFirst(t *testing.T) {
	for _, policy := range []Policy{PolicyEnvOnly, PolicyExternal} {
		opMarker, secMarker := stubExternalHelpers(t, "exit 1", "exit 1")
		t.Setenv(EnvKey, "test-key-env")

		key, err := ResolveCredential(context.Background(), policy)
		if err != nil {
			t.Fatalf("policy %v: unexpected error: %v", policy, err)
		}
		if key != "test-key-env" {
			t.Fatalf("policy %v: expected test-key-env, got %q", policy, key)
		}
		// The env var short-circuits everything: no helper may run even under
		// PolicyExternal, because paying for a subprocess we do not need is the
		// cost this split exists to avoid.
		if invocations(t, opMarker) > 0 || invocations(t, secMarker) > 0 {
			t.Fatalf("policy %v: an external helper ran even though %s was set", policy, EnvKey)
		}
	}
}

// TestResolveCredential_EnvOnlySpawnsNothing is the regression test for #507.
// The default round-trip merge resolves under PolicyEnvOnly on every search; if
// that path can reach `op` or `security` it can stall the search, surface an
// interactive credential prompt in the host terminal, and leave helper
// processes behind. These fakes hang for 30s, so a pre-fix build both invokes
// them and blows the deadline.
func TestResolveCredential_EnvOnlySpawnsNothing(t *testing.T) {
	opMarker, secMarker := stubExternalHelpers(t, "sleep 30", "sleep 30")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	start := time.Now()
	_, err := ResolveCredential(context.Background(), PolicyEnvOnly)
	elapsed := time.Since(start)

	if err != ErrNoCredential {
		t.Fatalf("expected ErrNoCredential, got %v", err)
	}
	if invocations(t, opMarker) > 0 {
		t.Fatal("PolicyEnvOnly invoked the 1Password CLI; the default search path must never spawn a credential helper")
	}
	if invocations(t, secMarker) > 0 {
		t.Fatal("PolicyEnvOnly invoked the Keychain helper; the default search path must never spawn a credential helper")
	}
	if elapsed > time.Second {
		t.Fatalf("PolicyEnvOnly took %v; it must be effectively free", elapsed)
	}
}

// TestResolveCredential_ExternalRequiresOpRef proves the hardcoded,
// maintainer-specific `op://Personal/...` reference is gone: with no
// AFKLM_OP_REF set, 1Password is never consulted even under PolicyExternal and
// even though `op` is on PATH.
func TestResolveCredential_ExternalRequiresOpRef(t *testing.T) {
	opMarker, _ := stubExternalHelpers(t, "echo should-not-be-used", "exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, "")

	_, err := ResolveCredential(context.Background(), PolicyExternal)
	if err != ErrNoCredential {
		t.Fatalf("expected ErrNoCredential, got %v", err)
	}
	if invocations(t, opMarker) > 0 {
		t.Fatalf("op was invoked without %s set; trvl must not guess where a user keeps secrets", EnvOpRef)
	}
}

func TestResolveCredential_ExternalUsesConfiguredRef(t *testing.T) {
	ref := testRef(t)
	// The fake asserts it received the configured reference verbatim.
	_, _ = stubExternalHelpers(t,
		`if [ "$2" = "`+ref+`" ]; then echo key-from-op; exit 0; fi
echo "wrong ref: $*" >&2
exit 1`,
		"exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, ref)

	key, err := ResolveCredential(context.Background(), PolicyExternal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "key-from-op" {
		t.Fatalf("expected key-from-op, got %q", key)
	}
}

// TestResolveCredential_ExternalIsBounded proves a hung helper can no longer
// stall a caller indefinitely — the second half of #507, where ~20 stalled
// `op read` processes accumulated behind an unbounded lookup.
//
// It must report ErrHelperTimedOut, not ErrNoCredential: a hung helper and an
// absent credential need different fixes from the user, so telling them to go
// set a variable when `op` is wedged sends them at the wrong problem.
func TestResolveCredential_ExternalIsBounded(t *testing.T) {
	_, _ = stubExternalHelpers(t, "sleep 30", "exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	start := time.Now()
	_, err := ResolveCredential(context.Background(), PolicyExternal)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrHelperTimedOut) {
		t.Fatalf("expected ErrHelperTimedOut, got %v", err)
	}
	if elapsed > externalLookupTimeout+3*time.Second {
		t.Fatalf("external lookup took %v; expected it bounded near %v", elapsed, externalLookupTimeout)
	}
}

// TestResolveCredential_TimeoutIsCached pins a deliberate decision. A timed-out
// helper IS negative-cached, unlike a cancelled caller. #507 was helpers
// accumulating because every search re-paid a lookup that never returned;
// leaving timeouts uncached would restore that at two seconds per search.
//
// The cached answer must stay ErrHelperTimedOut. Caching only an expiry would
// report the timeout correctly once and then call it "not configured" for the
// rest of the TTL, which is the misdiagnosis the distinct error exists to stop.
func TestResolveCredential_TimeoutIsCached(t *testing.T) {
	opMarker, _ := stubExternalHelpers(t, "sleep 30", "exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	for i := range 2 {
		_, err := ResolveCredential(context.Background(), PolicyExternal)
		if !errors.Is(err, ErrHelperTimedOut) {
			t.Fatalf("call %d: expected ErrHelperTimedOut, got %v", i, err)
		}
	}

	if got := invocations(t, opMarker); got != 1 {
		t.Fatalf("op invoked %d times across 2 resolves; a timeout must be cached like any other lookup failure", got)
	}
}

// TestResolveCredential_KeychainTimeoutIsNotSwallowed covers the macOS path. A
// Keychain that hangs is not a Keychain that said "no": falling through to
// 1Password would spend a second deadline and then report "not configured", so
// the user waits twice as long to be told to fix the wrong thing.
func TestResolveCredential_KeychainTimeoutIsNotSwallowed(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the Keychain backend is Darwin-only")
	}
	opMarker, _ := stubExternalHelpers(t, "echo key-from-op", "sleep 30")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	start := time.Now()
	_, err := ResolveCredential(context.Background(), PolicyExternal)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrHelperTimedOut) {
		t.Fatalf("a hung Keychain must surface as a timeout, got %v", err)
	}
	if invocations(t, opMarker) > 0 {
		t.Fatal("1Password was consulted after the Keychain hung; that spends a second deadline for an answer the user cannot use")
	}
	if elapsed > externalLookupTimeout+3*time.Second {
		t.Fatalf("took %v; one hung backend must not cost more than one deadline", elapsed)
	}
}

// TestResolveCredential_NegativeCacheSuppressesRespawn covers the MCP shape: a
// long-lived server serving many searches must not re-pay a failed lookup on
// every one of them.
func TestResolveCredential_NegativeCacheSuppressesRespawn(t *testing.T) {
	opMarker, _ := stubExternalHelpers(t, "exit 1", "exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	for i := range 3 {
		if _, err := ResolveCredential(context.Background(), PolicyExternal); err != ErrNoCredential {
			t.Fatalf("call %d: expected ErrNoCredential, got %v", i, err)
		}
	}

	if got := invocations(t, opMarker); got != 1 {
		t.Fatalf("op invoked %d times across 3 resolves; the negative cache should permit exactly 1", got)
	}
}

// TestResolveCredential_EnvBeatsNegativeCache proves a credential exported
// mid-session takes effect immediately: the cache must not hide it, which is
// why it carries a TTL instead of being a permanent sync.Once.
func TestResolveCredential_EnvBeatsNegativeCache(t *testing.T) {
	_, _ = stubExternalHelpers(t, "exit 1", "exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	if _, err := ResolveCredential(context.Background(), PolicyExternal); err != ErrNoCredential {
		t.Fatalf("expected ErrNoCredential to prime the cache, got %v", err)
	}

	t.Setenv(EnvKey, "late-key")
	key, err := ResolveCredential(context.Background(), PolicyExternal)
	if err != nil {
		t.Fatalf("unexpected error after setting %s: %v", EnvKey, err)
	}
	if key != "late-key" {
		t.Fatalf("expected late-key, got %q", key)
	}
}

func TestConfigured(t *testing.T) {
	_, _ = stubExternalHelpers(t, "exit 1", "exit 1")

	t.Setenv(EnvKey, "test-key")
	if !Configured(context.Background(), PolicyEnvOnly) {
		t.Fatalf("expected Configured to be true when %s is set", EnvKey)
	}

	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, "")
	resetExternalCache()
	if Configured(context.Background(), PolicyEnvOnly) {
		t.Fatal("expected Configured to be false with no credential configured")
	}
}

func TestCredentialCommand_IsIsolatedAndBounded(t *testing.T) {
	cmd, _, cancel := credentialCommand(context.Background(), "true")
	defer cancel()

	if cmd.SysProcAttr == nil {
		t.Fatal("credential helpers must run with SysProcAttr set so they cannot reach /dev/tty")
	}
	if cmd.WaitDelay == 0 {
		t.Fatal("credential helpers must set WaitDelay so a helper ignoring its kill signal cannot pin the caller")
	}
	if cmd.Cancel == nil {
		t.Fatal("credential helpers must set Cancel so the process group is signalled, not just the direct child")
	}
}

// TestResolveCredential_CancelledCallerDoesNotPoisonCache guards the failure
// mode that matters most on a long-lived MCP server: one request going away
// must not disable credential resolution for everyone else.
//
// Before the lookup was detached from the caller's context, whichever request
// happened to lead the shared flight owned it. A leader that was already
// cancelled failed the lookup and published that failure to the negative cache,
// so a perfectly valid credential stayed invisible for the whole TTL.
func TestResolveCredential_CancelledCallerDoesNotPoisonCache(t *testing.T) {
	// The helper would succeed if it were ever allowed to run.
	_, _ = stubExternalHelpers(t, "echo key-from-op", "exit 1")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ResolveCredential(ctx, PolicyExternal); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled caller should surface context.Canceled, not a setup error; got %v", err)
	}

	// A fresh caller must still be able to resolve.
	key, err := ResolveCredential(context.Background(), PolicyExternal)
	if err != nil {
		t.Fatalf("cancelled caller poisoned the cache: subsequent resolve failed with %v", err)
	}
	if key != "key-from-op" {
		t.Fatalf("expected key-from-op, got %q", key)
	}
}

// TestResolveCredential_ExternalUsesKeychainFirst covers the Keychain success
// path, which the other tests never exercise because their fake `security`
// always fails. It also pins the ordering: a Keychain hit must win before
// 1Password is consulted, so a user with both configured pays one subprocess.
func TestResolveCredential_ExternalUsesKeychainFirst(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the Keychain backend is Darwin-only")
	}
	opMarker, _ := stubExternalHelpers(t, "echo key-from-op", "echo key-from-keychain")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	key, err := ResolveCredential(context.Background(), PolicyExternal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "key-from-keychain" {
		t.Fatalf("expected the Keychain value to win, got %q", key)
	}
	if invocations(t, opMarker) > 0 {
		t.Fatal("1Password was consulted even though the Keychain answered; the chain must stop at the first hit")
	}
}

// TestResolveCredential_ExternalSkipsOpWhenAbsent covers the branch where a
// reference is configured but the `op` binary is not installed. Attempting to
// exec a missing binary would surface as an opaque failure rather than a clean
// "not configured".
func TestResolveCredential_ExternalSkipsOpWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	// A PATH containing neither `op` nor `security`.
	t.Setenv("PATH", dir)
	resetExternalCache()
	t.Setenv(EnvKey, "")
	t.Setenv(EnvOpRef, testRef(t))

	if _, err := ResolveCredential(context.Background(), PolicyExternal); err != ErrNoCredential {
		t.Fatalf("expected ErrNoCredential when op is not installed, got %v", err)
	}
}

// TestResolveCredential_CorrectedRefBypassesCache pins that the negative cache
// is keyed on the configuration that earned it. A user who fixes a wrong
// AFKLM_OP_REF must see the correction immediately: suppressing their retry for
// up to a minute, while the error text tells them to go set a variable, is a fix
// that appears not to work.
func TestResolveCredential_CorrectedRefBypassesCache(t *testing.T) {
	good := testRef(t)
	_, _ = stubExternalHelpers(t,
		`if [ "$2" = "`+good+`" ]; then echo key-from-op; exit 0; fi
exit 1`,
		"exit 1")
	t.Setenv(EnvKey, "")

	// A wrong reference fails and primes the cache for that reference.
	t.Setenv(EnvOpRef, testRef(t)+"-typo")
	if _, err := ResolveCredential(context.Background(), PolicyExternal); err == nil {
		t.Fatal("expected the wrong reference to fail")
	}

	// Correcting it must take effect at once, not after the TTL.
	t.Setenv(EnvOpRef, good)
	key, err := ResolveCredential(context.Background(), PolicyExternal)
	if err != nil {
		t.Fatalf("a corrected reference was suppressed by the previous failure: %v", err)
	}
	if key != "key-from-op" {
		t.Fatalf("expected key-from-op, got %q", key)
	}
}

func TestErrNoCredential_Sentinel(t *testing.T) {
	if ErrNoCredential == nil {
		t.Fatal("ErrNoCredential must not be nil")
	}
	if ErrNoCredential.Error() == "" {
		t.Fatal("ErrNoCredential.Error() must not be empty")
	}
}
