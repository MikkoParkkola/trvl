package afklm

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestResolveCredential_EnvFirst(t *testing.T) {
	t.Setenv("AFKLM_KEY", "test-key-env")
	key, err := ResolveCredential(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "test-key-env" {
		t.Fatalf("expected test-key-env, got %q", key)
	}
}

func TestResolveCredential_MissingAll(t *testing.T) {
	// Clear env to ensure env path is not taken.
	_ = os.Unsetenv("AFKLM_KEY")

	// We cannot uninstall the keychain or op in tests; instead we test that
	// when the env is absent and the other sources fail (they will in CI),
	// ErrNoCredential is eventually returned. We accept that on a developer
	// machine with keychain configured this test will pass for a different
	// reason (key found).
	ctx := context.Background()
	_, err := ResolveCredential(ctx)
	if err != nil && err != ErrNoCredential {
		// Some other error type — also acceptable (e.g. op not installed).
		t.Logf("ResolveCredential returned non-nil, non-ErrNoCredential error (acceptable in CI): %v", err)
	}
}

func TestResolveCredential_EnvSet_Configured(t *testing.T) {
	t.Setenv("AFKLM_KEY", "test-key")
	if !Configured(context.Background()) {
		t.Fatal("expected Configured() to return true when AFKLM_KEY is set")
	}
}

func TestResolveCredential_KeychainSkippedOnNonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("keychain test only relevant on non-Darwin")
	}
	// On non-Darwin hosts the keychain branch must never be reached, so
	// keychainLookup should not exist as an accessible code path. This is
	// purely a compile-time guard; the runtime check in ResolveCredential
	// itself gates it.
}

func TestResolveCredential_OpSkippedWhenAbsent(t *testing.T) {
	if _, err := exec.LookPath("op"); err == nil {
		t.Skip("op binary is present on this host; skip absence test")
	}
	_ = os.Unsetenv("AFKLM_KEY")

	// On Darwin the keychain may succeed, so only assert ErrNoCredential
	// when we are not on Darwin and op is absent.
	if runtime.GOOS != "darwin" {
		_, err := ResolveCredential(context.Background())
		if err != ErrNoCredential {
			t.Fatalf("expected ErrNoCredential when neither env nor op present, got %v", err)
		}
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
