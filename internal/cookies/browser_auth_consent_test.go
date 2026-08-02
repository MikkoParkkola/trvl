package cookies

import (
	"errors"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/consent"
)

// OpenBrowserForAuth launches the user's real, visible browser — the one they
// are logged into. A cookie decline is exactly the refusal that covers it, so
// no window may open and no launch command may run.
//
// This is the defect an independent review caught before 1.21.0 shipped: the
// rail providers called this seam on ChallengeNeedsHuman with no consent check
// anywhere between the decline and the `open` syscall.
func TestOpenBrowserForAuthRefusesWhenCookiesDeclined(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "1")

	launched := 0
	origStart := browserAuthStart
	browserAuthStart = func(cmd string, args ...string) error {
		launched++
		return nil
	}
	t.Cleanup(func() { browserAuthStart = origStart })

	err := OpenBrowserForAuth("https://declined.example.com/auth")
	if !errors.Is(err, ErrBrowserAuthDeclined) {
		t.Fatalf("want ErrBrowserAuthDeclined, got %v", err)
	}
	if launched != 0 {
		t.Fatalf("browser was launched %d time(s) after the user declined browser access", launched)
	}
}

// The decline must not be confused with the OTHER decline. Tier2 governs the
// empty-profile headless browser; it says nothing about the user's own browser,
// so it must not suppress this window on its own.
func TestOpenBrowserForAuthStillOpensWhenOnlyTier2Declined(t *testing.T) {
	t.Setenv(consent.CookiesEnv, "")
	t.Setenv(consent.Tier2Env, "1")

	launched := 0
	origStart := browserAuthStart
	browserAuthStart = func(cmd string, args ...string) error {
		launched++
		return nil
	}
	t.Cleanup(func() { browserAuthStart = origStart })

	if err := OpenBrowserForAuth("https://tier2-only.example.com/auth"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if launched != 1 {
		t.Fatalf("want 1 launch, got %d — a Tier2 decline must not gate the user's own browser", launched)
	}
}
